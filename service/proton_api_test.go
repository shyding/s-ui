package service

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestGenerateNewWireGuardKeyPair(t *testing.T) {
	priv, pub, err := GenerateNewWireGuardKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate WireGuard key pair: %v", err)
	}
	if len(priv) != 44 || len(pub) != 44 {
		t.Errorf("WireGuard keys must be 44-character Base64 strings, got priv=%d, pub=%d", len(priv), len(pub))
	}
	if priv == pub {
		t.Errorf("Private key and Public key must be different")
	}
}

func TestFilterFreeLogicalServers(t *testing.T) {
	mockServers := []*ProtonLogicalServer{
		{
			ID:          "us-free-1",
			Name:        "US-FREE#1",
			ExitCountry: "US",
			Tier:        0, // Free
			Load:        85,
			Status:      1,
		},
		{
			ID:          "us-plus-1",
			Name:        "US-PLUS#1",
			ExitCountry: "US",
			Tier:        2, // Plus (Not free)
			Load:        30,
			Status:      1,
		},
		{
			ID:          "jp-free-1",
			Name:        "JP-FREE#1",
			ExitCountry: "JP",
			Tier:        0, // Free
			Load:        60,
			Status:      1,
		},
		{
			ID:          "us-free-2",
			Name:        "US-FREE#2",
			ExitCountry: "US",
			Tier:        0, // Free
			Load:        45, // Lowest load in US
			Status:      1,
		},
	}

	// Filter only US free servers
	usFree := FilterFreeLogicalServers(mockServers, "US")
	if len(usFree) != 2 {
		t.Fatalf("Expected 2 US free servers, got %d", len(usFree))
	}

	// Lowest load should be first
	if usFree[0].Name != "US-FREE#2" || usFree[0].Load != 45 {
		t.Errorf("Expected lowest load server first (US-FREE#2), got %s with load %d", usFree[0].Name, usFree[0].Load)
	}

	// Filter all free servers
	allFree := FilterFreeLogicalServers(mockServers)
	if len(allFree) != 3 {
		t.Fatalf("Expected 3 free servers total, got %d", len(allFree))
	}
	if allFree[0].Name != "US-FREE#2" {
		t.Errorf("Expected US-FREE#2 (load 45) to be first among all free servers, got %s", allFree[0].Name)
	}
}

func TestConvertToWireGuardConfigs(t *testing.T) {
	servers := []*ProtonLogicalServer{
		{
			ID:          "us-free-9",
			Name:        "US-FREE#9",
			ExitCountry: "US",
			Status:      1,
			Servers: []*ProtonServer{
				{
					EntryIP:         "89.187.180.1",
					X25519PublicKey: "o3BjGF2/GcEQiWoP89XgDfXFVlFFm7ipBFVQaOCR00k=",
				},
			},
		},
	}

	clientPriv := "sDxK8N77o0zC7vjfJ3Yoxe/O4Nmgct8HtAwyS7pflmg="
	configs := ConvertToWireGuardConfigs(servers, clientPriv, nil)

	if len(configs) != 1 {
		t.Fatalf("Expected 1 converted WireGuard config, got %d", len(configs))
	}

	conf := configs[0]
	if conf.ServerIP != "89.187.180.1" || conf.ServerPort != 51820 {
		t.Errorf("Server endpoint mismatch: %s:%d", conf.ServerIP, conf.ServerPort)
	}
	if conf.PublicKey != "o3BjGF2/GcEQiWoP89XgDfXFVlFFm7ipBFVQaOCR00k=" {
		t.Errorf("Server public key mismatch: %s", conf.PublicKey)
	}
	if conf.PrivateKey != clientPriv {
		t.Errorf("Client private key mismatch: %s", conf.PrivateKey)
	}
	if len(conf.Address) != 2 || conf.Address[0] != "10.2.0.2/32" {
		t.Errorf("Client address mismatch: %v", conf.Address)
	}
}

func TestBatchImportWireGuardConfigs_Logic(t *testing.T) {
	configs := []*WireGuardConf{
		{
			Name:       "US-FREE#9",
			Country:    "US",
			PrivateKey: "privkey1",
			PublicKey:  "pubkey1",
			ServerIP:   "89.187.180.1",
			ServerPort: 51820,
			Address:    []string{"10.2.0.2/32"},
		},
		{
			Name:       "US-FREE#53",
			Country:    "US",
			PrivateKey: "privkey2",
			PublicKey:  "pubkey2",
			ServerIP:   "146.70.174.66",
			ServerPort: 51820,
			Address:    []string{"10.2.0.2/32"},
		},
	}

	for _, conf := range configs {
		cleanName := SanitizeTag(conf.Name)
		epTag := "ep-proton-" + cleanName
		outTag := "out-proton-" + cleanName

		epJson, err := BuildWireGuardEndpointJson(epTag, conf)
		if err != nil {
			t.Fatalf("Failed to build endpoint json for %s: %v", epTag, err)
		}
		if len(epJson) == 0 {
			t.Errorf("epJson is empty for %s", epTag)
		}

		outJson, err := BuildDirectOutboundJson(outTag, epTag)
		if err != nil {
			t.Fatalf("Failed to build outbound json for %s: %v", outTag, err)
		}
		if len(outJson) == 0 {
			t.Errorf("outJson is empty for %s", outTag)
		}
	}

	poolJson, err := BuildUrlTestPoolJson("us-pool", []string{"out-proton-us-free-9", "out-proton-us-free-53"}, "3m")
	if err != nil {
		t.Fatalf("Failed to build urltest pool json: %v", err)
	}
	if len(poolJson) == 0 {
		t.Errorf("poolJson is empty")
	}
}

func TestSanitizeTag(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"US-FREE#9", "us-free-9"},
		{"JP_FREE #12", "jp-free-12"},
		{"NL--FREE.3", "nl-free-3"},
	}

	for _, tc := range tests {
		actual := SanitizeTag(tc.input)
		if actual != tc.expected {
			t.Errorf("SanitizeTag(%q) = %q, want %q", tc.input, actual, tc.expected)
		}
	}
}

func TestHarvestResultParsing_Logic(t *testing.T) {
	mockOutput := `
Opening ProtonVPN portal (Headless: true)...
Captured 15 logical servers from response stream!
---SUI_HARVEST_START---
{
  "success": true,
  "servers": [
    {
      "ID": "us-free-1",
      "Name": "US-FREE#1",
      "EntryCountry": "US",
      "ExitCountry": "US",
      "Tier": 0,
      "Load": 55,
      "Status": 1,
      "Servers": [
        {
          "ID": "us-free-1-node",
          "EntryIP": "89.187.180.1",
          "X25519PublicKey": "o3BjGF2/GcEQiWoP89XgDfXFVlFFm7ipBFVQaOCR00k="
        }
      ]
    },
    {
      "ID": "jp-free-1",
      "Name": "JP-FREE#1",
      "EntryCountry": "JP",
      "ExitCountry": "JP",
      "Tier": 0,
      "Load": 42,
      "Status": 1,
      "Servers": [
        {
          "ID": "jp-free-1-node",
          "EntryIP": "103.152.220.1",
          "X25519PublicKey": "jpPubkeyBase64String12345678901234567890="
        }
      ]
    }
  ],
  "message": "Successfully harvested 2 ProtonVPN servers."
}
---SUI_HARVEST_END---
`
	startMarker := "---SUI_HARVEST_START---"
	endMarker := "---SUI_HARVEST_END---"
	startIndex := strings.Index(mockOutput, startMarker)
	endIndex := strings.Index(mockOutput, endMarker)

	if startIndex == -1 || endIndex == -1 || endIndex <= startIndex {
		t.Fatalf("Failed to locate harvest markers in output")
	}

	jsonStr := strings.TrimSpace(mockOutput[startIndex+len(startMarker) : endIndex])
	var res HarvestResult
	if err := json.Unmarshal([]byte(jsonStr), &res); err != nil {
		t.Fatalf("Failed to unmarshal HarvestResult: %v", err)
	}

	if !res.Success || len(res.Servers) != 2 {
		t.Fatalf("Expected 2 servers, got %d (success=%v)", len(res.Servers), res.Success)
	}

	freeUS := FilterFreeLogicalServers(res.Servers, "US")
	if len(freeUS) != 1 || freeUS[0].Name != "US-FREE#1" {
		t.Errorf("US free server filter mismatch: %v", freeUS)
	}

	freeJP := FilterFreeLogicalServers(res.Servers, "JP")
	if len(freeJP) != 1 || freeJP[0].Name != "JP-FREE#1" {
		t.Errorf("JP free server filter mismatch: %v", freeJP)
	}
}

func TestInferCountryFromPath_Logic(t *testing.T) {
	testCases := []struct {
		fileName string
		fullPath string
		expected string
	}{
		{"sui-node-US-FREE-9.conf", `C:\Users\acer\Downloads\openvpn\us\sui-node-US-FREE-9.conf`, "US"},
		{"wg-US-FREE-3.conf", `C:\Users\acer\Downloads\openvpn\us\wg-US-FREE-3.conf`, "US"},
		{"JP-FREE-12.conf", `C:\Users\acer\Downloads\openvpn\jp\JP-FREE-12.conf`, "JP"},
		{"NL-FREE-5.conf", `C:\Users\acer\Downloads\openvpn\nl\NL-FREE-5.conf`, "NL"},
		{"node1.conf", `C:\Users\acer\Downloads\openvpn\US\node1.conf`, "US"},
		{"japan_fast.conf", `C:\Users\acer\Downloads\japan_fast.conf`, "JP"},
	}

	countryRe := regexp.MustCompile(`(?i)(?:^|[^a-zA-Z])(US|JP|NL|SG)(?:[^a-zA-Z]|$)`)

	for _, tc := range testCases {
		baseUpper := strings.ToUpper(tc.fileName)
		pathUpper := strings.ToUpper(tc.fullPath)
		country := "US"

		if match := countryRe.FindStringSubmatch(baseUpper); len(match) > 1 {
			country = strings.ToUpper(match[1])
		} else if strings.Contains(pathUpper, "\\US\\") || strings.Contains(pathUpper, "/US/") {
			country = "US"
		} else if strings.Contains(pathUpper, "\\JP\\") || strings.Contains(pathUpper, "/JP/") || strings.Contains(pathUpper, "JAPAN") {
			country = "JP"
		} else if strings.Contains(pathUpper, "\\NL\\") || strings.Contains(pathUpper, "/NL/") || strings.Contains(pathUpper, "NETHERLANDS") {
			country = "NL"
		} else if strings.Contains(pathUpper, "\\SG\\") || strings.Contains(pathUpper, "/SG/") || strings.Contains(pathUpper, "SINGAPORE") {
			country = "SG"
		}

		if country != tc.expected {
			t.Errorf("Path %q + file %q expected %s, got %s", tc.fullPath, tc.fileName, tc.expected, country)
		}
	}
}

func TestInferCountryHelper(t *testing.T) {
	testCases := []struct {
		name     string
		content  string
		expected string
	}{
		{"sui-node-US-FREE-9.conf", "[Interface]\n# US-FREE#9\n", "US"},
		{"wg-US-FREE-3.conf", "[Interface]\n# US-FREE#3\n", "US"},
		{"JP-server.conf", "", "JP"},
		{"unknown.conf", "[Peer]\n# Netherlands node\n", "NL"},
		{"sg-node-1.conf", "", "SG"},
		{"custom.conf", "[Peer]\n# Tokyo server\n", "JP"},
	}

	for _, tc := range testCases {
		res := InferCountry(tc.name, tc.content)
		if res != tc.expected {
			t.Errorf("InferCountry(%q, %q) = %s, expected %s", tc.name, tc.content, res, tc.expected)
		}
	}
}

func TestSplitWireGuardConfigs(t *testing.T) {
	multi := `[Interface]
PrivateKey = key1
Address = 10.0.0.1/32
[Peer]
PublicKey = pub1
Endpoint = 1.1.1.1:51820

[Interface]
PrivateKey = key2
Address = 10.0.0.2/32
[Peer]
PublicKey = pub2
Endpoint = 2.2.2.2:51820
`
	blocks := SplitWireGuardConfigs(multi)
	if len(blocks) != 2 {
		t.Fatalf("Expected 2 config blocks, got %d", len(blocks))
	}
	if !strings.Contains(blocks[0], "1.1.1.1:51820") {
		t.Errorf("First block missing endpoint 1.1.1.1")
	}
	if !strings.Contains(blocks[1], "2.2.2.2:51820") {
		t.Errorf("Second block missing endpoint 2.2.2.2")
	}
}



