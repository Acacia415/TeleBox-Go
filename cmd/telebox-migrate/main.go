package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

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
	fmt.Fprintf(stdout, "converted %d plugins; session format=%s dc=%d; assets=%d files/%d bytes; preserved legacy assets=%d files/%d bytes\n",
		result.Inventory.PluginCount,
		result.Inventory.SessionFormat,
		result.Inventory.SessionDC,
		result.Assets.Files,
		result.Assets.Bytes,
		result.LegacyAssets.Files,
		result.LegacyAssets.Bytes,
	)
	return 0
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "usage: telebox-migrate <inspect|convert> [options]")
}
