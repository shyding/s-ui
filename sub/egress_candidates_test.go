package sub

import (
	"strings"
	"testing"
)

// TestSUITTypeProtocolMatrix 验证42类型协议矩阵包含预期的协议
func TestSUITTypeProtocolMatrix(t *testing.T) {
	// 38 SUI节点覆盖的协议类型都应在矩阵中
	required := []string{"vless", "vmess", "trojan", "hysteria2", "tuic", "ss", "shadowsocks", "socks", "socks5", "mixed"}
	for _, p := range required {
		if !suiTypeProtocolMatrix[p] {
			t.Errorf("协议 %s 应在42类型矩阵中", p)
		}
	}
	// WireGuard不应在矩阵中（无法映射为订阅URI）
	if suiTypeProtocolMatrix["wireguard"] {
		t.Error("wireguard 不应在42类型矩阵中")
	}
}

// TestNormalizeEgressProtocol 验证协议名称标准化
func TestNormalizeEgressProtocol(t *testing.T) {
	cases := []struct{ in, want string }{
		{"vless", "vless"},
		{"VLESS", "vless"},
		{"socks5", "socks"},
		{"shadowsocks", "ss"},
		{"wireguard", "wireguard"}, // 不在矩阵中，保持原样以便过滤
	}
	for _, c := range cases {
		if got := normalizeEgressProtocol(c.in); got != c.want {
			t.Errorf("normalizeEgressProtocol(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBuildVlessURI 验证VLESS URI生成
func TestBuildVlessURI(t *testing.T) {
	opts := map[string]interface{}{
		"uuid":        "test-uuid-1234",
		"server_port": float64(443),
		"tls": map[string]interface{}{
			"enabled":     true,
			"server_name": "example.com",
		},
	}
	uri := buildVlessURI(opts, "1.2.3.4", 443, "TestRemark")
	if !strings.HasPrefix(uri, "vless://test-uuid-1234@1.2.3.4:443?") {
		t.Errorf("VLESS URI格式错误: %s", uri)
	}
	if !strings.Contains(uri, "security=tls") {
		t.Errorf("VLESS URI应包含security=tls: %s", uri)
	}
}

// TestBuildSocksURI 验证SOCKS URI生成（HProxy场景）
func TestBuildSocksURI(t *testing.T) {
	opts := map[string]interface{}{}
	uri := buildSocksURI(opts, "5.6.7.8", 1080, "HProxy-Test")
	if !strings.HasPrefix(uri, "socks5://5.6.7.8:1080#") {
		t.Errorf("SOCKS URI格式错误: %s", uri)
	}
}

// TestBuildSSURI 验证Shadowsocks URI生成
func TestBuildSSURI(t *testing.T) {
	opts := map[string]interface{}{
		"method":   "aes-256-gcm",
		"password": "testpass",
	}
	uri := buildSSURI(opts, "9.9.9.9", 8388, "SS-Test")
	if !strings.HasPrefix(uri, "ss://") {
		t.Errorf("SS URI格式错误: %s", uri)
	}
	if !strings.Contains(uri, "9.9.9.9:8388") {
		t.Errorf("SS URI应包含服务器地址: %s", uri)
	}
}

// TestEgressSourcesDefined 验证四个来源已定义
func TestEgressSourcesDefined(t *testing.T) {
	providers := make(map[string]bool)
	for _, s := range egressSources {
		providers[s.Provider] = true
	}
	for _, p := range []string{"HProxy", "Cloudflare", "Proton"} {
		if !providers[p] {
			t.Errorf("来源 %s 未定义", p)
		}
	}
	// Seed通过文件加载，不在egressSources中（已在GetAuthorizedLinks中处理）
}
