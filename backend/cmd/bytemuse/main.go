// Command bytemuse runs the ByteMuse web application and its maintenance commands.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"bytemuse/backend/internal/bootstrap"
	"bytemuse/backend/internal/cli"
	"bytemuse/backend/internal/config"
	"bytemuse/backend/internal/platform/release"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		_, _ = os.Stderr.WriteString("配置错误: " + err.Error() + "\n")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) == 2 && os.Args[1] == "supervise" {
		executable, err := os.Executable()
		if err == nil {
			err = release.Supervise(ctx, "/data/upgrades", executable, cfg.WebStaticDir, cfg.Version, cfg.HTTPAddress, cfg.ShutdownTimeout, os.Stderr)
		}
		if err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	commands := bootstrap.NewCommands(cfg)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, cli.Commands{
		ActorSync: commands.ActorSync,
		Serve:     commands.Serve, MigrationStatus: commands.MigrationStatus, MigrationUp: commands.MigrationUp, Doctor: commands.Doctor, LegacyCatalog: commands.LegacyCatalog, LegacyActors: commands.LegacyActors, LegacyRanks: commands.LegacyRanks, VideoTypes: commands.VideoTypes, TranslationCleanup: commands.TranslationCleanup, TranslationFill: commands.TranslationFill, TagNormalize: commands.TagNormalize,
	})
	os.Exit(code)
}
