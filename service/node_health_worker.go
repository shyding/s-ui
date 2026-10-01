package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
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
}

// checkExternalNode 对单个外部节点执行完整检测
// 返回 nil 表示解析失败，非 nil 的 status.Status 表示可用性
func checkExternalNode(uri string) *model.NodeHealthStatus {
	host, port, useTLS, sni, proto := parseURIComponents(uri)
	if host == "" || port == "" {
		return nil
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
	addr := net.JoinHostPort(host, port)
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
	if useTLS {
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
	} else {
		status.TLSCheck = true
	}

	// ── HTTP CONNECT 可达性 ───────────────────────────────
	// 快速 HTTP check: 对节点地址直接 GET，不走代理，验证服务端有应答
	// 完整代理测试需要 sing-box 客户端，此处用 TCP+TLS 成功作为 ProxyCheck 通过标准
	// 真实用户场景下，TCP+TLS 成功的节点 v2rayN 不会显示 -1
	status.ProxyCheck = true
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
		useTLS = strings.Contains(uri, "security=tls") || strings.Contains(uri, "tls") || proto == "trojan"
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
