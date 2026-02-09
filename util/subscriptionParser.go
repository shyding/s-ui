package util

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ClashConfig represents the structure of a Clash YAML config
type ClashConfig struct {
	Proxies []map[string]interface{} `yaml:"proxies"`
}

// SingboxConfig represents the structure of a sing-box JSON config
type SingboxConfig struct {
	Outbounds []map[string]interface{} `json:"outbounds"`
}

// SubscriptionResult holds parsed outbound configurations
type SubscriptionResult struct {
	Outbounds []map[string]interface{}
	Errors    []string
	Format    string // detected format: "clash", "singbox", "v2rayn", "links"
}

// ParseSubscription auto-detects format and parses subscription content
func ParseSubscription(content string, subscriptionName string) (*SubscriptionResult, error) {
	content = strings.TrimSpace(content)
	
	// Try to detect format
	// 1. Check for sing-box JSON format (has "outbounds" key)
	if strings.HasPrefix(content, "{") && strings.Contains(content, "\"outbounds\"") {
		return ParseSingboxJSON(content, subscriptionName)
	}
	
	// 2. If it starts with "proxies:" or contains YAML structure, parse as Clash
	if strings.Contains(content, "proxies:") || strings.HasPrefix(content, "port:") {
		return ParseClashYAML(content, subscriptionName)
	}
	
	// 3. Try to decode as base64 (v2rayN format)
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err == nil && len(decoded) > 0 {
		decodedStr := string(decoded)
		// Check if decoded content is sing-box JSON
		if strings.HasPrefix(strings.TrimSpace(decodedStr), "{") && strings.Contains(decodedStr, "\"outbounds\"") {
			return ParseSingboxJSON(decodedStr, subscriptionName)
		}
		// Check if decoded content is Clash YAML
		if strings.Contains(decodedStr, "proxies:") {
			return ParseClashYAML(decodedStr, subscriptionName)
		}
		return ParseLinks(decodedStr, subscriptionName)
	}
	
	// 4. Try URL-safe base64
	decoded, err = base64.URLEncoding.DecodeString(content)
	if err == nil && len(decoded) > 0 {
		return ParseLinks(string(decoded), subscriptionName)
	}
	
	// 5. Try as plain text links (one per line)
	return ParseLinks(content, subscriptionName)
}

// ParseSingboxJSON parses sing-box JSON format subscription
func ParseSingboxJSON(content string, subscriptionName string) (*SubscriptionResult, error) {
	result := &SubscriptionResult{
		Outbounds: []map[string]interface{}{},
		Errors:    []string{},
		Format:    "singbox",
	}
	
	var config SingboxConfig
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		return nil, fmt.Errorf("failed to parse sing-box JSON: %v", err)
	}
	
	for _, outbound := range config.Outbounds {
		// Skip special outbound types (direct, block, dns, selector, urltest)
		outType, _ := outbound["type"].(string)
		if outType == "direct" || outType == "block" || outType == "dns" || 
		   outType == "selector" || outType == "urltest" {
			continue
		}
		
		// Get tag and add subscription prefix
		tag, _ := outbound["tag"].(string)
		if tag == "" {
			result.Errors = append(result.Errors, "Outbound missing tag")
			continue
		}
		
		// Clone the outbound and update tag with prefix
		newOutbound := make(map[string]interface{})
		for k, v := range outbound {
			newOutbound[k] = v
		}
		newOutbound["tag"] = fmt.Sprintf("[%s] %s", subscriptionName, tag)
		
		result.Outbounds = append(result.Outbounds, newOutbound)
	}
	
	return result, nil
}

// ParseClashYAML parses Clash YAML format subscription
func ParseClashYAML(content string, subscriptionName string) (*SubscriptionResult, error) {
	result := &SubscriptionResult{
		Outbounds: []map[string]interface{}{},
		Errors:    []string{},
		Format:    "clash",
	}
	
	var config ClashConfig
	if err := yaml.Unmarshal([]byte(content), &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %v", err)
	}
	
	for _, proxy := range config.Proxies {
		outbound, err := clashProxyToOutbound(proxy, subscriptionName)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to convert proxy: %v", err))
			continue
		}
		result.Outbounds = append(result.Outbounds, outbound)
	}
	
	return result, nil
}

// ParseLinks parses line-by-line proxy links (v2ray, ss, trojan, etc.)
func ParseLinks(content string, subscriptionName string) (*SubscriptionResult, error) {
	result := &SubscriptionResult{
		Outbounds: []map[string]interface{}{},
		Errors:    []string{},
	}
	
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		
		outbound, tag, err := GetOutbound(line, i)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Line %d: %v", i+1, err))
			continue
		}
		
		// Add subscription prefix to tag
		if outbound != nil {
			(*outbound)["tag"] = fmt.Sprintf("[%s] %s", subscriptionName, tag)
			result.Outbounds = append(result.Outbounds, *outbound)
		}
	}
	
	return result, nil
}

// clashProxyToOutbound converts a Clash proxy config to sing-box outbound format
func clashProxyToOutbound(proxy map[string]interface{}, subscriptionName string) (map[string]interface{}, error) {
	proxyType, _ := proxy["type"].(string)
	name, _ := proxy["name"].(string)
	server, _ := proxy["server"].(string)
	
	// Get port as int
	var port int
	switch p := proxy["port"].(type) {
	case int:
		port = p
	case float64:
		port = int(p)
	case string:
		port, _ = strconv.Atoi(p)
	}
	
	// Create base outbound with subscription prefix
	outbound := map[string]interface{}{
		"tag":    fmt.Sprintf("[%s] %s", subscriptionName, name),
		"server": server,
		"server_port": port,
	}
	
	switch proxyType {
	case "vmess":
		return clashVmessToOutbound(proxy, outbound)
	case "vless":
		return clashVlessToOutbound(proxy, outbound)
	case "trojan":
		return clashTrojanToOutbound(proxy, outbound)
	case "ss", "shadowsocks":
		return clashSsToOutbound(proxy, outbound)
	case "socks5":
		return clashSocksToOutbound(proxy, outbound)
	case "hysteria2", "hy2":
		return clashHy2ToOutbound(proxy, outbound)
	default:
		return nil, fmt.Errorf("unsupported proxy type: %s", proxyType)
	}
}

func clashVmessToOutbound(proxy map[string]interface{}, outbound map[string]interface{}) (map[string]interface{}, error) {
	outbound["type"] = "vmess"
	outbound["uuid"], _ = proxy["uuid"].(string)
	
	if alterId, ok := proxy["alterId"]; ok {
		switch v := alterId.(type) {
		case int:
			outbound["alter_id"] = v
		case float64:
			outbound["alter_id"] = int(v)
		}
	}
	
	if cipher, ok := proxy["cipher"].(string); ok && cipher != "" && cipher != "auto" {
		outbound["security"] = cipher
	}
	
	// TLS
	if tls, ok := proxy["tls"].(bool); ok && tls {
		tlsConfig := map[string]interface{}{
			"enabled": true,
		}
		if skipVerify, ok := proxy["skip-cert-verify"].(bool); ok {
			tlsConfig["insecure"] = skipVerify
		}
		if sni, ok := proxy["servername"].(string); ok && sni != "" {
			tlsConfig["server_name"] = sni
		}
		outbound["tls"] = tlsConfig
	}
	
	// Transport (ws, grpc, etc.)
	if network, ok := proxy["network"].(string); ok {
		transport := map[string]interface{}{
			"type": network,
		}
		
		if network == "ws" {
			if wsOpts, ok := proxy["ws-opts"].(map[string]interface{}); ok {
				if path, ok := wsOpts["path"].(string); ok {
					transport["path"] = path
				}
				if headers, ok := wsOpts["headers"].(map[string]interface{}); ok {
					transport["headers"] = headers
				}
			}
		}
		
		outbound["transport"] = transport
	}
	
	return outbound, nil
}

func clashVlessToOutbound(proxy map[string]interface{}, outbound map[string]interface{}) (map[string]interface{}, error) {
	outbound["type"] = "vless"
	outbound["uuid"], _ = proxy["uuid"].(string)
	
	// Flow for XTLS
	if flow, ok := proxy["flow"].(string); ok && flow != "" {
		outbound["flow"] = flow
	}
	
	// TLS (similar to vmess)
	if tls, ok := proxy["tls"].(bool); ok && tls {
		tlsConfig := map[string]interface{}{
			"enabled": true,
		}
		if skipVerify, ok := proxy["skip-cert-verify"].(bool); ok {
			tlsConfig["insecure"] = skipVerify
		}
		if sni, ok := proxy["servername"].(string); ok && sni != "" {
			tlsConfig["server_name"] = sni
		}
		outbound["tls"] = tlsConfig
	}
	
	// Transport
	if network, ok := proxy["network"].(string); ok {
		transport := map[string]interface{}{
			"type": network,
		}
		
		if network == "ws" {
			if wsOpts, ok := proxy["ws-opts"].(map[string]interface{}); ok {
				if path, ok := wsOpts["path"].(string); ok {
					transport["path"] = path
				}
			}
		}
		
		outbound["transport"] = transport
	}
	
	return outbound, nil
}

func clashTrojanToOutbound(proxy map[string]interface{}, outbound map[string]interface{}) (map[string]interface{}, error) {
	outbound["type"] = "trojan"
	outbound["password"], _ = proxy["password"].(string)
	
	// TLS is always enabled for trojan
	tlsConfig := map[string]interface{}{
		"enabled": true,
	}
	if skipVerify, ok := proxy["skip-cert-verify"].(bool); ok {
		tlsConfig["insecure"] = skipVerify
	}
	if sni, ok := proxy["sni"].(string); ok && sni != "" {
		tlsConfig["server_name"] = sni
	}
	outbound["tls"] = tlsConfig
	
	return outbound, nil
}

func clashSsToOutbound(proxy map[string]interface{}, outbound map[string]interface{}) (map[string]interface{}, error) {
	outbound["type"] = "shadowsocks"
	outbound["method"], _ = proxy["cipher"].(string)
	outbound["password"], _ = proxy["password"].(string)
	
	return outbound, nil
}

func clashSocksToOutbound(proxy map[string]interface{}, outbound map[string]interface{}) (map[string]interface{}, error) {
	outbound["type"] = "socks"
	
	if username, ok := proxy["username"].(string); ok && username != "" {
		outbound["username"] = username
	}
	if password, ok := proxy["password"].(string); ok && password != "" {
		outbound["password"] = password
	}
	
	return outbound, nil
}

func clashHy2ToOutbound(proxy map[string]interface{}, outbound map[string]interface{}) (map[string]interface{}, error) {
	outbound["type"] = "hysteria2"
	outbound["password"], _ = proxy["password"].(string)
	
	// TLS
	tlsConfig := map[string]interface{}{
		"enabled": true,
		"alpn":    []string{"h3"}, // Default ALPN for Hysteria2
	}
	
	// Check for explicit ALPN
	if alpn, ok := proxy["alpn"].([]interface{}); ok {
		var alpnList []string
		for _, a := range alpn {
			if s, ok := a.(string); ok {
				alpnList = append(alpnList, s)
			}
		}
		if len(alpnList) > 0 {
			tlsConfig["alpn"] = alpnList
		}
	} else if alpn, ok := proxy["alpn"].(string); ok && alpn != "" {
		tlsConfig["alpn"] = []string{alpn}
	}

	if skipVerify, ok := proxy["skip-cert-verify"].(bool); ok {
		tlsConfig["insecure"] = skipVerify
	}
	
	if sni, ok := proxy["sni"].(string); ok && sni != "" {
		tlsConfig["server_name"] = sni
	} else {
		// Default SNI to server address if not specified
		if server, ok := outbound["server"].(string); ok {
			tlsConfig["server_name"] = server
		}
	}
	
	if fingerprint, ok := proxy["fingerprint"].(string); ok && fingerprint != "" {
		tlsConfig["utls"] = map[string]interface{}{
			"enabled": true,
			"fingerprint": fingerprint,
		}
	}

	outbound["tls"] = tlsConfig
	
	return outbound, nil
}
