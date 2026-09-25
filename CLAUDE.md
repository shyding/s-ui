# CLAUDE.md — S-UI AI Assistant & Engineering Guide

> This document is automatically loaded by **Claude (Claude Code)** when working on the `s-ui` codebase. It outlines the project architecture, operational workflows, and non-negotiable engineering invariants.

---

## 1. Project Overview & Technology Stack
**s-ui** is a modern Sing-Box web panel and proxy node manager.
- **Backend**: Go 1.22+ (`main.go`, Gin HTTP framework, SQLite / GORM, Sing-Box core API).
- **Frontend**: Vue 3 + TypeScript + Vite + Vuetify (`frontend/`).
- **Production Server**: Ubuntu 22.04 LTS (Tencent Cloud Singapore, IP: `124.156.207.253`, Active Domain: `dash.icta.top`, Web Port: `2096`).
- **Remote Repo**: `origin/main` on GitHub (`alireza0/s-ui` fork).

---

## 2. Development, Build & CI/CD Commands

### 2.1 Backend Build
```bash
# Build binary with full feature tags (TUN, QUIC, WireGuard, ECH, uTLS, Clash API)
go build -v -tags "with_gvisor with_quic with_wireguard with_ech with_utls with_clash_api" -o s-ui main.go

# Run unit tests
go test ./service/ -v -run TestNodeTest
```

### 2.2 Frontend Build
```bash
cd frontend
npm install
npm run build
```

### 2.3 CI/CD & GitHub Actions Invariant
- Workflow file: `.github/workflows/deploy.yml`
- Path filter:
  ```yaml
  paths:
    - '**.go'
    - 'go.mod'
    - 'go.sum'
    - 'frontend/**'
    - '.github/workflows/deploy.yml'
  ```
- **CRITICAL**: Commits that only modify documentation (`*.md`, `docs/**`, `CLAUDE.md`, `CODEX.md`, `AGENTS.md`) **DO NOT** trigger remote redeployments. When committing documentation-only changes, always use `docs:` commit prefixes.

---

## 3. Non-Negotiable Architectural Invariants & Fatal Pitfalls

When modifying or refactoring this codebase, **Claude MUST strictly adhere to the following rules**:

### Rule 1: TCP Stream Half-Close & Zero Dummy Stubs in `core/`
- **Location**: [`core/tracker_conn.go`](core/tracker_conn.go)
- **Invariant**: **NEVER** add dummy/stub methods like `func (w *wrappedConn) CloseWrite() error { return nil }` or `func (w *wrappedConn) CloseRead() error { return nil }`.
- **Reason**: HTTP/2, Google `xjs` scripts, and YouTube video chunks rely on TCP Half-Close (FIN packet) to signal request completion. A dummy `CloseWrite()` swallows the FIN packet without transmitting it to the upstream server, causing the upstream Google CDN to hang indefinitely. This causes all transfers >1MB to freeze.
- **Implementation**: `wrappedConn` embeds `network.ExtendedConn` (`bufio.NewExtendedConn(conn)`), exposes `UpstreamReader()` and `UpstreamWriter()`, and sets `ReaderReplaceable() = true` and `WriterReplaceable() = true`.
- **Module Doc**: See [`core/README.md`](core/README.md) and [`docs/knowledge_base/01_core_stream_halfclose.md`](docs/knowledge_base/01_core_stream_halfclose.md).

### Rule 2: Dynamic Subscription Domain Sanitization & Protocol Filtering in `sub/`
- **Location**: [`sub/linkService.go`](sub/linkService.go), [`sub/subService.go`](sub/subService.go)
- **Invariant**:
  1. All links containing unresolvable/blocked legacy domains (`dash.icta.qzz.io`, `sub.icta.qzz.io`) **MUST** be dynamically replaced with the active domain (`dash.icta.top`).
  2. Obsolete protocols like `http2://` (NaiveProxy) **MUST** be filtered out because mainstream clients (v2rayN, Clash) cannot import them and will crash or throw errors.
  3. External and local links must be deduplicated (`seen[cleanUri]`).
- **Module Doc**: See [`sub/README.md`](sub/README.md) and [`docs/knowledge_base/02_subscription_domain_sanitization.md`](docs/knowledge_base/02_subscription_domain_sanitization.md).

### Rule 3: Public DNS Fallback Injection in `service/`
- **Location**: [`service/config.go`](service/config.go) (`sanitizeConfig`)
- **Invariant**: Sing-Box configuration generation must guarantee that `dns.servers` contains public upstream resolvers (`udp://8.8.8.8` and `udp://1.1.1.1`).
- **Reason**: Cloud provider internal DNS resolvers frequently fail to resolve international proxy domains. Public fallbacks prevent proxy routing failure.
- **Module Doc**: See [`service/README.md`](service/README.md) and [`docs/knowledge_base/04_dns_public_resolver_fallback.md`](docs/knowledge_base/04_dns_public_resolver_fallback.md).

### Rule 4: IPv6 Address Formatting in `service/nodetest.go`
- **Location**: [`service/nodetest.go`](service/nodetest.go)
- **Invariant**: Always format host and port with `net.JoinHostPort(host, portStr)`. Never use raw `fmt.Sprintf("%s:%s", host, port)`, which breaks on IPv6 addresses (e.g. `2400:cb00:...` results in `too many colons in address`).

### Rule 5: Client Incompatibility Knowledge (v2rayN Xray Fragment vs QUIC)
- **Issue**: Real ping (`Ctrl+R`) shows low latency (green), but browser cannot open Google (`ERR_CONNECTION_CLOSED` / TLS Handshake Failure).
- **Cause**: Client v2rayN enabled `"EnableFragment": true` and assigned Hysteria2 / TUIC to **Xray** core. Xray's `frag-proxy` chops TCP packets to evade SNI blocking, but it mangles UDP QUIC datagrams.
- **Fix**: In v2rayN, set Hysteria2 and TUIC core to **`sing-box`** (`CoreType: 24`).
- **Doc**: See [`docs/knowledge_base/03_client_xray_fragment_vs_quic.md`](docs/knowledge_base/03_client_xray_fragment_vs_quic.md).

### Rule 6: Cloudflare CDN, Port Matrix & acme.sh Rules
- **CDN Ports**: Cloudflare only proxies HTTPS traffic on: `443`, `2053`, `2083`, `2087`, `2096`, `8443`. S-UI listens on port `2096`.
- **SSL Error 526**: Cloudflare SSL mode must be set to "Full", NOT "Full (strict)" if self-signed origin certificates are used.
- **acme.sh Port 80**: Standalone mode conflicts with Nginx on port 80. Nginx must be stopped or `--webroot` used.
- **Doc**: See [`docs/knowledge_base/06_domain_migration_and_ssl_526.md`](docs/knowledge_base/06_domain_migration_and_ssl_526.md).

### Rule 7: Single-Port Egress Multiplexing & User-Space WireGuard
- **Single Port Inbound**: Single port (e.g. `2096`) handles all traffic; routes to country exits via deterministic RFC 4122 UUIDv5 derived credentials (`DeriveUUID`).
- **User-Space WireGuard**: ProtonVPN / WARP endpoints must enforce `system: false` (gVisor pure user-space).
- **URLTest Failover Pool**: Multi-endpoint groups must use `urltest` (interval: 3m, tolerance: 50) for auto-failover.
- **Unified Traffic Rollup**: Region-derived user traffic tags (`admin-us`, `admin-jp`, `admin-nl`) automatically roll up to root user `admin`.
- **Doc**: See [`docs/knowledge_base/08_single_port_parameterized_egress_protonvpn.md`](docs/knowledge_base/08_single_port_parameterized_egress_protonvpn.md).

---

## 4. Codebase Navigation Map
```
i:\learn_code\s-ui\
├── main.go                       # Application entry point
├── CLAUDE.md                     # Claude Code guidance (this file)
├── CODEX.md                      # OpenAI Codex guidance
├── AGENTS.md                     # General AI Agent specifications
├── README.md                     # Project README with knowledge base index
├── TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md # Master troubleshooting ledger
├── core/
│   ├── README.md                 # Core connection wrapper & half-close guide
│   ├── core.go                   # Sing-Box instance manager
│   └── tracker_conn.go           # Connection tracker, wrappedConn & ExtendedConn
├── sub/
│   ├── README.md                 # Subscription generation & sanitization guide
│   ├── linkService.go            # Link aggregation, sanitization & multi-egress expansion
│   ├── subService.go             # Base64 subscription encoder
│   ├── clashService.go           # Clash YAML configuration generator
│   └── jsonService.go            # Sing-Box JSON client outbound generator
├── service/
│   ├── README.md                 # Service architecture, DNS & TLS guide
│   ├── config.go                 # Sing-Box config sanitizer & persistence
│   ├── egress_multiplex.go       # Egress multiplexing, UUIDv5 & WireGuard parser
│   ├── inbounds.go               # Inbound protocol handlers & derived user expansion
│   ├── nodetest.go               # Latency & landing IP testing
│   ├── stats.go                  # User traffic aggregation & rollup
│   └── tls.go                    # Certificate management
└── docs/knowledge_base/          # Modular deep-dive architectural records
    ├── 01_core_stream_halfclose.md
    ├── 02_subscription_domain_sanitization.md
    ├── 03_client_xray_fragment_vs_quic.md
    ├── 04_dns_public_resolver_fallback.md
    ├── 05_server_bbr_tcp_tuning.md
    ├── 06_domain_migration_and_ssl_526.md
    ├── 07_cloudflare_warp_dedicated_egress.md
    ├── 08_single_port_parameterized_egress_protonvpn.md
    └── README.md
```

---

## 5. Mandatory Knowledge Base Protocol
Whenever you implement a fix, resolve an issue, or alter configuration:
1. Verify the change thoroughly.
2. Update the corresponding package `README.md` (`core/`, `sub/`, or `service/`).
3. If it is an architectural breakthrough or critical bugfix, document it in `docs/knowledge_base/`.
4. Keep `TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md` up to date.
