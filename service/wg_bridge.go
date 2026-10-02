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
// 包括：Cloudflare WARP (cf-*-pool), Proton (out-proton-*)
func GetHealthyWireGuardOutbounds(db *gorm.DB) []model.Outbound {
	var outbounds []model.Outbound
	// Cloudflare WARP (cf-*-pool) 和 Proton (out-proton-*) 都是 WireGuard
	// 通过SOCKS桥接暴露（WireGuard无法通过iptables DNAT）
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
// 规范：100%中文/数字，0英文（provider名称除外）
func buildWGBridgeRemark(outboundTag string) string {
	tag := strings.ToLower(outboundTag)
	if strings.HasPrefix(tag, "cf-") {
		// Cloudflare WARP: cf-de-pool -> ☁️Cloudflare-德国-WARP
		region := strings.TrimPrefix(tag, "cf-")
		region = strings.TrimSuffix(region, "-pool")
		countryCN := wgRegionToChinese(region)
		return fmt.Sprintf("☁️Cloudflare-%s-WARP", countryCN)
	}
	if strings.HasPrefix(tag, "out-proton-") {
		// Proton: out-proton-us-01 -> 🔒Proton-美国-01
		name := strings.TrimPrefix(tag, "out-proton-")
		// 提取国家代码和编号
		parts := strings.Split(name, "-")
		countryCN := name
		suffix := ""
		if len(parts) >= 2 {
			countryCN = wgRegionToChinese(parts[0])
			suffix = "-" + strings.Join(parts[1:], "-")
		} else if len(parts) == 1 {
			countryCN = wgRegionToChinese(parts[0])
		}
		return fmt.Sprintf("🔒Proton-%s%s", countryCN, suffix)
	}
	if strings.HasPrefix(tag, "hproxy-") {
		// HProxy: hproxy-us-north-bergen-198-199-86-11-3128 -> 🌐HProxy-美国-01
		// 提取国家代码（hproxy-<cc>-...）
		rest := strings.TrimPrefix(tag, "hproxy-")
		parts := strings.Split(rest, "-")
		countryCN := rest
		if len(parts) >= 1 {
			countryCN = wgRegionToChinese(parts[0])
		}
		// 使用tag的hash生成稳定编号，避免重复
		hash := 0
		for _, c := range tag {
			hash = hash*31 + int(c)
		}
		if hash < 0 {
			hash = -hash
		}
		num := hash % 900 + 100 // 100-999
		return fmt.Sprintf("🌐HProxy-%s-%d", countryCN, num)
	}
	return fmt.Sprintf("🔗%s", outboundTag)
}

// wgRegionToChinese 将地区代码转换为中文
func wgRegionToChinese(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	m := map[string]string{
		"de": "德国", "us": "美国", "uk": "英国", "fr": "法国",
		"nl": "荷兰", "se": "瑞典", "ch": "瑞士", "at": "奥地利",
		"be": "比利时", "dk": "丹麦", "fi": "芬兰", "ie": "爱尔兰",
		"it": "意大利", "es": "西班牙", "pt": "葡萄牙", "pl": "波兰",
		"cz": "捷克", "hu": "匈牙利", "ro": "罗马尼亚", "bg": "保加利亚",
		"hr": "克罗地亚", "sk": "斯洛伐克", "si": "斯洛文尼亚", "ee": "爱沙尼亚",
		"lv": "拉脱维亚", "lt": "立陶宛", "gr": "希腊", "no": "挪威",
		"is": "冰岛", "lu": "卢森堡", "mt": "马耳他", "cy": "塞浦路斯",
		"jp": "日本", "kr": "韩国", "sg": "新加坡", "hk": "香港",
		"tw": "台湾", "au": "澳大利亚", "nz": "新西兰", "ca": "加拿大",
		"mx": "墨西哥", "br": "巴西", "ar": "阿根廷", "cl": "智利",
		"in": "印度", "id": "印尼", "my": "马来西亚", "th": "泰国",
		"vn": "越南", "ph": "菲律宾",
	}
	if cn, ok := m[code]; ok {
		return cn
	}
	return code // 未知代码保留原文
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
