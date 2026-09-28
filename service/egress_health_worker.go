package service

import (
	"strings"
	"sync"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
)

var egressHealthWorkerOnce sync.Once

func StartEgressHealthWorker(interval time.Duration, reload ...func() error) {
	egressHealthWorkerOnce.Do(func() {
		go func() {
			time.Sleep(time.Minute)
			runEgressHealthCheck(reload...)
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				runEgressHealthCheck(reload...)
			}
		}()
	})
}

func runEgressHealthCheck(reload ...func() error) {
	db := database.GetDB()
	if db == nil {
		return
	}

	tags := []string{"warp-6eV"}
	var protonTags []string
	protonCutoff := time.Now().Add(-30 * time.Minute).Unix()
	db.Model(&model.Outbound{}).
		Where("tag LIKE ? AND (last_test_time < ? OR available = ?)", "out-proton-%", protonCutoff, false).
		Order("last_test_time ASC").Limit(12).Pluck("tag", &protonTags)
	if len(protonTags) > 0 {
		protonResults, err := (&NodeTestService{}).TestSelectedAndSave(protonTags, 6)
		if err != nil {
			logger.Warning("Proton child health check failed:", err)
		} else {
			passed := 0
			for _, result := range protonResults {
				if result.Available {
					passed++
				}
			}
			logger.Infof("Proton child health check finished: tested=%d passed=%d", len(protonResults), passed)
		}
	}
	var seedTags []string
	var seedSubscription model.Subscription
	if db.Where("name = ?", "Local v2rayN Seed Nodes").First(&seedSubscription).Error == nil {
		db.Model(&model.Outbound{}).
			Where("subscription_id = ? AND (last_test_time < ? OR available = ?)", seedSubscription.Id, protonCutoff, false).
			Order("last_test_time ASC").Limit(400).Pluck("tag", &seedTags)
	}
	seedPassed := 0
	if len(seedTags) > 0 {
		seedResults, err := (&NodeTestService{}).TestSelectedAndSave(seedTags, 30)
		if err != nil {
			logger.Warning("seed client node health check failed:", err)
		} else {
			for _, result := range seedResults {
				if result.Available {
					seedPassed++
				}
			}
			logger.Infof("seed client node health check finished: tested=%d passed=%d", len(seedResults), seedPassed)
		}
	}

	for _, region := range GetProtonEgressRegions(db) {
		if region.OutboundTag != "" {
			tags = append(tags, region.OutboundTag)
		}
	}
	for _, region := range GetActiveCloudflareRegions(db) {
		if region.OutboundTag != "" {
			tags = append(tags, region.OutboundTag)
		}
	}

	results, err := (&NodeTestService{}).TestSelectedAndSave(tags, min(len(tags), 12))
	if err != nil {
		logger.Warning("egress health check failed:", err)
	} else {
		for _, result := range results {
			if !result.Available {
				logger.Warningf("egress pool %s unavailable: %s", result.Tag, result.Error)
			}
		}
	}

	// Public-proxy candidates are tested through the running Sing-Box core and are
	// promoted in small batches. This keeps the VPS responsive while steadily
	// increasing the verified city inventory.
	var candidateTags []string
	cutoff := time.Now().Add(-30 * time.Minute).Unix()
	db.Model(&model.Outbound{}).
		Where("tag LIKE ? AND (last_test_time < ? OR available = ?)", "hproxy-%", cutoff, false).
		Order("last_test_time ASC").Limit(800).Pluck("tag", &candidateTags)
	if len(candidateTags) == 0 {
		return
	}
	proxyResults, err := (&NodeTestService{}).TestSelectedAndSave(candidateTags, 30)
	if err != nil {
		logger.Warning("public-proxy candidate health check failed:", err)
		return
	}
	passed := 0
	for _, result := range proxyResults {
		if result.Available && strings.HasPrefix(result.Tag, "hproxy-") {
			passed++
		}
	}
	logger.Infof("public-proxy candidate health check finished: tested=%d passed=%d", len(proxyResults), passed)
	if (passed > 0 || seedPassed > 0) && len(reload) > 0 && reload[0] != nil {
		if err := reload[0](); err != nil {
			logger.Warning("sing-box reload after egress promotion failed:", err)
		} else {
			logger.Infof("sing-box reloaded after promoting %d public-proxy and %d seed exits", passed, seedPassed)
		}
	}
}
