#!/usr/bin/env bash
set -euo pipefail

# GitHub repo in the form "owner/repo"
REPO="kilvil/oneclick_smartdns"
BIN="smartdnsctl"

# 提权工具（root 环境无需 sudo）
SUDO="sudo"; [ "$(id -u)" -eq 0 ] && SUDO=""

# 查找最新 Release 的 linux/amd64 二进制
API="https://api.github.com/repos/$REPO/releases/latest"
URL=$(curl -fsSL "$API" \
  | grep -oE '"browser_download_url"\s*:\s*"[^"]+"' \
  | cut -d '"' -f4 \
  | grep '/smartdnsctl_linux_amd64$' \
  | head -n1)

[ -n "$URL" ] || { echo "未找到 linux/amd64 二进制资产" >&2; exit 1; }

$SUDO curl -fL "$URL" -o "/usr/local/bin/$BIN"
$SUDO chmod +x "/usr/local/bin/$BIN"

# 以 root 权限启动
$SUDO "/usr/local/bin/$BIN"
