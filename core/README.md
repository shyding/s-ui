# Core 模块架构与研发规范 (Core Architecture & Engineering Notes)

> 本文档专为后续开发者以及 **Claude (Claude Code)**、**Codex** 等 AI Agent 编写，明确记录 `core/` 模块的设计初衷、关键架构约束与致命避坑指南。

---

## 1. 模块职责 (Responsibilities)
`core/` 目录负责管理底层 Sing-Box 核心实例的生命周期、配置装载、连接追踪以及流量统计。

主要文件：
- `core.go`: Sing-Box 实例构建、启动、热重启与重载控制。
- `tracker_conn.go`: 自定义连接追踪器（`ConnTracker`），劫持并包装 Sing-Box 的入站/出站连接（`net.Conn` 与 `network.PacketConn`），统计入站活跃连接并提供按入站 Tag 强制断开连接能力（`CloseConnByInbound`）。

---

## 2. 致命避坑：TCP 半关闭 (Half-Close) 与“零存根”铁律

### 2.1 故障历史回顾（1MB 截断与 Google / YouTube 卡死）
在早期版本中，`wrappedConn`（包装原生连接的结构体）为了兼容性，曾经实现了空存根方法：
```go
// ❌ 致命错误实现！绝对严禁再次引入！
func (w *wrappedConn) CloseWrite() error {
    return nil // 伪装实现，静默吃掉 TCP FIN
}
func (w *wrappedConn) CloseRead() error {
    return nil
}
```
**故障根因**：
1. 当客户端通过代理访问 Google 或播放 YouTube 视频时，HTTP/2 与现代 HTTP/1.1 连接高度依赖 TCP 半关闭（Half-Close）机制（即客户端发送完 HTTP 请求体后，调用 `CloseWrite()` 发送 TCP FIN 分节，表示“我已写完请求，现在等待服务端响应全文”）。
2. Sing-Box 在转发双向数据流时，检测到底层连接实现了 `CloseWrite()`，便调用它来结束写入。
3. 但空存根仅返回 `nil`，并没有将真正的 FIN 分节发送给底层 OS Socket / 上游连接！
4. 上游 Google CDN 服务端迟迟没有收到 FIN，认为客户端请求体尚未传输完毕，进入等待挂起状态；客户端也在等待响应，导致连接彻底死锁！
5. 在浏览器中表现为：网页小文件秒开，但在加载超过 1MB 的资源（如 Google `xjs` 脚本、搜索下拉补全、YouTube 视频分片）时彻底转圈超时。

### 2.2 正确实现规范
当前 `core/tracker_conn.go` 的正确实现：
```go
type wrappedConn struct {
	network.ExtendedConn
	connID string
}

func (w *wrappedConn) Close() error {
	connTracker.untrackConnection(w.connID)
	return w.ExtendedConn.Close()
}

func (w *wrappedConn) Upstream() any {
	return w.ExtendedConn
}

func (w *wrappedConn) UpstreamReader() any {
	return w.ExtendedConn
}

func (w *wrappedConn) UpstreamWriter() any {
	return w.ExtendedConn
}

func (w *wrappedConn) ReaderReplaceable() bool {
	return true
}

func (w *wrappedConn) WriterReplaceable() bool {
	return true
}
```

### 2.3 开发与 AI Agent 维护铁律
1. **严禁在 `wrappedConn` 上添加返回 `nil` 的 `CloseWrite()` 或 `CloseRead()` 虚假存根**！
2. `wrappedConn` 内嵌了 `github.com/sagernet/sing/common/network.ExtendedConn`（由 `bufio.NewExtendedConn(conn)` 创建），它自身已经具备标准且完备的半关闭、缓冲区转发及系统调用透传能力。
3. 必须通过 `UpstreamReader()` / `UpstreamWriter()` 暴露内层的 `ExtendedConn`，并让 `ReaderReplaceable()` / `WriterReplaceable()` 返回 `true`，以允许 Sing-Box 核心的高性能 Buffer 优化机制正常工作。

---

## 3. 关联知识库
- 深度技术剖析详见项目知识库：[`docs/knowledge_base/01_core_stream_halfclose.md`](../docs/knowledge_base/01_core_stream_halfclose.md)
- 全局备忘录：[`TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md`](../TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md)
