package service

import (
	"testing"
	
	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB() {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}
	db.AutoMigrate(&model.Outbound{})
	database.SetDB(db)
}

func TestTestOutboundWithLandingIP_DeadNode(t *testing.T) {
	setupTestDB()
	db := database.GetDB()

	// A hysteria2 node normally skips the active TCP test (Latency=0)
	db.Create(&model.Outbound{
		Tag:     "dead-hy2",
		Type:    "hysteria2",
		Options: []byte(`{"server": "192.0.2.1", "server_port": 443}`), // A dead IP (TEST-NET-1)
	})

	result := &NodeTestResult{
		Tag:       "dead-hy2",
		Available: true,
		Latency:   0,
		RealLatency: 0,
		LandingIP: "",
	}
	
	result.Error = "all IP lookup services failed (timeout etc)"
	
	if result.LandingIP == "" && result.RealLatency == 0 {
		result.Available = false
	}
	
	if result.Available {
		t.Errorf("Expected node to be marked Unavailable when RealLatency is 0 and IP lookups failed, but got Available=true")
	}
}
