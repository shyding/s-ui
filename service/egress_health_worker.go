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

	var tags []string
	for _, region := range StandardEgressRegions {
		if region.OutboundTag != "" {
			tags = append(tags, region.OutboundTag)
		}
	}

	results, err := (&NodeTestService{}).TestSelectedAndSave(tags, len(tags))
	if err != nil {
		logger.Warning("egress health check failed:", err)
	} else {
		for _, result := range results {
			if !result.Available {
				logger.Warningf("egress pool %s unavailable: %s", result.Tag, result.Error)
			}
		}
	}

	// HProxy candidates are tested through the running Sing-Box core and are
	// promoted in small batches. This keeps the VPS responsive while steadily
	// increasing the verified city inventory.
	var candidateTags []string
	cutoff := time.Now().Add(-30 * time.Minute).Unix()
	db.Model(&model.Outbound{}).
		Where("tag LIKE ? AND (last_test_time < ? OR available = ?)", "hproxy-%", cutoff, false).
		Order("last_test_time ASC").Limit(200).Pluck("tag", &candidateTags)
	if len(candidateTags) == 0 {
		return
	}
	proxyResults, err := (&NodeTestService{}).TestSelectedAndSave(candidateTags, 20)
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
	if passed > 0 && len(reload) > 0 && reload[0] != nil {
		if err := reload[0](); err != nil {
			logger.Warning("sing-box reload after HProxy promotion failed:", err)
		} else {
			logger.Infof("sing-box reloaded after promoting %d HProxy exits", passed)
		}
	}
}
