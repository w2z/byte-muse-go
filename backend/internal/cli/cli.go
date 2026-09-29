// Package cli parses the intentionally small ByteMuse operational command surface.
package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
)

// Commands contains side-effecting operations supplied by the application composition root.
type Commands struct {
	Serve              func(context.Context) error
	MigrationStatus    func(context.Context) (string, error)
	MigrationUp        func(context.Context) error
	Doctor             func(context.Context) error
	LegacyCatalog      func(context.Context, string) error
	LegacyActors       func(context.Context, string) error
	LegacyRanks        func(context.Context, string) error
	VideoTypes         func(context.Context) error
	TranslationCleanup func(context.Context) error
	TranslationFill    func(context.Context, int) error
	TagNormalize       func(context.Context) error
}

// Run validates args, invokes one command, writes operator-facing output, and returns a process exit code.
func Run(ctx context.Context, args []string, output io.Writer, commands Commands) int {
	usage := func() int {
		fmt.Fprintln(output, "用法: bytemuse serve | migrate status | migrate up | migrate legacy-catalog <旧版数据库路径> | migrate legacy-actors <旧版数据库路径> | migrate legacy-ranks <旧版数据库路径> | migrate video-types | migrate translation-cleanup | migrate translation-fill <数量> | migrate tag-normalize | doctor")
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
	case len(args) == 3 && args[0] == "migrate" && args[1] == "legacy-catalog" && commands.LegacyCatalog != nil:
		err = commands.LegacyCatalog(ctx, args[2])
	case len(args) == 3 && args[0] == "migrate" && args[1] == "legacy-actors" && commands.LegacyActors != nil:
		err = commands.LegacyActors(ctx, args[2])
	case len(args) == 3 && args[0] == "migrate" && args[1] == "legacy-ranks" && commands.LegacyRanks != nil:
		err = commands.LegacyRanks(ctx, args[2])
	case len(args) == 2 && args[0] == "migrate" && args[1] == "video-types" && commands.VideoTypes != nil:
		err = commands.VideoTypes(ctx)
	case len(args) == 2 && args[0] == "migrate" && args[1] == "translation-cleanup" && commands.TranslationCleanup != nil:
		err = commands.TranslationCleanup(ctx)
	case len(args) == 3 && args[0] == "migrate" && args[1] == "translation-fill" && commands.TranslationFill != nil:
		limit, parseErr := strconv.Atoi(args[2])
		if parseErr != nil || limit < 0 {
			fmt.Fprintln(output, "错误: 数量必须是非负整数")
			return 2
		}
		err = commands.TranslationFill(ctx, limit)
	case len(args) == 2 && args[0] == "migrate" && args[1] == "tag-normalize" && commands.TagNormalize != nil:
		err = commands.TagNormalize(ctx)
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
