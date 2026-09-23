package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"bytemuse/backend/internal/cli"
)

func TestRunDispatchesSupportedCommands(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"serve"}, want: "serve"},
		{args: []string{"migrate", "status"}, want: "status"},
		{args: []string{"migrate", "up"}, want: "up"},
		{args: []string{"doctor"}, want: "doctor"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			called := ""
			commands := cli.Commands{
				Serve: func(context.Context) error { called = "serve"; return nil },
				MigrationStatus: func(context.Context) (string, error) { called = "status"; return "current", nil },
				MigrationUp: func(context.Context) error { called = "up"; return nil },
				Doctor: func(context.Context) error { called = "doctor"; return nil },
			}
			var output bytes.Buffer
			if code := cli.Run(context.Background(), tc.args, &output, commands); code != 0 {
				t.Fatalf("exit code = %d, want 0; output=%q", code, output.String())
			}
			if called != tc.want {
				t.Fatalf("called = %q, want %q", called, tc.want)
			}
		})
	}
}

func TestRunRejectsUnsupportedWorkerAndLegacyImport(t *testing.T) {
	for _, args := range [][]string{{"worker"}, {"import-legacy"}} {
		var output bytes.Buffer
		code := cli.Run(context.Background(), args, &output, cli.Commands{})
		if code != 2 || !strings.Contains(output.String(), "用法") {
			t.Fatalf("args=%v code/output = %d/%q, want usage error", args, code, output.String())
		}
	}
}

func TestRunReportsCommandFailure(t *testing.T) {
	want := errors.New("database unavailable")
	var output bytes.Buffer
	code := cli.Run(context.Background(), []string{"doctor"}, &output, cli.Commands{Doctor: func(context.Context) error { return want }})
	if code != 1 || !strings.Contains(output.String(), want.Error()) {
		t.Fatalf("code/output = %d/%q, want command failure", code, output.String())
	}
}
