# TeleBox-Go 使用手册

本文面向通过 GitHub Release 安装 TeleBox-Go 的 Linux 用户。默认命令
前缀为 `-`；如果已经修改前缀，请把示例中的 `-` 替换为自己的前缀。

## 1. 更新方式先说明

安装了包含框架自更新功能的版本后，可以直接在 Telegram 中发送
`-update` 更新 TeleBox-Go。首次从旧版本升级到支持该命令的版本时，
仍需在 Linux 终端运行一次一键安装命令。

不同更新对象使用不同方式：

| 更新对象 | 操作 |
| --- | --- |
| TeleBox-Go 框架 | Telegram 中发送 `-update` |
| 只检查框架版本 | Telegram 中发送 `-update check` |
| 已安装的全部插件 | Telegram 中发送 `-p u` |
| 指定插件 | Telegram 中发送 `-p u 插件名` |
| yt-dlp 上游程序 | Telegram 中发送 `-yt update` |

终端一键安装命令保留为首次升级和故障恢复方式：

```bash
curl -fsSL https://raw.githubusercontent.com/Acacia415/TeleBox-Go/main/scripts/install.sh | sh
```

安装器会获取最新正式版、停止旧服务、替换主程序并重新启动服务。已有的
配置、数据库、插件和登录会话都会保留。检测到已有会话时直接回车继续
使用即可。

> `-p u` 只更新插件，不会更新 TeleBox-Go 主程序。

## 2. 支持环境

- Linux `amd64`
- Linux `arm64`
- 使用 systemd 用户服务
- 可访问 Telegram 和 GitHub Release

安装到当前用户目录，不要求 root。使用 root 安装时，文件会位于
`/root` 下；普通用户安装时位于该用户的主目录下。

## 3. 准备 Telegram API

安装前需要 Telegram API ID 和 API Hash。可以在
[my.telegram.org/apps](https://my.telegram.org/apps) 创建应用并取得。

- API ID 是数字。
- API Hash 是 32 位十六进制字符串。
- API Hash 不应发送给他人或发布到截图、日志和公开仓库。

安装器输入 API Hash 和二步验证密码时不会回显。重新安装时，如果检测
到已有 API ID，只会提示直接回车保留，不会显示具体数字。

## 4. 一键安装

在 SSH 终端运行：

```bash
curl -fsSL https://raw.githubusercontent.com/Acacia415/TeleBox-Go/main/scripts/install.sh | sh
```

安装器会依次完成：

1. 询问 Telegram API ID。
2. 隐藏输入 Telegram API Hash。
3. 选择 QR 二维码或手机号登录。
4. 下载与 CPU 架构匹配的正式版。
5. 校验安装包 SHA-256。
6. 完成 Telegram 登录。
7. 创建并启动 systemd 用户服务。

只安装、不登录：

```bash
curl -fsSL https://raw.githubusercontent.com/Acacia415/TeleBox-Go/main/scripts/install.sh |
  sh -s -- --no-login
```

完成登录但不启动服务：

```bash
curl -fsSL https://raw.githubusercontent.com/Acacia415/TeleBox-Go/main/scripts/install.sh |
  sh -s -- --no-start
```

安装指定版本：

```bash
curl -fsSL https://raw.githubusercontent.com/Acacia415/TeleBox-Go/main/scripts/install.sh |
  sh -s -- --version v0.2.1
```

## 5. Telegram 登录

### QR 登录

选择 `1) QR 二维码扫码` 后：

1. 打开 Telegram。
2. 进入“设置 → 设备 → 连接桌面设备”。
3. 扫描终端中显示的二维码。

二维码同时保存为：

```text
~/.local/share/telebox/login-qr.png
```

### 手机号登录

选择 `2) 手机号 + 验证码` 后：

1. 输入包含国际区号的手机号，例如 `+8613812345678`。
2. 输入 Telegram 或短信收到的验证码。
3. 如果账号启用了二步验证，再输入二步验证密码。

### 单独重新登录

```bash
set -a
. ~/.config/telebox/telebox.env
set +a
~/.local/bin/telebox \
  -config ~/.config/telebox/config.json \
  -login -login-mode phone
```

把最后的 `phone` 改成 `qr` 即可使用二维码。

如果安装器检测到已有会话，选择不继续使用时，会先把旧会话移动到
`session.json.backup` 或带数字后缀的备份文件，再开始新登录。

## 6. 安装后检查

查看服务状态：

```bash
systemctl --user status telebox.service
```

持续查看日志：

```bash
journalctl --user -u telebox.service -f
```

查看程序版本：

```bash
~/.local/bin/telebox -version
```

在 Telegram 的“我的收藏”中测试：

```text
-ping
-status
-help
```

## 7. 命令帮助

发送 `-help` 会显示紧凑的命令名称列表，不展开插件列表、源码或模块
信息。

查看特定命令：

```text
-help ping
-help tpm
-help p
```

通过别名查询时，也会显示对应主命令的说明、别名和权限要求。

## 8. 核心命令

| 命令 | 说明 |
| --- | --- |
| `-ping` | 检查 Telegram 连接和消息延迟 |
| `-status` | 查看版本、资源、插件数量和运行时间 |
| `-help` | 查看命令名称列表 |
| `-help 命令` | 查看指定命令的说明 |
| `-update check` | 检查最新 TeleBox-Go 版本 |
| `-update` | 更新 TeleBox-Go 并自动重启 |
| `-update force` | 重新安装最新正式版 |
| `-prefix show` | 查看当前命令前缀 |
| `-prefix set -` | 把前缀设置为 `-` |
| `-prefix add .` | 增加一个兼容前缀 |
| `-prefix remove .` | 删除一个前缀 |
| `-p help` | 查看插件管理帮助 |

`tpm` 是插件管理器的正式名称，`p` 和 `t` 是短别名。以下写法等价：

```text
-p ls
-t ls
-tpm ls
```

## 9. 插件管理

主程序只内置核心管理命令，业务插件按需安装。

查看已安装插件：

```text
-p ls
```

搜索插件：

```text
-p s
-p s 关键词
```

安装插件：

```text
-p i bin
-p i bin@0.2.1
```

安装全部官方插件：

```text
-p i all
```

更新插件：

```text
-p u
-p u bin
```

启用、停用和卸载：

```text
-p on bin
-p off bin
-p rm bin
```

查看插件信息和检查安装状态：

```text
-p info bin
-p doctor
```

插件包从项目的 GitHub Release 下载，并检查 HTTPS、文件大小和
SHA-256。插件在独立子进程中运行，单个插件退出不会直接带停主程序。

## 10. 更新 TeleBox-Go

### 在 Telegram 中更新框架

检查是否有新版本：

```text
-update check
```

更新到 GitHub 最新正式版：

```text
-update
```

重新安装当前最新正式版：

```text
-update force
```

框架更新命令仅允许账号所有者使用，目前支持 Linux `amd64` 和
Linux `arm64`。更新过程会：

1. 查询项目 GitHub Release 的最新正式版本。
2. 选择与当前 CPU 架构匹配的发布包。
3. 下载 `SHA256SUMS.txt` 和主程序包。
4. 校验下载包的 SHA-256。
5. 把当前程序备份为 `telebox.previous`。
6. 原子替换正在运行的主程序。
7. 退出并由 systemd 自动启动新版本。

配置、Telegram 会话、数据库和插件不会被替换。SHA-256 校验失败时
不会修改当前主程序。

首次从不支持 `-update` 的旧版本升级，或 Telegram 更新失败时，在
Linux 终端运行：

```bash
curl -fsSL https://raw.githubusercontent.com/Acacia415/TeleBox-Go/main/scripts/install.sh | sh
```

终端更新时：

- 已有 API ID 不会显示。
- API Hash 输入不回显。
- 直接回车保留已有登录会话。
- `config.json` 不会被覆盖。
- 数据库、资源和插件目录不会被删除。
- systemd 服务会在替换程序前停止，安装完成后重新启动。

框架自更新后的旧程序位于：

```text
~/.local/bin/telebox.previous
```

如果新版本无法启动，可以恢复：

```bash
systemctl --user stop telebox.service
install -m 0755 ~/.local/bin/telebox.previous ~/.local/bin/telebox
systemctl --user start telebox.service
```

更新后检查：

```bash
~/.local/bin/telebox -version
systemctl --user status telebox.service
journalctl --user -u telebox.service -n 50 --no-pager
```

### 更新插件

在 Telegram 中发送：

```text
-p u
```

这会更新全部已安装插件。只更新一个插件：

```text
-p u 插件名
```

如果新插件要求更高版本的 TeleBox-Go，请先更新框架，再更新插件。

### 更新外部工具

部分插件依赖系统工具，它们不属于 TeleBox-Go 框架：

- `yt-dlp` 插件可以使用 `-yt update` 更新 yt-dlp。
- 视频或音频转换通常需要 `ffmpeg`。
- Ookla 测速插件需要 Speedtest CLI。
- 多服务器测速需要 SSH 客户端。

插件提示缺少工具时，应使用 Linux 发行版的软件包管理器或对应工具的
官方安装方式处理。

## 11. 服务管理

启动：

```bash
systemctl --user start telebox.service
```

停止：

```bash
systemctl --user stop telebox.service
```

重启：

```bash
systemctl --user restart telebox.service
```

设置登录后自动启动：

```bash
systemctl --user enable telebox.service
```

取消自动启动：

```bash
systemctl --user disable telebox.service
```

重新加载服务配置：

```bash
systemctl --user daemon-reload
```

## 12. 文件位置

默认位置：

| 内容 | 路径 |
| --- | --- |
| 主程序 | `~/.local/bin/telebox` |
| JSON 配置 | `~/.config/telebox/config.json` |
| 环境变量 | `~/.config/telebox/telebox.env` |
| systemd 服务 | `~/.config/systemd/user/telebox.service` |
| Telegram 会话 | `~/.local/share/telebox/session.json` |
| SQLite 数据库 | `~/.local/share/telebox/telebox.db` |
| 插件目录 | `~/.local/share/telebox/plugins` |
| 资源目录 | `~/.local/share/telebox/assets` |
| 登录二维码 | `~/.local/share/telebox/login-qr.png` |
| 上一版主程序 | `~/.local/bin/telebox.previous` |

`telebox.env` 和 `session.json` 都包含敏感信息，不应上传、公开或发送给
他人。

## 13. 配置与安全

环境文件常用变量：

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

修改环境文件后重启：

```bash
systemctl --user restart telebox.service
```

建议：

- 不要把 API Hash、二步验证密码或 session 文件发给他人。
- 不要把 `telebox.env` 和数据目录提交到 Git。
- 只使用内置 `-update`、项目正式 Release 或一键安装脚本更新。
- 插件安装后可运行 `-p doctor` 检查状态。

## 14. 备份

停止服务后再备份，避免复制到写入中的数据库：

```bash
systemctl --user stop telebox.service
tar -czf telebox-backup.tar.gz \
  ~/.config/telebox \
  ~/.local/share/telebox
systemctl --user start telebox.service
```

备份包包含 API 配置和 Telegram 会话，应当加密保存。

## 15. 常见问题

### 安装后没有登录提示

新安装器从 `/dev/tty` 读取输入。请在正常 SSH 交互终端运行，不要通过
没有 TTY 的后台任务执行。查看安装器版本是否为最新：

```bash
curl -fsSL https://raw.githubusercontent.com/Acacia415/TeleBox-Go/main/scripts/install.sh |
  grep TELEBOX_INSTALL_LOGIN_MODE
```

### 服务启动后马上退出

```bash
journalctl --user -u telebox.service -n 100 --no-pager
```

重点检查：

- API ID 或 API Hash 是否正确。
- Telegram 会话是否有效。
- 是否能够连接 Telegram。
- 配置文件路径是否存在。

### Telegram 命令没有响应

1. 先在“我的收藏”中发送 `-ping`。
2. 用 `-prefix show` 检查当前前缀。
3. 用 `-help` 确认命令是否已注册。
4. 用 `-p ls` 检查插件是否已安装并启用。
5. 用 `-p doctor` 检查插件状态。
6. 查看 systemd 日志。

### `-update` 后没有自动重启

先检查状态和日志：

```bash
systemctl --user status telebox.service
journalctl --user -u telebox.service -n 100 --no-pager
```

然后手动重启：

```bash
systemctl --user restart telebox.service
```

如果不是通过 systemd 运行，`-update` 替换程序后进程会退出，需要自行
重新启动。

### Telegram 活跃会话显示旧版本

新版本启动并重新连接 Telegram 后，TeleBox-Go 会把真实构建版本报告给
Telegram。可先确认本机版本并重启服务：

```bash
~/.local/bin/telebox -version
systemctl --user restart telebox.service
```

Telegram 客户端的“设备 → 活跃会话”页面可能需要重新打开后才刷新。

### `permission denied`

`-prefix`、`-p` 等管理命令仅允许账号所有者使用。TeleBox-Go 作为用户
客户端运行时，登录账号自己发出的消息会被识别为所有者命令。

### 插件更新失败

依次检查：

```text
-p doctor
-p s 插件名
-p info 插件名
```

同时检查 GitHub 网络连接、磁盘空间以及 TeleBox-Go 框架版本。

## 16. 获取帮助

- 使用手册：本文件
- 项目主页：[Acacia415/TeleBox-Go](https://github.com/Acacia415/TeleBox-Go)
- 正式版本：[GitHub Releases](https://github.com/Acacia415/TeleBox-Go/releases)
- 问题反馈：[GitHub Issues](https://github.com/Acacia415/TeleBox-Go/issues)
