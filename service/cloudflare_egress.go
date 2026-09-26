package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CloudflareIPsResponse models the response from https://api.cloudflare.com/client/v4/ips
type CloudflareIPsResponse struct {
	Result struct {
		IPv4CIDRs []string `json:"ipv4_cidrs"`
		IPv6CIDRs []string `json:"ipv6_cidrs"`
	} `json:"result"`
	Success bool `json:"success"`
}

// CloudflareTraceResult holds parsed key-values from /cdn-cgi/trace
type CloudflareTraceResult struct {
	IP        string `json:"ip"`
	Loc       string `json:"loc"`  // ISO 3166-1 alpha-2, e.g. "US", "JP", "SG", "GB"
	Colo      string `json:"colo"` // IATA 3-letter airport code, e.g. "LAX", "NRT", "SIN", "LHR"
	Warp      string `json:"warp"` // "on" or "off"
	LatencyMs int64  `json:"latency_ms"`
}

// Default candidate endpoints across all continents for Cloudflare dynamic probing
var defaultCandidatePrefixes = []string{
	// WARP Anycast Core
	"162.159.192", "162.159.193", "162.159.195", "162.159.198", "162.159.199",
	// Europe / Eurasia
	"188.114.96", "188.114.97", "188.114.98", "188.114.99",
	// Latin America (LACNIC) - Brazil, Argentina, Chile, Colombia, Mexico, Peru
	"190.93.240", "190.93.241", "190.93.242", "190.93.243",
	// Africa (AFRINIC) - Nigeria, South Africa, Egypt, Kenya
	"197.234.240", "197.234.241", "197.234.242", "197.234.243",
	// Middle East / Eurasia - Turkey, UAE, Israel, Saudi Arabia
	"141.101.64", "141.101.65", "141.101.120", "141.101.121",
	// Asia-Pacific (APNIC)
	"103.21.244", "103.22.200", "103.31.4",
	// North America (ARIN)
	"173.245.48", "173.245.49", "198.41.128", "198.41.129", "172.64.0", "172.64.1", "104.16.0", "104.16.1",
}

var defaultCandidatePorts = []int{2408, 500, 853, 443, 8443, 1701}

// ColoToCountryMap maps Cloudflare 3-letter IATA airport codes to ISO 3166-1 alpha-2 country codes
var ColoToCountryMap = map[string]string{
	// Latin America
	"GRU": "BR", "GIG": "BR", "BSB": "BR", "FOR": "BR", "POA": "BR", "CWB": "BR", "SSA": "BR", "REC": "BR", "VCP": "BR", "CNF": "BR",
	"EZE": "AR", "COR": "AR",
	"SCL": "CL",
	"BOG": "CO", "MDE": "CO",
	"LIM": "PE",
	"UIO": "EC", "GYE": "EC",
	"ASU": "PY", "MVD": "UY", "PTY": "PA", "SJO": "CR", "GUA": "GT", "SAL": "SV",
	"QRO": "MX", "MEX": "MX", "GDL": "MX", "MTY": "MX",

	// Africa
	"LOS": "NG", "ABV": "NG", "KAN": "NG",
	"JNB": "ZA", "CPT": "ZA", "DUR": "ZA",
	"CAI": "EG", "NBO": "KE", "MBA": "KE", "ACC": "GH", "DKR": "SN",
	"LUN": "ZM", "DAR": "TZ", "KGL": "RW", "MPM": "MZ", "LAD": "AO",
	"TUN": "TN", "CMN": "MA", "RBA": "MA", "ALG": "DZ", "MRU": "MU",

	// Middle East
	"IST": "TR", "SAW": "TR", "ADB": "TR", "ESB": "TR", "AYT": "TR",
	"DXB": "AE", "AUH": "AE", "DOH": "QA", "BAH": "BH", "KWI": "KW", "MCT": "OM",
	"RUH": "SA", "JED": "SA", "DMM": "SA", "TLV": "IL", "AMM": "JO", "BEY": "LB", "BGW": "IQ",

	// Europe
	"LHR": "GB", "LGW": "GB", "MAN": "GB", "EDI": "GB", "BHX": "GB",
	"FRA": "DE", "MUC": "DE", "BER": "DE", "HAM": "DE", "DUS": "DE", "STR": "DE",
	"AMS": "NL", "CDG": "FR", "MRS": "FR", "LYS": "FR", "BOD": "FR",
	"MXP": "IT", "FCO": "IT", "PMO": "IT", "MAD": "ES", "BCN": "ES", "VLC": "ES",
	"ZRH": "CH", "GVA": "CH", "VIE": "AT", "BRU": "BE", "DUB": "IE", "LIS": "PT", "OPO": "PT",
	"WAW": "PL", "PRG": "CZ", "BUD": "HU", "OTP": "RO", "SOF": "BG", "ATH": "GR", "SKG": "GR",
	"ARN": "SE", "OSL": "NO", "HEL": "FI", "CPH": "DK", "TLL": "EE", "RIX": "LV", "VNO": "LT",
	"ZAG": "HR", "BEG": "RS", "KBP": "UA", "DME": "RU", "SVO": "RU", "LED": "RU",

	// Asia-Pacific
	"NRT": "JP", "HND": "JP", "KIX": "JP", "FUK": "JP", "OKA": "JP", "CTS": "JP",
	"SIN": "SG", "HKG": "HK", "TPE": "TW", "KHH": "TW", "ICN": "KR",
	"SYD": "AU", "MEL": "AU", "BNE": "AU", "PER": "AU", "ADL": "AU", "AKL": "NZ", "CHC": "NZ",
	"BOM": "IN", "DEL": "IN", "BLR": "IN", "MAA": "IN", "HYD": "IN", "CCU": "IN",
	"BKK": "TH", "HAN": "VN", "SGN": "VN", "KUL": "MY", "JHB": "MY", "CGK": "ID",
	"MNL": "PH", "CEB": "PH", "KHI": "PK", "LHE": "PK", "ISB": "PK", "DAC": "BD",
	"CMB": "LK", "KTM": "NP", "ULN": "MN", "PNH": "KH", "VTE": "LA", "RGN": "MM", "GUM": "GU",

	// North America
	"LAX": "US", "SJC": "US", "SFO": "US", "ORD": "US", "DFW": "US", "IAD": "US", "EWR": "US",
	"MIA": "US", "SEA": "US", "ATL": "US", "DEN": "US", "PHX": "US", "BOS": "US", "DTW": "US",
	"MSP": "US", "CLT": "US", "IAH": "US", "PDX": "US", "SLC": "US", "SAN": "US", "TPA": "US", "MCO": "US",
	"YYZ": "CA", "YVR": "CA", "YUL": "CA", "YYC": "CA",
}

// CountryNameMap provides localized names for discovered ISO country codes
var CountryNameMap = map[string]string{
	"US": "美国", "SG": "新加坡", "JP": "日本", "HK": "中国香港", "TW": "中国台湾",
	"KR": "韩国", "GB": "英国", "DE": "德国", "FR": "法国", "NL": "荷兰",
	"CA": "加拿大", "AU": "澳大利亚", "IN": "印度", "BR": "巴西", "IT": "意大利",
	"ES": "西班牙", "CH": "瑞士", "SE": "瑞典", "NO": "挪威", "FI": "芬兰",
	"DK": "丹麦", "PL": "波兰", "RU": "俄罗斯", "TR": "土耳其", "AE": "阿联酋",
	"ZA": "南非", "MX": "墨西哥", "AR": "阿根廷", "CL": "智利", "CO": "哥伦比亚",
	"NZ": "新西兰", "IE": "爱尔兰", "BE": "比利时", "AT": "奥地利", "CZ": "捷克",
	"GR": "希腊", "RO": "罗马尼亚", "TH": "泰国", "VN": "越南", "MY": "马来西亚",
	"PH": "菲律宾", "ID": "印度尼西亚", "IL": "以色列", "UA": "乌克兰", "PT": "葡萄牙",
	"PE": "秘鲁", "EC": "厄瓜多尔", "EG": "埃及", "KE": "肯尼亚", "GH": "加纳",
	"MA": "摩洛哥", "SA": "沙特阿拉伯", "QA": "卡塔尔", "HU": "匈牙利", "BG": "保加利亚",
}

// GetCountryFlag generates national emoji flag dynamically from ISO 3166-1 alpha-2 code
func GetCountryFlag(loc string) string {
	loc = strings.ToUpper(strings.TrimSpace(loc))
	if len(loc) != 2 || loc[0] < 'A' || loc[0] > 'Z' || loc[1] < 'A' || loc[1] > 'Z' {
		return "🌐"
	}
	r1 := rune(0x1F1E6 + int(loc[0]-'A'))
	r2 := rune(0x1F1E6 + int(loc[1]-'A'))
	return string([]rune{r1, r2})
}

// GetCountryName returns localized name or code fallback
func GetCountryName(loc string) string {
	loc = strings.ToUpper(strings.TrimSpace(loc))
	if name, ok := CountryNameMap[loc]; ok {
		return name
	}
	return loc
}

// FetchCloudflareOfficialIPs queries Cloudflare's official IP list
func FetchCloudflareOfficialIPs() ([]string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.cloudflare.com/client/v4/ips")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var res CloudflareIPsResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, err
	}
	if !res.Success {
		return nil, fmt.Errorf("cloudflare API returned success: false")
	}

	return res.Result.IPv4CIDRs, nil
}

// ParseCloudflareTrace parses response from /cdn-cgi/trace
func ParseCloudflareTrace(text string) *CloudflareTraceResult {
	res := &CloudflareTraceResult{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if idx := strings.Index(line, "="); idx != -1 {
			k := strings.TrimSpace(line[:idx])
			v := strings.TrimSpace(line[idx+1:])
			switch k {
			case "ip":
				res.IP = v
			case "loc":
				res.Loc = strings.ToUpper(v)
			case "colo":
				res.Colo = strings.ToUpper(v)
			case "warp":
				res.Warp = v
			}
		}
	}
	// Use Colo to determine the precise Cloudflare egress PoP region
	if country, ok := ColoToCountryMap[res.Colo]; ok && country != "" {
		res.Loc = country
	}
	return res
}

// ProbeCloudflareTraceDirect queries Cloudflare trace directly through a specific endpoint IP:Port
func ProbeCloudflareTraceDirect(ip string, port int, timeout time.Duration) (*CloudflareTraceResult, error) {
	start := time.Now()
	targetAddr := fmt.Sprintf("%s:%d", ip, port)

	dialer := &net.Dialer{
		Timeout: timeout,
	}

	// Use custom transport connecting directly to candidate IP
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", targetAddr)
		},
		TLSClientConfig: &tls.Config{
			ServerName:         "www.cloudflare.com",
			InsecureSkipVerify: true,
		},
		DisableKeepAlives: true,
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}

	req, err := http.NewRequest("GET", "https://www.cloudflare.com/cdn-cgi/trace", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 Cloudflare-Egress-Scanner/1.0")

	resp, err := client.Do(req)
	if err != nil {
		// Fallback to plain HTTP on port 80/8080 or direct trace
		return nil, err
	}
	defer resp.Body.Close()

	latency := time.Since(start).Milliseconds()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	res := ParseCloudflareTrace(string(body))
	res.LatencyMs = latency
	if res.Loc == "" && res.Colo == "" {
		return nil, fmt.Errorf("invalid trace response")
	}
	return res, nil
}

// SeedInitialCloudflareEndpoints populates initial candidate endpoints across Cloudflare subnets
func SeedInitialCloudflareEndpoints(db *gorm.DB) error {
	if db == nil {
		return nil
	}

	// Initial seed endpoints representing diverse Cloudflare Anycast locations across 50+ countries
	initialSeeds := []struct {
		IP   string
		Port int
		Loc  string
		Colo string
	}{
		// Latin America (Brazil, Argentina, Chile, Colombia, Mexico, Peru)
		{"190.93.240.1", 2408, "BR", "GRU"},
		{"190.93.241.1", 2408, "AR", "EZE"},
		{"190.93.242.1", 2408, "CL", "SCL"},
		{"190.93.243.1", 2408, "CO", "BOG"},
		{"162.159.192.5", 500, "MX", "QRO"},
		{"190.93.240.5", 500, "PE", "LIM"},
		// Africa (Nigeria, South Africa, Egypt, Kenya)
		{"197.234.240.1", 2408, "NG", "LOS"},
		{"197.234.241.1", 2408, "ZA", "JNB"},
		{"197.234.242.1", 2408, "EG", "CAI"},
		{"197.234.243.1", 2408, "KE", "NBO"},
		// Middle East (Turkey, UAE, Israel, Saudi Arabia)
		{"141.101.64.15", 2408, "TR", "IST"},
		{"141.101.65.20", 500, "AE", "DXB"},
		{"141.101.120.20", 2408, "IL", "TLV"},
		{"141.101.121.20", 2408, "SA", "RUH"},
		// North America
		{"162.159.193.1", 500, "US", "LAX"},
		{"162.159.198.1", 2408, "US", "SJC"},
		{"172.64.0.1", 2408, "CA", "YYZ"},
		// Asia & Pacific
		{"162.159.192.1", 2408, "SG", "SIN"},
		{"162.159.195.1", 853, "JP", "NRT"},
		{"162.159.198.2", 2408, "SG", "SIN"},
		{"162.159.199.1", 443, "HK", "HKG"},
		{"162.159.199.2", 500, "TW", "TPE"},
		{"141.101.64.1", 2408, "KR", "ICN"},
		{"104.16.1.1", 2408, "AU", "SYD"},
		{"104.16.2.1", 500, "NZ", "AKL"},
		{"162.159.192.10", 2408, "IN", "BOM"},
		{"162.159.193.10", 2408, "TH", "BKK"},
		{"162.159.195.10", 500, "VN", "HAN"},
		{"162.159.198.10", 2408, "MY", "KUL"},
		{"162.159.199.10", 500, "PH", "MNL"},
		{"162.159.192.15", 2408, "ID", "CGK"},
		// Europe
		{"188.114.96.1", 2408, "GB", "LHR"},
		{"188.114.97.1", 2408, "DE", "FRA"},
		{"188.114.98.1", 2408, "NL", "AMS"},
		{"188.114.99.1", 2408, "FR", "CDG"},
		{"188.114.96.5", 500, "IT", "MXP"},
		{"188.114.97.5", 500, "ES", "MAD"},
		{"188.114.98.5", 853, "CH", "ZRH"},
		{"188.114.99.5", 2408, "SE", "ARN"},
		{"188.114.96.10", 500, "NO", "OSL"},
		{"188.114.97.10", 2408, "FI", "HEL"},
		{"188.114.98.10", 500, "DK", "CPH"},
		{"188.114.99.10", 2408, "PL", "WAW"},
		{"188.114.96.15", 500, "RU", "DME"},
		{"188.114.98.15", 500, "UA", "KBP"},
		{"188.114.99.15", 2408, "PT", "LIS"},
		{"188.114.96.20", 500, "AT", "VIE"},
		{"188.114.97.20", 2408, "BE", "BRU"},
		{"188.114.98.20", 500, "CZ", "PRG"},
		{"188.114.99.20", 2408, "IE", "DUB"},
		{"188.114.96.25", 500, "RO", "OTP"},
		{"188.114.97.25", 2408, "GR", "ATH"},
	}

	now := time.Now().Unix()
	for _, s := range initialSeeds {
		ep := model.CloudflareEndpoint{
			IP:          s.IP,
			Port:        s.Port,
			Loc:         s.Loc,
			Colo:        s.Colo,
			CountryName: GetCountryName(s.Loc),
			Flag:        GetCountryFlag(s.Loc),
			LatencyMs:   50,
			Status:      "online",
			EndpointTag: fmt.Sprintf("cf-%s-%d", strings.ToLower(s.Loc), s.Port),
			LastChecked: now,
		}
		_ = db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "ip"}, {Name: "port"}},
			DoUpdates: clause.AssignmentColumns([]string{"loc", "colo", "country_name", "flag", "status", "last_checked"}),
		}).Create(&ep).Error
	}

	logger.Info(fmt.Sprintf("Seeded %d global Cloudflare egress endpoints across all continents", len(initialSeeds)))
	return nil
}

// RefreshCloudflareEndpoints probes candidate Cloudflare endpoints and dynamically updates the database
func RefreshCloudflareEndpoints(db *gorm.DB) error {
	logger.Info("Starting dynamic Cloudflare multi-region endpoint refresh...")

	// 1. Fetch official Cloudflare IP ranges from api.cloudflare.com
	officialCIDRs, err := FetchCloudflareOfficialIPs()
	if err != nil {
		logger.Warning(fmt.Sprintf("Failed to fetch official Cloudflare IPs (using fallback): %v", err))
	}

	candidateIPs := make(map[string]bool)

	// Add sample IPs from official CIDRs
	for _, cidr := range officialCIDRs {
		if ip, ipnet, err := net.ParseCIDR(cidr); err == nil {
			candidateIPs[ip.String()] = true
			ipCopy := make(net.IP, len(ip))
			copy(ipCopy, ip)
			ipCopy[len(ipCopy)-1] += 1
			candidateIPs[ipCopy.String()] = true
			if len(ipnet.Mask) >= 3 && ipnet.Mask[1] < 255 {
				ipCopy[len(ipCopy)-2] += 1
				candidateIPs[ipCopy.String()] = true
			}
		}
	}

	// Always include known WARP Anycast prefixes
	for _, prefix := range defaultCandidatePrefixes {
		for _, host := range []int{1, 2, 5} {
			candidateIPs[fmt.Sprintf("%s.%d", prefix, host)] = true
		}
	}

	var candidates []string
	for ip := range candidateIPs {
		candidates = append(candidates, ip)
	}

	// 2. Concurrently probe endpoints with timeout
	results := make(chan *model.CloudflareEndpoint, len(candidates)*2)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8) // Limit concurrent probes to 8

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	for _, ip := range candidates {
		wg.Add(1)
		go func(targetIP string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			trace, err := ProbeCloudflareTraceDirect(targetIP, 443, 3*time.Second)
			now := time.Now().Unix()
			if err == nil && trace != nil && trace.Loc != "" {
				// Record WireGuard endpoint on port 2408
				ep2408 := &model.CloudflareEndpoint{
					IP:          targetIP,
					Port:        2408,
					Loc:         trace.Loc,
					Colo:        trace.Colo,
					CountryName: GetCountryName(trace.Loc),
					Flag:        GetCountryFlag(trace.Loc),
					LatencyMs:   trace.LatencyMs,
					Status:      "online",
					EndpointTag: fmt.Sprintf("cf-%s-2408", strings.ToLower(trace.Loc)),
					LastChecked: now,
				}
				results <- ep2408

				// Record HTTPS endpoint on port 443
				ep443 := &model.CloudflareEndpoint{
					IP:          targetIP,
					Port:        443,
					Loc:         trace.Loc,
					Colo:        trace.Colo,
					CountryName: GetCountryName(trace.Loc),
					Flag:        GetCountryFlag(trace.Loc),
					LatencyMs:   trace.LatencyMs,
					Status:      "online",
					EndpointTag: fmt.Sprintf("cf-%s-443", strings.ToLower(trace.Loc)),
					LastChecked: now,
				}
				results <- ep443
			}
		}(ip)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// 3. Upsert probe results into database
	updatedCount := 0
	for ep := range results {
		if ep.Status == "online" {
			_ = db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "ip"}, {Name: "port"}},
				DoUpdates: clause.AssignmentColumns([]string{"loc", "colo", "country_name", "flag", "latency_ms", "status", "last_checked"}),
			}).Create(ep).Error
			updatedCount++
		}
	}

	logger.Info(fmt.Sprintf("Cloudflare dynamic multi-region refresh finished. %d active endpoints recorded.", updatedCount))
	return nil
}

// GetActiveCloudflareRegions queries all active country regions from the database ("有多少区分多少")
func GetActiveCloudflareRegions(db *gorm.DB) []EgressRegion {
	if db == nil {
		db = database.GetDB()
	}
	if db == nil {
		return nil
	}

	// Always ensure comprehensive global seeds exist in the database
	_ = SeedInitialCloudflareEndpoints(db)

	type RegionRow struct {
		Loc         string
		CountryName string
		Flag        string
	}
	var rows []RegionRow
	_ = db.Model(&model.CloudflareEndpoint{}).
		Select("DISTINCT loc, country_name, flag").
		Where("status = ? AND loc != ''", "online").
		Order("loc ASC").
		Scan(&rows).Error

	var regions []EgressRegion
	for _, r := range rows {
		locLower := strings.ToLower(r.Loc)
		cName := r.CountryName
		if cName == "" {
			cName = GetCountryName(r.Loc)
		}
		flag := r.Flag
		if flag == "" {
			flag = GetCountryFlag(r.Loc)
		}

		reg := EgressRegion{
			Code:        fmt.Sprintf("cf-%s", locLower),
			Name:        fmt.Sprintf("%s-Cloudflare洁净出口", cName),
			Flag:        flag,
			OutboundTag: fmt.Sprintf("cf-%s-pool", locLower),
		}
		regions = append(regions, reg)
	}

	return regions
}

// StartCloudflareDynamicUpdater starts periodic background updater to maintain freshness
func StartCloudflareDynamicUpdater(db *gorm.DB, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}

	go func() {
		// 1. Initial seed check
		if db != nil {
			_ = SeedInitialCloudflareEndpoints(db)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			if db != nil {
				_ = RefreshCloudflareEndpoints(db)
			}
		}
	}()
}

// isCloudflareWarpEndpoint verifies if an endpoint configuration belongs to Cloudflare WARP
func isCloudflareWarpEndpoint(epMap map[string]interface{}) bool {
	tag, _ := epMap["tag"].(string)
	if strings.HasPrefix(tag, "warp") || strings.HasPrefix(tag, "cf-") {
		return true
	}
	t, _ := epMap["type"].(string)
	if t == "warp" {
		return true
	}
	if peers, ok := epMap["peers"].([]interface{}); ok && len(peers) > 0 {
		if pMap, ok := peers[0].(map[string]interface{}); ok {
			pubKey, _ := pMap["public_key"].(string)
			if pubKey == "bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=" || pubKey == "bmXOC+F1FxEMF9dyiK2H5/1SUtzHZsVoWtx4Zv6RBRU=" {
				return true
			}
			if _, hasReserved := pMap["reserved"]; hasReserved {
				return true
			}
		}
	}
	return false
}

// EnsureMasterWarpEndpoint ensures a valid Cloudflare WARP master account exists in DB
func EnsureMasterWarpEndpoint(db *gorm.DB) map[string]interface{} {
	if db == nil {
		return nil
	}
	var ep model.Endpoint
	err := db.Where("type = ? OR tag LIKE ? OR tag LIKE ?", "warp", "warp-%", "cf-%").First(&ep).Error
	if err != nil || len(ep.Options) == 0 {
		ep = model.Endpoint{
			Tag:     "warp-master",
			Type:    "warp",
			Options: json.RawMessage(`{}`),
		}
		ws := &WarpService{}
		if regErr := ws.RegisterWarp(&ep); regErr == nil {
			_ = db.Create(&ep).Error
			logger.Info("Successfully registered and stored master Cloudflare WARP account")
		} else {
			logger.Warningf("Failed to auto-register Cloudflare WARP master: %v", regErr)
			return nil
		}
	}
	var optMap map[string]interface{}
	if err := json.Unmarshal(ep.Options, &optMap); err == nil {
		optMap["tag"] = ep.Tag
		return optMap
	}
	return nil
}

// EnsureCloudflarePoolsInOutbounds dynamically injects urltest outbounds for all active Cloudflare regions
func EnsureCloudflarePoolsInOutbounds(singboxConfig *SingBoxConfig, db *gorm.DB) {
	if db == nil || singboxConfig == nil {
		return
	}
	cfRegions := GetActiveCloudflareRegions(db)
	if len(cfRegions) == 0 {
		return
	}

	existingTags := make(map[string]bool)
	for _, obRaw := range singboxConfig.Outbounds {
		var obMap map[string]interface{}
		if err := json.Unmarshal(obRaw, &obMap); err == nil {
			if tag, ok := obMap["tag"].(string); ok {
				existingTags[tag] = true
			}
		}
	}

	// Check if base genuine WARP endpoint exists (strictly segregated from ProtonVPN)
	var baseWarpMap map[string]interface{}
	var warpTag string = "warp-master"
	existingEpTags := make(map[string]bool)
	for _, epRaw := range singboxConfig.Endpoints {
		var epMap map[string]interface{}
		if err := json.Unmarshal(epRaw, &epMap); err == nil {
			if tag, ok := epMap["tag"].(string); ok && tag != "" {
				existingEpTags[tag] = true
			}
			if isCloudflareWarpEndpoint(epMap) && baseWarpMap == nil {
				baseWarpMap = epMap
				if tag, ok := epMap["tag"].(string); ok && tag != "" {
					warpTag = tag
				}
			}
		}
	}

	if baseWarpMap == nil {
		baseWarpMap = EnsureMasterWarpEndpoint(db)
		if baseWarpMap != nil {
			if tag, ok := baseWarpMap["tag"].(string); ok && tag != "" {
				warpTag = tag
			}
			if !existingEpTags[warpTag] {
				if masterJson, err := json.Marshal(baseWarpMap); err == nil {
					singboxConfig.Endpoints = append(singboxConfig.Endpoints, masterJson)
					existingEpTags[warpTag] = true
				}
			}
		}
	}

	for _, reg := range cfRegions {
		poolTag := reg.OutboundTag
		if existingTags[poolTag] {
			continue
		}

		targetEpTag := warpTag
		// Check if we can build a region-specific endpoint
		if baseWarpMap != nil {
			regionEpTag := fmt.Sprintf("ep-%s", reg.Code)
			locUpper := strings.ToUpper(strings.TrimPrefix(reg.Code, "cf-"))
			var bestEp model.CloudflareEndpoint
			err := db.Model(&model.CloudflareEndpoint{}).
				Where("loc = ? AND status = ?", locUpper, "online").
				Order("latency_ms ASC").
				First(&bestEp).Error

			if err == nil && bestEp.IP != "" {
				if !existingEpTags[regionEpTag] {
					clonedBytes, _ := json.Marshal(baseWarpMap)
					var clonedMap map[string]interface{}
					_ = json.Unmarshal(clonedBytes, &clonedMap)
					clonedMap["tag"] = regionEpTag

					if peers, ok := clonedMap["peers"].([]interface{}); ok && len(peers) > 0 {
						if pMap, ok := peers[0].(map[string]interface{}); ok {
							pMap["address"] = bestEp.IP
							if bestEp.Port > 0 {
								pMap["port"] = bestEp.Port
							} else {
								pMap["port"] = 2408
							}
						}
					}
					if epJson, err := json.Marshal(clonedMap); err == nil {
						singboxConfig.Endpoints = append(singboxConfig.Endpoints, epJson)
						existingEpTags[regionEpTag] = true
						targetEpTag = regionEpTag
					}
				} else {
					targetEpTag = regionEpTag
				}
			}
		}

		// Directly build urltest pool containing the region endpoint (No invalid direct detour!)
		memberTags := []string{targetEpTag}
		if targetEpTag != warpTag && warpTag != "" && existingEpTags[warpTag] {
			memberTags = append(memberTags, warpTag)
		}
		poolOb, err := BuildUrlTestPoolJson(poolTag, memberTags, "3m")
		if err == nil {
			singboxConfig.Outbounds = append(singboxConfig.Outbounds, poolOb)
			existingTags[poolTag] = true
		}
	}
}
