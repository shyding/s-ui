# Sub 订阅服务模块架构与研发规范 (Subscription Architecture & Engineering Notes)

> 本文档专为后续维护开发者以及 **Claude (Claude Code)**、**Codex** 等 AI Agent 编写，明确记录 `sub/` 订阅管理模块的数据流转、清洗规则与客户端兼容性关键陷阱。

---

## 1. 模块职责 (Responsibilities)
`sub/` 目录负责订阅链接的分发、格式转换与客户端适配：
- `linkService.go`: 解析与聚合授权链接（支持 `local` 本地入站、`external` 外部单节点、`sub` 外部订阅抓取），执行域名清洗与协议过滤。
- `subService.go`: 生成标准 Base64 订阅链接文本（如 v2rayN、Shadowrocket 所需格式），附带流量统计响应头。
- `clashService.go`: 生成 Clash Meta (Mihomo) / Clash Premium 的 YAML 订阅配置。
- `jsonService.go`: 生成 Sing-Box 客户端的 JSON 原生出站与路由配置。
- `subHandler.go`: Gin Web 路由处理器（响应 `/sub/:id` 请求）。

---

## 2. 关键过滤清洗规则 (Sanitization & Filtering Rules)

### 2.1 历史域名动态清洗 (Domain Sanitization)
历史背景：旧域名 `dash.icta.qzz.io` 与 `sub.icta.qzz.io` 已被污染/弃用，系统已全面迁移至新域名 `dash.icta.top`。
为保证数据库中历史存量节点与导入订阅能平滑访问，代码必须在输出前动态替换：
```go
cleanUri := strings.ReplaceAll(link.Uri, "dash.icta.qzz.io", "dash.icta.top")
cleanUri = strings.ReplaceAll(cleanUri, "sub.icta.qzz.io", "dash.icta.top")
```
**铁律**：后续任何涉及订阅生成的接口，严禁将未清洗的 `*.qzz.io` 输出给客户端。

### 2.2 不支持协议剔除 (Protocol Filtering)
部分历史外部节点为 `http2://`（NaiveProxy）协议，主流客户端（如 v2rayN、Clash 等）默认内核无法直接识别该协议头，会导致客户端订阅解析异常或导入报错。
在 `linkService.go` 中必须过滤：
```go
if strings.HasPrefix(link.Uri, "http2://") {
    continue
}
```

### 2.3 节点去重 (Deduplication)
由于 `external` 和 `sub` 导入可能存在重叠，输出时必须使用 `seen[cleanUri]` 保证订阅节点唯一性，避免客户端显示重复条目。

---

## 3. 致命客户端兼容性陷阱：Xray Fragment 与 UDP/QUIC 冲突

### 3.1 故障现象（真连接延迟绿字，但浏览器无法翻墙/握手超时）
用户在 v2rayN 中使用 Hysteria2 或 TUIC 节点时：
- 按 `Ctrl+R` 测试真连接延迟：**延迟显示正常（如 100ms 绿字）**。
- 设为活动服务器后打开 Google：**网页彻底打不开，报错 `ERR_CONNECTION_CLOSED` 或 TLS 握手失败（Curl Code 35）**。

### 3.2 根因排查剖析
1. 用户在 v2rayN 中启用了 `"EnableFragment": true`（用于绕过 SNI 审查的 TCP 分片功能）。
2. 在 v2rayN 配置文件 `guiNConfig.json` 中，Hysteria2 (`ConfigType: 7`) 和 TUIC (`ConfigType: 8`) 被错误分配给 **Xray 内核** (`CoreType: 2`) 调度。
3. Xray 的 Fragment 机制在出站前挂载了 `frag-proxy`，其底层通过 TCP 报文切片破坏 TLS Client Hello 的特征。
4. 但 **Hysteria2 和 TUIC 是纯 UDP / QUIC 协议**！`frag-proxy` 将 TCP 切片逻辑套用在 UDP 数据报上，破坏了 QUIC 数据报文的 Header 与帧边界。
5. 服务端收到损毁的 UDP QUIC 数据包后直接丢弃，导致 TLS 1.3 握手彻底破裂。
6. 而 `Ctrl+R`（真连接测试）走的是直接测试模式，绕过了 `frag-proxy`，因此误报延迟正常。

### 3.3 解决方案
- **客户端规范配置**：在 v2rayN 的 `设置` -> `参数设置` -> `Core类型设置` 中，将 **Hysteria2** 与 **TUIC** 的内核类型强制切换为 **`sing-box`**（`CoreType: 24`）。
- **服务端保持稳健**：服务端 Sing-Box 核心对 UDP/QUIC 连接始终提供标准 ALPN（`h3,h2,http/1.1`）支持。

---

## 4. 关联知识库
- 深度技术剖析详见：
  - [`docs/knowledge_base/02_subscription_domain_sanitization.md`](../docs/knowledge_base/02_subscription_domain_sanitization.md)
  - [`docs/knowledge_base/03_client_xray_fragment_vs_quic.md`](../docs/knowledge_base/03_client_xray_fragment_vs_quic.md)
- 全局备忘录：[`TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md`](../TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md)
