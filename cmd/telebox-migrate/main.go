package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Acacia415/TeleBox-Go/internal/migration"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "inspect":
		return inspect(args[1:], stdout, stderr)
	case "convert":
		return convert(args[1:], stdout, stderr)
	case "import":
		return importConverted(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func inspect(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	archivePath := flags.String("archive", "", "path to TeleBox .tar.gz backup")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *archivePath == "" {
		fmt.Fprintln(stderr, "-archive is required")
		return 2
	}

	inventory, err := migration.InspectBackup(*archivePath)
	if err != nil {
		fmt.Fprintf(stderr, "inspect backup: %v\n", err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(inventory); err != nil {
		fmt.Fprintf(stderr, "encode inventory: %v\n", err)
		return 1
	}
	return 0
}

func convert(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("convert", flag.ContinueOnError)
	flags.SetOutput(stderr)
	archivePath := flags.String("archive", "", "path to TeleBox .tar.gz backup")
	configPath := flags.String("config", "config.json", "new TeleBox-Go config path")
	sessionPath := flags.String("session", "data/session.json", "new gotd session path")
	assetsPath := flags.String("assets", "data/assets", "new plugin asset directory")
	legacyAssetsPath := flags.String(
		"legacy-assets",
		"data/legacy-assets",
		"preserved legacy plugin asset directory",
	)
	apply := flags.Bool("apply", false, "write converted config and session")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *archivePath == "" {
		fmt.Fprintln(stderr, "-archive is required")
		return 2
	}
	if !*apply {
		fmt.Fprintln(stdout, "dry run: add -apply to write converted config and session")
		return inspect([]string{"-archive", *archivePath}, stdout, stderr)
	}

	result, err := migration.ConvertBackupWithOptions(
		context.Background(),
		*archivePath,
		migration.ConvertOptions{
			ConfigPath:       *configPath,
			SessionPath:      *sessionPath,
			AssetsPath:       *assetsPath,
			LegacyAssetsPath: *legacyAssetsPath,
		},
	)
	if err != nil {
		fmt.Fprintf(stderr, "convert backup: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "converted %d plugins; session format=%s dc=%d; assets=%d files/%d bytes; quarantined=%d files/%d bytes; preserved legacy assets=%d files/%d bytes\n",
		result.Inventory.PluginCount,
		result.Inventory.SessionFormat,
		result.Inventory.SessionDC,
		result.Assets.Files,
		result.Assets.Bytes,
		result.Assets.QuarantinedFiles,
		result.Assets.QuarantinedBytes,
		result.LegacyAssets.Files,
		result.LegacyAssets.Bytes,
	)
	return 0
}

func importConverted(args []string, stdout, stderr io.Writer) int {
	configPath, sessionPath, assetsPath, legacyAssetsPath := importDefaults()
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourceRoot := flags.String(
		"source",
		"",
		"directory created by telebox-migrate convert",
	)
	flags.StringVar(&configPath, "config", configPath, "installed TeleBox-Go config path")
	flags.StringVar(&sessionPath, "session", sessionPath, "installed gotd session path")
	flags.StringVar(&assetsPath, "assets", assetsPath, "installed plugin asset directory")
	flags.StringVar(
		&legacyAssetsPath,
		"legacy-assets",
		legacyAssetsPath,
		"installed preserved legacy plugin asset directory",
	)
	skipSession := flags.Bool(
		"skip-session",
		false,
		"keep the current login and do not copy the converted session",
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *sourceRoot == "" {
		fmt.Fprintln(stderr, "-source is required")
		return 2
	}
	result, err := migration.ImportConverted(migration.ImportOptions{
		SourceRoot:       *sourceRoot,
		ConfigPath:       configPath,
		SessionPath:      sessionPath,
		AssetsPath:       assetsPath,
		LegacyAssetsPath: legacyAssetsPath,
		SkipSession:      *skipSession,
	})
	if err != nil {
		fmt.Fprintf(stderr, "import converted data: %v\n", err)
		return 1
	}
	fmt.Fprintf(
		stdout,
		"迁移数据已导入；配置=%s；会话=%s；活动资产=%s；保留资产=%s\n",
		formatImportStats(result.Config),
		formatImportStats(result.Session),
		formatImportStats(result.Assets),
		formatImportStats(result.LegacyAssets),
	)
	return 0
}

func importDefaults() (configPath, sessionPath, assetsPath, legacyAssetsPath string) {
	home, _ := os.UserHomeDir()
	configRoot := os.Getenv("XDG_CONFIG_HOME")
	if configRoot == "" {
		configRoot = filepath.Join(home, ".config")
	}
	dataRoot := os.Getenv("XDG_DATA_HOME")
	if dataRoot == "" {
		dataRoot = filepath.Join(home, ".local", "share")
	}
	configPath = filepath.Join(configRoot, "telebox", "config.json")
	dataRoot = filepath.Join(dataRoot, "telebox")
	sessionPath = filepath.Join(dataRoot, "session.json")
	assetsPath = filepath.Join(dataRoot, "assets")
	legacyAssetsPath = filepath.Join(dataRoot, "legacy-assets")
	return
}

func formatImportStats(stats migration.ImportStats) string {
	text := fmt.Sprintf(
		"%d 个复制/%d 个已存在",
		stats.CopiedFiles,
		stats.SkippedFiles,
	)
	if stats.QuarantinedFiles > 0 {
		text += fmt.Sprintf("/%d 个可执行文件已隔离", stats.QuarantinedFiles)
	}
	return text
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "usage: telebox-migrate <inspect|convert|import> [options]")
}
