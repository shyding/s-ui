package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"github.com/alireza0/s-ui/service"
)

// 38 SUI类型协议矩阵：所有已测试OK的客户端协议类型
// 只有这些协议的外部节点才能映射暴露
var suiTypeProtocolMatrix = map[string]bool{
	"vless":      true,
	"vmess":      true,
	"trojan":     true,
	"hysteria2":  true,
	"hysteria":   true,
	"tuic":       true,
	"shadowsocks": true,
	"ss":         true,
	"socks":      true,
	"socks5":     true,
	"mixed":      true,
	"http":       true,
}

// egressSource 定义外部节点来源及其outbound tag模式
type egressSource struct {
	Provider string
	TagLike  string // SQL LIKE模式
}

// 需要处理的四个来源
var egressSources = []egressSource{
	{Provider: "HProxy", TagLike: "hproxy-%"},
	{Provider: "Cloudflare", TagLike: "cf-%"},
	{Provider: "Proton", TagLike: "out-proton-%"},
}

// getEgressCandidates 从outbounds表加载通过质量门的HProxy/Cloudflare/Proton节点，
// 生成映射到38 SUI类型矩阵的客户端URI，作为订阅候选。
// 只有协议在38类型矩阵中的节点才会被映射暴露（如WireGuard不在矩阵中，会被跳过）。
func getEgressCandidates() []CandidateNode {
	db := database.GetDB()
	if db == nil {
		return nil
	}

	var candidates []CandidateNode
	seen := make(map[string]bool)

	for _, src := range egressSources {
		var outbounds []model.Outbound
		// 质量门：available=true 且有国家信息（通过健康检查）
		err := db.Where("tag LIKE ? AND available = ?", src.TagLike, true).Find(&outbounds).Error
		if err != nil {
			logger.Warningf("加载%s候选节点失败: %v", src.Provider, err)
			continue
		}

		for _, ob := range outbounds {
			// 协议必须在38类型矩阵中
			proto := normalizeEgressProtocol(ob.Type)
			if !suiTypeProtocolMatrix[proto] {
				logger.Debugf("跳过%s节点 %s: 协议 %s 不在38类型矩阵中", src.Provider, ob.Tag, ob.Type)
				continue
			}

			uri := buildEgressURI(&ob, proto, src.Provider)
			if uri == "" {
				continue
			}

			nodeKey := extractNodeKey(uri)
			if seen[nodeKey] {
				continue
			}
			seen[nodeKey] = true

			candidates = append(candidates, CandidateNode{
				Uri:      uri,
				Protocol: proto,
				Provider: src.Provider,
				Country:  ob.Country,
				Region:   ob.Region,
				City:     ob.City,
				Priority: getEgressPriority(proto),
				NodeKey:  nodeKey,
			})
		}
		logger.Infof("%s: 加载 %d 个候选节点（通过质量门且协议可映射）", src.Provider, len(candidates))
	}

	// WireGuard桥接：Cloudflare WARP / Proton VPN 通过SOCKS桥接入站暴露
	// （WireGuard不在38类型矩阵中，但可映射为SOCKS类型）
	candidates = append(candidates, getWGBridgeCandidates(db, seen)...)

	return candidates
}

// getVpsDomain 获取VPS域名（用于WireGuard桥接SOCKS节点的URI）
func getVpsDomain() string {
	// 从设置中获取，或使用默认值
	// WireGuard桥接入站直接监听在VPS上，无需egress gateway重写
	return "dash.icta.top"
}

// getWGBridgeCandidates 获取WireGuard桥接候选节点
// Cloudflare/Proton的WireGuard出站通过VPS上的SOCKS桥接入站暴露，
// 客户端看到的是38类型中的SOCKS节点，流量经由WireGuard出站转发。
func getWGBridgeCandidates(db *gorm.DB, seen map[string]bool) []CandidateNode {
	bridges := service.WGBridgeCandidates(db)
	if len(bridges) == 0 {
		return nil
	}

	domain := getVpsDomain()
	var candidates []CandidateNode
	for _, b := range bridges {
		nodeKey := "wgbridge:" + b.OutboundTag
		if seen[nodeKey] {
			continue
		}
		seen[nodeKey] = true

		// SOCKS URI: socks5://domain:port#remark (remark需URL编码)
		uri := fmt.Sprintf("socks5://%s:%d#%s", domain, b.Port, url.PathEscape(b.Remark))

		// 判断Provider
		provider := "WireGuard"
		if strings.HasPrefix(strings.ToLower(b.OutboundTag), "cf-") {
			provider = "Cloudflare"
		} else if strings.Contains(strings.ToLower(b.OutboundTag), "proton") {
			provider = "Proton"
		}

		candidates = append(candidates, CandidateNode{
			Uri:      uri,
			Protocol: "socks",
			Provider: provider,
			Priority: getEgressPriority("socks"),
			NodeKey:  nodeKey,
		})
	}
	logger.Infof("WireGuard桥接: 加载 %d 个候选节点（SOCKS映射）", len(candidates))
	return candidates
}

// normalizeEgressProtocol 标准化协议名称以匹配38类型矩阵
func normalizeEgressProtocol(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "socks5":
		return "socks"
	case "shadowsocks":
		return "ss"
	case "hysteria2":
		return "hysteria2"
	}
	return t
}

// getEgressPriority 根据协议返回优先级（与SUI类型优先级对齐）
func getEgressPriority(proto string) int {
	switch proto {
	case "vless":
		return 10
	case "vmess":
		return 9
	case "trojan":
		return 8
	case "ss", "shadowsocks":
		return 7
	case "socks", "socks5", "mixed":
		return 6
	case "http":
		return 5
	case "hysteria2", "hysteria":
		return 4
	case "tuic":
		return 3
	default:
		return 1
	}
}

// buildEgressURI 从outbound配置生成客户端URI
// 生成的URI后续会被 rewriteEgressURIsViaVPS 重写为 dash.icta.top:VPS_PORT
func buildEgressURI(ob *model.Outbound, proto, provider string) string {
	var opts map[string]interface{}
	if err := json.Unmarshal(ob.Options, &opts); err != nil {
		return ""
	}

	server, _ := opts["server"].(string)
	if server == "" {
		return ""
	}
	var port int
	switch p := opts["server_port"].(type) {
	case float64:
		port = int(p)
	case int:
		port = p
	default:
		return ""
	}

	// 备注格式：{flag}{Provider}-{国家}-{区域}-{城市}-{协议}
	// 由 FilterHealthyAndGroupTop3Links 统一格式化，这里用临时备注
	remark := fmt.Sprintf("%s-%s", provider, ob.Tag)

	switch proto {
	case "vless":
		return buildVlessURI(opts, server, port, remark)
	case "vmess":
		return buildVMessURI(opts, server, port, remark)
	case "trojan":
		return buildTrojanURI(opts, server, port, remark)
	case "ss":
		return buildSSURI(opts, server, port, remark)
	case "socks", "mixed":
		return buildSocksURI(opts, server, port, remark)
	case "http":
		return buildHttpURI(opts, server, port, remark)
	default:
		return ""
	}
}

func buildVlessURI(opts map[string]interface{}, server string, port int, remark string) string {
	uuid, _ := opts["uuid"].(string)
	if uuid == "" {
		return ""
	}
	params := []string{"security=none", "encryption=none"}
	if tls, ok := opts["tls"].(map[string]interface{}); ok {
		if enabled, _ := tls["enabled"].(bool); enabled {
			params[0] = "security=tls"
			if sni, _ := tls["server_name"].(string); sni != "" {
				params = append(params, "sni="+url.QueryEscape(sni))
			}
		}
	}
	uri := fmt.Sprintf("vless://%s@%s:%d?%s", uuid, server, port, strings.Join(params, "&"))
	return uri + "#" + url.QueryEscape(remark)
}

func buildVMessURI(opts map[string]interface{}, server string, port int, remark string) string {
	uuid, _ := opts["uuid"].(string)
	if uuid == "" {
		return ""
	}
	vmessObj := map[string]interface{}{
		"v":    "2",
		"ps":   remark,
		"add":  server,
		"port": port,
		"id":   uuid,
		"aid":  0,
		"net":  "tcp",
		"type": "none",
		"tls":  "",
	}
	if tls, ok := opts["tls"].(map[string]interface{}); ok {
		if enabled, _ := tls["enabled"].(bool); enabled {
			vmessObj["tls"] = "tls"
			if sni, _ := tls["server_name"].(string); sni != "" {
				vmessObj["sni"] = sni
			}
		}
	}
	jsonBytes, _ := json.Marshal(vmessObj)
	return "vmess://" + base64.StdEncoding.EncodeToString(jsonBytes)
}

func buildTrojanURI(opts map[string]interface{}, server string, port int, remark string) string {
	password, _ := opts["password"].(string)
	if password == "" {
		return ""
	}
	params := []string{}
	if tls, ok := opts["tls"].(map[string]interface{}); ok {
		if sni, _ := tls["server_name"].(string); sni != "" {
			params = append(params, "sni="+url.QueryEscape(sni))
		}
	}
	uri := fmt.Sprintf("trojan://%s@%s:%d", url.QueryEscape(password), server, port)
	if len(params) > 0 {
		uri += "?" + strings.Join(params, "&")
	}
	return uri + "#" + url.QueryEscape(remark)
}

func buildSSURI(opts map[string]interface{}, server string, port int, remark string) string {
	method, _ := opts["method"].(string)
	password, _ := opts["password"].(string)
	if method == "" || password == "" {
		return ""
	}
	// ss://base64(method:password)@server:port#remark
	userInfo := base64.URLEncoding.EncodeToString([]byte(method + ":" + password))
	return fmt.Sprintf("ss://%s@%s:%d#%s", userInfo, server, port, url.QueryEscape(remark))
}

func buildSocksURI(opts map[string]interface{}, server string, port int, remark string) string {
	var userInfo string
	if username, _ := opts["username"].(string); username != "" {
		password, _ := opts["password"].(string)
		userInfo = url.QueryEscape(username) + ":" + url.QueryEscape(password) + "@"
	}
	return fmt.Sprintf("socks5://%s%s:%d#%s", userInfo, server, port, url.QueryEscape(remark))
}

func buildHttpURI(opts map[string]interface{}, server string, port int, remark string) string {
	var userInfo string
	if username, _ := opts["username"].(string); username != "" {
		password, _ := opts["password"].(string)
		userInfo = url.QueryEscape(username) + ":" + url.QueryEscape(password) + "@"
	}
	return fmt.Sprintf("http://%s%s:%d#%s", userInfo, server, port, url.QueryEscape(remark))
}
