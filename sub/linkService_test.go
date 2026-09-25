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

	if len(expanded) != 4 {
		t.Fatalf("Expected 4 expanded links, got %d", len(expanded))
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

	if len(expanded) != 4 {
		t.Fatalf("Expected 4 expanded links, got %d", len(expanded))
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
