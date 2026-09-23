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
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		_, _ = os.Stderr.WriteString("配置错误: " + err.Error() + "\n")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	commands := bootstrap.NewCommands(cfg)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, cli.Commands{
		Serve: commands.Serve, MigrationStatus: commands.MigrationStatus, MigrationUp: commands.MigrationUp, Doctor: commands.Doctor, LegacyActors: commands.LegacyActors,
	})
	os.Exit(code)
}
