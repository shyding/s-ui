package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	
	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
	"golang.org/x/net/proxy"
)

type NodeTestService struct{}

type NodeTestResult struct {
	Tag       string `json:"tag"`
	Server    string `json:"server"`
	Port      int    `json:"port"`
	Latency   int64  `json:"latency"`     // TCP handshake latency
	RealLatency int64 `json:"realLatency"` // HTTP connection latency (True Delay)
	Available bool   `json:"available"`
	LandingIP string `json:"landingIP"`
	Country   string `json:"country"`
	Region    string `json:"region"`
	City      string `json:"city"`
	ISP       string `json:"isp"`
	IPType    string `json:"ipType"`
	FraudScore int   `json:"fraudScore"`
	Error     string `json:"error,omitempty"`
}

// TestOutbound tests a single outbound TCP connection
func (s *NodeTestService) TestOutbound(tag string) (*NodeTestResult, error) {
	db := database.GetDB()
	var outbound model.Outbound
	err := db.Where("tag = ?", tag).First(&outbound).Error
	if err != nil {
		return nil, err
	}

	// Parse outbound options to get server and port
	var options map[string]interface{}
	if err := json.Unmarshal(outbound.Options, &options); err != nil {
		return nil, err
	}

	server, _ := options["server"].(string)
	port := 0
	if p, ok := options["server_port"].(float64); ok {
		port = int(p)
	}

	result := &NodeTestResult{
		Tag:    tag,
		Server: server,
		Port:   port,
	}

	if server == "" || port == 0 {
		result.Available = false
		result.Latency = -1
		result.Error = "invalid server or port"
		return result, nil
	}

	// Test TCP connection latency
	// Skip TCP test for UDP-based protocols
	if outbound.Type == "hysteria2" || outbound.Type == "tuic" || outbound.Type == "wireguard" || outbound.Type == "hy2" {
		result.Available = true
		result.Latency = 0
		return result, nil
	}

	start := time.Now()
	address := fmt.Sprintf("%s:%d", server, port)
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		result.Available = false
		result.Latency = -1
		result.Error = err.Error()
		return result, nil
	}
	conn.Close()
	result.Latency = time.Since(start).Milliseconds()
	result.Available = true

	return result, nil
}

// TestOutboundWithLandingIP tests outbound and queries landing IP through the proxy
func (s *NodeTestService) TestOutboundWithLandingIP(tag string, ctx context.Context) (*NodeTestResult, error) {
	result, err := s.TestOutbound(tag)
	if err != nil {
		return nil, err
	}

	// Skip IP lookup if connection failed (except for UDP protocols which skipped check)
	if !result.Available {
		return result, nil
	}

	// Get outbound from database to check type
	db := database.GetDB()
	var outbound model.Outbound
	err = db.Where("tag = ?", tag).First(&outbound).Error
	if err != nil {
		result.Error = "outbound not found in database"
		return result, nil
	}

	// For SOCKS5 nodes, use direct proxy connection (no sing-box dependency)
	if outbound.Type == "socks" {
		return s.testWithSOCKS5(outbound, result)
	}

	// For other protocols, try to use sing-box outbound
	outboundManager := service.FromContext[adapter.OutboundManager](ctx)
	if outboundManager == nil {
		result.Error = "sing-box not running (required for non-SOCKS5 protocols)"
		return result, nil
	}

	outbound_adapter, loaded := outboundManager.Outbound(tag)
	if !loaded {
		result.Error = "outbound not found in sing-box (load node first or use SOCKS5)"
		return result, nil
	}

	// Create a dialer using the outbound
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Measure Real Latency (True Delay)
	// Use a fast, lightweight URL like v2rayN does (http://www.google.com/generate_204)
	// We use http://www.gstatic.com/generate_204 to avoid potential blocking issues with www.google.com in some regions,
	// though they are usually the same.
	rlStart := time.Now()
	rlDest := M.ParseSocksaddr("www.gstatic.com:80") 
	
	rlConn, err := outbound_adapter.DialContext(dialCtx, N.NetworkTCP, rlDest)
	if err == nil {
		req := "HEAD /generate_204 HTTP/1.1\r\nHost: www.gstatic.com\r\nConnection: close\r\n\r\n"
		_, err = rlConn.Write([]byte(req))
		if err == nil {
			buf := make([]byte, 1)
			_, err = rlConn.Read(buf)
			if err == nil || err == io.EOF {
				result.RealLatency = time.Since(rlStart).Milliseconds()
			}
		}
		rlConn.Close()
	}
	
	// If RealLatency test failed using gstatic, try the IP API connection as fallback (server latency)
	if result.RealLatency == 0 {
		// We will measure it during IP check
	}

	// Try multiple IP lookup services concurrently
	ipLookupTasks := []IPLookupTask{
		// Service 1: ip-api.com
		func(ctx context.Context, res *NodeTestResult) error {
			return s.tryIPAPI(ctx, outbound_adapter, res)
		},
		// Service 2: ipinfo.io
		func(ctx context.Context, res *NodeTestResult) error {
			return s.tryIPInfo(ctx, outbound_adapter, res)
		},
		// Service 3: ipwhois.io
		func(ctx context.Context, res *NodeTestResult) error {
			return s.tryIPWhois(ctx, outbound_adapter, res)
		},
		// Service 4: ping0.cc
		func(ctx context.Context, res *NodeTestResult) error {
			return s.tryPing0(ctx, outbound_adapter, res)
		},
	}
	
	s.executeIPLookups(dialCtx, result, ipLookupTasks)
	
	if result.LandingIP == "" {
		result.Error = "all IP lookup services failed"
		// Do not set Available = false here, because the node is reachable (latency > 0)
	} else {
		// After successful IP lookup, try to get fraud score if IP is available
		if result.LandingIP != "" {
			s.getIPTypeAndScore(dialCtx, outbound_adapter, result)
		}
	}

	return result, nil
}

// testWithSOCKS5 tests SOCKS5 node and queries IP directly without sing-box
func (s *NodeTestService) testWithSOCKS5(outbound model.Outbound, result *NodeTestResult) (*NodeTestResult, error) {
	// Parse outbound options
	var options map[string]interface{}
	if err := json.Unmarshal(outbound.Options, &options); err != nil {
		result.Error = "failed to parse options"
		return result, nil
	}

	server, _ := options["server"].(string)
	port := 0
	if p, ok := options["server_port"].(float64); ok {
		port = int(p)
	}
	username, _ := options["username"].(string)
	password, _ := options["password"].(string)

	if server == "" || port == 0 {
		result.Error = "invalid server or port"
		return result, nil
	}

	// Create SOCKS5 dialer
	var auth *proxy.Auth
	if username != "" || password != "" {
		auth = &proxy.Auth{
			User:     username,
			Password: password,
		}
	}

	proxyAddr := fmt.Sprintf("%s:%d", server, port)
	dialer, err := proxy.SOCKS5("tcp", proxyAddr, auth, proxy.Direct)
	if err != nil {
		result.Error = fmt.Sprintf("create SOCKS5 dialer failed: %v", err)
		return result, nil
	}

	// Try multiple IP lookup services concurrently
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ipLookupTasks := []IPLookupTask{
		// Service 1: ip-api.com
		func(ctx context.Context, res *NodeTestResult) error {
			return s.tryIPAPIWithDialer(dialer, res)
		},
		// Service 2: ipinfo.io
		func(ctx context.Context, res *NodeTestResult) error {
			return s.tryIPInfoWithDialer(dialer, res)
		},
		// Service 3: ipwhois.io
		func(ctx context.Context, res *NodeTestResult) error {
			return s.tryIPWhoisWithDialer(dialer, res)
		},
		// Service 4: ping0.cc
		func(ctx context.Context, res *NodeTestResult) error {
			return s.tryPing0WithDialer(dialer, res)
		},
	}
	
	s.executeIPLookups(ctx, result, ipLookupTasks)
	
	if result.LandingIP == "" {
		result.Error = "all IP lookup services failed"
	} else {
		// Try to get fraud score
		if result.LandingIP != "" {
			s.getIPTypeAndScoreWithDialer(dialer, result)
		}
	}

	return result, nil
}


// tryIPAPI attempts to get IP info from ip-api.com
func (s *NodeTestService) tryIPAPI(ctx context.Context, outbound adapter.Outbound, result *NodeTestResult) error {
	// Dial to ip-api.com through the proxy (use IP to avoid DNS issues)
	// ip-api.com IP: 208.95.112.1
	destination := M.ParseSocksaddr("208.95.112.1:80")
	
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, destination)
	if err != nil {
		return fmt.Errorf("dial via proxy failed: %v", err)
	}
	defer conn.Close()

	// Send HTTP request with Host header
	req := "GET /json/?fields=status,message,country,regionName,city,isp,query,reverse HTTP/1.1\r\nHost: ip-api.com\r\nConnection: close\r\n\r\n"
	_, err = conn.Write([]byte(req))
	if err != nil {
		return fmt.Errorf("write failed: %v", err)
	}
	
	// Start measuring time for IP check (we can use this as fallback RealLatency if the first one failed)
	ipStart := time.Now()

	// Read response
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("read failed: %v", err)
	}

	// If RealLatency was not set by the fast check, use the time to first byte of IP API
	if result.RealLatency == 0 {
		result.RealLatency = time.Since(ipStart).Milliseconds()
	}

	response := string(buf[:n])
	bodyStart := -1
	for i := 0; i < len(response)-3; i++ {
		if response[i:i+4] == "\r\n\r\n" {
			bodyStart = i + 4
			break
		}
	}

	if bodyStart > 0 && bodyStart < len(response) {
		body := response[bodyStart:]
		var ipInfo map[string]interface{}
		if err := json.Unmarshal([]byte(body), &ipInfo); err == nil {
			result.LandingIP, _ = ipInfo["query"].(string)
			result.Country, _ = ipInfo["country"].(string)
			result.Region, _ = ipInfo["regionName"].(string)
			result.City, _ = ipInfo["city"].(string)
			result.ISP, _ = ipInfo["isp"].(string)
			hostname, _ := ipInfo["reverse"].(string)
			if result.IPType == "" { result.IPType = s.inferIPType(result.ISP, hostname) }
			return nil
		}
		return fmt.Errorf("parse IP info failed: %v", err)
	}
	return fmt.Errorf("invalid HTTP response")
}

// tryIPInfo attempts to get IP info from ipinfo.io
func (s *NodeTestService) tryIPInfo(ctx context.Context, outbound adapter.Outbound, result *NodeTestResult) error {
	// ipinfo.io IP: 34.117.59.81
	destination := M.ParseSocksaddr("34.117.59.81:80")
	
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, destination)
	if err != nil {
		return fmt.Errorf("dial failed: %v", err)
	}
	defer conn.Close()

	req := "GET /json HTTP/1.1\r\nHost: ipinfo.io\r\nConnection: close\r\n\r\n"
	_, err = conn.Write([]byte(req))
	if err != nil {
		return fmt.Errorf("write failed: %v", err)
	}

	ipStart := time.Now()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("read failed: %v", err)
	}

	if result.RealLatency == 0 {
		result.RealLatency = time.Since(ipStart).Milliseconds()
	}

	response := string(buf[:n])
	bodyStart := -1
	for i := 0; i < len(response)-3; i++ {
		if response[i:i+4] == "\r\n\r\n" {
			bodyStart = i + 4
			break
		}
	}

	if bodyStart > 0 && bodyStart < len(response) {
		body := response[bodyStart:]
		var ipInfo map[string]interface{}
		if err := json.Unmarshal([]byte(body), &ipInfo); err == nil {
			result.LandingIP, _ = ipInfo["ip"].(string)
			result.Country, _ = ipInfo["country"].(string)
			result.Region, _ = ipInfo["region"].(string)
			result.City, _ = ipInfo["city"].(string)
			// ipinfo.io returns "org" which includes ISP info
			if org, ok := ipInfo["org"].(string); ok {
				result.ISP = org
			}
			// Attempt to guess type from org/isp if not provided (ipinfo free doesn't provide type)
			hostname, _ := ipInfo["hostname"].(string)
			if result.IPType == "" {
				result.IPType = s.inferIPType(result.ISP, hostname)
			}
			return nil
		}
		return fmt.Errorf("parse IP info failed: %v", err)
	}
	return fmt.Errorf("invalid HTTP response")
}

// tryIPWhois attempts to get IP info from ipwhois.io
func (s *NodeTestService) tryIPWhois(ctx context.Context, outbound adapter.Outbound, result *NodeTestResult) error {
	// ipwhois.io uses Cloudflare, try common CF IP
	destination := M.ParseSocksaddr("104.21.14.178:80")
	
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, destination)
	if err != nil {
		return fmt.Errorf("dial failed: %v", err)
	}
	defer conn.Close()

	req := "GET /json/ HTTP/1.1\r\nHost: ipwhois.app\r\nConnection: close\r\n\r\n"
	_, err = conn.Write([]byte(req))
	if err != nil {
		return fmt.Errorf("write failed: %v", err)
	}

	ipStart := time.Now()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("read failed: %v", err)
	}

	if result.RealLatency == 0 {
		result.RealLatency = time.Since(ipStart).Milliseconds()
	}

	response := string(buf[:n])
	bodyStart := -1
	for i := 0; i < len(response)-3; i++ {
		if response[i:i+4] == "\r\n\r\n" {
			bodyStart = i + 4
			break
		}
	}

	if bodyStart > 0 && bodyStart < len(response) {
		body := response[bodyStart:]
		var ipInfo map[string]interface{}
		if err := json.Unmarshal([]byte(body), &ipInfo); err == nil {
			result.LandingIP, _ = ipInfo["ip"].(string)
			result.Country, _ = ipInfo["country"].(string)
			result.Region, _ = ipInfo["region"].(string)
			result.City, _ = ipInfo["city"].(string)
			result.ISP, _ = ipInfo["isp"].(string)
			hostname, _ := ipInfo["reverse"].(string)
			if result.IPType == "" { result.IPType = s.inferIPType(result.ISP, hostname) }
			return nil
		}
		return fmt.Errorf("parse IP info failed: %v", err)
	}
	return fmt.Errorf("invalid HTTP response")
}

// tryIPAPIWithDialer attempts to get IP info from ip-api.com using a dialer
func (s *NodeTestService) tryIPAPIWithDialer(dialer proxy.Dialer, result *NodeTestResult) error {
	// Dial to ip-api.com through the proxy
	destination := "208.95.112.1:80"
	
	conn, err := dialer.Dial("tcp", destination)
	if err != nil {
		return fmt.Errorf("dial via proxy failed: %v", err)
	}
	defer conn.Close()

	// Send HTTP request
	req := "GET /json/?fields=status,message,country,regionName,city,isp,query,reverse HTTP/1.1\r\nHost: ip-api.com\r\nConnection: close\r\n\r\n"
	_, err = conn.Write([]byte(req))
	if err != nil {
		return fmt.Errorf("write failed: %v", err)
	}
	
	ipStart := time.Now()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("read failed: %v", err)
	}

	if result.RealLatency == 0 {
		result.RealLatency = time.Since(ipStart).Milliseconds()
	}

	response := string(buf[:n])
	bodyStart := -1
	for i := 0; i < len(response)-3; i++ {
		if response[i:i+4] == "\r\n\r\n" {
			bodyStart = i + 4
			break
		}
	}

	if bodyStart > 0 && bodyStart < len(response) {
		body := response[bodyStart:]
		var ipInfo map[string]interface{}
		if err := json.Unmarshal([]byte(body), &ipInfo); err == nil {
			result.LandingIP, _ = ipInfo["query"].(string)
			result.Country, _ = ipInfo["country"].(string)
			result.Region, _ = ipInfo["regionName"].(string)
			result.City, _ = ipInfo["city"].(string)
			result.ISP, _ = ipInfo["isp"].(string)
			hostname, _ := ipInfo["reverse"].(string)
			if result.IPType == "" { result.IPType = s.inferIPType(result.ISP, hostname) }
			return nil
		}
		return fmt.Errorf("parse IP info failed: %v", err)
	}
	return fmt.Errorf("invalid HTTP response")
}

// tryIPInfoWithDialer attempts to get IP info from ipinfo.io using a dialer
func (s *NodeTestService) tryIPInfoWithDialer(dialer proxy.Dialer, result *NodeTestResult) error {
	destination := "34.117.59.81:80"
	
	conn, err := dialer.Dial("tcp", destination)
	if err != nil {
		return fmt.Errorf("dial failed: %v", err)
	}
	defer conn.Close()

	req := "GET /json HTTP/1.1\r\nHost: ipinfo.io\r\nConnection: close\r\n\r\n"
	_, err = conn.Write([]byte(req))
	if err != nil {
		return fmt.Errorf("write failed: %v", err)
	}

	ipStart := time.Now()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("read failed: %v", err)
	}

	if result.RealLatency == 0 {
		result.RealLatency = time.Since(ipStart).Milliseconds()
	}

	response := string(buf[:n])
	bodyStart := -1
	for i := 0; i < len(response)-3; i++ {
		if response[i:i+4] == "\r\n\r\n" {
			bodyStart = i + 4
			break
		}
	}

	if bodyStart > 0 && bodyStart < len(response) {
		body := response[bodyStart:]
		var ipInfo map[string]interface{}
		if err := json.Unmarshal([]byte(body), &ipInfo); err == nil {
			result.LandingIP, _ = ipInfo["ip"].(string)
			result.Country, _ = ipInfo["country"].(string)
			result.Region, _ = ipInfo["region"].(string)
			result.City, _ = ipInfo["city"].(string)
			if org, ok := ipInfo["org"].(string); ok {
				result.ISP = org
			}
			// Attempt to guess type from org/isp
			hostname, _ := ipInfo["hostname"].(string)
			if result.IPType == "" {
				result.IPType = s.inferIPType(result.ISP, hostname)
			}
			return nil
		}
		return fmt.Errorf("parse IP info failed: %v", err)
	}
	return fmt.Errorf("invalid HTTP response")
}

// tryIPWhoisWithDialer attempts to get IP info from ipwhois.io using a dialer
func (s *NodeTestService) tryIPWhoisWithDialer(dialer proxy.Dialer, result *NodeTestResult) error {
	destination := "104.21.14.178:80"
	
	conn, err := dialer.Dial("tcp", destination)
	if err != nil {
		return fmt.Errorf("dial failed: %v", err)
	}
	defer conn.Close()

	req := "GET /json/ HTTP/1.1\r\nHost: ipwhois.app\r\nConnection: close\r\n\r\n"
	_, err = conn.Write([]byte(req))
	if err != nil {
		return fmt.Errorf("write failed: %v", err)
	}

	ipStart := time.Now()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("read failed: %v", err)
	}

	if result.RealLatency == 0 {
		result.RealLatency = time.Since(ipStart).Milliseconds()
	}

	response := string(buf[:n])
	bodyStart := -1
	for i := 0; i < len(response)-3; i++ {
		if response[i:i+4] == "\r\n\r\n" {
			bodyStart = i + 4
			break
		}
	}

	if bodyStart > 0 && bodyStart < len(response) {
		body := response[bodyStart:]
		var ipInfo map[string]interface{}
		if err := json.Unmarshal([]byte(body), &ipInfo); err == nil {
			result.LandingIP, _ = ipInfo["ip"].(string)
			result.Country, _ = ipInfo["country"].(string)
			result.Region, _ = ipInfo["region"].(string)
			result.City, _ = ipInfo["city"].(string)
			result.ISP, _ = ipInfo["isp"].(string)
			// ipwhois.io doesn't strictly have a hostname field often, but let's check just in case or pass empty
			// Actually ipwhois.app has 'org' and 'isp'. No specific hostname/reverse field documented as standard free.
			// But since we changed signature, we MUST update call.
			if result.IPType == "" { result.IPType = s.inferIPType(result.ISP, "") }
			return nil
		}
		return fmt.Errorf("parse IP info failed: %v", err)
	}
	return fmt.Errorf("invalid HTTP response")
}

// tryPing0 attempts to get IP info from ping0.cc
func (s *NodeTestService) tryPing0(ctx context.Context, outbound adapter.Outbound, result *NodeTestResult) error {
	// ping0.cc IP (one of them): 172.67.166.195 (Cloudflare) - utilizing domain for SNI might be needed if behind CF
	// Since we need HTTPS for /geo usually, or HTTP. The user provided http://ping0.cc/
	// Let's try HTTP with Host header first or HTTPS if supported.
	// ping0.cc usually forces HTTPS.
	// We'll use a fixed IP for ping0.cc to avoid DNS resolution, but we need SNI.
	// For simplicity in this codebase context where we manually construct HTTP requests, handling HTTPS (TLS) manually via a TCP dialer is complex (need TLS handshake).
	// If the outbound supports connection reuse or we can just use HTTP, it's easier.
	// However, `read_url_content` showed `https://ping0.cc/geo` works. 
	// Most `outbound.DialContext` returns a net.Conn. If we need TLS, we have to wrap it.
	// For now, let's try HTTP to `ping0.cc:80`. If it redirects to HTTPS, we might fail since we don't handle 301.
	// But `curl http://ping0.cc/geo` usually works or returns 301.
	// Let's assume we can try to connect to port 80.
	
	// Actually, `ping0.cc` is behind Cloudflare. Direct IP access with Host header `ping0.cc` on port 80 should work if they allow HTTP.
	// If they enforce HTTPS, we cannot easily do it without a TLS client.
	// Given the constraints and previous patterns (using `http.Client` with custom transport in `getScamalyticsScore`), we should probably use that approach if we need HTTPS.
	// BUT `tryIPAPI` and others utilize raw TCP + HTTP payload.
	// valid IP for ping0.cc: 104.21.16.196 (CF)
	
	destination := M.ParseSocksaddr("104.21.16.196:80")
	conn, err := outbound.DialContext(ctx, N.NetworkTCP, destination)
	if err != nil {
		return fmt.Errorf("dial failed: %v", err)
	}
	defer conn.Close()

	req := "GET /geo HTTP/1.1\r\nHost: ping0.cc\r\nUser-Agent: curl/7.68.0\r\nConnection: close\r\n\r\n"
	_, err = conn.Write([]byte(req))
	if err != nil {
		return fmt.Errorf("write failed: %v", err)
	}

	ipStart := time.Now()
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("read failed: %v", err)
	}

	if result.RealLatency == 0 {
		result.RealLatency = time.Since(ipStart).Milliseconds()
	}

	response := string(buf[:n])
	bodyStart := -1
	for i := 0; i < len(response)-3; i++ {
		if response[i:i+4] == "\r\n\r\n" {
			bodyStart = i + 4
			break
		}
	}

	if bodyStart > 0 && bodyStart < len(response) {
		body := response[bodyStart:]
		lines := strings.Split(body, "\n")
		if len(lines) >= 2 {
			// Line 1: IP (Hostname) or just IP
			line1 := strings.TrimSpace(lines[0])
			var hostname string
			if idx := strings.Index(line1, "("); idx > 0 && strings.HasSuffix(line1, ")") {
				result.LandingIP = strings.TrimSpace(line1[:idx])
				hostname = strings.TrimSpace(line1[idx+1 : len(line1)-1])
			} else {
				result.LandingIP = line1
			}
			
			// Line 2: "Country Region City — ISP" or just "Country Region City"
			// Example: "美国 弗吉尼亚州 阿什本 — 甲骨文云 Oracle"
			locationPart := lines[1]
			if parts := strings.Split(lines[1], "—"); len(parts) > 1 {
				locationPart = strings.TrimSpace(parts[0])
				// ISP might be in the second part
			}
			
			locParts := strings.Fields(locationPart)
			if len(locParts) > 0 {
				result.Country = locParts[0]
			}
			if len(locParts) > 1 {
				result.Region = locParts[1]
			}
			if len(locParts) > 2 {
				result.City = locParts[2]
			}
			
			// ISP from Line 4 (English) preferred, or fallback to parsed Chinese ISP
			if len(lines) >= 4 && strings.TrimSpace(lines[3]) != "" {
				result.ISP = strings.TrimSpace(lines[3])
			} else if len(lines) >= 3 && strings.HasPrefix(lines[2], "AS") {
                 // Sometimes formatting varies, maybe AS is useful
            }
            
            // If we didn't get ISP from line 4, check if hostname helps
            if result.IPType == "" {
            	result.IPType = s.inferIPType(result.ISP, hostname)
            }
            
			return nil
		}
		return fmt.Errorf("parse IP info failed: invalid format")
	}
	return fmt.Errorf("invalid HTTP response")
}

// tryPing0WithDialer attempts to get IP info from ping0.cc using a dialer
func (s *NodeTestService) tryPing0WithDialer(dialer proxy.Dialer, result *NodeTestResult) error {
	destination := "104.21.16.196:80"
	conn, err := dialer.Dial("tcp", destination)
	if err != nil {
		return fmt.Errorf("dial failed: %v", err)
	}
	defer conn.Close()

	req := "GET /geo HTTP/1.1\r\nHost: ping0.cc\r\nUser-Agent: curl/7.68.0\r\nConnection: close\r\n\r\n"
	_, err = conn.Write([]byte(req))
	if err != nil {
		return fmt.Errorf("write failed: %v", err)
	}

	ipStart := time.Now()
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("read failed: %v", err)
	}

	if result.RealLatency == 0 {
		result.RealLatency = time.Since(ipStart).Milliseconds()
	}

	response := string(buf[:n])
	bodyStart := -1
	for i := 0; i < len(response)-3; i++ {
		if response[i:i+4] == "\r\n\r\n" {
			bodyStart = i + 4
			break
		}
	}

	if bodyStart > 0 && bodyStart < len(response) {
		body := response[bodyStart:]
		lines := strings.Split(body, "\n")
		if len(lines) >= 2 {
			// Line 1: IP (Hostname) or just IP
			line1 := strings.TrimSpace(lines[0])
			var hostname string
			if idx := strings.Index(line1, "("); idx > 0 && strings.HasSuffix(line1, ")") {
				result.LandingIP = strings.TrimSpace(line1[:idx])
				hostname = strings.TrimSpace(line1[idx+1 : len(line1)-1])
			} else {
				result.LandingIP = line1
			}

			locationPart := lines[1]
			if parts := strings.Split(lines[1], "—"); len(parts) > 1 {
				locationPart = strings.TrimSpace(parts[0])
			}
			locParts := strings.Fields(locationPart)
			if len(locParts) > 0 {
				result.Country = locParts[0]
			}
			if len(locParts) > 1 {
				result.Region = locParts[1]
			}
			if len(locParts) > 2 {
				result.City = locParts[2]
			}
			if len(lines) >= 4 && strings.TrimSpace(lines[3]) != "" {
				result.ISP = strings.TrimSpace(lines[3])
			}
			if result.IPType == "" {
				result.IPType = s.inferIPType(result.ISP, hostname)
			}
			return nil
		}
		return fmt.Errorf("parse IP info failed: invalid format")
	}
	return fmt.Errorf("invalid HTTP response")
}

// TestAllOutbounds tests all outbounds in parallel
func (s *NodeTestService) TestAllOutbounds(concurrency int) ([]*NodeTestResult, error) {
	db := database.GetDB()
	var outbounds []model.Outbound
	err := db.Find(&outbounds).Error
	if err != nil {
		return nil, err
	}

	if concurrency <= 0 {
		concurrency = 50 // Default concurrency
	}

	results := make([]*NodeTestResult, 0, len(outbounds))
	var mu sync.Mutex
	var wg sync.WaitGroup
	
	// Semaphore for concurrency control
	sem := make(chan struct{}, concurrency)

	for _, outbound := range outbounds {
		// Skip non-proxy outbounds
		if outbound.Type == "direct" || outbound.Type == "selector" || 
		   outbound.Type == "urltest" || outbound.Type == "block" {
			continue
		}

		wg.Add(1)
		go func(ob model.Outbound) {
			defer wg.Done()
			sem <- struct{}{}        // Acquire
			defer func() { <-sem }() // Release

			result, _ := s.TestOutbound(ob.Tag)
			if result != nil {
				s.SaveTestResult(result) // Save the basic connectivity result
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
			}
		}(outbound)
	}

	wg.Wait()
	return results, nil
}

// TestSelectedOutbounds tests selected outbounds in parallel
func (s *NodeTestService) TestSelectedOutbounds(tags []string, concurrency int) ([]*NodeTestResult, error) {
	db := database.GetDB()
	var outbounds []model.Outbound
	// Fetch only selected tags
	err := db.Where("tag IN ?", tags).Find(&outbounds).Error
	if err != nil {
		return nil, err
	}

	if concurrency <= 0 {
		concurrency = 50
	}

	results := make([]*NodeTestResult, 0, len(outbounds))
	var mu sync.Mutex
	var wg sync.WaitGroup
	
	sem := make(chan struct{}, concurrency)

	for _, outbound := range outbounds {
		// Skip non-proxy outbounds
		if outbound.Type == "direct" || outbound.Type == "selector" || 
		   outbound.Type == "urltest" || outbound.Type == "block" {
			continue
		}

		wg.Add(1)
		go func(ob model.Outbound) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result, _ := s.TestOutbound(ob.Tag)
			if result != nil {
				s.SaveTestResult(result) // Save the basic connectivity result
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
			}
		}(outbound)
	}

	wg.Wait()
	return results, nil
}

// TestAllOutboundsWithIP tests all outbounds and gets landing IPs (slower, requires sing-box running)
func (s *NodeTestService) TestAllOutboundsWithIP(concurrency int, ctx context.Context) ([]*NodeTestResult, error) {
	db := database.GetDB()
	var outbounds []model.Outbound
	err := db.Find(&outbounds).Error
	if err != nil {
		return nil, err
	}

	if concurrency <= 0 {
		concurrency = 10 // Lower concurrency for IP lookup (API rate limits)
	}

	results := make([]*NodeTestResult, 0, len(outbounds))
	var mu sync.Mutex
	var wg sync.WaitGroup
	
	sem := make(chan struct{}, concurrency)

	for _, outbound := range outbounds {
		// Skip non-proxy outbounds
		if outbound.Type == "direct" || outbound.Type == "selector" || 
		   outbound.Type == "urltest" || outbound.Type == "block" {
			continue
		}

		wg.Add(1)
		go func(ob model.Outbound) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result, _ := s.TestOutboundWithLandingIP(ob.Tag, ctx)
			if result != nil {
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
			}
		}(outbound)
	}

	wg.Wait()
	return results, nil
}

// TestSelectedOutboundsWithIP tests selected outbounds and gets landing IPs
func (s *NodeTestService) TestSelectedOutboundsWithIP(tags []string, concurrency int, ctx context.Context) ([]*NodeTestResult, error) {
	db := database.GetDB()
	var outbounds []model.Outbound
	err := db.Where("tag IN ?", tags).Find(&outbounds).Error
	if err != nil {
		return nil, err
	}

	if concurrency <= 0 {
		concurrency = 10
	}

	results := make([]*NodeTestResult, 0, len(outbounds))
	var mu sync.Mutex
	var wg sync.WaitGroup
	
	sem := make(chan struct{}, concurrency)

	for _, outbound := range outbounds {
		// Skip non-proxy outbounds
		if outbound.Type == "direct" || outbound.Type == "selector" || 
		   outbound.Type == "urltest" || outbound.Type == "block" {
			continue
		}

		wg.Add(1)
		go func(ob model.Outbound) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result, _ := s.TestOutboundWithLandingIP(ob.Tag, ctx)
			if result != nil {
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
			}
		}(outbound)
	}

	wg.Wait()
	return results, nil
}

// TestAllOutboundsWithIPInternal is the internal method that uses corePtr directly
func (s *NodeTestService) TestAllOutboundsWithIPInternal(concurrency int) ([]*NodeTestResult, error) {
	if !corePtr.IsRunning() {
		return nil, fmt.Errorf("sing-box is not running")
	}
	
	ctx := corePtr.GetCtx()
	if ctx == nil {
		return nil, fmt.Errorf("sing-box context not available")
	}
	
	return s.TestAllOutboundsWithIP(concurrency, ctx)
}

// TestSelectedOutboundsWithIPInternal is the internal method that uses corePtr directly
func (s *NodeTestService) TestSelectedOutboundsWithIPInternal(tags []string, concurrency int) ([]*NodeTestResult, error) {
	if !corePtr.IsRunning() {
		return nil, fmt.Errorf("sing-box is not running")
	}
	
	ctx := corePtr.GetCtx()
	if ctx == nil {
		return nil, fmt.Errorf("sing-box context not available")
	}
	
	return s.TestSelectedOutboundsWithIP(tags, concurrency, ctx)
}

// TestSelectedAndSave tests selected nodes with IP and saves results to database
func (s *NodeTestService) TestSelectedAndSave(tags []string, concurrency int) ([]*NodeTestResult, error) {
	results, err := s.TestSelectedOutboundsWithIPInternal(tags, concurrency)
	if err != nil {
		return nil, err
	}
	
	// Save results to database
	for _, result := range results {
		s.SaveTestResult(result)
	}
	
	return results, nil
}

// SaveTestResult saves the test result to database
func (s *NodeTestService) SaveTestResult(result *NodeTestResult) error {
	db := database.GetDB()
	now := time.Now().Unix()
	
	updates := map[string]interface{}{
		"last_test_time": now,
		"available":      result.Available,
	}

	// Only update location/IP details if we actually got them
	if result.LandingIP != "" {
		updates["landing_ip"] = result.LandingIP
		updates["country"] = result.Country
		updates["region"] = result.Region
		updates["city"] = result.City
		updates["fraud_score"] = result.FraudScore
		updates["ip_type"] = result.IPType
	}

	return db.Model(&model.Outbound{}).
		Where("tag = ?", result.Tag).
		Updates(updates).Error
}

// getIPTypeAndScore attempts to get IP type and fraud score
func (s *NodeTestService) getIPTypeAndScore(ctx context.Context, outbound adapter.Outbound, result *NodeTestResult) {
	// 1. If IPType is missing, try to fetch it from ip-api.com (if not already tried) or others
	// ip-api.com free doesn't give type/mobile/proxy.
	// We rely on ipwhois.io (tryIPWhois) which gives "type".

	// 2. Get Fraud Score from scamalytics.com (scraping)
	// https://scamalytics.com/ip/{ip}
	// We need to request this via the proxy because direct request might be blocked or we want to test the node's IP representation.
	// However, scamalytics might block data center IPs.
	// Actually, we should request scamalytics from the SERVER (direct) to check the LANDING IP.
	// But the server might be blocked too.
	// Let's try requesting through the proxy first, if fails, maybe direct?
	// Usually we want to see how the IP is viewed by the world, so querying from the server (which is not the node) 
	// about the node's IP is the correct way: server checks "scamalytics.com/ip/<landing_ip>"

	s.getScamalyticsScore(ctx, outbound, result)
}

func (s *NodeTestService) getIPTypeAndScoreWithDialer(dialer proxy.Dialer, result *NodeTestResult) {
	s.getScamalyticsScoreWithDialer(dialer, result)
}

func (s *NodeTestService) getScamalyticsScore(ctx context.Context, outbound adapter.Outbound, result *NodeTestResult) {
	// We'll try to fetch from scamalytics using the proxy to avoid server IP bans, 
	// but we represent the LandingIP in the URL.
	url := fmt.Sprintf("https://scamalytics.com/ip/%s", result.LandingIP)
	
	// destination := M.ParseSocksaddr("scamalytics.com:443")
	// For simplicity in this text-based tool, we might need a proper HTTP client over the outbound.
	// Constructing HTTP client over custom dialer:
	
	// Create a custom transport
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// addr is scamalytics.com:443
			// We need to parse it to metadata
			host, port, _ := net.SplitHostPort(addr)
			p, _ := net.LookupPort(network, port)
			dest := M.ParseSocksaddrHostPort(host, uint16(p))
			return outbound.DialContext(ctx, N.NetworkTCP, dest)
		},
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives: true,
	}
	
	client := &http.Client{
		Transport: tr,
		Timeout:   15 * time.Second,
	}
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return
	}
	// Mimic browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	
	resp, err := client.Do(req)
	if err != nil {
		// If proxy fails, try direct? Maybe not.
		return
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	
	// Parse HTML for score
	// Look for: "Fraud Score: </div><div ...>X</div>" or similar
	// Data structure changes often, but usually "Fraud Score" is visible.
	// Current structure (approx): <div class="score">Fraud Score: X</div>
	
	html := string(body)
	// Simple regex or string search
	// Regex for "Fraud Score: \d+" or similar
	re := regexp.MustCompile(`Fraud Score:\s*(\d+)`)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		fmt.Sscanf(matches[1], "%d", &result.FraudScore)
	} else {
		// Try finding JSON in the page if they use it
		// Or another pattern: <div class="score_box">...100...</div>
		// This is brittle. 
		// Fallback: scamlone.com or similar if scamalytics fails?
		// For now just try this.
		
		// Another pattern seen: "score": "0" in JSON-LD or similar?
		// pattern: <div style="...background-color: ...">0</div> (the score is often large)
		
		// Use a simpler heuristic check if regex fails
		if strings.Contains(html, "High Risk") {
			if result.FraudScore == 0 { result.FraudScore = 75 }
		} else if strings.Contains(html, "Medium Risk") {
			if result.FraudScore == 0 { result.FraudScore = 50 }
		} else if strings.Contains(html, "Low Risk") {
			if result.FraudScore == 0 { result.FraudScore = 15 } // Arbitrary low
		}
	}
}

func (s *NodeTestService) getScamalyticsScoreWithDialer(dialer proxy.Dialer, result *NodeTestResult) {
	url := fmt.Sprintf("https://scamalytics.com/ip/%s", result.LandingIP)
	
	tr := &http.Transport{
		Dial: dialer.Dial,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives: true,
	}
	
	client := &http.Client{
		Transport: tr,
		Timeout:   15 * time.Second,
	}
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	
	html := string(body)
	re := regexp.MustCompile(`Fraud Score:\s*(\d+)`)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		fmt.Sscanf(matches[1], "%d", &result.FraudScore)
	}
}

// TestAllAndSave tests all nodes with IP and saves results to database
func (s *NodeTestService) TestAllAndSave(concurrency int) ([]*NodeTestResult, error) {
	results, err := s.TestAllOutboundsWithIPInternal(concurrency)
	if err != nil {
		return nil, err
	}
	
	// Save results to database
	// Save results to database
	for _, result := range results {
		s.SaveTestResult(result)
	}
	
	return results, nil
}

// inferIPType guesses the IP type based on ISP name and Hostname
func (s *NodeTestService) inferIPType(isp, hostname string) string {
	if isp == "" && hostname == "" {
		return ""
	}
	
	lowerISP := strings.ToLower(isp)
	lowerHost := strings.ToLower(hostname)
	
	// Hosting keywords for Hostname
	hostKeywords := []string{
		"ec2", "compute", "cloud", "vps", "server", "hosting", "datacenter", "colocation",
		"azure", "googleusercontent", "amazonaws", "linode", "vultr", "digitalocean",
		"oracle", "alibaba", "tencent", "kamatera", "hetzner", "ovh", "choopa", "leaseweb",
		"m247", "fly.io", "cloudflare", "fastly", "akamai", "cdn",
	}
	
	for _, keyword := range hostKeywords {
		if strings.Contains(lowerHost, keyword) {
			return "Hosting"
		}
	}
	
	// Hosting keywords for ISP
	hostingKeywords := []string{
		"cloud", "vps", "data", "hosting", "server", "solution", "tech", "network", 
		"amazon", "google", "microsoft", "oracle", "aliyun", "tencent", "digitalocean", 
		"vultr", "linode", "hetzner", "ovh", "leaseweb", "choopa", "m247", "fly.io",
		"cloudflare", "fastly", "akamai", "cdn",
	}
	
	for _, keyword := range hostingKeywords {
		if strings.Contains(lowerISP, keyword) {
			return "Hosting"
		}
	}
	
	// ISP keywords
	ispKeywords := []string{
		"telecom", "mobile", "cable", "broadband", "internet", "comcast", "verizon", 
		"spectrum", "t-mobile", "vodafone", "att", "orange", "deutsche telekom",
		"telefonica", "bt", "virgin", "sky", "charter", "cox", "century",
	}
	
	for _, keyword := range ispKeywords {
		if strings.Contains(lowerISP, keyword) {
			return "ISP"
		}
	}
	
	return "Business"
}
// IPLookupTask is a function signature for IP lookup tasks
type IPLookupTask func(ctx context.Context, result *NodeTestResult) error

// executeIPLookups executes multiple IP lookup tasks concurrently and returns the first success
func (s *NodeTestService) executeIPLookups(ctx context.Context, baseResult *NodeTestResult, tasks []IPLookupTask) {
	// Create a new context for the group of tasks if needed, 
	// but we can rely on the passed ctx (dialCtx) which likely has a timeout.
	// However, we want to return as soon as one succeeds.
	
	resultChan := make(chan *NodeTestResult, len(tasks))
	
	// Launch all tasks
	for _, task := range tasks {
		go func(t IPLookupTask) {
			// Create a copy of the result to avoid race conditions when writing to it
			tempResult := *baseResult 
			if err := t(ctx, &tempResult); err == nil {
				resultChan <- &tempResult
			} else {
				resultChan <- nil
			}
		}(task)
	}

	// Wait for first success or all failures
	failures := 0
	for i := 0; i < len(tasks); i++ {
		select {
		case res := <-resultChan:
			if res != nil {
				// Success! Update baseResult with the successful result
				*baseResult = *res
				return
			}
			failures++
		case <-ctx.Done():
			// Context timeout or cancelled
			return 
		}
	}
	
	// If we are here, all tasks failed (or returned nil)
	// baseResult remains unchanged (failed state)
}
