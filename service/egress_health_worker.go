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

func StartEgressHealthWorker(interval time.Duration) {
	egressHealthWorkerOnce.Do(func() {
		go func() {
			runEgressHealthCheck()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				runEgressHealthCheck()
			}
		}()
	})
}

func runEgressHealthCheck() {
	db := database.GetDB()
	if db == nil {
		return
	}

	seen := make(map[string]bool)
	var tags []string
	for _, region := range GetActiveEgressRegions(db) {
		if region.OutboundTag != "" && !seen[region.OutboundTag] {
			seen[region.OutboundTag] = true
			tags = append(tags, region.OutboundTag)
		}
	}
	if len(tags) == 0 {
		return
	}

	results, err := (&NodeTestService{}).TestSelectedAndSave(tags, 1)
	if err != nil {
		logger.Warning("egress health check failed:", err)
		return
	}
	for _, result := range results {
		if !result.Available {
			logger.Warningf("egress pool %s unavailable: %s", result.Tag, result.Error)
		}
	}

	var candidateTags []string
	cutoff := time.Now().Add(-30 * time.Minute).Unix()
	db.Model(&model.Outbound{}).
		Where("tag LIKE ? AND (last_test_time < ? OR available = ?)", "hproxy-%", cutoff, false).
		Order("last_test_time ASC").Limit(200).Pluck("tag", &candidateTags)
	if len(candidateTags) == 0 {
		return
	}
	proxyResults, err := (&NodeTestService{}).TestSelectedOutboundsWithIPInternal(candidateTags, 20)
	if err != nil {
		logger.Warning("HProxy candidate health check failed:", err)
		return
	}
	passed := 0
	for _, result := range proxyResults {
		if result.Available && strings.HasPrefix(result.Tag, "hproxy-") {
			passed++
		}
	}
	logger.Infof("HProxy candidate health check finished: tested=%d passed=%d", len(proxyResults), passed)
}
