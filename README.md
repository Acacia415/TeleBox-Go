# TeleBox-Go

TeleBox 的 Go 重构版本。项目采用 `gotd/td` 连接 Telegram，插件以 Go
包的形式编译进单一可执行文件，并通过配置在运行时启用或停用。

原有 Node/TypeScript 项目和全量备份是迁移输入，不会被本项目原地修改。

## 当前状态

- [x] 配置模型和环境变量覆盖
- [x] 结构化日志
- [x] 命令解析、权限检查和路由
- [x] 插件注册、启停和生命周期
- [x] 核心管理命令骨架
- [x] `gotd/td` 会话、QR 登录、更新补洞和 peer 解析
- [x] SQLite 基础存储与 schema migration
- [x] 受限外部程序执行器
- [x] 可取消任务调度和统一关闭顺序
- [x] 只读备份检查与 GramJS/Telethon 会话转换器
- [x] Telegram 消息、媒体和常用管理操作
- [x] 备份内目标插件资产的安全提取与迁移清单
- [x] 备份中的 27 个插件全部完成 Go 移植
- [ ] 旧插件业务数据库的逐插件转换（AI、SpeedLink、Telegram Backup 已兼容）
- [ ] 隔离账号 Telegram 端到端验收

已完成的业务插件（仅包含全量备份实际安装的插件）：

- 查询与工具：`bin`、`convert`、`dc`、`dig`、`ids`、`ip`、`isalive`、
  `jointime`、`rate`、`search`、`trace`
- 消息与管理：`aban`、`bulk_delete`、`cezi`、`re`
- 媒体与贴纸：`eat`、`eatgif`、`gif`、`nsticker`、`yvlu`、`zhijiao`
- AI 与音乐：`ai`、`music_bot`、`yt-dlp`
- 系统与备份：`speedlink`、`speedtest`、`telegram-backup`

完整进度见 [重构计划](docs/refactor-plan.md)。

## 下载和运行

可从 [GitHub Releases](https://github.com/Acacia415/TeleBox-Go/releases)
下载对应平台的压缩包。解压后复制示例配置并填入自己的 Telegram API：

```bash
cp config.example.json config.json
./telebox -config config.json -check-config
./telebox -config config.json
```

Windows PowerShell 使用：

```powershell
Copy-Item config.example.json config.json
.\telebox.exe -config .\config.json -check-config
.\telebox.exe -config .\config.json
```

首次启动可使用二维码登录。`yt-dlp`、音视频转换和测速等插件还需要安装
各自调用的上游命令行工具。

## 目录

```text
cmd/telebox/          主程序
internal/app/         应用装配和生命周期
internal/command/     命令解析与路由
internal/config/      配置读取、校验和路径解析
internal/plugin/      插件契约与注册表
internal/plugins/     内置及迁移后的插件
internal/telegram/    Telegram 抽象和 gotd 适配器
docs/                 架构与迁移清单
```

## 本地开发

需要 Go 1.26 或更新版本。

```bash
cp config.example.json config.json
go test ./...
go run ./cmd/telebox -config config.json -check-config
```

`config.example.json` 默认列出全部 27 个已移植插件。可以从 `enabled`
移到 `disabled`，也可以运行后通过核心插件管理命令持久化启停状态。

可以使用环境变量覆盖敏感配置：

```text
TELEBOX_API_ID
TELEBOX_API_HASH
TELEBOX_SESSION_FILE
TELEBOX_LOGIN_MODE
TELEBOX_STORAGE_PATH
TELEBOX_ASSETS_PATH
TELEBOX_LOG_LEVEL
TELEBOX_LOG_FORMAT
```

主程序已接入 `gotd/td`。首次启动可使用 QR 登录；从旧版迁移时使用
`telebox-migrate` 生成的现有会话，避免重新登录。

## 检查和转换旧备份

检查命令不会输出 API hash、session 或 auth key：

```bash
go run ./cmd/telebox-migrate inspect -archive /path/to/backup.tar.gz
```

转换默认是 dry-run。只有显式增加 `-apply` 才会创建新配置和 gotd
session，并且拒绝覆盖已有文件：

```bash
go run ./cmd/telebox-migrate convert \
  -archive /path/to/backup.tar.gz \
  -config config.json \
  -session data/session.json \
  -assets data/assets \
  -apply
```

迁移器只读取原压缩包并写入新的 Go 配置、会话和资产目录，不会修改
TypeScript 原项目或备份包。`ai_config.db`、SpeedLink 数据以及
`telegram-backup/telegram_backup.db` 会在对应插件首次启动时按兼容规则读取。
