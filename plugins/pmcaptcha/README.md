# PMCaptcha-Go

`pmcaptcha` 是可选的陌生人私聊验证插件，通过 `-p i pmcaptcha` 安装。
命令帮助使用 `-help pmcaptcha` 或 `-pmcaptcha help` 查看。

行为基准来自 TeamPGM `PagerMaid_Plugins_Pyro` v2 分支中的
`pmcaptcha/main.py`（本次迁移核对版本 2.34）。Go 实现没有逐行复制 Python
源码，而是按公开命令和行为重新实现。

出于安全和隐私考虑，以下原有实现细节没有迁移：

- 向固定第三方机器人发送私聊、用户资料或手机号日志；
- 固定开发者 ID 自动放行；
- 联网下载并 `eval` 语言脚本；
- 通过 `exec` 执行任意 Python 自定义规则；
- 把配置编码后放入外部网页地址。

对应功能分别改为本机日志、无隐藏放行项、内置中文文本、受限规则表达式和
本地配置码。图片验证仍可主动选择原内联机器人
`@PagerMaid_Sam_Bot`，默认使用不依赖第三方的数学验证。

原版 `export_settings` 生成的 `.pmc-settings.json` 可由 Go 版
`import_settings` 读取；无法转换成安全表达式的自定义规则会跳过并给出提示。
