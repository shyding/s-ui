package database

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/alireza0/s-ui/config"
	"github.com/alireza0/s-ui/database/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

func initUser() error {
	var count int64
	err := db.Model(&model.User{}).Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		user := &model.User{
			Username: "admin",
			Password: "admin",
		}
		return db.Create(user).Error
	}
	return nil
}

func OpenDB(dbPath string) error {
	dir := path.Dir(dbPath)
	err := os.MkdirAll(dir, 01740)
	if err != nil {
		return err
	}

	var gormLogger logger.Interface

	if config.IsDebug() {
		gormLogger = logger.Default
	} else {
		gormLogger = logger.Discard
	}

	c := &gorm.Config{
		Logger: gormLogger,
	}

	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-32000)&_pragma=foreign_keys(1)", dbPath)
	db, err = gorm.Open(sqlite.Open(dsn), c)
	if err != nil {
		db, err = gorm.Open(sqlite.Open(dbPath), c)
		if err != nil {
			return err
		}
	}

	// Guarantee WAL and concurrency PRAGMAs
	db.Exec("PRAGMA journal_mode = WAL;")
	db.Exec("PRAGMA busy_timeout = 10000;")
	db.Exec("PRAGMA synchronous = NORMAL;")
	db.Exec("PRAGMA cache_size = -32000;")
	db.Exec("PRAGMA foreign_keys = ON;")

	// Serialize queries through safe pool to eliminate SQLite OS file lock contention
	if sqlDB, dbErr := db.DB(); dbErr == nil {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		sqlDB.SetConnMaxLifetime(time.Hour)
	}

	if config.IsDebug() {
		db = db.Debug()
	}
	return nil
}

func InitDB(dbPath string) error {
	err := OpenDB(dbPath)
	if err != nil {
		return err
	}

	// Default Outbounds
	if !db.Migrator().HasTable(&model.Outbound{}) {
		db.Migrator().CreateTable(&model.Outbound{})
		defaultOutbound := []model.Outbound{
			{Type: "direct", Tag: "direct", Options: json.RawMessage(`{}`)},
		}
		db.Create(&defaultOutbound)
	}

	err = db.AutoMigrate(
		&model.Setting{},
		&model.Tls{},
		&model.Inbound{},
		&model.Outbound{},
		&model.Service{},
		&model.Endpoint{},
		&model.User{},
		&model.Tokens{},
		&model.Stats{},
		&model.Client{},
		&model.Changes{},
		&model.Subscription{},
		&model.CloudflareEndpoint{},
		&model.NodeHealthStatus{},
	)
	if err != nil {
		return err
	}
	err = initUser()
	if err != nil {
		return err
	}

	// Migrate broken REALITY inbounds to standard TLS
	// REALITY handshake fails persistently between sing-box server and Xray clients
	migrateRealityToTLS()

	return nil
}

// migrateRealityToTLS converts the 4 REALITY inbounds (54161-54164) to standard
// VLESS+TLS. The REALITY protocol has a persistent incompatibility between the
// sing-box server and Xray clients ("processed invalid connection"), making
// the nodes unusable. Converting to TLS restores availability.
func migrateRealityToTLS() {
	realityPorts := map[int]bool{54161: true, 54162: true, 54163: true, 54164: true}

	var inbounds []model.Inbound
	if err := db.Find(&inbounds).Error; err != nil {
		return
	}

	for _, ib := range inbounds {
		var opts map[string]interface{}
		if err := json.Unmarshal(ib.Options, &opts); err != nil {
			continue
		}
		lp, ok := opts["listen_port"].(float64)
		if !ok || !realityPorts[int(lp)] {
			continue
		}

		tlsCfg, ok := opts["tls"].(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasReality := tlsCfg["reality"]; !hasReality {
			continue // Already converted
		}

		// Convert REALITY to standard TLS
		delete(tlsCfg, "reality")
		tlsCfg["server_name"] = "dash.icta.top"
		tlsCfg["alpn"] = []string{"h2", "http/1.1"}
		tlsCfg["certificate_path"] = "/usr/local/s-ui/certs/fullchain.pem"
		tlsCfg["key_path"] = "/usr/local/s-ui/certs/privkey.pem"
		opts["tls"] = tlsCfg

		newOpts, err := json.Marshal(opts)
		if err != nil {
			continue
		}

		ib.Options = newOpts
		if err := db.Save(&ib).Error; err != nil {
			continue
		}
	}
}

func GetDB() *gorm.DB {
	return db
}

func SetDB(ndb *gorm.DB) {
	db = ndb
}

func IsNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}
