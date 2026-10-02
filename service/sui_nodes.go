package service

// SUI 38节点固化定义
//
// 这38个 inbound（端口 54142-54179）是 SUI 订阅的核心节点。
// 本文件将它们的创建方法固化为代码，满足：
//  1. 幂等：已存在的 inbound（按 tag 匹配）不会被覆盖，可重复执行
//  2. 可重现：全新安装/重装系统后，调用 EnsureSUINodes 即可重建全部38个
//  3. 与管理界面联动：新建用户时可一键勾选这38个节点
//
// 端口分配表：
//  54142-54145  VLESS+TLS  (tcp/ws/grpc/httpupgrade)
//  54146-54149  VMess+TLS  (tcp/ws/grpc/httpupgrade)
//  54150-54152  Trojan+TLS (tcp/ws/grpc)
//  54153        Hysteria2+TLS
//  54154        TUIC+TLS
//  54155        VMess+TLS (tcp)
//  54156        SOCKS(mixed, 无TLS)
//  54157-54160  VLESS plain (tcp/ws/grpc/httpupgrade, 无TLS)
//  54161-54164  VLESS+TLS  (tcp/ws/grpc/httpupgrade) [原REALITY，已迁移为标准TLS]
//  54165-54168  VMess plain (tcp/ws/grpc/httpupgrade, 无TLS)
//  54169        Trojan+TLS (httpupgrade)
//  54170        Hysteria2+TLS
//  54171        TUIC+TLS
//  54172-54173  Shadowsocks (aes-256-gcm / aes-128-gcm)
//  54174-54175  VLESS+TLS  (tcp/vision, ws/vision)
//  54176        VMess plain (tcp)
//  54177        Trojan plain (tcp)
//  54178        Hysteria2+TLS
//  54179        TUIC+TLS

import (
	"encoding/json"
	"fmt"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"gorm.io/gorm"
)

const (
	// SUIPortMin / SUIPortMax 定义 SUI 节点端口范围
	SUIPortMin = 54142
	SUIPortMax = 54179

	// SUICertPath / SUICertKeyPath TLS 证书路径（与现有部署一致）
	SUICertPath    = "/usr/local/s-ui/certs/fullchain.pem"
	SUICertKeyPath = "/usr/local/s-ui/certs/privkey.pem"
	// SUIServerName TLS SNI
	SUIServerName = "dash.icta.top"
	// SUISSPassword Shadowsocks 密码（与现有部署一致，用户数据：icta-ss-aaad-2026）
	SUISSPassword = "icta-ss-aaad-2026"
	// SUITrojanPasswordBase Trojan 密码前缀（每端口唯一后缀，用户选择方案1）
	SUITrojanPasswordBase = "icta-trojan-2026"
)

// SUINodeSpec 描述一个 SUI inbound 的创建规格
type SUINodeSpec struct {
	Port      int
	Type      string // sing-box inbound type: vless/vmess/trojan/hysteria2/tuic/shadowsocks/mixed
	Tag       string
	TLS       bool
	Transport string // "", "ws", "grpc", "httpupgrade"
	// 传输层细节
	WSPath     string
	GRPCName   string
	HUPath     string
	SSMethod   string
	SSPassword string
}

// tlsOptions 返回标准 TLS 配置
func suiTLSOptions() map[string]interface{} {
	return map[string]interface{}{
		"enabled":          true,
		"server_name":      SUIServerName,
		"alpn":             []string{"h2", "http/1.1"},
		"certificate_path": SUICertPath,
		"key_path":         SUICertKeyPath,
	}
}

// tlsOptionsNoALPN 返回无 ALPN 的 TLS 配置（与现有部分节点一致）
func suiTLSOptionsNoALPN() map[string]interface{} {
	return map[string]interface{}{
		"enabled":          true,
		"server_name":      SUIServerName,
		"certificate_path": SUICertPath,
		"key_path":         SUICertKeyPath,
	}
}

// transportOptions 返回传输层配置，transport 为 "" 时返回 nil
func (s SUINodeSpec) transportOptions() map[string]interface{} {
	switch s.Transport {
	case "ws":
		path := s.WSPath
		if path == "" {
			path = "/ws"
		}
		return map[string]interface{}{
			"type":                  "ws",
			"path":                  path,
			"max_early_data":        2048,
			"early_data_header_name": "Sec-WebSocket-Protocol",
		}
	case "grpc":
		name := s.GRPCName
		if name == "" {
			name = "vgrpc"
		}
		return map[string]interface{}{
			"type":         "grpc",
			"service_name": name,
		}
	case "httpupgrade":
		path := s.HUPath
		if path == "" {
			path = "/vhu"
		}
		return map[string]interface{}{
			"type": "httpupgrade",
			"path": path,
			"host": SUIServerName,
		}
	}
	return nil
}

// BuildOptions 构建 sing-box inbound options
func (s SUINodeSpec) BuildOptions() map[string]interface{} {
	opts := map[string]interface{}{
		"type":                     s.Type,
		"tag":                      s.Tag,
		"listen":                   "::",
		"listen_port":              s.Port,
		"tcp_fast_open":            true,
		"sniff":                    true,
		"sniff_override_destination": false,
	}
	if s.TLS {
		// 54142-54149 使用带 ALPN 的配置，其余使用无 ALPN 配置（与现有部署一致）
		if s.Port >= 54142 && s.Port <= 54149 {
			opts["tls"] = suiTLSOptions()
		} else {
			opts["tls"] = suiTLSOptionsNoALPN()
		}
		// 54142 的 sniff_override_destination 为 true（与现有部署一致）
		if s.Port == 54142 {
			opts["sniff_override_destination"] = true
		}
	}
	if t := s.transportOptions(); t != nil {
		opts["transport"] = t
	}
	if s.Type == "shadowsocks" {
		opts["method"] = s.SSMethod
		opts["password"] = s.SSPassword
		if opts["password"] == "" {
			opts["password"] = SUISSPassword
		}
	}
	if s.Type == "tuic" {
		opts["congestion_control"] = "bbr"
	}
	if s.Type == "trojan" {
		// Trojan 每端口唯一密码（用户选择方案1，避免 sing-box "user already exists"）
		opts["users"] = []map[string]interface{}{
			{"password": fmt.Sprintf("%s-%d", SUITrojanPasswordBase, s.Port)},
		}
	}
	return opts
}

// GetSUINodeSpecs 返回38个 SUI 节点的完整规格（按端口排序）
func GetSUINodeSpecs() []SUINodeSpec {
	return []SUINodeSpec{
		{Port: 54142, Type: "vless", Tag: "vless-54142", TLS: true},
		{Port: 54143, Type: "vless", Tag: "vless-ws-54143", TLS: true, Transport: "ws", WSPath: "/ws"},
		{Port: 54144, Type: "vless", Tag: "vless-grpc-54144", TLS: true, Transport: "grpc", GRPCName: "vgrpc"},
		{Port: 54145, Type: "vless", Tag: "vless-httpupgrade-54145", TLS: true, Transport: "httpupgrade", HUPath: "/vhu"},
		{Port: 54146, Type: "vmess", Tag: "vmess-tcp-54146", TLS: true},
		{Port: 54147, Type: "vmess", Tag: "vmess-ws-54147", TLS: true, Transport: "ws", WSPath: "/ws"},
		{Port: 54148, Type: "vmess", Tag: "vmess-grpc-54148", TLS: true, Transport: "grpc", GRPCName: "vgrpc"},
		{Port: 54149, Type: "vmess", Tag: "vmess-httpupgrade-54149", TLS: true, Transport: "httpupgrade", HUPath: "/vhu"},
		{Port: 54150, Type: "trojan", Tag: "trojan-tcp-54150", TLS: true},
		{Port: 54151, Type: "trojan", Tag: "trojan-ws-54151", TLS: true, Transport: "ws", WSPath: "/trws"},
		{Port: 54152, Type: "trojan", Tag: "trojan-grpc-54152", TLS: true, Transport: "grpc", GRPCName: "trgrpc"},
		{Port: 54153, Type: "hysteria2", Tag: "hysteria2-54153", TLS: true},
		{Port: 54154, Type: "tuic", Tag: "tuic-54154", TLS: true},
		{Port: 54155, Type: "vmess", Tag: "vmess-tcp-54155", TLS: true},
		{Port: 54156, Type: "mixed", Tag: "mixed-54156"},
		{Port: 54157, Type: "vless", Tag: "vless-tcp-plain-54157"},
		{Port: 54158, Type: "vless", Tag: "vless-ws-plain-54158", Transport: "ws", WSPath: "/ws"},
		{Port: 54159, Type: "vless", Tag: "vless-grpc-plain-54159", Transport: "grpc", GRPCName: "vgrpc"},
		{Port: 54160, Type: "vless", Tag: "vless-httpupgrade-plain-54160", Transport: "httpupgrade", HUPath: "/vhu"},
		{Port: 54161, Type: "vless", Tag: "vless-tcp-reality-54161", TLS: true},
		{Port: 54162, Type: "vless", Tag: "vless-ws-reality-54162", TLS: true, Transport: "ws", WSPath: "/ws"},
		{Port: 54163, Type: "vless", Tag: "vless-grpc-reality-54163", TLS: true, Transport: "grpc", GRPCName: "vgrpc"},
		{Port: 54164, Type: "vless", Tag: "vless-httpupgrade-reality-54164", TLS: true, Transport: "httpupgrade", HUPath: "/vhu"},
		{Port: 54165, Type: "vmess", Tag: "vmess-tcp-plain-54165"},
		{Port: 54166, Type: "vmess", Tag: "vmess-ws-plain-54166", Transport: "ws", WSPath: "/vmws"},
		{Port: 54167, Type: "vmess", Tag: "vmess-grpc-plain-54167", Transport: "grpc", GRPCName: "vmgrpc"},
		{Port: 54168, Type: "vmess", Tag: "vmess-httpupgrade-plain-54168", Transport: "httpupgrade", HUPath: "/vmhu"},
		{Port: 54169, Type: "trojan", Tag: "trojan-httpupgrade-54169", TLS: true, Transport: "httpupgrade", HUPath: "/trhu"},
		{Port: 54170, Type: "hysteria2", Tag: "hysteria2-2-54170", TLS: true},
		{Port: 54171, Type: "tuic", Tag: "tuic-2-54171", TLS: true},
		{Port: 54172, Type: "shadowsocks", Tag: "ss-aes-256-gcm-54172", SSMethod: "aes-256-gcm", SSPassword: SUISSPassword},
		{Port: 54173, Type: "shadowsocks", Tag: "ss-aes-128-gcm-54173", SSMethod: "aes-128-gcm", SSPassword: SUISSPassword},
		{Port: 54174, Type: "vless", Tag: "vless-tcp-vision-54174", TLS: true},
		{Port: 54175, Type: "vless", Tag: "vless-ws-vision-54175", TLS: true, Transport: "ws", WSPath: "/wsv"},
		{Port: 54176, Type: "vmess", Tag: "vmess-tcp-plain-2-54176"},
		{Port: 54177, Type: "trojan", Tag: "trojan-tcp-plain-54177"},
		{Port: 54178, Type: "hysteria2", Tag: "hysteria2-3-54178", TLS: true},
		{Port: 54179, Type: "tuic", Tag: "tuic-3-54179", TLS: true},
	}
}

// EnsureSUINodes 幂等创建38个 SUI inbound。
// 已存在的（按 tag 匹配）会被跳过，不会被覆盖；返回新建数量。
func EnsureSUINodes(db *gorm.DB) (created int, err error) {
	if db == nil {
		db = database.GetDB()
	}
	specs := GetSUINodeSpecs()
	for _, spec := range specs {
		var existing model.Inbound
		if err := db.Where("tag = ?", spec.Tag).First(&existing).Error; err == nil {
			continue // 已存在，跳过
		}
		opts := spec.BuildOptions()
		optsJSON, err := json.MarshalIndent(opts, "", "  ")
		if err != nil {
			return created, fmt.Errorf("marshal options for %s: %w", spec.Tag, err)
		}
		inbound := model.Inbound{
			Type:    spec.Type,
			Tag:     spec.Tag,
			Options: optsJSON,
			Addrs:   json.RawMessage(`[]`),
			OutJson: json.RawMessage(`{}`),
		}
		if err := db.Create(&inbound).Error; err != nil {
			return created, fmt.Errorf("create inbound %s: %w", spec.Tag, err)
		}
		created++
		logger.Info(fmt.Sprintf("EnsureSUINodes: created %s (port %d)", spec.Tag, spec.Port))
	}
	return created, nil
}

// GetSUIInboundIDs 返回38个 SUI inbound 的数据库 ID（按端口排序）。
// 用于新建用户时一键关联这38个节点。
func GetSUIInboundIDs(db *gorm.DB) ([]uint, error) {
	if db == nil {
		db = database.GetDB()
	}
	specs := GetSUINodeSpecs()
	tags := make([]string, 0, len(specs))
	for _, s := range specs {
		tags = append(tags, s.Tag)
	}
	var inbounds []model.Inbound
	if err := db.Where("tag IN ?", tags).Find(&inbounds).Error; err != nil {
		return nil, err
	}
	byTag := make(map[string]uint, len(inbounds))
	for _, ib := range inbounds {
		byTag[ib.Tag] = ib.Id
	}
	ids := make([]uint, 0, len(specs))
	var missing []string
	for _, s := range specs {
		id, ok := byTag[s.Tag]
		if !ok {
			missing = append(missing, s.Tag)
			continue
		}
		ids = append(ids, id)
	}
	if len(missing) > 0 {
		return ids, fmt.Errorf("missing SUI inbounds: %v (run EnsureSUINodes first)", missing)
	}
	return ids, nil
}

// VerifySUINodes 校验38个 SUI inbound 是否全部存在，返回缺失的 tag 列表
func VerifySUINodes(db *gorm.DB) []string {
	if db == nil {
		db = database.GetDB()
	}
	var missing []string
	for _, spec := range GetSUINodeSpecs() {
		var count int64
		db.Model(&model.Inbound{}).Where("tag = ?", spec.Tag).Count(&count)
		if count == 0 {
			missing = append(missing, spec.Tag)
		}
	}
	return missing
}
