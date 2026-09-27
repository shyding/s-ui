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

	"github.com/alireza0/s-ui/database"
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

type CandidateNode struct {
	Uri      string
	Protocol string
	Provider string
	Country  string
	Region   string
	City     string
	Priority int
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
		origMap["ps"] = service.FormatStandardRemark("SUI", "新加坡", "中央区", "新加坡城", 1)
		origMap["add"] = "dash.icta.top"
		if raw, err := json.MarshalIndent(origMap, "", "  "); err == nil {
			candidates = append(candidates, CandidateNode{
				Uri:      "vmess://" + util.ByteToB64Str(raw),
				Protocol: proto,
				Provider: "SUI",
				Country:  "新加坡",
				Region:   "中央区",
				City:     "新加坡城",
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
		origUser := u.User.Username()
		origPass, hasPass := u.User.Password()

		// 1. Native Singapore Direct
		origU := *u
		origU.Fragment = service.FormatStandardRemark("SUI", "新加坡", "中央区", "新加坡城", 1)
		origU.Host = "dash.icta.top:" + origU.Port()
		candidates = append(candidates, CandidateNode{
			Uri:      origU.String(),
			Protocol: proto,
			Provider: "SUI",
			Country:  "新加坡",
			Region:   "中央区",
			City:     "新加坡城",
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
			})
		}
	}

	return candidates
}

// GroupAndFilterTop3Links groups candidate nodes by (provider, country, region, city),
// sorts each group by priority/performance, retains at most TOP 3, and assigns -01, -02, -03 remarks.
func GroupAndFilterTop3Links(candidates []CandidateNode) []string {
	groups := make(map[string][]CandidateNode)
	for _, c := range candidates {
		key := fmt.Sprintf("%s-%s-%s-%s", c.Provider, c.Country, c.Region, c.City)
		groups[key] = append(groups[key], c)
	}

	var groupKeys []string
	for k := range groups {
		if k != "SUI-新加坡-中央区-新加坡城" {
			groupKeys = append(groupKeys, k)
		}
	}
	sort.Strings(groupKeys)
	if _, ok := groups["SUI-新加坡-中央区-新加坡城"]; ok {
		groupKeys = append([]string{"SUI-新加坡-中央区-新加坡城"}, groupKeys...)
	}

	var result []string
	seenUris := make(map[string]bool)

	for _, k := range groupKeys {
		groupItems := groups[k]
		// Sort by protocol priority DESC, then protocol ASC
		sort.SliceStable(groupItems, func(i, j int) bool {
			if groupItems[i].Priority != groupItems[j].Priority {
				return groupItems[i].Priority > groupItems[j].Priority
			}
			return groupItems[i].Protocol < groupItems[j].Protocol
		})

		limit := 3
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

	return result
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

// ExpandEgressLinks expands a base inbound link across active country egress pools with TOP3 grouping
func (s *LinkService) ExpandEgressLinks(uri string, activeRegions []service.EgressRegion) []string {
	candidates := s.ExpandEgressCandidates(uri, activeRegions)
	return GroupAndFilterTop3Links(candidates)
}

func (s *LinkService) GetAuthorizedLinks(linkJson *json.RawMessage, types string, clientInfo string, allowedTags map[string]bool) []string {
	links := []Link{}
	err := json.Unmarshal(*linkJson, &links)
	if err != nil {
		return nil
	}

	var activeRegions []service.EgressRegion
	if types == "all" {
		activeRegions = service.GetActiveEgressRegions(database.GetDB())
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
		case "local":
			if types == "all" {
				if len(allowedTags) > 0 && !allowedTags[link.Remark] {
					continue
				}
				finalLink := s.addClientInfo(cleanUri, clientInfo)
				candidates := s.ExpandEgressCandidates(finalLink, activeRegions)
				allCandidates = append(allCandidates, candidates...)
			}
		case "external", "sub":
			// Under strict security isolation: only virtual nodes pointing to dash.icta.top are emitted.
			// Raw external IPs and upstream domains are never exposed in subscriptions.
			if strings.Contains(cleanUri, "dash.icta.top") {
				proto := strings.Split(cleanUri, "://")[0]
				p, c, r, ct := service.ParseStandardRemarkComponents(cleanUri)
				allCandidates = append(allCandidates, CandidateNode{
					Uri:      cleanUri,
					Protocol: proto,
					Provider: p,
					Country:  c,
					Region:   r,
					City:     ct,
					Priority: getProtocolPriority(proto),
				})
			}
		}
	}

	// Apply smart grouping and TOP3 selection across all inbounds
	return GroupAndFilterTop3Links(allCandidates)
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
	remarkRegex := regexp.MustCompile(`^[\p{Han}a-zA-Z0-9]+-[\p{Han}a-zA-Z0-9]+-[\p{Han}a-zA-Z0-9]+-[\p{Han}a-zA-Z0-9]+-\d{2}$`)
	ipRegex := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	bannedTokens := []string{
		"原生直连", "默认出口", "智能优选", "洁净出口",
		"vmess-", "vless-", "trojan-", "tuic-", "hysteria2-",
	}
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
			if !remarkRegex.MatchString(ps) {
				violations = append(violations, fmt.Sprintf("VMess remark '%s' does not match {来源}-{国家}-{区域}-{城市}-{编号}", ps))
			}
			for _, banned := range bannedTokens {
				if strings.Contains(ps, banned) {
					violations = append(violations, fmt.Sprintf("VMess remark '%s' contains banned token '%s'", ps, banned))
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
