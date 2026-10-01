#!/bin/sh
# s-ui 构建脚本
#
# 构建环境要求（永久记忆，2026-10-01 验证）：
# 1. Go 1.26.x（必须）
#    - Go 1.22 太旧：go.mod 要求 go >= 1.26.0
#    - Go 1.27 不兼容：sing-box v1.12.x 的 tailscale 协议代码
#      与 Go 1.27 的构建约束冲突
# 2. 构建标签必须包含 with_gvisor
#    - sing-tun 的 stack_gvisor.go 有 //go:build with_gvisor 约束
#    - 不加会报 undefined: tun.DefaultNIC, tun.NewTCPForwarder 等
# 3. sing-tun 必须是 v0.7.13（见 go.mod）
#    - v0.7.3 缺少 gvisor 相关符号
# 4. 必须设置 TMPDIR 和 GOCACHE 到非 /tmp 位置
#    - /tmp 是 tmpfs（通常仅 1.9G），CGO 编译 sqlite3 会填满
#    - 表现为 "disk quota exceeded" 或 "error writing to /tmp/cc*.s"
#
# 从零开始构建步骤（新系统）：
#   1. 安装 Go 1.26: 
#      wget https://go.dev/dl/go1.26.0.linux-amd64.tar.gz
#      tar -xzf go1.26.0.linux-amd64.tar.gz  # 得到 ./go/bin/go
#   2. 设置环境：
#      mkdir -p ~/tmp ~/go-build-cache
#      export TMPDIR=~/tmp
#      export GOCACHE=~/go-build-cache
#   3. 构建前端（需要 Node.js）：
#      cd frontend && npm i && npm run build && cd ..
#      mkdir -p web/html && rm -fr web/html/* && cp -R frontend/dist/* web/html/
#   4. 构建后端：
#      ./build.sh

# 设置临时目录（避免 /tmp tmpfs 爆满导致 "disk quota exceeded"）
if [ -z "$TMPDIR" ] || [ "$TMPDIR" = "/tmp" ]; then
    export TMPDIR="${HOME}/tmp"
    mkdir -p "$TMPDIR"
fi
if [ -z "$GOCACHE" ]; then
    export GOCACHE="${HOME}/.cache/go-build"
    mkdir -p "$GOCACHE"
fi

# 检查 Go 版本（需要 1.26.x）
GO_VERSION=$(go version 2>&1)
case "$GO_VERSION" in
    *"go1.26"*)
        echo "Go 版本检查通过: $GO_VERSION"
        ;;
    *)
        echo "警告：需要 Go 1.26.x，当前是: $GO_VERSION"
        echo "Go 1.22 太旧（go.mod 要求 >=1.26.0）"
        echo "Go 1.27 与 sing-box v1.12.x 不兼容"
        echo "安装: wget https://go.dev/dl/go1.26.0.linux-amd64.tar.gz && tar -xzf go1.26.0.linux-amd64.tar.gz"
        ;;
esac

echo "TMPDIR=$TMPDIR"
echo "GOCACHE=$GOCACHE"

cd frontend
npm i
npm run build

cd ..
echo "Backend"

mkdir -p web/html
rm -fr web/html/*
cp -R frontend/dist/* web/html/

go build -ldflags "-w -s" -tags "with_quic,with_grpc,with_utls,with_acme,with_gvisor" -o sui main.go

echo "构建完成: $(ls -lh sui 2>/dev/null | awk '{print $9, $5}')"
