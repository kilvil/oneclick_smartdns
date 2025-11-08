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

交互界面（tview + tcell）
- 全屏 TUI，适配终端宽度：宽屏双栏（左一级 Region / 右二级平台），窄屏自动切换单页（h/l 切换左右）。
- 导航与快捷键（底部栏常驻提示）：
  - 分组列表页：Enter 进入分组；n 新建；d 删除；r 刷新；u 默认 DNS 管理；z 服务管理；q 退出。
  - 分组配置页：方向键移动；空格勾选（右侧单项），左侧空格=该 Region 全选/取消；Enter 勾选右侧；m 切换 nameserver/address；e 编辑组名或 IP；s 保存；q 返回分组列表。
- 帮助始终显示在底部栏，无需输入 ?。
- 依赖：`github.com/rivo/tview`、`github.com/gdamore/tcell/v2`

DNS 上游分组与分配
- 首屏即为分组列表：n 创建分组（上游 DNS IP + 分组名），Enter 进入分组，r 刷新，d 删除。
- 进入分组后以 nameserver 方式将勾选平台域名指向该分组（写入 smartdns.conf 中带 `#> sub ident` 的块）。
- 可用 m 切换为 address 模式并 e 输入具体 IP。
- 同一二级平台若被其他分组占用，右侧以 “!” 标示并显示占用的分组名，不可勾选；左侧 Region 聚合标识：[\*] 全选、[=] 部分、[ ] 全未选。

服务管理
- z 打开服务管理：
  - SmartDNS：安装、卸载、启动、停止、重启（启动会关闭 systemd-resolved 并把 /etc/resolv.conf 指向 127.0.0.1）；查看配置。
  - Nginx：安装；写入/刷新 80/443 反向代理（stream+http），`nginx -t` 校验后 reload；启动/停止/重启；查看配置（nginx.conf、stream/http）。
  - 紧急重置 DNS：一键停止 smartdns 与 systemd-resolved，将 /etc/resolv.conf 设置为 8.8.8.8。
  - 顶部状态栏展示 smartdns、nginx、systemd-resolved 实时状态，并在 smartdns 与 systemd-resolved 同时运行时以黄色提示可能冲突。

默认（非分组）DNS 与回退
- 支持管理 smartdns 的默认上游 DNS（顺序生效，作为无分组时的回退）：添加推荐/自定义、删除。

本地构建
```bash
go env -w GOPROXY=https://proxy.golang.org,direct   # 无代理可省略
go get github.com/rivo/tview@latest github.com/gdamore/tcell/v2@latest
go mod tidy
go build
sudo ./smartdnsctl   # 需 root
```

发行版兼容
- 仅针对 Ubuntu/Debian 系列设计；其他 Linux 发行版可手动下载对应二进制并放入 `/usr/local/bin/`。
