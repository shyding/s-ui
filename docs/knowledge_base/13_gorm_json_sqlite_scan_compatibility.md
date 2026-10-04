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

