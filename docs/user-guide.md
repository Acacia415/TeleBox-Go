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

大版本升级时也不需要重新安装。先发送 `-update`，等待服务自动重启并用
`-status` 确认框架版本；然后发送 `-p u` 更新已安装插件。只有
`-update` 无法完成或当前版本尚不支持在线更新时，才需要重新运行一键安装
命令。

## 2. 支持环境

- Linux `amd64`
- Linux `arm64`
- 使用 systemd
- 可访问 Telegram 和 GitHub Release

安装到当前用户目录，不要求 root。使用 root 安装时，文件会位于
`/root` 下，并创建系统级服务；普通用户安装时位于该用户的主目录下，
安装器会启用 linger 后创建用户级服务。SSH 断开后两种服务都会继续运行。

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
7. 创建并启动不会随 SSH 会话退出的 systemd 服务。

从早期版本升级时建议重新运行一次一键安装命令。root 安装器会停止并移除
旧的用户级 `telebox.service`，迁移为系统级服务，避免两个 Telegram 客户端
同时运行。配置、会话、数据库和插件不会被删除。

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
# root 安装
systemctl status telebox.service

# 普通用户安装
systemctl --user status telebox.service
```

持续查看日志：

```bash
# root 安装
journalctl -u telebox.service -f

# 普通用户安装
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
| `-ping [目标]` | 检查 Telegram、主机或 DC1–DC5 的网络延迟 |
| `-status` | 查看版本、资源、插件数量和运行时间 |
| `-sysinfo` | 查看系统、CPU、内存、磁盘和网络详情 |
| `-help` | 查看命令名称列表 |
| `-help 命令` | 查看指定命令的说明 |
| `-update check` | 检查最新 TeleBox-Go 版本 |
| `-update` | 更新 TeleBox-Go 并自动重启 |
| `-update force` / `-update -f` | 重新安装最新正式版 |
| `-prefix show` | 查看当前命令前缀 |
| `-prefix set -` | 把前缀设置为 `-` |
| `-prefix add .` | 增加一个兼容前缀 |
| `-prefix remove .` | 删除一个前缀 |
| `-alias set de bd` | 创建动态命令别名 |
| `-alias ls` | 查看动态命令别名 |
| `-exec 命令` | 在主机执行 Shell 命令 |
| `-loglevel [等级]` | 查看或动态设置日志等级 |
| `-sendlog` | 发送、设置目标或清理日志 |
| `-reload` | 重启全部已启用业务插件 |
| `-restart` | 重启 TeleBox-Go 进程，兼容 `exit`、`pmr` |
| `-bf` / `-hf` | 创建或恢复 TeleBox-Go 数据备份 |
| `-sudo` / `-sure` | 管理委托命令和受控消息权限 |
| `-p help` | 查看插件管理帮助 |

`tpm` 是插件管理器的正式名称，`p` 和 `t` 是短别名。以下写法等价：

```text
-p ls
-t ls
-tpm ls
```

## 9. 插件管理

主程序只内置核心管理命令，业务插件按需安装。

查看已安装插件和详情：

```text
-p ls
-p ls -v
-p lv
```

搜索插件：

```text
-p s
-p s 关键词
```

安装一个或多个插件：

```text
-p i bin
-p i bin ip rate
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
-p ua
-p updateAll
```

启用、停用和卸载一个或多个插件：

```text
-p on bin
-p off bin
-p rm bin ip
-p rm all
```

查看插件信息和检查安装状态：

```text
-p info bin
-p doctor
```

回复一个由 TeleBox-Go 插件 SDK 编译的 `.zip` 或 `.tar.gz` 插件包后发送
`-p i`，可以本地安装。导出当前机器已安装的插件包：

```text
-p upload bin
-p ul bin
```

本地包是所有者主动提供的，因此不会经过官方目录的 SHA-256 对照；它仍会
接受归档路径、文件类型、体积、平台、插件 API 版本和 manifest 校验。

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
systemctl stop telebox.service
install -m 0755 ~/.local/bin/telebox.previous ~/.local/bin/telebox
systemctl start telebox.service
```

普通用户安装请在上面的 `systemctl` 后增加 `--user`。

更新后检查：

```bash
~/.local/bin/telebox -version
systemctl status telebox.service
journalctl -u telebox.service -n 50 --no-pager
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
- `-yt setup` 可自动下载并校验 yt-dlp，`-yt doctor` 可检查完整环境。
- YouTube 当前需要 JavaScript 运行时；服务器环境建议配置 Deno。
- 服务器 IP 遇到 YouTube 风控时，可用 `-yt cookies` 和 `-yt proxy`
  配置 Cookies 与代理；代理不能代替登录 Cookies。
- 视频或音频转换通常需要 `ffmpeg`。
- 多服务器测速需要 SSH 客户端。

插件提示缺少工具时，应使用 Linux 发行版的软件包管理器或对应工具的
官方安装方式处理。

## 11. 服务管理

下面默认展示 root 安装产生的系统级服务命令。普通用户安装时，在每条
`systemctl` 或 `journalctl` 命令后增加 `--user`。

启动：

```bash
systemctl start telebox.service
```

停止：

```bash
systemctl stop telebox.service
```

重启：

```bash
systemctl restart telebox.service
```

设置登录后自动启动：

```bash
systemctl enable telebox.service
```

取消自动启动：

```bash
systemctl disable telebox.service
```

重新加载服务配置：

```bash
systemctl daemon-reload
```

## 12. 文件位置

默认位置：

| 内容 | 路径 |
| --- | --- |
| 主程序 | `~/.local/bin/telebox` |
| JSON 配置 | `~/.config/telebox/config.json` |
| 环境变量 | `~/.config/telebox/telebox.env` |
| systemd 服务（root） | `/etc/systemd/system/telebox.service` |
| systemd 服务（普通用户） | `~/.config/systemd/user/telebox.service` |
| Telegram 会话 | `~/.local/share/telebox/session.json` |
| SQLite 数据库 | `~/.local/share/telebox/telebox.db` |
| 插件目录 | `~/.local/share/telebox/plugins` |
| 资源目录 | `~/.local/share/telebox/assets` |
| 旧版插件数据保留目录 | `~/.local/share/telebox/legacy-assets` |
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
TELEBOX_LEGACY_ASSETS_PATH
TELEBOX_PLUGIN_DIR
TELEBOX_PLUGIN_CATALOG
TELEBOX_LOG_LEVEL
TELEBOX_LOG_FORMAT
```

修改环境文件后重启：

```bash
systemctl restart telebox.service
```

普通用户安装使用 `systemctl --user restart telebox.service`。

建议：

- 不要把 API Hash、二步验证密码或 session 文件发给他人。
- 不要把 `telebox.env` 和数据目录提交到 Git。
- 只使用内置 `-update`、项目正式 Release 或一键安装脚本更新。
- 插件安装后可运行 `-p doctor` 检查状态。

## 14. 从原版 TeleBox 备份迁移

正式发布包中包含 `telebox-migrate`。它只用于首次迁移原版 TeleBox 的
`.tar.gz` 全量备份，不用于恢复 `-bf` 创建的 TeleBox-Go 备份。

建议在独立空目录中转换并测试，迁移器会拒绝覆盖已有配置、会话或资源目录：

```bash
mkdir -m 700 ~/telebox-migration
cd ~/telebox-migration
```

先检查备份。该命令只读取压缩包，不输出 API Hash、Session 或数据库内容：

```bash
/path/to/telebox-migrate inspect \
  -archive /path/to/telebox-backup.tar.gz
```

不带 `-apply` 的转换命令也是 dry-run，只显示检查结果：

```bash
/path/to/telebox-migrate convert \
  -archive /path/to/telebox-backup.tar.gz \
  -config config.json \
  -session data/session.json \
  -assets data/assets \
  -legacy-assets data/legacy-assets
```

确认无误后增加 `-apply`，生成 TeleBox-Go 配置、gotd 会话和插件资产：

```bash
/path/to/telebox-migrate convert \
  -archive /path/to/telebox-backup.tar.gz \
  -config config.json \
  -session data/session.json \
  -assets data/assets \
  -legacy-assets data/legacy-assets \
  -apply
```

转换完成后先检查配置并启动：

```bash
/path/to/telebox -config config.json -check-config
/path/to/telebox -config config.json
```

主程序只内置核心功能，迁移器不会把旧 TypeScript 插件作为 Go 插件安装。
登录成功后在 Telegram 中安装当前官方插件：

```text
-p i all
-p doctor
```

旧 alias、sudo、sure 和当前 Go 插件支持的数据会从迁移后的活动资产中读取，
并在插件首次启动时写入 Go 版存储。除此之外，原备份中
`telebox/assets` 下的全部插件数据都会完整保存到 `data/legacy-assets`：

- 文件固定为不可执行的私有权限，不会被框架或插件自动运行。
- `_legacy_manifest*.json` 记录来源备份摘要、相对路径、大小和逐文件 SHA-256。
- 暂不支持的旧插件不会被启用，但其数据可由未来的 Go 插件安全导入。
- 原压缩包不会被修改，也不会复制旧 API 配置或登录会话到该目录。

如果目标机器已经通过一键脚本运行 TeleBox-Go，不要直接覆盖正在使用的
`~/.config/telebox` 或 `~/.local/share/telebox`。应先停止服务，在独立目录
完成转换和启动验证，再把确认后的配置、会话和资产迁入正式目录。

迁移完成并开始使用 TeleBox-Go 后，日常备份与恢复使用下面的 `-bf` 和
`-hf`，不再使用 `telebox-migrate`。

## 15. 备份

标准备份包含已安装插件、活动插件资产、保留的旧版插件数据和一致的 SQLite
数据库快照：

```text
-bf
```

全量备份另外包含 JSON 配置，但仍不会包含 Telegram 登录会话、日志和
主程序：

```text
-bf all
```

管理默认发送目标，或只在本次发送到指定目标：

```text
-bf set me -1001234567890
-bf to @username
-bf del -1001234567890
-bf del all
```

恢复时回复由 `-bf` 生成的 `.tar.gz` 文件并发送：

```text
-hf
```

程序会先检查备份格式、路径、文件类型、数量、体积和每个文件的 SHA-256，
校验通过后暂存备份并重启。在数据库打开前应用恢复，当前文件会进入带时间戳
的回滚目录。无效归档不会覆盖现有数据。

如需操作系统级灾难恢复，可在停止服务后另外加密备份
`~/.config/telebox` 与 `~/.local/share/telebox`；该方式会包含 API 配置和
Telegram 会话，不应上传到公开位置。

## 16. 常见问题

### 安装后没有登录提示

新安装器从 `/dev/tty` 读取输入。请在正常 SSH 交互终端运行，不要通过
没有 TTY 的后台任务执行。查看安装器版本是否为最新：

```bash
curl -fsSL https://raw.githubusercontent.com/Acacia415/TeleBox-Go/main/scripts/install.sh |
  grep TELEBOX_INSTALL_LOGIN_MODE
```

### SSH 断开后 Bot 失联

v0.4.0 及更早安装器只创建用户级服务，root 的用户管理器可能在最后一个
SSH 会话退出后停止。直接重新运行最新一键安装命令即可：root 会迁移为
系统级服务，普通用户会启用 linger。然后检查：

```bash
# root 安装
systemctl status telebox.service

# 普通用户安装
loginctl show-user "$USER" -p Linger
systemctl --user status telebox.service
```

普通用户的 `Linger` 必须为 `yes`。如果安装器无法自动启用，可先执行：

```bash
sudo loginctl enable-linger "$USER"
```

### 服务启动后马上退出

```bash
journalctl -u telebox.service -n 100 --no-pager
```

普通用户安装使用 `journalctl --user -u telebox.service -n 100 --no-pager`。

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
systemctl status telebox.service
journalctl -u telebox.service -n 100 --no-pager
```

然后手动重启：

```bash
systemctl restart telebox.service
```

普通用户安装在这些命令后增加 `--user`。

如果不是通过 systemd 运行，`-update` 替换程序后进程会退出，需要自行
重新启动。

### Telegram 活跃会话显示旧版本

新版本启动并重新连接 Telegram 后，TeleBox-Go 会把真实构建版本报告给
Telegram。可先确认本机版本并重启服务：

```bash
~/.local/bin/telebox -version
systemctl restart telebox.service
```

普通用户安装使用 `systemctl --user restart telebox.service`。

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

## 17. 获取帮助

- 使用手册：本文件
- 项目主页：[Acacia415/TeleBox-Go](https://github.com/Acacia415/TeleBox-Go)
- 正式版本：[GitHub Releases](https://github.com/Acacia415/TeleBox-Go/releases)
- 问题反馈：[GitHub Issues](https://github.com/Acacia415/TeleBox-Go/issues)
