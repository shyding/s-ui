package model

import (
	"fmt"
	"strings"
	"time"
)

// NodeHealthStatus encapsulates the two-stage health status, provenance, and performance metrics of a proxy node
type NodeHealthStatus struct {
	Node          string  `json:"node" gorm:"primaryKey"` // Unique identifier: host:port
	OriginalURI   string  `json:"original_uri"`           // 完整原始 URI（供订阅直接发布）
	Provider      string  `json:"provider"`               // 来源: e.g. AWS, Azure, Cloudflare, S-UI, etc.
	Country       string  `json:"country"`                // 国家: e.g. 日本, 美国, 新加坡, 德国, 荷兰
	Region        string  `json:"region"`                 // 区域: e.g. 关东, 加州, 中央区, 黑森, 北荷兰
	City          string  `json:"city"`                   // 城市: e.g. 东京, 洛杉矶, 新加坡城, 法兰克福, 阿姆斯特丹
	TCPCheck      bool    `json:"tcp_check"`              // TCP connection check passed
	TLSCheck      bool    `json:"tls_check"`              // TLS handshake check passed
	ProxyCheck    bool    `json:"proxy_check"`            // HTTP proxy generate_204 check passed
	Latency       int64   `json:"latency"`                // Ping/handshake latency in ms
	Speed         float64 `json:"speed"`                  // Measured speed in Mbps or score
	Status        string  `json:"status"`                 // "available" or "unavailable"
	LastCheckTime string  `json:"last_check_time"`        // Timestamp of last check
	LastError     string  `json:"last_error"`             // Reason for failure e.g. WIREGUARD_HANDSHAKE_TIMEOUT
}

// DefaultHealthTTL defines maximum allowed age for a health check before it is considered STALE.
// Set to 1h to match frequent background checks.
const DefaultHealthTTL = 1 * time.Hour

// MaxSubscriptionLatency is the maximum latency (in ms) on VPS for a node to be published
// in subscriptions. With client-to-VPS overhead (100-150ms), this guarantees end-to-end <= 650ms.
const MaxSubscriptionLatency int64 = 500

// GroupKey returns provider + country + region + city
func (n *NodeHealthStatus) GroupKey() string {
	prov := strings.TrimSpace(n.Provider)
	if prov == "" {
		prov = "SUI"
	}
	clean := func(s, fallback string) string {
		s = strings.TrimSpace(s)
		for _, b := range []string{"未知地区", "未知城市", "未知", "unknown", "unknow", "null", "none"} {
			s = strings.ReplaceAll(s, b, "")
		}
		s = strings.ReplaceAll(s, "-", "")
		s = strings.TrimSpace(s)
		if s == "" {
			return fallback
		}
		return s
	}
	country := clean(n.Country, "全球")
	region := clean(n.Region, "亚太")
	city := clean(n.City, "新加坡城")
	return fmt.Sprintf("%s-%s-%s-%s", prov, country, region, city)
}

// StandardRemark formats remark as: 来源-国家-区域-城市-编号
func (n *NodeHealthStatus) StandardRemark(index int) string {
	return fmt.Sprintf("%s-%02d", n.GroupKey(), index)
}

// IsHealthyWithTTL enforces strict FAIL-CLOSED verification:
// 1. n != nil
// 2. Status == "available"
// 3. TCPCheck == true
// 4. TLSCheck == true
// 5. ProxyCheck == true
// 6. Latency > 0 AND Latency <= MaxSubscriptionLatency (650ms)
// 7. Speed > 0
// 8. LastCheckTime is within TTL
func (n *NodeHealthStatus) IsHealthyWithTTL(ttl time.Duration) bool {
	if n == nil {
		return false
	}
	if !n.TCPCheck || !n.TLSCheck || !n.ProxyCheck || n.Status != "available" || n.Latency <= 0 || n.Speed <= 0 {
		return false
	}
	// Enforce latency cap: nodes slower than 650ms are not published
	if n.Latency > MaxSubscriptionLatency {
		return false
	}
	if ttl <= 0 {
		ttl = DefaultHealthTTL
	}
	if strings.TrimSpace(n.LastCheckTime) == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, n.LastCheckTime)
	if err != nil {
		return false
	}
	return time.Since(t) <= ttl
}

// IsHealthy checks if the node health status satisfies all requirements within DefaultHealthTTL
func (n *NodeHealthStatus) IsHealthy() bool {
	return n.IsHealthyWithTTL(DefaultHealthTTL)
}

// SetCheckedAtNow updates LastCheckTime to current UTC timestamp
func (n *NodeHealthStatus) SetCheckedAtNow() {
	n.LastCheckTime = time.Now().UTC().Format(time.RFC3339)
}
