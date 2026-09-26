package service

import (
	"encoding/json"
	"testing"

	"github.com/alireza0/s-ui/database/model"
	"github.com/glebarez/sqlite"
	"github.com/sagernet/sing-box/option"
	"gorm.io/gorm"
)

const sampleUS9Conf = `[Interface]
# Key for sui-node
# Bouncing = 0
# NAT-PMP (Port Forwarding) = off
# VPN Accelerator = on
PrivateKey = sDxK8N77o0zC7vjfJ3Yoxe/O4Nmgct8HtAwyS7pflmg=
Address = 10.2.0.2/32, 2a07:b944::2:2/128
DNS = 10.2.0.1, 2a07:b944::2:1

[Peer]
# US-FREE#9
PublicKey = o3BjGF2/GcEQiWoP89XgDfXFVlFFm7ipBFVQaOCR00k=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 89.187.180.1:51820
PersistentKeepalive = 25
`

const sampleUS53Conf = `[Interface]
PrivateKey = sBLOlMMZVXAEeLdvqsjEkBzxzO13EHgHlw2Y2ltpOFY=
Address = 10.2.0.2/32, 2a07:b944::2:2/128
DNS = 10.2.0.1, 2a07:b944::2:1

[Peer]
# US-FREE#53
PublicKey = N1o6VqzZtb0UCQvmkZGQj909M1sM3Xb787U0YNyODEw=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 146.70.174.66:51820
PersistentKeepalive = 25
`

const sampleUS3Conf = `[Interface]
PrivateKey = yBVl8qcgy/OTwV7fZ4bQzeQv5OAR3AJ2C583nN5u218=
Address = 10.2.0.2/32, 2a07:b944::2:2/128
DNS = 10.2.0.1, 2a07:b944::2:1

[Peer]
# US-FREE#3
PublicKey = bOz7aS+OtfmIiGLlQmnHrWb+wzw5qFp6PKdWPRlVORc=
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 195.181.163.1:51820
PersistentKeepalive = 25
`

func TestParseWireGuardConf_AllThreeNodes(t *testing.T) {
	// 1. Test US-9
	conf9, err := ParseWireGuardConf(sampleUS9Conf)
	if err != nil {
		t.Fatalf("Failed to parse US-9: %v", err)
	}
	if conf9.ServerIP != "89.187.180.1" || conf9.ServerPort != 51820 {
		t.Errorf("US-9 Endpoint mismatch: got %s:%d, want 89.187.180.1:51820", conf9.ServerIP, conf9.ServerPort)
	}
	if conf9.PublicKey != "o3BjGF2/GcEQiWoP89XgDfXFVlFFm7ipBFVQaOCR00k=" {
		t.Errorf("US-9 PublicKey mismatch: %s", conf9.PublicKey)
	}
	if len(conf9.Address) != 2 || conf9.Address[0] != "10.2.0.2/32" {
		t.Errorf("US-9 Address mismatch: %v", conf9.Address)
	}

	// 2. Test US-53
	conf53, err := ParseWireGuardConf(sampleUS53Conf)
	if err != nil {
		t.Fatalf("Failed to parse US-53: %v", err)
	}
	if conf53.ServerIP != "146.70.174.66" || conf53.ServerPort != 51820 {
		t.Errorf("US-53 Endpoint mismatch: %s:%d", conf53.ServerIP, conf53.ServerPort)
	}

	// 3. Test US-3
	conf3, err := ParseWireGuardConf(sampleUS3Conf)
	if err != nil {
		t.Fatalf("Failed to parse US-3: %v", err)
	}
	if conf3.ServerIP != "195.181.163.1" || conf3.ServerPort != 51820 {
		t.Errorf("US-3 Endpoint mismatch: %s:%d", conf3.ServerIP, conf3.ServerPort)
	}
}

func TestDeriveUUID_Deterministic(t *testing.T) {
	baseUUID := "403db7be-930b-449e-b5f4-34537cb594c7"
	derivedUS1 := DeriveUUID(baseUUID, "us")
	derivedUS2 := DeriveUUID(baseUUID, "us")
	derivedJP := DeriveUUID(baseUUID, "jp")

	if derivedUS1 != derivedUS2 {
		t.Errorf("DeriveUUID must be deterministic, got %s != %s", derivedUS1, derivedUS2)
	}
	if derivedUS1 == derivedJP {
		t.Errorf("Different region codes must produce different UUIDs")
	}
	if derivedUS1 == baseUUID {
		t.Errorf("Derived UUID should not equal base UUID")
	}
}

func TestExpandUsersForMultiplexing(t *testing.T) {
	baseUser := json.RawMessage(`{"name":"admin","uuid":"403db7be-930b-449e-b5f4-34537cb594c7","alterId":0}`)
	expanded := ExpandUsersForMultiplexing([]json.RawMessage{baseUser}, "vmess", StandardEgressRegions)

	// StandardEgressRegions has sg (skipped, root user), us, jp, nl -> Total 1 (root) + 3 (derived) = 4
	if len(expanded) != 4 {
		t.Fatalf("Expected 4 expanded users, got %d", len(expanded))
	}

	expectedNames := []string{"admin", "admin-us", "admin-jp", "admin-nl"}
	for i, uRaw := range expanded {
		var uMap map[string]interface{}
		if err := json.Unmarshal(uRaw, &uMap); err != nil {
			t.Fatalf("Failed to unmarshal user %d: %v", i, err)
		}
		if uMap["name"] != expectedNames[i] {
			t.Errorf("User %d name mismatch: got %v, want %v", i, uMap["name"], expectedNames[i])
		}
		if uMap["uuid"] == "" {
			t.Errorf("User %d UUID is empty", i)
		}
	}
}

func TestInjectEgressRouteRules(t *testing.T) {
	initialRules := []interface{}{
		map[string]interface{}{"action": "sniff"},
	}

	newRules := InjectEgressRouteRules(initialRules, "admin", StandardEgressRegions)

	// Verify sniff is first, then region rules
	if len(newRules) < 4 {
		t.Fatalf("Expected at least 4 rules, got %d", len(newRules))
	}

	hasUSRule := false
	for _, r := range newRules {
		if rMap, ok := r.(map[string]interface{}); ok {
			if authUsers, ok := rMap["auth_user"].([]string); ok && len(authUsers) > 0 {
				if authUsers[0] == "admin-us" && rMap["outbound"] == "us-pool" {
					hasUSRule = true
				}
			}
		}
	}

	if !hasUSRule {
		t.Errorf("Expected admin-us -> us-pool rule to be injected")
	}
}

func TestSingBoxConfig_FullValidation(t *testing.T) {
	// Construct the complete production Sing-Box configuration
	conf9, _ := ParseWireGuardConf(sampleUS9Conf)
	conf53, _ := ParseWireGuardConf(sampleUS53Conf)
	conf3, _ := ParseWireGuardConf(sampleUS3Conf)

	ep9, _ := BuildWireGuardEndpointJson("ep-us-free-9", conf9)
	ep53, _ := BuildWireGuardEndpointJson("ep-us-free-53", conf53)
	ep3, _ := BuildWireGuardEndpointJson("ep-us-free-3", conf3)

	out9, _ := BuildDirectOutboundJson("out-us-9", "ep-us-free-9")
	out53, _ := BuildDirectOutboundJson("out-us-53", "ep-us-free-53")
	out3, _ := BuildDirectOutboundJson("out-us-3", "ep-us-free-3")
	outPool, _ := BuildUrlTestPoolJson("us-pool", []string{"out-us-9", "out-us-53", "out-us-3"}, "3m")

	baseUser := json.RawMessage(`{"name":"admin","uuid":"403db7be-930b-449e-b5f4-34537cb594c7","alterId":0}`)
	inboundUsers := ExpandUsersForMultiplexing([]json.RawMessage{baseUser}, "vmess", StandardEgressRegions)

	inboundMap := map[string]interface{}{
		"type":        "vmess",
		"tag":         "vmess-in-2096",
		"listen":      "::",
		"listen_port": 2096,
		"users":       inboundUsers,
		"transport": map[string]interface{}{
			"type": "ws",
			"path": "/",
		},
	}
	inboundRaw, _ := json.Marshal(inboundMap)

	routeRules := InjectEgressRouteRules([]interface{}{
		map[string]interface{}{"action": "sniff"},
	}, "admin", StandardEgressRegions)

	fullConfig := map[string]interface{}{
		"log": map[string]interface{}{
			"level": "info",
		},
		"dns": map[string]interface{}{
			"servers": []map[string]interface{}{
				{"tag": "remote", "address": "udp://8.8.8.8"},
			},
		},
		"inbounds": []json.RawMessage{inboundRaw},
		"endpoints": []json.RawMessage{ep9, ep53, ep3},
		"outbounds": []interface{}{
			outPool,
			out9,
			out53,
			out3,
			map[string]interface{}{"type": "direct", "tag": "direct"},
		},
		"route": map[string]interface{}{
			"rules": routeRules,
		},
	}

	configBytes, err := json.MarshalIndent(fullConfig, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal full config: %v", err)
	}

	// Validate against Sing-Box option.Options schema
	var options option.Options
	if err := json.Unmarshal(configBytes, &options); err != nil {
		t.Fatalf("Sing-Box Options validation failed: %v", err)
	}

	if len(options.Inbounds) != 1 {
		t.Errorf("Expected 1 inbound, got %d", len(options.Inbounds))
	}
	if len(options.Endpoints) != 3 {
		t.Errorf("Expected 3 endpoints, got %d", len(options.Endpoints))
	}
	if len(options.Outbounds) != 5 {
		t.Errorf("Expected 5 outbounds, got %d", len(options.Outbounds))
	}
}

func TestInboundFetchUsersAndExpansion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test in-memory sqlite: %v", err)
	}

	err = db.AutoMigrate(&model.Client{}, &model.CloudflareEndpoint{}, &model.Outbound{}, &model.Endpoint{})
	if err != nil {
		t.Fatalf("Failed to auto migrate: %v", err)
	}

	_ = db.Create(&model.Outbound{Tag: "us-pool", Type: "urltest"}).Error
	_ = db.Create(&model.Outbound{Tag: "jp-pool", Type: "urltest"}).Error
	_ = db.Create(&model.Outbound{Tag: "nl-pool", Type: "urltest"}).Error

	client := model.Client{
		Id:       1,
		Enable:   true,
		Name:     "my",
		Config:   json.RawMessage(`{"vless":{"uuid":"8c9fa6c8-bf77-4533-8b12-563cc157eb2a","flow":""}}`),
		Inbounds: json.RawMessage(`[1]`),
	}
	if err := db.Create(&client).Error; err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	_ = SeedInitialCloudflareEndpoints(db)
	activeRegions := GetActiveEgressRegions(db)

	inboundService := &InboundService{}
	inboundMap := map[string]interface{}{"type": "vless"}
	users, err := inboundService.fetchUsers(db, "vless", "id IN (1)", inboundMap)
	if err != nil {
		t.Fatalf("fetchUsers failed: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("Expected 1 user, got %d", len(users))
	}

	var rootUserMap map[string]interface{}
	_ = json.Unmarshal(users[0], &rootUserMap)
	if rootUserMap["name"] != "my" {
		t.Fatalf("Expected user name 'my', got %v", rootUserMap["name"])
	}

	expanded := ExpandUsersForMultiplexing(users, "vless", activeRegions)
	if len(expanded) < 10 {
		t.Fatalf("Expected at least 10 expanded users for active regions, got %d", len(expanded))
	}

	expandedNames := make(map[string]bool)
	for _, uRaw := range expanded {
		var uMap map[string]interface{}
		_ = json.Unmarshal(uRaw, &uMap)
		expandedNames[uMap["name"].(string)] = true
	}

	for _, reqName := range []string{"my", "my-us", "my-jp", "my-nl", "my-cf-br", "my-cf-ar", "my-cf-cl", "my-cf-ng", "my-cf-za", "my-cf-tr"} {
		if !expandedNames[reqName] {
			t.Errorf("Expected expanded user '%s' to be present", reqName)
		}
	}
}
