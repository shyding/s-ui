package service

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gorm.io/gorm"
)

const (
	SUIWireGuardPortMin = 54181
	SUIWireGuardPortMax = 54182
	legacyWireGuardPort = 54180
)

var suiWireGuardSpecs = []struct {
	Tag  string
	Port int
}{
	{Tag: "wireguard-warp-54181", Port: 54181},
	{Tag: "wireguard-warp-54182", Port: 54182},
}

// EnsureSUIWireGuardNodes provisions two independent WARP identities once.
// Existing identities are never overwritten, so issued subscription links stay
// stable across restarts and deployments.
func EnsureSUIWireGuardNodes(db *gorm.DB) (int, error) {
	if db == nil {
		db = database.GetDB()
	}
	if db == nil {
		return 0, fmt.Errorf("database is not initialized")
	}
	if err := migrateLegacySUIWireGuardPort(db); err != nil {
		return 0, err
	}
	created := 0
	for _, spec := range suiWireGuardSpecs {
		var existing model.SUIWireGuardNode
		err := db.Where("port = ?", spec.Port).First(&existing).Error
		if err == nil {
			if changed, e := ensureNativeWireGuardIdentity(db, &existing); e != nil {
				return created, e
			} else if changed {
				logger.Infof("migrated %s to native WireGuard server", existing.Tag)
			}
			continue
		}
		if err != gorm.ErrRecordNotFound {
			return created, err
		}

		ep := model.Endpoint{Type: "warp", Tag: spec.Tag, Options: json.RawMessage(`{}`)}
		if err := (&WarpService{}).RegisterWarp(&ep); err != nil {
			return created, fmt.Errorf("register %s: %w", spec.Tag, err)
		}
		if _, err := parseSUIWireGuardOptions(ep.Options); err != nil {
			return created, fmt.Errorf("validate %s: %w", spec.Tag, err)
		}
		node := model.SUIWireGuardNode{Tag: spec.Tag, Port: spec.Port, Options: nativeWireGuardOptions(spec.Port)}
		if err := db.Create(&node).Error; err != nil {
			return created, err
		}
		created++
		logger.Infof("initialized native WireGuard node %s on UDP %d", spec.Tag, spec.Port)
	}
	return created, nil
}

func nativeWireGuardOptions(port int) json.RawMessage {
	serverKey, _ := wgtypes.GenerateKey()
	clientKey, _ := wgtypes.GenerateKey()
	serverIP := fmt.Sprintf("10.200.%d.1/24", port-54100)
	clientIP := fmt.Sprintf("10.200.%d.2/32", port-54100)
	data := map[string]string{"server_private_key": serverKey.String(), "server_public_key": serverKey.PublicKey().String(), "client_private_key": clientKey.String(), "client_public_key": clientKey.PublicKey().String(), "server_address": serverIP, "client_address": clientIP}
	raw, _ := json.MarshalIndent(data, "", "  ")
	return raw
}

func ensureNativeWireGuardIdentity(db *gorm.DB, node *model.SUIWireGuardNode) (bool, error) {
	var probe struct {
		ServerPrivateKey string `json:"server_private_key"`
	}
	if json.Unmarshal(node.Options, &probe) == nil && probe.ServerPrivateKey != "" {
		return false, nil
	}
	return true, db.Model(node).Update("options", nativeWireGuardOptions(node.Port)).Error
}

// Port 54180 is already owned by the host's kernel WireGuard server (wg0).
// Early builds accidentally selected the same port for the first WARP relay.
// Preserve that WARP identity while moving it to the next free application
// port, so existing wg0 peers and the subscription nodes can coexist.
func migrateLegacySUIWireGuardPort(db *gorm.DB) error {
	var legacy model.SUIWireGuardNode
	err := db.Where("port = ?", legacyWireGuardPort).First(&legacy).Error
	if err == gorm.ErrRecordNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	var target model.SUIWireGuardNode
	err = db.Where("port = ?", SUIWireGuardPortMax).First(&target).Error
	if err == nil {
		return db.Delete(&legacy).Error
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}
	if err := db.Model(&legacy).Updates(map[string]interface{}{
		"port": SUIWireGuardPortMax,
		"tag":  "wireguard-warp-54182",
	}).Error; err != nil {
		return fmt.Errorf("migrate legacy WireGuard relay from UDP %d: %w", legacyWireGuardPort, err)
	}
	logger.Infof("migrated native WireGuard relay from occupied UDP %d to UDP %d", legacyWireGuardPort, SUIWireGuardPortMax)
	return nil
}

type suiWireGuardOptions struct {
	ServerPrivateKey string
	ServerPublicKey  string
	ClientPrivateKey string
	ClientPublicKey  string
	ServerAddress    string
	ClientAddress    string
	PrivateKey       string
	Addresses        []string
	PeerHost         string
	PeerPort         int
	PublicKey        string
	Reserved         []int
}

func parseSUIWireGuardOptions(raw json.RawMessage) (*suiWireGuardOptions, error) {
	var native struct {
		ServerPrivateKey string `json:"server_private_key"`
		ServerPublicKey  string `json:"server_public_key"`
		ClientPrivateKey string `json:"client_private_key"`
		ClientPublicKey  string `json:"client_public_key"`
		ServerAddress    string `json:"server_address"`
		ClientAddress    string `json:"client_address"`
	}
	_ = json.Unmarshal(raw, &native)
	if native.ServerPrivateKey != "" && native.ClientPrivateKey != "" && native.ServerAddress != "" && native.ClientAddress != "" {
		return &suiWireGuardOptions{ServerPrivateKey: native.ServerPrivateKey, ServerPublicKey: native.ServerPublicKey, ClientPrivateKey: native.ClientPrivateKey, ClientPublicKey: native.ClientPublicKey, ServerAddress: native.ServerAddress, ClientAddress: native.ClientAddress}, nil
	}
	var opts struct {
		PrivateKey string   `json:"private_key"`
		Address    []string `json:"address"`
		Peers      []struct {
			Address   string `json:"address"`
			Port      int    `json:"port"`
			PublicKey string `json:"public_key"`
			Reserved  []int  `json:"reserved"`
		} `json:"peers"`
	}
	if err := json.Unmarshal(raw, &opts); err != nil {
		return nil, err
	}
	if opts.PrivateKey == "" || len(opts.Address) == 0 || len(opts.Peers) == 0 ||
		opts.Peers[0].Address == "" || opts.Peers[0].Port == 0 || opts.Peers[0].PublicKey == "" {
		return nil, fmt.Errorf("incomplete WireGuard identity")
	}
	return &suiWireGuardOptions{
		PrivateKey: opts.PrivateKey, Addresses: opts.Address,
		PeerHost: opts.Peers[0].Address, PeerPort: opts.Peers[0].Port,
		PublicKey: opts.Peers[0].PublicKey, Reserved: opts.Peers[0].Reserved,
	}, nil
}

// GetSUIWireGuardLinks returns v2rayN-compatible native WireGuard URIs.  The
// client only sees dash.icta.top; the real WARP endpoint remains server-side.
func GetSUIWireGuardLinks(db *gorm.DB) []string {
	if db == nil {
		db = database.GetDB()
	}
	if db == nil {
		return nil
	}
	var nodes []model.SUIWireGuardNode
	if err := db.Order("port ASC").Find(&nodes).Error; err != nil {
		return nil
	}
	links := make([]string, 0, len(nodes))
	for i, node := range nodes {
		opts, err := parseSUIWireGuardOptions(node.Options)
		if err != nil {
			logger.Warningf("skip invalid WireGuard node %s: %v", node.Tag, err)
			continue
		}
		privateKey := opts.ClientPrivateKey
		publicKey := opts.ServerPublicKey
		address := opts.ClientAddress
		if privateKey == "" {
			privateKey = opts.PrivateKey
		}
		if publicKey == "" {
			publicKey = opts.PublicKey
		}
		if address == "" && len(opts.Addresses) > 0 {
			address = opts.Addresses[0]
		}
		u := &url.URL{
			Scheme:   "wireguard",
			User:     url.User(privateKey),
			Host:     net.JoinHostPort(SUIServerName, strconv.Itoa(node.Port)),
			Fragment: FormatStandardRemark("SUI", "新加坡", "中央区", "新加坡城", 39+i),
		}
		q := url.Values{}
		q.Set("publickey", publicKey)
		q.Set("address", address)
		// Explicit route selectors are required by v2rayN and several mobile
		// clients; without them the profile imports but is reported as -1.
		q.Set("allowed_ips", "0.0.0.0/0,::/0")
		q.Set("mtu", "1280")
		// Keep the NAT mapping alive for clients behind restrictive networks.
		// v2rayN/sing-box map this standard WireGuard URI field to
		// persistent_keepalive_interval; clients that do not support it simply
		// ignore the optional query parameter.
		q.Set("keepalive", "25")
		// sing-box/v2rayN versions differ in the URI query key they map to
		// WireGuard's persistent keepalive field. Emit the canonical key as
		// well, otherwise an imported profile may never refresh its handshake
		// when it is behind a NAT.
		q.Set("persistent_keepalive_interval", "25")
		q.Set("reserved", joinInts(opts.Reserved))
		u.RawQuery = q.Encode()
		links = append(links, u.String())
	}
	return links
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.Itoa(value)
	}
	return strings.Join(parts, ",")
}

var (
	suiWireGuardRelayMu        sync.Mutex
	suiWireGuardRelayListeners = make(map[int]*net.UDPConn)
)

type suiWireGuardRelaySession struct {
	backend  *net.UDPConn
	client   *net.UDPAddr
	lastSeen atomic.Int64
}

// ConfigureSUIWireGuardForwarding starts application-owned UDP relays. Keeping
// a real socket bound to each public port is reliable behind a cloud 1:1 NAT;
// the former PREROUTING DNAT path could return the WARP handshake on the host
// while intermittently losing it before it reached the client.
func ConfigureSUIWireGuardForwarding(db *gorm.DB) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if db == nil {
		db = database.GetDB()
	}
	var nodes []model.SUIWireGuardNode
	if err := db.Order("port ASC").Find(&nodes).Error; err != nil {
		return err
	}
	if len(nodes) != len(suiWireGuardSpecs) {
		return fmt.Errorf("expected %d WireGuard nodes, found %d", len(suiWireGuardSpecs), len(nodes))
	}
	// Always remove the obsolete 54180 DNAT before starting listeners. The
	// port belongs to the host's kernel wg0 service, not the WARP relays.
	if len(nodes) > 0 {
		opts, err := parseSUIWireGuardOptions(nodes[0].Options)
		if err != nil {
			return err
		}
		if opts.ServerPrivateKey == "" {
			peerIP, err := firstIPv4(opts.PeerHost)
			if err != nil {
				return err
			}
			destination := net.JoinHostPort(peerIP, strconv.Itoa(opts.PeerPort))
			if err := deleteIPTablesRule("nat", "PREROUTING", []string{"-p", "udp", "--dport", strconv.Itoa(legacyWireGuardPort), "-j", "DNAT", "--to-destination", destination}); err != nil {
				return err
			}
		}
	}
	for _, node := range nodes {
		opts, err := parseSUIWireGuardOptions(node.Options)
		if err != nil {
			return err
		}
		if opts.ServerPrivateKey == "" {
			return fmt.Errorf("WireGuard node %d is not native", node.Port)
		}
		if err := configureNativeWireGuard(node.Port, opts); err != nil {
			return err
		}
		logger.Infof("native WireGuard server prepared: UDP %d (%s)", node.Port, opts.ServerAddress)
	}
	return nil
}

func configureNativeWireGuard(port int, opts *suiWireGuardOptions) error {
	name := fmt.Sprintf("suiwg%d", port)
	clientIP := opts.ClientAddress
	if err := exec.Command("ip", "link", "show", name).Run(); err != nil {
		if out, e := runRoot("ip", "link", "add", "dev", name, "type", "wireguard"); e != nil {
			return fmt.Errorf("create %s: %w (%s)", name, e, strings.TrimSpace(string(out)))
		}
	}
	keyFile := fmt.Sprintf("/etc/wireguard/%s.key", name)
	if err := os.WriteFile(fmt.Sprintf("/tmp/%s.key", name), []byte(opts.ServerPrivateKey+"\n"), 0600); err != nil {
		return err
	}
	if _, err := runRoot("install", "-m", "600", fmt.Sprintf("/tmp/%s.key", name), keyFile); err != nil {
		return err
	}
	if out, e := runRoot("ip", "address", "replace", opts.ServerAddress, "dev", name); e != nil {
		return fmt.Errorf("address %s: %w (%s)", name, e, out)
	}
	// A previous deployment may have used a different generated identity. Remove
	// stale peers before installing the current one; otherwise the kernel keeps
	// accepting old keys and clients receive inconsistent subscription profiles.
	if peersOut, e := runRoot("wg", "show", name, "peers"); e == nil {
		for _, peer := range strings.Fields(string(peersOut)) {
			if peer != "" && peer != opts.ClientPublicKey {
				_, _ = runRoot("wg", "set", name, "peer", peer, "remove")
			}
		}
	}
	if out, e := runRoot("wg", "set", name, "listen-port", strconv.Itoa(port), "private-key", keyFile, "peer", opts.ClientPublicKey, "allowed-ips", clientIP); e != nil {
		return fmt.Errorf("configure %s: %w (%s)", name, e, out)
	}
	if out, e := runRoot("ip", "link", "set", "up", "dev", name); e != nil {
		return fmt.Errorf("start %s: %w (%s)", name, e, out)
	}
	_, _ = runRoot("sysctl", "-w", "net.ipv4.ip_forward=1")
	_ = ensureIPTablesRule([]string{"-A", "FORWARD", "-i", name, "-j", "ACCEPT"})
	_ = ensureIPTablesRule([]string{"-A", "FORWARD", "-o", name, "-j", "ACCEPT"})
	_, subnet, _ := net.ParseCIDR(opts.ServerAddress)
	sourceNet := subnet.String()
	egressIf := defaultEgressInterface()
	if egressIf == "" {
		return fmt.Errorf("cannot determine default egress interface for WireGuard %d", port)
	}
	_ = ensureIPTablesRule([]string{"-t", "nat", "-A", "POSTROUTING", "-s", sourceNet, "-o", egressIf, "-j", "MASQUERADE"})
	return nil
}

// defaultEgressInterface returns the interface selected by the host routing
// table.  Tencent/Ubuntu images are not guaranteed to call it eth0 (ens5,
// enp1s0 and renamed interfaces are common), so a fixed interface silently
// breaks WireGuard handshakes after migration.
func defaultEgressInterface() string {
	out, err := exec.Command("ip", "route", "get", "1.1.1.1").Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	for i, field := range fields {
		if field == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}

func runRoot(name string, args ...string) ([]byte, error) {
	return exec.Command("sudo", append([]string{name}, args...)...).CombinedOutput()
}

func ensureIPTablesRule(rule []string) error {
	table := "filter"
	args := append([]string(nil), rule...)
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-t" {
			table = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	chain := ""
	for i, v := range args {
		if v == "-A" && i+1 < len(args) {
			chain = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	if chain == "" {
		return fmt.Errorf("invalid iptables rule")
	}
	check := append([]string{"-t", table, "-C", chain}, args...)
	if _, err := runRoot("iptables", check...); err == nil {
		return nil
	}
	add := append([]string{"-t", table, "-A", chain}, args...)
	_, err := runRoot("iptables", add...)
	return err
}

func startSUIWireGuardUDPRelay(port int, peerIP string, peerPort int) error {
	suiWireGuardRelayMu.Lock()
	defer suiWireGuardRelayMu.Unlock()
	if suiWireGuardRelayListeners[port] != nil {
		return nil
	}
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		return fmt.Errorf("listen WireGuard UDP relay on %d: %w", port, err)
	}
	suiWireGuardRelayListeners[port] = listener
	go runSUIWireGuardUDPRelay(listener, &net.UDPAddr{IP: net.ParseIP(peerIP).To4(), Port: peerPort})
	return nil
}

func runSUIWireGuardUDPRelay(listener *net.UDPConn, remote *net.UDPAddr) {
	sessions := make(map[string]*suiWireGuardRelaySession)
	var sessionsMu sync.Mutex
	buffer := make([]byte, 64*1024)
	for {
		n, client, err := listener.ReadFromUDP(buffer)
		if err != nil {
			logger.Warning("WireGuard UDP relay stopped:", err)
			return
		}
		key := client.String()
		sessionsMu.Lock()
		session := sessions[key]
		if session == nil {
			backend, dialErr := net.DialUDP("udp4", nil, remote)
			if dialErr != nil {
				sessionsMu.Unlock()
				logger.Warningf("WireGuard relay backend dial failed for %s: %v", key, dialErr)
				continue
			}
			session = &suiWireGuardRelaySession{backend: backend, client: client}
			sessions[key] = session
			go readSUIWireGuardRelayReplies(listener, session, key, sessions, &sessionsMu)
		}
		session.lastSeen.Store(time.Now().UnixNano())
		sessionsMu.Unlock()
		packet := append([]byte(nil), buffer[:n]...)
		if _, err := session.backend.Write(packet); err != nil {
			logger.Warningf("WireGuard relay backend write failed for %s: %v", key, err)
		}
	}
}

func readSUIWireGuardRelayReplies(listener *net.UDPConn, session *suiWireGuardRelaySession, key string, sessions map[string]*suiWireGuardRelaySession, sessionsMu *sync.Mutex) {
	defer func() {
		_ = session.backend.Close()
		sessionsMu.Lock()
		if sessions[key] == session {
			delete(sessions, key)
		}
		sessionsMu.Unlock()
	}()
	buffer := make([]byte, 64*1024)
	for {
		_ = session.backend.SetReadDeadline(time.Now().Add(2 * time.Minute))
		n, err := session.backend.Read(buffer)
		if err != nil {
			return
		}
		if _, err := listener.WriteToUDP(buffer[:n], session.client); err != nil {
			return
		}
	}
}

func firstIPv4(host string) (string, error) {
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		return ip.String(), nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return "", err
	}
	sort.SliceStable(addrs, func(i, j int) bool { return addrs[i].String() < addrs[j].String() })
	for _, ip := range addrs {
		if ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address for %s", host)
}

func deleteIPTablesRule(table, chain string, rule []string) error {
	prefix := []string{"-w", "5"}
	if table != "" {
		prefix = append(prefix, "-t", table)
	}
	for {
		check := append(append([]string{}, prefix...), "-C", chain)
		check = append(check, rule...)
		if exec.Command("iptables", check...).Run() != nil {
			return nil
		}
		remove := append(append([]string{}, prefix...), "-D", chain)
		remove = append(remove, rule...)
		if out, err := exec.Command("iptables", remove...).CombinedOutput(); err != nil {
			return fmt.Errorf("remove legacy iptables %s rule: %w (%s)", chain, err, strings.TrimSpace(string(out)))
		}
	}
}
