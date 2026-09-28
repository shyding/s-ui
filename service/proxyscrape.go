package service

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/alireza0/s-ui/util"
)

type proxyScrapeRecord struct {
	Protocol      string  `json:"protocol"`
	IP            string  `json:"ip"`
	Port          int     `json:"port"`
	CountryCode   string  `json:"country_code"`
	City          string  `json:"city"`
	Anonymity     string  `json:"anonymity"`
	UptimePercent float64 `json:"uptime_percent"`
	LatencyMS     float64 `json:"latency_ms"`
}

func parseProxyScrapeCandidates(content, subscriptionName string) (*util.SubscriptionResult, bool, error) {
	var records []proxyScrapeRecord
	if err := json.Unmarshal([]byte(content), &records); err != nil {
		return nil, false, nil
	}
	if len(records) == 0 || records[0].Protocol == "" || records[0].IP == "" {
		return nil, false, nil
	}

	valid := make([]proxyScrapeRecord, 0, len(records))
	for _, record := range records {
		if net.ParseIP(record.IP) == nil || record.Port < 1 || record.Port > 65535 {
			continue
		}
		if !strings.EqualFold(record.Anonymity, "anonymous") && !strings.EqualFold(record.Anonymity, "elite") {
			continue
		}
		if record.UptimePercent < 50 || record.LatencyMS < 0 || record.LatencyMS > 2500 {
			continue
		}
		if proxyScrapeOutboundType(record.Protocol) == "" {
			continue
		}
		valid = append(valid, record)
	}

	sort.SliceStable(valid, func(left, right int) bool {
		if valid[left].UptimePercent != valid[right].UptimePercent {
			return valid[left].UptimePercent > valid[right].UptimePercent
		}
		return valid[left].LatencyMS < valid[right].LatencyMS
	})
	if len(valid) > hproxyCandidateLimit {
		valid = valid[:hproxyCandidateLimit]
	}

	result := &util.SubscriptionResult{Format: "proxyscrape-json"}
	seen := make(map[string]struct{}, len(valid))
	for _, record := range valid {
		key := fmt.Sprintf("%s:%d", record.IP, record.Port)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		country := strings.ToUpper(strings.TrimSpace(record.CountryCode))
		city := strings.TrimSpace(record.City)
		if len(country) != 2 || city == "" {
			continue
		}
		tag := fmt.Sprintf("hproxy-%s-%s-ps-%s-%d", strings.ToLower(country), SanitizeTag(city), strings.ReplaceAll(record.IP, ".", "-"), record.Port)
		result.Outbounds = append(result.Outbounds, map[string]interface{}{
			"type":        proxyScrapeOutboundType(record.Protocol),
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

func proxyScrapeOutboundType(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "socks5":
		return "socks"
	case "http", "https":
		return "http"
	default:
		return ""
	}
}
