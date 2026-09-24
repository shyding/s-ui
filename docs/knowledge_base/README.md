# S-UI 核心工程知识库 (Engineering Knowledge Base)

本文档库归档了 `s-ui` 高性能代理面板在服务治理、连接跟踪、订阅分发、协议互通及客户端兼容性调试等方面的全部深度工程实践与避坑准则。

## 目录索引 (Index)

- [01. 核心流控制: VMess/WebSocket 单向半关闭阻塞修复](01_core_stream_halfclose.md)
  - 阐述 `wrappedConn` 导致大文件挂起转圈的根本原因与接口代理实现。
- [02. 订阅系统: 历史死域名动态重定向与非标协议过滤](02_subscription_domain_sanitization.md)
  - 阐述订阅解析中的 NXDOMAIN 死域名过滤、http2 协议清理以及哈希去重方案。
- [03. 客户端适配: Xray Fragment (TLS分片) 与 UDP/QUIC 的底层冲突铁律](03_client_xray_fragment_vs_quic.md)
  - 深度剖析“真连接测速有绿色延迟、设为主节点打不开 Google”的致命原因与内核分工方案。
- [04. 域名解析: 服务端公共递归 DNS 回退与机房防污染](04_dns_public_resolver_fallback.md)
  - 阐述 `sanitizeConfig` 注入公共 DNS (`8.8.8.8` / `1.1.1.1`) 的机制与必要性。
- [05. 传输加速: 宿主机 Google BBR 与 TCP 缓冲区调优](05_server_bbr_tcp_tuning.md)
  - 阐述 16MB TCP 缓冲区与 FQ + BBR 算法的生产配置。
- [06. 域名迁移与证书运维: Cloudflare 526 根因与 80 端口冲突排查](06_domain_migration_and_ssl_526.md)
  - 阐述历史旧域名迁移全链路避坑、acme.sh 独立签发与 80 端口释放、以及 Cloudflare 端口兼容性准则。

---

## 快速回顾总览

完整全景备忘录可参考项目根目录的：[`../../TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md`](../../TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md)。
