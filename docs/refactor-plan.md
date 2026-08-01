# Go 重构计划

范围以当前确认保留的业务插件为准，不把其他插件加入重构范围。

## 第一阶段：整体框架

- [x] Go module、目录结构、配置和结构化日志
- [x] `gotd/td` MTProto 会话、QR/手机号交互登录、更新同步和 peer 解析
- [x] 稳定的 Telegram 消息、媒体、用户查询接口
- [x] 命令路由、owner 权限、动态前缀和发送者限流
- [x] 有界任务队列、panic 隔离和插件生命周期
- [x] SQLite 状态/KV 存储和 schema migration
- [x] 有界 HTTP 客户端、外部程序执行器和任务调度器
- [x] 备份检查、GramJS/Telethon session 转换和目标资产提取
- [x] Linux amd64/arm64 二进制和 CI 构建

## 第二阶段：备份内插件

- [x] `aban`
- [x] `ai`
- [x] `bin`
- [x] `bulk_delete`
- [x] `cezi`
- [x] `convert`
- [x] `dc`
- [x] `dig`
- [x] `eat`
- [x] `eatgif`
- [x] `gif`
- [x] `ids`
- [x] `ip`
- [x] `isalive`
- [x] `jointime`
- [x] `nsticker`
- [x] `pmcaptcha`
- [x] `rate`
- [x] `re`
- [x] `search`
- [x] `speedlink`
- [x] `telegram-backup`
- [x] `trace`
- [x] `yt-dlp`
- [x] `yvlu`
- [x] `zhijiao`

## 第三阶段：插件模块化与 Linux 发布

- [x] 主程序仅保留 core 插件
- [x] `tpm`/`p`/`t` 短命令与默认 `-` 前缀
- [x] 多前缀配置和运行时持久化
- [x] 进程隔离插件协议与自动重启
- [x] HTTPS、SHA-256 和安全解包安装器
- [x] monorepo 插件 SDK 与跨平台目录生成
- [x] Linux amd64/arm64 一键安装脚本
- [x] 安装器发现迁移结果、安全接管缺失文件和插件延迟恢复
- [x] Telegram 内检查/更新框架、SHA-256 校验和自动重启
- [x] 原版 alias/sudo/sure、调试、备份恢复和运行管理命令兼容
- [x] 多插件安装/卸载、全部更新、本地包安装和插件包导出
- [ ] Linux 隔离账号逐插件端到端验收

每个插件按以下验收条件推进：

1. 以全量备份中的 TypeScript 源码和资产为行为基准。
2. 核对外部上游是否仍维护，优先使用官方 HTTPS/API。
3. 接入统一权限、并发、HTTP、存储、媒体或外部程序边界。
4. 为解析、格式化和关键错误路径补测试。
5. 通过 `gofmt`、`go vet ./...`、`go test ./...`、Linux amd64/arm64
   主程序及官方插件包构建。

## 第三阶段：兼容与部署

- [ ] 为有持久业务数据的插件编写逐项数据转换
  - [x] AI 旧 `ai_config.db` 配置与历史
  - [x] SpeedLink 旧数据库、密钥与凭据
  - [x] Telegram Backup 旧 SQLite 和导出 ZIP/JSON
  - [x] alias/sudo/sure、yt-dlp、trace、eat/eatgif 等已安装资产兼容导入
  - [ ] 其余没有持久业务数据的插件进行 Linux 实机行为验收
- [x] 使用备份做一次不输出密钥的真实 dry-run
- [ ] 在隔离账号进行 Telegram 端到端兼容测试
- [x] 编写 Linux 部署、升级和回滚说明
- [ ] 生成首个可替换旧程序的发布包
