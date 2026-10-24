package service

import (
	"encoding/json"
	"fmt"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
)

// rebuildPublicPool binds the public pool listeners to exits that passed the
// current health round.  The previous implementation left pool routes bound
// to tags removed by subscription refreshes, which produced "router outbound
// not found" and made otherwise healthy listeners unusable.
//
// A rebuild is fail-closed: if fewer than 300 fresh exits are available, the
// existing pool is left untouched and the caller receives an error.
func rebuildPublicPool(roundStart int64) (int, error) {
	db := database.GetDB()
	if db == nil {
		return 0, fmt.Errorf("database unavailable")
	}
	var rows []model.Outbound
	if err := db.Where("(tag LIKE ? OR subscription_id IN (SELECT id FROM subscriptions WHERE name = ?)) AND available = ? AND last_test_time >= ? AND country <> '' AND region <> '' AND city <> ''", "hproxy-%", "Local v2rayN Seed Nodes", true, roundStart).
		Order("last_test_time DESC, id ASC").Find(&rows).Error; err != nil {
		return 0, err
	}
	chosen := make([]model.Outbound, 0, 300)
	seenCity := make(map[string]struct{}, 300)
	seenTag := make(map[string]struct{}, 300)
	for _, row := range rows {
		key := row.Country + "\x00" + row.Region + "\x00" + row.City
		if _, ok := seenCity[key]; ok {
			continue
		}
		chosen = append(chosen, row)
		seenCity[key] = struct{}{}
		seenTag[row.Tag] = struct{}{}
		if len(chosen) == 300 {
			break
		}
	}
	if len(chosen) < 300 {
		for _, row := range rows {
			if _, ok := seenTag[row.Tag]; ok {
				continue
			}
			chosen = append(chosen, row)
			seenTag[row.Tag] = struct{}{}
			if len(chosen) == 300 {
				break
			}
		}
	}
	if len(chosen) < 300 {
		return len(chosen), fmt.Errorf("only %d fresh healthy hproxy exits; pool remains unchanged", len(chosen))
	}

	tx := db.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	var inbounds []model.Inbound
	if err := tx.Where("tag LIKE ?", "pool-socks-%").Find(&inbounds).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	byTag := make(map[string]model.Inbound, len(inbounds))
	for _, inbound := range inbounds {
		byTag[inbound.Tag] = inbound
	}
	ids := make([]uint, 0, 300)
	links := make([]map[string]string, 0, 300)
	rules := make([]map[string]interface{}, 0, 300)
	for i, row := range chosen {
		port := 56000 + i
		tag := fmt.Sprintf("pool-socks-%d", port)
		options, _ := json.Marshal(map[string]interface{}{"listen": "::", "listen_port": port, "tag": tag, "type": "socks"})
		if old, ok := byTag[tag]; ok {
			if err := tx.Model(&model.Inbound{}).Where("id = ?", old.Id).Updates(map[string]interface{}{"type": "socks", "options": options, "out_json": []byte(`{}`)}).Error; err != nil {
				tx.Rollback()
				return 0, err
			}
			ids = append(ids, old.Id)
		} else {
			inbound := model.Inbound{Type: "socks", Tag: tag, Options: options, OutJson: []byte(`{}`), Addrs: []byte(`{}`)}
			if err := tx.Create(&inbound).Error; err != nil {
				tx.Rollback()
				return 0, err
			}
			ids = append(ids, inbound.Id)
		}
		remark := FormatStandardRemark("SUI", row.Country, row.Region, row.City, i+1)
		links = append(links, map[string]string{"type": "local", "remark": tag, "uri": fmt.Sprintf("socks5://dash.icta.top:%d#%s", port, remark)})
		rules = append(rules, map[string]interface{}{"inbound": []string{tag}, "outbound": row.Tag})
	}

	var setting model.Setting
	if err := tx.Where("key = ?", "config").First(&setting).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(setting.Value), &config); err != nil {
		tx.Rollback()
		return 0, err
	}
	route, _ := config["route"].(map[string]interface{})
	if route == nil {
		route = map[string]interface{}{}
		config["route"] = route
	}
	oldRules, _ := route["rules"].([]interface{})
	newRules := make([]interface{}, 0, len(oldRules)+len(rules))
	for _, raw := range oldRules {
		m, _ := raw.(map[string]interface{})
		remove := false
		if m != nil {
			if in, ok := m["inbound"].([]interface{}); ok {
				for _, v := range in {
					if s, ok := v.(string); ok && len(s) >= 11 && s[:11] == "pool-socks-" {
						remove = true
					}
				}
			}
		}
		if !remove {
			newRules = append(newRules, raw)
		}
	}
	for _, rule := range rules {
		newRules = append(newRules, rule)
	}
	route["rules"] = newRules
	updated, _ := json.Marshal(config)
	if err := tx.Model(&model.Setting{}).Where("id = ?", setting.Id).Update("value", string(updated)).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	var client model.Client
	if err := tx.Where("name = ?", "pool").First(&client).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	inboundJSON, _ := json.Marshal(ids)
	linksJSON, _ := json.Marshal(links)
	if err := tx.Model(&model.Client{}).Where("id = ?", client.Id).Updates(map[string]interface{}{"inbounds": inboundJSON, "links": linksJSON}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return len(chosen), nil
}
