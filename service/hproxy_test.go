package service

import "testing"

func TestParseHProxyCandidatesFiltersAndKeepsGeoMetadata(t *testing.T) {
	content := `[
		{"proxy":"198.51.100.10:1080","ip":"198.51.100.10","port":1080,"protocols":["socks5"],"anonymity":"elite","country":"JP","city":"Tokyo","latency_ms":80,"uptime_24h":99,"uptime_7d":90,"alive":true},
		{"proxy":"198.51.100.11:8080","ip":"198.51.100.11","port":8080,"protocols":["http"],"anonymity":"transparent","country":"US","city":"New York","latency_ms":70,"uptime_24h":99,"alive":true}
	]`
	result, ok, err := parseHProxyCandidates(content, "hproxy")
	if err != nil || !ok {
		t.Fatalf("expected HProxy data to be detected, ok=%v err=%v", ok, err)
	}
	if len(result.Outbounds) != 1 {
		t.Fatalf("got %d candidates, want 1", len(result.Outbounds))
	}
	node := result.Outbounds[0]
	if node["type"] != "socks" || node["country"] != "JP" || node["city"] != "Tokyo" {
		t.Fatalf("unexpected candidate: %#v", node)
	}
}
