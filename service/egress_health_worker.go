package service

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
)

var egressHealthWorkerOnce sync.Once
var egressReloadMu sync.Mutex
var lastEgressReload time.Time

// isEgressCheckRunning 防止并发：同一时刻只允许一个 check 实例运行
var isEgressCheckRunning atomic.Bool

// 进度追踪：已完成数 / 总数
var egressProgressDone atomic.Int32
var egressProgressTotal atomic.Int32
var nodeProgressDone atomic.Int32
var nodeProgressTotal atomic.Int32

// egressReloadFns 存储 reload 回调，供手动触发时使用
var egressReloadFns []func() error

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

// nextDailyAt 计算距今最近的下一个 hour:minute（本地时间）
func nextDailyAt(hour, minute int) time.Duration {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return time.Until(next)
}

// nextScheduledRun 从数据库读取 healthCheckTime 设置（格式 HH:MM），
// 计算距下一次运行的时长。解析失败时默认使用 03:30。
func nextScheduledRun() (time.Duration, string) {
	timeStr := GetHealthCheckTime() // e.g. "03:30"
	parts := strings.SplitN(timeStr, ":", 2)
	hour, minute := 3, 30
	if len(parts) == 2 {
		if h, err := strconv.Atoi(parts[0]); err == nil && h >= 0 && h <= 23 {
			hour = h
		}
		if m, err := strconv.Atoi(parts[1]); err == nil && m >= 0 && m <= 59 {
			minute = m
		}
	}
	return nextDailyAt(hour, minute), timeStr
}

// StartEgressHealthWorker 启动出口健康检查 Worker：
//   - 启动后延迟 5 分钟再跑第一轮（避免启动时 CPU 过载导致 VPS 卡死）
//   - 之后每 30 分钟跑一次
//   - 用户可通过前端手动触发
func StartEgressHealthWorker(reload ...func() error) {
	egressReloadFns = reload
	egressHealthWorkerOnce.Do(func() {
		go func() {
			// 启动后延迟 5 分钟再跑第一轮，让系统先稳定下来
			// （直接启动就跑曾导致 VPS CPU/内存耗尽卡死）
			logger.Info("EgressHealthWorker: first run in 5 minutes (delayed to avoid startup overload)")
			time.Sleep(5 * time.Minute)
			safeRunEgressHealthCheck()
			for {
				d := 30 * time.Minute
				logger.Infof("EgressHealthWorker: Next run in 30 minutes")
				time.Sleep(d)
				safeRunEgressHealthCheck()
			}
		}()
	})
}

// TriggerEgressHealthCheck 供 UI 手动触发；若已在运行则返回 false
func TriggerEgressHealthCheck() bool {
	if !isEgressCheckRunning.CompareAndSwap(false, true) {
		return false // 已有实例在跑，拒绝
	}
	go func() {
		defer isEgressCheckRunning.Store(false)
		runEgressHealthCheck(egressReloadFns...)
	}()
	return true
}

// IsEgressCheckRunning 查询当前是否正在检测（供 UI 显示状态）
func IsEgressCheckRunning() bool {
	return isEgressCheckRunning.Load()
}

// GetEgressProgress 返回出口检测进度 (done, total)
func GetEgressProgress() (int32, int32) {
	return egressProgressDone.Load(), egressProgressTotal.Load()
}

// GetNodeProgress 返回节点检测进度 (done, total)
func GetNodeProgress() (int32, int32) {
	return nodeProgressDone.Load(), nodeProgressTotal.Load()
}

// ResetEgressProgress 重置进度计数器
func ResetEgressProgress(total int32) {
	egressProgressDone.Store(0)
	egressProgressTotal.Store(total)
}

// ResetNodeProgress 重置节点检测进度计数器
func ResetNodeProgress(total int32) {
	nodeProgressDone.Store(0)
	nodeProgressTotal.Store(total)
}

func safeRunEgressHealthCheck() {
	if !isEgressCheckRunning.CompareAndSwap(false, true) {
		logger.Info("EgressHealthWorker: 上次检测仍在运行，跳过本次")
		return
	}
	defer isEgressCheckRunning.Store(false)
	runEgressHealthCheck(egressReloadFns...)
}

func runEgressHealthCheck(reload ...func() error) {
	db := database.GetDB()
	if db == nil {
		return
	}

	tags := []string{"warp-6eV"}
	// 1. Regional pools (Cloudflare and Proton) are high-priority: test them FIRST
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

	var protonTags []string
	protonCutoff := time.Now().Add(-25 * time.Hour).Unix()
	// 2. Proton child nodes: daily check
	db.Model(&model.Outbound{}).
		Where("tag LIKE ? AND (last_test_time < ? OR available = ?)", "out-proton-%", protonCutoff, false).
		Order("last_test_time ASC").Limit(20).Pluck("tag", &protonTags)

	// 3. Seed nodes: background check
	var seedTags []string
	var seedSubscription model.Subscription
	if db.Where("name = ?", "Local v2rayN Seed Nodes").First(&seedSubscription).Error == nil {
		db.Model(&model.Outbound{}).
			Where("subscription_id = ? AND (last_test_time < ? OR available = ?)", seedSubscription.Id, protonCutoff, false).
			Order("last_test_time ASC").Limit(200).Pluck("tag", &seedTags)
	}

	// 设置总进度（包含 HProxy）
	hproxyCutoff := time.Now().Add(-15 * time.Minute).Unix()
	var hproxyTags []string
	db.Model(&model.Outbound{}).
		Where("tag LIKE ? AND (last_test_time < ? OR available = ?)", "hproxy-%", hproxyCutoff, false).
		Order("last_test_time ASC").Limit(300).Pluck("tag", &hproxyTags)

	total := int32(len(tags) + len(protonTags) + len(seedTags) + len(hproxyTags))
	ResetEgressProgress(total)
	logger.Infof("EgressHealthWorker: 开始检测 %d 个出口节点", total)

	results, err := (&NodeTestService{}).TestSelectedAndSave(tags, min(len(tags), 12))
	egressProgressDone.Add(int32(len(tags)))
	if err != nil {
		logger.Warning("egress health check failed:", err)
	} else {
		for _, result := range results {
			if !result.Available {
				logger.Warningf("egress pool %s unavailable: %s", result.Tag, result.Error)
			}
		}
	}

	// 2. Proton child nodes: daily check (tags already collected above)
	if len(protonTags) > 0 {
		protonResults, err := (&NodeTestService{}).TestSelectedAndSave(protonTags, 3)
		egressProgressDone.Add(int32(len(protonTags)))
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

	// 3. Seed nodes: background check (tags already collected above)
	seedPassed := 0
	if len(seedTags) > 0 {
		seedResults, err := (&NodeTestService{}).TestSelectedAndSave(seedTags, 5)
		egressProgressDone.Add(int32(len(seedTags)))
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


	// HProxy候选：每天测一遍，低并发(10)，每次300个
	// （曾用 50 并发/1500 上限导致 VPS 资源耗尽卡死，现降低）
	// 标签已在进度设置时收集
	candidateTags := hproxyTags
	if len(candidateTags) == 0 {
		return
	}
	proxyResults, err := (&NodeTestService{}).TestSelectedAndSave(candidateTags, 10)
	egressProgressDone.Add(int32(len(candidateTags)))
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
