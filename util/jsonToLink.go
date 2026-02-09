package util

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// OutboundToLink converts an outbound config to a shareable link
func OutboundToLink(outbound map[string]interface{}) (string, error) {
	outType, _ := outbound["type"].(string)
	
	switch outType {
	case "vmess":
		return vmessToLink(outbound)
	case "vless":
		return vlessToLink(outbound)
	case "trojan":
		return trojanToLink(outbound)
	case "shadowsocks":
		return ssToLink(outbound)
	case "socks":
		return socksToLink(outbound)
	case "hysteria2":
		return hy2ToLink(outbound)
	case "hysteria":
		return hyToLink(outbound)
	case "tuic":
		return tuicToLink(outbound)
	case "anytls":
		return anytlsToLink(outbound)
	default:
		return "", fmt.Errorf("unsupported outbound type: %s", outType)
	}
}

func vmessToLink(out map[string]interface{}) (string, error) {
	tag, _ := out["tag"].(string)
	server, _ := out["server"].(string)
	port := getPort(out["server_port"])
	uuid, _ := out["uuid"].(string)
	
	vmessJson := map[string]interface{}{
		"v":    "2",
		"ps":   tag,
		"add":  server,
		"port": port,
		"id":   uuid,
		"aid":  0,
		"net":  "tcp",
		"type": "none",
	}
	
	// Handle transport
	if transport, ok := out["transport"].(map[string]interface{}); ok {
		tpType, _ := transport["type"].(string)
		vmessJson["net"] = tpType
		if path, ok := transport["path"].(string); ok {
			vmessJson["path"] = path
		}
		if host, ok := transport["host"].(string); ok {
			vmessJson["host"] = host
		}
		if headers, ok := transport["headers"].(map[string]interface{}); ok {
			if host, ok := headers["Host"].(string); ok {
				vmessJson["host"] = host
			}
		}
		if serviceName, ok := transport["service_name"].(string); ok {
			vmessJson["path"] = serviceName
		}
	}
	
	// Handle TLS
	if tls, ok := out["tls"].(map[string]interface{}); ok {
		if enabled, ok := tls["enabled"].(bool); ok && enabled {
			vmessJson["tls"] = "tls"
			if sni, ok := tls["server_name"].(string); ok {
				vmessJson["sni"] = sni
			}
			if alpn, ok := tls["alpn"].([]interface{}); ok {
				alpnStrs := make([]string, len(alpn))
				for i, a := range alpn {
					alpnStrs[i], _ = a.(string)
				}
				vmessJson["alpn"] = strings.Join(alpnStrs, ",")
			}
			if utls, ok := tls["utls"].(map[string]interface{}); ok {
				if fp, ok := utls["fingerprint"].(string); ok {
					vmessJson["fp"] = fp
				}
			}
		}
	}
	
	jsonBytes, err := json.Marshal(vmessJson)
	if err != nil {
		return "", err
	}
	
	encoded := base64.StdEncoding.EncodeToString(jsonBytes)
	return "vmess://" + encoded, nil
}

func vlessToLink(out map[string]interface{}) (string, error) {
	tag, _ := out["tag"].(string)
	server, _ := out["server"].(string)
	port := getPort(out["server_port"])
	uuid, _ := out["uuid"].(string)
	flow, _ := out["flow"].(string)
	
	query := url.Values{}
	query.Set("type", "tcp")
	
	if flow != "" {
		query.Set("flow", flow)
	}
	
	// Handle transport
	if transport, ok := out["transport"].(map[string]interface{}); ok {
		tpType, _ := transport["type"].(string)
		query.Set("type", tpType)
		if path, ok := transport["path"].(string); ok {
			query.Set("path", path)
		}
		if host, ok := transport["host"].(string); ok {
			query.Set("host", host)
		}
		if headers, ok := transport["headers"].(map[string]interface{}); ok {
			if host, ok := headers["Host"].(string); ok {
				query.Set("host", host)
			}
		}
		if serviceName, ok := transport["service_name"].(string); ok {
			query.Set("serviceName", serviceName)
		}
	}
	
	// Handle TLS
	if tls, ok := out["tls"].(map[string]interface{}); ok {
		if enabled, ok := tls["enabled"].(bool); ok && enabled {
			// Check for reality
			if reality, ok := tls["reality"].(map[string]interface{}); ok {
				if realityEnabled, ok := reality["enabled"].(bool); ok && realityEnabled {
					query.Set("security", "reality")
					if pbk, ok := reality["public_key"].(string); ok {
						query.Set("pbk", pbk)
					}
					if sid, ok := reality["short_id"].(string); ok {
						query.Set("sid", sid)
					}
				}
			} else {
				query.Set("security", "tls")
			}
			if sni, ok := tls["server_name"].(string); ok {
				query.Set("sni", sni)
			}
			if alpn, ok := tls["alpn"].([]interface{}); ok {
				alpnStrs := make([]string, len(alpn))
				for i, a := range alpn {
					alpnStrs[i], _ = a.(string)
				}
				query.Set("alpn", strings.Join(alpnStrs, ","))
			}
			if utls, ok := tls["utls"].(map[string]interface{}); ok {
				if fp, ok := utls["fingerprint"].(string); ok {
					query.Set("fp", fp)
				}
			}
		}
	}
	
	return fmt.Sprintf("vless://%s@%s:%d?%s#%s", uuid, server, port, query.Encode(), url.PathEscape(tag)), nil
}

func trojanToLink(out map[string]interface{}) (string, error) {
	tag, _ := out["tag"].(string)
	server, _ := out["server"].(string)
	port := getPort(out["server_port"])
	password, _ := out["password"].(string)
	
	query := url.Values{}
	query.Set("type", "tcp")
	
	// Handle transport
	if transport, ok := out["transport"].(map[string]interface{}); ok {
		tpType, _ := transport["type"].(string)
		query.Set("type", tpType)
		if path, ok := transport["path"].(string); ok {
			query.Set("path", path)
		}
		if host, ok := transport["host"].(string); ok {
			query.Set("host", host)
		}
		if serviceName, ok := transport["service_name"].(string); ok {
			query.Set("serviceName", serviceName)
		}
	}
	
	// Handle TLS
	if tls, ok := out["tls"].(map[string]interface{}); ok {
		if enabled, ok := tls["enabled"].(bool); ok && enabled {
			query.Set("security", "tls")
			if sni, ok := tls["server_name"].(string); ok {
				query.Set("sni", sni)
			}
			if alpn, ok := tls["alpn"].([]interface{}); ok {
				alpnStrs := make([]string, len(alpn))
				for i, a := range alpn {
					alpnStrs[i], _ = a.(string)
				}
				query.Set("alpn", strings.Join(alpnStrs, ","))
			}
		}
	}
	
	return fmt.Sprintf("trojan://%s@%s:%d?%s#%s", password, server, port, query.Encode(), url.PathEscape(tag)), nil
}

func ssToLink(out map[string]interface{}) (string, error) {
	tag, _ := out["tag"].(string)
	server, _ := out["server"].(string)
	port := getPort(out["server_port"])
	method, _ := out["method"].(string)
	password, _ := out["password"].(string)
	
	// Encode method:password in base64
	userInfo := base64.URLEncoding.EncodeToString([]byte(method + ":" + password))
	
	return fmt.Sprintf("ss://%s@%s:%d#%s", userInfo, server, port, url.PathEscape(tag)), nil
}

func socksToLink(out map[string]interface{}) (string, error) {
	tag, _ := out["tag"].(string)
	server, _ := out["server"].(string)
	port := getPort(out["server_port"])
	username, _ := out["username"].(string)
	password, _ := out["password"].(string)
	
	if username != "" && password != "" {
		return fmt.Sprintf("socks5://%s:%s@%s:%d#%s", url.PathEscape(username), url.PathEscape(password), server, port, url.PathEscape(tag)), nil
	}
	return fmt.Sprintf("socks5://%s:%d#%s", server, port, url.PathEscape(tag)), nil
}

func hy2ToLink(out map[string]interface{}) (string, error) {
	tag, _ := out["tag"].(string)
	server, _ := out["server"].(string)
	port := getPort(out["server_port"])
	password, _ := out["password"].(string)
	
	query := url.Values{}
	
	// Handle TLS
	if tls, ok := out["tls"].(map[string]interface{}); ok {
		if sni, ok := tls["server_name"].(string); ok {
			query.Set("sni", sni)
		}
		if insecure, ok := tls["insecure"].(bool); ok && insecure {
			query.Set("insecure", "1")
		}
		if alpn, ok := tls["alpn"].([]interface{}); ok && len(alpn) > 0 {
			alpnStrs := make([]string, len(alpn))
			for i, a := range alpn {
				alpnStrs[i], _ = a.(string)
			}
			query.Set("alpn", strings.Join(alpnStrs, ","))
		}
	}
	
	// Handle obfs
	if obfs, ok := out["obfs"].(map[string]interface{}); ok {
		if obfsType, ok := obfs["type"].(string); ok {
			query.Set("obfs", obfsType)
			if obfsPassword, ok := obfs["password"].(string); ok {
				query.Set("obfs-password", obfsPassword)
			}
		}
	}
	
	queryStr := ""
	if len(query) > 0 {
		queryStr = "?" + query.Encode()
	}
	
	return fmt.Sprintf("hy2://%s@%s:%d%s#%s", password, server, port, queryStr, url.PathEscape(tag)), nil
}

func hyToLink(out map[string]interface{}) (string, error) {
	tag, _ := out["tag"].(string)
	server, _ := out["server"].(string)
	port := getPort(out["server_port"])
	authStr, _ := out["auth_str"].(string)
	obfsParam, _ := out["obfs"].(string)
	
	query := url.Values{}
	
	if authStr != "" {
		query.Set("auth", authStr)
	}
	if obfsParam != "" {
		query.Set("obfsParam", obfsParam)
	}
	
	// Handle TLS
	if tls, ok := out["tls"].(map[string]interface{}); ok {
		if sni, ok := tls["server_name"].(string); ok {
			query.Set("peer", sni)
		}
		if insecure, ok := tls["insecure"].(bool); ok && insecure {
			query.Set("insecure", "1")
		}
		if alpn, ok := tls["alpn"].([]interface{}); ok && len(alpn) > 0 {
			alpnStrs := make([]string, len(alpn))
			for i, a := range alpn {
				alpnStrs[i], _ = a.(string)
			}
			query.Set("alpn", strings.Join(alpnStrs, ","))
		}
	}
	
	if downMbps, ok := out["down_mbps"].(float64); ok && downMbps > 0 {
		query.Set("downmbps", fmt.Sprintf("%.0f", downMbps))
	}
	if upMbps, ok := out["up_mbps"].(float64); ok && upMbps > 0 {
		query.Set("upmbps", fmt.Sprintf("%.0f", upMbps))
	}
	
	return fmt.Sprintf("hysteria://%s:%d?%s#%s", server, port, query.Encode(), url.PathEscape(tag)), nil
}

func tuicToLink(out map[string]interface{}) (string, error) {
	tag, _ := out["tag"].(string)
	server, _ := out["server"].(string)
	port := getPort(out["server_port"])
	uuid, _ := out["uuid"].(string)
	password, _ := out["password"].(string)
	
	query := url.Values{}
	
	if cc, ok := out["congestion_control"].(string); ok && cc != "" {
		query.Set("congestion_control", cc)
	}
	if udpMode, ok := out["udp_relay_mode"].(string); ok && udpMode != "" {
		query.Set("udp_relay_mode", udpMode)
	}
	
	// Handle TLS
	if tls, ok := out["tls"].(map[string]interface{}); ok {
		if sni, ok := tls["server_name"].(string); ok {
			query.Set("sni", sni)
		}
		if insecure, ok := tls["insecure"].(bool); ok && insecure {
			query.Set("allow_insecure", "1")
		}
		if alpn, ok := tls["alpn"].([]interface{}); ok && len(alpn) > 0 {
			alpnStrs := make([]string, len(alpn))
			for i, a := range alpn {
				alpnStrs[i], _ = a.(string)
			}
			query.Set("alpn", strings.Join(alpnStrs, ","))
		}
	}
	
	return fmt.Sprintf("tuic://%s:%s@%s:%d?%s#%s", uuid, password, server, port, query.Encode(), url.PathEscape(tag)), nil
}

func anytlsToLink(out map[string]interface{}) (string, error) {
	tag, _ := out["tag"].(string)
	server, _ := out["server"].(string)
	port := getPort(out["server_port"])
	password, _ := out["password"].(string)
	
	query := url.Values{}
	
	// Handle TLS
	if tls, ok := out["tls"].(map[string]interface{}); ok {
		if sni, ok := tls["server_name"].(string); ok {
			query.Set("sni", sni)
		}
		if insecure, ok := tls["insecure"].(bool); ok && insecure {
			query.Set("insecure", "1")
		}
		if alpn, ok := tls["alpn"].([]interface{}); ok && len(alpn) > 0 {
			alpnStrs := make([]string, len(alpn))
			for i, a := range alpn {
				alpnStrs[i], _ = a.(string)
			}
			query.Set("alpn", strings.Join(alpnStrs, ","))
		}
	}
	
	queryStr := ""
	if len(query) > 0 {
		queryStr = "?" + query.Encode()
	}
	
	return fmt.Sprintf("anytls://%s@%s:%d%s#%s", password, server, port, queryStr, url.PathEscape(tag)), nil
}

func getPort(port interface{}) int {
	switch v := port.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		p, _ := v.Int64()
		return int(p)
	default:
		return 0
	}
}
