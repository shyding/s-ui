package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

	// Try multiple IP lookup services with fallback
	ipLookupSuccess := false
	
	// Service 1: ip-api.com (fast, free, no auth required)
	if !ipLookupSuccess {
		if err := s.tryIPAPI(dialCtx, outbound_adapter, result); err == nil {
			ipLookupSuccess = true
		}
	}
	
	// Service 2: ipinfo.io (reliable, free tier available)
	if !ipLookupSuccess {
		if err := s.tryIPInfo(dialCtx, outbound_adapter, result); err == nil {
			ipLookupSuccess = true
		}
	}
	
	// Service 3: ipwhois.io (alternative)
	if !ipLookupSuccess {
		if err := s.tryIPWhois(dialCtx, outbound_adapter, result); err == nil {
			ipLookupSuccess = true
		}
	}
	
	if !ipLookupSuccess {
		result.Error = "all IP lookup services failed"
		result.Available = false
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

	// Try multiple IP lookup services with fallback
	ipLookupSuccess := false
	
	// Service 1: ip-api.com (fast, free, no auth required)
	if !ipLookupSuccess {
		if err := s.tryIPAPIWithDialer(dialer, result); err == nil {
			ipLookupSuccess = true
		}
	}
	
	// Service 2: ipinfo.io (reliable, free tier available)
	if !ipLookupSuccess {
		if err := s.tryIPInfoWithDialer(dialer, result); err == nil {
			ipLookupSuccess = true
		}
	}
	
	// Service 3: ipwhois.io (alternative)
	if !ipLookupSuccess {
		if err := s.tryIPWhoisWithDialer(dialer, result); err == nil {
			ipLookupSuccess = true
		}
	}
	
	if !ipLookupSuccess {
		result.Error = "all IP lookup services failed"
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
	req := "GET /json/ HTTP/1.1\r\nHost: ip-api.com\r\nConnection: close\r\n\r\n"
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
	req := "GET /json/ HTTP/1.1\r\nHost: ip-api.com\r\nConnection: close\r\n\r\n"
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
			return nil
		}
		return fmt.Errorf("parse IP info failed: %v", err)
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
		if result.LandingIP != "" {
			s.SaveTestResult(result)
		}
	}
	
	return results, nil
}

// SaveTestResult saves the test result to database
func (s *NodeTestService) SaveTestResult(result *NodeTestResult) error {
	if result.LandingIP == "" {
		return nil // no IP info to save
	}
	
	db := database.GetDB()
	now := time.Now().Unix()
	
	return db.Model(&model.Outbound{}).
		Where("tag = ?", result.Tag).
		Updates(map[string]interface{}{
			"landing_ip":     result.LandingIP,
			"country":        result.Country,
			"region":         result.Region,
			"city":           result.City,
			"last_test_time": now,
		}).Error
}

// TestAllAndSave tests all nodes with IP and saves results to database
func (s *NodeTestService) TestAllAndSave(concurrency int) ([]*NodeTestResult, error) {
	results, err := s.TestAllOutboundsWithIPInternal(concurrency)
	if err != nil {
		return nil, err
	}
	
	// Save results to database
	for _, result := range results {
		if result.LandingIP != "" {
			s.SaveTestResult(result)
		}
	}
	
	return results, nil
}
