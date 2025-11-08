# smartdnsctl

一键安装与启动（Ubuntu/Debian，amd64）
- 极简：通过 curl 获取 oneclick.sh 并直接执行；脚本会自动下载最新 linux/amd64 二进制到 `/usr/local/bin/smartdnsctl` 并以 root 启动。
- 使用前请将以下命令中的仓库路径替换为你的实际 GitHub 仓库（owner/repo）。

```bash
curl -fsSL https://raw.githubusercontent.com/kilvil/oneclick_smartdns/main/oneclick.sh | bash
```

注意事项
- 需要联网以访问 GitHub Releases；如遭遇 API 频率限制，可稍后再试或配置代理。
- 如果希望安装指定版本，可将脚本中 `releases/latest` 替换为 `releases/tags/<你的版本标签>` 并相应过滤资产。
- 程序运行需要 root 权限（会管理 systemd 服务与 `/etc/resolv.conf`）。

发行版兼容
- 仅针对 Ubuntu/Debian 系列设计；其他 Linux 发行版可手动下载对应二进制并放入 `/usr/local/bin/`。
