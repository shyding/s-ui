package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alireza0/s-ui/database/model"
	"gorm.io/gorm"
)

func hproxyPoolTag(country, city string) string {
	return fmt.Sprintf("hproxy-%s-%s-pool", strings.ToLower(SanitizeTag(country)), strings.ToLower(SanitizeTag(city)))
}

func EnsureHProxyPoolsInOutbounds(config *SingBoxConfig, db *gorm.DB) {
	if config == nil || db == nil {
		return
	}
	var candidates []model.Outbound
	if err := db.Where("tag LIKE ? AND available = ?", "hproxy-%", true).Find(&candidates).Error; err != nil {
		return
	}
	groups := make(map[string][]string)
	for _, candidate := range candidates {
		if candidate.Country == "" || candidate.City == "" {
			continue
		}
		groups[hproxyPoolTag(candidate.Country, candidate.City)] = append(groups[hproxyPoolTag(candidate.Country, candidate.City)], candidate.Tag)
	}

	filtered := make([]json.RawMessage, 0, len(config.Outbounds))
	for _, raw := range config.Outbounds {
		var item map[string]interface{}
		if json.Unmarshal(raw, &item) == nil {
			if tag, _ := item["tag"].(string); strings.HasPrefix(tag, "hproxy-") && strings.HasSuffix(tag, "-pool") {
				continue
			}
		}
		filtered = append(filtered, raw)
	}
	config.Outbounds = filtered
	for tag, members := range groups {
		if len(members) == 0 {
			continue
		}
		pool, err := BuildUrlTestPoolJsonWithTolerance(tag, members, "5m", 1000)
		if err == nil {
			config.Outbounds = append(config.Outbounds, pool)
		}
	}
}

func EnsureSeedPoolInOutbounds(config *SingBoxConfig, db *gorm.DB) {
	if config == nil || db == nil {
		return
	}
	var subscription model.Subscription
	if db.Where("name = ?", "Local v2rayN Seed Nodes").First(&subscription).Error != nil {
		return
	}
	var candidates []model.Outbound
	if err := db.Where("subscription_id = ? AND available = ?", subscription.Id, true).Find(&candidates).Error; err != nil {
		return
	}
	filtered := make([]json.RawMessage, 0, len(config.Outbounds))
	for _, raw := range config.Outbounds {
		var item map[string]interface{}
		if json.Unmarshal(raw, &item) == nil {
			if tag, _ := item["tag"].(string); tag == "seed-pool" {
				continue
			}
		}
		filtered = append(filtered, raw)
	}
	config.Outbounds = filtered
	if len(candidates) == 0 {
		return
	}
	members := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		members = append(members, candidate.Tag)
	}
	pool, err := BuildUrlTestPoolJsonWithTolerance("seed-pool", members, "5m", 1000)
	if err == nil {
		config.Outbounds = append(config.Outbounds, pool)
	}
}
