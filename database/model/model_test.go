package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// SQLite's pure-Go driver commonly returns JSON TEXT columns as string values.
// Keep this regression test so a model change cannot reintroduce the
// json.RawMessage scan failure seen in production.
func TestClientJSONFieldsScanFromSQLiteText(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Client{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO clients (enable, name, config, inbounds, links) VALUES (?, ?, ?, ?, ?)`, true, "scan-test", `{"mode":"x"}`, `[1,2]`, `{"url":"x"}`).Error; err != nil {
		t.Fatal(err)
	}

	var got Client
	if err := db.Where("name = ?", "scan-test").First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if string(got.Config) != `{"mode":"x"}` {
		t.Fatalf("unexpected config: %s", got.Config)
	}
	if string(got.Inbounds) != `[1,2]` {
		t.Fatalf("unexpected inbounds: %s", got.Inbounds)
	}
	if string(got.Links) != `{"url":"x"}` {
		t.Fatalf("unexpected links: %s", got.Links)
	}
}
