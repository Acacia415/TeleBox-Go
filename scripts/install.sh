#!/bin/sh
set -eu

REPOSITORY="Acacia415/TeleBox-Go"
VERSION="latest"
PREFIX="${HOME}/.local"
CONFIG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/telebox"
DATA_DIR="${XDG_DATA_HOME:-${HOME}/.local/share}/telebox"
START_SERVICE=1
PERFORM_LOGIN=1
TTY_PATH="/dev/tty"
TEMP_DIR=""
TTY_STATE=""

usage() {
    cat <<'EOF'
TeleBox-Go Linux installer

Usage:
  install.sh [--version v0.2.0] [--prefix PATH] [--no-start] [--no-login]

Environment:
  TELEBOX_API_ID                 Telegram API ID
  TELEBOX_API_HASH               Telegram API hash
  TELEBOX_INSTALL_LOGIN_MODE     qr or phone
  TELEBOX_INSTALL_VERSION
  TELEBOX_INSTALL_PREFIX

Without environment overrides, the installer prompts for the API credentials
and lets you choose QR or phone-number login. Interactive prompts work when the
script is run through "curl ... | sh".
EOF
}

fail() {
    printf 'TeleBox-Go: %s\n' "$*" >&2
    exit 1
}

need() {
    command -v "$1" >/dev/null 2>&1 || fail "缺少命令：$1"
}

cleanup() {
    if [ -n "$TTY_STATE" ] && [ -r "$TTY_PATH" ]; then
        stty "$TTY_STATE" <"$TTY_PATH" 2>/dev/null || true
        TTY_STATE=""
    fi
    if [ -n "$TEMP_DIR" ] && [ -d "$TEMP_DIR" ]; then
        rm -rf "$TEMP_DIR"
    fi
}

trap cleanup EXIT
trap 'exit 130' HUP INT TERM

has_tty() {
    [ -r "$TTY_PATH" ] && [ -w "$TTY_PATH" ]
}

prompt_line() {
    has_tty || fail "当前没有可交互终端；请直接在 SSH 终端中运行安装命令"
    printf '%s' "$1" >"$TTY_PATH"
    IFS= read -r REPLY <"$TTY_PATH" ||
        fail "无法从终端读取输入"
}

prompt_secret() {
    has_tty || fail "当前没有可交互终端；请直接在 SSH 终端中运行安装命令"
    printf '%s' "$1" >"$TTY_PATH"
    TTY_STATE="$(stty -g <"$TTY_PATH")" ||
        fail "无法读取终端状态"
    stty -echo <"$TTY_PATH"
    read_status=0
    IFS= read -r REPLY <"$TTY_PATH" || read_status=$?
    stty "$TTY_STATE" <"$TTY_PATH"
    TTY_STATE=""
    printf '\n' >"$TTY_PATH"
    [ "$read_status" -eq 0 ] || fail "无法从终端读取输入"
}

read_env_value() {
    key="$1"
    file="$2"
    [ -f "$file" ] || return 0
    awk -v key="$key" '
        index($0, key "=") == 1 {
            print substr($0, length(key) + 2)
            exit
        }
    ' "$file"
}

valid_api_id() {
    case "$1" in
        ""|0|*[!0-9]*) return 1 ;;
        *) return 0 ;;
    esac
}

valid_api_hash() {
    [ "${#1}" -eq 32 ] || return 1
    case "$1" in
        *[!0-9a-fA-F]*) return 1 ;;
        *) return 0 ;;
    esac
}

backup_session() {
    source_file="$1"
    backup_file="${source_file}.backup"
    suffix=1
    while [ -e "$backup_file" ]; do
        backup_file="${source_file}.backup.${suffix}"
        suffix=$((suffix + 1))
    done
    mv "$source_file" "$backup_file"
    printf '原会话已备份到 %s\n' "$backup_file"
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
        --no-login)
            PERFORM_LOGIN=0
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
if [ "$PERFORM_LOGIN" -eq 1 ]; then
    need stty
fi

ENV_FILE="${CONFIG_DIR}/telebox.env"
SESSION_FILE="${DATA_DIR}/session.json"
EXISTING_API_ID="$(read_env_value TELEBOX_API_ID "$ENV_FILE")"
EXISTING_API_HASH="$(read_env_value TELEBOX_API_HASH "$ENV_FILE")"
API_ID="${TELEBOX_API_ID:-$EXISTING_API_ID}"
API_HASH="${TELEBOX_API_HASH:-$EXISTING_API_HASH}"
LOGIN_MODE="${TELEBOX_INSTALL_LOGIN_MODE:-}"
NEED_LOGIN="$PERFORM_LOGIN"
REPLACE_SESSION=0

if [ "$PERFORM_LOGIN" -eq 1 ]; then
    has_tty ||
        fail "登录需要交互终端；请在 SSH 终端运行，或使用 --no-login 仅安装"

    if [ -n "${TELEBOX_API_ID:-}" ]; then
        valid_api_id "$API_ID" ||
            fail "TELEBOX_API_ID 必须是大于零的数字"
    else
        while :; do
            if [ -n "$API_ID" ]; then
                prompt_line "已检测到 Telegram API ID，直接回车保留，或输入新值："
                [ -n "$REPLY" ] && API_ID="$REPLY"
            else
                prompt_line "请输入 Telegram API ID："
                API_ID="$REPLY"
            fi
            valid_api_id "$API_ID" && break
            printf 'API ID 必须是大于零的数字。\n' >"$TTY_PATH"
        done
    fi

    if [ -n "${TELEBOX_API_HASH:-}" ]; then
        valid_api_hash "$API_HASH" ||
            fail "TELEBOX_API_HASH 必须是 32 位十六进制字符串"
    else
        while :; do
            if [ -n "$API_HASH" ]; then
                prompt_secret "请输入 Telegram API Hash（直接回车保留现有值）："
                [ -n "$REPLY" ] && API_HASH="$REPLY"
            else
                prompt_secret "请输入 Telegram API Hash（输入内容不会显示）："
                API_HASH="$REPLY"
            fi
            valid_api_hash "$API_HASH" && break
            printf 'API Hash 必须是 32 位十六进制字符串。\n' >"$TTY_PATH"
        done
    fi

    if [ -s "$SESSION_FILE" ]; then
        prompt_line "检测到已有登录会话，是否继续使用？[Y/n]："
        case "$REPLY" in
            n|N|no|NO|No)
                REPLACE_SESSION=1
                ;;
            *)
                NEED_LOGIN=0
                ;;
        esac
    fi

    if [ "$NEED_LOGIN" -eq 1 ]; then
        if [ -n "$LOGIN_MODE" ]; then
            case "$LOGIN_MODE" in
                qr|phone) ;;
                *) fail "TELEBOX_INSTALL_LOGIN_MODE 必须是 qr 或 phone" ;;
            esac
        else
            while :; do
                printf '\n请选择 Telegram 登录方式：\n' >"$TTY_PATH"
                printf '  1) QR 二维码扫码\n' >"$TTY_PATH"
                printf '  2) 手机号 + 验证码\n' >"$TTY_PATH"
                prompt_line "请输入 1 或 2 [1]："
                case "$REPLY" in
                    ""|1)
                        LOGIN_MODE="qr"
                        break
                        ;;
                    2)
                        LOGIN_MODE="phone"
                        break
                        ;;
                    *)
                        printf '请输入 1 或 2。\n' >"$TTY_PATH"
                        ;;
                esac
            done
        fi
    fi
fi

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

if command -v systemctl >/dev/null 2>&1; then
    systemctl --user stop telebox.service >/dev/null 2>&1 || true
fi

install -d -m 0755 "${PREFIX}/bin"
install -d -m 0700 "$CONFIG_DIR" "$DATA_DIR" \
    "${DATA_DIR}/assets" "${DATA_DIR}/plugins"
install -m 0755 "$TELEBOX_SOURCE" "${PREFIX}/bin/telebox"
install -m 0644 "$EXAMPLE_SOURCE" "${CONFIG_DIR}/config.example.json"
if [ ! -f "${CONFIG_DIR}/config.json" ]; then
    install -m 0600 "$EXAMPLE_SOURCE" "${CONFIG_DIR}/config.json"
fi

ENV_OUTPUT="${TEMP_DIR}/telebox.env"
awk \
    -v api_id="$API_ID" \
    -v api_hash="$API_HASH" \
    -v session_file="$SESSION_FILE" \
    -v storage_path="${DATA_DIR}/telebox.db" \
    -v assets_path="${DATA_DIR}/assets" \
    -v plugin_dir="${DATA_DIR}/plugins" '
    BEGIN {
        values["TELEBOX_API_ID"] = api_id
        values["TELEBOX_API_HASH"] = api_hash
        values["TELEBOX_LOGIN_MODE"] = "existing"
        values["TELEBOX_SESSION_FILE"] = session_file
        values["TELEBOX_STORAGE_PATH"] = storage_path
        values["TELEBOX_ASSETS_PATH"] = assets_path
        values["TELEBOX_PLUGIN_DIR"] = plugin_dir
        order[1] = "TELEBOX_API_ID"
        order[2] = "TELEBOX_API_HASH"
        order[3] = "TELEBOX_LOGIN_MODE"
        order[4] = "TELEBOX_SESSION_FILE"
        order[5] = "TELEBOX_STORAGE_PATH"
        order[6] = "TELEBOX_ASSETS_PATH"
        order[7] = "TELEBOX_PLUGIN_DIR"
    }
    {
        separator = index($0, "=")
        key = separator > 0 ? substr($0, 1, separator - 1) : ""
        if (key in values) {
            if (!seen[key]++) {
                print key "=" values[key]
            }
            next
        }
        print
    }
    END {
        for (i = 1; i <= 7; i++) {
            key = order[i]
            if (!seen[key]) {
                print key "=" values[key]
            }
        }
    }
' "$ENV_FILE" 2>/dev/null >"$ENV_OUTPUT" || {
    : >"$ENV_OUTPUT"
    printf 'TELEBOX_API_ID=%s\n' "$API_ID" >>"$ENV_OUTPUT"
    printf 'TELEBOX_API_HASH=%s\n' "$API_HASH" >>"$ENV_OUTPUT"
    printf 'TELEBOX_LOGIN_MODE=existing\n' >>"$ENV_OUTPUT"
    printf 'TELEBOX_SESSION_FILE=%s\n' "$SESSION_FILE" >>"$ENV_OUTPUT"
    printf 'TELEBOX_STORAGE_PATH=%s\n' "${DATA_DIR}/telebox.db" >>"$ENV_OUTPUT"
    printf 'TELEBOX_ASSETS_PATH=%s\n' "${DATA_DIR}/assets" >>"$ENV_OUTPUT"
    printf 'TELEBOX_PLUGIN_DIR=%s\n' "${DATA_DIR}/plugins" >>"$ENV_OUTPUT"
}
install -m 0600 "$ENV_OUTPUT" "$ENV_FILE"

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

if [ "$NEED_LOGIN" -eq 1 ]; then
    if [ "$REPLACE_SESSION" -eq 1 ] && [ -e "$SESSION_FILE" ]; then
        backup_session "$SESSION_FILE"
    fi
    printf '\n开始 Telegram 登录，请按终端提示操作。\n' >"$TTY_PATH"
    if ! TELEBOX_API_ID="$API_ID" \
        TELEBOX_API_HASH="$API_HASH" \
        TELEBOX_LOGIN_MODE="existing" \
        TELEBOX_SESSION_FILE="$SESSION_FILE" \
        TELEBOX_STORAGE_PATH="${DATA_DIR}/telebox.db" \
        TELEBOX_ASSETS_PATH="${DATA_DIR}/assets" \
        TELEBOX_PLUGIN_DIR="${DATA_DIR}/plugins" \
        "${PREFIX}/bin/telebox" \
        -config "${CONFIG_DIR}/config.json" \
        -login \
        -login-mode "$LOGIN_MODE" \
        <"$TTY_PATH" >"$TTY_PATH" 2>&1; then
        fail "Telegram 登录失败；配置已保留，请检查提示后重新运行安装命令"
    fi
fi

if [ "$START_SERVICE" -eq 1 ] &&
    [ -s "$SESSION_FILE" ] &&
    command -v systemctl >/dev/null 2>&1; then
    if systemctl --user daemon-reload &&
        systemctl --user enable --now telebox.service; then
        printf 'TeleBox-Go 已安装、登录并启动。\n'
        printf '查看日志：journalctl --user -u telebox -f\n'
    else
        printf 'TeleBox-Go 已安装并完成登录，但当前用户的 systemd 服务未能启动。\n'
        printf '可手动运行：set -a; . %s; set +a; %s/bin/telebox -config %s/config.json\n' \
            "$ENV_FILE" "$PREFIX" "$CONFIG_DIR"
    fi
else
    printf 'TeleBox-Go 已安装到 %s/bin/telebox\n' "$PREFIX"
    if [ "$PERFORM_LOGIN" -eq 0 ]; then
        printf '已跳过登录；稍后可重新运行安装器，或手动执行 telebox -login。\n'
    elif [ "$START_SERVICE" -eq 0 ]; then
        printf 'Telegram 登录已完成，未按 --no-start 要求启动服务。\n'
    fi
    printf '启动服务：systemctl --user enable --now telebox.service\n'
    printf '手动运行：set -a; . %s; set +a; %s/bin/telebox -config %s/config.json\n' \
        "$ENV_FILE" "$PREFIX" "$CONFIG_DIR"
fi
