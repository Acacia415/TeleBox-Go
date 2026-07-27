package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"golang.org/x/term"
)

type terminalAuthenticator struct {
	reader *bufio.Reader
	input  *os.File
	output io.Writer
}

func newTerminalAuthenticator(input io.Reader, output io.Writer) *terminalAuthenticator {
	a := &terminalAuthenticator{
		reader: bufio.NewReader(input),
		output: output,
	}
	if inputFile, ok := input.(*os.File); ok {
		a.input = inputFile
	}
	return a
}

func (a *terminalAuthenticator) Phone(ctx context.Context) (string, error) {
	for {
		value, err := a.readLine(ctx, "请输入 Telegram 手机号（含国际区号，例如 +8613812345678）：")
		if err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
		if validPhoneNumber(value) {
			return value, nil
		}
		fmt.Fprintln(a.output, "手机号格式不正确，请输入 +、国家/地区代码和手机号码。")
	}
}

func (a *terminalAuthenticator) Code(ctx context.Context, _ *tg.AuthSentCode) (string, error) {
	fmt.Fprintln(a.output, "验证码已发送，请查看 Telegram 或短信。")
	for {
		value, err := a.readLine(ctx, "请输入登录验证码：")
		if err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value != "" && onlyDigits(value) {
			return value, nil
		}
		fmt.Fprintln(a.output, "验证码应为数字，请重新输入。")
	}
}

func (a *terminalAuthenticator) Password(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	const prompt = "账号启用了二步验证，请输入密码："
	if a.input != nil && term.IsTerminal(int(a.input.Fd())) {
		fmt.Fprint(a.output, prompt)
		password, err := term.ReadPassword(int(a.input.Fd()))
		fmt.Fprintln(a.output)
		if err != nil {
			return "", fmt.Errorf("读取二步验证密码：%w", err)
		}
		if len(password) == 0 {
			return "", errors.New("二步验证密码不能为空")
		}
		return string(password), nil
	}

	password, err := a.readLine(ctx, prompt)
	if err != nil {
		return "", err
	}
	if password == "" {
		return "", errors.New("二步验证密码不能为空")
	}
	return password, nil
}

func (a *terminalAuthenticator) AcceptTermsOfService(
	context.Context,
	tg.HelpTermsOfService,
) error {
	return errors.New("该手机号尚未注册；请先使用 Telegram 官方客户端创建账号")
}

func (a *terminalAuthenticator) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("TeleBox-Go 不支持在安装过程中注册 Telegram 账号")
}

func (a *terminalAuthenticator) readLine(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fmt.Fprint(a.output, prompt)
	value, err := a.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("读取终端输入：%w", err)
	}
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if errors.Is(err, io.EOF) && value == "" {
		return "", io.EOF
	}
	return value, nil
}

func validPhoneNumber(value string) bool {
	if len(value) < 8 || len(value) > 17 || value[0] != '+' {
		return false
	}
	return onlyDigits(value[1:])
}

func onlyDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !unicode.IsDigit(r) || r > unicode.MaxASCII {
			return false
		}
	}
	return true
}
