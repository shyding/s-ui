# 05. 传输加速: 宿主机 Google BBR 与 TCP 缓冲区调优

## 1. 背景
代理服务属于高带宽、高并发、远距离跨国通信场景。默认的 Linux 内核 TCP 缓冲区较小（通常为 4MB 左右），且采用默认的 Cubic 拥塞控制算法，在丢包率稍高的跨国网络中吞吐量会急剧下降。

## 2. 生产优化方案 (.github/workflows/deploy.yml)
在 CI/CD 自动部署脚本中，在启动 `s-ui` 前自动加载以下内核调优指令：

```bash
# 1. 禁用 IPv6 (避免 DNS 或双栈网络优先发起无路由 IPv6 请求导致卡顿)
sudo sysctl -w net.ipv6.conf.all.disable_ipv6=1 || true

# 2. 启用 Google BBR 拥塞控制算法与 FQ 公平排队队列
sudo modprobe tcp_bbr || true
sudo sysctl -w net.core.default_qdisc=fq || true
sudo sysctl -w net.ipv4.tcp_congestion_control=bbr || true

# 3. 扩大系统 TCP 接收与发送缓冲区至 16MB
sudo sysctl -w net.core.rmem_max=16777216 || true
sudo sysctl -w net.core.wmem_max=16777216 || true
sudo sysctl -w net.ipv4.tcp_rmem="4096 87380 16777216" || true
sudo sysctl -w net.ipv4.tcp_wmem="4096 65536 16777216" || true
```

## 3. 效益
- 跨国链路在 1%~5% 轻度丢包环境下仍能维持 90% 以上的带宽跑满率；
- 避免 TCP 窗口满溢造成的短暂停滞，大静态文件秒级下发。
