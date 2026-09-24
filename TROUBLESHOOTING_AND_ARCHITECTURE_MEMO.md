# S-UI & 客户端 (v2rayN) 架构优化与故障排查永久备忘录

> 本文档记录了 `s-ui` 核心代理层、订阅生成逻辑、DNS 递归解析、以及客户端（v2rayN）协议内核调度的全套关键排查经验与修复方案，供后续运维、二次开发与架构复用。

---

## 一、核心服务端修复记录

### 1. VMess/WebSocket 非双工流半关闭悬挂故障 (Commit: `ce66345`)
- **文件**: `core/tracker_conn.go`
- **故障现象**: 
  - 访问 Google 能拿到第一段 HTML（如 10KB~20KB），但大体积静态资源（1MB+ 的 `xjs` 脚本、CSS、媒体流、YouTube）下载卡死，浏览器标签栏持续转圈直至超时。
- **底层根因**:
  - `wrappedConn` 内部封装了 `ExtendedConn`，但在结构体上显式实现了空的 `CloseWrite()` 与 `CloseRead()` 方法。
  - Sing-Box 底层 `bufio.CopyConn` 在数据传输完毕时会调用 `common.Close(source)`。
  - 因为空方法的屏蔽，Sing-Box 无法探测到底层真实的连接状态，且无法执行正常的 TCP 半关闭（FIN），导致 WebSocket 流挂起不释放。
- **解决方案**:
  1. 彻底删除 `wrappedConn` 中的 dummy `CloseWrite()` 和 `CloseRead()`。
  2. 实现 `Upstream()`、`UpstreamReader()`、`UpstreamWriter()` 返回内层 `ExtendedConn`。
  3. 保留 `ReaderReplaceable() bool` 和 `WriterReplaceable() bool` 返回 `true`。
- **验收成效**: 1.11MB 的 `xjs` 脚本加载耗时从 15 秒超时降至 **2.73 秒**，页面秒级渲染。

---

### 2. 订阅系统死域名残留与非标协议过滤 (Commit: `9b0e938`)
- **文件**: `sub/linkService.go`, `sub/subService.go`
- **故障现象**:
  - 用户拉取订阅后，部分节点（如 `naive-48847`）无法解析主机名（`NXDOMAIN`），部分节点 SNI 携带历史旧域名 `sub.icta.qzz.io`。
  - 订阅中存在常规客户端无法导入的私有协议（如 `http2://`），以及重复节点。
- **底层根因**:
  - 服务器域名曾发生迁移（从 `dash.icta.qzz.io` 迁移至 `dash.icta.top`），但客户端数据库的 `Links` 字段缓存了历史生成的节点 URI。
  - 在生成订阅时，未对历史死域名进行运行时动态重定向，且未剔除客户端不兼容协议。
- **解决方案**:
  1. 在 `sub/linkService.go` 的 `GetAuthorizedLinks` 中：
     - 自动检测并过滤 `http2://` 等非标协议链接。
     - 运行时动态将 `dash.icta.qzz.io` 和 `sub.icta.qzz.io` 归一化替换为生效中的 `dash.icta.top`。
     - 增加哈希表 `seen` 机制，自动对重复的节点链接进行去重。
  2. 在 `sub/subService.go` 输出最终 Base64 前进行二次兜底替换。
- **验收成效**: 订阅列表净化为 13 个纯净高可用节点，全量自动化连通测试达到 **100% 通过率**。

---

### 3. 服务端 DNS 上游回退与防污染 (Commit: `9b0e938`)
- **文件**: `service/config.go`
- **故障现象**:
  - 遇到部分境外或特定区域域名（如台湾地区域名、特定查 IP 网站）时，机房默认 DNS 返回拒绝或解析失败。
- **底层根因**:
  - Sing-box 默认 DNS 仅配置了 `{"tag": "local", "address": "local"}`，全量依赖宿主机 `/etc/resolv.conf`（腾讯云机房内网 DNS 解析器），容易受机房出口 DNS 限制。
- **解决方案**:
  - 在 `service/config.go` 的 `sanitizeConfig` 中增加公共递归 DNS 回退：
    ```json
    [
      {"tag": "remote", "address": "udp://8.8.8.8"},
      {"tag": "cf", "address": "udp://1.1.1.1"},
      {"tag": "local", "address": "local"}
    ]
    ```

---

### 4. 宿主机内核网络性能加固 (`deploy.yml`)
- **文件**: `.github/workflows/deploy.yml`
- **配置项**:
  - 开启 Google BBR 拥塞控制算法：`net.ipv4.tcp_congestion_control=bbr`，队列调度算法 `net.core.default_qdisc=fq`。
  - 放大 TCP 缓冲区上限：`rmem_max=16777216`, `wmem_max=16777216`，匹配百兆以上跨国带宽。

---

## 二、客户端 (v2rayN) 关键排错与避坑准则

### 1. “真连接测速有绿色延迟，但设为活动服务器后打不开网页” 的根本原因
- **致命冲突**: **Xray Fragment (TLS分片防封) 与 UDP (QUIC / Hysteria2 / TUIC) 水火不容**。
- **原理解剖**:
  1. 用户在 v2rayN 中开启了全局 **“启用分片 (Enable Fragment)”**。
  2. v2rayN 默认把所有协议都分配给了 `Xray` 内核。
  3. Xray 的 Fragment 机制是在底层挂载 `frag-proxy`，用于把 **TCP** 上的 TLS Client Hello 切成两截以绕过防火墙检测。
  4. 当用户选中 **Hysteria2** 或 **TUIC** 节点时，这些协议走的是 **UDP / QUIC**！
  5. Xray 把针对 TCP 的切包逻辑强加给 UDP 数据包，导致 **QUIC 握手包被切碎损坏**，服务端无法识别直接丢弃，客户端报 `Code 35 (TLS握手崩溃)`。
  6. 为什么测真连接（Ctrl+R）显示通？因为测速生成的是临时独立配置，**不挂载全局分片代理**；一旦按 Enter 设为活动服务器，就必须走全局分片，立刻瘫痪。
- **标准解决方案**:
  - 在 v2rayN 中点击 **设置 ➡️ 参数设置 ➡️ Core类型设置**：
    - 将 **Hysteria2** 映射为 **`sing-box`**
    - 将 **TUIC** 映射为 **`sing-box`**
  - *(注：v2rayN 内部已内置 `sing-box.exe`，它是目前全球公认运行 QUIC/Hysteria2 最稳定强劲的官方核心，且不会受 Xray Fragment 的破坏)*。

---

## 三、当前全量节点基线（13个节点 100% 验收）

| 协议分类 | 节点名称 | 端口 | 推荐内核 | 连通状态 |
| :--- | :--- | :--- | :--- | :---: |
| **VMess** | `vmess-21026 ♾` | 21026 | Xray / sing-box | ✅ 200 OK |
| **VMess** | `vmess-2083 ♾` | 2083 | Xray / sing-box | ✅ 200 OK |
| **VMess** | `vmess-14314 ♾` | 14314 | Xray / sing-box | ✅ 200 OK |
| **VMess** | `vmess-33193 ♾` | 33193 | Xray / sing-box | ✅ 200 OK |
| **VMess** | `vmess-50973 ♾` | 50973 | Xray / sing-box | ✅ 200 OK |
| **VLESS** | `wireguard_vless-57239 ♾` | 57239 | Xray / sing-box | ✅ 200 OK |
| **Hysteria2** | `hysteria2-25536 ♾` | 25536 | **sing-box** | ✅ 200 OK |
| **Hysteria2** | `hysteria2-8444 ♾` | 8444 | **sing-box** | ✅ 200 OK |
| **Hysteria2** | `hysteria2-45378 ♾` | 45378 | **sing-box** | ✅ 200 OK |
| **TUIC** | `tuic-57295 ♾` | 57295 | **sing-box** | ✅ 200 OK |
| **TUIC** | `tuic-57142 ♾` | 57142 | **sing-box** | ✅ 200 OK |
| **TUIC** | `tuic-8445 ♾` | 8445 | **sing-box** | ✅ 200 OK |
| **TUIC** | `tuic-55656 ♾` | 55656 | **sing-box** | ✅ 200 OK |
