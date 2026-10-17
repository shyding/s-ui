package sub

import (
	"encoding/json"
	"testing"

	"github.com/alireza0/s-ui/database/model"
	"gopkg.in/yaml.v3"
)

func TestMixedSubscriptionAuthMatchesRuntime(t *testing.T) {
	for _, tc := range []struct{ name, config, user, password string }{
		{"missing mixed", `{"socks":{"username":"u","password":"p"}}`, "u", "p"},
		{"null mixed", `{"mixed":null,"socks":{"username":"u","password":"p"}}`, "u", "p"},
		{"explicit mixed", `{"mixed":{"username":"m","password":"mp"},"socks":{"username":"u","password":"p"}}`, "m", "mp"},
	} {
		for _, addrs := range []string{`[]`, `[{"server":"alternate.example","server_port":54156}]`} {
			t.Run(tc.name+addrs, func(t *testing.T) {
				inbound := &model.Inbound{Type: "mixed", Addrs: json.RawMessage(addrs),
					OutJson: json.RawMessage(`{"type":"mixed","tag":"mixed-54156","server":"example.test","server_port":54156,"username":"stale","password":"stale"}`)}
				outbounds, _, err := (&JsonService{}).getOutbounds(json.RawMessage(tc.config), []*model.Inbound{inbound})
				if err != nil {
					t.Fatal(err)
				}
				if len(*outbounds) != 2 {
					t.Fatalf("got %d outbounds", len(*outbounds))
				}
				for i, kind := range []string{"socks", "http"} {
					ob := (*outbounds)[i]
					if ob["type"] != kind || ob["username"] != tc.user || ob["password"] != tc.password {
						t.Fatalf("%s credentials differ from runtime", kind)
					}
				}
				clash, err := (&ClashService{}).ConvertToClashMeta(outbounds)
				if err != nil {
					t.Fatal(err)
				}
				var config struct {
					Proxies []map[string]interface{} `yaml:"proxies"`
				}
				if err := yaml.Unmarshal(clash, &config); err != nil {
					t.Fatal(err)
				}
				if len(config.Proxies) != 2 {
					t.Fatalf("got %d Clash proxies", len(config.Proxies))
				}
				for i, kind := range []string{"socks5", "http"} {
					ob := config.Proxies[i]
					if ob["type"] != kind || ob["username"] != tc.user || ob["password"] != tc.password {
						t.Fatalf("Clash %s credentials differ from runtime", kind)
					}
				}
			})
		}
	}
}
