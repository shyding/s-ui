package sub

import "testing"

func TestValidateSubscriptionSecurityAllowsWireGuardTunnelAddresses(t *testing.T) {
	link := "wireguard://private-key@dash.icta.top:54180?address=172.16.0.2%2F32%2C2606%3A4700%3A110%3A8abc%3A%3A2%2F128&mtu=1280&publickey=peer-key&reserved=1%2C2%2C3#🇸🇬SUI-新加坡-中央区-新加坡城-39"
	ok, violations := ValidateSubscriptionSecurity([]string{link}, "dash.icta.top")
	if !ok {
		t.Fatalf("native WireGuard link should pass security validation: %v", violations)
	}
}
