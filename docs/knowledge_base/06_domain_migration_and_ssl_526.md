# 06. 域名迁移、Cloudflare 526 证书故障与 80 端口占用排查指南

## 1. 业务背景
在本项目运维过程中，服务端域名由历史域名 `dash.icta.qzz.io` 全面迁移至新域名 `dash.icta.top`。迁移涉及：
- Web 管理面板 (`https://dash.icta.top:2053/app/`)
- 客户端订阅服务 (`https://dash.icta.top:2096/sub/my`)
- 节点底层 TLS 证书与 SNI 校验

在此过程中，遇到了 Cloudflare 526 错误、acme.sh 80 端口冲突、以及订阅历史链接域名缓存等一系列典型问题。

---

## 2. Cloudflare Error 526 (Invalid SSL Certificate) 根因与解决

### 故障现象
用户通过浏览器访问 `https://dash.icta.top:2053/` 时，页面无法打开，显示 Cloudflare 拦截页面：
`Error 526: Invalid SSL certificate`。

### 底层根因
1. **Cloudflare SSL 严格模式 (Full Strict)**：
   - 当 Cloudflare 的 SSL/TLS 加密模式设置为 **Full (Strict)** 时，Cloudflare 回源请求源服务器时，强制要求源服务器必须出示由受信任公有 CA（如 Let'\''s Encrypt）签发、且**匹配当前访问域名（`dash.icta.top`）的有效未过期证书**。
2. **源站证书不匹配**：
   - 域名刚刚迁移至 `dash.icta.top`，但源站服务器（`124.156.207.253`）的 2053/2096 端口上绑定的依然是旧域名 `dash.icta.qzz.io` 的证书，或者使用的自签名证书不受信任。
   - Cloudflare 发现源站证书与请求主机名不匹配，校验失败，立即阻断访问并返回 526 错误代码。

### 解决方案
1. **源站签发受信任公有证书**：
   - 为源站申请 `dash.icta.top` 的 Let'\''s Encrypt 证书并正确配置到面板与订阅服务。
2. **或在 Cloudflare 控制台使用 Origin Certificate（源服务器证书）**：
   - 在 Cloudflare SSL/TLS -> Origin Server 生成 15 年有效期的源站证书，安装到源站服务器，即可完美兼容 Full (Strict) 模式。
3. **临时应急测试**：
   - 可将 Cloudflare 的 SSL/TLS 模式由 `Full (Strict)` 临时降级为 `Full`，允许源站证书不严格校验主机名（不推荐生产使用）。

---

## 3. acme.sh 独立模式 (Standalone) 80 端口占用冲突

### 故障现象
在 Ubuntu 服务器上使用 `acme.sh` 申请证书时执行：
```bash
~/.acme.sh/acme.sh --set-default-ca --server letsencrypt
~/.acme.sh/acme.sh --issue -d dash.icta.top --standalone --force
```
报错中断：
```text
[Thu Sep 24 04:06:41 PM CST 2026] tcp port 80 is already used by (("nginx",pid=2670300,fd=6)...)
[Thu Sep 24 04:06:41 PM CST 2026] Please stop it first
```

### 根因剖析
- `acme.sh --standalone` 模式依赖在宿主机本地临时拉起一个内置的简易 HTTP Web 监听（占用 `0.0.0.0:80` 端口），用于响应 Let'\''s Encrypt CA 的 HTTP-01 验证请求。
- 此时宿主机系统上后台运行了 Nginx 守护进程，已经占据了 TCP 80 端口，导致 `acme.sh` 无法绑定该端口。

### 解决方案 (方案 A: 临时释放 80 端口)
```bash
# 1. 停止占用 80 端口的 Nginx 服务
sudo systemctl stop nginx
# 或杀掉占用进程: sudo fuser -k 80/tcp

# 2. 重新发起独立签发
~/.acme.sh/acme.sh --issue -d dash.icta.top --standalone --force

# 3. 安装证书到 s-ui 指定目录
~/.acme.sh/acme.sh --install-cert -d dash.icta.top \
    --key-file /usr/local/s-ui/cert/dash.icta.top.key \
    --fullchain-file /usr/local/s-ui/cert/dash.icta.top.cer

# 4. 恢复 Nginx 服务 (如有需要)
sudo systemctl start nginx
```

---

## 4. 域名迁移引发的订阅历史缓存污染与自愈机制

### 故障现象
域名迁移完成后，用户在客户端拉取订阅发现：
- 节点列表里很多节点依然是 `dash.icta.qzz.io` 或 `sub.icta.qzz.io`；
- 由于旧域名已被注销或删除 DNS 解析，客户端连接直接报 `NXDOMAIN` 或 `Couldn'\''t resolve host`。

### 根因剖析
- `s-ui` 的客户端数据库表（`clients`）中，`links` 字段持久化了之前生成好的完整节点链接 JSON。
- 当用户在后台修改 `webDomain` 或更换服务器域名时，系统**不会自动扫描历史数据库去重写所有已有客户端的静态链接**。

### 终极自愈方案 (Commit `9b0e938`)
在订阅导出服务（`sub/linkService.go` 和 `sub/subService.go`）中建立**运行时动态域名净化管线**：
```go
// 运行时检测并强制替换历史旧域名为现行生效域名
cleanUri := strings.ReplaceAll(link.Uri, "dash.icta.qzz.io", "dash.icta.top")
cleanUri = strings.ReplaceAll(cleanUri, "sub.icta.qzz.io", "dash.icta.top")
```
- **收益**: 无论数据库历史遗留了什么旧域名数据，订阅在输出瞬间均被无感纠偏为当前正确域名，彻底避免因历史缓存导致的用户节点失效。

---

## 5. Cloudflare CDN 端口兼容性准则
若域名解析开启了 Cloudflare CDN（小黄云开启 Proxy 模式）：
- **支持的 HTTPS 端口仅限于**: `443`, `2053`, `2083`, `2087`, `2096`, `8443`。
- 本项目设计：
  - **管理面板**: `2053` (合规 HTTPS 端口)
  - **订阅服务**: `2096` (合规 HTTPS 端口)
- 其余非标端口的节点（如 `8444`, `45378`, `57295` 等）必须采用 **DNS Only（灰云直连）**，不可走 CDN 代理，否则 UDP 和非标 TCP 端口流量会被 Cloudflare 边缘节点直接丢弃。
