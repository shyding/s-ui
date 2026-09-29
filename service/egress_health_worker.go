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
var egressReloadMu sync.Mutex
var lastEgressReload time.Time

const minimumEgressReloadInterval = 15 * time.Minute

func canReloadEgress(now time.Time) bool {
	egressReloadMu.Lock()
	defer egressReloadMu.Unlock()
	if !lastEgressReload.IsZero() && now.Sub(lastEgressReload) < minimumEgressReloadInterval {
		return false
	}
	lastEgressReload = now
	return true
}

func StartEgressHealthWorker(interval time.Duration, reload ...func() error) {
	egressHealthWorkerOnce.Do(func() {
		go func() {
			// Wait 5 minutes after startup so sing-box core can stabilize
			// before we begin testing nodes (avoids CPU spike at boot)
			time.Sleep(5 * time.Minute)
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
	protonCutoff := time.Now().Add(-25 * time.Hour).Unix()
	// Proton子节点：每天测一遍，低并发(3)
	db.Model(&model.Outbound{}).
		Where("tag LIKE ? AND (last_test_time < ? OR available = ?)", "out-proton-%", protonCutoff, false).
		Order("last_test_time ASC").Limit(20).Pluck("tag", &protonTags)
	if len(protonTags) > 0 {
		protonResults, err := (&NodeTestService{}).TestSelectedAndSave(protonTags, 3)
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
	// Seed节点：每天慢慢测，低并发(5)，每次200个
	var seedTags []string
	var seedSubscription model.Subscription
	if db.Where("name = ?", "Local v2rayN Seed Nodes").First(&seedSubscription).Error == nil {
		db.Model(&model.Outbound{}).
			Where("subscription_id = ? AND (last_test_time < ? OR available = ?)", seedSubscription.Id, protonCutoff, false).
			Order("last_test_time ASC").Limit(200).Pluck("tag", &seedTags)
	}
	seedPassed := 0
	if len(seedTags) > 0 {
		seedResults, err := (&NodeTestService{}).TestSelectedAndSave(seedTags, 5)
		if err != nil {
			logger.Warning("seed client node health check failed:", err)
		} else {
			failures := make(map[string]int)
			for _, result := range seedResults {
				if result.Available {
					seedPassed++
				} else {
					failures[seedFailureClass(result.Error)]++
				}
			}
			logger.Infof("seed client node health check finished: tested=%d passed=%d failures=%v", len(seedResults), seedPassed, failures)
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

	// HProxy候选：每天测一遍，低并发(5)，每次300个
	var candidateTags []string
	cutoff := time.Now().Add(-25 * time.Hour).Unix()
	db.Model(&model.Outbound{}).
		Where("tag LIKE ? AND (last_test_time < ? OR available = ?)", "hproxy-%", cutoff, false).
		Order("last_test_time ASC").Limit(300).Pluck("tag", &candidateTags)
	if len(candidateTags) == 0 {
		return
	}
	proxyResults, err := (&NodeTestService{}).TestSelectedAndSave(candidateTags, 5)
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
	if (passed > 0 || seedPassed > 0) && len(reload) > 0 && reload[0] != nil && canReloadEgress(time.Now()) {
		if err := reload[0](); err != nil {
			logger.Warning("sing-box reload after egress promotion failed:", err)
		} else {
			logger.Infof("sing-box reloaded after promoting %d public-proxy and %d seed exits", passed, seedPassed)
		}
	}
}

func seedFailureClass(err string) string {
	err = strings.ToLower(strings.TrimSpace(err))
	switch {
	case err == "":
		return "unknown"
	case strings.Contains(err, "not found in sing-box"):
		return "not_loaded"
	case strings.Contains(err, "deadline") || strings.Contains(err, "timeout"):
		return "timeout"
	case strings.Contains(err, "network is unreachable"):
		return "unreachable"
	case strings.Contains(err, "tls"):
		return "tls"
	case strings.Contains(err, "dns") || strings.Contains(err, "lookup"):
		return "dns"
	default:
		return "other"
	}
}
