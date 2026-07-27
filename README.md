# TeleBox-Go

TeleBox 的 Go 重构版本，使用 `gotd/td` 连接 Telegram。主程序只内置
核心管理功能；业务插件作为独立进程按需安装、更新和卸载。框架、官方
插件源码和插件目录仍维护在同一个仓库中。

原有 Node/TypeScript 项目和全量备份只是迁移输入，本项目不会原地修改它们。

## 当前状态

- [x] 配置、日志、命令路由和所有者权限
- [x] `gotd/td` 会话、QR 登录、更新补洞和 peer 解析
- [x] SQLite 存储、迁移器、任务调度和统一关闭流程
- [x] 全量备份中的 27 个插件全部完成 Go 移植
- [x] 可校验的插件目录、按平台安装和独立进程运行
- [x] Linux amd64/arm64 一键安装与 systemd 用户服务
- [ ] 旧插件业务数据库的逐插件转换（AI、SpeedLink、Telegram Backup 已兼容）
- [ ] Linux 隔离账号端到端验收

本仓库只提供全量备份中实际安装过的插件：

- 查询与工具：`bin`、`convert`、`dc`、`dig`、`ids`、`ip`、`isalive`、
  `jointime`、`rate`、`search`、`trace`
- 消息与管理：`aban`、`bulk_delete`、`cezi`、`re`
- 媒体与贴纸：`eat`、`eatgif`、`gif`、`nsticker`、`yvlu`、`zhijiao`
- AI 与音乐：`ai`、`music_bot`、`yt-dlp`
- 系统与备份：`speedlink`、`speedtest`、`telegram-backup`

完整进度见 [重构计划](docs/refactor-plan.md)。

## Linux 一键安装

默认安装到当前用户目录，不需要 root：

```bash
curl -fsSL https://raw.githubusercontent.com/Acacia415/TeleBox-Go/main/scripts/install.sh | sh
```

安装器会识别 `amd64` 或 `arm64`、校验发布包 SHA-256，并创建 systemd
用户服务。首次安装后编辑：

```text
~/.config/telebox/telebox.env
```

填入 `TELEBOX_API_ID` 和 `TELEBOX_API_HASH`，再启动：

```bash
systemctl --user enable --now telebox
journalctl --user -u telebox -f
```

也可以在安装时传入环境变量，或仅安装不启动：

```bash
TELEBOX_API_ID=123456 TELEBOX_API_HASH=... sh install.sh
sh install.sh --version v0.2.0 --no-start
```

首次登录生成的二维码图片位于数据目录，日志会显示完整路径。

## 手动运行

从 [GitHub Releases](https://github.com/Acacia415/TeleBox-Go/releases)
下载对应平台的压缩包：

```bash
cp config.example.json config.json
./telebox -config config.json -check-config
./telebox -config config.json
```

Windows PowerShell：

```powershell
Copy-Item config.example.json config.json
.\telebox.exe -config .\config.json -check-config
.\telebox.exe -config .\config.json
```

## 命令和插件

默认命令前缀是 `-`。插件管理器的正式名称是 `tpm`，同时提供短别名
`p`、`t`，并保留 `plugins`、`plugin` 兼容别名：

```text
-p ls                 查看已安装插件
-p s [关键词]         搜索插件
-p i bin              安装并启用插件
-p i all              安装全部官方插件
-p i bin@0.1.0        安装指定版本
-p u [插件名]         更新一个或全部插件
-p rm bin             停用并卸载插件
-p on bin             启用插件
-p off bin            停用插件
-p doctor             检查插件目录
```

`-t`、`-tpm` 与 `-p` 完全等价。前缀仍可自定义，并且可以同时保留多个：

```text
-prefix show
-prefix set - .
-prefix add !
-prefix remove .
```

主程序初次安装不携带业务插件。通过 `-p i` 下载的压缩包会经过 HTTPS、
大小限制和 SHA-256 校验，再安装到配置的插件目录。每个插件运行在独立
子进程中；一个插件退出不会带崩 TeleBox，下一次调用会自动重新启动它。

`yt-dlp`、音视频转换和测速等插件仍需要系统中存在相应的上游命令行工具。

## 项目目录

```text
cmd/telebox/                 主程序入口
cmd/telebox-migrate/         备份迁移工具入口
cmd/telebox-plugin-sdk/      官方插件构建与发布工具入口
internal/app/                应用装配和生命周期
internal/plugin*/            注册表、安装器、RPC 和子进程运行时
internal/plugins/core/       唯一内置插件
pkg/pluginapi/               稳定的插件清单与目录协议
plugins/                     全量备份中的官方插件源码
scripts/install.sh           Linux 一键安装脚本
docs/                        架构与迁移文档
```

`cmd` 是 Go 社区约定，表示“可执行程序入口”，与 Windows 的 `cmd.exe`
没有关系。业务逻辑不会放在这里。

## 本地开发

需要 Go 1.26 或更新版本：

```bash
cp config.example.json config.json
go test ./...
go run ./cmd/telebox -config config.json -check-config
```

构建单个官方插件：

```bash
go run ./cmd/telebox-plugin-sdk build \
  -plugin bin -goos linux -goarch amd64 -output .build/bin
```

生成整个插件发布目录和带校验值的目录文件：

```bash
go run ./cmd/telebox-plugin-sdk release \
  -tag v0.2.0 \
  -platforms linux/amd64,linux/arm64 \
  -output dist/plugins
```

配置可用以下环境变量覆盖，敏感值不必写入 JSON：

```text
TELEBOX_API_ID
TELEBOX_API_HASH
TELEBOX_SESSION_FILE
TELEBOX_LOGIN_MODE
TELEBOX_STORAGE_PATH
TELEBOX_ASSETS_PATH
TELEBOX_PLUGIN_DIR
TELEBOX_PLUGIN_CATALOG
TELEBOX_LOG_LEVEL
TELEBOX_LOG_FORMAT
```

## 检查和转换旧备份

检查命令不会输出 API hash、session 或 auth key：

```bash
go run ./cmd/telebox-migrate inspect -archive /path/to/backup.tar.gz
```

转换默认是 dry-run，只有增加 `-apply` 才会写入新目录，并且拒绝覆盖
已有文件：

```bash
go run ./cmd/telebox-migrate convert \
  -archive /path/to/backup.tar.gz \
  -config config.json \
  -session data/session.json \
  -assets data/assets \
  -apply
```

迁移器只读取原压缩包，不会修改 TypeScript 项目或备份包。
