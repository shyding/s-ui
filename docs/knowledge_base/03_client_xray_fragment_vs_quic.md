# 03. 客户端适配: Xray Fragment (TLS分片) 与 UDP/QUIC 的底层冲突铁律

## 1. 经典故障现象
- 在 v2rayN 客户端中选中 Hysteria2 或 TUIC 节点：
  - 按 `Ctrl+R`（测试服务器真连接延迟）时，**显示绿色正常延迟（如 100ms~150ms）**。
  - 但一旦在列表中按回车将其设为活动服务器，浏览器打开 Google / YouTube 时**无限转圈，完全无法访问，报错 TLS 握手失败 (Curl Code 35)**。
  - 与此同时，切换到 VMess 节点却能正常上网。

## 2. 根因剖析 (两者的水火不容)
1. **v2rayN 的全局配置 (`guiNConfig.json`) 开启了 `"EnableFragment": true`**。
   - 分片（Fragment）机制是在出站上强行注入 `dialerProxy: frag-proxy`。
   - 该功能的设计初衷是用于 **TCP** 协议（如 VMess/VLESS/Trojan），在握手阶段将 Client Hello 按照指定字节长度切碎发送，从而躲避针对 TLS SNI 的深度包检测 (DPI)。
2. **Hysteria2 和 TUIC 属于纯粹的 UDP / QUIC 协议**。
   - QUIC 是在 UDP 数据报上直接封装加密帧的。
   - 当用户将 Hysteria2 / TUIC 的内核配置为 **Xray** 时，Xray 会尝试使用针对 TCP 的 `frag-proxy` 去拆解 QUIC 数据包。
   - 结果：**UDP 握手包被切断损坏，服务端完全无法将其识别为合法的 QUIC 帧并直接丢弃**。客户端收不到服务器响应，发生超时或握手断开 (Code 35)。
3. **为什么测延迟时显示通？**
   - v2rayN 测真连接延迟时调用的是精简版临时测试配置 (`configTest.json`)，**该测试配置并不加载全局路由与分片代理**；一旦作为活动节点正式运行时，全局分片代理强制生效，导致立刻瘫痪。

## 3. 标准解决方案
在客户端进行清晰的协议与内核分工：

### 推荐方案: 启用 Sing-Box 原生内核（v2rayN 已内置）
- 打开 v2rayN 菜单：**设置 ➡️ 参数设置 ➡️ Core类型设置**。
- 将 **Hysteria2** 映射为 **`sing-box`**。
- 将 **TUIC** 映射为 **`sing-box`**。
- **优势**:
  - VMess / VLESS 继续使用 Xray 享受分片防封；
  - Hysteria2 / TUIC 使用官方原生的 Sing-Box 引擎以 UDP 满速狂飙，互不干扰，全协议可用。

### 备选方案: 关闭分片
- 打开 v2rayN 菜单：**设置 ➡️ 参数设置 ➡️ v2ray/xray设置**，取消勾选 **“启用分片 (Enable Fragment)”**。
