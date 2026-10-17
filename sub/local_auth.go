package sub

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/alireza0/s-ui/database/model"
)

// authorizedLocalLinks updates only stored local links which match an already
// authorized inbound's tag, endpoint and protocol. No port-only database lookup.
func (j *JsonService) authorizedLocalLinks(client *model.Client, inbounds []*model.Inbound) []string {
	var links []Link
	var configs map[string]interface{}
	if json.Unmarshal(client.Links, &links) != nil || json.Unmarshal(client.Config, &configs) != nil {
		return j.GetLocalLinks(&client.Links, "", nil)
	}
	for i := range links {
		link := &links[i]
		if link.Type != "local" {
			continue
		}
		u, err := url.Parse(link.Uri)
		if err != nil || (u.Scheme != "socks" && u.Scheme != "socks5" && u.Scheme != "http") {
			// Local links for VMess/Hysteria2/etc. are still valid subscription
			// entries; only SOCKS/HTTP links participate in credential syncing.
			continue
		}
		for _, in := range inbounds {
			// Coverage SOCKS inbounds are explicitly anonymous at runtime.
			// Do not inject client credentials into their stored links.
			if in.Type == "socks" && strings.HasPrefix(in.Tag, "coverage-socks-") {
				continue
			}
			if link.Remark != in.Tag || (in.Type != "mixed" && in.Type != "socks" && in.Type != "http") {
				continue
			}
			if in.Type == "socks" && u.Scheme == "http" || in.Type == "http" && u.Scheme != "http" {
				continue
			}
			if !matchesLocalEndpoint(u, in) {
				continue
			}
			cfg, _ := configs[in.Type].(map[string]interface{})
			if in.Type == "mixed" && configs[in.Type] == nil {
				cfg, _ = configs["socks"].(map[string]interface{})
			}
			user, _ := cfg["username"].(string)
			pass, _ := cfg["password"].(string)
			if user != "" {
				u.User = url.UserPassword(user, pass)
				link.Uri = u.String()
			}
			break
		}
	}
	raw, err := json.Marshal(links)
	if err != nil {
		return j.GetLocalLinks(&client.Links, "", nil)
	}
	msg := json.RawMessage(raw)
	return j.GetLocalLinks(&msg, "", nil)
}

func matchesLocalEndpoint(u *url.URL, in *model.Inbound) bool {
	normalize := func(host string) string {
		host = strings.ToLower(host)
		if host == "dash.icta.qzz.io" || host == "sub.icta.qzz.io" {
			return "dash.icta.top"
		}
		return host
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		return false
	}
	var opts struct {
		Port int `json:"listen_port"`
	}
	if json.Unmarshal(in.Options, &opts) == nil && normalize(u.Hostname()) == "dash.icta.top" && opts.Port == port {
		return true
	}
	var endpoints []struct {
		Host string `json:"server"`
		Port int    `json:"server_port"`
	}
	_ = json.Unmarshal(in.Addrs, &endpoints)
	var primary struct {
		Host string `json:"server"`
		Port int    `json:"server_port"`
	}
	if json.Unmarshal(in.OutJson, &primary) == nil {
		endpoints = append(endpoints, primary)
	}
	for _, ep := range endpoints {
		if ep.Port == port && ep.Host != "" && normalize(ep.Host) == normalize(u.Hostname()) {
			return true
		}
	}
	return false
}
