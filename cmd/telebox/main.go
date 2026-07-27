package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/app"
	"github.com/Acacia415/TeleBox-Go/internal/buildinfo"
	"github.com/Acacia415/TeleBox-Go/internal/config"
	gotdclient "github.com/Acacia415/TeleBox-Go/internal/telegram/gotd"
	"golang.org/x/term"
	"rsc.io/qr"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("telebox", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config.json", "path to TeleBox JSON config")
	checkConfig := flags.Bool("check-config", false, "validate configuration and exit")
	showVersion := flags.Bool("version", false, "print version and exit")
	loginOnly := flags.Bool("login", false, "log in to Telegram and exit")
	loginMode := flags.String("login-mode", "", "login method used with -login: qr or phone")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintf(
			stdout,
			"telebox %s commit=%s built=%s\n",
			buildinfo.Version,
			buildinfo.Commit,
			buildinfo.Date,
		)
		return 0
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "configuration error: %v\n", err)
		return 1
	}
	if *checkConfig {
		fmt.Fprintln(stdout, "configuration is valid")
		return 0
	}
	if value := strings.ToLower(strings.TrimSpace(*loginMode)); value != "" {
		switch value {
		case "qr", "phone":
			cfg.Telegram.LoginMode = value
		default:
			fmt.Fprintln(stderr, "login mode must be qr or phone")
			return 2
		}
	}

	logger := newLogger(stderr, cfg.Logging)
	var phoneAuth *terminalAuthenticator
	if strings.EqualFold(cfg.Telegram.LoginMode, "phone") {
		phoneAuth = newTerminalAuthenticator(os.Stdin, stderr)
	}
	client, err := gotdclient.New(gotdclient.Config{
		APIID:       cfg.Telegram.APIID,
		APIHash:     cfg.Telegram.APIHash,
		SessionFile: cfg.Telegram.SessionFile,
		LoginMode:   cfg.Telegram.LoginMode,
		OnQRCode: func(_ context.Context, code gotdclient.QRCode) error {
			return showLoginQRCode(stderr, cfg.Telegram.SessionFile, code)
		},
		PhoneAuth: phoneAuth,
	})
	if err != nil {
		logger.Error("initialize Telegram client", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *loginOnly {
		if err := client.Login(ctx); err != nil {
			logger.Error("Telegram login failed", "error", err)
			return 1
		}
		fmt.Fprintln(stdout, "Telegram 登录成功，会话已保存。")
		return 0
	}
	application, err := app.New(ctx, cfg, logger, client)
	if err != nil {
		logger.Error("initialize TeleBox", "error", err)
		return 1
	}
	if err := application.Run(ctx); err != nil {
		logger.Error("TeleBox stopped", "error", err)
		return 1
	}
	return 0
}

func showLoginQRCode(stderr io.Writer, sessionFile string, code gotdclient.QRCode) error {
	loginQR, err := qr.Encode(code.URL, qr.M)
	if err != nil {
		return fmt.Errorf("encode Telegram login QR: %w", err)
	}

	qrPath := filepath.Join(filepath.Dir(sessionFile), "login-qr.png")
	if err := os.MkdirAll(filepath.Dir(qrPath), 0o700); err != nil {
		return fmt.Errorf("create Telegram login QR directory: %w", err)
	}
	if err := os.WriteFile(qrPath, loginQR.PNG(), 0o600); err != nil {
		return fmt.Errorf("write Telegram login QR: %w", err)
	}

	absolutePath, err := filepath.Abs(qrPath)
	if err != nil {
		absolutePath = qrPath
	}
	fmt.Fprintf(stderr, "请在 %s 前扫描 Telegram 登录二维码。\n二维码图片：%s\n",
		code.ExpiresAt.Local().Format(time.RFC3339),
		absolutePath,
	)
	if output, ok := stderr.(*os.File); ok && term.IsTerminal(int(output.Fd())) {
		fmt.Fprintln(stderr, "请使用 Telegram 扫描下方二维码：")
		renderTerminalQRCode(stderr, loginQR)
	}
	if runtime.GOOS == "windows" {
		if err := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", absolutePath).Start(); err != nil {
			fmt.Fprintf(stderr, "无法自动打开二维码图片：%v\n", err)
		}
	}
	return nil
}

func renderTerminalQRCode(output io.Writer, code *qr.Code) {
	const quietZone = 4
	size := code.Size
	for y := -quietZone; y < size+quietZone; y += 2 {
		fmt.Fprint(output, "\x1b[30;47m")
		for x := -quietZone; x < size+quietZone; x++ {
			top := x >= 0 && x < size && y >= 0 && y < size && code.Black(x, y)
			bottomY := y + 1
			bottom := x >= 0 && x < size &&
				bottomY >= 0 && bottomY < size &&
				code.Black(x, bottomY)
			switch {
			case top && bottom:
				fmt.Fprint(output, "█")
			case top:
				fmt.Fprint(output, "▀")
			case bottom:
				fmt.Fprint(output, "▄")
			default:
				fmt.Fprint(output, " ")
			}
		}
		fmt.Fprintln(output, "\x1b[0m")
	}
}

func newLogger(output io.Writer, cfg config.LoggingConfig) *slog.Logger {
	level := new(slog.LevelVar)
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}

	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.EqualFold(cfg.Format, "json") {
		handler = slog.NewJSONHandler(output, options)
	} else {
		handler = slog.NewTextHandler(output, options)
	}
	return slog.New(handler)
}
