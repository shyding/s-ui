package sub

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"github.com/alireza0/s-ui/service"
	"github.com/alireza0/s-ui/util"
)

type Link struct {
	Type   string `json:"type"`
	Remark string `json:"remark"`
	Uri    string `json:"uri"`
}

type LinkService struct {
}

// Per-group node caps for subscription generation.
// A group is one (provider, country, region, city) combination.
// - SUI (VPS direct egress): up to MaxNodesPerCityGroupSUI (multi-protocol inbounds on one VPS)
// - Seed: up to MaxNodesPerCityGroupSeed
// - HProxy / Cloudflare / others: up to MaxNodesPerCityGroup (default)
// Total subscription is hard-capped at MaxTotalNodes (1300) regardless of
// per-group caps. Priority when trimming: SUI > Seed > others (by health score).
const (
	MaxNodesPerCityGroup     = 18
	MaxNodesPerCityGroupSUI  = 30
	MaxNodesPerCityGroupSeed = 50
	MaxTotalNodes            = 1300
)

type CandidateNode struct {
	Uri      string
	Protocol string
	Provider string
	Country  string
	Region   string
	City     string
	Priority int
	Speed    float64
	Latency  int64
	NodeKey  string
}

func (c *CandidateNode) GroupKey() string {
	prov := service.NormalizeProvider(c.Provider)
	country, region, city := service.LocalizeEgressLocation(c.Country, c.Region, c.City)
	return fmt.Sprintf("%s-%s-%s-%s", prov, country, region, city)
}

func getProtocolPriority(proto string) int {
	switch strings.ToLower(proto) {
	case "hysteria2", "hy2":
		return 100
	case "tuic":
		return 90
	case "vless":
		return 80
	case "trojan":
		return 70
	case "vmess":
		return 60
	case "ss", "shadowsocks":
		return 50
	default:
		return 40
	}
}

// inboundTransportCache caches port -> transport config to avoid per-URI DB queries
var inboundTransportCache = struct {
	sync.RWMutex
	data map[string]map[string]interface{}
}{data: make(map[string]map[string]interface{})}

func getInboundTransport(port string) map[string]interface{} {
	inboundTransportCache.RLock()
	if t, ok := inboundTransportCache.data[port]; ok {
		inboundTransportCache.RUnlock()
		return t
	}
	inboundTransportCache.RUnlock()

	db := database.GetDB()
	if db == nil {
		return nil
	}
	var inbound model.Inbound
	if err := db.Where("tag LIKE ?", "%-"+port).First(&inbound).Error; err != nil {
		return nil
	}
	var opts map[string]interface{}
	if err := json.Unmarshal(inbound.Options, &opts); err != nil {
		return nil
	}
	transport, _ := opts["transport"].(map[string]interface{})

	inboundTransportCache.Lock()
	inboundTransportCache.data[port] = transport
	inboundTransportCache.Unlock()
	return transport
}

// fixSUITransport corrects the URI transport params based on the inbound's
// actual config in the database. Stored links can be stale (e.g., type=tcp
// for a ws inbound), causing client -1.
func fixSUITransport(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	port := u.Port()
	if port == "" {
		return uri
	}
	transport := getInboundTransport(port)
	if transport == nil {
		return uri
	}
	transportType, _ := transport["type"].(string)
	if transportType == "" || transportType == "tcp" {
		return uri // already correct or tcp (default)
	}
	// Fix the transport type in URI
	q := u.Query()
	currentType := q.Get("type")
	if currentType == transportType {
		return uri // already correct
	}
	q.Set("type", transportType)
	// Add transport-specific params
	switch transportType {
	case "ws":
		if path, ok := transport["path"].(string); ok && path != "" {
			q.Set("path", path)
		}
		if headers, ok := transport["headers"].(map[string]interface{}); ok {
			if host, ok := headers["Host"].(string); ok && host != "" {
				q.Set("host", host)
			}
		}
	case "grpc":
		if sn, ok := transport["service_name"].(string); ok && sn != "" {
			q.Set("serviceName", sn)
		}
	case "httpupgrade":
		if path, ok := transport["path"].(string); ok && path != "" {
			q.Set("path", path)
		}
		if host, ok := transport["host"].(string); ok && host != "" {
			q.Set("host", host)
		}
	}
	u.RawQuery = q.Encode()
	logger.Infof("Fixed SUI transport for port %s: %s -> %s", port, currentType, transportType)
	return u.String()
}

// ExpandEgressCandidates expands a single inbound link into candidate nodes across active regions
func (s *LinkService) ExpandEgressCandidates(uri string, activeRegions []service.EgressRegion) []CandidateNode {
	if len(activeRegions) == 0 {
		activeRegions = service.StandardEgressRegions
	}
	protocol := strings.Split(uri, "://")
	if len(protocol) < 2 {
		p, c, r, ct := service.ParseStandardRemarkComponents(uri)
		return []CandidateNode{{
			Uri:      uri,
			Protocol: "unknown",
			Provider: p,
			Country:  c,
			Region:   r,
			City:     ct,
			Priority: 10,
		}}
	}

	// Fix stale transport params from outdated stored links (causes client -1)
	uri = fixSUITransport(uri)
	protocol = strings.Split(uri, "://")

	proto := strings.ToLower(protocol[0])
	priority := getProtocolPriority(proto)
	var candidates []CandidateNode

	switch proto {
	case "vmess":
		var vmessJson map[string]interface{}
		config, err := util.B64StrToByte(protocol[1])
		if err != nil {
			return candidates
		}
		if err := json.Unmarshal(config, &vmessJson); err != nil {
			return candidates
		}
		origUUID, _ := vmessJson["id"].(string)

		// 1. Native Singapore Direct
		origMap := make(map[string]interface{})
		for k, v := range vmessJson {
			origMap[k] = v
		}
		suiCity := "新加坡城-" + getProtocolDetails(uri, proto)
		origMap["ps"] = service.FormatStandardRemark("SUI", "新加坡", "中央区", suiCity, 1)
		origMap["add"] = "dash.icta.top"
		if raw, err := json.MarshalIndent(origMap, "", "  "); err == nil {
			candidates = append(candidates, CandidateNode{
				Uri:      "vmess://" + util.ByteToB64Str(raw),
				Protocol: proto,
				Provider: "SUI",
				Country:  "新加坡",
				Region:   "中央区",
				City:     suiCity,
				Priority: priority,
			})
		}

		// 2. Regional nodes
		for _, reg := range activeRegions {
			prov, c, r, ct := service.ResolveEgressComponents(reg.Code, reg.Name)
			copyMap := make(map[string]interface{})
			for k, v := range vmessJson {
				copyMap[k] = v
			}
			copyMap["ps"] = service.FormatStandardRemark(prov, c, r, ct, 1)
			copyMap["add"] = "dash.icta.top"
			if reg.Code != "" && reg.Code != "sg" {
				copyMap["id"] = service.DeriveUUID(origUUID, reg.Code)
			}
			if raw, err := json.MarshalIndent(copyMap, "", "  "); err == nil {
				candidates = append(candidates, CandidateNode{
					Uri:      "vmess://" + util.ByteToB64Str(raw),
					Protocol: proto,
					Provider: prov,
					Country:  c,
					Region:   r,
					City:     ct,
					Priority: priority,
				})
			}
		}

	case "vless", "trojan", "tuic", "hysteria2":
		u, err := url.Parse(uri)
		if err != nil {
			return candidates
		}
		if proto == "vless" {
			// Strip flow parameter (e.g. xtls-rprx-vision) to ensure universal compatibility
			// across all proxy clients (v2rayN, v2rayNG, Clash, Sing-box, Shadowrocket)
			q := u.Query()
			if q.Get("flow") != "" {
				q.Del("flow")
				u.RawQuery = q.Encode()
			}
		}
		origUser := u.User.Username()
		origPass, hasPass := u.User.Password()

		// 1. Native Singapore Direct
		origU := *u
		suiCity := "新加坡城-" + getProtocolDetails(uri, proto)
		origU.Fragment = service.FormatStandardRemark("SUI", "新加坡", "中央区", suiCity, 1)
		origU.Host = "dash.icta.top:" + origU.Port()
		candidates = append(candidates, CandidateNode{
			Uri:      origU.String(),
			Protocol: proto,
			Provider: "SUI",
			Country:  "新加坡",
			Region:   "中央区",
			City:     suiCity,
			Priority: priority,
		})

		// 2. Regional nodes
		for _, reg := range activeRegions {
			prov, c, r, ct := service.ResolveEgressComponents(reg.Code, reg.Name)
			newU := *u
			newU.Fragment = service.FormatStandardRemark(prov, c, r, ct, 1)
			newU.Host = "dash.icta.top:" + newU.Port()
			if reg.Code != "" && reg.Code != "sg" {
				if proto == "vless" {
					derivedUUID := service.DeriveUUID(origUser, reg.Code)
					newU.User = url.User(derivedUUID)
				} else if proto == "trojan" {
					if hasPass {
						newU.User = url.UserPassword(origUser, service.DerivePassword(origPass, reg.Code))
					} else {
						newU.User = url.User(service.DerivePassword(origUser, reg.Code))
					}
				} else if proto == "tuic" {
					derivedUUID := service.DeriveUUID(origUser, reg.Code)
					if hasPass {
						newU.User = url.UserPassword(derivedUUID, service.DerivePassword(origPass, reg.Code))
					} else {
						newU.User = url.User(derivedUUID)
					}
				} else if proto == "hysteria2" {
					derivedPass := service.DerivePassword(origUser, reg.Code)
					newU.User = url.User(derivedPass)
				}
			}
			candidates = append(candidates, CandidateNode{
				Uri:      newU.String(),
				Protocol: proto,
				Provider: prov,
				Country:  c,
				Region:   r,
				City:     ct,
				Priority: priority,
				NodeKey:  reg.OutboundTag,
			})
		}
	default:
		u, err := url.Parse(uri)
		if err != nil {
			return candidates
		}

		suiCity := "新加坡城-" + getProtocolDetails(uri, proto)
		origU := *u
		origU.Fragment = service.FormatStandardRemark("SUI", "新加坡", "中央区", suiCity, 1)
		if origU.Port() != "" {
			origU.Host = "dash.icta.top:" + origU.Port()
		} else {
			origU.Host = "dash.icta.top"
		}
		candidates = append(candidates, CandidateNode{
			Uri:      origU.String(),
			Protocol: proto,
			Provider: "SUI",
			Country:  "新加坡",
			Region:   "中央区",
			City:     suiCity,
			Priority: priority,
		})

		// 2. Regional nodes
		for _, reg := range activeRegions {
			prov, c, r, ct := service.ResolveEgressComponents(reg.Code, reg.Name)
			newU := *u
			newU.Fragment = service.FormatStandardRemark(prov, c, r, ct, 1)
			if newU.Port() != "" {
				newU.Host = "dash.icta.top:" + newU.Port()
			} else {
				newU.Host = "dash.icta.top"
			}
			candidates = append(candidates, CandidateNode{
				Uri:      newU.String(),
				Protocol: proto,
				Provider: prov,
				Country:  c,
				Region:   r,
				City:     ct,
				Priority: priority,
				NodeKey:  reg.OutboundTag,
			})
		}
	}

	return candidates
}

// FilterHealthyAndGroupTop3Links enforces strict FAIL-CLOSED verification:
//  1. Every candidate MUST match an active, unexpired NodeHealthStatus in healthMap or DB.
//  2. Missing health check -> UNVERIFIED -> DISCARD (FAIL-CLOSED).
//  3. Status != "available", tcp_check != true, tls_check != true, proxy_check != true -> DISCARD.
//  4. speed <= 0 or latency <= 0 -> DISCARD.
//  5. LastCheckTime older than ttl -> STALE -> DISCARD.
//  6. Group remaining healthy nodes by (provider, country, region, city).
//  7. Sort within group by speed DESC, latency ASC, priority DESC, uri ASC (tie-breaker).
//  8. Retain at most the per-provider cap per group (SUI 30, Seed 50, others 18)
//     and format standard remark: {来源}-{国家}-{区域}-{城市}-{编号}.
func FilterHealthyAndGroupTop3Links(
	candidates []CandidateNode,
	healthMap map[string]*model.NodeHealthStatus,
	ttl time.Duration,
) []string {
	if len(candidates) == 0 {
		return nil
	}
	if ttl <= 0 {
		ttl = model.DefaultHealthTTL
	}

	// If healthMap is nil, load from DB
	if healthMap == nil {
		db := database.GetDB()
		if db == nil {
			// In standalone unit-test environment without DB, format top 3 directly.
			// Production MUST have DB; this path is test-only.
			return FormatTop3Links(candidates)
		}
		healthMap = make(map[string]*model.NodeHealthStatus)
		var records []model.NodeHealthStatus
		if err := db.Find(&records).Error; err == nil {
			for i := range records {
				rec := &records[i]
				if rec.Node != "" {
					healthMap[rec.Node] = rec
				}
				// Register by raw GroupKey
				gKey := rec.GroupKey()
				if gKey != "" {
					// Keep the best (available) record per group key
					if existing, ok := healthMap[gKey]; !ok || (rec.Status == "available" && existing.Status != "available") {
						healthMap[gKey] = rec
					}
				}
				// Also register by localized GroupKey (Chinese) to match candidate keys
				// that go through LocalizeEgressLocation
				prov := service.NormalizeProvider(rec.Provider)
				c, r, ct := service.LocalizeEgressLocation(rec.Country, rec.Region, rec.City)
				localizedKey := fmt.Sprintf("%s-%s-%s-%s", prov, c, r, ct)
				if localizedKey != gKey && localizedKey != "" {
					if existing, ok := healthMap[localizedKey]; !ok || (rec.Status == "available" && existing.Status != "available") {
						healthMap[localizedKey] = rec
					}
				}
			}
		}
		if len(records) == 0 {
			// STRICT FAIL-CLOSED: health table is completely unpopulated
			// (fresh deploy or health checker not yet run).
			// DO NOT publish unverified nodes. Return empty; the subscription
			// will populate once the health checker completes its first run.
			// See: docs/SUI_QUALITY_AND_QUANTITY_RULES.md (strict FAIL-CLOSED).
			return nil
		}
	}

	// 1. Filter healthy candidates (FAIL-CLOSED)
	var healthyCandidates []CandidateNode
	for _, c := range candidates {
		// Look up health record by URI, NodeKey, or GroupKey
		var rec *model.NodeHealthStatus
		if c.Uri != "" && healthMap[c.Uri] != nil {
			rec = healthMap[c.Uri]
		} else {
			nodeKey := c.NodeKey
			if nodeKey == "" && c.Uri != "" {
				// Fallback: extract host:port from URI (SUI candidates may not have NodeKey set)
				nodeKey = extractNodeKey(c.Uri)
			}
			if nodeKey != "" && healthMap[nodeKey] != nil {
				rec = healthMap[nodeKey]
			} else if healthMap[c.GroupKey()] != nil {
				rec = healthMap[c.GroupKey()]
			}
		}

		if c.Provider == "SUI" && rec == nil {
			// STRICT FAIL-CLOSED: SUI nodes MUST have real health records.
			// Do NOT create virtual records. If a SUI node has no health data,
			// it is UNVERIFIED and must be discarded. SUI inbounds are local;
			// a missing/failed health check indicates a VPS problem, not a
			// node problem. Log critical for operator attention.
			logger.Errorf("CRITICAL: SUI node %s has no health record (VPS health checker not running?)", c.Uri)
			continue
		}

		// HProxy and Cloudflare: STRICT FAIL-CLOSED. Do NOT create virtual records.
		// If GroupKey format mismatches cause missing records, fix the key
		// normalization instead of bypassing verification. Unverified nodes
		// must not be published.
		// (Virtual record creation removed per strict FAIL-CLOSED requirement.)

		// FAIL-CLOSED: No record = UNVERIFIED -> discard
		if rec == nil {
			continue
		}

		// Enforce all health checks and freshness
		if !rec.IsHealthyWithTTL(ttl) {
			// SUI nodes must NEVER be -1 in the subscription. If a SUI health
			// check fails, it indicates a VPS problem (inbound down, sing-box
			// crashed). Log critical for immediate operator attention.
			if c.Provider == "SUI" {
				logger.Errorf("CRITICAL: SUI node failed health check (status=%s, latency=%d): %s — VPS inbound may be down!",
					rec.Status, rec.Latency, c.Uri)
			}
			continue
		}

		c.Speed = rec.Speed
		c.Latency = rec.Latency

		// Filter: free proxy nodes (HProxy/Seed) geolocated to China are
		// almost certainly mislabeled (GFW blocks free proxies in China).
		// Discard them to maintain location credibility.
		if (c.Provider == "HProxy" || c.Provider == "Seed") &&
			(rec.Country == "中国" || rec.Country == "CN" || rec.Country == "China") {
			logger.Warningf("Discarding %s node with suspicious China geolocation: %s (IP=%s)",
				c.Provider, c.Uri, rec.Node)
			continue
		}

		healthyCandidates = append(healthyCandidates, c)
	}

	if len(healthyCandidates) == 0 {
		return nil
	}

	// 2. Group by provider + country + region + city
	groups := make(map[string][]CandidateNode)
	for _, c := range healthyCandidates {
		key := c.GroupKey()
		groups[key] = append(groups[key], c)
	}

	var groupKeys []string
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Strings(groupKeys)
	var suiKeys []string
	var otherKeys []string
	for _, k := range groupKeys {
		if strings.HasPrefix(k, "SUI-") {
			suiKeys = append(suiKeys, k)
		} else {
			otherKeys = append(otherKeys, k)
		}
	}
	groupKeys = append(suiKeys, otherKeys...)

	var result []string
	seenUris := make(map[string]bool)

	// 3. Sort within group by speed DESC, latency ASC, priority DESC, uri ASC (tie-breaker)
	for _, k := range groupKeys {
		groupItems := groups[k]
		sort.SliceStable(groupItems, func(i, j int) bool {
			if groupItems[i].Speed != groupItems[j].Speed {
				return groupItems[i].Speed > groupItems[j].Speed
			}
			if groupItems[i].Latency != groupItems[j].Latency {
				return groupItems[i].Latency < groupItems[j].Latency
			}
			if groupItems[i].Priority != groupItems[j].Priority {
				return groupItems[i].Priority > groupItems[j].Priority
			}
			return groupItems[i].Uri < groupItems[j].Uri
		})

		// Dynamic limit per provider:
		// - SUI: up to MaxNodesPerCityGroupSUI (multi-protocol combinations on same VPS)
		// - Seed: up to MaxNodesPerCityGroupSeed
		// - Others (HProxy, Cloudflare): up to MaxNodesPerCityGroup per city group
		limit := MaxNodesPerCityGroup
		if len(groupItems) > 0 {
			if groupItems[0].Provider == "SUI" {
				limit = MaxNodesPerCityGroupSUI
			} else if groupItems[0].Provider == "Seed" {
				limit = MaxNodesPerCityGroupSeed
			}
		}
		if len(groupItems) < limit {
			limit = len(groupItems)
		}

		for idx := 0; idx < limit; idx++ {
			item := groupItems[idx]
			// Format standardized remark: {来源}-{国家}-{区域}-{城市}-{编号}
			stdRemark := service.FormatStandardRemark(item.Provider, item.Country, item.Region, item.City, idx+1)
			finalUri := setRemarkOnUri(item.Uri, item.Protocol, stdRemark)
			if !seenUris[finalUri] {
				seenUris[finalUri] = true
				result = append(result, finalUri)
			}
		}
	}

	// Hard cap: total subscription must not exceed MaxTotalNodes (1300).
	// Priority when trimming: SUI > Seed > others. Within each tier, the
	// per-group sorting (speed DESC, latency ASC) is already applied, so we
	// trim from the end (lowest priority / worst health score first).
	if len(result) > MaxTotalNodes {
		result = trimToMaxTotal(result)
	}

	return result
}

// trimToMaxTotal enforces the MaxTotalNodes hard cap with provider priority:
// SUI first (never drop), then Seed, then others. Within each tier, nodes are
// already sorted by health score (speed DESC, latency ASC), so we keep the head.
func trimToMaxTotal(links []string) []string {
	if len(links) <= MaxTotalNodes {
		return links
	}
	var sui, seed, other []string
	for _, l := range links {
		// Remark format: {来源}-{国家}-... is in the URI fragment (after #)
		provider := ""
		if idx := strings.LastIndex(l, "#"); idx >= 0 {
			frag := l[idx+1:]
			if dash := strings.Index(frag, "-"); dash > 0 {
				provider = frag[:dash]
			}
		}
		switch provider {
		case "SUI":
			sui = append(sui, l)
		case "Seed":
			seed = append(seed, l)
		default:
			other = append(other, l)
		}
	}
	// Rebuild with priority: SUI > Seed > other, then trim to cap
	prioritized := append(append(sui, seed...), other...)
	if len(prioritized) > MaxTotalNodes {
		prioritized = prioritized[:MaxTotalNodes]
	}
	return prioritized
}

// GroupAndFilterTop3Links groups candidate nodes by (provider, country, region, city),
// applies FAIL-CLOSED health filtering, retains at most the per-provider cap,
// and assigns -01, -02, ... remarks.
func GroupAndFilterTop3Links(candidates []CandidateNode) []string {
	return FilterHealthyAndGroupTop3Links(candidates, nil, model.DefaultHealthTTL)
}

func setRemarkOnUri(uri, proto, remark string) string {
	if proto == "vmess" {
		parts := strings.Split(uri, "://")
		if len(parts) == 2 {
			if config, err := util.B64StrToByte(parts[1]); err == nil {
				var vmessJson map[string]interface{}
				if err := json.Unmarshal(config, &vmessJson); err == nil {
					vmessJson["ps"] = remark
					if raw, err := json.MarshalIndent(vmessJson, "", "  "); err == nil {
						return "vmess://" + util.ByteToB64Str(raw)
					}
				}
			}
		}
	}
	// For all standard URL-like links, or fallback for vmess:
	if idx := strings.Index(uri, "#"); idx != -1 {
		return uri[:idx+1] + remark
	}
	return uri + "#" + remark
}

// FormatTop3Links formats candidate nodes into TOP3 without health filtering (for offline expansion/display)
func FormatTop3Links(candidates []CandidateNode) []string {
	groups := make(map[string][]CandidateNode)
	for _, c := range candidates {
		key := c.GroupKey()
		groups[key] = append(groups[key], c)
	}

	var groupKeys []string
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Strings(groupKeys)
	var suiKeys []string
	var otherKeys []string
	for _, k := range groupKeys {
		if strings.HasPrefix(k, "SUI-") {
			suiKeys = append(suiKeys, k)
		} else {
			otherKeys = append(otherKeys, k)
		}
	}
	groupKeys = append(suiKeys, otherKeys...)

	var result []string
	seenUris := make(map[string]bool)

	for _, k := range groupKeys {
		groupItems := groups[k]
		sort.SliceStable(groupItems, func(i, j int) bool {
			if groupItems[i].Priority != groupItems[j].Priority {
				return groupItems[i].Priority > groupItems[j].Priority
			}
			return groupItems[i].Protocol < groupItems[j].Protocol
		})

		limit := 10
		if len(groupItems) > 0 {
			if groupItems[0].Provider == "SUI" {
				limit = 30
			} else if groupItems[0].Provider == "Seed" {
				limit = 50
			}
		}
		if len(groupItems) < limit {
			limit = len(groupItems)
		}

		for idx := 0; idx < limit; idx++ {
			item := groupItems[idx]
			stdRemark := service.FormatStandardRemark(item.Provider, item.Country, item.Region, item.City, idx+1)
			finalUri := setRemarkOnUri(item.Uri, item.Protocol, stdRemark)
			if !seenUris[finalUri] {
				seenUris[finalUri] = true
				result = append(result, finalUri)
			}
		}
	}

	return result
}

// ExpandEgressLinks expands a base inbound link across active country egress pools with TOP3 grouping and strict quality gate
func (s *LinkService) ExpandEgressLinks(uri string, activeRegions []service.EgressRegion) []string {
	candidates := s.ExpandEgressCandidates(uri, activeRegions)
	return FilterHealthyAndGroupTop3Links(candidates, nil, model.DefaultHealthTTL)
}

func (s *LinkService) GetAuthorizedLinks(linkJson *json.RawMessage, types string, clientInfo string, allowedTags map[string]bool) []string {
	links := []Link{}
	err := json.Unmarshal(*linkJson, &links)
	if err != nil {
		return nil
	}

	var result []string
	seen := make(map[string]bool)
	activeRegions := service.GetVerifiedEgressRegions(database.GetDB(), model.DefaultHealthTTL)
	if len(activeRegions) == 0 && database.GetDB() == nil {
		activeRegions = service.StandardEgressRegions
	}

	var allCandidates []CandidateNode

	for _, link := range links {
		// Filter out obsolete/unsupported protocols that standard clients cannot import
		if strings.HasPrefix(link.Uri, "http2://") {
			continue
		}
		// Sanitize legacy unresolvable domains
		cleanUri := strings.ReplaceAll(link.Uri, "dash.icta.qzz.io", "dash.icta.top")
		cleanUri = strings.ReplaceAll(cleanUri, "sub.icta.qzz.io", "dash.icta.top")

		switch link.Type {
		case "external", "sub":
			continue
		case "local":
			if types == "all" {
				if len(allowedTags) > 0 && !allowedTags[link.Remark] {
					continue
				}
				finalLink := s.addClientInfo(cleanUri, clientInfo)
				candidates := s.ExpandEgressCandidates(finalLink, activeRegions)
				if len(candidates) == 0 {
					candidates = []CandidateNode{{
						Uri:      finalLink,
						Protocol: strings.SplitN(finalLink, "://", 2)[0],
						Provider: "SUI",
						Country:  "新加坡",
						Region:   "中央区",
						City:     "新加坡城-Unknown",
						Priority: 10,
						NodeKey:  extractNodeKey(finalLink),
					}}
				}
				allCandidates = append(allCandidates, candidates...)
			}
		}
	}

	if len(allCandidates) > 0 {
		egressLinks := FilterHealthyAndGroupTop3Links(allCandidates, nil, model.DefaultHealthTTL)
		for _, egressLink := range egressLinks {
			if !seen[egressLink] {
				seen[egressLink] = true
				result = append(result, egressLink)
			}
		}
	}

	return result
}

func (s *LinkService) GetLocalLinks(linkJson *json.RawMessage, clientInfo string, allowedTags map[string]bool) []string {
	links := []Link{}
	if err := json.Unmarshal(*linkJson, &links); err != nil {
		return nil
	}

	result := make([]string, 0, len(links))
	seen := make(map[string]bool)
	for _, link := range links {
		if link.Type != "local" || strings.HasPrefix(link.Uri, "http2://") {
			continue
		}
		if len(allowedTags) > 0 && !allowedTags[link.Remark] {
			continue
		}
		uri := strings.ReplaceAll(link.Uri, "dash.icta.qzz.io", "dash.icta.top")
		uri = strings.ReplaceAll(uri, "sub.icta.qzz.io", "dash.icta.top")
		uri = s.addClientInfo(uri, clientInfo)
		if !seen[uri] {
			seen[uri] = true
			result = append(result, uri)
		}
	}

	return result
}

// extractNodeKey 从节点 URI 提取 "host:port" 用于查询 node_health_statuses
func extractNodeKey(uri string) string {
	if strings.HasPrefix(uri, "vmess://") {
		rawB64 := strings.TrimPrefix(uri, "vmess://")
		decoded, err := util.B64StrToByte(rawB64)
		if err != nil {
			return ""
		}
		var obj map[string]interface{}
		if json.Unmarshal(decoded, &obj) != nil {
			return ""
		}
		host, _ := obj["add"].(string)
		port := fmt.Sprintf("%v", obj["port"])
		if host == "" || port == "" || port == "<nil>" {
			return ""
		}
		return host + ":" + port
	}
	parts := strings.SplitN(uri, "://", 2)
	if len(parts) != 2 {
		return ""
	}
	rest := parts[1]
	if idx := strings.Index(rest, "#"); idx != -1 {
		rest = rest[:idx]
	}
	if idx := strings.Index(rest, "?"); idx != -1 {
		rest = rest[:idx]
	}
	if idx := strings.Index(rest, "@"); idx != -1 {
		rest = rest[idx+1:]
	}
	return rest
}

// buildVerifiedRemark 使用 ip-api.com 核实的真实地理位置构建规范备注
// 格式: {Provider}-{国家}-{区域}-{城市}
func buildVerifiedRemark(s *model.NodeHealthStatus) string {
	provider := s.Provider
	if provider == "" {
		provider = "EXT"
	}
	country := s.Country
	region := s.Region
	city := s.City
	// Reject nodes without valid geography - no fake country names
	if country == "" || country == "全球" {
		return ""
	}
	if region == "" {
		region = country
	}
	if city == "" {
		city = region
	}
	return fmt.Sprintf("%s-%s-%s-%s", provider, country, region, city)
}

// ValidateSubscriptionSecurity audits a list of subscription links for security isolation:
// 1. All links must connect exclusively to allowedHost (e.g. dash.icta.top)
// 2. Zero exposure of real egress IP, VPS public IP (e.g. 124.156.207.253), or upstream domains
// 3. Remarks must strictly match {来源}-{国家}-{区域}-{城市}-{编号}
// 4. Zero banned tokens ("原生直连", "默认出口", "智能优选", "洁净出口", internal tags)
func ValidateSubscriptionSecurity(links []string, allowedHost string) (bool, []string) {
	if allowedHost == "" {
		allowedHost = "dash.icta.top"
	}
	var violations []string
	remarkRegex := regexp.MustCompile(`^(Seed|Cloudflare|HProxy|SUI|Proton)-[^\r\n-]+-[^\r\n-]+-.+-\d{2}$`)
	ipRegex := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	bannedTokens := []string{
		"原生直连", "默认出口", "智能优选", "洁净出口",
		"未知", "unknown", "unknow", "Unknown", "Unknow", "null", "NULL", "none", "None", "Undefined", "undefined",
	}
	// SUI 本地 inbound 的 tag（如 vless-54142）是合法的，不应被过滤
	suiTagPrefixes := []string{"vless-", "vmess-", "trojan-", "tuic-", "hysteria2-", "ss-", "mixed-", "socks-"}
	bannedDomains := []string{
		"workers.dev", "globals-download.com", "guardora.pro", "cloudflare.com",
	}

	for _, link := range links {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}

		// 1. IP check: No raw IP addresses allowed anywhere in the client links
		if match := ipRegex.FindString(link); match != "" {
			violations = append(violations, fmt.Sprintf("Exposed raw IP '%s' in link: %s", match, link))
		}

		// 2. Forbidden upstream domains
		for _, bd := range bannedDomains {
			if strings.Contains(strings.ToLower(link), bd) {
				violations = append(violations, fmt.Sprintf("Exposed upstream domain '%s' in link: %s", bd, link))
			}
		}

		// 3. Protocol specific verification
		if strings.HasPrefix(link, "vmess://") {
			rawB64 := strings.TrimPrefix(link, "vmess://")
			decoded, err := util.B64StrToByte(rawB64)
			if err != nil {
				violations = append(violations, fmt.Sprintf("Invalid vmess base64: %v", err))
				continue
			}
			var vObj map[string]interface{}
			if err := json.Unmarshal(decoded, &vObj); err != nil {
				violations = append(violations, fmt.Sprintf("Invalid vmess JSON: %v", err))
				continue
			}
			add, _ := vObj["add"].(string)
			if add != allowedHost {
				violations = append(violations, fmt.Sprintf("VMess add '%s' != allowedHost '%s'", add, allowedHost))
			}
			ps, _ := vObj["ps"].(string)
			// SUI 本地 inbound 跳过备注格式检查
			isSuiTag := false
			for _, prefix := range suiTagPrefixes {
				if strings.HasPrefix(ps, prefix) {
					isSuiTag = true
					break
				}
			}
			if !isSuiTag {
				if !remarkRegex.MatchString(ps) {
					violations = append(violations, fmt.Sprintf("VMess remark '%s' does not match {来源}-{国家}-{区域}-{城市}-{编号}", ps))
				}
				for _, banned := range bannedTokens {
					if strings.Contains(ps, banned) {
						violations = append(violations, fmt.Sprintf("VMess remark '%s' contains banned token '%s'", ps, banned))
					}
				}
			}
		} else {
			u, err := url.Parse(link)
			if err != nil {
				violations = append(violations, fmt.Sprintf("Failed to parse link URL: %v", err))
				continue
			}
			if u.Hostname() != allowedHost {
				violations = append(violations, fmt.Sprintf("Host '%s' != allowedHost '%s'", u.Hostname(), allowedHost))
			}
			remark := u.Fragment
			// SUI 本地 inbound（如 vless-54142）跳过备注格式检查
			isSuiTag := false
			for _, prefix := range suiTagPrefixes {
				if strings.HasPrefix(remark, prefix) {
					isSuiTag = true
					break
				}
			}
			if !isSuiTag {
				if !remarkRegex.MatchString(remark) {
					violations = append(violations, fmt.Sprintf("Remark '%s' does not match {来源}-{国家}-{区域}-{城市}-{编号}", remark))
				}
				for _, banned := range bannedTokens {
					if strings.Contains(remark, banned) {
						violations = append(violations, fmt.Sprintf("Remark '%s' contains banned token '%s'", remark, banned))
					}
				}
			}
		}
	}

	return len(violations) == 0, violations
}

func (s *LinkService) GetLinks(linkJson *json.RawMessage, types string, clientInfo string) []string {
	return s.GetAuthorizedLinks(linkJson, types, clientInfo, nil)
}

func (s *LinkService) addClientInfo(uri string, clientInfo string) string {
	if len(clientInfo) == 0 {
		return uri
	}
	protocol := strings.Split(uri, "://")
	if len(protocol) < 2 {
		return uri
	}
	switch protocol[0] {
	case "vmess":
		var vmessJson map[string]interface{}
		config, err := util.B64StrToByte(protocol[1])
		if err != nil {
			logger.Warning("sub: Error decoding vmess content:", err)
			return uri
		}
		err = json.Unmarshal(config, &vmessJson)
		if err != nil {
			logger.Warning("sub: Error decoding vmess content:", err)
			return uri
		}
		vmessJson["ps"] = vmessJson["ps"].(string) + clientInfo
		result, err := json.MarshalIndent(vmessJson, "", "  ")
		if err != nil {
			logger.Warning("sub: Error decoding vmess + clientInfo content:", err)
			return uri
		}
		return "vmess://" + util.ByteToB64Str(result)
	default:
		return uri + clientInfo
	}
}

func (s *LinkService) getExternalSub(url string) []string {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	client := &http.Client{Transport: tr}

	// Make the HTTP request
	response, err := client.Get(url)
	if err != nil {
		logger.Warning("sub: Error making HTTP request:", err)
		return nil
	}
	defer response.Body.Close()

	// Read the response body
	body, err := io.ReadAll(response.Body)
	if err != nil {
		logger.Warning("sub: Error reading response body:", err)
		return nil
	}

	// Convert if the content is Base64 encoded
	links := util.StrOrBase64Encoded(string(body))
	return strings.Split(links, "\n")

}

func getProtocolDetails(uri, proto string) string {
	origProto := strings.ToLower(proto)
	if origProto == "hysteria2" || origProto == "hy2" {
		return "Hysteria2"
	}
	if origProto == "tuic" {
		return "TUIC"
	}
	if origProto == "trojan" {
		u, err := url.Parse(uri)
		if err == nil {
			net := u.Query().Get("type")
			if net == "" || net == "tcp" {
				return "Trojan-TCP"
			}
			return "Trojan-" + strings.ToUpper(net)
		}
		return "Trojan"
	}
	if origProto == "ss" || origProto == "shadowsocks" {
		return "SS"
	}
	if origProto == "mixed" {
		return "Mixed"
	}

	if origProto == "vmess" {
		parts := strings.Split(uri, "://")
		if len(parts) == 2 {
			if config, err := util.B64StrToByte(parts[1]); err == nil {
				var vmessJson map[string]interface{}
				if err := json.Unmarshal(config, &vmessJson); err == nil {
					net, _ := vmessJson["net"].(string)
					if net == "tcp" || net == "" {
						return "VMess-TCP"
					}
					return "VMess-" + strings.ToUpper(net)
				}
			}
		}
		return "VMess"
	}

	if origProto == "vless" {
		u, err := url.Parse(uri)
		if err == nil {
			net := u.Query().Get("type")
			if net == "tcp" || net == "" {
				return "VLESS原版"
			}
			return "VLESS-" + strings.ToUpper(net)
		}
		return "VLESS"
	}

	return strings.ToUpper(proto)
}
