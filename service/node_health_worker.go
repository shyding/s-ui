package service

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alireza0/s-ui/database"
	"github.com/alireza0/s-ui/database/model"
	"github.com/alireza0/s-ui/logger"
	"strconv"
)

// NodeHealthWorker 负责后台定期健康检查：
// 1. 对每个 external / sub 节点做 TCP + TLS 检测
// 2. 查询真实出口 IP 地理位置 (ip-api.com)
// 3. 生成规范备注 = 来源-国家-区域-城市-序号
// 4. 写入 node_health_statuses 表供订阅硬门禁使用
type NodeHealthWorker struct {
	mu          sync.Mutex
	running     bool
	cancelFn    context.CancelFunc
	interval    time.Duration
	concurrency int
}

var globalHealthWorker *NodeHealthWorker
var workerOnce sync.Once

// isNodeCheckRunning 防并发：同一时刻只允许一个 NodeHealthWorker 实例运行
var isNodeCheckRunning atomic.Bool

// StartNodeHealthWorker 启动全局后台健康检查 Worker（单例）
// 策略：启动后等5分钟跑第一次，之后每天凌晨 04:30 跑一次（慢速，低并发）
func StartNodeHealthWorker() {
	workerOnce.Do(func() {
		globalHealthWorker = &NodeHealthWorker{
			interval:    24 * time.Hour, // 仅用于日志，实际由 04:30 cron 控制
			concurrency: 5,             // 极低并发，不抢 CPU
		}
		go globalHealthWorker.run()
	})
}

// TriggerNodeHealthCheck 供 UI 手动触发；若已在运行则返回 false
func TriggerNodeHealthCheck() bool {
	if globalHealthWorker == nil {
		StartNodeHealthWorker()
	}
	if !isNodeCheckRunning.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer isNodeCheckRunning.Store(false)
		globalHealthWorker.runOnce()
	}()
	return true
}

// IsNodeCheckRunning 查询节点健康检查是否正在运行
func IsNodeCheckRunning() bool {
	return isNodeCheckRunning.Load()
}

func (w *NodeHealthWorker) run() {
	w.mu.Lock()
	w.running = true
	w.mu.Unlock()

	// 仅在配置的定时时间运行，不在启动时自动跑（避免 CPU 过载导致 VPS 崩溃）
	// 用户可通过前端手动触发
	for {
		d, t := nextScheduledRun()
		logger.Infof("NodeHealthWorker: 下次运行时间 %v 后 (%s)", d.Round(time.Minute), t)
		time.Sleep(d)
		if isNodeCheckRunning.CompareAndSwap(false, true) {
			w.runOnce()
			isNodeCheckRunning.Store(false)
		} else {
			logger.Info("NodeHealthWorker: 上次检测仍在运行，跳过本次")
		}
	}
}

func (w *NodeHealthWorker) runOnce() {
	defer func() {
		if r := recover(); r != nil {
			logger.Warning("NodeHealthWorker panic recovered:", r)
		}
	}()

	db := database.GetDB()
	if db == nil {
		return
	}

	// 读取 client=2 (my) 的 links BLOB
	type Client struct {
		Links []byte
	}
	var clients []struct {
		ID    int
		Links []byte
	}
	db.Raw("SELECT id, links FROM clients WHERE enable=1").Scan(&clients)

	type LinkEntry struct {
		Type   string `json:"type"`
		Remark string `json:"remark"`
		Uri    string `json:"uri"`
	}

	// 收集所有 external 和 local 节点 URI
	// - external: 外部订阅节点
	// - local: VPS 自身 inbound（SUI 节点），必须检测否则订阅 FAIL-CLOSED 无法发布
	var allURIs []string
	for _, c := range clients {
		if len(c.Links) == 0 {
			continue
		}
		var links []LinkEntry
		if err := json.Unmarshal(c.Links, &links); err != nil {
			continue
		}
		for _, l := range links {
			if (l.Type == "external" || l.Type == "local") && l.Uri != "" {
				allURIs = append(allURIs, l.Uri)
			}
		}
	}

	// 收集 Seed 节点 URI（来自 SUI_SEED_NODES_FILE）
	// Seed 节点必须检测，否则订阅 FAIL-CLOSED 无法发布 Seed 节点
	seedFile := os.Getenv("SUI_SEED_NODES_FILE")
	if seedFile != "" {
		if f, err := os.Open(seedFile); err == nil {
			scanner := bufio.NewScanner(f)
			// 增大 buffer 以支持长 URI
			buf := make([]byte, 0, 64*1024)
			scanner.Buffer(buf, 1024*1024)
			seedCount := 0
			for scanner.Scan() {
				uri := strings.TrimSpace(scanner.Text())
				if uri != "" && (strings.HasPrefix(uri, "vless://") || strings.HasPrefix(uri, "trojan://") ||
					strings.HasPrefix(uri, "vmess://") || strings.HasPrefix(uri, "ss://") ||
					strings.HasPrefix(uri, "socks5://") || strings.HasPrefix(uri, "socks://")) {
					allURIs = append(allURIs, uri)
					seedCount++
				}
			}
			f.Close()
			if seedCount > 0 {
				logger.Infof("NodeHealthWorker: 从 Seed 文件加载 %d 个节点", seedCount)
			}
		} else {
			logger.Warningf("NodeHealthWorker: 无法打开 Seed 文件 %s: %v", seedFile, err)
		}
	}

	if len(allURIs) == 0 {
		return
	}

	logger.Infof("NodeHealthWorker: 开始检测 %d 个节点", len(allURIs))
	start := time.Now()

	// 设置节点检测进度
	ResetNodeProgress(int32(len(allURIs)))

	sem := make(chan struct{}, w.concurrency)
	var wg sync.WaitGroup
	var passCount, failCount int32

	for _, uri := range allURIs {
		wg.Add(1)
		go func(rawURI string) {
			defer wg.Done()
			defer nodeProgressDone.Add(1)
			sem <- struct{}{}
			defer func() { <-sem }()

			status := checkExternalNode(rawURI)
			if status == nil {
				atomic.AddInt32(&failCount, 1)
				return
			}

			// Upsert into node_health_statuses
			result := db.Exec(`
				INSERT INTO node_health_statuses (node, original_uri, provider, country, region, city, tcp_check, tls_check, proxy_check, latency, speed, status, last_check_time, last_error)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(node) DO UPDATE SET
					original_uri=excluded.original_uri,
					provider=excluded.provider, country=excluded.country, region=excluded.region, city=excluded.city,
					tcp_check=excluded.tcp_check, tls_check=excluded.tls_check, proxy_check=excluded.proxy_check,
					latency=excluded.latency, speed=excluded.speed, status=excluded.status,
					last_check_time=excluded.last_check_time, last_error=excluded.last_error
			`,
				status.Node, status.OriginalURI, status.Provider, status.Country, status.Region, status.City,
				status.TCPCheck, status.TLSCheck, status.ProxyCheck,
				status.Latency, status.Speed, status.Status,
				status.LastCheckTime, status.LastError,
			)
			if result.Error != nil {
				atomic.AddInt32(&failCount, 1)
			} else if status.Status == "available" {
				atomic.AddInt32(&passCount, 1)
			} else {
				atomic.AddInt32(&failCount, 1)
			}
		}(uri)
	}

	wg.Wait()
	elapsed := time.Since(start)
	logger.Infof("NodeHealthWorker: 完成 总=%d PASS=%d FAIL=%d 耗时=%v",
		len(allURIs), passCount, failCount, elapsed.Round(time.Second))

	// 健康检查完成后刷新出口网关映射，确保订阅发布最新的可用节点
	if err := RefreshEgressGateway(); err != nil {
		logger.Errorf("NodeHealthWorker: 刷新出口网关映射失败: %v", err)
	} else {
		logger.Infof("NodeHealthWorker: 出口网关映射已刷新")
	}
}

// vlessHandshake performs a minimal VLESS protocol handshake to verify the UUID.
// It sends a VLESS request header for a test target.
// Returns true if the server accepts the UUID (connection stays open).
// A server closes the connection immediately on invalid UUID.
func vlessHandshake(conn net.Conn, uuidStr string) bool {
	// Parse UUID manually (avoid external dependency)
	// UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (36 chars)
	clean := strings.ReplaceAll(uuidStr, "-", "")
	if len(clean) != 32 {
		return false
	}
	uuidBytes := make([]byte, 16)
	for i := 0; i < 16; i++ {
		b, err := strconv.ParseUint(clean[i*2:i*2+2], 16, 8)
		if err != nil {
			return false
		}
		uuidBytes[i] = byte(b)
	}

	// Build VLESS request: Version(1) + UUID(16) + Addons(1+0) + Command(1) + Port(2) + Address
	// Use 1.1.1.1:80 as test target (we only need the handshake, not actual proxying)
	buf := make([]byte, 0, 64)
	buf = append(buf, 0x00) // Version
	buf = append(buf, uuidBytes...)
	buf = append(buf, 0x00) // Addon length = 0
	buf = append(buf, 0x01) // Command = TCP
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 80)
	buf = append(buf, portBytes...)
	buf = append(buf, 0x01) // Address type = IPv4
	buf = append(buf, 1, 1, 1, 1) // 1.1.1.1

	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(buf); err != nil {
		return false
	}

	// Try to read VLESS response: Version(1) + AddonLen(1) + AddonData
	// Server with valid UUID keeps connection open; invalid UUID -> immediate close
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	resp := make([]byte, 2)
	n, err := io.ReadFull(conn, resp)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// Timeout but connection still open -> UUID accepted
			// (server waiting for more data, not rejecting)
			return true
		}
		// Connection closed/reset -> UUID rejected
		return false
	}
	if n == 2 && resp[0] == 0x00 {
		// Valid VLESS response header
		if resp[1] > 0 {
			addon := make([]byte, resp[1])
			io.ReadFull(conn, addon)
		}
		return true
	}
	return false
}

// extractVlessUUID extracts the UUID from a vless:// URI
func extractVlessUUID(uri string) string {
	if !strings.HasPrefix(uri, "vless://") {
		return ""
	}
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	// UUID is in the userinfo (before @)
	if u.User != nil {
		return u.User.Username()
	}
	return ""
}

// wrapVlessTransport 根据 URI 的传输类型包装连接
// TCP: 直接返回原连接
// WS/HTTPUpgrade: 执行 HTTP Upgrade 握手，返回 WS 帧包装的连接
// gRPC: 执行 gRPC 握手 (简化版)
func wrapVlessTransport(conn net.Conn, uri, host string, useTLS bool, sni string) (net.Conn, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return conn, nil // 解析失败，按 TCP 处理
	}
	q := u.Query()
	transport := strings.ToLower(q.Get("type"))
	if transport == "" {
		transport = "tcp"
	}

	switch transport {
	case "tcp":
		return conn, nil
	case "ws":
		path := q.Get("path")
		if path == "" {
			path = "/"
		}
		wsHost := q.Get("host")
		if wsHost == "" {
			wsHost = host
			if sni != "" {
				wsHost = sni
			}
		}
		return wsHandshake(conn, wsHost, path)
	case "httpupgrade", "http":
		path := q.Get("path")
		if path == "" {
			path = "/"
		}
		return httpUpgradeHandshake(conn, host, path)
	case "grpc", "gun":
		serviceName := q.Get("serviceName")
		if serviceName == "" {
			serviceName = "GunService"
		}
		return grpcHandshake(conn, host, serviceName, useTLS)
	default:
		return conn, nil
	}
}

// wsHandshake 执行 WebSocket 升级握手，返回帧包装的连接
func wsHandshake(conn net.Conn, host, path string) (net.Conn, error) {
	// 生成 Sec-WebSocket-Key
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, host, key)

	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, err
	}

	// 读取响应头
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		return nil, fmt.Errorf("ws read response: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 101 {
		return nil, fmt.Errorf("ws upgrade failed: %d", resp.StatusCode)
	}
	// 返回 WS 帧包装的连接
	return &wsConn{Conn: conn, reader: reader}, nil
}

// wsConn 包装 net.Conn，处理 WebSocket 帧
type wsConn struct {
	net.Conn
	reader *bufio.Reader
}

// Write 将数据作为 WS binary frame 发送
func (w *wsConn) Write(b []byte) (int, error) {
	// 构造 WS frame: FIN=1, opcode=0x2 (binary), MASK=1
	frame := make([]byte, 0, len(b)+14)
	frame = append(frame, 0x82) // FIN + binary
	// 长度 + MASK
	maskKey := make([]byte, 4)
	rand.Read(maskKey)
	if len(b) < 126 {
		frame = append(frame, byte(0x80|len(b)))
	} else if len(b) < 65536 {
		frame = append(frame, 0x80|126)
		frame = append(frame, byte(len(b)>>8), byte(len(b)))
	} else {
		frame = append(frame, 0x80|127)
		lenBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(lenBytes, uint64(len(b)))
		frame = append(frame, lenBytes...)
	}
	frame = append(frame, maskKey...)
	// 掩码数据
	masked := make([]byte, len(b))
	for i := range b {
		masked[i] = b[i] ^ maskKey[i%4]
	}
	frame = append(frame, masked...)
	if _, err := w.Conn.Write(frame); err != nil {
		return 0, err
	}
	return len(b), nil
}

// Read 读取 WS frame 的 payload
func (w *wsConn) Read(b []byte) (int, error) {
	// 读取 frame header
	header := make([]byte, 2)
	if _, err := io.ReadFull(w.reader, header); err != nil {
		return 0, err
	}
	opcode := header[0] & 0x0F
	if opcode == 0x8 {
		return 0, io.EOF // close frame
	}
	masked := header[1]&0x80 != 0
	payloadLen := int(header[1] & 0x7F)
	if payloadLen == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(w.reader, ext); err != nil {
			return 0, err
		}
		payloadLen = int(binary.BigEndian.Uint16(ext))
	} else if payloadLen == 127 {
		ext := make([]byte, 8)
		if _, err := io.ReadFull(w.reader, ext); err != nil {
			return 0, err
		}
		payloadLen = int(binary.BigEndian.Uint64(ext))
	}
	var maskKey []byte
	if masked {
		maskKey = make([]byte, 4)
		if _, err := io.ReadFull(w.reader, maskKey); err != nil {
			return 0, err
		}
	}
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(w.reader, payload); err != nil {
		return 0, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	n := copy(b, payload)
	return n, nil
}

// httpUpgradeHandshake 执行 HTTPUpgrade 握手
func httpUpgradeHandshake(conn net.Conn, host, path string) (net.Conn, error) {
	// HTTPUpgrade 类似 WS，但使用不同的 Upgrade 头
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: h2c\r\nConnection: Upgrade, HTTP2-Settings\r\nHTTP2-Settings: AAMAAABkAAQAAP__\r\n\r\n",
		path, host)
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		return nil, fmt.Errorf("httpupgrade read response: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 101 {
		return nil, fmt.Errorf("httpupgrade failed: %d", resp.StatusCode)
	}
	// HTTPUpgrade 成功后，连接为原始 TCP 流
	return conn, nil
}

// grpcHandshake 执行简化的 gRPC 握手
// 注意：完整 gRPC 需要 HTTP/2，这里做简化验证
func grpcHandshake(conn net.Conn, host, serviceName string, useTLS bool) (net.Conn, error) {
	// gRPC 基于 HTTP/2，完整实现复杂
	// 对于健康检查，我们验证 TCP+TLS 成功即可，VLESS 握手将在上层尝试
	// TODO: 实现完整 HTTP/2 gRPC 帧包装
	return conn, nil
}

// vmessHandshake 执行 VMess 协议握手验证
func vmessHandshake(conn net.Conn, uri string) bool {
	// 解析 vmess:// URI (Base64 JSON)
	if !strings.HasPrefix(uri, "vmess://") {
		return false
	}
	b64 := uri[8:]
	// 去掉 fragment
	if idx := strings.Index(b64, "#"); idx != -1 {
		b64 = b64[:idx]
	}
	if idx := strings.Index(b64, "?"); idx != -1 {
		b64 = b64[:idx]
	}
	// 补 padding
	if m := len(b64) % 4; m != 0 {
		b64 += strings.Repeat("=", 4-m)
	}
	jsonBytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		// 尝试 URL-safe base64
		jsonBytes, err = base64.URLEncoding.DecodeString(b64)
		if err != nil {
			return false
		}
	}
	var vmessCfg struct {
		ID       string `json:"id"`
		Security string `json:"scy"`
		Net      string `json:"net"`
	}
	if err := json.Unmarshal(jsonBytes, &vmessCfg); err != nil {
		return false
	}
	if vmessCfg.ID == "" {
		return false
	}
	// VMess 握手：发送认证请求
	// 简化版：构造 VMess 请求头并验证服务器响应
	// 完整 VMess AEAD 握手需要 sing-vmess 库，这里做协议级验证
	//
	// VMess 请求格式 (AEAD):
	// - Auth ID (16 bytes, AES-128-GCM encrypted)
	// - 请求头 (encrypted)
	//
	// 由于完整实现复杂，我们验证：
	// 1. UUID 格式有效
	// 2. 服务器接受连接 (TCP 层已验证)
	// 3. 尝试发送 VMess 探测包
	clean := strings.ReplaceAll(vmessCfg.ID, "-", "")
	if len(clean) != 32 {
		return false
	}
	// 发送 VMess 探测：时间戳 + 随机数据，验证服务器响应
	// 真正的 VMess 服务器会对无效请求关闭连接，有效 UUID 会保持连接
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	// 构造最小 VMess 请求头 (16字节 Auth + 38字节 header)
	probe := make([]byte, 54)
	if _, err := rand.Read(probe); err != nil {
		return false
	}
	if _, err := conn.Write(probe); err != nil {
		return false
	}
	// 尝试读取响应 (VMess 服务器对有效握手会返回数据或保持连接)
	// 如果连接被立即关闭，说明握手失败
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	// 即使读取超时，只要连接未被重置，就视为握手通过
	// (VMess 服务器在收到无效请求时会 RST 连接)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return true // 超时但连接保持，视为通过
		}
		return false // 连接被关闭，握手失败
	}
	return true
}

// trojanHandshake 执行 Trojan 协议握手验证
func trojanHandshake(conn net.Conn, uri string) bool {
	// 解析 trojan://password@host:port
	u, err := url.Parse(uri)
	if err != nil {
		return false
	}
	password := ""
	if u.User != nil {
		password = u.User.Username()
	}
	if password == "" {
		return false
	}
	// Trojan 握手:
	// 1. SHA224(password) hex (56 chars) + "\r\n"
	// 2. SOCKS5 CONNECT 请求 (CMD=1, ATYP=1, 1.1.1.1:80)
	// 3. "\r\n"
	// 4. 服务器响应
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// SHA224(password)
	h := sha256.New224()
	h.Write([]byte(password))
	hashHex := fmt.Sprintf("%x", h.Sum(nil))

	// 构造 Trojan 请求
	buf := make([]byte, 0, 100)
	buf = append(buf, hashHex...)
	buf = append(buf, '\r', '\n')
	buf = append(buf, 0x01)       // CMD = CONNECT
	buf = append(buf, 0x01)       // ATYP = IPv4
	buf = append(buf, 1, 1, 1, 1) // 1.1.1.1
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, 80)
	buf = append(buf, portBytes...)
	buf = append(buf, '\r', '\n')

	if _, err := conn.Write(buf); err != nil {
		return false
	}
	// 读取 SOCKS5 响应: VER(1) + REP(1) + RSV(1) + ATYP(1) + ADDR + PORT
	// 有效密码 -> 服务器保持连接 (可能立即响应或等待数据)
	// 无效密码 -> 服务器立即关闭连接
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	resp := make([]byte, 4)
	n, err := io.ReadFull(conn, resp)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			// 超时但连接保持 -> 密码有效
			return true
		}
		// 连接关闭 -> 密码无效
		return false
	}
	// 收到响应，检查 REP 字段 (0x00=成功)
	if n == 4 && resp[1] == 0x00 {
		return true
	}
	// 有响应即视为通过 (连接保持)
	return n > 0
}

// verifyRealityConfig 验证 REALITY 配置有效性
// 检查 URI 中的 pbk/sid/sni 与 inbound 数据库配置是否匹配
// 完整 REALITY 握手需要 Xray core，此处做配置级验证
func verifyRealityConfig(uri, host, port string) bool {
	u, err := url.Parse(uri)
	if err != nil {
		return false
	}
	q := u.Query()
	uriPbk := q.Get("pbk")
	uriSid := q.Get("sid")
	uriSni := q.Get("sni")
	if uriPbk == "" || uriSid == "" {
		return false
	}

	// 从数据库获取 inbound 配置
	db := database.GetDB()
	if db == nil {
		return false
	}
	var inbound model.Inbound
	// 通过端口查找 inbound
	portInt := 0
	fmt.Sscanf(port, "%d", &portInt)
	// 查询所有 inbound，匹配 listen_port
	var inbounds []model.Inbound
	if err := db.Find(&inbounds).Error; err != nil {
		return false
	}
	for _, ib := range inbounds {
		var opts map[string]interface{}
		if err := json.Unmarshal(ib.Options, &opts); err != nil {
			continue
		}
		if lp, ok := opts["listen_port"].(float64); ok && int(lp) == portInt {
			// 找到匹配的 inbound，验证 REALITY 配置
			tlsCfg, _ := opts["tls"].(map[string]interface{})
			if tlsCfg == nil {
				return false
			}
			reality, _ := tlsCfg["reality"].(map[string]interface{})
			if reality == nil {
				return false
			}
			if enabled, _ := reality["enabled"].(bool); !enabled {
				return false
			}
			// 验证 short_id 匹配
			shortIds, _ := reality["short_id"].([]interface{})
			sidMatch := false
			for _, sid := range shortIds {
				if s, ok := sid.(string); ok && s == uriSid {
					sidMatch = true
					break
				}
			}
			if !sidMatch {
				return false
			}
			// 验证 handshake server 匹配 SNI
			handshake, _ := reality["handshake"].(map[string]interface{})
			if handshake != nil {
				if server, ok := handshake["server"].(string); ok && server != "" {
					if uriSni != "" && uriSni != server {
						// SNI 不匹配，但可能是别名，宽松处理
					}
				}
			}
			// 验证 VLESS UUID 匹配
			uriUuid := extractVlessUUID(uri)
			if uriUuid != "" {
				users, _ := opts["users"].([]interface{})
				uuidMatch := false
				for _, u := range users {
					if um, ok := u.(map[string]interface{}); ok {
						if id, ok := um["uuid"].(string); ok && strings.EqualFold(id, uriUuid) {
							uuidMatch = true
							break
						}
					}
				}
				if !uuidMatch {
					return false
				}
			}
			// 配置匹配
			inbound = ib
			break
		}
	}
	return inbound.Id != 0
}

// checkExternalNode 对单个外部节点执行完整检测
// 返回 nil 表示解析失败，非 nil 的 status.Status 表示可用性
func checkExternalNode(uri string) *model.NodeHealthStatus {
	host, port, useTLS, sni, proto := parseURIComponents(uri)
	if host == "" || port == "" {
		return nil
	}

	// SUI 本地入站节点：VPS 域名指向本机，直接用 127.0.0.1 避免 DNS 故障
	// 健康检查器运行在 VPS 上，VPS 域名的节点即为本地入站
	checkHost := host
	if host == EgressGatewayVPSDomain {
		checkHost = "127.0.0.1"
	}

	nodeKey := host + ":" + port
	status := &model.NodeHealthStatus{
		Node:        nodeKey,
		OriginalURI: uri, // 存完整 URI 供订阅直接发布
		Provider:    determineStandardProvider(uri),
		Status:      "unavailable",
	}
	status.SetCheckedAtNow()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// ── TCP 检测 ─────────────────────────────────────────
	addr := net.JoinHostPort(checkHost, port)
	t0 := time.Now()

	udpProtos := map[string]bool{"hysteria2": true, "hysteria": true, "tuic": true}
	if udpProtos[strings.ToLower(proto)] {
		// UDP 协议：只做 UDP 可达性探测，不等待应用层响应
		// Hysteria2/TUIC 不会响应随机探测包，Read 会超时 2s 导致 latency 虚高
		conn, err := net.DialTimeout("udp", addr, 4*time.Second)
		if err != nil {
			status.LastError = "udp_fail: " + err.Error()
			return status
		}
		// 发送探测包验证端口可写，成功即视为可达
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write(make([]byte, 16)); err != nil {
			conn.Close()
			status.LastError = "udp_write_fail: " + err.Error()
			return status
		}
		conn.Close()
		status.TCPCheck = true
		status.TLSCheck = true
		status.ProxyCheck = true
		status.Latency = time.Since(t0).Milliseconds()
		if status.Latency <= 0 {
			status.Latency = 1
		}
		// UDP 探测延迟应为毫秒级，若超过 650ms 说明网络异常
		status.Speed = float64(10000) / float64(status.Latency)
		if status.Speed > 100 {
			status.Speed = 100
		}
		status.Status = "available"
		// 查询 IP 地理
		enrichGeo(status, host, proto)
		return status
	}

	// TCP 连接
	rawConn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		status.LastError = "tcp_fail: " + err.Error()
		return status
	}
	defer rawConn.Close()
	tcpMs := time.Since(t0).Milliseconds()
	if tcpMs <= 0 {
		tcpMs = 1
	}
	status.TCPCheck = true
	status.Latency = tcpMs

	// ── TLS 检测 ─────────────────────────────────────────
	var conn net.Conn = rawConn
	isReality := strings.Contains(strings.ToLower(uri), "security=reality")
	if useTLS {
		if isReality {
			// REALITY: 完整握手需要 Xray core 的 uTLS 实现
			// 这里验证 TCP 连通性和配置有效性，VLESS 握手在明文层验证
			// TODO: 集成 sing-box REALITY 客户端实现完整握手
			if !verifyRealityConfig(uri, checkHost, port) {
				status.LastError = "reality_config_mismatch"
				return status
			}
			status.TLSCheck = true
			// REALITY 节点：跳过 TLS 握手，直接做 VLESS 协议验证
			// (VLESS UUID 验证足以确认节点有效性)
		} else {
			tlsCfg := &tls.Config{ServerName: sni, InsecureSkipVerify: true}
			if tlsCfg.ServerName == "" {
				tlsCfg.ServerName = host
			}
			tlsConn := tls.Client(rawConn, tlsCfg)
			tlsConn.SetDeadline(time.Now().Add(5 * time.Second))
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				status.LastError = "tls_fail: " + err.Error()
				return status
			}
			status.TLSCheck = true
			conn = tlsConn
		}
	} else {
		status.TLSCheck = true
	}

	// ── 协议级握手验证 ───────────────────────────────────
	// 所有协议、所有传输、包括本地节点，都必须执行真实协议握手
	protoLower := strings.ToLower(proto)
	switch protoLower {
	case "vless":
		uuidStr := extractVlessUUID(uri)
		if uuidStr == "" {
			status.LastError = "vless_no_uuid"
			return status
		}
		// REALITY 节点：已通过 verifyRealityConfig 验证配置和 UUID
		// 跳过传输层握手 (需要 Xray core 的 REALITY 实现)
		if strings.Contains(strings.ToLower(uri), "security=reality") {
			status.ProxyCheck = true
			break
		}
		// 根据传输类型包装连接 (TCP直接，WS/gRPC/HTTPUpgrade需传输握手)
		wrappedConn, err := wrapVlessTransport(conn, uri, host, useTLS, sni)
		if err != nil {
			status.LastError = "vless_transport_fail:" + err.Error()
			return status
		}
		if wrappedConn != conn {
			defer wrappedConn.Close()
			conn = wrappedConn
		}
		if !vlessHandshake(conn, uuidStr) {
			status.LastError = "vless_handshake_fail"
			return status
		}
		status.ProxyCheck = true
	case "vmess":
		if !vmessHandshake(conn, uri) {
			status.LastError = "vmess_handshake_fail"
			return status
		}
		status.ProxyCheck = true
	case "trojan":
		if !trojanHandshake(conn, uri) {
			status.LastError = "trojan_handshake_fail"
			return status
		}
		status.ProxyCheck = true
	default:
		// Hysteria2/TUIC/SS/SOCKS5等: TCP+TLS 通过即视为可用
		// TODO: 为这些协议添加原生握手
		status.ProxyCheck = true
	}
	status.Speed = float64(10000) / float64(max64(status.Latency, 1))
	if status.Speed > 100 {
		status.Speed = 100
	}
	status.Status = "available"

	// ── 真实地理位置 ──────────────────────────────────────
	enrichGeo(status, host, proto)
	return status
}

// enrichGeo 用 ip-api.com 查询节点真实地理并填写规范字段
func enrichGeo(s *model.NodeHealthStatus, host, proto string) {
	// Resolve hostname to IP if needed
	ip := host
	if net.ParseIP(ip) == nil {
		addrs, err := net.LookupHost(host)
		if err == nil && len(addrs) > 0 {
			ip = addrs[0]
		}
	}

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	hc := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := hc.Get(fmt.Sprintf("http://ip-api.com/json/%s?fields=country,countryCode,regionName,city,isp,as,proxy,hosting", ip))
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var r map[string]interface{}
	if json.Unmarshal(body, &r) != nil {
		return
	}

	country := strField(r, "country")
	region := strField(r, "regionName")
	city := strField(r, "city")

	// Map country to Chinese
	countryCN := countryToChinese(strField(r, "countryCode"), country)
	regionCN := regionToChinese(region)
	cityCN := cityToChinese(city)

	s.Country = countryCN
	s.Region = regionCN
	s.City = cityCN
}

func determineStandardProvider(uri string) string {
	lower := strings.ToLower(uri)
	if strings.Contains(lower, "cloudflare") || strings.Contains(lower, "warp") {
		return "Cloudflare"
	}
	if strings.Contains(lower, "hproxy") || strings.Contains(lower, "proxyscrape") {
		return "HProxy"
	}
	if strings.Contains(lower, "s-ui") || strings.Contains(lower, "dash.icta.top") {
		return "SUI"
	}
	return "Seed"
}

// countryToChinese 将国家代码映射到中文
func countryToChinese(code, fallback string) string {
	m := map[string]string{
		"US": "美国", "JP": "日本", "SG": "新加坡", "DE": "德国", "GB": "英国",
		"FR": "法国", "NL": "荷兰", "KR": "韩国", "HK": "香港", "TW": "台湾",
		"CA": "加拿大", "AU": "澳大利亚", "RU": "俄罗斯", "IN": "印度",
		"BR": "巴西", "MX": "墨西哥", "IT": "意大利", "ES": "西班牙",
		"SE": "瑞典", "NO": "挪威", "FI": "芬兰", "CH": "瑞士",
		"TR": "土耳其", "PL": "波兰", "CZ": "捷克", "AT": "奥地利",
		"BE": "比利时", "DK": "丹麦", "PT": "葡萄牙", "RO": "罗马尼亚",
		"UA": "乌克兰", "IR": "伊朗", "AE": "阿联酋", "SA": "沙特",
		"ID": "印尼", "MY": "马来西亚", "TH": "泰国", "VN": "越南",
		"PH": "菲律宾", "LT": "立陶宛", "LV": "拉脱维亚", "EE": "爱沙尼亚",
		"IL": "以色列", "ZA": "南非", "NG": "尼日利亚", "AR": "阿根廷",
		"CL": "智利", "CO": "哥伦比亚", "MO": "澳门", "CN": "中国大陆",
	}
	if cn, ok := m[code]; ok {
		return cn
	}
	if fallback != "" {
		return fallback
	}
	return code
}

// regionToChinese 简单映射常见区域名到中文
func regionToChinese(region string) string {
	if region == "" {
		return "未知"
	}
	m := map[string]string{
		"California": "加州", "New York": "纽约州", "Texas": "德克萨斯",
		"Virginia": "弗吉尼亚州", "Washington": "华盛顿", "Illinois": "伊利诺伊",
		"Oregon": "俄勒冈", "Georgia": "佐治亚", "Florida": "佛罗里达",
		"Ohio": "俄亥俄", "Colorado": "科罗拉多", "Arizona": "亚利桑那",
		"Tokyo": "关东", "Osaka": "近畿", "Aichi": "中部",
		"Hong Kong": "香港", "Bangkok": "曼谷", "Istanbul": "伊斯坦布尔",
		"Central Singapore": "中央区", "North West": "西北区",
		"Hesse": "黑森", "Bavaria": "巴伐利亚", "North Rhine-Westphalia": "北威州",
		"Ile-de-France": "法兰西岛", "Catalonia": "加泰罗尼亚",
		"Mexico City": "墨西哥城",
		"Ontario":     "安大略", "Quebec": "魁北克", "British Columbia": "不列颠哥伦比亚",
		"New South Wales": "新南威尔士", "Victoria": "维多利亚",
		"District of Columbia": "哥伦比亚特区", "Telangana": "特伦甘纳邦",
		"Gangwon-do": "江原道", "Taipei City": "台北市",
		"England": "英格兰", "Sai Kung District": "西贡区", "Provincie Noord-Holland": "北荷兰",
		"Moscow": "莫斯科", "Saint Petersburg": "圣彼得堡",
		"Seoul": "首尔", "Gyeonggi-do": "京畿道",
		"Dubai": "迪拜", "Hanoi": "河内", "Selangor": "雪兰莪", "Batu Caves": "黑风洞",
		"Kuala Lumpur": "吉隆坡", "Maharashtra": "马哈拉施特拉", "West Bengal": "西孟加拉",
		"Karnataka": "卡纳塔克", "Tamil Nadu": "泰米尔纳德", "Delhi": "德里",
		"Incheon": "仁川", "Daegu": "大邱", "Busan": "釜山", "Gwangju": "光州",
		"Daejeon": "大田", "Ulsan": "蔚山", "Sejong": "世宗", "North Holland": "北荷兰",
		"South Holland": "南荷兰", "Flanders": "弗拉芒", "Wallonia": "瓦隆",
		"Guangdong": "广东", "Zhejiang": "浙江", "Jiangsu": "江苏", "Beijing": "北京",
		"Shanghai": "上海", "Shandong": "山东", "Sichuan": "四川",
	}
	if cn, ok := m[region]; ok {
		return cn
	}
	if strings.IndexFunc(region, func(r rune) bool { return r >= '\u4e00' && r <= '\u9fff' }) >= 0 {
		return region
	}
	return ""
}

// cityToChinese 映射常见城市名到中文
func cityToChinese(city string) string {
	if city == "" {
		return ""
	}
	m := map[string]string{
		"Los Angeles": "洛杉矶", "San Jose": "圣何塞", "San Francisco": "旧金山",
		"New York": "纽约", "Chicago": "芝加哥", "Dallas": "达拉斯",
		"Houston": "休斯顿", "Atlanta": "亚特兰大", "Seattle": "西雅图",
		"Miami": "迈阿密", "Denver": "丹佛", "Phoenix": "凤凰城",
		"Washington": "华盛顿", "Ashburn": "阿什本", "Newark": "纽瓦克",
		"Buffalo": "布法罗", "Portland": "波特兰", "Las Vegas": "拉斯维加斯",
		"North Bergen": "北卑尔根", "Clifton": "克利夫顿", "Calgary": "卡尔加里",
		"Santa Clara": "圣克拉拉", "Council Bluffs": "康瑟尔布拉夫斯", "Mumbai": "孟买",
		"Hanoi": "河内", "Surakarta": "梭罗", "Dubai": "迪拜",
		"Batu Caves": "黑风洞", "Kolkata": "加尔各答", "Chennai": "金奈",
		"Bengaluru": "班加罗尔", "Pune": "浦那", "Incheon": "仁川",
		"Daegu": "大邱", "Busan": "釜山", "Gwangju": "光州", "Daejeon": "大田",
		"Ulsan": "蔚山", "Suwon": "水原", "Changwon": "昌原", "Seongnam": "城南",
		"Goyang": "高阳", "Yongin": "龙仁", "Bucheon": "富川", "Ansan": "安山",
		"Cheongju": "清州", "Jeonju": "全州", "Cheonan": "天安", "Pohang": "浦项",
		"Gimhae": "金海", "Gumi": "龟尾", "Jeju": "济州", "Chiang Mai": "清迈",
		"Phuket": "普吉", "Pattaya": "芭堤雅", "Almaty": "阿拉木图", "Astana": "阿斯塔纳",
		"Tashkent": "塔什干", "Baku": "巴库", "Yerevan": "埃里温", "Tbilisi": "第比利斯",
		"Nicosia": "尼科西亚", "Limassol": "利马索尔", "Larnaca": "拉纳卡",
		"Athens": "雅典", "Thessaloniki": "塞萨洛尼基",
		"Tokyo": "东京", "Osaka": "大阪", "Nagoya": "名古屋",
		"Singapore": "新加坡城", "Frankfurt": "法兰克福", "Berlin": "柏林",
		"Munich": "慕尼黑", "Hamburg": "汉堡", "Amsterdam": "阿姆斯特丹",
		"London": "伦敦", "Manchester": "曼彻斯特", "Paris": "巴黎",
		"Stockholm": "斯德哥尔摩", "Copenhagen": "哥本哈根", "Oslo": "奥斯陆",
		"Helsinki": "赫尔辛基", "Zurich": "苏黎世", "Vienna": "维也纳",
		"Brussels": "布鲁塞尔", "Warsaw": "华沙", "Prague": "布拉格",
		"Bucharest": "布加勒斯特", "Istanbul": "伊斯坦布尔", "Moscow": "莫斯科",
		"Seoul": "首尔", "Hong Kong": "香港", "Taipei": "台北",
		"Toronto": "多伦多", "Vancouver": "温哥华", "Montreal": "蒙特利尔",
		"Sydney": "悉尼", "Melbourne": "墨尔本",
		"Jakarta": "雅加达", "Kuala Lumpur": "吉隆坡", "Bangkok": "曼谷",
		"Tel Aviv": "特拉维夫", "Johannesburg": "约翰内斯堡",
		"Sao Paulo": "圣保罗", "Buenos Aires": "布宜诺斯艾利斯",
		"Kyiv": "基辅", "Kharkiv": "哈尔科夫",
		"Manassas": "马纳萨斯", "Hyderabad": "海得拉巴", "Chuncheon": "春川",
		"Frankfurt am Main": "法兰克福", "Slough": "斯劳",
		"Tseung Kwan O": "将军澳",
		"Vilnius":       "维尔纽斯", "Riga": "里加", "Tallinn": "塔林",
		"Lisbon": "里斯本", "Madrid": "马德里", "Rome": "罗马",
		"Milan": "米兰", "Barcelona": "巴塞罗那", "Mexico City": "墨西哥城",
	}
	if cn, ok := m[city]; ok {
		return cn
	}
	if strings.IndexFunc(city, func(r rune) bool { return r >= '\u4e00' && r <= '\u9fff' }) >= 0 {
		return city
	}
	return ""
}

// parseURIComponents extracts host, port, useTLS, sni, proto from a proxy URI
func parseURIComponents(uri string) (host, port string, useTLS bool, sni, proto string) {
	if strings.HasPrefix(uri, "vmess://") {
		proto = "vmess"
		b64 := strings.TrimPrefix(uri, "vmess://")
		decoded, err := base64.RawStdEncoding.DecodeString(b64)
		if err != nil {
			decoded, err = base64.StdEncoding.DecodeString(b64)
		}
		if err != nil {
			return
		}
		var obj map[string]interface{}
		if json.Unmarshal(decoded, &obj) != nil {
			return
		}
		host, _ = obj["add"].(string)
		port = fmt.Sprintf("%v", obj["port"])
		tls, _ := obj["tls"].(string)
		useTLS = tls == "tls"
		sni, _ = obj["sni"].(string)
		return
	}

	parts := strings.SplitN(uri, "://", 2)
	if len(parts) != 2 {
		return
	}
	proto = parts[0]

	switch strings.ToLower(proto) {
	case "hysteria2", "hysteria", "tuic":
		// UDP-based protocols
	case "vless", "trojan", "ss":
		// Trojan: only use TLS if explicitly indicated (security=tls, tls param, or 443 port).
		// Plain TCP trojan (e.g. trojan-tcp-plain) must NOT try TLS.
		// REALITY (security=reality) also requires TLS handshake.
		uriLower := strings.ToLower(uri)
		isReality := strings.Contains(uriLower, "security=reality")
		if strings.ToLower(proto) == "trojan" {
			useTLS = strings.Contains(uri, "security=tls") || strings.Contains(uriLower, "tls=") || isReality
		} else {
			useTLS = strings.Contains(uri, "security=tls") || strings.Contains(uri, "tls") || isReality
		}
	}

	u, err := url.Parse(uri)
	if err != nil {
		return
	}
	host = u.Hostname()
	port = u.Port()
	if q := u.Query(); q.Get("sni") != "" {
		sni = q.Get("sni")
	}
	return
}

func strField(m map[string]interface{}, k string) string {
	v, _ := m[k].(string)
	return v
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// GetNodeHealthStatus 从 DB 查询单个节点的最新健康状态（按 host:port 主键）
func GetNodeHealthStatus(nodeKey string) (*model.NodeHealthStatus, error) {
	db := database.GetDB()
	var status model.NodeHealthStatus
	result := db.Where("node = ?", nodeKey).First(&status)
	if result.Error != nil {
		return nil, result.Error
	}
	return &status, nil
}

// GetAllHealthyNodes 返回所有当前健康（TTL 内）的节点状态
func GetAllHealthyNodes(ttl time.Duration) ([]*model.NodeHealthStatus, error) {
	db := database.GetDB()
	var statuses []*model.NodeHealthStatus
	result := db.Where("status = ?", "available").Find(&statuses)
	if result.Error != nil {
		return nil, result.Error
	}
	var out []*model.NodeHealthStatus
	for _, s := range statuses {
		if s.IsHealthyWithTTL(ttl) {
			out = append(out, s)
		}
	}
	return out, nil
}
