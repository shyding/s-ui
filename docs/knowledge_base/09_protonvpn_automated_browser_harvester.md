# 知识库 09: ProtonVPN 模拟浏览器全自动节点收割器与免人工干预入站出口编排系统

## 1. 业务背景与方案演进决策 (Decision Rationale)

在前期方案中，曾讨论过直接从浏览器 DevTools F12 抓取 Session Token / UID 填入面板（方式 A）。经过深入推演与用户明确反馈：
- **痛点**：手动抓取 Token / Cookie 门槛高、步骤冗长繁杂、Token 易过期失效，极大损害了生产系统的自动化可用性。
- **裁决**：**彻底舍弃手动复制 Token / Cookie 方案**。
- **全新技术路线**：引入**模拟浏览器全自动化收割器 (Simulated Browser Automation)** 与**本地配置目录递归秒级导入引擎**双引擎联动架构，实现真正的 100% 免人工干预 (Zero Manual Intervention)。

---

## 2. 系统核心架构与数据流图

```mermaid
flowchart TD
    subgraph UI_CLI [交互与触发层]
        CMD_BROWSER["sui proton -browser<br/>(展现模拟浏览器·首次一键认证)"]
        CMD_AUTO["sui proton -auto<br/>(后台静默定时收割)"]
        CMD_DIR["sui proton -import-dir<br/>(本地 .conf 递归秒级导入)"]
        WEB_API["POST /api/protonAutoHarvest<br/>(S-UI 面板一键同步)"]
    end

    subgraph Harvester [模拟浏览器收割核心 (Playwright + Chrome)]
        PROFILE["持久化专用隔离会话<br/>(~/.sui_proton_profile)"]
        NAV["直达 Proton 门户<br/>account.proton.me/u/0/vpn"]
        DETECT{"会话状态探测"}
        POPUP["展现 Chrome 窗口<br/>用户单次免密/自动填充登录"]
        INPAGE_EXEC["页面内部沙箱 Fetch<br/>/api/vpn/logicals"]
        STREAM_SNIFF["响应流被动嗅探<br/>(x-pm-appversion 自动对齐)"]
    end

    subgraph SUI_Core [S-UI 后端编排引擎 (Go + Pure SQLite)]
        PARSER["Proton 节点解析与负载排序<br/>(Tier 0 免费节点: US, JP, NL)"]
        KEYGEN["RFC 7748 X25519<br/>本地客户端密钥对自动生成"]
        DB["S-UI 数据库 (endpoints / outbounds)"]
        POOLS["URLTest 智能竞速池<br/>(us-pool, jp-pool, nl-pool)"]
        ROUTER["Sing-Box 智能路由注入<br/>(auth_user: admin-us / admin-jp / admin-nl)"]
    end

    CMD_BROWSER --> Harvester
    CMD_AUTO --> Harvester
    WEB_API --> Harvester
    CMD_DIR --> PARSER

    Harvester --> PROFILE
    PROFILE --> NAV
    NAV --> DETECT
    DETECT -->|未登录| POPUP
    DETECT -->|已就绪| INPAGE_EXEC & STREAM_SNIFF

    INPAGE_EXEC --> PARSER
    STREAM_SNIFF --> PARSER

    PARSER --> KEYGEN
    KEYGEN --> DB
    DB --> POOLS
    POOLS --> ROUTER
```

---

## 3. 核心技术实现细节

### 3.1 模拟浏览器自动化引擎 (`scripts/proton_harvester.py`)
1. **持久化隔离 Profile (`~/.sui_proton_profile`)**：
   - 彻底解决 Chrome 正在运行时文件独占锁定 (`WinError 32`) 的痛点。
   - 独立存储 Proton 登录会话与 Cookie，单次登录终身受用，后续均可后台无头 (`--headless`) 静默执行。
2. **展现模式 (`-browser`, headless=False)**：
   - 当会话失效或首次启动时，程序弹出可视化 Chrome 窗口，用户直接登录（支持密码管理器自动填充）。
   - 脚本通过 URL、页面 Title 与 DOM 事件实时感知登录完成，**用户无需打开任何 F12 控制台，零复制粘贴**。
3. **页面上下文安全穿透 (In-Page Context Fetch)**：
   - 登录成功后，脚本直接在页面沙箱上下文执行 `fetch('/api/vpn/logicals')`，天然携带当前页面的合规请求头、CSP Nonce 与反爬指纹。
   - 自动适配 `x-pm-appversion: linux-vpn@4.14.1` 与 `web-vpn@5.0.421.0`，彻底根除 400 与 500 错误。
   - 数据通过标准边界标记 `---SUI_HARVEST_START---` 封装为 JSON 传递给 Go 主程序。

### 3.2 跨平台纯 Go SQLite 驱动升级 (`github.com/glebarez/sqlite`)
- 原 `gorm.io/driver/sqlite` 依赖 CGO (`mattn/go-sqlite3`)，在 Windows/无 GCC 环境下会抛出 `Binary was compiled with 'CGO_ENABLED=0'` 致命崩溃。
- 全面切换至纯 Go 实现的 `github.com/glebarez/sqlite`，实现 Windows、Linux、macOS 全平台零 CGO 编译与运行，保证测试与构建 100% 顺畅。

### 3.3 目录导入与国家代码精准推断算法
- 针对本地批量下载的 WireGuard `.conf` 配置文件（如 `C:\Users\acer\Downloads\openvpn\us\`）：
- 采用基于正则单词边界的严格提取模式：`(?i)(?:^|[^a-zA-Z])(US|JP|NL|SG)(?:[^a-zA-Z]|$)`。
- **消除了子串误判陷阱**：彻底规避了目录名 `Downloads` 含有 `NL` 子串而导致的误识别问题，确保美国节点绝对归属 `US`。

### 3.4 纯用户态 gVisor 与单用户流量记账
- 所有 Proton 节点在 S-UI 中生成的 `Endpoint` 强制指定 `"system": false`，通过 Sing-Box 内置 gVisor 运行在纯用户空间，不创建系统网卡，不干扰主机默认网关。
- 客户端通过单一端口（如 `2096`）连接，凭证派生体系：
  - `admin` -> 默认新加坡 Warp 出口 (`warp-6eV`)
  - `admin-us` -> 自动进入 `us-pool`（美国 Proton WireGuard 竞速池，3 分钟自动故障剔除）
  - `admin-jp` -> 自动进入 `jp-pool`（日本 Proton WireGuard 竞速池）
  - `admin-nl` -> 自动进入 `nl-pool`（荷兰 Proton WireGuard 竞速池）
- 流量统计拦截器在入库前自动剔除 `-us`, `-jp`, `-nl` 后缀，保证 100% 聚合归属主账户 `admin`，零多用户杂质。

---

## 4. 验证与自测结果

| 验证项 | 测试命令 / 路径 | 验证结果 |
| :--- | :--- | :--- |
| **单元测试全量覆盖** | `go test -v -tags "..." ./service/ ./sub/` | **100% PASS** (16/16 单元测试全部通过) |
| **收割器解析逻辑** | `TestHarvestResultParsing_Logic` | **PASS** |
| **路径与文件名国别推断** | `TestInferCountryFromPath_Logic` | **PASS** (覆盖 Downloads 陷阱与多命名格式) |
| **纯 Go SQLite 测试** | `TestTestOutboundWithLandingIP_DeadNode` | **PASS** (无 CGO，零跳过) |
| **本地实盘导入测试** | `sui.exe proton -import-dir "C:\Users\acer\Downloads\openvpn\"` | **PASS** (3 个美国节点精准导入 `us-pool`) |
| **Sing-Box 编译与验证** | `go build -tags "..." -o s-ui.exe main.go` | **PASS** (二进制大小 141MB，零告警) |

---

## 5. 操作手册 (CLI & API)

### 命令行使用 (CLI)
```bash
# 1. 展现模拟浏览器进行登录并自动收割节点 (首次使用或更新登录态)
sui proton -browser -countries "US,JP,NL"

# 2. 后台静默自动收割更新节点 (基于已保存会话)
sui proton -auto -countries "US,JP,NL"

# 3. 递归扫描并导入本地 WireGuard .conf 目录
sui proton -import-dir "C:\Users\acer\Downloads\openvpn\"
```

### 接口调用 (API)
```http
POST /api/protonAutoHarvest
Content-Type: application/json

{
  "headless": true,
  "countries": ["US", "JP", "NL"]
}
```
响应示例：
```json
{
  "success": true,
  "msg": "Successfully harvested and imported 12 ProtonVPN free nodes into S-UI pools (12 nodes)"
}
```
