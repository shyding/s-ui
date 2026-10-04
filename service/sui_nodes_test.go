package service

import (
	"encoding/json"
	"testing"

	"github.com/alireza0/s-ui/database/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.Inbound{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestGetSUINodeSpecs_CountAndPorts(t *testing.T) {
	specs := GetSUINodeSpecs()
	if len(specs) != 38 {
		t.Fatalf("expected 38 specs, got %d", len(specs))
	}
	seen := map[int]bool{}
	for i, s := range specs {
		if s.Port != SUIPortMin+i {
			t.Fatalf("spec %d: expected port %d, got %d", i, SUIPortMin+i, s.Port)
		}
		if seen[s.Port] {
			t.Fatalf("duplicate port %d", s.Port)
		}
		seen[s.Port] = true
		if s.Tag == "" || s.Type == "" {
			t.Fatalf("spec %d: empty tag/type", i)
		}
	}
}

func TestSUINodeSpec_BuildOptions(t *testing.T) {
	specs := GetSUINodeSpecs()
	for _, s := range specs {
		opts := s.BuildOptions()
		if opts["listen_port"] != s.Port {
			t.Fatalf("%s: listen_port mismatch", s.Tag)
		}
		if opts["type"] != s.Type {
			t.Fatalf("%s: type mismatch", s.Tag)
		}
		tls, hasTLS := opts["tls"]
		if s.TLS && !hasTLS {
			t.Fatalf("%s: expected tls options", s.Tag)
		}
		if !s.TLS && hasTLS {
			t.Fatalf("%s: unexpected tls options", s.Tag)
		}
		if s.TLS {
			tlsMap := tls.(map[string]interface{})
			if tlsMap["certificate_path"] != SUICertPath {
				t.Fatalf("%s: cert path mismatch", s.Tag)
			}
		}
		// JSON 必须可序列化
		if _, err := json.Marshal(opts); err != nil {
			t.Fatalf("%s: options not JSON-serializable: %v", s.Tag, err)
		}
	}
	// 抽查几个关键节点的传输配置
	byTag := map[string]SUINodeSpec{}
	for _, s := range specs {
		byTag[s.Tag] = s
	}
	ws := byTag["vless-ws-54143"].BuildOptions()["transport"].(map[string]interface{})
	if ws["type"] != "ws" || ws["path"] != "/ws" {
		t.Fatalf("vless-ws-54143 transport mismatch: %v", ws)
	}
	grpc := byTag["vless-grpc-54144"].BuildOptions()["transport"].(map[string]interface{})
	if grpc["type"] != "grpc" || grpc["service_name"] != "vgrpc" {
		t.Fatalf("vless-grpc-54144 transport mismatch: %v", grpc)
	}
	ss := byTag["ss-aes-256-gcm-54172"].BuildOptions()
	if ss["method"] != "aes-256-gcm" || ss["password"] != SUISSPassword {
		t.Fatalf("ss-54172 mismatch: %v", ss)
	}
}

func TestEnsureSUINodes_Idempotent(t *testing.T) {
	db := testDB(t)
	created, err := EnsureSUINodes(db)
	if err != nil {
		t.Fatalf("first EnsureSUINodes: %v", err)
	}
	if created != 38 {
		t.Fatalf("expected 38 created, got %d", created)
	}
	// 第二次执行应为幂等：0 新建
	created2, err := EnsureSUINodes(db)
	if err != nil {
		t.Fatalf("second EnsureSUINodes: %v", err)
	}
	if created2 != 0 {
		t.Fatalf("expected 0 created on second run, got %d", created2)
	}
	// 校验全部存在
	if missing := VerifySUINodes(db); len(missing) > 0 {
		t.Fatalf("missing after ensure: %v", missing)
	}
	// 获取 ID
	ids, err := GetSUIInboundIDs(db)
	if err != nil {
		t.Fatalf("GetSUIInboundIDs: %v", err)
	}
	if len(ids) != 38 {
		t.Fatalf("expected 38 ids, got %d", len(ids))
	}
}

func TestEnsureSUINodes_PartialExisting(t *testing.T) {
	db := testDB(t)
	// 预置一个已存在的（模拟旧数据，options 不同也不应被覆盖）
	old := model.Inbound{Type: "vless", Tag: "vless-54142", Options: json.RawMessage(`{"custom":"old"}`)}
	if err := db.Create(&old).Error; err != nil {
		t.Fatalf("create old: %v", err)
	}
	created, err := EnsureSUINodes(db)
	if err != nil {
		t.Fatalf("EnsureSUINodes: %v", err)
	}
	if created != 37 {
		t.Fatalf("expected 37 created, got %d", created)
	}
	// 旧数据不应被覆盖
	var kept model.Inbound
	db.Where("tag = ?", "vless-54142").First(&kept)
	var opts map[string]interface{}
	json.Unmarshal(kept.Options, &opts)
	if opts["custom"] != "old" {
		t.Fatalf("existing inbound was overwritten: %s", string(kept.Options))
	}
}
