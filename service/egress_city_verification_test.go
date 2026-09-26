package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sagernet/sing-box/option"
)

func TestCityLevelEgressAndProtonKeyExtraction(t *testing.T) {
	db := setupCFTestDB(t)
	_ = SeedInitialCloudflareEndpoints(db)

	// 1. Verify city-level Cloudflare regions
	cfRegions := GetActiveCloudflareRegions(db)
	if len(cfRegions) == 0 {
		t.Fatalf("Expected non-empty active Cloudflare regions")
	}

	foundCity := false
	for _, reg := range cfRegions {
		if strings.Contains(reg.Name, "·") { // e.g. "比利时·布鲁塞尔-Cloudflare洁净出口"
			foundCity = true
			break
		}
	}
	if !foundCity {
		t.Errorf("Expected at least one city-level Cloudflare region with '·' in displayName")
	}

	// 2. Simulate Sing-Box config with an existing Proton endpoint
	validKey := "aGVsbG8td29ybGQtcHJvdG9uLXByaXZhdGUta2V5LTEyMzQ="
	singboxConfig := &SingBoxConfig{
		Outbounds: []json.RawMessage{
			json.RawMessage(`{"type":"direct","tag":"direct"}`),
			json.RawMessage(`{"type":"direct","tag":"warp-master"}`),
		},
		Endpoints: []json.RawMessage{
			json.RawMessage(`{"type":"wireguard","tag":"warp-master","peers":[{"address":"162.159.192.1","port":2408,"public_key":"bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="}]}`),
			json.RawMessage(`{"type":"wireguard","tag":"ep-proton-us-free-1","private_key":"` + validKey + `","peers":[{"address":"156.146.51.132","port":51820,"public_key":"testpub"}]}`),
		},
	}

	// 3. Run EnsureCloudflarePoolsInOutbounds
	EnsureCloudflarePoolsInOutbounds(singboxConfig, db)

	// 4. Run EnsureProtonPoolsInOutbounds
	EnsureProtonPoolsInOutbounds(singboxConfig, db)

	// 5. Verify JP pool was created and dynamically extracted validKey
	foundJPPool := false
	for _, obRaw := range singboxConfig.Outbounds {
		var obMap map[string]interface{}
		if err := json.Unmarshal(obRaw, &obMap); err == nil {
			if tag, ok := obMap["tag"].(string); ok && tag == "jp-pool" {
				foundJPPool = true
				break
			}
		}
	}
	if !foundJPPool {
		t.Errorf("Expected jp-pool to be created in Outbounds")
	}

	// Verify at least one dynamic JP endpoint was added with validKey
	foundDynJPEp := false
	for _, epRaw := range singboxConfig.Endpoints {
		var epMap map[string]interface{}
		if err := json.Unmarshal(epRaw, &epMap); err == nil {
			tag, _ := epMap["tag"].(string)
			if strings.HasPrefix(tag, "ep-dyn-jp-") {
				foundDynJPEp = true
				pk, _ := epMap["private_key"].(string)
				if pk != validKey {
					t.Errorf("Expected dynamic JP endpoint to inherit active Proton key %s, got %s", validKey, pk)
				}
			}
		}
	}
	if !foundDynJPEp {
		t.Errorf("Expected dynamic JP endpoint ep-dyn-jp-* to be created in Endpoints")
	}

	// 6. Validate the entire generated SingBox config against Sing-Box schema
	fullCfgMap := map[string]interface{}{
		"log":       map[string]interface{}{"level": "info"},
		"endpoints": singboxConfig.Endpoints,
		"outbounds": singboxConfig.Outbounds,
	}
	cfgBytes, err := json.Marshal(fullCfgMap)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	var opts option.Options
	if err := json.Unmarshal(cfgBytes, &opts); err != nil {
		t.Fatalf("Generated Sing-Box configuration failed schema validation: %v", err)
	}
	if len(opts.Endpoints) == 0 {
		t.Errorf("Expected non-empty parsed endpoints in Sing-Box options")
	}
	if len(opts.Outbounds) == 0 {
		t.Errorf("Expected non-empty parsed outbounds in Sing-Box options")
	}
}
