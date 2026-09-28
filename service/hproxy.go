package service

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/alireza0/s-ui/util"
)

const hproxyCandidateLimit = 5000

type hproxyRecord struct {
	Proxy       string   `json:"proxy"`
	IP          string   `json:"ip"`
	Port        int      `json:"port"`
	Protocols   []string `json:"protocols"`
	Anonymity   string   `json:"anonymity"`
	Country     string   `json:"country"`
	City        string   `json:"city"`
	LatencyMS   int      `json:"latency_ms"`
	Uptime24H   float64  `json:"uptime_24h"`
	Uptime7D    float64  `json:"uptime_7d"`
	Reliability string   `json:"reliability"`
	Alive       bool     `json:"alive"`
}

func parseSubscriptionContent(content, subscriptionName string) (*util.SubscriptionResult, error) {
	if result, ok, err := parseHProxyCandidates(content, subscriptionName); ok || err != nil {
		return result, err
	}
	if result, ok, err := parseProxyScrapeCandidates(content, subscriptionName); ok || err != nil {
		return result, err
	}
	return util.ParseSubscription(content, subscriptionName)
}

func parseHProxyCandidates(content, subscriptionName string) (*util.SubscriptionResult, bool, error) {
	var records []hproxyRecord
	if err := json.Unmarshal([]byte(content), &records); err != nil {
		return nil, false, nil
	}
	if len(records) == 0 || records[0].Proxy == "" || len(records[0].Protocols) == 0 {
		return nil, false, nil
	}

	valid := make([]hproxyRecord, 0, len(records))
	for _, record := range records {
		if !record.Alive || net.ParseIP(record.IP) == nil || record.Port < 1 || record.Port > 65535 {
			continue
		}
		if record.Anonymity != "anonymous" && record.Anonymity != "elite" {
			continue
		}
		if record.Uptime24H < 50 || record.LatencyMS < 0 || record.LatencyMS > 2500 {
			continue
		}
		if hproxyOutboundType(record.Protocols) == "" {
			continue
		}
		valid = append(valid, record)
	}

	sort.SliceStable(valid, func(left, right int) bool {
		if valid[left].Uptime24H != valid[right].Uptime24H {
			return valid[left].Uptime24H > valid[right].Uptime24H
		}
		if valid[left].Uptime7D != valid[right].Uptime7D {
			return valid[left].Uptime7D > valid[right].Uptime7D
		}
		return valid[left].LatencyMS < valid[right].LatencyMS
	})
	if len(valid) > hproxyCandidateLimit {
		valid = valid[:hproxyCandidateLimit]
	}

	result := &util.SubscriptionResult{Format: "hproxy-json"}
	seen := make(map[string]struct{}, len(valid))
	for _, record := range valid {
		key := fmt.Sprintf("%s:%d", record.IP, record.Port)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		country := strings.ToUpper(strings.TrimSpace(record.Country))
		city := strings.TrimSpace(record.City)
		tag := fmt.Sprintf("hproxy-%s-%s-%s-%d", strings.ToLower(country), SanitizeTag(city), strings.ReplaceAll(record.IP, ".", "-"), record.Port)
		result.Outbounds = append(result.Outbounds, map[string]interface{}{
			"type":        hproxyOutboundType(record.Protocols),
			"tag":         tag,
			"server":      record.IP,
			"server_port": record.Port,
			"country":     country,
			"city":        city,
			"region":      city,
		})
	}
	return result, true, nil
}

func hproxyOutboundType(protocols []string) string {
	for _, protocol := range protocols {
		if strings.EqualFold(protocol, "socks5") {
			return "socks"
		}
	}
	for _, protocol := range protocols {
		if strings.EqualFold(protocol, "http") || strings.EqualFold(protocol, "https") {
			return "http"
		}
	}
	return ""
}
