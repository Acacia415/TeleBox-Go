package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Acacia415/TeleBox-Go/internal/pluginbuilder"
	"github.com/Acacia415/TeleBox-Go/internal/pluginrelease"
	"github.com/Acacia415/TeleBox-Go/internal/pluginspec"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "telebox-plugin-sdk:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}
	switch args[0] {
	case "list", "ls":
		for _, item := range pluginspec.All() {
			fmt.Println(item.Name)
		}
		return nil
	case "build":
		return buildOne(ctx, args[1:])
	case "build-all":
		return buildAll(ctx, args[1:])
	case "release":
		return buildRelease(ctx, args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func buildRelease(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("release", flag.ContinueOnError)
	tag := flags.String("tag", "", "GitHub release tag")
	platformsValue := flags.String(
		"platforms",
		"linux/amd64,linux/arm64",
		"comma-separated os/arch targets",
	)
	output := flags.String("output", "", "release output directory")
	repository := flags.String("repository", "", "repository root")
	repositoryURL := flags.String(
		"repository-url",
		"https://github.com/Acacia415/TeleBox-Go",
		"public repository URL",
	)
	minHost := flags.String("min-host", "0.2.0", "minimum TeleBox-Go version")
	goBinary := flags.String("go", "", "Go toolchain binary")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *tag == "" || *output == "" {
		return fmt.Errorf("-tag and -output are required")
	}
	platforms, err := pluginrelease.ParsePlatforms(*platformsValue)
	if err != nil {
		return err
	}
	result, err := pluginrelease.Generate(ctx, pluginrelease.Options{
		Repository:    *repository,
		OutputDir:     *output,
		Tag:           *tag,
		RepositoryURL: *repositoryURL,
		Platforms:     platforms,
		GoBinary:      *goBinary,
		MinimumHost:   *minHost,
	})
	if err != nil {
		return err
	}
	fmt.Printf(
		"generated %d plugin archives\n%s\n",
		len(result.Artifacts),
		result.CatalogPath,
	)
	return nil
}

func buildOne(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	pluginName := flags.String("plugin", "", "plugin package name")
	goos := flags.String("goos", runtime.GOOS, "target operating system")
	goarch := flags.String("goarch", runtime.GOARCH, "target architecture")
	output := flags.String("output", "", "output directory")
	repository := flags.String("repository", "", "repository root")
	goBinary := flags.String("go", "", "Go toolchain binary")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *pluginName == "" || *output == "" {
		return fmt.Errorf("-plugin and -output are required")
	}
	result, err := pluginbuilder.Build(ctx, pluginbuilder.Options{
		Repository: *repository,
		Plugin:     *pluginName,
		GOOS:       *goos,
		GOARCH:     *goarch,
		OutputDir:  *output,
		GoBinary:   *goBinary,
	})
	if err != nil {
		return err
	}
	fmt.Printf(
		"built %s %s for %s/%s\n%s\n",
		result.Manifest.Name,
		result.Manifest.Version,
		*goos,
		*goarch,
		result.BinaryPath,
	)
	return nil
}

func buildAll(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("build-all", flag.ContinueOnError)
	goos := flags.String("goos", runtime.GOOS, "target operating system")
	goarch := flags.String("goarch", runtime.GOARCH, "target architecture")
	output := flags.String("output", "", "output root directory")
	repository := flags.String("repository", "", "repository root")
	goBinary := flags.String("go", "", "Go toolchain binary")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *output == "" {
		return fmt.Errorf("-output is required")
	}
	for _, item := range pluginspec.All() {
		result, err := pluginbuilder.Build(ctx, pluginbuilder.Options{
			Repository: *repository,
			Plugin:     item.Name,
			GOOS:       *goos,
			GOARCH:     *goarch,
			OutputDir:  filepath.Join(*output, item.Name),
			GoBinary:   *goBinary,
		})
		if err != nil {
			return err
		}
		fmt.Printf("built %s %s\n", result.Manifest.Name, result.Manifest.Version)
	}
	return nil
}

func printUsage() {
	fmt.Println(`TeleBox-Go plugin SDK

Usage:
  telebox-plugin-sdk list
  telebox-plugin-sdk build -plugin <name> -goos linux -goarch amd64 -output <dir>
  telebox-plugin-sdk build-all -goos linux -goarch arm64 -output <dir>
  telebox-plugin-sdk release -tag <tag> -platforms linux/amd64,linux/arm64 -output <dir>`)
}
