package service

import (
	"encoding/json"
	"testing"

	"github.com/alireza0/s-ui/database/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestEnsureSeedPoolSeparatesMeasuredLocations(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.Subscription{}, &model.Outbound{}); err != nil {
		t.Fatal(err)
	}
	subscription := model.Subscription{Name: seededClientNodesSubscriptionName}
	if err = db.Create(&subscription).Error; err != nil {
		t.Fatal(err)
	}
	nodes := []model.Outbound{
		{Tag: "seed-us-1", Type: "vless", SubscriptionId: &subscription.Id, Available: true, LandingIP: "198.51.100.1", Country: "US", Region: "California", City: "Los Angeles"},
		{Tag: "seed-us-2", Type: "vless", SubscriptionId: &subscription.Id, Available: true, LandingIP: "198.51.100.2", Country: "US", Region: "California", City: "Los Angeles"},
		{Tag: "seed-jp-1", Type: "vless", SubscriptionId: &subscription.Id, Available: true, LandingIP: "203.0.113.1", Country: "JP", Region: "Tokyo", City: "Tokyo"},
	}
	if err = db.Create(&nodes).Error; err != nil {
		t.Fatal(err)
	}

	config := &SingBoxConfig{}
	EnsureSeedPoolInOutbounds(config, db)
	if len(config.Outbounds) != 2 {
		t.Fatalf("expected one pool per measured location, got %d", len(config.Outbounds))
	}
	tags := map[string]bool{}
	for _, raw := range config.Outbounds {
		var pool map[string]interface{}
		if err = json.Unmarshal(raw, &pool); err != nil {
			t.Fatal(err)
		}
		tags[pool["tag"].(string)] = true
	}
	if !tags[seedPoolTag("US", "California", "Los Angeles")] || !tags[seedPoolTag("JP", "Tokyo", "Tokyo")] {
		t.Fatalf("missing measured seed pools: %#v", tags)
	}
}
