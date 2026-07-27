# 架构决策

## 目标

1. 使用 Go 和 `gotd/td` 替换 Node/teleproto 运行时。
2. 保留旧命令、权限、消息操作和插件数据的可迁移性。
3. 只迁移全量备份 `telebox/plugins/` 中实际存在的插件。
4. 单个插件故障不得终止消息循环或阻塞其他插件。
5. 备份只读导入，迁移过程可重复、可审计、可回滚。

## 插件模型

不使用 Go 标准库的动态 `.so` 插件机制。所有受支持插件作为独立 Go
包编译进主程序，由注册表负责：

- 名称和版本校验；
- 命令冲突检查；
- 启动和停止；
- 运行时启用和停用；
- 关闭时逆序释放资源；
- 状态和错误报告。

这种方式适合当前固定的 27 个插件范围，并能生成单文件部署产物。
后续如果需要第三方扩展，再评估进程隔离 RPC 或 WASM，不把 ABI
不稳定的动态链接作为基础能力。

## 依赖方向

```text
cmd/telebox
    -> internal/app
        -> internal/config
        -> internal/plugin
            -> internal/command
                -> internal/telegram (interfaces and message types)
        -> internal/plugins/*
        -> internal/telegram/gotd (implementation)
```

业务插件只依赖稳定的命令与 Telegram 抽象，不直接依赖 gotd 的 TL
类型。这样 Telegram schema 更新不会扩散到所有插件。

## 数据

新系统以 SQLite 为主要持久化存储。旧 SQLite、LowDB JSON、配置文件
和媒体资产由一次性迁移器导入。迁移器不得写入备份包，并为每个数据源
记录：

- 来源文件和校验值；
- 迁移版本；
- 成功、跳过和失败记录；
- 导入行数和资源文件数。

GramJS/teleproto StringSession 与 gotd 会话格式不同。迁移阶段优先尝试
转换 DC、地址和 auth key；无法安全验证时回退为一次性重新登录，不能
把无效会话静默写入新存储。

## 外部程序

`yt-dlp`、`ffmpeg`、`ffprobe`、`dig`、`ping` 和测速工具通过统一的
工具执行器调用。执行器必须提供：

- 参数数组调用，禁止 shell 字符串拼接；
- 超时、并发限制和输出上限；
- 可执行文件版本及能力检查；
- 下载文件校验和原子替换；
- 可测试的接口。
