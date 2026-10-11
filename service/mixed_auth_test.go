package service

import (
	"encoding/json"
	"testing"

	"github.com/alireza0/s-ui/database/model"
	"github.com/glebarez/sqlite"
	"github.com/sagernet/sing-box/option"
	"gorm.io/gorm"
)

func TestMixedAuthUsesClientSOCKSCredentials(t *testing.T) {
	for _, tc := range []struct{ name, config, want string }{
		{"editor socks", `{"socks":{"username":"user","password":"pass"}}`, "user"},
		{"explicit mixed wins", `{"mixed":{"username":"explicit","password":"pass"},"socks":{"username":"user","password":"other"}}`, "explicit"},
		{"null mixed falls back", `{"mixed":null,"socks":{"username":"user","password":"pass"}}`, "user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			if err := db.AutoMigrate(&model.Client{}); err != nil {
				t.Fatal(err)
			}
			for _, client := range []model.Client{
				{Id: 1, Enable: true, Name: "allowed", Config: json.RawMessage(tc.config), Inbounds: json.RawMessage(`[99]`)},
				{Id: 2, Enable: false, Name: "disabled", Config: json.RawMessage(tc.config), Inbounds: json.RawMessage(`[99]`)},
				{Id: 3, Enable: true, Name: "ungranted", Config: json.RawMessage(tc.config), Inbounds: json.RawMessage(`[]`)},
			} {
				if err := db.Create(&client).Error; err != nil {
					t.Fatal(err)
				}
			}
			service := &InboundService{}
			raw, err := service.addUsers(db, []byte(`{"type":"mixed","tag":"mixed-54156","listen":"127.0.0.1","listen_port":54156}`), 99, "mixed", nil)
			if err != nil {
				t.Fatal(err)
			}
			var inbound option.Inbound
			if err := json.Unmarshal(raw, &inbound); err != nil {
				t.Fatalf("invalid core schema: %v", err)
			}
			var cfg struct {
				Users []map[string]string `json:"users"`
			}
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			if len(cfg.Users) != 1 || cfg.Users[0]["username"] != tc.want || cfg.Users[0]["password"] != "pass" {
				t.Fatal("runtime credentials differ from authorized client")
			}
			if _, ok := cfg.Users[0]["name"]; ok {
				t.Fatal("mixed auth.User must not contain name")
			}
		})
	}
}
