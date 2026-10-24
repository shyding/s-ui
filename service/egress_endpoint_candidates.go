package service

import (
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
	upsert := func(endpointTag, country, region, city string) {
		if endpointTag == "" {
			return
		}
		tag := "out-" + endpointTag
		wrapper, err := BuildDirectOutboundJson(tag, endpointTag)
		if err != nil {
			return
		}
		var ob model.Outbound
		err = db.Where("tag = ?", tag).First(&ob).Error
		if err == gorm.ErrRecordNotFound {
			ob = model.Outbound{Tag: tag, Type: "selector"}
			if ob.UnmarshalJSON(wrapper) != nil {
				return
			}
		} else if err != nil {
			return
		}
		if ob.Id != 0 {
			// Repair rows created by the previous broken implementation.
			ob.Type = "selector"
			if ob.UnmarshalJSON(wrapper) != nil {
				return
			}
		}
		ob.Country, ob.Region, ob.City = country, region, city
		if ob.Id == 0 {
			if db.Create(&ob).Error != nil {
				return
			}
		} else {
			_ = db.Save(&ob).Error
		}
		tags = append(tags, tag)
	}
	// Remove only synthetic rows from the old implementation; endpoint records
	// (and their private keys) remain untouched.
	db.Where("tag LIKE ? AND type = ?", "ep-%", "wireguard").Delete(&model.Outbound{})

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
