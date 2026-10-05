package sub

import (
	"encoding/json"
	"testing"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
)

func TestSOCKSAuthRuntimeCredentials(t *testing.T) {
	previous := database.GetDB()
	if err := database.InitDB(t.TempDir() + "/auth.db"); err != nil {
		t.Fatal(err)
	}
	db := database.GetDB()
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		database.SetDB(previous)
	})
	cfg := json.RawMessage(`{"mixed":{"username":"mixed-user","password":"mixed-pass"},"socks":{"username":"socks-user","password":"socks-pass"}}`)
	tlsRecord := model.Tls{Name: "auth-test", Server: json.RawMessage(`{"enabled":false}`)}
	if err := db.Create(&tlsRecord).Error; err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, kind, tag, port, want string
		config                      *json.RawMessage
	}{
		{"mixed users injected at runtime", "mixed", "custom-31001", "31001", "socks5://mixed-user:mixed-pass@example.com:31001", &cfg},
		{"socks uses its own credentials", "socks", "custom-31002", "31002", "socks5://socks-user:socks-pass@example.com:31002", &cfg},
		{"unknown credentials preserved", "mixed", "custom-31003", "31003", "socks5://old:secret@example.com:31003", nil},
		{"explicit anonymous coverage", "socks", "coverage-socks-31004", "31004", "socks5://example.com:31004", &cfg},
		{"unrelated protocol untouched", "vless", "custom-31005", "31005", "socks5://old:secret@example.com:31005", &cfg},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inbound := model.Inbound{Type: tc.kind, Tag: tc.tag, TlsId: tlsRecord.Id, Options: json.RawMessage(`{}`)}
			if err := db.Create(&inbound).Error; err != nil {
				t.Fatal(err)
			}
			got := fixSOCKSAuth("socks5://old:secret@example.com:"+tc.port, tc.config)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
