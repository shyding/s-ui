package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alireza0/s-ui/database/model"
	"gorm.io/gorm"
)

// syncEndpointCandidates mirrors configured sing-box endpoints into the
// outbound health table. Endpoint records are not normal outbounds, so they
// were previously never tested or eligible for the public pool.
func syncEndpointCandidates(db *gorm.DB) []string {
	if db == nil {
		return nil
	}
	var tags []string
	upsert := func(tag, country, region, city string) {
		if tag == "" {
			return
		}
		var ob model.Outbound
		err := db.Where("tag = ?", tag).First(&ob).Error
		if err == gorm.ErrRecordNotFound {
			ob = model.Outbound{Tag: tag, Type: "wireguard", Options: json.RawMessage(`{}`)}
		} else if err != nil {
			return
		}
		ob.Country, ob.Region, ob.City = country, region, city
		if ob.Id == 0 {
			if db.Create(&ob).Error != nil {
				return
			}
		} else {
			_ = db.Model(&ob).Updates(map[string]interface{}{"country": country, "region": region, "city": city}).Error
		}
		tags = append(tags, tag)
	}

	var endpoints []model.Endpoint
	if db.Where("tag LIKE ?", "ep-proton-%").Find(&endpoints).Error == nil {
		for _, ep := range endpoints {
			if country, ok := protonCountryFromEndpointTag(ep.Tag); ok {
				upsert(ep.Tag, strings.ToUpper(country), strings.ToUpper(country), strings.ToUpper(country))
			}
		}
	}

	var cloudflare []model.CloudflareEndpoint
	if db.Where("status = ?", "online").Find(&cloudflare).Error == nil {
		for _, ep := range cloudflare {
			if ep.IP == "" || ep.Port == 0 || ep.Loc == "" {
				continue
			}
			cleanIP := strings.NewReplacer(".", "-", ":", "-").Replace(ep.IP)
			tag := fmt.Sprintf("ep-cf-%s-%d", cleanIP, ep.Port)
			region, city := ep.Colo, ep.City
			if region == "" {
				region = ep.Loc
			}
			if city == "" {
				city = region
			}
			upsert(tag, strings.ToUpper(ep.Loc), region, city)
		}
	}
	return tags
}
