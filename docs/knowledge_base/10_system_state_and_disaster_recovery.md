# 10. S-UI 全系统状态快照与灾难恢复手册 (System State Snapshot & Disaster Recovery)

> **最后更新**: 2026-09-29T23:28:00+08:00
> **目的**: 当 VPS 崩溃/重装/数据丢失时，凭此文档可在 30 分钟内从零完整恢复全部服务与数据。

---

## 一、基础设施与连接信息

| 项目 | 值 |
|------|-----|
| **VPS 提供商** | 腾讯云 (新加坡) |
| **VPS IP** | `124.156.207.253` |
| **SSH 用户** | `ubuntu` |
| **SSH 密钥** | `i:\learn_code\s-ui\scratch\deploy_key` (ed25519) |
| **SSH 命令** | `ssh -i i:\learn_code\s-ui\scratch\deploy_key -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL ubuntu@124.156.207.253` |
| **域名** | `dash.icta.top` → 124.156.207.253 (Cloudflare CDN, SSL Full) |
| **管理面板** | `https://dash.icta.top:2053/app/` (admin/admin) |
| **订阅端口** | `https://dash.icta.top:2096/sub/my` |
| **客户端入站端口** | `54142` (VLESS+TCP+TLS) |
| **GitHub 仓库** | `https://github.com/shyding/s-ui` branch `main` |
| **CI/CD** | GitHub Actions → push main → go test → build → SCP → bootstrap |

---

## 二、VPS 目录结构与关键文件

```
/usr/local/s-ui/
├── sui                          # Go 二进制主程序
├── db/s-ui.db                   # SQLite 数据库（核心！所有配置、节点、健康数据）
├── certs/
│   ├── fullchain.pem            # Let's Encrypt TLS 证书 (到期 2026-12-28)
│   └── privkey.pem              # TLS 私钥
├── bin/                         # sing-box 等辅助二进制
└── scripts/                     # Python 辅助脚本

/etc/s-ui/
└── seed_nodes.txt               # 108 个用户提供的 VLESS Reality 种子节点

/etc/systemd/system/
└── s-ui.service                 # systemd 服务单元
```

---

## 三、数据库关键表与当前数据快照

### 3.1 settings 表
```sql
-- 关键配置
webPort = 2053
subPort = 2096
webCertFile = /usr/local/s-ui/certs/fullchain.pem
webKeyFile = /usr/local/s-ui/certs/privkey.pem
subCertFile = /usr/local/s-ui/certs/fullchain.pem
subKeyFile = /usr/local/s-ui/certs/privkey.pem
healthCheckTime = 03:30
subEncode = true
secret = zwMhY5O7vBwKCASbOTUc5CB6GDCMWzlQ
```

### 3.2 users 表 (管理员)
```sql
INSERT INTO users VALUES(1,'admin','admin','2026-09-29 20:24:06 127.0.0.1');
```

### 3.3 inbounds 表 (当前仅 1 个入站！需扩充至 20+)
```sql
-- 当前唯一入站: VLESS + TCP + TLS on port 54142
INSERT INTO inbounds VALUES(1,'vless','vless-54142',NULL,
  '[]',  -- addrs (空，使用 options 中的 listen)
  '{}',  -- out_json (空)
  '{"listen":"::","listen_port":54142,"tcp_fast_open":true,"sniff":true,
    "sniff_override_destination":false,
    "tls":{"enabled":true,"server_name":"dash.icta.top",
           "certificate_path":"/usr/local/s-ui/certs/fullchain.pem",
           "key_path":"/usr/local/s-ui/certs/privkey.pem"}}'
);
-- ⚠️ 缺陷：仅有 1 个 VLESS 入站，导致 SUI 来源只有 1 个节点
-- 需要补充: VMess, Trojan, Hysteria2, TUIC, Shadowsocks, WS/gRPC/HTTPUpgrade 等组合
```

### 3.4 clients 表 (订阅用户)
```sql
-- 客户端 "my"（v2rayNG 订阅名 mynewicta）
INSERT INTO clients VALUES(1, 1, 'my',
  '{"vless": {"uuid": "8c9fa6c8-bf77-4533-8b12-563cc157eb2a", "flow": ""}}',
  '[1]',  -- 仅关联 inbound 1
  '[{"type":"local","remark":"vless-54142","uri":"vless://8c9fa6c8-bf77-4533-8b12-563cc157eb2a@dash.icta.top:54142?security=tls&sni=dash.icta.top&type=tcp#vless-54142"}]',
  0, 0, 639300, 303154, NULL, NULL
);
-- ⚠️ 缺陷：client config 仅有 vless 凭证，缺少 vmess/trojan/hy2/tuic/ss 等凭证
-- 需要扩充 config 为全协议凭证集合
```

### 3.5 subscriptions 表 (外部订阅源)
```sql
INSERT INTO subscriptions VALUES(1,'HProxy Live Candidates',
  'https://raw.githubusercontent.com/hproxy-com/free-proxy-list/main/live.json',
  1,30,'replace',1790694679,1790673905,853);

INSERT INTO subscriptions VALUES(2,'ProxyScrape Live Candidates',
  'https://cdn.jsdelivr.net/gh/proxyscrape/free-proxy-list@main/proxies/all/data.json',
  1,30,'replace',1790694680,1790673905,342);

INSERT INTO subscriptions VALUES(3,'Local v2rayN Seed Nodes',
  'file:///etc/s-ui/seed_nodes.txt',
  1,1440,'replace',1790674588,NULL,96);
```

### 3.6 node_health_statuses 统计 (2026-09-29 快照)
| Provider | Status | Count | Avg Latency (ms) |
|----------|--------|-------|-------------------|
| Seed | available | 172 | 132.8 |
| HProxy | available | 282 | 243.7 |
| Cloudflare | available | 5 | 182.8 |
| SUI | available | 2 | 97.5 |
| Seed | unavailable | 8 | 1760.8 |
| HProxy | unavailable | 30 | 536.1 |
| Cloudflare | unavailable | 10 | 551.4 |

### 3.7 outbounds 关键统计
| Tag 前缀 | 总数 | 可用数 | 说明 |
|----------|------|--------|------|
| seed-香 | 15 | 15 | 香港种子 |
| seed-日 | 25 | 25 | 日本种子 |
| seed-美 | 34 | 30 | 美国种子 |
| seed-德 | 3 | 3 | 德国种子 |
| seed-台 | 2 | 2 | 台湾种子 |
| seed-韩 | 2 | 2 | 韩国种子 |
| seed-印 | 2 | 2 | 印度种子 |
| seed-英 | 2 | 2 | 英国种子 |
| seed-新 | 1 | 1 | 新加坡种子 |
| seed-泰 | 1 | 1 | 泰国种子 |
| seed-澳 | 1 | 1 | 澳大利亚种子 |
| seed-土 | 1 | 1 | 土耳其种子 |
| seed-荷 | 1 | 1 | 荷兰种子 |
| hproxy | 1195 | 123 | HProxy 隧道代理 |
| cf-sg- | 5 | 3 | Cloudflare WARP |
| cf-us- | 6 | 0 | Cloudflare WARP (US) |
| cf-es- | 2 | 0 | Cloudflare WARP (ES) |

### 3.8 cloudflare_endpoints 统计
| Status | Count |
|--------|-------|
| online | 114 |
| offline | 98 |

---

## 四、当前订阅输出状态 (2026-09-29 22:55 快照)

**总节点数**: 39 个（严重不足！目标 >= 1000）

**各来源分布**:
| 来源 | 节点数 | 备注合规 |
|------|--------|----------|
| SUI | 1 | ✅ `SUI-新加坡-中央区-新加坡城-01` |
| Seed | 25 | ✅ 全中文（如 `Seed-中国-香港-香港-01`） |
| HProxy | 13 | ✅ 全中文（如 `HProxy-日本-东京-东京-01`） |
| Cloudflare | 0 | ⚠️ 无节点 |

**已修复的问题** (Commit `d0bf493`):
- ✅ 备注全中文化（消除了 Hanoi/Dubai/Batu Caves 等英文）
- ✅ 消除了所有"未知"/"unknown" 词汇
- ✅ 来源规范化为 Seed/Cloudflare/HProxy/SUI 四大标准
- ✅ 延迟门槛锁定 ≤ 500ms

**待解决的缺陷**:
1. ❌ SUI 原生来源仅 1 个节点（需 >= 15~20 种协议组合）
2. ❌ 总节点数仅 39 个（需 >= 1000）
3. ❌ 每城市分组限制 TOP3（已改为 SUI=30, 其他=10）
4. ❌ Seed 每城限 3 个 + seedIndex 上限 150（需放开）

---

## 五、Git 提交历史 (最近关键 commits)

```
d0bf493 fix(remark): enforce 100% Chinese geography, zero unknown, and 500ms latency cap
e96edb6 fix(egress): wrap endpoints in direct outbounds and enhance geo localization
8af82ba fix(quality): enforce strict TOP3 quality gate <=650ms, normalize providers
2584da5 fix(sub): strip flow parameter in VLESS links for v2rayN/v2rayNG compatibility
9a97f37 feat(egress): expand WARP Anycast edge IP candidates to reach 100+ verified nodes
```

---

## 六、刚性铁律速查 (Quality & Quantity Rigid Rules)

> 完整版: [SUI_QUALITY_AND_QUANTITY_RULES.md](SUI_QUALITY_AND_QUANTITY_RULES.md)

| 维度 | 刚性指标 | 一票否决红线 |
|------|---------|-------------|
| 总节点数 | >= 1000 | < 1000 |
| SUI 原生 | >= 15~20 种协议组合 | 仅 1 个或 < 10 个 |
| 质量门 | 100% 连通, <= 650ms | 任何 -1, 或 > 650ms |
| 备注语言 | 除来源外 100% 中文/数字 | 出现英文字母 |
| 备注禁词 | 零"未知"/零"unknown" | 出现无意义词汇 |
| 来源白名单 | Seed/Cloudflare/HProxy/SUI | 将协议名当来源 |
| 安全隔离 | 100% 锁定 dash.icta.top | 暴露物理 IP |

---

## 七、灾难恢复完整流程

### 7.1 VPS 重装后恢复步骤
```bash
# Step 1: 部署 SSH 公钥
cd i:\learn_code\s-ui && go run scratch/setup_ssh.go

# Step 2: 更新 GitHub Secret (SERVER_SSH_KEY)
# → https://github.com/shyding/s-ui/settings/secrets/actions

# Step 3: 触发 CI/CD 部署（碰一个 .go 文件后 push）
echo "" >> service/config.go && git add -A && git commit -m "ci: retrigger deploy" && git push

# Step 4: bootstrap.sh 自动处理：安装包、证书、DB、BBR、systemd、防火墙

# Step 5: 恢复种子节点文件
scp /etc/s-ui/seed_nodes.txt ubuntu@VPS:/etc/s-ui/seed_nodes.txt
# 若源文件丢失，从本仓库 scratch/ 目录或用户提供的 108 节点重新生成

# Step 6: 恢复健康检查数据（手动触发一次全量检查）
# POST https://dash.icta.top:2053/api/egressHealthCheck
```

### 7.2 数据库重建 SQL (灾难恢复用)
```sql
-- 恢复管理员
INSERT INTO users VALUES(1,'admin','admin','');

-- 恢复核心设置
UPDATE settings SET value = '2053' WHERE key = 'webPort';
UPDATE settings SET value = '2096' WHERE key = 'subPort';
UPDATE settings SET value = '/usr/local/s-ui/certs/fullchain.pem' WHERE key IN ('webCertFile','subCertFile');
UPDATE settings SET value = '/usr/local/s-ui/certs/privkey.pem' WHERE key IN ('webKeyFile','subKeyFile');
UPDATE settings SET value = '03:30' WHERE key = 'healthCheckTime';
UPDATE settings SET value = 'true' WHERE key = 'subEncode';

-- 恢复基础入站 (VLESS+TCP+TLS)
INSERT INTO inbounds (type, tag, options) VALUES ('vless', 'vless-54142',
  '{"listen":"::","listen_port":54142,"tcp_fast_open":true,"sniff":true,
    "sniff_override_destination":false,
    "tls":{"enabled":true,"server_name":"dash.icta.top",
           "certificate_path":"/usr/local/s-ui/certs/fullchain.pem",
           "key_path":"/usr/local/s-ui/certs/privkey.pem"}}');

-- 恢复客户端
INSERT INTO clients (enable, name, config, inbounds, links, volume, expiry, down, up)
VALUES (1, 'my',
  '{"vless":{"uuid":"8c9fa6c8-bf77-4533-8b12-563cc157eb2a","flow":""}}',
  '[1]',
  '[{"type":"local","remark":"vless-54142","uri":"vless://8c9fa6c8-bf77-4533-8b12-563cc157eb2a@dash.icta.top:54142?security=tls&sni=dash.icta.top&type=tcp#vless-54142"}]',
  0, 0, 0, 0);

-- 恢复外部订阅
INSERT INTO subscriptions (name, url, enabled, update_interval, update_mode)
VALUES ('HProxy Live Candidates', 'https://raw.githubusercontent.com/hproxy-com/free-proxy-list/main/live.json', 1, 30, 'replace');
INSERT INTO subscriptions (name, url, enabled, update_interval, update_mode)
VALUES ('ProxyScrape Live Candidates', 'https://cdn.jsdelivr.net/gh/proxyscrape/free-proxy-list@main/proxies/all/data.json', 1, 30, 'replace');
INSERT INTO subscriptions (name, url, enabled, update_interval, update_mode)
VALUES ('Local v2rayN Seed Nodes', 'file:///etc/s-ui/seed_nodes.txt', 1, 1440, 'replace');
```

### 7.3 种子节点来源 (108 个 VLESS Reality)
- **存储位置**: VPS `/etc/s-ui/seed_nodes.txt`
- **备份位置**: 用户对话中提供（覆盖 13 国：新加坡、泰国、香港、台湾、韩国、日本、美国、德国、英国、荷兰、澳大利亚、土耳其、印度）
- **协议**: 全部为 VLESS + TCP + Reality (xtls-rprx-vision)
- **UUID**: `bada0879-b06e-4d84-b3ff-2b25fc698035` (所有节点共用)
- **SNI**: `addons.mozilla.org`

---

## 八、待实施攻坚任务清单

### 8.1 补齐 SUI 多协议组合 (优先级 P0)
**目标**: 从 1 个入站扩充至 20 个入站（覆盖全协议栈）

| 编号 | 入站类型 | 端口 | 传输 | TLS |
|------|---------|------|------|-----|
| 1 | VLESS | 54142 | TCP | ✅ (已有) |
| 2 | VLESS | 54143 | WebSocket | ✅ |
| 3 | VLESS | 54144 | gRPC | ✅ |
| 4 | VLESS | 54145 | HTTPUpgrade | ✅ |
| 5 | VMess | 54146 | TCP | ✅ |
| 6 | VMess | 54147 | WebSocket | ✅ |
| 7 | VMess | 54148 | gRPC | ✅ |
| 8 | VMess | 54149 | HTTPUpgrade | ✅ |
| 9 | Trojan | 54150 | TCP | ✅ |
| 10 | Trojan | 54151 | WebSocket | ✅ |
| 11 | Trojan | 54152 | gRPC | ✅ |
| 12 | Trojan | 54153 | HTTPUpgrade | ✅ |
| 13 | Hysteria2 | 40734 | QUIC(UDP) | ✅ |
| 14 | TUIC v5 | 40735 | QUIC(UDP) | ✅ |
| 15 | Shadowsocks 2022 | 54154 | TCP | ❌ |
| 16 | Shadowsocks AEAD | 54155 | TCP | ❌ |
| 17 | Mixed (SOCKS5+HTTP) | 54156 | TCP | ❌ |
| 18 | HTTP Proxy | 54157 | TCP | ✅ |
| 19 | VLESS | 54158 | TCP+Reality | ✅ |
| 20 | Trojan | 54159 | gRPC | ✅ |

**客户端 config 需扩充为**:
```json
{
  "vless": {"uuid": "8c9fa6c8-bf77-4533-8b12-563cc157eb2a", "flow": ""},
  "vmess": {"uuid": "8c9fa6c8-bf77-4533-8b12-563cc157eb2a"},
  "trojan": {"password": "icta-trojan-2026"},
  "hysteria2": {"password": "icta-hy2-2026"},
  "tuic": {"uuid": "8c9fa6c8-bf77-4533-8b12-563cc157eb2a", "password": "icta-tuic-2026"},
  "shadowsocks": {"password": "icta-ss-aead-2026"},
  "shadowsocks16": {"password": "aWN0YS1zczIwMjItMjAyNg=="},
  "socks": {"username": "my", "password": "icta-socks-2026"},
  "http": {"username": "my", "password": "icta-http-2026"}
}
```

### 8.2 代码层限制解除 (优先级 P0)
| 文件 | 改动 | 状态 |
|------|------|------|
| `sub/linkService.go:356` | TOP3 → SUI=30, 其他=10 | ✅ 已完成 |
| `sub/linkService.go:441` | FormatTop3Links limit=3 → 同上逻辑 | ⏳ 待做 |
| `service/egress_multiplex.go:648` | `seedCityCounts >= 3` → 移除/放大 | ⏳ 待做 |
| `service/egress_multiplex.go:660` | `seedIndex > 150` → 移除 | ⏳ 待做 |
| `sub/linkService.go:574-591` | `buildVerifiedRemark` 消除"未知"回退 | ⏳ 待做 |

### 8.3 端到端闭环测试 (优先级 P0)
1. 本地 `go test -short -tags "with_gvisor,with_quic" ./service/... ./sub/...`
2. Push → CI/CD 部署
3. 远端拉取 `https://dash.icta.top:2096/sub/my` 验证节点总数 >= 1000
4. 验证备注 0 英文、0 未知
5. v2rayNG 真连接测试验证 0 个 -1，延迟 <= 650ms

---

## 九、关键密钥与凭证汇总 (⚠️ 敏感信息)

| 凭证 | 值 |
|------|-----|
| VPS 用户 | ubuntu (SSH key auth) |
| 面板管理员 | admin / admin |
| 客户端 VLESS UUID | 8c9fa6c8-bf77-4533-8b12-563cc157eb2a |
| Seed 节点 UUID | bada0879-b06e-4d84-b3ff-2b25fc698035 |
| API Secret | zwMhY5O7vBwKCASbOTUc5CB6GDCMWzlQ |
| GitHub Repo | shyding/s-ui (private) |
| Deploy Key | `i:\learn_code\s-ui\scratch\deploy_key` |
