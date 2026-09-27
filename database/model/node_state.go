package model

import (
	"fmt"
	"strings"
	"time"
)

// NodeHealthStatus encapsulates the two-stage health status, provenance, and performance metrics of a proxy node
type NodeHealthStatus struct {
	Node          string  `json:"node" gorm:"primaryKey"` // Unique identifier or outbound tag
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
}

// GroupKey returns provider + country + region + city for TOP3 aggregation
func (n *NodeHealthStatus) GroupKey() string {
	prov := strings.TrimSpace(n.Provider)
	if prov == "" {
		prov = "SUI"
	}
	country := strings.TrimSpace(n.Country)
	if country == "" {
		country = "未知"
	}
	region := strings.TrimSpace(n.Region)
	if region == "" {
		region = "未知"
	}
	city := strings.TrimSpace(n.City)
	if city == "" {
		city = "未知"
	}
	return fmt.Sprintf("%s-%s-%s-%s", prov, country, region, city)
}

// StandardRemark formats remark as: 来源-国家-区域-城市-编号
func (n *NodeHealthStatus) StandardRemark(index int) string {
	return fmt.Sprintf("%s-%02d", n.GroupKey(), index)
}

// IsHealthy enforces the strict state rule:
// 只有：tcp_check=true AND tls_check=true AND proxy_check=true 才允许进入订阅
func (n *NodeHealthStatus) IsHealthy() bool {
	return n.TCPCheck && n.TLSCheck && n.ProxyCheck && n.Status == "available" && n.Latency > 0
}

// SetCheckedAtNow updates LastCheckTime to current UTC timestamp
func (n *NodeHealthStatus) SetCheckedAtNow() {
	n.LastCheckTime = time.Now().UTC().Format(time.RFC3339)
}
