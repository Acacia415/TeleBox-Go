# 备份迁移清单

来源：

`telebox_backup_20260726_054103_70aa79a0.tar.gz`

## 迁移插件

当前迁移范围共 25 个：

```text
aban
ai
bin
bulk_delete
cezi
convert
dc
dig
eat
eatgif
gif
ids
ip
isalive
jointime
nsticker
rate
re
search
speedlink
telegram-backup
trace
yt-dlp
yvlu
zhijiao
```

这些插件均已加入 monorepo 的 `plugins/` 源码目录；它们不编译进主程序，
而是由插件 SDK 生成独立安装包。
详细功能分支见 [原版功能对照表](plugin-parity.md)。

## 已发现的数据

- 主配置：`api_id`、`api_hash`、GramJS/teleproto StringSession
- SQLite：权限、别名、插件状态和业务数据
- JSON/LowDB：缓存、规则及部分插件状态
- 媒体资产：eat/eatgif 模板
- 外部程序：yt-dlp、ffmpeg、ffprobe、Deno 等
- 压缩归档：telegram-backup 生成的数据包

迁移程序不得输出 API hash、session、密钥或完整数据库内容到日志。

## 已实现的兼容读取

- `ai_config.db`：导入服务商、密钥、地址、模型、提示词、开关和旧上下文。
- SpeedLink：迁移旧密钥/数据库，密码重新使用 AES-256-GCM 保存。
- Telegram Backup：直接兼容旧 `telegram_backup.db` 表结构，并可恢复
  旧版 `backup.json`、单备份 ZIP 和批量 ZIP。
- eat/eatgif、yt-dlp 等资产仍由迁移器按插件白名单复制。
- 旧 `alias`、`sudo`、`sure` 数据会兼容导入到 Go 版持久化存储。
