# 08 - 单端口·单用户·传参动态切国与 ProtonVPN 自动负载架构实战指南

> **创建时间**: 2026-09-25  
> **状态**: 已落地 / 已通过自动化测试自测验证  
> **适用范围**: S-UI、Sing-Box v1.12+、v2rayN、Clash Verge、Shadowrocket、WireGuard 用户态隧道、ProtonVPN Free 节点池

---

## 1. 业务痛点与用户诉求

在多出口代理架构（例如同时拥有 Cloudflare 新加坡 WARP、ProtonVPN 美国、日本、荷兰出口）中，传统配置存在极大使用摩擦：
1. **多端口膨胀（Port Bloat）**：为每个出口开辟独立入站端口（如 56230 为新加坡、56231 为美国、56232 为日本），在公网暴露大量端口，不仅容易被防火墙识别和阻断，还需要维护多组安全组策略。
2. **多假用户污染（User Pollution）**：为了分流出口，后台必须创建 `admin-us`、`admin-jp`、`admin-nl` 多个假账号，导致流量配额分散、无法统一统计计费与到期时间。
3. **节点不稳定与手动切换繁琐**：ProtonVPN 免费节点负载高、偶发拥塞失联，用户需要在客户端手动频繁切换节点。

**核心解决目标**：
- **单端口**：客户端无论访问哪国出口，始终仅连接服务端的单一端口（如 `2096` 或 `443`）。
- **单用户**：面板与数据库中保持唯一的真实主用户 `admin`，流量统一统计，配额统一扣除。
- **动态切国**：客户端在 v2rayN / Clash 中无需修改端口，只需点击对应国家节点或修改派生参数，服务端自动精准路由至目标国家出口。
- **自动负载与竞速容灾**：ProtonVPN 节点群通过 Sing-Box `urltest` 纯用户态竞速池纳管，延迟高或失联的节点毫秒级自动剔除，恢复后自动纳管。

---

## 2. 核心架构与原理剖析

### 2.1 整体拓扑图

```mermaid
flowchart TD
    subgraph Client [客户端 v2rayN / Clash / Sing-Box]
        C1["唯一用户: admin (UUID: c0ffee...)"]
        C2["🇸🇬 [SG] 新加坡-WARP (Port 2096)"]
        C3["🇺🇸 [US] 美国-ProtonVPN-智能优选 (Port 2096)"]
        C4["🇯🇵 [JP] 日本-ProtonVPN-智能优选 (Port 2096)"]
        C5["🇳🇱 [NL] 荷兰-ProtonVPN-智能优选 (Port 2096)"]
    end

    subgraph Server [S-UI 单端口统一入口 (Port 2096)]
        IN["VMess / VLESS / Trojan 单端口入站<br/>(dash.icta.top:2096)"]
        ROUTER{"Sing-Box 路由引擎<br/>(按 auth_user 瞬时路由)"}
    end

    subgraph OutboundPool [出口负载池与用户态 WireGuard 隧道]
        WARP["🇸🇬 warp-6eV (Cloudflare WARP)"]
        US_POOL["🇺🇸 us-pool (URLTest 自动测速竞速池)"]
        JP_POOL["🇯🇵 jp-pool (URLTest 自动测速竞速池)"]
        NL_POOL["🇳🇱 nl-pool (URLTest 自动测速竞速池)"]
    end

    subgraph US_Endpoints [ProtonVPN 美国真实 WireGuard 节点群]
        US9["US-FREE#9<br/>89.187.180.1:51820<br/>(gVisor 用户态)"]
        US53["US-FREE#53<br/>146.70.174.66:51820<br/>(gVisor 用户态)"]
        US3["US-FREE#3<br/>195.181.163.1:51820<br/>(gVisor 用户态)"]
    end

    C1 -.订阅展开.-> C2 & C3 & C4 & C5
    C2 & C3 & C4 & C5 ==>|同一IP + 唯一端口 2096| IN
    IN --> ROUTER
    ROUTER -->|"auth_user: admin (默认)"| WARP
    ROUTER -->|"auth_user: admin-us"| US_POOL
    ROUTER -->|"auth_user: admin-jp"| JP_POOL
    ROUTER -->|"auth_user: admin-nl"| NL_POOL

    US_POOL -->|3分钟周期竞速探测| US9 & US53 & US3
```

---

### 2.2 派生凭证分发机制 (Deterministic Credential Derivation)

为了彻底避免“数据库生成多个假账号”的污染，系统在 `service/egress_multiplex.go` 中引入 **RFC 4122 UUIDv5 确定性派生算法**：
- **算法核心**：`DeriveUUID(baseUUID, regionCode)`。通过主账号 UUID 与国家代码（`"us"`, `"jp"`, `"nl"`）计算确定性哈希，生成的 UUID 既完全合规，又绝对不变。
- **入站自动扩展**：`service/inbounds.go` 在向 Sing-Box 内核下发入站配置时，自动为每个主用户注入对应国家的凭据（`admin-us`, `admin-jp` 等），入站监听同一个端口。
- **流量统一归集**：`service/stats.go` 在保存流量上报数据时，自动对带有 `-us`, `-jp` 等后缀的 tag 进行前缀裁剪，统一归集至主账号 `admin` 的 `up` 与 `down`，不漏记一字节流量。

---

## 3. ProtonVPN WireGuard 配置落地实操

用户下载的三个官方 WireGuard 配置文件：
- `sui-node-US-FREE-9.conf` (89.187.180.1:51820)
- `wg-US-FREE-53.conf` (146.70.174.66:51820)
- `wg-US-FREE-3.conf` (195.181.163.1:51820)

### 3.1 关键参数与纯用户态设定（铁律：`system: false`）

| 字段 | US-FREE#9 | US-FREE#53 | US-FREE#3 | 规范要求 |
| :--- | :--- | :--- | :--- | :--- |
| **Endpoint Tag** | `ep-us-free-9` | `ep-us-free-53` | `ep-us-free-3` | 统一标识 |
| **System** | `false` | `false` | `false` | **严禁开启系统接口**，纯 gVisor 运行 |
| **Local Address** | `10.2.0.2/32`, `2a07:b944::2:2/128` | `10.2.0.2/32`, `2a07:b944::2:2/128` | `10.2.0.2/32`, `2a07:b944::2:2/128` | 保留双栈 |
| **Private Key** | `sDxK8N77...mg=` | `sBLOlMMZ...FY=` | `yBVl8qcg...18=` | 对应私钥 |
| **Server** | `89.187.180.1` | `146.70.174.66` | `195.181.163.1` | 远程对端 |
| **Server Port** | `51820` | `51820` | `51820` | WireGuard UDP 端口 |
| **Peer Public Key** | `o3BjGF2...k=` | `N1o6Vqz...w=` | `bOz7aS+...c=` | 对端公钥 |

### 3.2 URLTest 出口池编排

在 Sing-Box 中定义竞速出站：
```json
{
  "type": "urltest",
  "tag": "us-pool",
  "outbounds": ["out-us-9", "out-us-53", "out-us-3"],
  "url": "http://www.gstatic.com/generate_204",
  "interval": "3m",
  "tolerance": 50
}
```
每 3 分钟向 Google 探测点发起健康检查，Sing-Box 内部自动选择延迟最低的通道承载后续流量。当某一节点拥塞断流时，Sing-Box 会将后续请求瞬时漂移到另外两个节点，实现零感知容灾。

---

## 4. 产品经理视角：ProtonVPN 自动化与多账户设计

### 4.1 为什么 ProtonVPN 不能单纯靠 Token 自动免密拉取全部免费节点？
1. **反爬与黑产防御（Cloudflare / Arkose 质押）**：Proton 官方免费计划限制每个账号仅允许 1 个并发连接。为了防止黑产脚本批量扫荡免费节点，网页端在生成 WireGuard 配置时挂载了 Cloudflare 人机验证与 Web Session 校验。
2. **公开 API 的利用方式**：Proton 提供公开状态接口 `https://api.protonmail.ch/vpn/logicals`，无需登录即可实时获取全球免费/付费节点的实时负载率（`Load: 82%`）及 IP。
3. **产品演进三步走**：
   - **阶段一（当前已实现）**：`ParseWireGuardConf` 批量拖拽解析器。用户可将下载的 `.conf` 文件一键拖入面板，系统自动提取配置、识别国家标签并建档。
   - **阶段二**：多账户凭据管理器。在后台保存多个账号（Account A, Account B...），不同国家出口分配不同账号凭据，彻底解决单一账号并发上限。
   - **阶段三**：定时抓取 `logicals` 接口，结合实时真实测速，自动置顶低负载物理节点。

---

## 5. 验证与自测结果

系统落地代码经过了严密的单元测试与内核验证：

### 5.1 自动化单元测试验证 (`service/` & `sub/`)
```bash
go test -v -tags "with_quic,with_grpc,with_utls,with_acme,with_gvisor" ./service/ -run "TestParseWireGuardConf|TestDeriveUUID|TestExpandUsers|TestInjectEgress|TestSingBoxConfig"
```
**测试输出**：
- `TestParseWireGuardConf_AllThreeNodes`: PASS（精确解析 3 个 ProtonVPN conf 文件的私钥、对端 IP、端口与公钥）
- `TestDeriveUUID_Deterministic`: PASS（验证确定性派生算法无漂移）
- `TestExpandUsersForMultiplexing`: PASS（验证单用户自动展开 4 国派生凭证）
- `TestInjectEgressRouteRules`: PASS（验证 Sing-Box 路由表正确挂载 auth_user 策略）
- `TestSingBoxConfig_FullValidation`: PASS（将完整包含 3 个 WireGuard 节点、URLTest 池、单端口入站与分流规则的完整配置喂入 Sing-Box 内核，解析校验 100% 通过）

### 5.2 订阅生成测试 (`sub/`)
```bash
go test -v -tags "with_quic,with_grpc,with_utls,with_acme,with_gvisor" ./sub/
```
**测试输出**：
- `TestExpandEgressLinks_VMess`: PASS（验证订阅自动下发 4 国单端口 2096 节点，客户端直接一键导入使用）
- `TestExpandEgressLinks_VLESS`: PASS

---

## 6. 运维与快速排障命令

1. **检查本地测试套件**：
   ```powershell
   go test -v -tags "with_quic,with_grpc,with_utls,with_acme,with_gvisor" ./service/
   go test -v -tags "with_quic,with_grpc,with_utls,with_acme,with_gvisor" ./sub/
   ```
2. **核查 Sing-Box 核心配置合规性**：
   ```bash
   /usr/local/bin/sing-box check -c /etc/s-ui/config.json
   ```
3. **测试出站真实落地 IP**：
   ```bash
   curl -x socks5h://127.0.0.1:1080 https://ipinfo.io/json
   ```
