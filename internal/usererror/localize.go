package usererror

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	errorCodePattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]+(?:_[A-Z0-9]+)+\b`)
	fileErrorPattern = regexp.MustCompile(
		`(?i)\b(?:open|read|write|mkdir|remove|rename) [^:\r\n]+:`,
	)
	httpStatusPattern = regexp.MustCompile(
		`(?i)\b(?:http (?:status|code)|status code)\D{0,8}([1-5][0-9]{2})\b`,
	)
)

var technicalMarkers = []string{
	"remote_error",
	"rpcdorequest",
	"rpc error",
	"exit status",
	"signal:",
	"context deadline exceeded",
	"context canceled",
	"connection refused",
	"connection reset",
	"network is unreachable",
	"no route to host",
	"dial tcp",
	"dial udp",
	"lookup ",
	"no such file or directory",
	"permission denied",
	"access is denied",
	"i/o timeout",
	"unexpected eof",
	"invalid character",
	"cannot unmarshal",
	"failed to decode",
	"syntax error",
	"x509:",
	"tls:",
	"http status",
	"status code",
	"basic_string",
	"std::",
	"terminate called",
	"panic:",
	"stack trace",
	"database is locked",
	"sqlite_",
	"executable file not found",
	"exec format error",
	"text file busy",
	"read-only file system",
	"not found in %path%",
	"ssh: handshake failed",
	"unable to authenticate",
	"not authorized",
	"not permitted",
	"not found",
	"failed to ",
	"could not ",
	"cannot ",
	"can't ",
	"invalid ",
	"unsupported ",
	"malformed ",
	"bad request",
	"already exists",
	"too large",
	"exceeds ",
	"broken pipe",
	"closed pipe",
	"already closed",
	"not resolved",
	"unavailable",
	"knownhosts:",
	"host key",
	"short write",
	"unexpected end",
	"unexpected status",
	"http:",
	"error:",
	"warning:",
	"http://",
	"https://",
}

// SanitizeText replaces library, RPC, operating-system and external-tool
// details in a user-facing failure with a concise Chinese explanation. It
// returns the original text unchanged when no technical error is detected.
func SanitizeText(text string) (string, bool) {
	if !looksLikeFailure(text) {
		return text, false
	}
	start := technicalStart(text)
	if start < 0 {
		return text, false
	}
	detail := Localize(text[start:])
	if detail == "" {
		return text, false
	}
	prefix := safePrefix(text, start)
	localized := prefix + detail
	if localized == text {
		return text, false
	}
	return localized, true
}

// Localize converts a raw technical error into a Chinese user-facing reason.
// When a precise translation is unsafe, only a stable upstream error code is
// retained; raw paths, URLs, stack traces and library messages are omitted.
func Localize(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "发生未知错误，请查看服务日志"
	}
	if code := extractErrorCode(raw); code != "" {
		return localizeCode(code)
	}

	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "sign in to confirm you’re not a bot"),
		strings.Contains(lower, "sign in to confirm you're not a bot"),
		strings.Contains(lower, "use --cookies"):
		return "上游要求登录验证，请配置有效的 Cookies 后重试"
	case strings.Contains(lower, "no supported javascript runtime"),
		strings.Contains(lower, "javascript runtime could not be found"):
		return "未找到可用的 JavaScript 运行时，请安装或配置 Deno"
	case strings.Contains(lower, "video unavailable"):
		return "目标视频不可用，可能已删除、私有或受地区限制"
	case strings.Contains(lower, "requested format is not available"):
		return "上游没有提供可下载的媒体格式"
	case strings.Contains(lower, "invalid data found when processing input"):
		return "媒体文件无效或格式不受支持"
	case strings.Contains(lower, "context deadline exceeded"),
		strings.Contains(lower, "i/o timeout"),
		strings.Contains(lower, "timed out"),
		strings.Contains(lower, "timeout"):
		return "请求超时，请稍后重试"
	case strings.Contains(lower, "context canceled"),
		strings.Contains(lower, "operation canceled"),
		strings.Contains(lower, "operation cancelled"):
		return "操作已取消"
	case strings.Contains(lower, "too many requests"),
		strings.Contains(lower, "rate limit"),
		strings.Contains(lower, "status 429"),
		strings.Contains(lower, "status code 429"):
		return "请求过于频繁，请稍后重试"
	case strings.Contains(lower, "unauthorized"),
		strings.Contains(lower, "status 401"),
		strings.Contains(lower, "status code 401"),
		strings.Contains(lower, "invalid api key"),
		strings.Contains(lower, "incorrect api key"):
		return "身份验证失败，请检查 API Key 或登录信息"
	case strings.Contains(lower, "forbidden"),
		strings.Contains(lower, "status 403"),
		strings.Contains(lower, "status code 403"):
		return "当前账号没有执行该操作的权限"
	case strings.Contains(lower, "status 404"),
		strings.Contains(lower, "status code 404"):
		return "请求的资源不存在"
	case strings.Contains(lower, "status 5"),
		strings.Contains(lower, "status code 5"):
		return "上游服务暂时不可用，请稍后重试"
	case strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "connection reset"),
		strings.Contains(lower, "network is unreachable"),
		strings.Contains(lower, "no route to host"),
		strings.Contains(lower, "dial tcp"),
		strings.Contains(lower, "dial udp"),
		strings.Contains(lower, "no such host"),
		strings.Contains(lower, "server misbehaving"),
		strings.Contains(lower, "proxyconnect tcp"),
		strings.Contains(lower, "lookup "):
		return "网络连接失败，请检查网络、代理或上游地址"
	case strings.Contains(lower, "broken pipe"),
		strings.Contains(lower, "closed pipe"),
		strings.Contains(lower, "already closed"),
		strings.Contains(lower, "connection closed"):
		return "连接已经中断，请稍后重试"
	case strings.Contains(lower, "x509:"),
		strings.Contains(lower, "tls:"),
		strings.Contains(lower, "certificate signed by unknown authority"):
		return "安全连接验证失败，请检查系统时间、证书或代理设置"
	case strings.Contains(lower, "permission denied"),
		strings.Contains(lower, "access is denied"),
		strings.Contains(lower, "read-only file system"):
		return "系统权限不足，请检查文件权限或服务运行账号"
	case strings.Contains(lower, "no such file or directory"),
		strings.Contains(lower, "file does not exist"):
		return "所需文件不存在或路径无效"
	case strings.Contains(lower, "executable file not found"),
		strings.Contains(lower, "not found in %path%"),
		strings.Contains(lower, "not found in path"):
		return "未找到所需的外部程序，请先完成依赖安装"
	case strings.Contains(lower, "exec format error"):
		return "外部程序与当前系统架构不兼容，请安装对应架构的版本"
	case strings.Contains(lower, "text file busy"):
		return "外部程序正在使用中，请稍后重试或重启服务"
	case strings.Contains(lower, "not found"):
		return "未找到目标或所需资源"
	case strings.Contains(lower, "not resolved"):
		return "无法解析目标用户或会话"
	case strings.Contains(lower, "unavailable"):
		return "相关服务暂时不可用，请稍后重试"
	case strings.Contains(lower, "no space left on device"),
		strings.Contains(lower, "disk full"):
		return "磁盘空间不足"
	case strings.Contains(lower, "database is locked"),
		strings.Contains(lower, "sqlite_busy"):
		return "数据库正忙，请稍后重试"
	case strings.Contains(lower, "invalid character"),
		strings.Contains(lower, "cannot unmarshal"),
		strings.Contains(lower, "failed to decode"),
		strings.Contains(lower, "syntax error"),
		strings.Contains(lower, "malformed"),
		strings.Contains(lower, "bad request"):
		return "返回数据格式无效，请稍后重试"
	case strings.Contains(lower, "invalid "):
		return "输入参数或目标数据无效"
	case strings.Contains(lower, "unsupported "):
		return "当前环境或目标不支持该操作"
	case strings.Contains(lower, "already exists"):
		return "目标已经存在"
	case strings.Contains(lower, "too large"),
		strings.Contains(lower, "exceeds "):
		return "数据超出允许的大小或数量限制"
	case strings.Contains(lower, "unexpected eof"),
		strings.Contains(lower, "unexpected end"),
		strings.TrimSpace(lower) == "eof":
		return "连接提前中断，返回数据不完整"
	case strings.Contains(lower, "short write"):
		return "文件写入不完整，请检查磁盘空间和文件权限"
	case strings.Contains(lower, "ssh: handshake failed"),
		strings.Contains(lower, "unable to authenticate"),
		strings.Contains(lower, "no supported methods remain"):
		return "SSH 验证失败，请检查账号、密码或密钥"
	case strings.Contains(lower, "knownhosts:"),
		strings.Contains(lower, "host key"):
		return "SSH 主机密钥验证失败，请确认服务器身份和密钥记录"
	case strings.Contains(lower, "exit status"),
		strings.Contains(lower, "signal:"),
		strings.Contains(lower, "terminate called"),
		strings.Contains(lower, "basic_string"),
		strings.Contains(lower, "std::"):
		return "外部程序执行失败，请检查依赖和服务日志"
	case strings.Contains(lower, "not authorized"),
		strings.Contains(lower, "not permitted"):
		return "当前账号没有执行该操作的权限"
	case strings.Contains(lower, "failed to "),
		strings.Contains(lower, "could not "),
		strings.Contains(lower, "cannot "),
		strings.Contains(lower, "can't "):
		return "操作未完成，请查看服务日志"
	}
	if match := httpStatusPattern.FindStringSubmatch(raw); len(match) == 2 {
		status, _ := strconv.Atoi(match[1])
		switch {
		case status == 401:
			return "身份验证失败，请检查 API Key 或登录信息"
		case status == 403:
			return "当前账号没有执行该操作的权限"
		case status == 404:
			return "请求的资源不存在"
		case status == 429:
			return "请求过于频繁，请稍后重试"
		case status >= 500:
			return "上游服务暂时不可用，请稍后重试"
		default:
			return fmt.Sprintf("上游请求失败（HTTP %d）", status)
		}
	}
	return "操作未完成，请查看服务日志"
}

func looksLikeFailure(text string) bool {
	if strings.Contains(text, "❌") {
		return true
	}
	// Status and error messages put their outcome in the leading block. Help,
	// tutorials and settings may legitimately mention words such as "无法" or
	// "失败" later in the body and must not be rewritten as runtime errors.
	header := text
	if index := strings.Index(header, "\n\n"); index >= 0 {
		header = header[:index]
	}
	return strings.Contains(header, "失败") ||
		strings.Contains(header, "错误") ||
		strings.Contains(header, "无法")
}

func technicalStart(text string) int {
	lower := strings.ToLower(text)
	start := -1
	for _, marker := range technicalMarkers {
		if index := strings.Index(lower, marker); index >= 0 &&
			(start < 0 || index < start) {
			start = index
		}
	}
	if match := errorCodePattern.FindStringIndex(text); match != nil &&
		(start < 0 || match[0] < start) {
		start = match[0]
	}
	if match := fileErrorPattern.FindStringIndex(text); match != nil &&
		(start < 0 || match[0] < start) {
		start = match[0]
	}
	if start < 0 {
		start = englishDetailStart(text)
	}
	return start
}

func englishDetailStart(text string) int {
	start := -1
	if end := failureDelimiterEnd(text, len(text)); end >= 0 {
		start = end
	} else if index := strings.LastIndex(text, "\n\n"); index >= 0 {
		start = index + 2
	} else if index := strings.Index(text, "❌"); index >= 0 {
		start = index + len("❌")
	}
	if start < 0 || start >= len(text) {
		return -1
	}
	for start < len(text) {
		switch text[start] {
		case ' ', '\t', '\r', '\n':
			start++
		default:
			goto inspected
		}
	}
inspected:
	detail := text[start:]
	if containsHan(detail) {
		return -1
	}
	letters := 0
	for _, char := range detail {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' {
			letters++
		}
	}
	if letters >= 4 || strings.EqualFold(strings.TrimSpace(detail), "EOF") {
		return start
	}
	return -1
}

func containsHan(text string) bool {
	for _, char := range text {
		if char >= '\u3400' && char <= '\u9fff' {
			return true
		}
	}
	return false
}

func safePrefix(text string, technicalStart int) string {
	before := text[:technicalStart]
	if end := failureDelimiterEnd(before, len(before)); end >= 0 {
		return text[:end]
	}
	if index := strings.LastIndex(before, "\n\n"); index >= 0 {
		return text[:index+2]
	}
	if strings.Contains(before, "❌") {
		return strings.TrimRight(before, " \t\r\n:：-") + " "
	}
	return ""
}

func failureDelimiterEnd(text string, limit int) int {
	if limit > len(text) {
		limit = len(text)
	}
	before := text[:limit]
	keywordEnd := -1
	for _, keyword := range []string{"失败", "错误", "无法"} {
		if index := strings.LastIndex(before, keyword); index >= 0 {
			end := index + len(keyword)
			if end > keywordEnd {
				keywordEnd = end
			}
		}
	}
	if keywordEnd < 0 {
		return -1
	}
	tail := before[keywordEnd:]
	chinese := strings.Index(tail, "：")
	ascii := strings.Index(tail, ":")
	delimiter := -1
	switch {
	case chinese >= 0 && (ascii < 0 || chinese < ascii):
		delimiter = chinese
	case ascii >= 0:
		delimiter = ascii
	default:
		return -1
	}
	// The delimiter must immediately follow the failure keyword. Otherwise a
	// normal setting label such as "验证失败处理：none" would be mistaken
	// for an error whose technical detail happens to be the English word none.
	if strings.TrimSpace(tail[:delimiter]) != "" {
		return -1
	}
	if delimiter == chinese {
		return keywordEnd + delimiter + len("：")
	}
	return keywordEnd + delimiter + 1
}

func extractErrorCode(raw string) string {
	for _, code := range errorCodePattern.FindAllString(raw, -1) {
		switch code {
		case "TELEBOX_API_ID", "TELEBOX_API_HASH", "HTTP_PROXY", "HTTPS_PROXY":
			continue
		default:
			return code
		}
	}
	return ""
}

func localizeCode(code string) string {
	switch {
	case strings.HasPrefix(code, "FLOOD_WAIT_"):
		wait := strings.TrimPrefix(code, "FLOOD_WAIT_")
		if seconds, err := strconv.Atoi(wait); err == nil && seconds > 0 {
			return fmt.Sprintf("操作过于频繁，请等待 %d 秒后重试", seconds)
		}
		return "操作过于频繁，请稍后重试"
	case strings.HasPrefix(code, "SLOWMODE_WAIT_"):
		wait := strings.TrimPrefix(code, "SLOWMODE_WAIT_")
		if seconds, err := strconv.Atoi(wait); err == nil && seconds > 0 {
			return fmt.Sprintf("当前会话已开启慢速模式，请等待 %d 秒后重试", seconds)
		}
		return "当前会话已开启慢速模式，请稍后重试"
	}
	switch code {
	case "FLOOD_WAIT":
		return "操作过于频繁，请稍后重试"
	case "PEER_FLOOD":
		return "Telegram 限制了当前账号的操作频率，请稍后重试"
	case "USER_ADMIN_INVALID":
		return "无法执行管理员操作。若目标是管理员，请先取消其管理员权限；群主无法被踢出"
	case "CHAT_ADMIN_REQUIRED":
		return "当前账号不是管理员，或缺少执行该操作所需的管理权限"
	case "RIGHT_FORBIDDEN":
		return "当前账号的管理员权限不足"
	case "USER_CREATOR":
		return "群主无法被执行此操作"
	case "USER_ID_INVALID", "PARTICIPANT_ID_INVALID", "PEER_ID_INVALID":
		return "目标用户无效或不在当前会话中"
	case "CHAT_ID_INVALID", "CHANNEL_INVALID":
		return "目标群组或频道无效"
	case "USER_NOT_PARTICIPANT":
		return "目标用户不是当前群组或频道的成员"
	case "USER_PRIVACY_RESTRICTED":
		return "目标用户的隐私设置不允许执行该操作"
	case "CHANNEL_PRIVATE":
		return "当前账号无法访问该群组或频道"
	case "CHAT_WRITE_FORBIDDEN":
		return "当前账号无法在该会话发送消息"
	case "CHAT_SEND_MEDIA_FORBIDDEN", "CHAT_SEND_PHOTOS_FORBIDDEN",
		"CHAT_SEND_VIDEOS_FORBIDDEN", "CHAT_SEND_STICKERS_FORBIDDEN",
		"CHAT_SEND_GIFS_FORBIDDEN":
		return "当前账号没有在该会话发送此类媒体的权限"
	case "USER_BANNED_IN_CHANNEL":
		return "当前账号已被限制，无法在该会话执行操作"
	case "MESSAGE_ID_INVALID", "MSG_ID_INVALID":
		return "目标消息不存在或已经失效"
	case "MESSAGE_NOT_MODIFIED":
		return "消息内容没有变化"
	case "MESSAGE_DELETE_FORBIDDEN", "MESSAGE_AUTHOR_REQUIRED":
		return "当前账号没有删除该消息的权限"
	case "MESSAGE_EDIT_TIME_EXPIRED":
		return "消息已超过允许编辑的时间"
	case "MESSAGE_EMPTY":
		return "消息内容不能为空"
	case "MESSAGE_TOO_LONG":
		return "消息内容超过 Telegram 允许的长度"
	case "ENTITY_BOUNDS_INVALID":
		return "消息排版格式无效，请减少特殊格式后重试"
	case "MEDIA_EMPTY", "MEDIA_INVALID", "PHOTO_EXT_INVALID",
		"FILE_PARTS_INVALID", "STICKER_FILE_INVALID":
		return "媒体文件无效或格式不受支持"
	case "FILE_REFERENCE_EXPIRED":
		return "文件引用已过期，请重新发送文件后重试"
	case "AUTH_KEY_UNREGISTERED", "AUTH_KEY_DUPLICATED", "SESSION_REVOKED":
		return "Telegram 登录会话已失效，请重新登录"
	case "API_ID_INVALID":
		return "Telegram API ID 或 API Hash 无效"
	case "PHONE_NUMBER_INVALID":
		return "手机号码格式无效"
	case "PHONE_CODE_INVALID":
		return "Telegram 登录验证码无效"
	case "PHONE_CODE_EXPIRED":
		return "Telegram 登录验证码已过期，请重新获取"
	case "PASSWORD_HASH_INVALID":
		return "两步验证密码错误"
	case "STICKERSET_INVALID":
		return "贴纸包不存在或当前账号无法访问"
	case "STICKER_EMOJI_INVALID":
		return "贴纸对应的 Emoji 无效"
	case "STICKERS_TOO_MUCH":
		return "该贴纸包已经达到贴纸数量上限"
	case "USER_ALREADY_PARTICIPANT":
		return "当前账号已经加入该群组或频道"
	case "INVITE_HASH_INVALID":
		return "邀请链接无效"
	case "INVITE_HASH_EXPIRED":
		return "邀请链接已经过期"
	case "CHANNELS_TOO_MUCH":
		return "当前账号加入的群组或频道数量已达上限"
	case "USERNAME_INVALID":
		return "用户名格式无效"
	case "USERNAME_NOT_OCCUPIED":
		return "未找到该用户名"
	default:
		return "操作未完成（错误代码：" + code + "）"
	}
}
