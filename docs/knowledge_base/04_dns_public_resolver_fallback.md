# 04. 域名解析: 服务端公共递归 DNS 回退与机房防污染

## 1. 背景
在腾讯云、阿里云等海外轻量机房（如新加坡）运行代理节点时，部分境外域名（如台湾地区 `.tw` 域名、部分特定公网 IP 检测站点 `whatismyip.com.tw`）可能出现解析超时或连接拒绝。

## 2. 根因
在 `s-ui` 默认配置中，DNS servers 仅为：
```json
{
  "servers": [
    {
      "tag": "local",
      "address": "local"
    }
  ]
}
```
`address: local` 使得出站解析全权委托给 VPS 宿主机的 `/etc/resolv.conf`（机房内部 DNS 服务器，如腾讯云新加坡的 `183.60.83.19`）。某些机房内网 DNS 存在特定的域解析策略或防火墙拦截。

## 3. 终极解决方案 (Commit `9b0e938`)
在 `service/config.go` 的 `sanitizeConfig` 启动自检逻辑中，自动补齐国际公共递归 DNS 解析器：
```go
if len(servers) == 0 {
    dnsMap["servers"] = []map[string]string{
        {"tag": "remote", "address": "udp://8.8.8.8"},
        {"tag": "cf", "address": "udp://1.1.1.1"},
        {"tag": "local", "address": "local"},
    }
}
```
即便机房内网 DNS 无法解析特定区域域名，Sing-Box 也可平滑回退至 Google DNS (`8.8.8.8`) 或 Cloudflare DNS (`1.1.1.1`)，保证全球所有域名均能正确寻址。
