# AGENTS.md — Autonomous Coding Agent Handbook

> Universal instruction set for AI coding agents (Claude, OpenAI Codex, Antigravity, Cursor, Windsurf, Copilot) operating on the `s-ui` codebase.

---

## 1. Quick Start & Architecture Orientation
- **Project**: `s-ui` (Sing-Box Web GUI & Multi-Protocol Proxy Controller).
- **Core Technology**: Go 1.22+ backend with embedded Sing-Box core, SQLite/GORM database, Vue 3 + TypeScript frontend.
- **Production Endpoint**: `https://dash.icta.top:2096` (hosted on Tencent Cloud Singapore `124.156.207.253` behind Cloudflare CDN).

---

## 2. Agent Operational Directives & Invariants

| Module / Scope | Key Files | Critical Invariant | Reason & Consequence of Violation |
| :--- | :--- | :--- | :--- |
| **Connection Wrapping** | [`core/tracker_conn.go`](core/tracker_conn.go) | **NEVER add dummy `CloseWrite()` / `CloseRead()` returning `nil`.** Delegate to `network.ExtendedConn`. | Dummy stubs swallow TCP FIN, breaking TCP half-close. Google xjs and YouTube streams freeze at 1MB. |
| **Subscription Delivery** | [`sub/linkService.go`](sub/linkService.go), [`sub/subService.go`](sub/subService.go) | **Sanitize `*.qzz.io` to `dash.icta.top`.** Filter `http2://`. Deduplicate entries. | `qzz.io` is unresolvable/blocked. Raw output breaks client connections. |
| **DNS Resolution** | [`service/config.go`](service/config.go) | **Inject public DNS (`8.8.8.8`, `1.1.1.1`) into Sing-Box config.** | Cloud server local DNS fails on international domains, stalling outbounds. |
| **Address Formatting** | [`service/nodetest.go`](service/nodetest.go) | **Use `net.JoinHostPort(host, port)`** for target address construction. | Raw `fmt.Sprintf("%s:%s")` breaks IPv6 syntax with "too many colons in address". |
| **Client Diagnostics** | Client Configuration | **v2rayN Hysteria2 & TUIC must use `sing-box` core (`CoreType: 24`).** | Xray `frag-proxy` corrupts UDP QUIC packets, causing TLS handshake failure. |
| **CDN & SSL** | Deployment Architecture | **Panel port is `2096` (Cloudflare CDN whitelist). SSL mode: Full.** | Cloudflare drops non-whitelisted ports (521/522) and rejects self-signed certs in Full Strict (526). |

---

## 3. Package Documentation Index (Source-Adjacent Docs)
For in-depth module architecture and rules, consult the READMEs located directly alongside the source code:
- [`core/README.md`](core/README.md): Stream half-close mechanics, buffer delegation, and connection tracking.
- [`sub/README.md`](sub/README.md): Link sanitization pipeline, protocol filters, and client routing quirks.
- [`service/README.md`](service/README.md): Sing-Box config sanitizer, public DNS fallback, and TLS/acme.sh integration.
- [`docs/knowledge_base/`](docs/knowledge_base/README.md): Modular engineering knowledge base.
- [`TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md`](TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md): Comprehensive historical troubleshooting log.
