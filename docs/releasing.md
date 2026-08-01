# 发布维护手册

TeleBox-Go 保持单一 monorepo，但框架和业务插件独立发布。这样修复一个插件
时不需要重新编译或上传其余插件。

## 发布通道

| 通道 | 标签或 Release | 内容 |
| --- | --- | --- |
| 框架正式版 | `v<版本>` | Linux amd64/arm64 主程序、迁移器、安装脚本、目录兼容快照和校验文件 |
| 单插件版本 | `plugin-<名称>-v<版本>` | 该插件的 Linux amd64/arm64 ZIP 和校验文件 |
| 插件目录 | `plugin-registry` | 可变的 `plugin-catalog.json` 与目录摘要 |

插件版本和 `plugin-registry` 标记为 Pre-release，不会影响 GitHub 的“最新
正式版本”。因此 `-update` 始终只查找框架版本，`-p u` 独立查找插件版本。

## 发布框架

框架代码合并并通过 CI 后创建 `v*` 标签：

```bash
git tag -a v0.8.0 -m "TeleBox-Go v0.8.0"
git push origin v0.8.0
```

`.github/workflows/release.yml` 只构建两种 Linux 架构的主程序和迁移器，不再
编译全部插件。工作流会把发布时的插件目录快照放入框架 Release，保证仍在
使用旧目录地址的客户端可以正常工作。

## 发布单个插件

先修改插件 `Metadata()` 中的版本并通过测试。然后可以推送插件标签：

```bash
git tag -a plugin-speedlink-v0.4.2 -m "speedlink 0.4.2"
git push origin plugin-speedlink-v0.4.2
```

也可以在 GitHub Actions 中手动运行 `plugin-release`，填写规范插件名和
`Metadata()` 中的版本。工作流会：

1. 下载当前 `plugin-registry` 目录。
2. 只构建所选插件的 amd64 和 arm64 包。
3. 验证标签版本与插件元数据一致。
4. 发布独立的插件 Pre-release。
5. 把新版本合并进目录，并保留最近三个版本。
6. 更新目录文件和目录 SHA-256。

例如更新 YT 插件时使用 `plugin=yt-dlp`，不会重新构建 SpeedLink 或其他插件。

## 何时需要批量发布插件

普通业务逻辑、帮助文本和单插件错误修复只发布对应插件。主程序内部修复只
发布框架。只有插件 RPC、manifest API 或共享插件运行时代码发生不兼容变化
时，才需要提升受影响插件的版本并逐个触发插件发布。

`min_host` 用于阻止旧框架安装依赖新能力的插件。新增主程序能力但保持协议
向后兼容时，也只需要发布使用该能力的插件。

## 本地演练

下载当前目录后可以在本地验证增量合并：

```bash
go run ./cmd/telebox-plugin-sdk release \
  -plugins speedlink \
  -base-catalog plugin-catalog.json \
  -keep-releases 3 \
  -tag plugin-speedlink-v0.4.2 \
  -platforms linux/amd64,linux/arm64 \
  -output dist/plugins
```

输出目录只应出现两个 SpeedLink ZIP 和 `catalog.json`。发布前必须运行：

```bash
go test ./...
go vet ./...
```
