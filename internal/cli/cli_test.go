package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunHelpListsCommands(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, stdout, _ := run(t, arg)
		if code != ExitOK {
			t.Fatalf("%s: exit code = %d, want %d", arg, code, ExitOK)
		}
		for _, c := range commands {
			if !strings.Contains(stdout, c.name) {
				t.Errorf("%s: usage does not mention %q:\n%s", arg, c.name, stdout)
			}
		}
	}
}

func TestRunWithoutArgsIsUsageError(t *testing.T) {
	code, _, stderr := run(t)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "Commands:") {
		t.Errorf("stderr missing usage:\n%s", stderr)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	code, _, stderr := run(t, "bogus")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, `unknown command "bogus"`) {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunVersion(t *testing.T) {
	code, stdout, _ := run(t, "version")
	if code != ExitOK || stdout != "icb dev\n" {
		t.Fatalf("got code %d, stdout %q", code, stdout)
	}
}

func TestSubcommandHelp(t *testing.T) {
	for _, c := range commands {
		code, _, stderr := run(t, c.name, "-h")
		if code != ExitOK {
			t.Errorf("%s -h: exit code = %d, want %d", c.name, code, ExitOK)
		}
		if !strings.Contains(stderr, c.usage) {
			t.Errorf("%s -h: usage missing %q:\n%s", c.name, c.usage, stderr)
		}
	}
}

func TestSubcommandArgumentValidation(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"analyze missing dir", []string{"analyze"}, ExitUsage, "expected 1 argument(s), got 0"},
		{"analyze unknown flag", []string{"analyze", "-nope", dir}, ExitUsage, "flag provided but not defined"},
		{"analyze nonexistent dir", []string{"analyze", dir + "/missing"}, ExitError, "no such file or directory"},
		{"analyze stub", []string{"analyze", dir}, ExitError, "not implemented yet"},
		{"serve stub", []string{"serve", "-addr", ":0", "-watch", dir}, ExitError, "not implemented yet"},
		{"mcp stub", []string{"mcp", dir}, ExitError, "not implemented yet"},
		{"query missing query", []string{"query", dir}, ExitUsage, "expected 2 argument(s), got 1"},
		{"query stub", []string{"query", "-json", dir, "routes"}, ExitError, "not implemented yet"},
		{"version extra arg", []string{"version", "x"}, ExitUsage, "expected 0 argument(s), got 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := run(t, tt.args...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.code, stderr)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr missing %q:\n%s", tt.want, stderr)
			}
		})
	}
}
