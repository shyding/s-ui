package service

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
)

// EgressGateway manages VPS port forwarding for Seed/Cloudflare egress nodes.
// Architecture: Client -> dash.icta.top:VPS_PORT -> (iptables DNAT) -> Seed real address.
// This isolates egress from clients: clients only see VPS+port, never Seed IPs.
//
// Port range: 56000-57999 (2000 ports max).
// Mappings persist to /usr/local/s-ui/seed/port_mappings.json.

const (
	EgressGatewayPortStart = 56000
	EgressGatewayPortEnd   = 57999
	EgressGatewayMapFile   = "/usr/local/s-ui/seed/port_mappings.json"
	EgressGatewayVPSDomain = "dash.icta.top"
)

// EgressMapping maps a VPS port to a Seed egress target.
type EgressMapping struct {
	VpsPort    int    `json:"vps_port"`
	SeedHost   string `json:"seed_host"` // Original hostname from URI (for lookup)
	SeedIP     string `json:"seed_ip"`   // Resolved IP (for iptables DNAT)
	SeedPort   string `json:"seed_port"`
	Provider   string `json:"provider"`    // Seed, Cloudflare, etc.
	ConfigHash string `json:"config_hash"` // Normalized config fingerprint
}

var (
	egressGatewayMu sync.Mutex
	cachedMappings  []EgressMapping
	cachedOnce      sync.Once
)

// getCachedMappings returns cached mappings, loading once.
func getCachedMappings() []EgressMapping {
	cachedOnce.Do(func() {
		m, err := LoadEgressMappings()
		if err != nil {
			logger.Warning("Failed to load egress mappings:", err)
			return
		}
		cachedMappings = m
		logger.Info(fmt.Sprintf("Egress gateway: cached %d mappings", len(m)))
	})
	return cachedMappings
}

// GenerateEgressMappings creates VPS port mappings from healthy egress nodes.
// Reads from node health records, assigns ports 56000+, resolves hostnames to IPs.
func GenerateEgressMappings() ([]EgressMapping, error) {
	egressGatewayMu.Lock()
	defer egressGatewayMu.Unlock()

	db := database.GetDB()
	var healthRecords []model.NodeHealthStatus
	// Get healthy non-SUI egress nodes
	result := db.Where("status = ? AND provider != ?", "available", "SUI").Find(&healthRecords)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to query health records: %w", result.Error)
	}

	// Sort by latency (best first) for deterministic port assignment
	sort.Slice(healthRecords, func(i, j int) bool {
		return healthRecords[i].Latency < healthRecords[j].Latency
	})

	var mappings []EgressMapping
	seenConfigs := make(map[string]bool)
	port := EgressGatewayPortStart

	for _, h := range healthRecords {
		if port > EgressGatewayPortEnd {
			logger.Warning("Egress gateway port range exhausted")
			break
		}

		// Parse host:port from Node key
		host, portStr := splitEgressHostPort(h.Node)
		if host == "" || portStr == "" {
			continue
		}

		// Build config fingerprint to dedupe (use Node key which is host:port)
		configKey := h.Node + "|" + h.Provider
		if seenConfigs[configKey] {
			continue
		}
		seenConfigs[configKey] = true

		// Resolve hostname to IP for iptables
		seedIP := host
		if ip := net.ParseIP(host); ip == nil {
			// It's a hostname, resolve it
			ips, err := net.LookupIP(host)
			if err != nil || len(ips) == 0 {
				logger.Warning(fmt.Sprintf("Failed to resolve %s: %v", host, err))
				continue
			}
			// Prefer IPv4
			for _, ip := range ips {
				if ip.To4() != nil {
					seedIP = ip.String()
					break
				}
			}
			if seedIP == host {
				seedIP = ips[0].String()
			}
		}

		mappings = append(mappings, EgressMapping{
			VpsPort:    port,
			SeedHost:   host,
			SeedIP:     seedIP,
			SeedPort:   portStr,
			Provider:   h.Provider,
			ConfigHash: configKey,
		})
		port++
	}

	// Persist to file
	if err := saveEgressMappings(mappings); err != nil {
		return nil, err
	}

	logger.Info(fmt.Sprintf("Generated %d egress port mappings", len(mappings)))
	return mappings, nil
}

// splitEgressHostPort splits "host:port" into host and port.
// Handles IPv6 addresses with brackets.
func splitEgressHostPort(node string) (host, port string) {
	// Handle IPv6 with brackets: [::1]:8080
	if strings.HasPrefix(node, "[") {
		end := strings.Index(node, "]")
		if end != -1 {
			host = node[1:end]
			rest := node[end+1:]
			if strings.HasPrefix(rest, ":") {
				port = rest[1:]
			}
			return host, port
		}
	}
	// IPv4 or hostname: host:port
	idx := strings.LastIndex(node, ":")
	if idx != -1 {
		host = node[:idx]
		port = node[idx+1:]
	}
	return host, port
}

func saveEgressMappings(mappings []EgressMapping) error {
	data, err := json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		return err
	}
	// Ensure directory exists
	if err := os.MkdirAll("/usr/local/s-ui/seed", 0755); err != nil {
		return err
	}
	// Atomic write
	tmpFile := EgressGatewayMapFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpFile, EgressGatewayMapFile)
}

// LoadEgressMappings reads the persisted port mappings.
func LoadEgressMappings() ([]EgressMapping, error) {
	data, err := os.ReadFile(EgressGatewayMapFile)
	if err != nil {
		return nil, err
	}
	var mappings []EgressMapping
	if err := json.Unmarshal(data, &mappings); err != nil {
		return nil, err
	}
	return mappings, nil
}

// ApplyEgressIPTables applies DNAT rules for all egress mappings.
// Also ensures IP forwarding and MASQUERADE are configured.
func ApplyEgressIPTables() error {
	egressGatewayMu.Lock()
	defer egressGatewayMu.Unlock()

	mappings, err := LoadEgressMappings()
	if err != nil {
		return fmt.Errorf("failed to load mappings: %w", err)
	}

	// Ensure IP forwarding
	if err := exec.Command("sh", "-c", "echo 1 > /proc/sys/net/ipv4/ip_forward").Run(); err != nil {
		logger.Warning("Failed to enable IP forwarding:", err)
	}

	// Ensure MASQUERADE
	exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-j", "MASQUERADE").Run()
	if err := exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-j", "MASQUERADE").Run(); err != nil {
		// Rule doesn't exist, add it
		if err := exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING", "-j", "MASQUERADE").Run(); err != nil {
			logger.Warning("Failed to add MASQUERADE:", err)
		}
	}

	applied := 0
	for _, m := range mappings {
		target := fmt.Sprintf("%s:%s", m.SeedIP, m.SeedPort)
		portStr := strconv.Itoa(m.VpsPort)

		for _, proto := range []string{"tcp", "udp"} {
			// Check if rule exists
			check := exec.Command("iptables", "-t", "nat", "-C", "PREROUTING",
				"-p", proto, "--dport", portStr, "-j", "DNAT", "--to-destination", target)
			if check.Run() != nil {
				// Add rule
				add := exec.Command("iptables", "-t", "nat", "-A", "PREROUTING",
					"-p", proto, "--dport", portStr, "-j", "DNAT", "--to-destination", target)
				if add.Run() == nil {
					applied++
				}
			}
		}
	}

	// Save rules for persistence
	exec.Command("sh", "-c", "iptables-save > /etc/iptables/rules.v4").Run()

	logger.Info(fmt.Sprintf("Applied %d iptables DNAT rules for egress gateway", applied/2))
	return nil
}

// GetEgressVpsPort returns the VPS port for a given seed host:port, or 0 if not mapped.
// Checks both original hostname and resolved IP.
func GetEgressVpsPort(seedHost, seedPort string) int {
	// The VPS port-forwarding gateway is intentionally disabled.  Its DNAT
	// rules conflict with the public pool-socks listeners: packets destined
	// for a local SOCKS listener were rewritten to unrelated Cloudflare
	// endpoints before reaching s-ui.  Keep the mapping reader for backward
	// compatibility, but never expose a rewritten gateway port to clients.
	return 0
}

// RefreshEgressGateway regenerates mappings and applies iptables.
// Called after health checks complete or on manual trigger.
func RefreshEgressGateway() error {
	// Do not regenerate or apply DNAT rules. Public pool listeners bind the
	// VPS ports directly; forwarding those ports makes every public SOCKS
	// handshake fail before it reaches s-ui.  Older releases did install rules,
	// however, so merely returning here left stale rules active on upgraded
	// servers. Remove only the gateway's documented port range; unrelated NAT
	// rules are never touched.
	if err := clearEgressIPTables(); err != nil {
		return err
	}
	logger.Info("Egress gateway DNAT disabled; stale gateway rules removed")
	return nil
}

// clearEgressIPTables removes legacy DNAT rules for the public pool port range.
// It is intentionally best-effort on non-Linux development machines where
// iptables is not installed. The server path returns an error only when an
// available iptables command fails, so deployment cannot silently claim that
// stale rules were removed.
func clearEgressIPTables() error {
	for _, binary := range []string{"iptables", "ip6tables"} {
		path, err := exec.LookPath(binary)
		if err != nil {
			continue
		}
		out, err := exec.Command(path, "-t", "nat", "-S", "PREROUTING").Output()
		if err != nil {
			return fmt.Errorf("list %s PREROUTING rules: %w", binary, err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) == 0 || fields[0] != "-A" || !isLegacyGatewayDNAT(fields) {
				continue
			}
			fields[0] = "-D"
			if err := exec.Command(path, append([]string{"-t", "nat"}, fields...)...).Run(); err != nil {
				return fmt.Errorf("remove stale %s rule %q: %w", binary, line, err)
			}
		}
	}
	return nil
}

func isLegacyGatewayDNAT(fields []string) bool {
	hasDNAT := false
	hasGatewayPort := false
	for i, field := range fields {
		switch field {
		case "-j":
			if i+1 < len(fields) && strings.EqualFold(fields[i+1], "DNAT") {
				hasDNAT = true
			}
		case "--dport":
			if i+1 < len(fields) && gatewayPortSpec(fields[i+1]) {
				hasGatewayPort = true
			}
		case "--dports":
			if i+1 < len(fields) && gatewayPortSpec(fields[i+1]) {
				hasGatewayPort = true
			}
		}
	}
	return hasDNAT && hasGatewayPort
}

func gatewayPortSpec(spec string) bool {
	parts := strings.SplitN(spec, ":", 2)
	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	end := start
	if len(parts) == 2 {
		end, err = strconv.Atoi(parts[1])
		if err != nil {
			return false
		}
	}
	return start >= EgressGatewayPortStart && end <= EgressGatewayPortEnd
}
