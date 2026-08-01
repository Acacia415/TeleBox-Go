# 原版功能对照表

本文以全量备份中的 TypeScript 源码为功能基线，记录 Go 版本实际保留的
功能。排版、错误提示、并发边界和上游接入可以优化，但不以“简化实现”为由
删除原版功能。

统一约定：

- 默认前缀为 `-`，并兼容多个自定义前缀。
- 查看命令帮助统一使用 `-help <命令>`，例如 `-help sl`。
- 业务插件按需安装，不编译进主程序。
- 当前业务插件版本统一从 `0.2.0` 起发布。

## 核心框架

| 原版模块 | Go 版命令 | 保留功能 |
| --- | --- | --- |
| help | `help` | 紧凑命令列表、按主命令或别名查看说明、权限和用法 |
| prefix | `prefix` | 查看、设置、增加、删除多个命令前缀 |
| alias | `alias` | 设置、删除、列出动态别名；支持多词别名、固定参数和旧数据库导入 |
| ping | `ping` | Telegram API/消息延迟、主机/IP、DC1–DC5 和全部 DC 测试 |
| status/sysinfo | `status`、`sysinfo` | 版本、运行时、插件、CPU、内存、磁盘、网络和 Telegram 会话信息 |
| exec | `exec` | 所有者 Shell 执行、超时和长输出文件回传 |
| loglevel/sendLog | `loglevel`、`sendlog` | 动态日志等级、持久化、目标设置、发送和清理日志 |
| reload/exit/pmr | `reload`、`restart` | 重载业务插件或重启进程，保留 `exit`、`pmr` 别名 |
| update | `update` | 检查、普通更新、`force`/`-f` 强制更新、SHA-256 校验和自动重启 |
| sudo | `sudo` | 用户授权、对话白名单、回复或用户名解析、旧数据库导入 |
| sure | `sure` | 用户/对话/消息白名单、命令前缀匹配、参数保留、重定向和旧数据导入 |
| bf/hf | `bf`、`hf` | 标准/全量备份、持久/临时发送目标、安全校验、重启恢复和回滚目录 |
| debug | `id`、`entity`、`msg`、`echo` | 用户/群组/消息链接解析、三种用户跳转链接、JSON 和消息原样复制 |
| tpm | `tpm`、`p`、`t` | 搜索、详情、安装、更新、启停、卸载、诊断和批量操作 |

Go 版备份使用 SQLite 一致快照和逐文件 SHA-256；恢复拒绝路径穿越、链接、
重复文件、超量文件和摘要不匹配。出于安全考虑，登录会话、日志和主程序不
进入 Telegram 备份。

插件管理额外支持一次安装/卸载多个插件、`p ua`/`p updateAll`、`p rm all`、
回复已编译插件包后 `p i` 本地安装，以及 `p upload <插件>` 导出当前平台
插件包。官方插件仍从同一仓库的 Release 目录下载，不拆分第二个仓库。

## 业务插件

| 插件 | 命令 | 原版功能覆盖 |
| --- | --- | --- |
| `aban` | `aban`、`kick`、`ban`、`unban`、`mute`、`unmute`、`sb`、`unsb`、`refresh` | 群管理动作、回复/目标解析、管理员缓存刷新 |
| `ai` | `ai` | 对话、搜索、图像、TTS/音频、服务商/模型选择、API Key/Base URL、第三方模式、提示词、上下文、最大 Token、折叠与 Telegraph |
| `bin` | `bin` | BIN/IIN 查询、主上游和备用页面解析、卡组织/类型/国家/银行信息 |
| `bulk_delete` | `bd` | 回复区间删除、最近自己消息计数删除、`on/off` 控制删除他人消息 |
| `cezi` | `cezi` | 单字测字、AI 解签、API/模型配置和旧配置导入 |
| `convert` | `convert` | 回复媒体转音频、FFmpeg/FFprobe 检查、可选 Gemini 元数据 |
| `dc` | `dc` | 当前对话、回复用户或指定用户的数据中心查询 |
| `dig` | `dig` | DNS 类型查询、系统 dig 调用、IP 归属地和 ASN 补充 |
| `eat` | `eat`、`eat2` | 头像融合、回复/用户目标、多种模板与旧资产读取 |
| `eatgif` | `eatgif` | 回复图片生成头像融合动图、FFmpeg 转码和旧资产读取 |
| `gif` | `gif` | GIF/视频转 Telegram 视频贴纸，保留时长、尺寸和格式限制 |
| `ids` | `ids`/`id`、`entity`、`msg`、`echo` | 用户/群组/频道详情、消息链接、原版三种跳转链接，并合并核心调试命令 |
| `ip` | `ip` | IP 或域名解析、位置、ASN、运营商和网络信息 |
| `isalive` | `isalive` | 查询用户最后在线/在线状态及隐私状态提示 |
| `jointime` | `jointime`/`jt` | 查询普通成员或回复用户的入群时间 |
| `nsticker` | `sticker`/`s` | 收藏回复贴纸、取消收藏和贴纸包信息处理 |
| `pmcaptcha` | `pmcaptcha`/`pmc` | 陌生人私聊验证、数学/贴纸/可选图片验证、关键词/Premium/共同群/历史规则、失败处理、反洪水、状态恢复和设置导入导出 |
| `rate` | `rate` | 加密货币价格、法币汇率和数量换算，保留多上游回退 |
| `re` | `re` | 回复消息复读、消息数量和重复次数 |
| `search` | `search`/`so` | 多频道搜索、来源增删/默认/列表、导入导出、讨论组来源和结果转发 |
| `speedlink` | `speedlink`/`sl` | 服务器增删、单机/多机/全部测速、本机测速、备份恢复、旧凭据迁移和完整教程 |
| `telegram-backup` | `tb` | 收藏夹、私聊、指定对话、全部/群组/频道/链接备份，群组列表、加入、导出、恢复、删除和清理 |
| `trace` | `trace` | 回复用户、关键词、状态、日志、大日志、清理和重置；兼容旧规则/数据库 |
| `yt-dlp` | `yt` | 搜索下载、手动链接、API/Base URL 配置和更新；新增自动安装、环境诊断、代理、Cookies 和 JS 运行时配置 |
| `yvlu` | `yvlu` | 文字/回复语录图、头像、媒体、用户模式和多张生成 |
| `zhijiao` | `zhijiao` | 强随机掷筊与原版卦辞结果 |

## 架构替换，不是功能删除

- TypeScript 动态源码插件改为普通 Go 子进程包，避免 Go `.so` 的平台和 ABI
  限制，并支持 Linux amd64/arm64。
- 原版可编辑源码源改为同仓库、带版本和摘要的发布目录；仍可由所有者安装或
  导出本地编译包。
- yt-dlp 的 Cloudflare/自建接口、代理和登录相关配置被保留；服务器遇到
  YouTube 风控时还可显式配置 Cookies 和 Deno。
- PMCaptcha 保留正常可见功能，但不迁移第三方日志上报、硬编码放行 ID、
  远程 `eval` 和任意 Python `exec`。自定义规则使用受限表达式；图片验证
  仍可选择原内联机器人，默认验证方式为本地数学题。
- 旧 alias、sudo、sure、AI、SpeedLink、Trace、Telegram Backup 和媒体资产
  在首次启动或迁移时兼容读取，再写入 Go 版持久化格式。

仍需在 Linux 隔离账号完成的是 Telegram/第三方上游的端到端实机验收，不是
代码分支移植。上游接口如果临时拒绝服务器 IP，插件应给出可操作的错误和
配置方式，而不是静默删除对应功能。
