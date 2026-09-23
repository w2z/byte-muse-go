// Package cli parses the intentionally small ByteMuse operational command surface.
package cli

import (
	"context"
	"fmt"
	"io"
)

// Commands contains side-effecting operations supplied by the application composition root.
type Commands struct {
	Serve           func(context.Context) error
	MigrationStatus func(context.Context) (string, error)
	MigrationUp     func(context.Context) error
	Doctor          func(context.Context) error
	LegacyActors    func(context.Context, string) error
}

// Run validates args, invokes one command, writes operator-facing output, and returns a process exit code.
func Run(ctx context.Context, args []string, output io.Writer, commands Commands) int {
	usage := func() int {
		fmt.Fprintln(output, "用法: bytemuse serve | migrate status | migrate up | migrate legacy-actors <旧版数据库路径> | doctor")
		return 2
	}
	var err error
	switch {
	case len(args) == 1 && args[0] == "serve" && commands.Serve != nil:
		err = commands.Serve(ctx)
	case len(args) == 2 && args[0] == "migrate" && args[1] == "status" && commands.MigrationStatus != nil:
		var status string
		status, err = commands.MigrationStatus(ctx)
		if err == nil {
			fmt.Fprintln(output, status)
		}
	case len(args) == 2 && args[0] == "migrate" && args[1] == "up" && commands.MigrationUp != nil:
		err = commands.MigrationUp(ctx)
	case len(args) == 3 && args[0] == "migrate" && args[1] == "legacy-actors" && commands.LegacyActors != nil:
		err = commands.LegacyActors(ctx, args[2])
	case len(args) == 1 && args[0] == "doctor" && commands.Doctor != nil:
		err = commands.Doctor(ctx)
	default:
		return usage()
	}
	if err != nil {
		fmt.Fprintf(output, "错误: %v\n", err)
		return 1
	}
	return 0
}
