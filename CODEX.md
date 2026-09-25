# CODEX.md — S-UI Codex Agent Operational Rules & Architecture Guide

> This specification is designated for **OpenAI Codex**, **Codex Runner**, and automated agent systems working on the `s-ui` codebase. It establishes operational constraints, architectural invariants, and mandatory protocols.

---

## 1. Project Context & Environment
- **Project**: `s-ui` (Sing-Box Web Panel & Traffic Orchestrator)
- **Primary Language**: Go 1.22+ (Backend), Vue 3 / TypeScript (Frontend)
- **Production Server**: Ubuntu 22.04 LTS (`124.156.207.253`, domain: `dash.icta.top`, port: `2096`)
- **Key Modules**:
  - `core/`: Sing-Box instance lifecycle, traffic metrics, connection wrapping (`wrappedConn`).
  - `sub/`: Link harvesting, sanitization (`linkService.go`), subscription formats (`subService.go`, `clashService.go`, `jsonService.go`).
  - `service/`: Sing-Box configuration generation (`config.go`), node speed/landing tests (`nodetest.go`), TLS/acme.sh certificates (`tls.go`).

---

## 2. Non-Negotiable Engineering Invariants (Zero-Stub & Strict Rules)

### Rule 1: Zero Dummy Stubs & Mandatory Stream Half-Close in `core/tracker_conn.go`
- **Location**: [`core/tracker_conn.go`](core/tracker_conn.go)
- **Constraint**: Under NO circumstances should dummy stub implementations of `CloseWrite()` or `CloseRead()` returning `nil` be added to `wrappedConn`.
- **Failure Mode**: Returning `nil` swallows TCP FIN packets. Upstream Google CDN servers hang waiting for request termination, freezing all transfers >1MB (Google search `xjs`, YouTube video streams).
- **Required Architecture**: `wrappedConn` embeds `network.ExtendedConn` (`bufio.NewExtendedConn(conn)`), exposes `UpstreamReader()` and `UpstreamWriter()`, and delegates buffer replacement via `ReaderReplaceable() = true` and `WriterReplaceable() = true`.
- **Reference**: See [`core/README.md`](core/README.md) and [`docs/knowledge_base/01_core_stream_halfclose.md`](docs/knowledge_base/01_core_stream_halfclose.md).

### Rule 2: In-Memory Subscription Sanitization in `sub/linkService.go`
- **Location**: [`sub/linkService.go`](sub/linkService.go), [`sub/subService.go`](sub/subService.go)
- **Constraint**:
  1. Dead/blocked legacy domains (`dash.icta.qzz.io`, `sub.icta.qzz.io`) must be dynamically replaced with `dash.icta.top`.
  2. Incompatible `http2://` (NaiveProxy) schemes must be omitted from output subscriptions.
  3. Nodes must be deduplicated via `seen[cleanUri]`.
- **Reference**: See [`sub/README.md`](sub/README.md) and [`docs/knowledge_base/02_subscription_domain_sanitization.md`](docs/knowledge_base/02_subscription_domain_sanitization.md).

### Rule 3: Upstream Public DNS Injection in `service/config.go`
- **Location**: [`service/config.go`](service/config.go) (`sanitizeConfig`)
- **Constraint**: Sing-Box configuration must contain public resolvers (`8.8.8.8` Google DNS and `1.1.1.1` Cloudflare DNS).
- **Reason**: Overcomes regional DNS timeouts and resolver blocking on cloud provider local resolvers.
- **Reference**: See [`service/README.md`](service/README.md) and [`docs/knowledge_base/04_dns_public_resolver_fallback.md`](docs/knowledge_base/04_dns_public_resolver_fallback.md).

### Rule 4: Client Diagnostic Axiom (v2rayN Xray Fragment vs QUIC)
- **Phenomenon**: Ping test passes (`Ctrl+R` green), but browser fails to connect to Google (`ERR_CONNECTION_CLOSED` / Curl error 35).
- **Root Cause**: v2rayN client enabled `EnableFragment: true` and routed Hysteria2 or TUIC through Xray core. Xray's `frag-proxy` mangles UDP QUIC datagrams.
- **Resolution**: Reconfigure client v2rayN to use `sing-box` core (`CoreType: 24`) for Hysteria2 and TUIC.
- **Reference**: See [`docs/knowledge_base/03_client_xray_fragment_vs_quic.md`](docs/knowledge_base/03_client_xray_fragment_vs_quic.md).

### Rule 5: Cloudflare CDN & acme.sh Port Restrictions
- **Cloudflare CDN Allowed Ports**: 443, 2053, 2083, 2087, 2096, 8443. The panel listens on `2096`. Do not change to unproxied ports.
- **Cloudflare SSL 526**: Cloudflare SSL mode must be "Full", not "Full (strict)" when using self-signed or non-public CA certs.
- **acme.sh Port 80**: Standalone mode conflicts with Nginx on port 80.
- **Reference**: See [`docs/knowledge_base/06_domain_migration_and_ssl_526.md`](docs/knowledge_base/06_domain_migration_and_ssl_526.md).

### Rule 6: Single-Port Egress Multiplexing & User-Space WireGuard (ProtonVPN / WARP)
- **Constraint**:
  1. Sing-Box single port (e.g. `2096`) multiplexes multiple egress countries (US, JP, NL, SG) using deterministic RFC 4122 UUIDv5 derived credentials (`DeriveUUID(baseUUID, regionCode)`).
  2. WireGuard endpoints must enforce `system: false` (gVisor user-space mode) to prevent touching host routing tables and physical interfaces.
  3. Multi-node ProtonVPN clusters must be wrapped in `urltest` outbound pools (interval: 3m, tolerance: 50) for automated failover.
  4. User traffic statistics must strip region suffixes (`-us`, `-jp`, `-nl`) and roll up 100% to the root user `admin`.
- **Reference**: See [`docs/knowledge_base/08_single_port_parameterized_egress_protonvpn.md`](docs/knowledge_base/08_single_port_parameterized_egress_protonvpn.md).

### Rule 7: Zero Manual Token/Cookie Copying & Simulated Browser Automation
- **Constraint**:
  1. **Strictly Forbidden**: Asking users to manually inspect DevTools (F12) to copy-paste Bearer tokens or Cookies is prohibited.
  2. **Automated Harvesting**: Use Playwright browser automation (`scripts/proton_harvester.py`) with dedicated isolated profile (`~/.sui_proton_profile`).
  3. **Interactive & Background Modes**: `sui proton -browser` displays Chrome for one-click authentication; `sui proton -auto` executes background headless node refreshes.
  4. **Pure Go SQLite**: Use `github.com/glebarez/sqlite` to maintain 100% CGO-free portability across Windows and Linux.
- **Reference**: See [`docs/knowledge_base/09_protonvpn_automated_browser_harvester.md`](docs/knowledge_base/09_protonvpn_automated_browser_harvester.md).

---

## 3. Build & CI/CD Guardrails
- **Backend Build**:
  `go build -v -tags "with_gvisor with_quic with_wireguard with_ech with_utls with_clash_api" -o s-ui main.go`
- **Frontend Build**:
  `cd frontend && npm install && npm run build`
- **Deployment Safety**:
  `.github/workflows/deploy.yml` uses path filters for `.go`, `go.mod`, `go.sum`, and `frontend/**`. Documentation commits (`*.md`, `docs/**`, `CLAUDE.md`, `CODEX.md`, `AGENTS.md`) **do not** trigger builds. Prefix documentation commits with `docs:`.

---

## 4. Documentation & Knowledge Base Maintenance Protocol
Every architectural adjustment or bug resolution verified with the user must be committed to:
1. The relevant package doc (`core/README.md`, `sub/README.md`, or `service/README.md`).
2. The root memo (`TROUBLESHOOTING_AND_ARCHITECTURE_MEMO.md`).
3. The modular deep dives in `docs/knowledge_base/`.
4. Synchronized in `CLAUDE.md`, `CODEX.md`, and `AGENTS.md`.
