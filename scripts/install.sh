#!/bin/sh
set -eu

REPOSITORY="Acacia415/TeleBox-Go"
VERSION="latest"
PREFIX="${HOME}/.local"
CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/telebox"
DATA_DIR="${XDG_DATA_HOME:-${HOME}/.local/share}/telebox"
START_SERVICE=1

usage() {
    cat <<'EOF'
TeleBox-Go Linux installer

Usage:
  install.sh [--version v0.2.0] [--prefix PATH] [--no-start]

Environment:
  TELEBOX_API_ID       Telegram API ID (optional during installation)
  TELEBOX_API_HASH     Telegram API hash (optional during installation)
  TELEBOX_INSTALL_VERSION
  TELEBOX_INSTALL_PREFIX
EOF
}

fail() {
    printf 'TeleBox-Go: %s\n' "$*" >&2
    exit 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || fail "缺少命令：$1"
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version)
            [ "$#" -ge 2 ] || fail "--version 缺少值"
            VERSION="$2"
            shift 2
            ;;
        --prefix)
            [ "$#" -ge 2 ] || fail "--prefix 缺少值"
            PREFIX="$2"
            shift 2
            ;;
        --no-start)
            START_SERVICE=0
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            fail "未知参数：$1"
            ;;
    esac
done

VERSION="${TELEBOX_INSTALL_VERSION:-$VERSION}"
PREFIX="${TELEBOX_INSTALL_PREFIX:-$PREFIX}"
case "${PREFIX}${CONFIG_DIR}${DATA_DIR}" in
    *[[:space:]]*) fail "安装路径暂不支持空格" ;;
esac

need curl
need tar
need awk
need find
need uname
need mktemp
need install

case "$(uname -m)" in
    x86_64|amd64)
        ARCH="amd64"
        ;;
    aarch64|arm64)
        ARCH="arm64"
        ;;
    *)
        fail "暂不支持的 CPU 架构：$(uname -m)"
        ;;
esac

if [ "$VERSION" = "latest" ]; then
    LATEST_URL="https://github.com/${REPOSITORY}/releases/latest"
    EFFECTIVE_URL="$(curl --proto '=https' --tlsv1.2 -fsSL \
        -o /dev/null -w '%{url_effective}' "$LATEST_URL")"
    VERSION="${EFFECTIVE_URL##*/}"
fi
case "$VERSION" in
    v*) ;;
    *) VERSION="v${VERSION}" ;;
esac

ASSET="telebox-go_${VERSION}_linux_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPOSITORY}/releases/download/${VERSION}"
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/telebox-install.XXXXXX")"
trap 'rm -rf "$TEMP_DIR"' EXIT HUP INT TERM

printf '下载 TeleBox-Go %s (%s)...\n' "$VERSION" "$ARCH"
curl --proto '=https' --tlsv1.2 -fL \
    "${BASE_URL}/${ASSET}" -o "${TEMP_DIR}/${ASSET}"
curl --proto '=https' --tlsv1.2 -fL \
    "${BASE_URL}/SHA256SUMS.txt" -o "${TEMP_DIR}/SHA256SUMS.txt"

EXPECTED="$(awk -v file="$ASSET" '
    $2 == file || $2 == "*" file { print $1; exit }
' "${TEMP_DIR}/SHA256SUMS.txt")"
[ -n "$EXPECTED" ] || fail "校验文件中没有 ${ASSET}"
if command -v sha256sum >/dev/null 2>&1; then
    ACTUAL="$(sha256sum "${TEMP_DIR}/${ASSET}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
    ACTUAL="$(shasum -a 256 "${TEMP_DIR}/${ASSET}" | awk '{print $1}')"
else
    fail "缺少 sha256sum 或 shasum"
fi
[ "$EXPECTED" = "$ACTUAL" ] || fail "安装包 SHA-256 校验失败"

mkdir -p "${TEMP_DIR}/extract"
tar -tzf "${TEMP_DIR}/${ASSET}" | awk '
    /^\// || /(^|\/)\.\.(\/|$)/ { bad = 1 }
    END { exit bad }
' || fail "安装包包含不安全路径"
tar -xzf "${TEMP_DIR}/${ASSET}" -C "${TEMP_DIR}/extract"

TELEBOX_SOURCE="$(find "${TEMP_DIR}/extract" -type f -name telebox -print | awk 'NR == 1')"
[ -n "$TELEBOX_SOURCE" ] || fail "安装包中没有 telebox"
EXAMPLE_SOURCE="$(find "${TEMP_DIR}/extract" -type f -name config.example.json -print | awk 'NR == 1')"
[ -n "$EXAMPLE_SOURCE" ] || fail "安装包中没有 config.example.json"

install -d -m 0755 "${PREFIX}/bin"
install -d -m 0700 "$CONFIG_DIR" "$DATA_DIR" \
    "${DATA_DIR}/assets" "${DATA_DIR}/plugins"
install -m 0755 "$TELEBOX_SOURCE" "${PREFIX}/bin/telebox"
install -m 0644 "$EXAMPLE_SOURCE" "${CONFIG_DIR}/config.example.json"
if [ ! -f "${CONFIG_DIR}/config.json" ]; then
    install -m 0600 "$EXAMPLE_SOURCE" "${CONFIG_DIR}/config.json"
fi

ENV_FILE="${CONFIG_DIR}/telebox.env"
if [ ! -f "$ENV_FILE" ]; then
    API_ID="${TELEBOX_API_ID:-}"
    API_HASH="${TELEBOX_API_HASH:-}"
    umask 077
    {
        printf 'TELEBOX_API_ID=%s\n' "$API_ID"
        printf 'TELEBOX_API_HASH=%s\n' "$API_HASH"
        printf 'TELEBOX_LOGIN_MODE=qr\n'
        printf 'TELEBOX_SESSION_FILE=%s\n' "${DATA_DIR}/session.json"
        printf 'TELEBOX_STORAGE_PATH=%s\n' "${DATA_DIR}/telebox.db"
        printf 'TELEBOX_ASSETS_PATH=%s\n' "${DATA_DIR}/assets"
        printf 'TELEBOX_PLUGIN_DIR=%s\n' "${DATA_DIR}/plugins"
    } >"$ENV_FILE"
fi
chmod 0600 "$ENV_FILE"

SYSTEMD_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/systemd/user"
SERVICE_FILE="${SYSTEMD_DIR}/telebox.service"
install -d -m 0755 "$SYSTEMD_DIR"
cat >"$SERVICE_FILE" <<EOF
[Unit]
Description=TeleBox-Go Telegram userbot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=${ENV_FILE}
ExecStart=${PREFIX}/bin/telebox -config ${CONFIG_DIR}/config.json
WorkingDirectory=${DATA_DIR}
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=default.target
EOF

HAS_API="$(awk -F= '
    $1 == "TELEBOX_API_ID" && length($2) > 0 { id = 1 }
    $1 == "TELEBOX_API_HASH" && length($2) > 0 { hash = 1 }
    END { if (id && hash) print "yes" }
' "$ENV_FILE")"

if [ "$START_SERVICE" -eq 1 ] &&
    [ "$HAS_API" = "yes" ] &&
    command -v systemctl >/dev/null 2>&1; then
    if systemctl --user daemon-reload &&
        systemctl --user enable --now telebox.service; then
        printf 'TeleBox-Go 已安装并启动。\n'
        printf '查看日志：journalctl --user -u telebox -f\n'
    else
        printf 'TeleBox-Go 已安装，但当前用户的 systemd 服务未能启动。\n'
        printf '可手动运行：set -a; . %s; set +a; %s/bin/telebox -config %s/config.json\n' \
            "$ENV_FILE" "$PREFIX" "$CONFIG_DIR"
    fi
else
    printf 'TeleBox-Go 已安装到 %s/bin/telebox\n' "$PREFIX"
    if [ "$HAS_API" != "yes" ]; then
        printf '请编辑 %s，填入 TELEBOX_API_ID 和 TELEBOX_API_HASH。\n' "$ENV_FILE"
    fi
    printf '启动服务：systemctl --user enable --now telebox.service\n'
    printf '手动运行：set -a; . %s; set +a; %s/bin/telebox -config %s/config.json\n' \
        "$ENV_FILE" "$PREFIX" "$CONFIG_DIR"
fi
