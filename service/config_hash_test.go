package service

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
)

func TestInboundHashIgnoresDynamicOutboundHealth(t *testing.T) {
	if err := database.InitDB(filepath.Join(t.TempDir(), "hash.db")); err != nil {
		t.Fatal(err)
	}
	db := database.GetDB()
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	tlsRow := model.Tls{Name: "hash-tls", Server: json.RawMessage(`{"enabled":true}`), Client: json.RawMessage(`{}`)}
	if err := db.Create(&tlsRow).Error; err != nil {
		t.Fatal(err)
	}
	inbound := model.Inbound{Type: "vless", Tag: "hash-test-54142", TlsId: tlsRow.Id, Options: json.RawMessage(`{"listen_port":54142}`)}
	if err := db.Create(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	client := model.Client{Enable: true, Name: "hash-user", Config: json.RawMessage(`{"vless":{"uuid":"test"}}`), Inbounds: json.RawMessage(`[]`)}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	svc := &ConfigService{}
	before, err := svc.computeInboundHash()
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Create(&model.Outbound{Type: "direct", Tag: "dynamic-health", Options: json.RawMessage(`{}`), Available: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.NodeHealthStatus{Node: "example.test:443", Status: "available"}).Error; err != nil {
		t.Fatal(err)
	}
	afterHealth, err := svc.computeInboundHash()
	if err != nil {
		t.Fatal(err)
	}
	if before != afterHealth {
		t.Fatal("outbound/health-only changes must not alter inbound restart hash")
	}

	if err := db.Model(&inbound).Update("options", json.RawMessage(`{"listen_port":54143}`)).Error; err != nil {
		t.Fatal(err)
	}
	afterInbound, err := svc.computeInboundHash()
	if err != nil {
		t.Fatal(err)
	}
	if afterInbound == before {
		t.Fatal("listener change must alter inbound restart hash")
	}
}
