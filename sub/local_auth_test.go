package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alireza0/s-ui/database/model"
)

func TestAuthorizedLocalLinksCredentialScope(t *testing.T) {
	for _, tc := range []struct {
		name, kind, tag, host string
		changed               bool
	}{
		{"authorized native", "local", "mixed-54156", "dash.icta.top", true},
		{"legacy native", "local", "mixed-54156", "dash.icta.qzz.io", true},
		{"external same port", "external", "mixed-54156", "dash.icta.top", false},
		{"foreign same port", "local", "mixed-54156", "foreign.example", false},
		{"unauthorized tag", "local", "other", "dash.icta.top", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			links, _ := json.Marshal([]Link{{Type: tc.kind, Remark: tc.tag, Uri: "socks5://old:old@" + tc.host + ":54156"}})
			client := &model.Client{Config: json.RawMessage(`{"socks":{"username":"current","password":"current-pass"}}`), Links: links}
			inbound := &model.Inbound{Type: "mixed", Tag: "mixed-54156", Options: json.RawMessage(`{"listen_port":54156}`), Addrs: json.RawMessage(`[]`)}
			got := (&JsonService{}).authorizedLocalLinks(client, []*model.Inbound{inbound})
			if tc.kind != "local" {
				if len(got) != 0 {
					t.Fatal("external link leaked into local subscription")
				}
				return
			}
			if len(got) != 1 || strings.Contains(got[0], "current:current-pass@") != tc.changed {
				t.Fatal("credential rewrite escaped scope or missed native link")
			}
		})
	}
}

func TestAuthorizedLocalLinksPreservesOtherLocalProtocols(t *testing.T) {
	links, _ := json.Marshal([]Link{{
		Type: "local", Remark: "vmess-2083", Uri: "vmess://eyJhZGQiOiJkYXNoLmljdGEudG9wIiwicG9ydCI6MjA4M30=",
	}})
	client := &model.Client{Config: json.RawMessage(`{"socks":{"username":"u","password":"p"}}`), Links: links}
	got := (&JsonService{}).authorizedLocalLinks(client, nil)
	if len(got) != 1 || !strings.HasPrefix(got[0], "vmess://") {
		t.Fatalf("non-SOCKS local link was dropped: %#v", got)
	}
}
