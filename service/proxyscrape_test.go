package service

import "testing"

func TestParseProxyScrapeCandidatesFiltersAndKeepsGeoMetadata(t *testing.T) {
	content := `[
		{"protocol":"socks5","ip":"198.51.100.10","port":1080,"country_code":"DE","city":"Frankfurt","anonymity":"elite","uptime_percent":99,"latency_ms":80},
		{"protocol":"socks4","ip":"198.51.100.11","port":1080,"country_code":"US","city":"New York","anonymity":"elite","uptime_percent":99,"latency_ms":70},
		{"protocol":"http","ip":"198.51.100.12","port":8080,"country_code":"JP","city":"Tokyo","anonymity":"transparent","uptime_percent":99,"latency_ms":70}
	]`
	result, ok, err := parseProxyScrapeCandidates(content, "proxyscrape")
	if err != nil || !ok {
		t.Fatalf("expected ProxyScrape data to be detected, ok=%v err=%v", ok, err)
	}
	if len(result.Outbounds) != 1 {
		t.Fatalf("got %d candidates, want 1", len(result.Outbounds))
	}
	node := result.Outbounds[0]
	if node["type"] != "socks" || node["country"] != "DE" || node["city"] != "Frankfurt" {
		t.Fatalf("unexpected candidate: %#v", node)
	}
}

func TestProtonDynamicCountryPoolNaming(t *testing.T) {
	region := protonRegionForCountry("CA")
	if region.Code != "proton-ca" || region.OutboundTag != "proton-ca-pool" {
		t.Fatalf("unexpected dynamic Proton region: %#v", region)
	}
	if protonPoolTag("US") != "us-pool" {
		t.Fatal("existing US pool tag must remain stable")
	}
}
