package service

import "testing"

func TestIsLegacyGatewayDNAT(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"tcp pool rule", []string{"-A", "PREROUTING", "-p", "tcp", "--dport", "56001", "-j", "DNAT", "--to-destination", "1.2.3.4:443"}, true},
		{"range pool rule", []string{"-A", "PREROUTING", "-p", "tcp", "--dports", "56000:57999", "-j", "DNAT", "--to-destination", "1.2.3.4:443"}, true},
		{"port before target", []string{"-A", "PREROUTING", "--dport", "56002", "-j", "DNAT"}, true},
		{"outside range", []string{"-A", "PREROUTING", "--dport", "2096", "-j", "DNAT", "--to-destination", "1.2.3.4:2096"}, false},
		{"not dnat", []string{"-A", "PREROUTING", "--dport", "56003", "-j", "ACCEPT"}, false},
		{"partial range", []string{"-A", "PREROUTING", "--dports", "55999:56001", "-j", "DNAT"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLegacyGatewayDNAT(tt.args); got != tt.want {
				t.Fatalf("isLegacyGatewayDNAT(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
