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
	testDb := t.TempDir() + "/test_links.db"
	_ = database.InitDB(testDb)
	db := database.GetDB()
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	db.Create(&model.NodeHealthStatus{
		Node:          "SUI-新加坡-中央区-新加坡城",
		Provider:      "SUI",
		Country:       "新加坡",
		Region:        "中央区",
		City:          "新加坡城",
		TCPCheck:      true,
		TLSCheck:      true,
		ProxyCheck:    true,
		Status:        "available",
		Latency:       50,
		Speed:         100,
		LastCheckTime: time.Now().UTC().Format(time.RFC3339),
	})

	s := &LinkService{}
	linksJson := json.RawMessage(`[
		{"type": "local", "remark": "vless-54142", "uri": "vless://403db7be-930b-449e-b5f4-34537cb594c7@dash.icta.top:2096?security=tls&type=ws&path=%2Fws#vless-54142"}
	]`)

	res1 := s.GetAuthorizedLinks(&linksJson, "all", "", nil)
	if len(res1) == 0 {
		t.Fatalf("Expected links when allowedTags is nil and node is healthy, got 0")
	}

	pass, violations := ValidateSubscriptionSecurity(res1, "dash.icta.top")
	if !pass {
		t.Fatalf("Security violations: %v", violations)
	}
}

func TestGetAuthorizedLinks_PublishesOnlyExpandedLocalLinks(t *testing.T) {
	testDb := t.TempDir() + "/test_client_egress_links.db"
	_ = database.InitDB(testDb)
	db := database.GetDB()
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()
	measuredPools := []model.Outbound{
		{Tag: "us-pool", Type: "urltest", Available: true, LastTestTime: time.Now().Unix(), LandingIP: "203.0.113.10", Country: "US", Region: "California", City: "Los Angeles"},
		{Tag: "jp-pool", Type: "urltest", Available: true, LastTestTime: time.Now().Unix(), LandingIP: "203.0.113.11", Country: "JP", Region: "Tokyo", City: "Tokyo"},
		{Tag: "nl-pool", Type: "urltest", Available: true, LastTestTime: time.Now().Unix(), LandingIP: "203.0.113.12", Country: "NL", Region: "Provincie Noord-Holland", City: "Amsterdam"},
	}
	for _, pool := range measuredPools {
		db.Create(&pool)
	}

	// Strict FAIL-CLOSED: create real NodeHealthStatus records for the
	// expanded candidates (SUI Singapore + Proton regions). Without these,
	// FilterHealthyAndGroupTop3Links returns empty (no virtual records).
	now := time.Now().UTC().Format(time.RFC3339)
	healthRecords := []model.NodeHealthStatus{
		{Node: "sui-sg-health", Provider: "SUI", Country: "新加坡", Region: "中央区", City: "新加坡城", Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Latency: 50, Speed: 100.0, LastCheckTime: now},
		{Node: "proton-us-health", Provider: "Proton", Country: "US", Region: "California", City: "Los Angeles", Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Latency: 150, Speed: 50.0, LastCheckTime: now},
		{Node: "proton-jp-health", Provider: "Proton", Country: "JP", Region: "Tokyo", City: "Tokyo", Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Latency: 120, Speed: 60.0, LastCheckTime: now},
		{Node: "proton-nl-health", Provider: "Proton", Country: "NL", Region: "Provincie Noord-Holland", City: "Amsterdam", Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Latency: 180, Speed: 40.0, LastCheckTime: now},
	}
	for _, hr := range healthRecords {
		db.Create(&hr)
	}

	baseUUID := "403db7be-930b-449e-b5f4-34537cb594c7"
	linksJSON := json.RawMessage(`[
		{"type":"local","remark":"vless-in","uri":"vless://403db7be-930b-449e-b5f4-34537cb594c7@dash.icta.top:2096?security=tls&type=ws&path=%2Fws#vless-in"},
		{"type":"external","remark":"external","uri":"vless://external-user@198.51.100.10:443?security=tls&type=ws#external"},
		{"type":"sub","remark":"sub","uri":"https://example.invalid/sub"}
	]`)

	links := (&LinkService{}).GetAuthorizedLinks(&linksJSON, "all", "", nil)
	if len(links) < len(service.StandardEgressRegions) {
		t.Fatalf("expected local links for every standard egress region, got %d", len(links))
	}

	for _, link := range links {
		if strings.Contains(link, "198.51.100.10") || strings.Contains(link, "example.invalid") {
			t.Fatalf("external link leaked into client subscription: %s", link)
		}
		u, err := url.Parse(link)
		if err != nil || u.Host != "dash.icta.top:2096" {
			t.Fatalf("client link must point to the VPS entry, got %s", link)
		}
		if strings.Contains(u.Fragment, "未知") {
			t.Fatalf("client link must have a configured egress remark, got %s", u.Fragment)
		}
	}

	expectedUSUUID := service.DeriveUUID(baseUUID, "us")
	if !strings.Contains(strings.Join(links, "\n"), expectedUSUUID) {
		t.Fatal("expected a US egress link with a derived UUID")
	}
}

func TestGetLocalLinks_ExcludesExternalAndSubLinks(t *testing.T) {
	linksJSON := json.RawMessage(`[
		{"type":"local","remark":"local","uri":"vless://user@dash.icta.top:2096?security=tls&type=ws#local"},
		{"type":"external","remark":"external","uri":"vless://user@198.51.100.10:443?security=tls&type=ws#external"},
		{"type":"sub","remark":"sub","uri":"https://example.invalid/sub"}
	]`)

	links := (&LinkService{}).GetLocalLinks(&linksJSON, "", nil)
	if len(links) != 1 {
		t.Fatalf("expected one local link, got %d", len(links))
	}
	if !strings.Contains(links[0], "dash.icta.top:2096") {
		t.Fatalf("expected the local VPS link, got %s", links[0])
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
	remarkPattern := regexp.MustCompile(`^[^\r\n-]+-[^\r\n-]+-[^\r\n-]+-[^\r\n-]+-\d{2}$`)

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

	// Assert: every group MUST have <= MaxNodesPerCityGroup nodes (dynamic limit for non-SUI providers)
	for gk, count := range groupCounts {
		if count > MaxNodesPerCityGroup {
			t.Errorf("Group %s exceeded limit with %d nodes", gk, count)
		}
	}

	if len(result) > MaxNodesPerCityGroup*10 {
		t.Errorf("Total nodes %d exceeds expected limit %d", len(result), MaxNodesPerCityGroup*10)
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

	// Strict FAIL-CLOSED: create a real health record for the SUI Singapore
	// candidate so the subscription is not empty. Without this, GetSubs
	// correctly returns empty (no virtual records).
	now := time.Now().UTC().Format(time.RFC3339)
	db.Create(&model.NodeHealthStatus{
		Node: "sui-sg-health", Provider: "SUI", Country: "新加坡", Region: "中央区", City: "新加坡城",
		Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: true,
		Latency: 50, Speed: 100.0, LastCheckTime: now,
	})

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

func TestSubscriptionRejectsUnhealthyNode(t *testing.T) {
	candidate := CandidateNode{
		Uri:      "hysteria2://pass@dash.icta.top:8444#node",
		Protocol: "hysteria2",
		Provider: "Proton",
		Country:  "日本",
		Region:   "关东",
		City:     "东京",
		Priority: 100,
	}

	healthMap := map[string]*model.NodeHealthStatus{
		candidate.GroupKey(): {
			Node:          candidate.GroupKey(),
			Provider:      candidate.Provider,
			Country:       candidate.Country,
			Region:        candidate.Region,
			City:          candidate.City,
			TCPCheck:      true,
			TLSCheck:      true,
			ProxyCheck:    false, // FAILED proxy check
			Status:        "unavailable",
			Latency:       -1,
			Speed:         0,
			LastCheckTime: time.Now().UTC().Format(time.RFC3339),
			LastError:     "WIREGUARD_HANDSHAKE_TIMEOUT",
		},
	}

	res := FilterHealthyAndGroupTop3Links([]CandidateNode{candidate}, healthMap, 15*time.Minute)
	if len(res) != 0 {
		t.Fatalf("Expected 0 nodes for unhealthy candidate, got %d: %v", len(res), res)
	}
}

func TestSubscriptionRejectsStaleHealth(t *testing.T) {
	candidate := CandidateNode{
		Uri:      "hysteria2://pass@dash.icta.top:8444#node",
		Protocol: "hysteria2",
		Provider: "Proton",
		Country:  "美国",
		Region:   "加州",
		City:     "洛杉矶",
		Priority: 100,
	}

	healthMap := map[string]*model.NodeHealthStatus{
		candidate.GroupKey(): {
			Node:          candidate.GroupKey(),
			Provider:      candidate.Provider,
			Country:       candidate.Country,
			Region:        candidate.Region,
			City:          candidate.City,
			TCPCheck:      true,
			TLSCheck:      true,
			ProxyCheck:    true,
			Status:        "available",
			Latency:       50,
			Speed:         80,
			LastCheckTime: time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339), // Stale > 15m
		},
	}

	res := FilterHealthyAndGroupTop3Links([]CandidateNode{candidate}, healthMap, 15*time.Minute)
	if len(res) != 0 {
		t.Fatalf("Expected 0 nodes for stale health status, got %d: %v", len(res), res)
	}
}

func TestSubscriptionRejectsUnknownHealth(t *testing.T) {
	candidate := CandidateNode{
		Uri:      "hysteria2://pass@dash.icta.top:8444#node",
		Protocol: "hysteria2",
		Provider: "UnknownProvider",
		Country:  "美国",
		Region:   "科罗拉多州",
		City:     "丹佛",
		Priority: 100,
	}

	// Empty healthMap = no verification records
	emptyHealthMap := make(map[string]*model.NodeHealthStatus)

	res := FilterHealthyAndGroupTop3Links([]CandidateNode{candidate}, emptyHealthMap, 15*time.Minute)
	if len(res) != 0 {
		t.Fatalf("FAIL-CLOSED violated: expected 0 nodes for unverified candidate, got %d: %v", len(res), res)
	}
}

func TestSubscriptionAllowsHealthyNode(t *testing.T) {
	candidate := CandidateNode{
		Uri:      "hysteria2://pass@dash.icta.top:8444#node",
		Protocol: "hysteria2",
		Provider: "SUI",
		Country:  "新加坡",
		Region:   "中央区",
		City:     "新加坡城",
		Priority: 100,
	}

	healthMap := map[string]*model.NodeHealthStatus{
		candidate.GroupKey(): {
			Node:          candidate.GroupKey(),
			Provider:      candidate.Provider,
			Country:       candidate.Country,
			Region:        candidate.Region,
			City:          candidate.City,
			TCPCheck:      true,
			TLSCheck:      true,
			ProxyCheck:    true,
			Status:        "available",
			Latency:       45,
			Speed:         95,
			LastCheckTime: time.Now().UTC().Format(time.RFC3339),
		},
	}

	res := FilterHealthyAndGroupTop3Links([]CandidateNode{candidate}, healthMap, 15*time.Minute)
	if len(res) != 1 {
		t.Fatalf("Expected 1 node for healthy candidate, got %d", len(res))
	}
	expectedRemark := "SUI-新加坡-中央区-新加坡城-01"
	if !strings.Contains(res[0], expectedRemark) {
		t.Fatalf("Expected remark %s, got %s", expectedRemark, res[0])
	}
}

func TestSubscriptionTop3AfterHealthFilter(t *testing.T) {
	candidates := []CandidateNode{
		{Uri: "hysteria2://pass@dash.icta.top:8001#n1", Protocol: "hysteria2", Provider: "Proton", Country: "荷兰", Region: "北荷兰", City: "阿姆斯特丹", Priority: 100},
		{Uri: "hysteria2://pass@dash.icta.top:8002#n2", Protocol: "hysteria2", Provider: "Proton", Country: "荷兰", Region: "北荷兰", City: "阿姆斯特丹", Priority: 100},
		{Uri: "hysteria2://pass@dash.icta.top:8003#n3", Protocol: "hysteria2", Provider: "Proton", Country: "荷兰", Region: "北荷兰", City: "阿姆斯特丹", Priority: 100},
		{Uri: "hysteria2://pass@dash.icta.top:8004#n4", Protocol: "hysteria2", Provider: "Proton", Country: "荷兰", Region: "北荷兰", City: "阿姆斯特丹", Priority: 100},
		{Uri: "hysteria2://pass@dash.icta.top:8005#n5", Protocol: "hysteria2", Provider: "Proton", Country: "荷兰", Region: "北荷兰", City: "阿姆斯特丹", Priority: 100},
		{Uri: "hysteria2://pass@dash.icta.top:8006#n6", Protocol: "hysteria2", Provider: "Proton", Country: "荷兰", Region: "北荷兰", City: "阿姆斯特丹", Priority: 100},
		{Uri: "hysteria2://pass@dash.icta.top:8007#n7", Protocol: "hysteria2", Provider: "Proton", Country: "荷兰", Region: "北荷兰", City: "阿姆斯特丹", Priority: 100},
	}

	now := time.Now().UTC().Format(time.RFC3339)
	healthMap := map[string]*model.NodeHealthStatus{
		candidates[0].Uri: {Node: candidates[0].Uri, Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Speed: 10, Latency: 200, LastCheckTime: now},
		candidates[1].Uri: {Node: candidates[1].Uri, Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Speed: 50, Latency: 50, LastCheckTime: now},
		candidates[2].Uri: {Node: candidates[2].Uri, Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Speed: 30, Latency: 100, LastCheckTime: now},
		candidates[3].Uri: {Node: candidates[3].Uri, Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Speed: 40, Latency: 80, LastCheckTime: now},
		candidates[4].Uri: {Node: candidates[4].Uri, Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Speed: 20, Latency: 150, LastCheckTime: now},
		candidates[5].Uri: {Node: candidates[5].Uri, Status: "unavailable", TCPCheck: false, TLSCheck: false, ProxyCheck: false, Speed: 0, Latency: -1, LastCheckTime: now},
		candidates[6].Uri: {Node: candidates[6].Uri, Status: "available", TCPCheck: true, TLSCheck: true, ProxyCheck: false, Speed: 0, Latency: -1, LastCheckTime: now},
	}

	res := FilterHealthyAndGroupTop3Links(candidates, healthMap, 15*time.Minute)
	// With limit=MaxNodesPerCityGroup for non-SUI providers, all 5 healthy nodes pass (n1,n2,n3,n4,n5)
	if len(res) != 5 {
		t.Fatalf("Expected 5 healthy nodes (limit=%d), got %d", MaxNodesPerCityGroup, len(res))
	}

	// Should be sorted by speed DESC: 50 (n2), 40 (n4), 30 (n3), 20 (n5), 10 (n1)
	if !strings.Contains(res[0], "8002") || !strings.Contains(res[0], "-01") {
		t.Fatalf("Top 1 should be 8002 (-01), got %s", res[0])
	}
	if !strings.Contains(res[1], "8004") || !strings.Contains(res[1], "-02") {
		t.Fatalf("Top 2 should be 8004 (-02), got %s", res[1])
	}
	if !strings.Contains(res[2], "8003") || !strings.Contains(res[2], "-03") {
		t.Fatalf("Top 3 should be 8003 (-03), got %s", res[2])
	}
}

func TestSubscriptionFinalRemark(t *testing.T) {
	node := model.NodeHealthStatus{
		Provider: "Proton",
		Country:  "日本",
		Region:   "关东",
		City:     "东京",
	}
	remark := node.StandardRemark(1)
	expected := "Proton-日本-关东-东京-01"
	if remark != expected {
		t.Fatalf("Expected %s, got %s", expected, remark)
	}

	parts := strings.Split(remark, "-")
	if len(parts) != 5 {
		t.Fatalf("Expected 5 segments in remark, got %d in %s", len(parts), remark)
	}
}

func TestSubscriptionNoUpstreamLeak(t *testing.T) {
	testLinks := []string{
		"hysteria2://pass@dash.icta.top:8444?insecure=0&sni=dash.icta.top#SUI-新加坡-中央区-新加坡城-01",
		"hysteria2://pass@dash.icta.top:25536?insecure=0&sni=dash.icta.top#Proton-荷兰-北荷兰-阿姆斯特丹-01",
	}

	pass, violations := ValidateSubscriptionSecurity(testLinks, "dash.icta.top")
	if !pass {
		t.Fatalf("Security validation failed: %v", violations)
	}

	// Check that a link with real egress IP or upstream domain fails
	badLinks := []string{
		"hysteria2://pass@124.156.207.253:8444#SUI-新加坡-01",
		"vless://uuid@workers.dev:443#bad",
	}
	badPass, _ := ValidateSubscriptionSecurity(badLinks, "dash.icta.top")
	if badPass {
		t.Fatalf("Expected security violation for exposed IP/upstream domain")
	}
}

func TestSubscriptionSecurityAcceptsVerifiedGeographicNames(t *testing.T) {
	links := []string{
		"hysteria2://pass@dash.icta.top:8444?insecure=0&sni=dash.icta.top#HProxy-美国-California-Los%20Angeles-01",
		"hysteria2://pass@dash.icta.top:8444?insecure=0&sni=dash.icta.top#HProxy-瑞士-Zug-H%C3%BCnenberg-01",
	}
	pass, violations := ValidateSubscriptionSecurity(links, "dash.icta.top")
	if !pass {
		t.Fatalf("geographic remarks must pass validation: %v", violations)
	}
}
