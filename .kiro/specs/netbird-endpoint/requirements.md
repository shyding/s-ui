# Requirements Document

## Introduction

本功能为 s-ui（sing-box 管理面板）新增 **NetBird** 节点（Endpoint）类型。NetBird 通过官方 embed SDK（`github.com/netbirdio/netbird/client/embed`，BSD-3-Clause）以独立进程方式运行，不经过 sing-box 的 endpoint registry，生命周期与 sing-box 保持一致。前端新增对应的配置表单，后端新增 NetbirdService 管理 embed.Client 实例，并在 sing-box 启停时同步启停所有 NetBird 节点。

---

## Glossary

- **NetBird_Client**：`github.com/netbirdio/netbird/client/embed` 提供的 `embed.Client`，负责建立 NetBird 网络连接。
- **NetbirdService**：新建的 Go 服务（`service/netbird.go`），负责管理所有 NetBird_Client 实例的生命周期。
- **EndpointService**：现有的 `service/endpoints.go` 中的服务，处理所有节点类型的 CRUD，将嵌入 NetbirdService。
- **ConfigService**：现有的 `service/config.go` 中的服务，管理 sing-box 启停，需同步调用 NetbirdService。
- **Endpoint_Model**：`database/model/endpoints.go` 中的 `Endpoint` 结构体，存储节点配置。
- **Setup_Key**：NetBird 认证方式之一，用于设备注册。
- **JWT_Token**：NetBird 认证方式之一，用于用户级认证。
- **Private_Key**：NetBird 认证方式之一，用于密钥认证。
- **Management_URL**：NetBird 管理服务器地址，默认为 `https://api.netbird.io:443`。
- **Config_Path**：NetBird 客户端配置文件路径，默认为 `./db/netbird-{tag}.json`，空值表示内存模式。
- **Userspace_Mode**：`NoUserspace: false` 时的运行模式，无需 root 权限。
- **Kernel_Mode**：`NoUserspace: true` 时的运行模式，需要 root 权限。
- **NetBird_Endpoint_Form**：前端 Vue 组件（`NetBird.vue`），用于配置 NetBird 节点参数。

---

## Requirements

### Requirement 1：NetBird 节点数据模型

**User Story：** 作为管理员，我希望系统能够存储 NetBird 节点的配置参数，以便持久化管理 NetBird 连接信息。

#### Acceptance Criteria

1. THE Endpoint_Model SHALL 支持 `type` 字段值为 `"netbird"`，用于标识 NetBird 节点类型。
2. THE Endpoint_Model SHALL 在 `Options` 字段中存储以下 NetBird 专属参数：`device_name`、`setup_key`、`jwt_token`、`private_key`、`management_url`、`pre_shared_key`、`log_level`、`no_userspace`、`config_path`、`state_path`、`disable_client_routes`、`block_inbound`、`wireguard_port`、`mtu`、`dns_labels`。
3. WHEN Endpoint_Model 的 `MarshalJSON` 被调用且 `type` 为 `"netbird"` 时，THE Endpoint_Model SHALL 返回 `nil`，不生成任何 sing-box 配置片段。
4. THE Endpoint_Model SHALL 在 `Config_Path` 字段为空时，默认使用 `./db/netbird-{tag}.json` 作为持久化配置路径。

---

### Requirement 2：NetbirdService 生命周期管理

**User Story：** 作为系统，我希望有一个专门的服务管理所有 NetBird 客户端实例，以便统一控制其启停和状态。

#### Acceptance Criteria

1. THE NetbirdService SHALL 维护一个以节点 `tag` 为键、`embed.Client` 指针为值的映射，用于跟踪所有运行中的 NetBird_Client 实例。
2. WHEN `StartAll` 被调用时，THE NetbirdService SHALL 从数据库加载所有 `type` 为 `"netbird"` 的节点，并为每个节点启动一个 NetBird_Client 实例。
3. WHEN `StopAll` 被调用时，THE NetbirdService SHALL 停止并释放所有运行中的 NetBird_Client 实例。
4. WHEN `StartClient` 被调用且指定 `tag` 的客户端已在运行时，THE NetbirdService SHALL 先停止旧实例，再启动新实例。
5. WHEN `StopClient` 被调用且指定 `tag` 的客户端不存在时，THE NetbirdService SHALL 静默返回，不产生错误。
6. THE NetbirdService SHALL 使用 `NoUserspace: false`（userspace 模式）作为默认运行模式，无需 root 权限。
7. WHEN 启动 NetBird_Client 时，THE NetbirdService SHALL 将 `Config_Path` 设置为 `./db/netbird-{tag}.json`（若节点配置中未指定自定义路径）。
8. IF 启动 NetBird_Client 发生错误，THEN THE NetbirdService SHALL 记录错误日志并继续尝试启动其余节点，不中断整体流程。

---

### Requirement 3：sing-box 启停时同步 NetBird 节点

**User Story：** 作为管理员，我希望 NetBird 节点随 sing-box 一起启停，以便保持网络连接状态与 sing-box 一致。

#### Acceptance Criteria

1. WHEN `ConfigService.StartCore` 成功启动 sing-box 后，THE ConfigService SHALL 调用 `NetbirdService.StartAll` 启动所有 NetBird 节点。
2. WHEN `ConfigService.StopCore` 被调用时，THE ConfigService SHALL 在停止 sing-box 之前调用 `NetbirdService.StopAll` 停止所有 NetBird 节点。
3. WHILE sing-box 未运行时，THE ConfigService SHALL 不启动任何 NetBird 节点。

---

### Requirement 4：NetBird 节点 CRUD 实时生效

**User Story：** 作为管理员，我希望新增、编辑或删除 NetBird 节点时立即生效，无需重启 sing-box。

#### Acceptance Criteria

1. WHEN `EndpointService.Save` 以 `act="new"` 处理 `type="netbird"` 的节点且 sing-box 正在运行时，THE EndpointService SHALL 调用 `NetbirdService.StartClient` 启动该节点的 NetBird_Client。
2. WHEN `EndpointService.Save` 以 `act="edit"` 处理 `type="netbird"` 的节点且 sing-box 正在运行时，THE EndpointService SHALL 先调用 `NetbirdService.StopClient` 停止旧实例，再调用 `NetbirdService.StartClient` 启动新实例。
3. WHEN `EndpointService.Save` 以 `act="del"` 处理 `type="netbird"` 的节点且 sing-box 正在运行时，THE EndpointService SHALL 调用 `NetbirdService.StopClient` 停止并移除该节点的 NetBird_Client。
4. WHEN `EndpointService.Save` 处理 `type="netbird"` 的节点时，THE EndpointService SHALL 不调用 `corePtr.AddEndpoint` 或 `corePtr.RemoveEndpoint`，因为 NetBird 不经过 sing-box endpoint registry。
5. IF sing-box 未运行时，THEN THE EndpointService SHALL 仅执行数据库操作，不启停任何 NetBird_Client。

---

### Requirement 5：NetBird 节点认证配置

**User Story：** 作为管理员，我希望能够选择不同的认证方式连接 NetBird 网络，以适应不同的部署场景。

#### Acceptance Criteria

1. THE NetbirdService SHALL 支持三种互斥的认证方式：`Setup_Key`、`JWT_Token`、`Private_Key`，对应 `embed.Options` 的 `SetupKey`、`JWTToken`、`PrivateKey` 字段。
2. WHEN 构建 `embed.Options` 时，THE NetbirdService SHALL 仅将用户配置的非空认证字段传入，其余认证字段保持零值。
3. WHERE `Management_URL` 未配置时，THE NetbirdService SHALL 使用 `https://api.netbird.io:443` 作为默认管理服务器地址。
4. THE NetbirdService SHALL 支持配置 `PreSharedKey`、`LogLevel`、`DisableClientRoutes`、`BlockInbound`、`WireguardPort`、`MTU`、`DNSLabels` 等可选参数。

---

### Requirement 6：前端 NetBird 节点类型支持

**User Story：** 作为管理员，我希望在前端界面中能够创建和编辑 NetBird 节点，以便通过图形界面管理 NetBird 连接。

#### Acceptance Criteria

1. THE NetBird_Endpoint_Form SHALL 在 `EpTypes` 枚举中新增 `Netbird: 'netbird'` 条目，使其出现在节点类型下拉列表中。
2. THE NetBird_Endpoint_Form SHALL 提供以下必填/常用字段的输入控件：`device_name`（设备名称）、认证方式选择（`setup_key` / `jwt_token` / `private_key` 三选一）。
3. THE NetBird_Endpoint_Form SHALL 通过可选项开关（Options 菜单）控制以下可选字段的显示：`management_url`、`pre_shared_key`、`log_level`、`no_userspace`、`config_path`、`disable_client_routes`、`block_inbound`、`wireguard_port`、`mtu`、`dns_labels`。
4. WHEN 用户在认证方式选择中切换选项时，THE NetBird_Endpoint_Form SHALL 清空其他认证字段的值，确保同一时刻只有一种认证方式有效。
5. WHERE `no_userspace` 选项被启用时，THE NetBird_Endpoint_Form SHALL 显示提示信息，说明内核模式需要 root 权限。
6. THE NetBird_Endpoint_Form SHALL 在 `Endpoint.vue` 弹窗中与其他节点类型（Wireguard、Warp、Tailscale）保持一致的交互模式。
7. WHEN 用户新建 NetBird 节点时，THE NetBird_Endpoint_Form SHALL 自动生成格式为 `netbird-{随机3位字符}` 的默认 tag。

---

### Requirement 7：多语言支持

**User Story：** 作为管理员，我希望 NetBird 节点的界面文本支持多语言，以便不同语言的用户都能正常使用。

#### Acceptance Criteria

1. THE NetBird_Endpoint_Form SHALL 在英文语言文件（`en.ts`）的 `types` 命名空间下新增 `nb` 键，包含所有 NetBird 专属字段的英文标签。
2. THE NetBird_Endpoint_Form SHALL 在简体中文语言文件（`zhcn.ts`）的 `types` 命名空间下新增 `nb` 键，包含所有 NetBird 专属字段的中文标签。
3. THE NetBird_Endpoint_Form SHALL 对其他语言文件（`fa.ts`、`ru.ts`、`vi.ts`、`zhtw.ts`）同步新增 `nb` 键，至少提供英文回退值。

---

### Requirement 8：配置持久化与内存模式

**User Story：** 作为管理员，我希望 NetBird 节点的配置默认持久化到磁盘，同时支持内存模式，以适应不同的运行环境。

#### Acceptance Criteria

1. THE NetbirdService SHALL 默认将每个节点的配置持久化到 `./db/netbird-{tag}.json`，确保重启后无需重新认证。
2. WHERE 用户将 `config_path` 设置为空字符串时，THE NetbirdService SHALL 以内存模式（`ConfigPath: ""`）启动 NetBird_Client，不写入任何配置文件。
3. IF `./db/` 目录不存在时，THEN THE NetbirdService SHALL 在启动前创建该目录。
4. WHEN 节点被删除时，THE NetbirdService SHALL 停止对应的 NetBird_Client，但不自动删除磁盘上的配置文件（`./db/netbird-{tag}.json`）。
