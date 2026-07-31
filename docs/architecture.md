# 架构决策

## 目标

1. 使用 Go 和 `gotd/td` 替换 Node/teleproto 运行时。
2. 保留旧命令、权限、消息操作和插件数据的可迁移性。
3. 已支持插件的数据直接激活，其他旧插件数据完整隔离保存，便于后续导入。
4. 框架只内置基本能力，业务插件可以独立安装和维护。
5. 单个插件故障不得终止消息循环或影响其他插件。
6. 框架、插件源码和发布工具保留在同一个 monorepo。

## 插件模型

不使用 Go 的动态 `.so` 插件。它受到编译器版本、平台和 ABI 限制，不适合
稳定支持 Linux amd64、arm64 的独立发布与更新。

主程序只编译 `internal/plugins/core`。`plugins/` 中的官方插件由
`telebox-plugin-sdk` 分别编译为普通可执行文件，每个发布包包含：

```text
plugin.json
telebox-plugin-<name>
```

`plugin.json` 记录 API 版本、命令、架构、权限和可执行文件。`tpm` 从同一
GitHub Release 下载目录和压缩包，依次验证 HTTPS、文件大小、SHA-256、
归档路径和 manifest，再原子替换安装目录。

运行时通过 stdin/stdout 上的双向 JSON RPC 连接插件子进程。插件看到的是
稳定的 `service.Container` 代理，不直接接触 gotd 的 TL 类型。大文件通过
受控工作目录传递，避免放进 JSON。

子进程边界解决的是崩溃隔离和接口解耦，不是完整的操作系统安全沙箱。
官方插件仍与 TeleBox 使用同一系统用户；对不受信任的第三方插件，应再配合
容器、独立用户或 systemd sandbox。

## 生命周期

```text
读取配置
  -> 打开 SQLite、HTTP、调度器和 Telegram
  -> 注册 core
  -> 扫描 data/plugins/*/plugin.json
  -> 注册已安装插件（尚未启动）
  -> 恢复持久化启用状态
  -> 启动各插件子进程并注册命令
  -> Telegram 消息循环
```

安装、更新和卸载由一个控制器同步处理磁盘目录、运行中注册表和 SQLite
状态。插件异常退出后，当前调用返回错误；下次调用会启动新进程。关闭时按
启用顺序逆序停止。

## 依赖方向

```text
cmd/telebox
  -> internal/app
    -> internal/plugins/core
    -> internal/pluginmanager
      -> internal/pluginmarket
      -> internal/pluginexternal
        -> internal/pluginbridge
          -> internal/pluginrpc
    -> internal/telegram/gotd

cmd/telebox-plugin-sdk
  -> internal/pluginbuilder
  -> internal/pluginrelease
  -> plugins/<official plugin>
```

公开、可版本化的 manifest 和 catalog 类型位于 `pkg/pluginapi`。应用装配、
RPC 实现和官方插件构建细节放在 `internal`，防止它们被误当成长期兼容 API。

## 命令

默认前缀为 `-`，配置和运行时持久化均支持多个自定义前缀。插件管理命令以
`tpm` 为正式名称，别名为 `p`、`t`、`plugins`、`plugin`。短操作包括：

```text
p i      install
p u      update
p ua     update all
p rm     remove
p ls     list
p s      search
p upload export installed package
p on     enable
p off    disable
```

插件管理支持一次安装或卸载多个插件，也支持回复已编译的 ZIP/TAR.GZ
插件包本地安装。官方目录安装仍要求 HTTPS 和 SHA-256；本地包由所有者主动
提供，因此会明确提示其绕过目录校验。无论来源，归档路径、体积、平台、
插件 API 版本和 manifest 都必须通过校验。

## 数据与备份

新系统以 SQLite 为主要持久化存储。旧 SQLite、LowDB JSON、配置文件和
媒体资产由一次性迁移器导入。迁移器不得写入备份包；已支持数据进入活动
资产目录，其余 `telebox/assets` 文件以不可执行权限进入隔离目录，并记录
来源摘要、相对路径、大小和逐文件 SHA-256。

迁移器转换结果通过 `_migration.json` 与 `_legacy_manifest*.json` 关联同一
源备份。安装器发现结果后仍需用户确认，再调用迁移器的安全导入接口：拒绝
符号链接和路径重叠，只以 `O_EXCL` 创建缺失文件，不覆盖现有 Go 配置、
会话或插件数据。导入完成后记录源摘要，后续安装不再重复询问。尚未安装的
插件只保留资产；插件安装后通过活动资产和隔离资产两个只读来源完成导入。

运行时 `bf` 使用 SQLite `VACUUM INTO` 获取一致快照，并对备份内每个文件
记录 SHA-256。`hf` 先执行路径、类型、数量、体积和摘要校验，再暂存到数据
目录同一文件系统；主进程重启并在打开数据库前原子应用，旧文件保存在带
时间戳的回滚目录。Telegram 登录会话、日志和可执行文件不进入该备份。

GramJS/teleproto StringSession 与 gotd 会话格式不同。迁移阶段只转换可验证
的 DC、地址和 auth key；无法验证时明确回退为 QR 登录。

## 外部工具

`yt-dlp`、`ffmpeg`、`ffprobe` 和测速工具通过统一执行器调用，使用参数数组、
超时、并发和输出上限。插件 manifest 会声明工具权限，但由于插件进程与
主程序属于同一系统用户，这仍不是恶意代码沙箱。

所有核心与插件的 Telegram 文本、HTML 和文件标题在发送前都会经过统一的
错误安全转换。常见 Telegram RPC、网络、文件、数据库和外部工具错误显示为
中文处理建议；无法可靠翻译时只显示稳定错误代码。原始 URL、路径、调用栈、
库名称和命令行输出不会直接发送到聊天。
