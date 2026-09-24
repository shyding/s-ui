# Service 业务逻辑模块架构与研发规范 (Service Architecture & Engineering Notes)

> 本文档专为后续维护开发者以及 **Claude (Claude Code)**、**Codex** 等 AI Agent 编写，明确记录 `service/` 业务逻辑模块的核心设计、配置注入规则、网络排障与运维关键约束。

---

## 1. 模块职责 (Responsibilities)
`service/` 目录负责面板的核心业务编排与底层 Sing-Box 核心的交互：
- `config.go`: 生成 Sing-Box 的运行时 JSON 配置文件，支持热重载，并提供核心配置的合规化检查与自动修复（`sanitizeConfig`）。
- `inbounds.go` / `outbounds.go`: 代理入站协议（VMess、VLESS、Trojan、Shadowsocks、Hysteria2、TUIC）与出站路由配置管理。
- `nodetest.go`: 节点连通性、真连接延迟（Real Ping）、落地 IP 查询（Landing IP）及测速功能。
- `tls.go`: TLS 证书生命周期管理、自动化签发（Let's Encrypt / acme.sh 适配）。
- `client.go` / `user.go` / `stats.go`: 用户授权、流量计量与统计归档。

---

## 2. 关键配置注入与容错规则 (Invariants & Sanitization)

### 2.1 公共 DNS 解析器平滑回退 (Public DNS Resolvers Fallback)
在 `config.go` 的 `sanitizeConfig` 函数中，必须确保 Sing-Box 的 `dns.servers` 配置包含高可靠的公共 DNS 解析器：
```go
if len(singboxConfig.Dns) > 0 {
    var dnsMap map[string]interface{}
    if err := json.Unmarshal(singboxConfig.Dns, &dnsMap); err == nil {
        servers, _ := dnsMap["servers"].([]interface{})
        if len(servers) == 0 {
            dnsMap["servers"] = []map[string]string{
                {"tag": "remote", "address": "udp://8.8.8.8"},
                {"tag": "cf", "address": "udp://1.1.1.1"},
                {"tag": "local", "address": "local"},
            }
            if newDns, err := json.Marshal(dnsMap); err == nil {
                singboxConfig.Dns = newDns
                modified = true
            }
        }
    }
}
```
**设计背景**：
云服务器提供商（如腾讯云、阿里云）分配的内网默认 DNS（如 `183.60.83.19`）经常对国际域名解析不稳定或存在解析屏蔽。如果不注入 `8.8.8.8` (Google DNS) 和 `1.1.1.1` (Cloudflare DNS)，Sing-Box 核心在代理国外流量时会发生上游 DNS 解析超时，导致整机断流。

### 2.2 节点连通性测试 IPv6 地址格式化 (IPv6 Address Formatting)
在 `nodetest.go` 中，向被测代理节点发起测试探测连接时，必须使用标准 `net.JoinHostPort` 处理目标地址：
```go
// 正确做法：
targetAddr := net.JoinHostPort(host, portStr)
```
**避坑指南**：
严禁使用原始拼接 `fmt.Sprintf("%s:%s", host, port)`。当目标节点为 IPv6 地址时（例如 `2400:cb00:...`），原始拼接会生成 `http://2400:cb00:...:80`，导致 Go 的 `net/http` 解析报 `too many colons in address` 严重错误。标准做法要求包裹方括号：`http://[2400:cb00:...]:80`。

---

## 3. 运维与外部网络架构约束 (Cloudflare & acme.sh)

### 3.1 Cloudflare CDN 端口白名单
若域名托管在 Cloudflare 并开启 CDN 代理（橙色小云朵），Cloudflare **仅支持以下特定端口的 HTTPS 回源**：
- `443`, `2053`, `2083`, `2087`, `2096`, `8443`
**生产规范**：
当前面板 Web 监听端口为 **`2096`**（位于 Cloudflare 允许列表中）。严禁任意修改为非白名单端口（如 8080、9090），否则 Cloudflare 会直接返回 HTTP 521 / 522 拒绝连接。

### 3.2 Cloudflare SSL 526 错误防范
- 如果源站使用的证书不是受信任的公共 CA 证书（例如使用了自签名证书），Cloudflare 的 SSL/TLS 加密模式**必须设置为 "Full"（完全），严禁设置为 "Full (strict)"（完全严格）**。
- 否则 Cloudflare 回源校验证书时会直接阻断并报 `Error 526: Invalid SSL certificate`。

### 3.3 acme.sh 80 端口冲突死锁
在使用 `acme.sh` Standalone 模式申请 Let's Encrypt 证书时，它必须在宿主机监听 `0.0.0.0:80` 接收 ACME 校验请求。
如果宿主机已安装 Nginx 并占用了 80 端口，签发会彻底失败（`port 80 is already used`）。
**处理方式**：
- 方式 A：临时关闭 Nginx：`systemctl stop nginx`，证书签发完成后再启动。
- 方式 B：配置 Nginx 路径反代，采用 `--webroot /var/www/html` 模式进行文件验证。

---

## 4. 关联知识库
- 深度技术剖析详见：
  - [`docs/knowledge_base/04_dns_public_resolver_fallback.md`](../docs/knowledge_base/04_dns_public_resolver_fallback.md)
  - [`docs/knowledge_base/06_domain_migration_and_ssl_526.md`](../docs/knowledge_base/06_domain_migration_and_ssl_526.md)
- 全局备忘录：[`TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md`](../TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md)
