package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"

	"gorm.io/gorm"
)

// WireGuard桥接：将WireGuard出站（Cloudflare WARP / Proton VPN）通过SOCKS入站暴露
// 客户端看到的是38类型中的SOCKS节点，实际流量经由WireGuard出站转发
//
// 设计：
//   - 每个健康的WireGuard出站分配一个独立的SOCKS入站（端口54200-54399）
//   - 路由规则：wg-bridge-<name> -> <wg-outbound-tag>
//   - 订阅中发布为 socks5://dash.icta.top:PORT#remark

const (
	// WGBridgePortStart WireGuard桥接端口起始
	WGBridgePortStart = 54200
	// WGBridgePortEnd WireGuard桥接端口结束
	WGBridgePortEnd = 54399
	// WGBridgeTagPrefix 桥接入站标签前缀
	WGBridgeTagPrefix = "wg-bridge-"
)

var (
	wgBridgeMu       sync.Mutex
	wgNonAlnumRegex  = regexp.MustCompile(`[^a-z0-9]+`)
)

// WGBridgeInfo WireGuard桥接信息
type WGBridgeInfo struct {
	OutboundTag string // WireGuard出站标签
	BridgeTag   string // 桥接入站标签
	Port        int    // 桥接端口
	Remark      string // 订阅显示名称
}

// sanitizeBridgeName 清理名称用于标签
func sanitizeBridgeName(name string) string {
	s := strings.ToLower(name)
	s = wgNonAlnumRegex.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "wg"
	}
	return s
}

// GetHealthyWireGuardOutbounds 获取健康的WireGuard出站
func GetHealthyWireGuardOutbounds(db *gorm.DB) []model.Outbound {
	var outbounds []model.Outbound
	// Cloudflare WARP (cf-*-pool) 和 Proton (out-proton-*) 都是 WireGuard
	db.Where("available = ? AND (tag LIKE ? OR tag LIKE ? OR type = ?)",
		true, "cf-%", "out-proton-%", "wireguard").Find(&outbounds)
	return outbounds
}

// EnsureWireGuardBridges 确保WireGuard桥接入站存在（幂等）
// 为每个健康的WireGuard出站创建一个SOCKS桥接入站
// 返回创建/已存在的桥接列表
func EnsureWireGuardBridges(db *gorm.DB) ([]WGBridgeInfo, error) {
	wgBridgeMu.Lock()
	defer wgBridgeMu.Unlock()

	wgOutbounds := GetHealthyWireGuardOutbounds(db)
	if len(wgOutbounds) == 0 {
		return nil, nil
	}

	// 获取已使用的桥接端口
	var existingBridges []model.Inbound
	db.Where("tag LIKE ?", WGBridgeTagPrefix+"%").Find(&existingBridges)

	usedPorts := make(map[int]bool)
	existingByOutbound := make(map[string]*model.Inbound) // outboundTag -> inbound
	for i := range existingBridges {
		ib := &existingBridges[i]
		var opts map[string]interface{}
		if err := json.Unmarshal(ib.Options, &opts); err == nil {
			if port, ok := opts["listen_port"].(float64); ok {
				usedPorts[int(port)] = true
			}
		}
		// 从标签解析出站标签：wg-bridge-<sanitized-outbound-tag>
		// 我们在 options 中存储源出站标签以便精确匹配
		if srcTag, ok := opts["wg_bridge_source"].(string); ok && srcTag != "" {
			existingByOutbound[srcTag] = ib
		}
	}

	var bridges []WGBridgeInfo
	nextPort := WGBridgePortStart
	// 找到下一个可用端口
	for nextPort <= WGBridgePortEnd && usedPorts[nextPort] {
		nextPort++
	}

	for _, ob := range wgOutbounds {
		// 检查是否已存在该出站的桥接
		if existing, ok := existingByOutbound[ob.Tag]; ok {
			var opts map[string]interface{}
			port := 0
			if err := json.Unmarshal(existing.Options, &opts); err == nil {
				if p, ok := opts["listen_port"].(float64); ok {
					port = int(p)
				}
			}
			bridges = append(bridges, WGBridgeInfo{
				OutboundTag: ob.Tag,
				BridgeTag:   existing.Tag,
				Port:        port,
				Remark:      buildWGBridgeRemark(ob.Tag),
			})
			continue
		}

		// 分配新端口
		for nextPort <= WGBridgePortEnd && usedPorts[nextPort] {
			nextPort++
		}
		if nextPort > WGBridgePortEnd {
			break // 端口耗尽
		}
		port := nextPort
		usedPorts[port] = true
		nextPort++

		bridgeTag := WGBridgeTagPrefix + sanitizeBridgeName(ob.Tag)

		// 创建SOCKS入站
		opts := map[string]interface{}{
			"type":             "socks",
			"tag":              bridgeTag,
			"listen":           "::",
			"listen_port":      port,
			"tcp_fast_open":    true,
			"sniff":            true,
			"wg_bridge_source": ob.Tag, // 记录源出站标签
		}
		optsJSON, _ := json.Marshal(opts)

		inbound := model.Inbound{
			Type:    "socks",
			Tag:     bridgeTag,
			Options: optsJSON,
		}
		if err := db.Create(&inbound).Error; err != nil {
			// 可能标签已存在（并发），跳过
			continue
		}

		bridges = append(bridges, WGBridgeInfo{
			OutboundTag: ob.Tag,
			BridgeTag:   bridgeTag,
			Port:        port,
			Remark:      buildWGBridgeRemark(ob.Tag),
		})
	}

	// 按端口排序，保证稳定性
	sort.Slice(bridges, func(i, j int) bool {
		return bridges[i].Port < bridges[j].Port
	})

	return bridges, nil
}

// buildWGBridgeRemark 构建桥接节点的订阅显示名称
func buildWGBridgeRemark(outboundTag string) string {
	// cf-de-pool -> 🇩🇪CF-德国-法兰克福-WARP-01
	// out-proton-us-01 -> 🇺🇸Proton-美国-VPN-01
	tag := strings.ToLower(outboundTag)
	if strings.HasPrefix(tag, "cf-") {
		// Cloudflare WARP
		region := strings.TrimPrefix(tag, "cf-")
		region = strings.TrimSuffix(region, "-pool")
		return fmt.Sprintf("☁️CF-WARP-%s", strings.ToUpper(region))
	}
	if strings.HasPrefix(tag, "out-proton-") {
		name := strings.TrimPrefix(tag, "out-proton-")
		return fmt.Sprintf("🔒Proton-%s", name)
	}
	return fmt.Sprintf("🔗%s", outboundTag)
}

// CleanupStaleWGBridges 清理已不健康的WireGuard出站对应的桥接
func CleanupStaleWGBridges(db *gorm.DB) error {
	wgBridgeMu.Lock()
	defer wgBridgeMu.Unlock()

	// 获取当前健康的WG出站标签集合
	healthy := GetHealthyWireGuardOutbounds(db)
	healthyTags := make(map[string]bool)
	for _, ob := range healthy {
		healthyTags[ob.Tag] = true
	}

	// 获取所有桥接入站
	var bridges []model.Inbound
	db.Where("tag LIKE ?", WGBridgeTagPrefix+"%").Find(&bridges)

	for _, ib := range bridges {
		var opts map[string]interface{}
		if err := json.Unmarshal(ib.Options, &opts); err != nil {
			continue
		}
		srcTag, _ := opts["wg_bridge_source"].(string)
		if srcTag == "" || !healthyTags[srcTag] {
			// 源出站已不健康或不存在，删除桥接
			db.Delete(&ib)
		}
	}
	return nil
}

// GetWGBridgeRouteRules 生成WireGuard桥接的路由规则
// 格式：{"action": "route", "inbound": ["wg-bridge-x"], "outbound": "cf-x-pool"}
func GetWGBridgeRouteRules(db *gorm.DB) []map[string]interface{} {
	var bridges []model.Inbound
	db.Where("tag LIKE ?", WGBridgeTagPrefix+"%").Find(&bridges)

	var rules []map[string]interface{}
	for _, ib := range bridges {
		var opts map[string]interface{}
		if err := json.Unmarshal(ib.Options, &opts); err != nil {
			continue
		}
		srcTag, _ := opts["wg_bridge_source"].(string)
		if srcTag == "" {
			continue
		}
		// 验证源出站仍然健康
		var ob model.Outbound
		if err := db.Where("tag = ? AND available = ?", srcTag, true).First(&ob).Error; err != nil {
			continue
		}
		rules = append(rules, map[string]interface{}{
			"action":   "route",
			"inbound":  []string{ib.Tag},
			"outbound": srcTag,
		})
	}
	return rules
}

// WGBridgeCandidates 将WireGuard桥接转换为订阅候选节点（SOCKS类型）
// 返回桥接信息，由调用方（sub包）转换为CandidateNode
func WGBridgeCandidates(db *gorm.DB) []WGBridgeInfo {
	bridges, err := EnsureWireGuardBridges(db)
	if err != nil || len(bridges) == 0 {
		return nil
	}
	// 过滤掉无效端口
	var valid []WGBridgeInfo
	for _, b := range bridges {
		if b.Port > 0 {
			valid = append(valid, b)
		}
	}
	return valid
}

// InjectWGBridgeRouteRules 将WireGuard桥接路由规则注入到现有的路由规则列表
// 每个桥接规则：{"action": "route", "inbound": ["wg-bridge-x"], "outbound": "cf-x-pool"}
// 规则插入到列表开头，确保优先匹配
func InjectWGBridgeRouteRules(rules []interface{}) []interface{} {
	db := database.GetDB()
	if db == nil {
		return rules
	}

	bridgeRules := GetWGBridgeRouteRules(db)
	if len(bridgeRules) == 0 {
		return rules
	}

	// 移除已存在的桥接规则（避免重复）
	var filtered []interface{}
	for _, r := range rules {
		if rMap, ok := r.(map[string]interface{}); ok {
			if inbounds, ok := rMap["inbound"].([]interface{}); ok && len(inbounds) > 0 {
				if inStr, ok := inbounds[0].(string); ok && strings.HasPrefix(inStr, WGBridgeTagPrefix) {
					continue // 跳过旧的桥接规则
				}
			}
			// 也检查 []string 类型
			if inbounds, ok := rMap["inbound"].([]string); ok && len(inbounds) > 0 {
				if strings.HasPrefix(inbounds[0], WGBridgeTagPrefix) {
					continue
				}
			}
		}
		filtered = append(filtered, r)
	}

	// 将新的桥接规则插入到开头
	var newRules []interface{}
	for _, br := range bridgeRules {
		newRules = append(newRules, br)
	}
	newRules = append(newRules, filtered...)

	return newRules
}

// 确保 database 包被引用（避免未使用导入错误）
var _ = database.GetDB
