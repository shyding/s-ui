package sub

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

func (s *LinkService) GetAuthorizedLinks(linkJson *json.RawMessage, types string, clientInfo string, allowedTags map[string]bool) []string {
	links := []Link{}
	var result []string
	seen := make(map[string]bool)
	err := json.Unmarshal(*linkJson, &links)
	if err != nil {
		return nil
	}
	for _, link := range links {
		// Filter out obsolete/unsupported protocols that standard clients cannot import
		if strings.HasPrefix(link.Uri, "http2://") {
			continue
		}
		// Sanitize legacy unresolvable domains
		cleanUri := strings.ReplaceAll(link.Uri, "dash.icta.qzz.io", "dash.icta.top")
		cleanUri = strings.ReplaceAll(cleanUri, "sub.icta.qzz.io", "dash.icta.top")

		switch link.Type {
		case "external":
			if !seen[cleanUri] {
				seen[cleanUri] = true
				result = append(result, cleanUri)
			}
		case "sub":
			for _, subLink := range s.getExternalSub(link.Uri) {
				subLink = strings.ReplaceAll(subLink, "dash.icta.qzz.io", "dash.icta.top")
				subLink = strings.ReplaceAll(subLink, "sub.icta.qzz.io", "dash.icta.top")
				if !seen[subLink] {
					seen[subLink] = true
					result = append(result, subLink)
				}
			}
		case "local":
			if types == "all" {
				// Prevent returning local links for inbounds the client is not currently authorized for
				if allowedTags != nil && !allowedTags[link.Remark] {
					continue
				}
				finalLink := s.addClientInfo(cleanUri, clientInfo)
				activeRegions := service.GetActiveEgressRegions(database.GetDB())
				expandedLinks := s.ExpandEgressLinks(finalLink, activeRegions)
				for _, expLink := range expandedLinks {
					if !seen[expLink] {
						seen[expLink] = true
						result = append(result, expLink)
					}
				}
			}
		}
	}
	return result
}

// ExpandEgressLinks expands a base inbound link across active country egress pools
func (s *LinkService) ExpandEgressLinks(uri string, activeRegions []service.EgressRegion) []string {
	if len(activeRegions) == 0 {
		activeRegions = service.StandardEgressRegions
	}
	protocol := strings.Split(uri, "://")
	if len(protocol) < 2 {
		return []string{uri}
	}

	proto := protocol[0]
	switch proto {
	case "vmess":
		var vmessJson map[string]interface{}
		config, err := util.B64StrToByte(protocol[1])
		if err != nil {
			return []string{uri}
		}
		if err := json.Unmarshal(config, &vmessJson); err != nil {
			return []string{uri}
		}
		origPS, _ := vmessJson["ps"].(string)
		origUUID, _ := vmessJson["id"].(string)

		var expanded []string
		// 1. Always preserve the original native direct entry node
		origMap := make(map[string]interface{})
		for k, v := range vmessJson {
			origMap[k] = v
		}
		origMap["ps"] = fmt.Sprintf("🌐 [原生直连] 默认出口 - %s", origPS)
		if raw, err := json.MarshalIndent(origMap, "", "  "); err == nil {
			expanded = append(expanded, "vmess://"+util.ByteToB64Str(raw))
		} else {
			expanded = append(expanded, uri)
		}

		for _, reg := range activeRegions {
			copyMap := make(map[string]interface{})
			for k, v := range vmessJson {
				copyMap[k] = v
			}
			copyMap["ps"] = fmt.Sprintf("%s [%s] %s - %s", reg.Flag, strings.ToUpper(reg.Code), reg.Name, origPS)
			if reg.Code != "" && reg.Code != "sg" {
				copyMap["id"] = service.DeriveUUID(origUUID, reg.Code)
			}
			if raw, err := json.MarshalIndent(copyMap, "", "  "); err == nil {
				expanded = append(expanded, "vmess://"+util.ByteToB64Str(raw))
			}
		}
		if len(expanded) > 0 {
			return expanded
		}
	case "vless", "trojan":
		u, err := url.Parse(uri)
		if err != nil {
			return []string{uri}
		}
		origRemark := u.Fragment
		origUser := u.User.Username()
		origPass, hasPass := u.User.Password()

		var expanded []string
		// 1. Always preserve the original native direct entry node
		origU := *u
		origU.Fragment = fmt.Sprintf("🌐 [原生直连] 默认出口 - %s", origRemark)
		expanded = append(expanded, origU.String())

		for _, reg := range activeRegions {
			newU := *u
			newU.Fragment = fmt.Sprintf("%s [%s] %s - %s", reg.Flag, strings.ToUpper(reg.Code), reg.Name, origRemark)
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
				}
			}
			expanded = append(expanded, newU.String())
		}
		if len(expanded) > 0 {
			return expanded
		}
	}

	return []string{uri}
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
