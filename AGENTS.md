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
| **Subscription Quality & Quantity** | [`docs/SUI_QUALITY_AND_QUANTITY_RULES.md`](docs/SUI_QUALITY_AND_QUANTITY_RULES.md), [`sub/linkService.go`](sub/linkService.go) | **>=1000 nodes total, S-UI native >=15~20 protocol combinations, 100% connect (0 -1), <=650ms latency, remarks 100% Chinese/digits (0 English except provider, 0 unknown).** | Low node count, single protocol, high latency, or English/unknown remarks violate core user requirements. |
| **CDN & SSL** | Deployment Architecture | **Panel port is `2096` (Cloudflare CDN whitelist). SSL mode: Full.** | Cloudflare drops non-whitelisted ports (521/522) and rejects self-signed certs in Full Strict (526). |


## 4. Build Requirements (Permanent Memory, verified 2026-10-01)

**Go version: 1.26.x ONLY**
- Go 1.22 too old: `go.mod` requires `go >= 1.26.0`
- Go 1.27 incompatible: sing-box v1.12.x tailscale protocol code conflicts with Go 1.27 build constraints
- Install: `wget https://go.dev/dl/go1.26.0.linux-amd64.tar.gz && tar -xzf go1.26.0.linux-amd64.tar.gz`

**Build tags MUST include `with_gvisor`**
- `sing-tun/stack_gvisor.go` has `//go:build with_gvisor` constraint
- Without it: `undefined: tun.DefaultNIC`, `tun.NewTCPForwarder`, `tun.NewUDPForwarder`
- Full tags: `-tags "with_quic,with_grpc,with_utls,with_acme,with_gvisor"`

**Dependency pin: `sing-tun v0.7.13`**
- v0.7.3 lacks gvisor symbols required by sing-box v1.12.14
- Pinned in `go.mod` as indirect dependency

**TMPDIR/GOCACHE must NOT be /tmp**
- `/tmp` is tmpfs (~1.9G), CGO sqlite3 build fills it → "disk quota exceeded"
- Set: `export TMPDIR=~/tmp GOCACHE=~/.cache/go-build`

**Build from scratch (new system):**
1. Install Go 1.26 (see above)
2. `mkdir -p ~/tmp ~/.cache/go-build && export TMPDIR=~/tmp GOCACHE=~/.cache/go-build`
3. `./build.sh` (handles frontend + backend)

---

## 5. Package Documentation Index (Source-Adjacent Docs)
For in-depth module architecture and rules, consult the READMEs located directly alongside the source code:
- [`core/README.md`](core/README.md): Stream half-close mechanics, buffer delegation, and connection tracking.
- [`sub/README.md`](sub/README.md): Link sanitization pipeline, protocol filters, and client routing quirks.
- [`service/README.md`](service/README.md): Sing-Box config sanitizer, public DNS fallback, and TLS/acme.sh integration.
- [`docs/knowledge_base/`](docs/knowledge_base/README.md): Modular engineering knowledge base.
- [`TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md`](TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md): Comprehensive historical troubleshooting log.
