package service

import (
	"encoding/json"
	"testing"

	"github.com/alireza0/s-ui/database/model"
)

func TestRepairWireGuardEndpoint(t *testing.T) {
	// 1. Legacy format with missing private_key and local_address / server
	legacyOptions := `{
		"system": false,
		"local_address": ["10.2.0.2/32"],
		"private_key": "",
		"server": "84.20.27.34",
		"server_port": 51820,
		"peer_public_key": "weCSxoXSpUf0pBwokYIRqxzHNc60zcEthqkNpuCCsWY="
	}`

	ep := &model.Endpoint{
		Type:    "wireguard",
		Tag:     "ep-proton-us-free-114",
		Options: json.RawMessage(legacyOptions),
	}

	ok := repairWireGuardEndpoint(nil, ep)
	if !ok {
		t.Fatalf("Expected repairWireGuardEndpoint to succeed, but got false")
	}

	var optMap map[string]interface{}
	err := json.Unmarshal(ep.Options, &optMap)
	if err != nil {
		t.Fatalf("Repaired options invalid JSON: %v", err)
	}

	// Verify private_key was generated
	privKey, _ := optMap["private_key"].(string)
	if privKey == "" {
		t.Errorf("Expected non-empty private_key")
	}

	// Verify local_address was converted to address
	if _, exists := optMap["local_address"]; exists {
		t.Errorf("local_address should have been deleted")
	}
	addr, exists := optMap["address"].([]interface{})
	if !exists || len(addr) == 0 {
		t.Errorf("address should exist and be non-empty")
	}

	// Verify server / peer_public_key was converted to peers
	if _, exists := optMap["server"]; exists {
		t.Errorf("server should have been deleted")
	}
	peers, exists := optMap["peers"].([]interface{})
	if !exists || len(peers) == 0 {
		t.Fatalf("peers should exist and have items")
	}
	peer0 := peers[0].(map[string]interface{})
	if peer0["address"] != "84.20.27.34" {
		t.Errorf("Expected peer address 84.20.27.34, got %v", peer0["address"])
	}
	if peer0["public_key"] != "weCSxoXSpUf0pBwokYIRqxzHNc60zcEthqkNpuCCsWY=" {
		t.Errorf("Expected peer public key, got %v", peer0["public_key"])
	}
}
