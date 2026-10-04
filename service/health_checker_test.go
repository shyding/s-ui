package service

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alireza0/s-ui/database/model"
)

func TestHealthChecker_Stage1_CheckNode(t *testing.T) {
	// Create mock HTTP 204 server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer mockServer.Close()

	checker := NewHealthChecker()
	checker.DefaultTargetURL = mockServer.URL
	checker.Timeout = 2 * time.Second

	node := &model.NodeHealthStatus{
		Node:     "Cloudflare-美国-加州-洛杉矶-01",
		Provider: "Cloudflare",
		Country:  "美国",
		Region:   "加州",
		City:     "洛杉矶",
	}

	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", mockServer.Listener.Addr().String())
	}

	err := checker.CheckNode(context.Background(), node, dialContext, "127.0.0.1", 80, false, "")
	if err != nil {
		t.Fatalf("CheckNode failed: %v", err)
	}

	if !node.IsHealthy() {
		t.Fatalf("Expected healthy node, got %+v", node)
	}
	if node.Status != "available" {
		t.Errorf("Expected status available, got %s", node.Status)
	}
	if !node.TCPCheck || !node.TLSCheck || !node.ProxyCheck {
		t.Errorf("Expected all checks true, got tcp=%v, tls=%v, proxy=%v", node.TCPCheck, node.TLSCheck, node.ProxyCheck)
	}
}

func TestHealthChecker_FilterAndGroupTop3(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	nodes := []*model.NodeHealthStatus{
		// Group 1: 4 healthy nodes (should retain top 3)
		{Node: "n1", Provider: "Cloudflare", Country: "美国", Region: "加州", City: "洛杉矶", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Status: "available", Latency: 50, Speed: 200, LastCheckTime: now},
		{Node: "n2", Provider: "Cloudflare", Country: "美国", Region: "加州", City: "洛杉矶", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Status: "available", Latency: 80, Speed: 125, LastCheckTime: now},
		{Node: "n3", Provider: "Cloudflare", Country: "美国", Region: "加州", City: "洛杉矶", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Status: "available", Latency: 100, Speed: 100, LastCheckTime: now},
		{Node: "n4", Provider: "Cloudflare", Country: "美国", Region: "加州", City: "洛杉矶", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Status: "available", Latency: 150, Speed: 66, LastCheckTime: now},

		// Group 2: 1 healthy, 1 unhealthy
		{Node: "n5", Provider: "Proton", Country: "日本", Region: "关东", City: "东京", TCPCheck: true, TLSCheck: true, ProxyCheck: true, Status: "available", Latency: 30, Speed: 333, LastCheckTime: now},
		{Node: "n6", Provider: "Proton", Country: "日本", Region: "关东", City: "东京", TCPCheck: false, TLSCheck: false, ProxyCheck: false, Status: "unavailable", Latency: -1, Speed: 0, LastCheckTime: now},
	}

	filtered := FilterAndGroupTop3(nodes)

	// Group 1 should have 3, Group 2 should have 1 -> Total 4
	if len(filtered) != 4 {
		t.Fatalf("Expected 4 filtered nodes, got %d", len(filtered))
	}

	// Verify standard remark formatting
	if filtered[0].Node != "Cloudflare-美国-加州-洛杉矶-01" {
		t.Errorf("First node expected Cloudflare-美国-加州-洛杉矶-01, got %s", filtered[0].Node)
	}
	if filtered[1].Node != "Cloudflare-美国-加州-洛杉矶-02" {
		t.Errorf("Second node expected Cloudflare-美国-加州-洛杉矶-02, got %s", filtered[1].Node)
	}
	if filtered[2].Node != "Cloudflare-美国-加州-洛杉矶-03" {
		t.Errorf("Third node expected Cloudflare-美国-加州-洛杉矶-03, got %s", filtered[2].Node)
	}
	if filtered[3].Node != "Proton-日本-关东-东京-01" {
		t.Errorf("Fourth node expected Proton-日本-关东-东京-01, got %s", filtered[3].Node)
	}
}

func TestResolveEgressComponents(t *testing.T) {
	prov, c, r, ct := ResolveEgressComponents("cf-us-lax", "美国·洛杉矶-Cloudflare洁净出口")
	if prov != "Cloudflare" || c != "美国" || r != "加州" || ct != "洛杉矶" {
		t.Errorf("LAX mismatch: prov=%s, c=%s, r=%s, ct=%s", prov, c, r, ct)
	}

	prov, c, r, ct = ResolveEgressComponents("cf-jp-nrt", "日本·东京-Cloudflare洁净出口")
	if prov != "Cloudflare" || c != "日本" || r != "关东" || ct != "东京" {
		t.Errorf("NRT mismatch: prov=%s, c=%s, r=%s, ct=%s", prov, c, r, ct)
	}

	prov, c, r, ct = ResolveEgressComponents("us", "Proton-美国-加州-洛杉矶")
	if prov != "Proton" || c != "美国" || r != "加州" || ct != "洛杉矶" {
		t.Errorf("Proton US mismatch: prov=%s, c=%s, r=%s, ct=%s", prov, c, r, ct)
	}

	prov, c, r, ct = ResolveEgressComponents("", "")
	if prov != "SUI" || c != "新加坡" || r != "中央区" || ct != "新加坡城" {
		t.Errorf("Default SUI mismatch: prov=%s, c=%s, r=%s, ct=%s", prov, c, r, ct)
	}
}

func TestSubscriptionGeographyIsLocalizedToChinese(t *testing.T) {
	provider, country, region, city := ResolveEgressComponents(
		"seed-us-california-los-angeles",
		"Seed-美国-California-Los Angeles",
	)
	if provider != "Seed" || country != "美国" || region != "California" || city != "Los Angeles" {
		t.Fatalf("unexpected seed components: %s/%s/%s/%s", provider, country, region, city)
	}
	if got := FormatStandardRemark(provider, country, region, city, 1); got != "🇺🇸Seed-美国-加州-洛杉矶-01" {
		t.Fatalf("expected fully localized remark, got %q", got)
	}
	if got := FormatStandardRemark("Proton", "MX", "Mexico City", "Mexico City", 1); got != "🇲🇽Proton-墨西哥-墨西哥城-墨西哥城-01" {
		t.Fatalf("expected Mexican geography to be localized, got %q", got)
	}
}
