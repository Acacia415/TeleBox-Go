# TeleBox-Go

TeleBox 的 Go 重构版本，使用 `gotd/td` 连接 Telegram。主程序只内置
核心管理功能；业务插件作为独立进程按需安装、更新和卸载。框架、官方
插件源码和插件目录仍维护在同一个仓库中。

原有 Node/TypeScript 项目和全量备份只是迁移输入，本项目不会原地修改它们。

## 当前状态

- [x] 配置、日志、命令路由和所有者权限
- [x] `gotd/td` 会话、QR/手机号登录、更新补洞和 peer 解析
- [x] SQLite 存储、迁移器、任务调度和统一关闭流程
- [x] 已保留插件完成原版功能对照与 Go 移植
- [x] 可校验的插件目录、按平台安装和独立进程运行
- [x] Linux amd64/arm64 一键安装与持久 systemd 服务
- [x] GitHub Release SHA-256 校验与 Telegram 内框架自更新
- [x] 安装器自动接管迁移结果，支持插件后装时延迟恢复旧配置
- [ ] 其余未移植插件业务数据库的逐插件转换（原文件已完整保留）
- [ ] Linux 隔离账号端到端验收

本仓库提供全量备份中实际安装过的插件，以及后来单独确认迁移的
`pmcaptcha` 私聊验证插件：

- 查询与工具：`bin`、`convert`、`dc`、`dig`、`ids`、`ip`、`isalive`、
  `jointime`、`rate`、`search`、`trace`
- 消息与管理：`aban`、`bulk_delete`、`cezi`、`re`
- 隐私与防护：`pmcaptcha`
- 媒体与贴纸：`eat`、`eatgif`、`gif`、`nsticker`、`yvlu`、`zhijiao`
- AI 与下载：`ai`、`yt-dlp`
- 系统与备份：`speedlink`、`telegram-backup`

详细安装、登录、命令和更新说明见 [使用手册](docs/user-guide.md)，完整
开发进度见 [重构计划](docs/refactor-plan.md)，逐项功能核对见
[原版功能对照表](docs/plugin-parity.md)。

## Linux 一键安装

默认安装到当前用户目录，不需要 root：

```bash
curl -fsSL https://raw.githubusercontent.com/Acacia415/TeleBox-Go/main/scripts/install.sh | sh
```

安装器会识别 `amd64` 或 `arm64`、校验发布包 SHA-256，然后依次询问
Telegram API ID、API Hash 和登录方式。可以选择直接扫描终端中的 QR
二维码，也可以输入手机号、验证码以及账号启用时的二步验证密码。只有
登录成功后才会启动 systemd 服务。root 安装会使用系统级服务；普通用户
安装会启用 linger 后使用用户级服务，两种方式都不会因 SSH 断开而停止。

如果已经在 `~/telebox-migration` 中运行过 `telebox-migrate convert
-apply`，安装器会主动发现并询问是否导入。确认后只复制正式目录中不存在
的配置、会话和插件资产，不覆盖当前 Go 数据；没有发现迁移结果时安装流程
保持不变。自定义迁移目录可以通过 `TELEBOX_MIGRATION_DIR` 指定。

即使使用 `curl | sh`，安装器也会从 `/dev/tty` 读取输入，不会把管道
内容误当成验证码。重新安装时不会显示已保存的 API ID；API Hash 和
二步验证密码输入时也不会回显。

也可以使用环境变量预填 API 信息，或仅安装不登录：

```bash
TELEBOX_API_ID=123456 TELEBOX_API_HASH=... \
  TELEBOX_INSTALL_LOGIN_MODE=phone sh install.sh
sh install.sh --version v0.2.0 --no-login
```

`--no-start` 会完成登录但不启动服务；`--no-login` 会同时跳过登录和
启动。一键安装同时安装 `telebox-migrate`，方便以后检查或接管原版备份。
迁移时数据库、配置和密钥按私有权限导入；旧程序和脚本只保存在隔离目录，
SpeedLink、YT 所需工具由插件从官方上游重新安装。
QR 登录还会把二维码图片保存到数据目录，终端会显示完整路径。

已经安装后也可以单独重新登录：

```bash
set -a
. ~/.config/telebox/telebox.env
set +a
~/.local/bin/telebox -config ~/.config/telebox/config.json \
  -login -login-mode phone
```

安装支持框架自更新的版本后，后续可以直接在 Telegram 中操作：

```text
-update check          检查新版本
-update                更新框架并自动重启
-p u                   更新全部已安装插件
```

## 手动运行

从 [GitHub Releases](https://github.com/Acacia415/TeleBox-Go/releases)
下载对应 Linux 架构的压缩包：

```bash
cp config.example.json config.json
./telebox -config config.json -check-config
./telebox -config config.json
```

## 命令和插件

默认命令前缀是 `-`。插件管理器的正式名称是 `tpm`，同时提供短别名
`p`、`t`，并保留 `plugins`、`plugin` 兼容别名：

```text
-p ls [-v]            查看已安装插件（可显示详情）
-p s [关键词]         搜索插件
-p i bin ip           安装并启用一个或多个插件
-p i                  回复已编译插件包后本地安装
-p i all              安装全部官方插件
-p i bin@0.2.0        安装指定版本
-p u [插件名]         更新一个或全部插件
-p ua                 更新全部插件
-p rm bin ip          停用并卸载一个或多个插件
-p rm all             卸载全部业务插件
-p upload bin         导出当前平台的已安装插件包
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

旧版自定义命令别名也会迁移；运行时可以继续管理：

```text
-alias set de bd
-alias set t p
-alias ls
-alias del de
```

主程序初次安装不携带业务插件。通过 `-p i` 下载的压缩包会经过 HTTPS、
大小限制和 SHA-256 校验，再安装到配置的插件目录。每个插件运行在独立
子进程中；一个插件退出不会带崩 TeleBox，下一次调用会自动重新启动它。
Release 中的 Linux 插件 ZIP 是按需安装和独立更新所必需的，不会在安装
主程序时自动下载。正式 Release 不再发布缺少一键安装与守护流程的 Windows
主程序和插件包。

`yt-dlp` 可以通过 `-yt setup` 自动安装并校验上游程序，通过
`-yt doctor` 检查 Deno、FFmpeg、Cookies 和代理。音视频转换等插件仍需要
系统中存在相应的上游命令行工具。

陌生人私聊验证插件按需安装：

```text
-p i pmcaptcha
-pmcaptcha status
-help pmcaptcha
```

`pmcaptcha` 默认使用本地数学验证；贴纸和图片模式需手动选择。图片模式
会调用原插件使用的 `@PagerMaid_Sam_Bot`，启用时会在 Telegram 内再次说明。
Go 版不包含原插件的第三方日志上报、硬编码放行用户、远程语言脚本执行或
任意 Python 规则执行；自定义规则改为受限表达式，配置码只在本机生成。

框架备份可直接在 Telegram 中完成。`-bf` 备份插件与运行数据，`-bf all`
另外包含 JSON 配置；回复备份文件发送 `-hf` 会先完整校验，重启后再恢复。
登录会话、日志和主程序不会写入备份。

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
TELEBOX_LEGACY_ASSETS_PATH
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
  -legacy-assets data/legacy-assets \
  -apply
```

迁移器只读取原压缩包，不会修改 TypeScript 项目或备份包。当前 Go 插件可
读取的数据会进入 `data/assets`；旧备份内 `telebox/assets` 的全部文件还会以
不可执行方式保存到 `data/legacy-assets`，并生成逐文件 SHA-256 清单，供以后
新增的 Go 插件导入。
