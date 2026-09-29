# PROJECT_RULES.md — S-UI 订阅核心质与量刚性规则

> 参见完整细则文档: [SUI_QUALITY_AND_QUANTITY_RULES.md](file:///i:/learn_code/s-ui/docs/SUI_QUALITY_AND_QUANTITY_RULES.md)

## 核心刚性铁律速查表

1. **品质门槛（100% 连通，零 -1，<= 650ms）**：
   - 客户端真连接测试（HTTP 204）100% 成功，绝无 -1。
   - 延迟必须 <= 650ms，VPS 端门槛锁定 <= 500ms，任何超大延迟（1000ms+/4000ms+）一律剔除。

2. **备注语言（100% 中文与数字，零英文，零“未知”）**：
   - 格式：`{来源}-{国家}-{区域}-{城市}-{编号}`
   - 来源仅限：`Seed`, `Cloudflare`, `HProxy`, `SUI`（严禁以 `vless` 等协议名为来源）。
   - 除来源允许英文外，其余部分（国家、区域、城市、编号）100% 为中文或数字或中文+数字。
   - 绝对零“未知”、零“unknown”、零“unknow”。

3. **数量底线（总数 >= 1000，S-UI 原生 >= 15~20 组合）**：
   - S-UI 原生来源节点绝不能只有 1 个，必须覆盖 Sing-Box 支持的 >= 15~20 种协议与传输组合（VLESS/VMess/Trojan 的 TCP/WS/gRPC/HTTPUpgrade，以及 Hysteria2, TUIC, Shadowsocks 等），独立编号发布。
   - 排除 SUI 外，外部来源（Seed + Cloudflare + HProxy）合计必须 >= 1000 个优质节点。
   - Seed 种子节点 108+ 全量准确对接，严禁截断丢弃。

4. **端到端闭环验证**：
   - 每次变更与部署必须实际拉取订阅，通过自动化测试验证 100% 符合上述指标后方可汇报。
