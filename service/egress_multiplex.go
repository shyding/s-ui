package service

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/gofrs/uuid/v5"
	"gorm.io/gorm"
)

// EgressRegion defines a dynamic landing region (country egress)
type EgressRegion struct {
	Code        string `json:"code"`        // e.g. "us", "jp", "nl", "sg"
	Name        string `json:"name"`        // e.g. "美国", "日本", "荷兰", "新加坡"
	Flag        string `json:"flag"`        // e.g. "🇺🇸", "🇯🇵", "🇳🇱", "🇸🇬"
	OutboundTag string `json:"outboundTag"` // e.g. "us-pool", "jp-pool", "nl-pool", "warp-6eV"
}

// StandardEgressRegions provides the predefined zero-cost country egress definitions
var StandardEgressRegions = []EgressRegion{
	{Code: "sg", Name: "新加坡-WARP", Flag: "🇸🇬", OutboundTag: "warp-6eV"},
	{Code: "us", Name: "美国-ProtonVPN-智能优选", Flag: "🇺🇸", OutboundTag: "us-pool"},
	{Code: "jp", Name: "日本-ProtonVPN-智能优选", Flag: "🇯🇵", OutboundTag: "jp-pool"},
	{Code: "nl", Name: "荷兰-ProtonVPN-智能优选", Flag: "🇳🇱", OutboundTag: "nl-pool"},
}

// DeriveUUID generates a deterministic, standard RFC 4122 UUIDv5 for a given user UUID and region code.
func DeriveUUID(baseUUIDStr string, regionCode string) string {
	baseUUID, err := uuid.FromString(strings.TrimSpace(baseUUIDStr))
	if err != nil {
		// Fallback to SHA256 deterministic UUID format
		h := sha256.Sum256([]byte(baseUUIDStr + ":" + regionCode))
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
	}
	return uuid.NewV5(baseUUID, regionCode).String()
}

// DerivePassword derives a deterministic credential password for Trojan/Shadowsocks
func DerivePassword(basePass string, regionCode string) string {
	if regionCode == "" || regionCode == "sg" {
		return basePass
	}
	return fmt.Sprintf("%s-%s", basePass, regionCode)
}

// WireGuardConf holds parsed data from a standard WireGuard .conf file
type WireGuardConf struct {
	Name       string   `json:"name,omitempty"`
	Country    string   `json:"country,omitempty"`
	Load       int      `json:"load,omitempty"`
	PrivateKey string   `json:"private_key"`
	Address    []string `json:"address"`
	DNS        []string `json:"dns,omitempty"`
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips,omitempty"`
	Endpoint   string   `json:"endpoint"`
	ServerIP   string   `json:"server_ip"`
	ServerPort uint16   `json:"server_port"`
	Keepalive  int      `json:"keepalive,omitempty"`
}

// ParseWireGuardConf parses a standard WireGuard .conf (INI format)
func ParseWireGuardConf(content string) (*WireGuardConf, error) {
	conf := &WireGuardConf{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	var section string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(line[1 : len(line)-1])
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		switch section {
		case "interface":
			switch key {
			case "privatekey":
				conf.PrivateKey = val
			case "address":
				addrs := strings.Split(val, ",")
				for _, a := range addrs {
					if trimmed := strings.TrimSpace(a); trimmed != "" {
						conf.Address = append(conf.Address, trimmed)
					}
				}
			case "dns":
				dnsList := strings.Split(val, ",")
				for _, d := range dnsList {
					if trimmed := strings.TrimSpace(d); trimmed != "" {
						conf.DNS = append(conf.DNS, trimmed)
					}
				}
			}
		case "peer":
			switch key {
			case "publickey":
				conf.PublicKey = val
			case "endpoint":
				conf.Endpoint = val
				if host, portStr, err := splitHostPort(val); err == nil {
					conf.ServerIP = host
					if p, err := strconv.Atoi(portStr); err == nil {
						conf.ServerPort = uint16(p)
					}
				}
			case "allowedips":
				ips := strings.Split(val, ",")
				for _, ip := range ips {
					if trimmed := strings.TrimSpace(ip); trimmed != "" {
						conf.AllowedIPs = append(conf.AllowedIPs, trimmed)
					}
				}
			case "persistentkeepalive":
				if ka, err := strconv.Atoi(val); err == nil {
					conf.Keepalive = ka
				}
			}
		}
	}

	if conf.PrivateKey == "" || conf.PublicKey == "" || conf.ServerIP == "" || conf.ServerPort == 0 {
		return nil, fmt.Errorf("invalid wireguard conf: missing required fields")
	}

	return conf, nil
}

func splitHostPort(endpoint string) (string, string, error) {
	idx := strings.LastIndex(endpoint, ":")
	if idx == -1 {
		return "", "", fmt.Errorf("no port in endpoint: %s", endpoint)
	}
	return endpoint[:idx], endpoint[idx+1:], nil
}

// BuildWireGuardEndpointJson formats the parsed WireGuard config into Sing-Box endpoint JSON
func BuildWireGuardEndpointJson(tag string, conf *WireGuardConf) (json.RawMessage, error) {
	keepalive := conf.Keepalive
	if keepalive == 0 {
		keepalive = 25
	}
	peers := []map[string]interface{}{
		{
			"address":                       conf.ServerIP,
			"port":                          conf.ServerPort,
			"public_key":                    conf.PublicKey,
			"allowed_ips":                   []string{"0.0.0.0/0", "::/0"},
			"persistent_keepalive_interval": keepalive,
		},
	}
	epMap := map[string]interface{}{
		"type":        "wireguard",
		"tag":         tag,
		"system":      false, // Pure user-space gVisor mode
		"address":     conf.Address,
		"private_key": conf.PrivateKey,
		"listen_port": 0,
		"peers":       peers,
	}
	return json.Marshal(epMap)
}

// BuildDirectOutboundJson creates a direct outbound tied to a specific endpoint
func BuildDirectOutboundJson(tag string, endpointTag string) (json.RawMessage, error) {
	outMap := map[string]interface{}{
		"type":     "direct",
		"tag":      tag,
		"detour":   endpointTag,
		"endpoint": endpointTag,
	}
	return json.Marshal(outMap)
}

// BuildUrlTestPoolJson creates an urltest auto-failover/load-balancing outbound
func BuildUrlTestPoolJson(tag string, outbounds []string, interval string) (json.RawMessage, error) {
	if interval == "" {
		interval = "3m"
	}
	poolMap := map[string]interface{}{
		"type":      "urltest",
		"tag":       tag,
		"outbounds": outbounds,
		"url":       "http://www.gstatic.com/generate_204",
		"interval":  interval,
		"tolerance": 50,
	}
	return json.Marshal(poolMap)
}

// ExpandUsersForMultiplexing expands base user identities into country-specific credentials
func ExpandUsersForMultiplexing(baseUsers []json.RawMessage, inboundType string, activeRegions []EgressRegion) []json.RawMessage {
	if len(activeRegions) == 0 {
		activeRegions = StandardEgressRegions
	}

	var expanded []json.RawMessage
	for _, userRaw := range baseUsers {
		expanded = append(expanded, userRaw) // Keep root user

		var userMap map[string]interface{}
		if err := json.Unmarshal(userRaw, &userMap); err != nil {
			continue
		}

		baseName, _ := userMap["name"].(string)
		if baseName == "" {
			continue
		}

		for _, reg := range activeRegions {
			if reg.Code == "" || reg.Code == "sg" {
				// Base user already defaults to SG / WARP
				continue
			}

			derivedUser := make(map[string]interface{})
			for k, v := range userMap {
				derivedUser[k] = v
			}
			derivedUser["name"] = fmt.Sprintf("%s-%s", baseName, reg.Code)

			switch inboundType {
			case "vmess", "vless", "tuic":
				if baseUUID, ok := userMap["uuid"].(string); ok && baseUUID != "" {
					derivedUser["uuid"] = DeriveUUID(baseUUID, reg.Code)
				}
			case "trojan", "shadowsocks", "anytls":
				if basePass, ok := userMap["password"].(string); ok && basePass != "" {
					derivedUser["password"] = DerivePassword(basePass, reg.Code)
				}
			}

			if derivedRaw, err := json.Marshal(derivedUser); err == nil {
				expanded = append(expanded, derivedRaw)
			}
		}
	}
	return expanded
}

// InjectEgressRouteRulesForClients ensures that auth_user rules for all provided clients and active egress regions are present in route rules
func InjectEgressRouteRulesForClients(rules []interface{}, rootUsernames []string, activeRegions []EgressRegion) []interface{} {
	if len(rootUsernames) == 0 {
		rootUsernames = []string{"admin", "my"}
	}
	if len(activeRegions) == 0 {
		activeRegions = StandardEgressRegions
	}

	existingUserRules := make(map[string]bool)
	for _, r := range rules {
		if rMap, ok := r.(map[string]interface{}); ok {
			if authUsers, ok := rMap["auth_user"].([]interface{}); ok {
				for _, u := range authUsers {
					if uStr, ok := u.(string); ok {
						existingUserRules[uStr] = true
					}
				}
			}
		}
	}

	var newRules []interface{}
	// Insert sniff action if not first
	hasSniff := false
	for _, r := range rules {
		if rMap, ok := r.(map[string]interface{}); ok {
			if act, ok := rMap["action"].(string); ok && act == "sniff" {
				hasSniff = true
				break
			}
		}
	}
	if !hasSniff {
		newRules = append(newRules, map[string]interface{}{"action": "sniff"})
	}

	// Add region-specific auth_user rules for each client username
	for _, username := range rootUsernames {
		if strings.TrimSpace(username) == "" {
			continue
		}
		for _, reg := range activeRegions {
			if reg.Code == "" || reg.Code == "sg" {
				// Root user represents native direct egress; do not bind to regional pools
				continue
			}
			userName := fmt.Sprintf("%s-%s", username, reg.Code)
			if !existingUserRules[userName] {
				rule := map[string]interface{}{
					"auth_user": []string{userName},
					"outbound":  reg.OutboundTag,
				}
				newRules = append(newRules, rule)
				existingUserRules[userName] = true
			}
		}
	}

	// Append existing rules
	for _, r := range rules {
		newRules = append(newRules, r)
	}

	return newRules
}

// EnsureProtonPoolsInOutbounds dynamically injects urltest outbounds for ProtonVPN regions (us-pool, jp-pool, nl-pool)
func EnsureProtonPoolsInOutbounds(singboxConfig *SingBoxConfig, db *gorm.DB) {
	if db == nil || singboxConfig == nil {
		return
	}

	// Filter out stale proton pool definitions so clean ones with active endpoints are rebuilt
	cleanOutbounds := make([]json.RawMessage, 0, len(singboxConfig.Outbounds))
	for _, obRaw := range singboxConfig.Outbounds {
		var obMap map[string]interface{}
		if err := json.Unmarshal(obRaw, &obMap); err == nil {
			tag, _ := obMap["tag"].(string)
			if tag == "us-pool" || tag == "jp-pool" || tag == "nl-pool" {
				continue
			}
		}
		cleanOutbounds = append(cleanOutbounds, obRaw)
	}
	singboxConfig.Outbounds = cleanOutbounds

	// Map each region code ("us", "jp", "nl") to its matching endpoint tags
	regionEndpoints := make(map[string][]string)
	for _, epRaw := range singboxConfig.Endpoints {
		var epMap map[string]interface{}
		if err := json.Unmarshal(epRaw, &epMap); err == nil {
			tag, _ := epMap["tag"].(string)
			if tag == "" {
				continue
			}
			lowerTag := strings.ToLower(tag)
			for _, code := range []string{"us", "jp", "nl"} {
				if strings.Contains(lowerTag, "proton-"+code) || strings.HasPrefix(lowerTag, "ep-"+code) || strings.Contains(lowerTag, "-"+code+"-") {
					regionEndpoints[code] = append(regionEndpoints[code], tag)
				}
			}
		}
	}

	// Robustly extract active registered WireGuard client private key and address
	protonClientPrivKey, protonClientAddrs := FindWorkingWireGuardPrivateKey(singboxConfig, db)

	existingEpTags := make(map[string]bool)
	var warpTag string
	for _, epRaw := range singboxConfig.Endpoints {
		var epMap map[string]interface{}
		if err := json.Unmarshal(epRaw, &epMap); err == nil {
			if tag, ok := epMap["tag"].(string); ok && tag != "" {
				existingEpTags[tag] = true
			}
			if isCloudflareWarpEndpoint(epMap) && warpTag == "" {
				if tag, ok := epMap["tag"].(string); ok && tag != "" {
					warpTag = tag
				}
			}
		}
	}

	countryCache := GetCountryCache()
	for _, reg := range StandardEgressRegions {
		if reg.Code == "sg" {
			continue
		}
		poolTag := reg.OutboundTag // e.g. "us-pool", "jp-pool", "nl-pool"
		eps := regionEndpoints[reg.Code]

		// Supplement with dynamic physical servers from country cache for guaranteed connectivity
		countryCode := strings.ToUpper(reg.Code)
		cServers := countryCache.GetCountryServers(countryCode)
		if len(cServers) > 0 {
			var dynTags []string
			for sIdx, s := range cServers {
				if len(dynTags) >= 3 {
					break
				}
				epTag := fmt.Sprintf("ep-dyn-%s-%d", reg.Code, sIdx)
				if !existingEpTags[epTag] {
					epJson, err := BuildWireGuardEndpointJsonForServer(epTag, s, protonClientPrivKey, protonClientAddrs)
					if err == nil {
						singboxConfig.Endpoints = append(singboxConfig.Endpoints, epJson)
						existingEpTags[epTag] = true
					}
				}
				if existingEpTags[epTag] {
					dynTags = append(dynTags, epTag)
				}
			}
			if len(dynTags) > 0 {
				eps = append(eps, dynTags...)
			}
		}

		if len(eps) > 0 {
			poolOb, err := BuildUrlTestPoolJson(poolTag, eps, "3m")
			if err == nil {
				singboxConfig.Outbounds = append(singboxConfig.Outbounds, poolOb)
			}
		} else {
			// Fallback direct outbound if no endpoints configured yet
			fallbackOb, _ := json.Marshal(map[string]interface{}{"type": "direct", "tag": poolTag})
			singboxConfig.Outbounds = append(singboxConfig.Outbounds, fallbackOb)
		}
	}
}

// InjectEgressRouteRules ensures that auth_user rules for active egress regions are present in route rules
func InjectEgressRouteRules(rules []interface{}, rootUsername string, activeRegions []EgressRegion) []interface{} {
	if rootUsername == "" {
		rootUsername = "admin"
	}
	return InjectEgressRouteRulesForClients(rules, []string{rootUsername}, activeRegions)
}

// InjectEgressRouteRulesBytes takes raw Route JSON and injects egress routing rules
func InjectEgressRouteRulesBytes(routeRaw json.RawMessage, rootUsername string, activeRegions []EgressRegion) json.RawMessage {
	if len(routeRaw) == 0 {
		return routeRaw
	}
	var routeMap map[string]interface{}
	if err := json.Unmarshal(routeRaw, &routeMap); err != nil {
		return routeRaw
	}
	rules, ok := routeMap["rules"].([]interface{})
	if !ok {
		rules = []interface{}{}
	}
	newRules := InjectEgressRouteRules(rules, rootUsername, activeRegions)
	routeMap["rules"] = newRules
	if newRaw, err := json.Marshal(routeMap); err == nil {
		return newRaw
	}
	return routeRaw
}

// GetActiveEgressRegions returns the list of active egress regions based on outbounds/endpoints and dynamic Cloudflare regions in the database
func GetActiveEgressRegions(db *gorm.DB) []EgressRegion {
	if db == nil {
		db = database.GetDB()
	}

	var active []EgressRegion
	seenCodes := make(map[string]bool)

	// 1. Dynamic Cloudflare Regions directly from Cloudflare ("有多少区分多少")
	cfRegions := GetActiveCloudflareRegions(db)
	for _, cfReg := range cfRegions {
		if !seenCodes[cfReg.Code] {
			active = append(active, cfReg)
			seenCodes[cfReg.Code] = true
		}
	}

	// 2. Dedicated ProtonVPN Regions from existing database endpoints/outbounds (strict segregation from Cloudflare)
	if db != nil {
		var tags []string
		_ = db.Model(&model.Outbound{}).Pluck("tag", &tags)
		tagMap := make(map[string]bool)
		for _, t := range tags {
			tagMap[t] = true
		}

		var epTags []string
		_ = db.Model(&model.Endpoint{}).Pluck("tag", &epTags)
		for _, ep := range epTags {
			tagMap[ep] = true
		}

		// Check StandardEgressRegions (e.g. us, jp, nl)
		for _, reg := range StandardEgressRegions {
			if reg.Code == "sg" {
				continue // Cloudflare cf-sg handles Singapore egress
			}
			if !seenCodes[reg.Code] {
				hasEp := false
				for _, epTag := range epTags {
					if strings.HasPrefix(epTag, "ep-proton-"+reg.Code) || strings.HasPrefix(epTag, "ep-"+reg.Code) {
						hasEp = true
						break
					}
				}
				// Include if its specific outbound/endpoint exists in DB
				if tagMap[reg.OutboundTag] || hasEp {
					active = append(active, reg)
					seenCodes[reg.Code] = true
				}
			}
		}
	}

	if len(active) == 0 {
		return StandardEgressRegions
	}
	return active
}

// FindWorkingWireGuardPrivateKey finds an active, valid WireGuard client private key from existing non-WARP endpoints
func FindWorkingWireGuardPrivateKey(singboxConfig *SingBoxConfig, db *gorm.DB) (string, []string) {
	dummyKey := "yBVl8qcgy/OTwV7fZ4bQzeQv5OAR3AJ2C583nN5u218="
	if singboxConfig != nil {
		for _, epRaw := range singboxConfig.Endpoints {
			var epMap map[string]interface{}
			if err := json.Unmarshal(epRaw, &epMap); err == nil {
				if isCloudflareWarpEndpoint(epMap) {
					continue
				}
				pk, _ := epMap["private_key"].(string)
				if pk != "" && pk != dummyKey {
					var addrs []string
					if aList, ok := epMap["address"].([]interface{}); ok {
						for _, a := range aList {
							if aStr, ok := a.(string); ok {
								addrs = append(addrs, aStr)
							}
						}
					}
					return pk, addrs
				}
			}
		}
	}
	if db != nil {
		var endpoints []model.Endpoint
		_ = db.Where("type = ? OR type = ?", "wireguard", "").Find(&endpoints).Error
		for _, ep := range endpoints {
			if strings.HasPrefix(ep.Tag, "warp") || strings.HasPrefix(ep.Tag, "cf-") {
				continue
			}
			var optMap map[string]interface{}
			if err := json.Unmarshal(ep.Options, &optMap); err == nil {
				pk, _ := optMap["private_key"].(string)
				if pk != "" && pk != dummyKey {
					var addrs []string
					if aList, ok := optMap["address"].([]interface{}); ok {
						for _, a := range aList {
							if aStr, ok := a.(string); ok {
								addrs = append(addrs, aStr)
							}
						}
					}
					return pk, addrs
				}
			}
		}
	}
	return "", nil
}

