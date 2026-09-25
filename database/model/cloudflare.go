package model

// CloudflareEndpoint stores probed Cloudflare Anycast/WARP egress endpoints dynamically
type CloudflareEndpoint struct {
	Id          uint   `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	IP          string `json:"ip" form:"ip" gorm:"index;uniqueIndex:idx_cf_ip_port"`
	Port        int    `json:"port" form:"port" gorm:"uniqueIndex:idx_cf_ip_port"`
	Loc         string `json:"loc" form:"loc" gorm:"index"`         // ISO 3166-1 alpha-2, e.g. "US", "JP", "SG", "HK", "GB", "DE"
	Colo        string `json:"colo" form:"colo"`                 // Cloudflare 3-letter IATA code, e.g. "LAX", "NRT", "SIN", "LHR"
	CountryName string `json:"countryName" form:"countryName"`  // e.g. "美国", "日本", "新加坡", "英国"
	Flag        string `json:"flag" form:"flag"`                 // e.g. "🇺🇸", "🇯🇵", "🇸🇬", "🇬🇧"
	LatencyMs   int64  `json:"latencyMs" form:"latencyMs"`        // RTT latency in milliseconds
	Status      string `json:"status" form:"status" gorm:"index"` // "online" / "offline"
	EndpointTag string `json:"endpointTag" form:"endpointTag" gorm:"index"` // e.g. "cf-ep-us-1"
	LastChecked int64  `json:"lastChecked" form:"lastChecked"`    // Unix timestamp
}
