package sub

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/alireza0/s-ui/service"
	"github.com/alireza0/s-ui/util"
)

func TestExpandEgressLinks_VMess(t *testing.T) {
	s := &LinkService{}

	baseObj := map[string]interface{}{
		"v":    "2",
		"ps":   "vmess-in-2096",
		"add":  "dash.icta.top",
		"port": "2096",
		"id":   "403db7be-930b-449e-b5f4-34537cb594c7",
		"aid":  0,
		"net":  "ws",
		"type": "none",
		"host": "dash.icta.top",
		"path": "/ws",
		"tls":  "tls",
	}
	baseRaw, _ := json.Marshal(baseObj)
	baseUri := "vmess://" + util.ByteToB64Str(baseRaw)

	expanded := s.ExpandEgressLinks(baseUri, service.StandardEgressRegions)

	if len(expanded) != 5 {
		t.Fatalf("Expected 5 expanded links (1 native direct + 4 regional), got %d", len(expanded))
	}
	raw0, _ := util.B64StrToByte(strings.TrimPrefix(expanded[0], "vmess://"))
	if !strings.Contains(string(raw0), "原生直连") {
		t.Errorf("First node must be native direct node, got %s", string(raw0))
	}

	// Verify each expanded link
	for _, link := range expanded {
		parts := strings.Split(link, "://")
		if len(parts) != 2 || parts[0] != "vmess" {
			t.Fatalf("Malformed vmess link: %s", link)
		}
		raw, err := util.B64StrToByte(parts[1])
		if err != nil {
			t.Fatalf("Failed to decode vmess base64: %v", err)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("Failed to unmarshal vmess payload: %v", err)
		}

		ps := obj["ps"].(string)
		id := obj["id"].(string)
		port := obj["port"].(string)
		add := obj["add"].(string)

		// Port and address MUST be identical
		if port != "2096" || add != "dash.icta.top" {
			t.Errorf("Port or Addr altered: port=%s, add=%s", port, add)
		}

		if strings.Contains(ps, "US") {
			expectedUUID := service.DeriveUUID("403db7be-930b-449e-b5f4-34537cb594c7", "us")
			if id != expectedUUID {
				t.Errorf("US node UUID mismatch: got %s, want %s", id, expectedUUID)
			}
		} else if strings.Contains(ps, "SG") {
			if id != "403db7be-930b-449e-b5f4-34537cb594c7" {
				t.Errorf("SG node UUID should equal base UUID: %s", id)
			}
		}
	}
}

func TestExpandEgressLinks_VLESS(t *testing.T) {
	s := &LinkService{}

	baseUUID := "403db7be-930b-449e-b5f4-34537cb594c7"
	baseUri := "vless://" + baseUUID + "@dash.icta.top:2096?security=tls&type=ws&path=%2Fws#vmess-in"

	expanded := s.ExpandEgressLinks(baseUri, service.StandardEgressRegions)

	if len(expanded) != 5 {
		t.Fatalf("Expected 5 expanded links (1 native direct + 4 regional), got %d", len(expanded))
	}
	u0, _ := url.Parse(expanded[0])
	if !strings.Contains(u0.Fragment, "原生直连") {
		t.Errorf("First node must be native direct node, got %s", u0.Fragment)
	}

	for _, link := range expanded {
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("Failed to parse link: %v", err)
		}
		if u.Host != "dash.icta.top:2096" {
			t.Errorf("Host mismatch: %s", u.Host)
		}
		if strings.Contains(u.Fragment, "US") {
			expectedUUID := service.DeriveUUID(baseUUID, "us")
			if u.User.Username() != expectedUUID {
				t.Errorf("US node UUID mismatch: got %s, want %s", u.User.Username(), expectedUUID)
			}
		}
	}
}

func TestExpandEgressLinks_DynamicCloudflareRegions(t *testing.T) {
	s := &LinkService{}

	baseUUID := "403db7be-930b-449e-b5f4-34537cb594c7"
	baseUri := "vless://" + baseUUID + "@dash.icta.top:2096?security=tls&type=ws&path=%2Fws#vmess-in"

	// Dynamically simulated Cloudflare regions ("有多少区分多少")
	dynamicCFRegions := []service.EgressRegion{
		{Code: "cf-us", Name: "美国-Cloudflare洁净出口", Flag: "🇺🇸", OutboundTag: "cf-us-pool"},
		{Code: "cf-jp", Name: "日本-Cloudflare洁净出口", Flag: "🇯🇵", OutboundTag: "cf-jp-pool"},
		{Code: "cf-sg", Name: "新加坡-Cloudflare洁净出口", Flag: "🇸🇬", OutboundTag: "cf-sg-pool"},
		{Code: "cf-hk", Name: "中国香港-Cloudflare洁净出口", Flag: "🇭🇰", OutboundTag: "cf-hk-pool"},
		{Code: "cf-gb", Name: "英国-Cloudflare洁净出口", Flag: "🇬🇧", OutboundTag: "cf-gb-pool"},
		{Code: "cf-de", Name: "德国-Cloudflare洁净出口", Flag: "🇩🇪", OutboundTag: "cf-de-pool"},
		{Code: "cf-nl", Name: "荷兰-Cloudflare洁净出口", Flag: "🇳🇱", OutboundTag: "cf-nl-pool"},
		{Code: "cf-fr", Name: "法国-Cloudflare洁净出口", Flag: "🇫🇷", OutboundTag: "cf-fr-pool"},
		{Code: "cf-ca", Name: "加拿大-Cloudflare洁净出口", Flag: "🇨🇦", OutboundTag: "cf-ca-pool"},
		{Code: "cf-au", Name: "澳大利亚-Cloudflare洁净出口", Flag: "🇦🇺", OutboundTag: "cf-au-pool"},
	}

	expanded := s.ExpandEgressLinks(baseUri, dynamicCFRegions)

	if len(expanded) != 1+len(dynamicCFRegions) {
		t.Fatalf("Expected %d expanded links (1 native direct + %d dynamic Cloudflare regions), got %d",
			1+len(dynamicCFRegions), len(dynamicCFRegions), len(expanded))
	}
	u0, _ := url.Parse(expanded[0])
	if !strings.Contains(u0.Fragment, "原生直连") {
		t.Errorf("First node must be native direct node, got %s", u0.Fragment)
	}

	for _, link := range expanded[1:] {
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("Failed to parse link: %v", err)
		}
		if u.Host != "dash.icta.top:2096" {
			t.Errorf("Host mismatch: %s", u.Host)
		}
		if !strings.Contains(u.Fragment, "Cloudflare") {
			t.Errorf("Expected link fragment to mention Cloudflare: %s", u.Fragment)
		}
	}
}

