# 02. 订阅系统: 历史死域名动态重定向与非标协议过滤

## 1. 故障背景
在 `https://dash.icta.top:2096/sub/my` 导出的节点链接中：
1. 部分节点（如 `naive-48847`）连接地址指向了不可解析的历史死域名 `dash.icta.qzz.io`，客户端报错 `Returncode 7 / Couldn'\''t resolve host`。
2. 部分节点（如 `hysteria2-8444`）的 SNI 参数仍然残留 `sub.icta.qzz.io`。
3. 导出了私有非标格式（`http2://`），大部分常用客户端（v2rayN、Clash 等）无法识别导入该协议。
4. 订阅中存在重复节点（如相同的 TUIC 端口节点出现两次）。

## 2. 根因剖析
- 服务器的历史域名曾做过迁移（从 `qzz.io` 换成 `top`）。但数据库中的 `clients.links` 字段持久化了旧配置生成时的静态链接字符串。
- 用户通过 Web 界面更换域名时，并不会级联更新旧客户端所有已固化的链接字段。
- 订阅处理器直接读取 `client.Links` 输出，导致死域名直出。

## 3. 终极解决方案 (Commit `9b0e938`)
在 `sub/linkService.go` 的 `GetAuthorizedLinks` 中引入**运行时净化与去重管线**：

1. **协议白名单过滤**:
   ```go
   // 过滤常规客户端无法识别的私有非标链接 (如 http2://)
   if strings.HasPrefix(link.Uri, "http2://") {
       continue
   }
   ```
2. **死域名运行时归一化**:
   ```go
   cleanUri := strings.ReplaceAll(link.Uri, "dash.icta.qzz.io", "dash.icta.top")
   cleanUri = strings.ReplaceAll(cleanUri, "sub.icta.qzz.io", "dash.icta.top")
   ```
3. **哈希去重**:
   利用 `seen := make(map[string]bool)` 保证输出的节点 URL 全局唯一，杜绝重复端口。
4. **输出兜底**:
   在 `sub/subService.go` 对拼装后的文本再次执行域名安全替换。

## 4. 效果对比
- **修复前**: 16 个节点，存在 1 个死域名不可解析节点、2 个重复节点、1 个非标节点。
- **修复后**: 13 个纯净高可用节点，全部指向 `dash.icta.top`，通过率 100%。
