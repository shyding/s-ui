# 01. 核心流控制: VMess/WebSocket 单向半关闭阻塞修复

## 1. 故障背景
用户使用 VMess (WebSocket 传输) 节点访问外网时：
- 访问 Google 首页能秒开基础 HTML；
- 但加载大体积资源（如 1.11MB 的 Google `xjs` 脚本集合、CSS、图片、YouTube 视频流）时，浏览器标签页处于长时间转圈状态，耗时达到 15 秒以上直至连接超时断开。

## 2. 根因剖析
在 `core/tracker_conn.go` 中，为了统计客户端连接而设计了 `wrappedConn`：
```go
type wrappedConn struct {
    network.ExtendedConn
    connID string
}
```
原本的实现中：
1. 错误地在结构体上实现了空的 `CloseWrite()` 与 `CloseRead()` 方法：
   ```go
   func (w *wrappedConn) CloseWrite() error { return nil }
   func (w *wrappedConn) CloseRead() error { return nil }
   ```
2. Sing-Box 的 `bufio.CopyConn` 内部包含对 `UpstreamReader()` 与 `UpstreamWriter()` 的类型断言。当对端发送 EOF 时，由于这两个空方法的拦截，连接既无法感知底层 `ExtendedConn` 的半关闭意图，也无法向下传递 FIN 信令。
3. WebSocket 流在非双工传输场景下因此持续挂起，对端一直在等待最后的关闭确认，导致大文件接收悬空。

## 3. 终极解决方案 (Commit `ce66345`)
1. **彻底移除** `wrappedConn` 上的 dummy `CloseWrite()` 与 `CloseRead()` 方法。
2. 完整暴露内层 `ExtendedConn` 的接口代理：
   ```go
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
3. 保持数据流能够直接穿透到内核的完整 ExtendedConn 读写闭环。

## 4. 效果对比
- **修复前**: 1.11MB `xjs` 脚本请求挂起 15 秒并超时中断。
- **修复后**: 1.11MB `xjs` 脚本在 2.73 秒内全量下载完成，YouTube 2.74 秒加载，Google 全面秒开。
