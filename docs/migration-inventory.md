# 备份迁移清单

来源：

`telebox_backup_20260726_054103_70aa79a0.tar.gz`

## 实际插件

以备份内 `telebox/plugins/*.ts` 为准，共 27 个：

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
music_bot
nsticker
rate
re
search
speedlink
speedtest
telegram-backup
trace
yt-dlp
yvlu
zhijiao
```

`assets/tpm/plugins.json` 中存在额外历史条目，但其源码不在实际插件
目录，因此不自动纳入迁移。相关数据文件仍原样保留，除非用户以后明确
要求恢复对应插件。

上述 27 个插件均已完成 Go 代码移植并加入编译期插件目录；未安装的
仓库插件没有被加入。

## 已发现的数据

- 主配置：`api_id`、`api_hash`、GramJS/teleproto StringSession
- SQLite：权限、别名、插件状态和业务数据
- JSON/LowDB：缓存、规则及部分插件状态
- 媒体资产：eat/eatgif 模板
- 外部程序：yt-dlp、speedtest 等
- 压缩归档：telegram-backup 生成的数据包

迁移程序不得输出 API hash、session、密钥或完整数据库内容到日志。

## 已实现的兼容读取

- `ai_config.db`：导入服务商、密钥、地址、模型、提示词、开关和旧上下文。
- SpeedLink：迁移旧密钥/数据库，密码重新使用 AES-256-GCM 保存。
- Telegram Backup：直接兼容旧 `telegram_backup.db` 表结构，并可恢复
  旧版 `backup.json`、单备份 ZIP 和批量 ZIP。
- eat/eatgif、yt-dlp、speedtest 等资产仍由迁移器按白名单复制。
