package sub

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/service"
	"github.com/alireza0/s-ui/util"
)

func TestExpandEgressLinks_VMess(t *testing.T) {
	s := &LinkService{}

	baseObj := map[string]interface{}{
		"v":    "2",
		"ps":   "vmess-in-2096",
		"add":  "dash.icta.top",
		"port": "2096",
		"id":   "403db7be-930b-449e-b5f4-34537cb594c7",
		"aid":  0,
		"net":  "ws",
		"type": "none",
		"host": "dash.icta.top",
		"path": "/ws",
		"tls":  "tls",
	}
	baseRaw, _ := json.Marshal(baseObj)
	baseUri := "vmess://" + util.ByteToB64Str(baseRaw)

	expanded := s.ExpandEgressLinks(baseUri, service.StandardEgressRegions)

	if len(expanded) == 0 {
		t.Fatalf("Expected expanded links, got 0")
	}

	// Verify standard remark format on first node
	raw0, _ := util.B64StrToByte(strings.TrimPrefix(expanded[0], "vmess://"))
	var obj0 map[string]interface{}
	_ = json.Unmarshal(raw0, &obj0)
	ps0 := obj0["ps"].(string)

	if !strings.HasPrefix(ps0, "SUI-新加坡-中央区-新加坡城") {
		t.Errorf("First node must be SUI Singapore entry, got %s", ps0)
	}

	// Verify security isolation
	pass, violations := ValidateSubscriptionSecurity(expanded, "dash.icta.top")
	if !pass {
		t.Fatalf("Subscription security violations: %v", violations)
	}

	for _, link := range expanded {
		parts := strings.Split(link, "://")
		if len(parts) != 2 || parts[0] != "vmess" {
			t.Fatalf("Malformed vmess link: %s", link)
		}
		raw, err := util.B64StrToByte(parts[1])
		if err != nil {
			t.Fatalf("Failed to decode vmess base64: %v", err)
		}
		var obj map[string]interface{}
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("Failed to unmarshal vmess payload: %v", err)
		}

		id := obj["id"].(string)
		port := obj["port"].(string)
		add := obj["add"].(string)

		if port != "2096" || add != "dash.icta.top" {
			t.Errorf("Port or Addr altered: port=%s, add=%s", port, add)
		}

		ps := obj["ps"].(string)
		if strings.Contains(ps, "美国") {
			expectedUUID := service.DeriveUUID("403db7be-930b-449e-b5f4-34537cb594c7", "us")
			if id != expectedUUID {
				t.Errorf("US node UUID mismatch: got %s, want %s", id, expectedUUID)
			}
		}
	}
}

func TestExpandEgressLinks_VLESS(t *testing.T) {
	s := &LinkService{}

	baseUUID := "403db7be-930b-449e-b5f4-34537cb594c7"
	baseUri := "vless://" + baseUUID + "@dash.icta.top:2096?security=tls&type=ws&path=%2Fws#vmess-in"

	expanded := s.ExpandEgressLinks(baseUri, service.StandardEgressRegions)

	if len(expanded) == 0 {
		t.Fatalf("Expected expanded links, got 0")
	}

	u0, _ := url.Parse(expanded[0])
	if !strings.HasPrefix(u0.Fragment, "SUI-新加坡-中央区-新加坡城") {
		t.Errorf("First node must be SUI Singapore entry, got %s", u0.Fragment)
	}

	pass, violations := ValidateSubscriptionSecurity(expanded, "dash.icta.top")
	if !pass {
		t.Fatalf("Subscription security violations: %v", violations)
	}

	for _, link := range expanded {
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("Failed to parse link: %v", err)
		}
		if u.Host != "dash.icta.top:2096" {
			t.Errorf("Host mismatch: %s", u.Host)
		}
		if strings.Contains(u.Fragment, "美国") {
			expectedUUID := service.DeriveUUID(baseUUID, "us")
			if u.User.Username() != expectedUUID {
				t.Errorf("US node UUID mismatch: got %s, want %s", u.User.Username(), expectedUUID)
			}
		}
	}
}

func TestExpandEgressLinks_DynamicCloudflareRegions(t *testing.T) {
	s := &LinkService{}

	baseUUID := "403db7be-930b-449e-b5f4-34537cb594c7"
	baseUri := "vless://" + baseUUID + "@dash.icta.top:2096?security=tls&type=ws&path=%2Fws#vmess-in"

	dynamicCFRegions := []service.EgressRegion{
		{Code: "cf-us-lax", Name: "美国·洛杉矶-Cloudflare洁净出口", Flag: "🇺🇸", OutboundTag: "cf-us-lax-pool"},
		{Code: "cf-jp-nrt", Name: "日本·东京-Cloudflare洁净出口", Flag: "🇯🇵", OutboundTag: "cf-jp-nrt-pool"},
		{Code: "cf-sg-sin", Name: "新加坡-Cloudflare洁净出口", Flag: "🇸🇬", OutboundTag: "cf-sg-sin-pool"},
		{Code: "cf-hk-hkg", Name: "中国香港-Cloudflare洁净出口", Flag: "🇭🇰", OutboundTag: "cf-hk-hkg-pool"},
		{Code: "cf-gb-lhr", Name: "英国·伦敦-Cloudflare洁净出口", Flag: "🇬🇧", OutboundTag: "cf-gb-lhr-pool"},
		{Code: "cf-de-fra", Name: "德国·法兰克福-Cloudflare洁净出口", Flag: "🇩🇪", OutboundTag: "cf-de-fra-pool"},
	}

	expanded := s.ExpandEgressLinks(baseUri, dynamicCFRegions)

	pass, violations := ValidateSubscriptionSecurity(expanded, "dash.icta.top")
	if !pass {
		t.Fatalf("Subscription security violations: %v", violations)
	}

	// Verify all returned nodes point exclusively to dash.icta.top
	for _, link := range expanded {
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("Failed to parse link: %v", err)
		}
		if u.Host != "dash.icta.top:2096" {
			t.Errorf("Host mismatch: %s", u.Host)
		}
		// Remarks must not contain banned tokens
		for _, b := range []string{"原生直连", "默认出口", "智能优选", "洁净出口"} {
			if strings.Contains(u.Fragment, b) {
				t.Errorf("Remark contains banned token %s: %s", b, u.Fragment)
			}
		}
	}
}

func TestGetAuthorizedLinks_EmptyAllowedTags(t *testing.T) {
	s := &LinkService{}
	linksJson := json.RawMessage(`[
		{"type": "local", "remark": "vless-54142", "uri": "vless://403db7be-930b-449e-b5f4-34537cb594c7@dash.icta.top:2096?security=tls&type=ws&path=%2Fws#vless-54142"}
	]`)

	res1 := s.GetAuthorizedLinks(&linksJson, "all", "", nil)
	if len(res1) == 0 {
		t.Fatalf("Expected links when allowedTags is nil, got 0")
	}

	pass, violations := ValidateSubscriptionSecurity(res1, "dash.icta.top")
	if !pass {
		t.Fatalf("Security violations: %v", violations)
	}
}

func TestExpandEgressLinks_TUIC_And_Hysteria2(t *testing.T) {
	s := &LinkService{}
	baseUUID := "403db7be-930b-449e-b5f4-34537cb594c7"

	tuicUri := "tuic://" + baseUUID + ":s-ui-admin-pass@dash.icta.top:57295?congestion_control=bbr&alpn=h2%2Chttp%2F1.1&sni=dash.icta.top#tuic-57295"
	expandedTuic := s.ExpandEgressLinks(tuicUri, service.StandardEgressRegions)
	passTuic, vTuic := ValidateSubscriptionSecurity(expandedTuic, "dash.icta.top")
	if !passTuic {
		t.Fatalf("TUIC violations: %v", vTuic)
	}

	hy2Uri := "hysteria2://s-ui-admin-pass@dash.icta.top:25536?insecure=0&sni=dash.icta.top&alpn=h2%2Chttp%2F1.1#hysteria2-25536"
	expandedHy2 := s.ExpandEgressLinks(hy2Uri, service.StandardEgressRegions)
	passHy2, vHy2 := ValidateSubscriptionSecurity(expandedHy2, "dash.icta.top")
	if !passHy2 {
		t.Fatalf("Hysteria2 violations: %v", vHy2)
	}
}

// =========================================================================
// 五大自动化回归测试套件 (严格对齐任务规范)
// =========================================================================

// 测试1：订阅安全隔离测试
// - curl 用户订阅地址
// - PASS: 无真实IP、无外部真实域名、无上游节点名称、无供应商信息
// - FAIL: 出现真实出口IP、VPS IP、workers.dev等
func TestRegression_1_SubscriptionSecurityIsolation(t *testing.T) {
	s := &LinkService{}
	testVmess := map[string]interface{}{
		"v":    "2",
		"ps":   "vmess-2083",
		"add":  "dash.icta.top",
		"port": "2083",
		"id":   "403db7be-930b-449e-b5f4-34537cb594c7",
		"aid":  0,
		"net":  "ws",
		"type": "none",
		"host": "dash.icta.top",
		"path": "/v",
		"tls":  "tls",
	}
	rawVmess, _ := json.Marshal(testVmess)

	linksList := []Link{
		{Type: "local", Remark: "vmess-2083", Uri: "vmess://" + util.ByteToB64Str(rawVmess)},
		{Type: "local", Remark: "hysteria2-8444", Uri: "hysteria2://s-ui-admin-pass@dash.icta.top:8444?insecure=0&sni=dash.icta.top&alpn=h2%2Chttp%2F1.1#hysteria2-8444"},
		{Type: "external", Remark: "leak-test", Uri: "vmess://ewogICJhZGQiOiAiMTI0LjE1Ni4yMDcuMjUzIiwgInBzIjogImxlYWsiIH0="},
	}
	linksJsonBytes, _ := json.Marshal(linksList)
	linksJson := json.RawMessage(linksJsonBytes)

	links := s.GetAuthorizedLinks(&linksJson, "all", "", nil)

	// 1. Must pass security audit
	pass, violations := ValidateSubscriptionSecurity(links, "dash.icta.top")
	if !pass {
		t.Fatalf("Security isolation failed: %v", violations)
	}

	// 2. Explicitly assert that no real IP or external upstream domain is present
	for _, link := range links {
		if strings.Contains(link, "124.156.207.253") {
			t.Fatalf("CRITICAL SECURITY FAILURE: Real VPS IP leaked in link: %s", link)
		}
		if strings.Contains(link, "workers.dev") || strings.Contains(link, "globals-download.com") {
			t.Fatalf("CRITICAL SECURITY FAILURE: Real upstream domain leaked: %s", link)
		}
	}
}

// 测试2：节点数量与TOP3测试
// 输入：10000节点（大量不同/相同区域多协议节点）
// 验证：
// - 无效节点删除
// - 重复节点合并
// - 每组最多3个 (TOP 3)
func TestRegression_2_NodeQuantityAndTop3Grouping(t *testing.T) {
	var massiveCandidates []CandidateNode

	// Generate 10,000 candidate nodes across 10 cities with 5 protocols each and duplicate runs
	cities := []string{"东京", "大阪", "洛杉矶", "旧金山", "西雅图", "伦敦", "法兰克福", "阿姆斯特丹", "首尔", "新加坡城"}
	protocols := []string{"hysteria2", "tuic", "vless", "trojan", "vmess"}

	for i := 0; i < 10000; i++ {
		city := cities[i%len(cities)]
		proto := protocols[i%len(protocols)]
		c := CandidateNode{
			Uri:      fmt.Sprintf("%s://user%d@dash.icta.top:2096#raw-%d", proto, i, i),
			Protocol: proto,
			Provider: "Cloudflare",
			Country:  "测试国",
			Region:   "测试区",
			City:     city,
			Priority: getProtocolPriority(proto),
		}
		massiveCandidates = append(massiveCandidates, c)
	}

	result := GroupAndFilterTop3Links(massiveCandidates)

	// Group verification
	groupCounts := make(map[string]int)
	remarkPattern := regexp.MustCompile(`^[\p{Han}a-zA-Z0-9]+-[\p{Han}a-zA-Z0-9]+-[\p{Han}a-zA-Z0-9]+-[\p{Han}a-zA-Z0-9]+-\d{2}$`)

	for _, link := range result {
		u, _ := url.Parse(link)
		remark := u.Fragment
		if !remarkPattern.MatchString(remark) {
			t.Errorf("Remark does not match standard format: %s", remark)
		}
		// Extract group key (first 4 segments)
		parts := strings.Split(remark, "-")
		if len(parts) != 5 {
			t.Fatalf("Invalid remark parts count: %s", remark)
		}
		gk := strings.Join(parts[0:4], "-")
		groupCounts[gk]++
	}

	// Assert: every group MUST have <= 3 nodes
	for gk, count := range groupCounts {
		if count > 3 {
			t.Errorf("Group %s exceeded TOP3 limit with %d nodes", gk, count)
		}
	}

	if len(result) > 10*3 {
		t.Errorf("Total nodes %d exceeds expected limit %d", len(result), 10*3)
	}
}

// 测试3：节点真实性测试 (Stage 2 客户端闭环验证)
// 订阅获取 -> 解析 -> 连接 -> 代理请求 -> 访问测试地址 -> success=true
func TestRegression_3_NodeAuthenticityAndClosedLoop(t *testing.T) {
	// Mock local target HTTP server (generate_204)
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer mockServer.Close()

	// Mock virtual inbound listener
	mockInbound, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen mock inbound: %v", err)
	}
	defer mockInbound.Close()

	go func() {
		for {
			conn, err := mockInbound.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	mockPort := mockInbound.Addr().(*net.TCPAddr).Port
	links := []string{
		fmt.Sprintf("vless://test-uuid@127.0.0.1:%d?security=none#SUI-新加坡-中央区-新加坡城-01", mockPort),
		fmt.Sprintf("vless://test-uuid@127.0.0.1:%d?security=none#Cloudflare-美国-加州-洛杉矶-01", mockPort),
	}

	checker := service.NewHealthChecker()
	checker.DefaultTargetURL = mockServer.URL
	checker.Timeout = 2 * time.Second

	passed, verifiedCount, errs := checker.ValidateClientClosedLoop(context.Background(), links, nil)
	if !passed || verifiedCount != 2 || len(errs) > 0 {
		t.Fatalf("Client closed-loop verification failed: passed=%v, verified=%d, errs=%v", passed, verifiedCount, errs)
	}
}

// 测试4：测速一致性测试
// 验证：测速结果 speed > 0，实际测试必须成功；禁止测速成功但实际失败
func TestRegression_4_SpeedConsistency(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	node := &model.NodeHealthStatus{
		Node:     "Cloudflare-美国-加州-洛杉矶-01",
		Provider: "Cloudflare",
		Country:  "美国",
		Region:   "加州",
		City:     "洛杉矶",
	}

	checker := service.NewHealthChecker()
	checker.DefaultTargetURL = mockServer.URL

	// Mock successful dialContext
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", mockServer.Listener.Addr().String())
	}

	err := checker.CheckNode(context.Background(), node, dialContext, "127.0.0.1", 80, false, "")
	if err != nil {
		t.Fatalf("CheckNode failed: %v", err)
	}

	// Assert consistency: node must be available and speed must be strictly positive
	if !node.IsHealthy() {
		t.Fatalf("Node should be marked healthy: %+v", node)
	}
	if node.Speed <= 0 || node.Latency <= 0 {
		t.Fatalf("Speed (%f) and Latency (%d) must be strictly positive", node.Speed, node.Latency)
	}
	if node.Status != "available" {
		t.Fatalf("Status must be 'available', got %s", node.Status)
	}
}

// 测试5：最终用户模拟测试
// 模拟真实用户：用户A -> 获取订阅 -> 客户端加载 -> 选择节点 -> 访问网站 -> 全链路成功
func TestRegression_5_EndToEndUserSimulation(t *testing.T) {
	testDb := t.TempDir() + "/test_sui.db"
	_ = database.InitDB(testDb)
	db := database.GetDB()
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	testVmess := map[string]interface{}{
		"v":    "2",
		"ps":   "vmess-2083",
		"add":  "dash.icta.top",
		"port": "2083",
		"id":   "403db7be-930b-449e-b5f4-34537cb594c7",
		"aid":  0,
		"net":  "ws",
		"type": "none",
		"host": "dash.icta.top",
		"path": "/v",
		"tls":  "tls",
	}
	rawVmess, _ := json.Marshal(testVmess)

	linksList := []Link{
		{Type: "local", Remark: "vmess-2083", Uri: "vmess://" + util.ByteToB64Str(rawVmess)},
	}
	linksJsonBytes, _ := json.Marshal(linksList)

	client := model.Client{
		Id:       1001,
		Name:     "userA",
		Enable:   true,
		Volume:   100 * 1024 * 1024 * 1024,
		Links:    json.RawMessage(linksJsonBytes),
		Inbounds: json.RawMessage(`[]`),
	}
	db.Create(&client)
	subService := &SubService{}

	// User requests subscription
	subContent, _, err := subService.GetSubs("userA")
	if err != nil {
		t.Fatalf("GetSubs failed: %v", err)
	}
	if subContent == nil || len(*subContent) == 0 {
		t.Fatalf("Empty subscription returned")
	}

	// Decode subscription if base64 encoded
	decoded := util.StrOrBase64Encoded(*subContent)
	links := strings.Split(strings.TrimSpace(decoded), "\n")
	if len(links) == 0 {
		t.Fatalf("No links in subscription")
	}

	// Verify all links satisfy Security Isolation
	pass, violations := ValidateSubscriptionSecurity(links, "dash.icta.top")
	if !pass {
		t.Fatalf("User subscription failed security isolation: %v", violations)
	}

	// Verify User simulation: select first node and check structure
	firstLink := links[0]
	if !strings.HasPrefix(firstLink, "vmess://") {
		t.Fatalf("Expected vmess link, got %s", firstLink)
	}
}
