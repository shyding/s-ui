# 13. SQLite/GORM JSON 字段扫描兼容性

## 适用场景

当日志出现以下错误时，优先检查 GORM 模型的 JSON 字段声明：

```text
sql: Scan error on column index N, name "inbounds":
unsupported Scan, storing driver.Value type string into type *json.RawMessage
```

该问题常见于 `github.com/glebarez/sqlite`：SQLite 中的 JSON 实际存储为 `TEXT`，驱动读取时可能返回 `string`，而不是 `[]byte`。

## Evidence → Finding → Path

### Evidence

- 生产错误明确指出列 `inbounds` 的数据库值类型为 `string`。
- `database/model/model.go` 中 `Client.Inbounds` 原先直接声明为 `json.RawMessage`。
- 同一个 `Client` 模型的 `config`、`inbounds`、`links` 均为 JSON 文本字段。
- 回归测试使用 SQLite `TEXT` 值插入 `[1,2]`，并成功查询回 `json.RawMessage`。

### Finding

问题不是订阅内容或节点协议错误，而是 GORM 默认扫描路径没有把 SQLite 返回的字符串转换成 `json.RawMessage`。直接将字段类型改为 `string` 会破坏现有 `json.Unmarshal` 调用，不应采用。

### Path

为 JSON 字段添加 GORM JSON 序列化器：

```go
Config   json.RawMessage `gorm:"serializer:json"`
Inbounds json.RawMessage `gorm:"serializer:json"`
Links    json.RawMessage `gorm:"serializer:json"`
```

GORM 的 `serializer:json` 同时处理 SQLite 返回的 `string`、`[]byte` 和 `NULL`，并保留 `json.RawMessage` 公共接口，因此不会要求业务层改写现有 `json.Unmarshal` 调用。

## 固化规则

1. 所有模型中的 JSON 文本字段必须显式使用 `gorm:"serializer:json"`。
2. 不要为了绕过扫描错误把 JSON 字段改成 `string`；这会把解析责任泄漏到所有调用方。
3. 新增或修改 JSON 字段时，必须覆盖 `string` 数据库值的 SQLite 回归测试。
4. 修改模型后必须运行：

```powershell
go test ./database/model -run TestClientJSONFieldsScanFromSQLiteText -count=1
go test -tags 'with_quic,with_grpc,with_utls,with_acme,with_gvisor' ./database/... ./service/... ./sub/...
```

## 已验证修复

- 修复提交：`43c2fb4`（`fix sqlite JSON field scanning`）
- 回归测试：`TestClientJSONFieldsScanFromSQLiteText` 通过
- 覆盖字段：`Client.Config`、`Client.Inbounds`、`Client.Links`

## 相关节点测速故障的排查基线

固定 SUI 节点出现少量 `-1` 时，不能只看端口是否开放，必须区分传输层和认证层：

| 节点 | 根因 | 修复位置 |
|---|---|---|
| VMess HTTPUpgrade（54149） | 服务端旧配置同时声明 `h2`，HTTPUpgrade 实际只接受 HTTP/1.1 | `service/sui_nodes.go`：HTTPUpgrade 专用 ALPN |
| VMess HTTPUpgrade（54168） | 健康检查器绕过了 WS/HTTPUpgrade 包装，直接在裸 TCP 上检查 VMess | `service/node_health_worker.go`：VMess 先包装传输 |
| Mixed/SOCKS（54156） | 已轮换的 mixed/socks 用户密码可能仍留在旧 URI | `sub/linkService.go`：按当前客户端配置同步凭据 |
| WireGuard（54181/54182） | 云主机出口网卡名不固定，旧实现硬编码 `eth0`；反向路径过滤也可能丢弃隧道回包 | `service/sui_wireguard.go`：按路由探测出口网卡并关闭 rp_filter |

WireGuard 的正确链路是：客户端 URI → 公网 UDP 54181/54182 → Linux 原生
WireGuard 接口 `suiwg54181/54182` → peer 的隧道地址 → 默认出口网卡 NAT。它不是
普通 TCP 端口，不能用 TCP connect 或随机 UDP 回包作为应用层成功证明；验证时要同时
检查监听端口、`wg show` 的 latest-handshake、隧道地址路由和 POSTROUTING MASQUERADE。
