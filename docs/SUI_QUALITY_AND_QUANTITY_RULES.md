# S-UI 订阅节点品质与数量刚性铁律 (Quality & Quantity Rigid Rules)

> [!IMPORTANT]
> 本文件为 `s-ui` 项目的**绝对刚性规则（Rigid Invariant Rules）**。任何 AI Agent（Antigravity、Codex、Claude、Cursor 等）或开发者在进行代码修改、配置调整、订阅生成、节点检测与部署时，**必须 100% 严格遵守，零妥协、零例外、严禁遗忘**！

---

## 核心目标与量化指标看板

| 维度 | 刚性指标 | 判定不合格的红线（一票否决） |
| :--- | :--- | :--- |
| **总节点数量** | **$\ge 1000$ 个优质节点**（排除 SUI 原生后外部来源合计 $\ge 1000$） | 订阅节点总数 $< 1000$ |
| **S-UI 原生来源** | **$\ge 15 \sim 20$ 种协议组合**（覆盖 VLESS/VMess/Trojan/Hy2/TUIC/SS 等多协议多传输组合） | S-UI 来源只有 1 个或少于 10 个 |
| **真连接质量门** | **100% 连通（HTTP 204），延迟 $\le 650\text{ms}$** | 出现任何 `-1`，或延迟 $> 650\text{ms}$，或出现 1000ms+/4000ms+ 大延迟 |
| **备注语言与字符** | **除来源允许英文外，其余部分 100% 中文或数字或中文+数字** | 出现任何英文字母（如 `Hanoi`, `Dubai`, `Batu Caves` 等） |
| **备注禁用词** | **绝对零“未知”/零“unknown”/零“unknow”** | 出现 `未知`、`unknown`、`unknow` 等无意义词汇 |
| **来源合法性** | **严格限定四大来源**：`Seed`, `Cloudflare`, `HProxy`, `SUI` | 将协议名（如 `vless`, `vmess` 等）当作来源 |
| **端到端闭环** | **本地/远端 100% 自测闭环，输出真实验证指标** | 仅改代码不自测、不跑端到端握手测试、静默盲猜 |

---

## 刚性规则第一部分：品质铁律 (Quality Invariants)

### 1. 真实质量门 (Quality Gate: $\le 650\text{ms}$, 零 `-1`)
1. **客户端真连接测试（True Connection Test / HTTP 204）100% 成功**：
   - 客户端（v2rayNG, v2rayN, Clash, Sing-Box, Shadowrocket）拉取订阅并执行 URL Test（`http://www.gstatic.com/generate_204`），结果必须全部为绿色真实低延迟，**绝对不允许存在任何一个 `-1`（连接失败/超时）**。
2. **延迟硬上限 $\le 650\text{ms}$**：
   - 客户端端到端延迟必须严格 $\le 650\text{ms}$。
   - 服务端健康检查网关门限锁定在 $\le 500\text{ms}$（预留 100~150ms 客户端至 VPS 的网络抖动与握手裕量）。
   - 任何在 VPS 测量延迟 $> 500\text{ms}$ 的候选节点，一律标记为 `unavailable`，严禁下发给客户端。此前测试中出现的 1000ms+、2000ms+、4000ms+ 的高延迟节点已被证实为抖动失效的假代理，必须坚决在第一道质量门予以剔除！
3. **FAIL-CLOSED 安全过滤原则**：
   - 缺少健康检查记录 $\implies$ 丢弃；
   - `tcp_check != true` 或 `tls_check != true` 或 `proxy_check != true` $\implies$ 丢弃；
   - `latency <= 0` 或 `speed <= 0` $\implies$ 丢弃；
   - 检查记录时间过期（$> 25\text{h}$） $\implies$ 丢弃。

### 2. 备注规范与 100% 中文化铁律
1. **格式严格固定**：
   $$\text{\{来源\}-\{国家\}-\{区域\}-\{城市\}-\{编号\}}$$
   示例：`Seed-中国-香港-香港-01`, `Seed-日本-关东-东京-01`, `SUI-新加坡-中央区-新加坡城-01`。
2. **来源 (Provider) 严格白名单**：
   - 仅限以下 4 种英文名称：
     * `Seed`：用户提供的真实高速种子节点；
     * `Cloudflare`：Cloudflare 官方 WARP/Anycast 洁净出口；
     * `HProxy`：高可用公网安全隧道出站；
     * `SUI`：S-UI 面板原生多协议入站直连。
   - **严禁把协议名称（如 `vless`, `vmess`, `trojan`, `ss` 等）当作来源**！
3. **除来源外，100% 纯中文与数字**：
   - 国家、区域、城市三级地理信息**严禁包含任何英文字母**（如禁止 `Hanoi` 必须为 `河内`，禁止 `Dubai` 必须为 `迪拜`，禁止 `Batu Caves` 必须为 `黑风洞`，禁止 `Tokyo` 必须为 `东京`）。
   - 所有的入库与订阅管线必须强制执行 `service.CleanChineseOrDigit` 与海外地名硬字典映射清洗。
4. **绝对零“未知”**：
   - 无论中文还是英文，备注中**绝对不允许出现“未知”、“unknown”、“unknow”、“Undefined”、“null”等毫无意义的词汇**。遇到无法识别的细分区域，必须回退至该国家标准行政区域或主要城市中文名。

### 3. 网络与安全架构铁律
1. **100% 隔离保护**：
   - 客户端订阅直连地址必须 100% 锁定 VPS 域名与端口（`dash.icta.top:<port>`）。
   - 绝对严禁将外部节点的真实物理 IP（如 `152.53.193.122`）、VPS 主机公网 IP（`124.156.207.253`）或第三方域名直接暴露在客户端订阅链接的 host 字段中。
2. **Sing-Box 路由动态解耦**：
   - 客户端连接通过 VPS 入站的多租户机制（`auth_user` 路由与 UUID 衍生算法），由 VPS 内部动态分发转发至出口点（VLESS Reality, WARP, HProxy），实现出口多样性与入口一致性。

---

## 刚性规则第二部分：数量铁律 (Quantity Invariants)

### 1. S-UI 原生来源必须覆盖多样化协议组合 ($\ge 15 \sim 20$ 组)
1. **常识底线**：
   - S-UI 是基于 Sing-Box 的专业代理管理面板，原生支持现代全协议栈与多传输层组合。
   - S-UI 原生来源节点**绝不能只有一个孤独的 `vless` 节点**，必须提供多样化的协议与传输组合，至少覆盖以下 15~20 种组合：
     1. `VLESS + TCP + TLS`
     2. `VLESS + WebSocket + TLS`
     3. `VLESS + gRPC + TLS`
     4. `VLESS + HTTPUpgrade + TLS`
     5. `VMess + TCP`
     6. `VMess + WebSocket + TLS`
     7. `VMess + gRPC + TLS`
     8. `VMess + HTTPUpgrade + TLS`
     9. `Trojan + TCP + TLS`
     10. `Trojan + WebSocket + TLS`
     11. `Trojan + gRPC + TLS`
     12. `Trojan + HTTPUpgrade + TLS`
     13. `Hysteria2 + QUIC + TLS`
     14. `TUIC v5 + QUIC + TLS`
     15. `Shadowsocks 2022 (2022-blake3-aes-128-gcm)`
     16. `Shadowsocks AEAD (aes-256-gcm)`
     17. `Shadowsocks AEAD (chacha20-ietf-poly1305)`
     18. `Socks5 (认证代理)`
     19. `HTTP Proxy (认证代理)`
     20. `Mixed (SOCKS5 + HTTP)`
2. **独立编号发布**：
   - 每个 SUI 协议组合在订阅中作为独立节点发布，依次编号：
     `SUI-新加坡-中央区-新加坡城-01`, `SUI-新加坡-中央区-新加坡城-02`, ..., `SUI-新加坡-中央区-新加坡城-20`。
   - 严禁通过简单的 `limit := 3` 将 SUI 原生节点截断成 1 个或 3 个！

### 2. 外部来源全量扩充并突破 $\ge 1000$ 节点
1. **Seed 种子节点全量实装 (108+ 节点全盘激活)**：
   - 用户提供的 108 个 VLESS Reality 节点具有超高连通率与极低延迟，必须全量入库，严禁截断丢弃。
   - 移除 `seedCityCounts >= 3` 硬性限制，每个城市允许扩展至 50 个节点（`-01` 到 `-50`），充分利用同城不同落地服务器的承载力。
2. **Cloudflare WARP/Anycast 全量 POP 展开**：
   - 激活 Cloudflare 官方 15 个 CIDR 的全球 Anycast 端点，覆盖全球 300+ 核心城市。
   - 统一按照标准全中文地理备注发布。
3. **HProxy 公网低延迟隧道扩容**：
   - 严格通过真实隧道连通性测试（`CONNECT www.gstatic.com:80`），保留真实可用的健康代理。
   - 解除单城市单池限制，按可用节点流水线输出编号。
4. **总量门限**：
   - 排除 SUI 原生节点后，`Seed` + `Cloudflare` + `HProxy` 的健康节点总数必须 $\ge 1000$ 个！

---

## 刚性规则第三部分：自测闭环与自证铁律 (Self-Audit & Closed-Loop)

1. **严禁盲猜与静默汇报**：
   - 任何改动部署完成后，必须在本地或远端运行端到端自动化测试脚本，实际请求 `https://dash.icta.top:2096/sub/my`。
2. **必须输出包含以下核心指标的自检看板**：
   - 节点总数（必须 $\ge 1000$）；
   - 各来源（SUI, Seed, Cloudflare, HProxy）节点分布；
   - 节点备注合规性审计结果（英文地名检出数 = 0，“未知”检出数 = 0）；
   - 真实连通性测试采样结果（RTT 范围、平均延迟、超时 `-1` 数量 = 0）。
