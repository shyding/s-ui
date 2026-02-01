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
)

type NodeTestService struct{}

type NodeTestResult struct {
	Tag       string `json:"tag"`
	Server    string `json:"server"`
	Port      int    `json:"port"`
	Latency   int64  `json:"latency"`   // milliseconds, -1 means failed
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

	// Skip IP lookup if connection failed
	if !result.Available {
		return result, nil
	}

	// Try to get landing IP via sing-box outbound
	outboundManager := service.FromContext[adapter.OutboundManager](ctx)
	if outboundManager == nil {
		result.Error = "sing-box not running"
		return result, nil
	}

	outbound, loaded := outboundManager.Outbound(tag)
	if !loaded {
		result.Error = "outbound not found in sing-box"
		return result, nil
	}

	// Create a dialer using the outbound
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Dial to ip-api.com through the proxy
	destination := M.ParseSocksaddr("ip-api.com:80")
	
	conn, err := outbound.DialContext(dialCtx, N.NetworkTCP, destination)
	if err != nil {
		result.Error = fmt.Sprintf("dial via proxy failed: %v", err)
		return result, nil
	}
	defer conn.Close()

	// Send HTTP request
	req := "GET /json/ HTTP/1.1\r\nHost: ip-api.com\r\nConnection: close\r\n\r\n"
	_, err = conn.Write([]byte(req))
	if err != nil {
		result.Error = fmt.Sprintf("write failed: %v", err)
		return result, nil
	}

	// Read response
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		result.Error = fmt.Sprintf("read failed: %v", err)
		return result, nil
	}

	// Parse response body (skip HTTP headers)
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
		} else {
			result.Error = fmt.Sprintf("parse IP info failed: %v", err)
		}
	} else {
		result.Error = "invalid HTTP response"
	}

	return result, nil
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
