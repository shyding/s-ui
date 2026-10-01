# SUI 38节点：创建方法固化与灾难恢复

> 适用版本：shyding/s-ui main（含 `service/sui_nodes.go` 的提交之后）
> 最后更新：2026-10-02

## 1. 概述

SUI 订阅的核心是 38 个 inbound，监听端口 **54142–54179**，覆盖 VLESS / VMess /
Trojan / Hysteria2 / TUIC / Shadowsocks / SOCKS(mixed) 七种协议，
传输层覆盖 TCP / WS / gRPC / HTTPUpgrade。

这 38 个节点的**创建方法已固化为代码**（`service/sui_nodes.go`），不再依赖手工在
管理界面逐个添加，也不再依赖任何一次性 SQL/脚本。重装系统或全新部署后，
调用一次即可重建。

## 2. 端口分配表

| 端口 | 协议 | Tag | TLS | 传输 |
|------|------|-----|-----|------|
| 54142 | vless | vless-54142 | ✅ | tcp |
| 54143 | vless | vless-ws-54143 | ✅ | ws (/ws) |
| 54144 | vless | vless-grpc-54144 | ✅ | grpc (vgrpc) |
| 54145 | vless | vless-httpupgrade-54145 | ✅ | httpupgrade (/vhu) |
| 54146 | vmess | vmess-tcp-54146 | ✅ | tcp |
| 54147 | vmess | vmess-ws-54147 | ✅ | ws (/ws) |
| 54148 | vmess | vmess-grpc-54148 | ✅ | grpc (vgrpc) |
| 54149 | vmess | vmess-httpupgrade-54149 | ✅ | httpupgrade (/vhu) |
| 54150 | trojan | trojan-tcp-54150 | ✅ | tcp |
| 54151 | trojan | trojan-ws-54151 | ✅ | ws (/ws) |
| 54152 | trojan | trojan-grpc-54152 | ✅ | grpc (vgrpc) |
| 54153 | hysteria2 | hysteria2-54153 | ✅ | udp |
| 54154 | tuic | tuic-54154 | ✅ | udp |
| 54155 | vmess | vmess-tcp-54155 | ✅ | tcp |
| 54156 | mixed | mixed-54156 | ❌ | tcp (SOCKS) |
| 54157 | vless | vless-tcp-plain-54157 | ❌ | tcp |
| 54158 | vless | vless-ws-plain-54158 | ❌ | ws |
| 54159 | vless | vless-grpc-plain-54159 | ❌ | grpc |
| 54160 | vless | vless-httpupgrade-plain-54160 | ❌ | httpupgrade |
| 54161 | vless | vless-tcp-reality-54161 | ✅ | tcp |
| 54162 | vless | vless-ws-reality-54162 | ✅ | ws |
| 54163 | vless | vless-grpc-reality-54163 | ✅ | grpc |
| 54164 | vless | vless-httpupgrade-reality-54164 | ✅ | httpupgrade |
| 54165 | vmess | vmess-tcp-plain-54165 | ❌ | tcp |
| 54166 | vmess | vmess-ws-plain-54166 | ❌ | ws (/vmws) |
| 54167 | vmess | vmess-grpc-plain-54167 | ❌ | grpc (vmgrpc) |
| 54168 | vmess | vmess-httpupgrade-plain-54168 | ❌ | httpupgrade (/vmhu) |
| 54169 | trojan | trojan-httpupgrade-54169 | ✅ | httpupgrade (/trhu) |
| 54170 | hysteria2 | hysteria2-2-54170 | ✅ | udp |
| 54171 | tuic | tuic-2-54171 | ✅ | udp |
| 54172 | shadowsocks | ss-aes-256-gcm-54172 | ❌ | aes-256-gcm |
| 54173 | shadowsocks | ss-aes-128-gcm-54173 | ❌ | aes-128-gcm |
| 54174 | vless | vless-tcp-vision-54174 | ✅ | tcp |
| 54175 | vless | vless-ws-vision-54175 | ✅ | ws (/wsv) |
| 54176 | vmess | vmess-tcp-plain-2-54176 | ❌ | tcp |
| 54177 | trojan | trojan-tcp-plain-54177 | ❌ | tcp |
| 54178 | hysteria2 | hysteria2-3-54178 | ✅ | udp |
| 54179 | tuic | tuic-3-54179 | ✅ | udp |

注意：
- 54161–54164 的 tag 保留 `reality` 字样是历史命名，实际已迁移为标准 VLESS+TLS。
- TLS 证书路径固定为 `/usr/local/s-ui/certs/fullchain.pem` 与 `privkey.pem`，
  SNI 为 `dash.icta.top`。更换域名/证书时需同步修改 `service/sui_nodes.go` 中的常量。

## 3. 代码位置

| 内容 | 位置 |
|------|------|
| 38节点规格定义 | `service/sui_nodes.go` → `GetSUINodeSpecs()` |
| 幂等创建 | `service/sui_nodes.go` → `EnsureSUINodes(db)` |
| 获取38个ID | `service/sui_nodes.go` → `GetSUIInboundIDs(db)` |
| 完整性校验 | `service/sui_nodes.go` → `VerifySUINodes(db)` |
| 单元测试 | `service/sui_nodes_test.go` |
| 查询API | `GET /api/suiNodes`（`api/apiService.go`） |
| 创建API | `POST /api/ensureSUINodes`（`api/apiService.go`） |
| 前端一键勾选 | `frontend/src/layouts/modals/Client.vue` → “添加38个SUI节点”按钮 |

## 4. 灾难恢复（重装系统后）

```bash
# 1. fresh clone 并构建
git clone https://github.com/shyding/s-ui.git
cd s-ui && ./build.sh

# 2. 启动后，调用 API 幂等创建38个节点（需登录后的 session）
curl -X POST https://dash.icta.top:2053/api/ensureSUINodes

# 返回示例：{"success":true,"obj":{"created":38,"missing":[],"total":38}}
# 重复调用 created 为 0，不会覆盖已有节点
```

## 5. 新建用户一键关联38节点

1. 管理界面 → 用户 → 新增
2. 在“入站标签”下方点击 **“添加38个SUI节点”** 按钮
3. 38个 SUI inbound 会被自动勾选（与已选项合并去重）
4. 保存后，该用户的订阅地址即包含38个 SUI 节点：
   `https://dash.icta.top:2096/sub/<用户名>`

## 6. 闭环验证方法

```bash
# 新建测试用户 test-sui（通过管理界面，一键勾选38节点），然后：
curl -s https://dash.icta.top:2096/sub/test-sui | base64 -d | grep -oE ':[0-9]{5}' | sort -u
# 期望输出 54142..54179 共38个端口
```

验收标准：
- 订阅解码后恰好 38 个 SUI 节点
- 端口唯一覆盖 54142–54179
- 无重复 URI

## 7. 设计原则（避免重蹈覆辙）

1. **Inbound 定义只认代码**：`GetSUINodeSpecs()` 是唯一事实来源，
   不要再用手工 SQL 或界面点击来“补”节点。
2. **幂等优先**：`EnsureSUINodes` 按 tag 跳过已存在项，绝不覆盖线上数据。
3. **用户关联走界面**：新建用户时用“一键勾选”，不要手工拼 inbound ID。
4. **改规格先改代码**：增删端口、换传输层，先改 `sui_nodes.go` 并通过
   `sui_nodes_test.go`，再部署。
