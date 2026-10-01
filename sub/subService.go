package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"github.com/alireza0/s-ui/service"
	"github.com/alireza0/s-ui/util"
)

type SubService struct {
	service.SettingService
	LinkService
}

// VPS Seed egress port forwarding.
// See service/egress_gateway.go for mapping generation and iptables management.
// Client sees dash.icta.top:VPS_PORT, VPS forwards via iptables DNAT to real Seed.

// rewriteEgressURIsViaVPS rewrites Seed/Cloudflare egress URIs to use VPS port
// forwarding. The original URI's host:port is replaced with dash.icta.top:VPS_PORT,
// where VPS_PORT forwards via iptables DNAT to the real Seed address.
// This hides egress real addresses from clients while preserving node count.
func rewriteEgressURIsViaVPS(links []string) []string {
	// Use the egress gateway's lookup which handles hostname/IP matching
	var result []string
	for _, link := range links {
		rewritten := rewriteSingleEgressURI(link)
		result = append(result, rewritten)
	}
	return result
}

func rewriteSingleEgressURI(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return link
	}
	// Skip if already dash.icta.top (SUI ingress, no rewrite needed)
	if strings.Contains(link, "dash.icta.top") {
		return link
	}

	var host, port string
	var isVMess bool
	var vmessObj map[string]interface{}

	if strings.HasPrefix(link, "vmess://") {
		isVMess = true
		rawB64 := strings.TrimPrefix(link, "vmess://")
		fragment := ""
		if idx := strings.Index(rawB64, "#"); idx != -1 {
			fragment = rawB64[idx:]
			rawB64 = rawB64[:idx]
		}
		decoded, err := util.B64StrToByte(rawB64)
		if err != nil {
			return link
		}
		if err := json.Unmarshal(decoded, &vmessObj); err != nil {
			return link
		}
		host, _ = vmessObj["add"].(string)
		switch p := vmessObj["port"].(type) {
		case float64:
			port = fmt.Sprintf("%.0f", p)
		case string:
			port = p
		case int:
			port = fmt.Sprintf("%d", p)
		}
		_ = fragment
	} else {
		// Parse as URL: vless://, trojan://, ss://, tuic://, hysteria2://, etc.
		u, err := url.Parse(link)
		if err != nil {
			return link
		}
		host = u.Hostname()
		port = u.Port()
		if port == "" {
			// Default ports by scheme
			switch u.Scheme {
			case "https":
				port = "443"
			case "http":
				port = "80"
			default:
				port = "443"
			}
		}
	}

	// Look up VPS port for this egress host:port
	// Uses egress gateway which matches both original hostname and resolved IP
	vpsPort := service.GetEgressVpsPort(host, port)
	if vpsPort == 0 {
		// No mapping found, return as-is (will be filtered by security check)
		return link
	}

	// Rewrite to dash.icta.top:VPS_PORT
	if isVMess {
		vmessObj["add"] = "dash.icta.top"
		vmessObj["port"] = vpsPort
		// Also update port as string if it was string
		newJSON, err := json.Marshal(vmessObj)
		if err != nil {
			return link
		}
		fragment := ""
		if idx := strings.Index(link, "#"); idx != -1 {
			fragment = link[idx:]
		}
		return "vmess://" + base64.StdEncoding.EncodeToString(newJSON) + fragment
	}
	// For URL-based URIs, replace host:port via string replacement
	// (preserves original URI formatting exactly)
	oldHostPort := host + ":" + port
	if strings.Contains(link, "["+host+"]:"+port) {
		oldHostPort = "[" + host + "]:" + port
	}
	newHostPort := fmt.Sprintf("dash.icta.top:%d", vpsPort)
	return strings.Replace(link, oldHostPort, newHostPort, 1)
}

func (s *SubService) GetSubs(subId string) (*string, []string, error) {
	var err error

	client, err := s.getClientBySubId(subId)
	if err != nil {
		return nil, nil, err
	}

	clientInfo := ""
	subShowInfo, _ := s.SettingService.GetSubShowInfo()
	if subShowInfo {
		clientInfo = s.getClientInfo(client)
	}

	var clientInbounds []uint
	_ = json.Unmarshal(client.Inbounds, &clientInbounds)

	var allowedTags map[string]bool
	if len(clientInbounds) > 0 {
		allowedTags = make(map[string]bool)
		var activeTags []string
		db := database.GetDB()
		db.Model(&model.Inbound{}).Where("id in ?", clientInbounds).Pluck("tag", &activeTags)
		for _, tag := range activeTags {
			allowedTags[tag] = true
		}
	}

	linksArray := s.LinkService.GetAuthorizedLinks(&client.Links, "all", clientInfo, allowedTags)

	// Rewrite Seed/Cloudflare egress URIs to use VPS port forwarding.
	// Client sees dash.icta.top:VPS_PORT, VPS forwards to Seed via iptables DNAT.
	// This preserves node count while hiding egress real addresses.
	linksArray = rewriteEgressURIsViaVPS(linksArray)

	// Enforce strict subscription security isolation: only allow links connecting to dash.icta.top
	var secureLinks []string
	for _, l := range linksArray {
		if pass, violations := ValidateSubscriptionSecurity([]string{l}, "dash.icta.top"); pass {
			secureLinks = append(secureLinks, l)
		} else {
			logger.Warning("Subscription security isolation filtered out non-VPS link:", violations)
		}
	}
	linksArray = secureLinks

	result := strings.Join(linksArray, "\n")
	result = strings.ReplaceAll(result, "dash.icta.qzz.io", "dash.icta.top")
	result = strings.ReplaceAll(result, "sub.icta.qzz.io", "dash.icta.top")

	headers := s.getClientHeaders(client)

	subEncode, _ := s.SettingService.GetSubEncode()
	if subEncode {
		result = base64.StdEncoding.EncodeToString([]byte(result))
	}

	return &result, headers, nil
}

func (j *SubService) getClientBySubId(subId string) (*model.Client, error) {
	db := database.GetDB()
	client := &model.Client{}
	err := db.Model(model.Client{}).Where("enable = true and name = ?", subId).First(client).Error
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (s *SubService) getClientHeaders(client *model.Client) []string {
	updateInterval, _ := s.SettingService.GetSubUpdates()
	return util.GetHeaders(client, updateInterval)
}

func (s *SubService) getClientInfo(c *model.Client) string {
	now := time.Now().Unix()

	var result []string
	if vol := c.Volume - (c.Up + c.Down); vol > 0 {
		result = append(result, fmt.Sprintf("%s%s", s.formatTraffic(vol), "📊"))
	}
	if c.Expiry > 0 {
		result = append(result, fmt.Sprintf("%d%s⏳", (c.Expiry-now)/86400, "Days"))
	}
	if len(result) > 0 {
		return " " + strings.Join(result, " ")
	} else {
		return " ♾"
	}
}

func (s *SubService) formatTraffic(trafficBytes int64) string {
	if trafficBytes < 1024 {
		return fmt.Sprintf("%.2fB", float64(trafficBytes)/float64(1))
	} else if trafficBytes < (1024 * 1024) {
		return fmt.Sprintf("%.2fKB", float64(trafficBytes)/float64(1024))
	} else if trafficBytes < (1024 * 1024 * 1024) {
		return fmt.Sprintf("%.2fMB", float64(trafficBytes)/float64(1024*1024))
	} else if trafficBytes < (1024 * 1024 * 1024 * 1024) {
		return fmt.Sprintf("%.2fGB", float64(trafficBytes)/float64(1024*1024*1024))
	} else if trafficBytes < (1024 * 1024 * 1024 * 1024 * 1024) {
		return fmt.Sprintf("%.2fTB", float64(trafficBytes)/float64(1024*1024*1024*1024))
	} else {
		return fmt.Sprintf("%.2fEB", float64(trafficBytes)/float64(1024*1024*1024*1024*1024))
	}
}
