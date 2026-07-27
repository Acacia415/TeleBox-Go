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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Acacia415/TeleBox-Go/internal/app"
	"github.com/Acacia415/TeleBox-Go/internal/buildinfo"
	"github.com/Acacia415/TeleBox-Go/internal/config"
	"github.com/Acacia415/TeleBox-Go/internal/corebackup"
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
	if !*loginOnly {
		result, restoreErr := corebackup.ApplyPending(corebackup.Paths{
			Config:  cfg.SourcePath,
			Storage: cfg.Storage.Path,
			Assets:  cfg.Storage.AssetsPath,
			Plugins: cfg.Plugins.Directory,
		})
		if restoreErr != nil {
			fmt.Fprintf(stderr, "pending restore was not applied: %v\n", restoreErr)
		} else if result.Applied {
			fmt.Fprintf(
				stderr,
				"TeleBox-Go backup restored; previous files: %s\n",
				result.RollbackDir,
			)
			if result.Full {
				cfg, err = config.Load(*configPath)
				if err != nil {
					fmt.Fprintf(stderr, "restored configuration error: %v\n", err)
					return 1
				}
			}
		}
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

	logOutput, logFile := openLogOutput(stderr, cfg.Logging.Path)
	if logFile != nil {
		defer logFile.Close()
	}
	logger, logLevel := newLogger(logOutput, cfg.Logging)
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
	var restartRequested atomic.Bool
	requestRestart := func() {
		restartRequested.Store(true)
		stop()
	}
	application, err := app.New(
		ctx,
		cfg,
		logger,
		logLevel,
		client,
		requestRestart,
	)
	if err != nil {
		logger.Error("initialize TeleBox", "error", err)
		return 1
	}
	if err := application.Run(ctx); err != nil {
		logger.Error("TeleBox stopped", "error", err)
		return 1
	}
	if restartRequested.Load() {
		logger.Info("TeleBox restart requested")
		return 75
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

func newLogger(
	output io.Writer,
	cfg config.LoggingConfig,
) (*slog.Logger, *slog.LevelVar) {
	level := new(slog.LevelVar)
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warning", "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	case "silent", "off":
		level.Set(slog.Level(100))
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
	return slog.New(handler), level
}

func openLogOutput(
	stderr io.Writer,
	path string,
) (io.Writer, *os.File) {
	if strings.TrimSpace(path) == "" {
		return stderr, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintf(stderr, "create log directory: %v\n", err)
		return stderr, nil
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 32<<20 {
		rotated := path + ".1"
		_ = os.Remove(rotated)
		if err := os.Rename(path, rotated); err != nil {
			fmt.Fprintf(stderr, "rotate log file: %v\n", err)
		}
	}
	file, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		fmt.Fprintf(stderr, "open log file: %v\n", err)
		return stderr, nil
	}
	return io.MultiWriter(stderr, file), file
}
