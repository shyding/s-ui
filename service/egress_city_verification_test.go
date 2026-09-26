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

func TestCityLevelMatchingAndEgressBinding(t *testing.T) {
	cache := GetCountryCache()
	if err := cache.ReloadFromDisk(); err != nil {
		t.Fatalf("Failed to load country cache: %v", err)
	}

	testCases := []struct {
		loc          string
		colo         string
		expectedCity string
	}{
		{"JP", "NRT", "tokyo"},
		{"NG", "LOS", "lagos"},
		{"BE", "BRU", "brussels"},
		{"AR", "EZE", "buenos aires"},
		{"US", "LAX", "los angeles"},
		{"BR", "GRU", "sao paulo"},
	}

	for _, tc := range testCases {
		servers := cache.GetCityServers(tc.loc, tc.colo)
		if len(servers) == 0 {
			t.Errorf("Expected servers for %s-%s, got 0", tc.loc, tc.colo)
			continue
		}
		foundMatch := false
		for _, s := range servers {
			if strings.Contains(normalizeCityString(s.City), tc.expectedCity) {
				foundMatch = true
				break
			}
		}
		if !foundMatch {
			t.Errorf("Expected city server matching %s for %s-%s, got %s", tc.expectedCity, tc.loc, tc.colo, servers[0].City)
		}
	}

	// Test endpoint generation with working key and address
	db := setupCFTestDB(t)
	_ = SeedInitialCloudflareEndpoints(db)

	testKey := "bXktcHJpdmF0ZS1rZXktZm9yLXZlcmlmaWNhdGlvbi0xMjM="
	testAddrs := []string{"10.2.0.5/32", "2a07:b944::2:5/128"}
	singboxCfg := &SingBoxConfig{
		Outbounds: []json.RawMessage{
			json.RawMessage(`{"type":"direct","tag":"direct"}`),
			json.RawMessage(`{"type":"direct","tag":"warp-master"}`),
		},
		Endpoints: []json.RawMessage{
			json.RawMessage(`{"type":"wireguard","tag":"warp-master","peers":[{"address":"162.159.192.1","port":2408,"public_key":"bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="}]}`),
			json.RawMessage(`{"type":"wireguard","tag":"ep-proton-us","private_key":"` + testKey + `","address":["` + testAddrs[0] + `","` + testAddrs[1] + `"],"peers":[{"address":"156.146.51.132","port":51820,"public_key":"pubkey"}]}`),
		},
	}

	EnsureCloudflarePoolsInOutbounds(singboxCfg, db)

	// Check if cf-jp-nrt-pool and cf-ng-los-pool exist and contain physical server endpoints
	foundPools := make(map[string]bool)
	for _, obRaw := range singboxCfg.Outbounds {
		var obMap map[string]interface{}
		if err := json.Unmarshal(obRaw, &obMap); err == nil {
			tag, _ := obMap["tag"].(string)
			if tag == "cf-jp-nrt-pool" || tag == "cf-ng-los-pool" || tag == "cf-be-bru-pool" {
				foundPools[tag] = true
				members, _ := obMap["outbounds"].([]interface{})
				if len(members) == 0 {
					t.Errorf("Pool %s has no outbound members", tag)
				}
			}
		}
	}

	for _, expectedPool := range []string{"cf-jp-nrt-pool", "cf-ng-los-pool", "cf-be-bru-pool"} {
		if !foundPools[expectedPool] {
			t.Errorf("Expected pool %s in outbounds", expectedPool)
		}
	}

	// Validate Sing-Box schema compliance
	fullCfgMap := map[string]interface{}{
		"log":       map[string]interface{}{"level": "info"},
		"endpoints": singboxCfg.Endpoints,
		"outbounds": singboxCfg.Outbounds,
	}
	cfgBytes, err := json.Marshal(fullCfgMap)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	var opts option.Options
	if err := json.Unmarshal(cfgBytes, &opts); err != nil {
		t.Fatalf("Sing-Box options validation failed: %v", err)
	}
}

func TestResilientEgressFailoverAndNigeriaLocalization(t *testing.T) {
	// 1. Verify Nigeria localization
	if name := GetCountryName("NG"); name != "尼日利亚" {
		t.Errorf("Expected country name for NG to be '尼日利亚', got '%s'", name)
	}

	db := setupCFTestDB(t)
	_ = SeedInitialCloudflareEndpoints(db)

	singboxCfg := &SingBoxConfig{
		Outbounds: []json.RawMessage{
			json.RawMessage(`{"type":"direct","tag":"direct"}`),
		},
		Endpoints: []json.RawMessage{
			json.RawMessage(`{"type":"wireguard","tag":"warp-master","peers":[{"address":"162.159.192.1","port":2408,"public_key":"bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo="}]}`),
			json.RawMessage(`{"type":"wireguard","tag":"ep-proton-us","private_key":"aGVsbG8td29ybGQtcHJvdG9uLXByaXZhdGUta2V5LTEyMzQ=","peers":[{"address":"156.146.51.132","port":51820,"public_key":"pubkey123"}]}`),
		},
	}

	EnsureCloudflarePoolsInOutbounds(singboxCfg, db)
	EnsureProtonPoolsInOutbounds(singboxCfg, db)

	// Verify all created urltest outbounds have warp-master as fallback and tolerance >= 800
	checkedPools := 0
	for _, obRaw := range singboxCfg.Outbounds {
		var obMap map[string]interface{}
		if err := json.Unmarshal(obRaw, &obMap); err == nil {
			if obMap["type"] == "urltest" {
				tag, _ := obMap["tag"].(string)
				tol, _ := obMap["tolerance"].(float64)
				if tol < 800 {
					t.Errorf("Pool %s expected tolerance >= 800, got %v", tag, tol)
				}
				outbounds, _ := obMap["outbounds"].([]interface{})
				hasWarp := false
				for _, o := range outbounds {
					if oStr, ok := o.(string); ok && oStr == "warp-master" {
						hasWarp = true
						break
					}
				}
				if !hasWarp {
					t.Errorf("Pool %s missing warp-master fallback outbound", tag)
				}
				checkedPools++
			}
		}
	}
	if checkedPools == 0 {
		t.Fatalf("Expected at least one urltest pool to be checked")
	}

	// Verify Sing-Box schema validation passes
	fullCfgMap := map[string]interface{}{
		"log":       map[string]interface{}{"level": "info"},
		"endpoints": singboxCfg.Endpoints,
		"outbounds": singboxCfg.Outbounds,
	}
	cfgBytes, err := json.Marshal(fullCfgMap)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}
	var opts option.Options
	if err := json.Unmarshal(cfgBytes, &opts); err != nil {
		t.Fatalf("Sing-Box options validation failed: %v", err)
	}
}
