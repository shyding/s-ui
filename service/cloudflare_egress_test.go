package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alireza0/s-ui/database/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupCFTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test in-memory sqlite: %v", err)
	}

	err = db.AutoMigrate(
		&model.CloudflareEndpoint{},
		&model.Endpoint{},
		&model.Outbound{},
	)
	if err != nil {
		t.Fatalf("Failed to auto migrate: %v", err)
	}
	return db
}

func TestParseCloudflareTrace(t *testing.T) {
	sampleTrace := `fl=741f23
h=www.cloudflare.com
ip=104.28.19.45
ts=1790343273.000
visit_scheme=https
uag=curl/8.21.0
colo=LAX
sliver=050-tier1
http=http/1.1
loc=US
tls=TLSv1.3
sni=plaintext
warp=on
gateway=off
rbi=off
kex=X25519
`
	res := ParseCloudflareTrace(sampleTrace)
	if res.Loc != "US" {
		t.Errorf("Expected Loc=US, got %s", res.Loc)
	}
	if res.Colo != "LAX" {
		t.Errorf("Expected Colo=LAX, got %s", res.Colo)
	}
	if res.IP != "104.28.19.45" {
		t.Errorf("Expected IP=104.28.19.45, got %s", res.IP)
	}
	if res.Warp != "on" {
		t.Errorf("Expected Warp=on, got %s", res.Warp)
	}
}

func TestGetCountryFlagAndName_Dynamic(t *testing.T) {
	testCases := []struct {
		loc          string
		expectedFlag string
		expectedName string
	}{
		{"US", "🇺🇸", "美国"},
		{"JP", "🇯🇵", "日本"},
		{"SG", "🇸🇬", "新加坡"},
		{"HK", "🇭🇰", "中国香港"},
		{"GB", "🇬🇧", "英国"},
		{"DE", "🇩🇪", "德国"},
		{"FR", "🇫🇷", "法国"},
		{"NL", "🇳🇱", "荷兰"},
		{"CA", "🇨🇦", "加拿大"},
		{"AU", "🇦🇺", "澳大利亚"},
		{"KR", "🇰🇷", "韩国"},
		{"TW", "🇹🇼", "中国台湾"},
		{"br", "🇧🇷", "巴西"}, // case insensitive
	}

	for _, tc := range testCases {
		flag := GetCountryFlag(tc.loc)
		if flag != tc.expectedFlag {
			t.Errorf("For %s, expected flag %s, got %s", tc.loc, tc.expectedFlag, flag)
		}
		name := GetCountryName(tc.loc)
		if name != tc.expectedName {
			t.Errorf("For %s, expected name %s, got %s", tc.loc, tc.expectedName, name)
		}
	}
}

func TestSeedInitialCloudflareEndpoints_AndDynamicRegions(t *testing.T) {
	db := setupCFTestDB(t)

	err := SeedInitialCloudflareEndpoints(db)
	if err != nil {
		t.Fatalf("SeedInitialCloudflareEndpoints failed: %v", err)
	}

	var count int64
	db.Model(&model.CloudflareEndpoint{}).Count(&count)
	if count < 10 {
		t.Errorf("Expected at least 10 seeded endpoints, got %d", count)
	}

	// Test "有多少区分多少"
	regions := GetActiveCloudflareRegions(db)
	if len(regions) < 8 {
		t.Errorf("Expected at least 8 distinct country regions, got %d", len(regions))
	}

	// Verify each region has proper Code, Name, Flag, and OutboundTag
	seenCodes := make(map[string]bool)
	for _, reg := range regions {
		if !strings.HasPrefix(reg.Code, "cf-") {
			t.Errorf("Region Code must have prefix 'cf-', got %s", reg.Code)
		}
		if !strings.HasPrefix(reg.OutboundTag, "cf-") || !strings.HasSuffix(reg.OutboundTag, "-pool") {
			t.Errorf("OutboundTag must match pattern 'cf-<loc>-pool', got %s", reg.OutboundTag)
		}
		if reg.Flag == "" || reg.Flag == "🌐" {
			t.Errorf("Region %s has invalid flag: %s", reg.Code, reg.Flag)
		}
		if !strings.Contains(reg.Name, "Cloudflare洁净出口") {
			t.Errorf("Region %s name should mention Cloudflare: %s", reg.Code, reg.Name)
		}
		seenCodes[reg.Code] = true
	}

	// Check core countries are present (including Latin America, Africa, Middle East)
	for _, expectedCode := range []string{"cf-us", "cf-jp", "cf-sg", "cf-gb", "cf-de", "cf-nl", "cf-fr", "cf-hk", "cf-br", "cf-ar", "cf-cl", "cf-ng", "cf-za", "cf-tr"} {
		if !seenCodes[expectedCode] {
			t.Errorf("Expected discovered region %s to be present", expectedCode)
		}
	}
}

func TestEnsureCloudflarePoolsInOutbounds(t *testing.T) {
	db := setupCFTestDB(t)
	_ = SeedInitialCloudflareEndpoints(db)

	singboxConfig := &SingBoxConfig{
		Outbounds: []json.RawMessage{
			json.RawMessage(`{"type":"direct","tag":"direct"}`),
			json.RawMessage(`{"type":"direct","tag":"warp-6eV"}`),
		},
		Endpoints: []json.RawMessage{
			json.RawMessage(`{"type":"warp","tag":"warp-6eV"}`),
		},
	}

	EnsureCloudflarePoolsInOutbounds(singboxConfig, db)

	// Verify outbounds now contain cf-*-pool
	foundUSPool := false
	foundJPPool := false
	foundGBPool := false

	for _, obRaw := range singboxConfig.Outbounds {
		var obMap map[string]interface{}
		if err := json.Unmarshal(obRaw, &obMap); err == nil {
			tag, _ := obMap["tag"].(string)
			if tag == "cf-us-pool" {
				foundUSPool = true
				if obMap["type"] != "urltest" {
					t.Errorf("cf-us-pool must be type urltest, got %v", obMap["type"])
				}
			}
			if tag == "cf-jp-pool" {
				foundJPPool = true
			}
			if tag == "cf-gb-pool" {
				foundGBPool = true
			}
		}
	}

	if !foundUSPool {
		t.Errorf("Expected cf-us-pool in Outbounds")
	}
	if !foundJPPool {
		t.Errorf("Expected cf-jp-pool in Outbounds")
	}
	if !foundGBPool {
		t.Errorf("Expected cf-gb-pool in Outbounds")
	}

	foundDirectWrap := false
	for _, obRaw := range singboxConfig.Outbounds {
		var obMap map[string]interface{}
		if err := json.Unmarshal(obRaw, &obMap); err == nil {
			tag, _ := obMap["tag"].(string)
			if strings.HasPrefix(tag, "out-ep-cf-") || strings.HasPrefix(tag, "out-warp-") {
				foundDirectWrap = true
				if obMap["type"] != "direct" {
					t.Errorf("Endpoint wrapper %s must be direct outbound, got %v", tag, obMap["type"])
				}
				if obMap["endpoint"] == nil && obMap["detour"] == nil {
					t.Errorf("Endpoint wrapper %s must have endpoint/detour field", tag)
				}
			}
		}
	}
	if !foundDirectWrap {
		t.Errorf("Expected direct outbounds wrapping endpoints (out-ep-cf-* or out-warp-*) in Outbounds")
	}
}

func TestGetActiveEgressRegions_IntegrationWithCloudflare(t *testing.T) {
	db := setupCFTestDB(t)
	_ = SeedInitialCloudflareEndpoints(db)

	active := GetActiveEgressRegions(db)
	if len(active) < 8 {
		t.Errorf("Expected at least 8 active regions, got %d", len(active))
	}

	// Verify client routing injection
	routes := InjectEgressRouteRulesForClients(nil, []string{"my", "admin"}, active)
	if len(routes) < len(active)*2 {
		t.Errorf("Expected routes for all clients and all regions, got %d rules", len(routes))
	}
}
