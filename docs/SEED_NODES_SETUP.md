# Seed Nodes Configuration (≥300 required)

## Overview

The "Local v2rayN Seed Nodes" subscription provides high-quality seed nodes
that are prioritized in the subscription output. The system requires **at least
300 seed nodes** to meet the quantity target.

## Configuration

Set the `SUI_SEED_NODES_FILE` environment variable to the absolute path of a
file containing seed node URIs.

### Systemd (bare metal)

Edit `/etc/systemd/system/s-ui.service`:
```ini
[Service]
Environment=SUI_SEED_NODES_FILE=/usr/local/s-ui/seed/nodes.txt
```

Then reload and restart:
```bash
sudo systemctl daemon-reload
sudo systemctl restart s-ui
```

### Docker Compose

1. Place your seed file at `./seed/nodes.txt` (relative to docker-compose.yml)
2. Uncomment the volume and environment lines in `docker-compose.yml`:
```yaml
volumes:
  - "./seed/nodes.txt:/app/seed/nodes.txt:ro"
environment:
  - SUI_SEED_NODES_FILE=/app/seed/nodes.txt
```

## File Format

- Plain text, UTF-8
- One node URI per line
- Supported schemes: `vless://`, `vmess://`, `trojan://`, `ss://`, `hy2://`, `hysteria2://`, `tuic://`, `socks5://`
- Empty lines and lines starting with `#` are ignored
- Example:
```
# Tokyo seed nodes
vless://uuid@1.2.3.4:443?security=reality&...#Seed-日本-关东-东京-01
trojan://password@5.6.7.8:443?sni=...#Seed-美国-加州-洛杉矶-01
```

## Requirements

- **Minimum 300 nodes** (system target)
- Each node must pass the three-stage health check:
  1. Client → VPS ingress (TCP/TLS handshake)
  2. VPS ingress → egress (proxy protocol handshake)
  3. VPS egress → target site (HTTP 200, ≤650ms, real landing IP verified)
- Nodes are grouped by (country, region, city), max 50 per city group
- Total Seed region cap: 500 nodes

## Verification

After configuring, check the subscription:
```bash
curl -s https://your-domain:2096/sub/my | grep -c "^Seed-"
```

Or check the database:
```sql
SELECT COUNT(*) FROM outbounds WHERE provider='seed' AND enabled=1;
```

## Notes

- If `SUI_SEED_NODES_FILE` is unset or empty, the Seed subscription is disabled
  (no error, just 0 seed nodes).
- The file is read at startup and on subscription update (every 24h by default).
- For a fresh deploy without seed nodes, the subscription will contain 0 Seed
  nodes until the file is configured. This is intentional (FAIL-CLOSED).
