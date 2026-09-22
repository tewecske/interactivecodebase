package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
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
		{"serve stub", []string{"serve", "-addr", ":0", "-watch", dir}, ExitError, "not implemented yet"},
		{"mcp stub", []string{"mcp", dir}, ExitError, "not implemented yet"},
		{"query missing command", []string{"query", dir}, ExitUsage, "expected at least 2 argument(s), got 1"},
		{"query unknown command", []string{"query", dir, "bogus"}, ExitUsage, `unknown query command "bogus"`},
		{"query wrong arity", []string{"query", dir, "paths", "a"}, ExitUsage, "wrong number of arguments for paths"},
		{"analyze not a module", []string{"analyze", dir}, ExitError, "icb analyze:"},
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

// sharedWebapp analyzes the webapp fixture once for the whole package: a
// full analysis costs over a gigabyte under -race.
var sharedWebapp = sync.OnceValues(func() (*analysis.Result, error) {
	return analysis.Analyze(context.Background(), fixture.WebappDir(), analysis.Options{})
})

func TestMain(m *testing.M) {
	analyze := openAnalysis
	openAnalysis = func(ctx context.Context, dir string) (*analysis.Result, func() error, error) {
		if dir != fixture.WebappDir() {
			return analyze(ctx, dir)
		}
		r, err := sharedWebapp()
		return r, func() error { return nil }, err
	}
	code := m.Run()
	if r, err := sharedWebapp(); err == nil {
		_ = r.Close()
	}
	os.Exit(code)
}

// TestAnalyzeEndToEnd runs one real analysis through the command, the way
// the binary does, without the shared result.
func TestAnalyzeEndToEnd(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":  "module example.com/tiny\n\ngo 1.26.0\n",
		"main.go": "package main\n\nimport \"os\"\n\nfunc main() { run() }\n\nfunc run() { os.Exit(0) }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := run(t, "analyze", dir)
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	for _, want := range []string{"module example.com/tiny", "1 packages, 2 functions in the module", "calls 2"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestAnalyzeWebapp(t *testing.T) {
	code, stdout, stderr := run(t, "analyze", fixture.WebappDir())
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	for _, want := range []string{"module example.com/webapp", "11 packages", "nodes: method", "dispatches_to 8", "implements 3"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestAnalyzeWebappJSON(t *testing.T) {
	code, stdout, stderr := run(t, "analyze", "-json", fixture.WebappDir())
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	var got struct {
		Module string `json:"module"`
		Stats  struct {
			Packages int   `json:"packages"`
			TotalMS  int64 `json:"totalMs"`
		} `json:"stats"`
		Counts struct {
			Edges map[string]int `json:"edges"`
		} `json:"counts"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("%v:\n%s", err, stdout)
	}
	if got.Module != "example.com/webapp" || got.Stats.Packages != 11 || got.Stats.TotalMS <= 0 || got.Counts.Edges["calls"] == 0 {
		t.Errorf("summary = %+v", got)
	}
}

func TestQueryWebapp(t *testing.T) {
	dir := fixture.WebappDir()
	tests := []struct {
		name  string
		args  []string
		code  int
		out   string   // expected in stdout (or stderr on failure)
		flags []string // query flags, before the directory
	}{
		{"callees by ID without kind", []string{"callees", "(*example.com/webapp/internal/web.noteHandler).create"}, ExitOK,
			"calls\tmethod:(*example.com/webapp/internal/web.noteHandler).user\tinternal/web/handlers.go:", nil},
		{"callers by unique search", []string{"callers", "Authenticator).Authenticate"}, ExitOK, "web.requireAdmin$1", nil},
		{"paths through interface dispatch", []string{"paths", "noteHandler).create", "sql.Tx).ExecContext"}, ExitOK,
			"-dispatches_to-> method:(*example.com/webapp/internal/store/postgres.NoteRepository).Create", nil},
		{"search", []string{"search", "SMTPMailer"}, ExitOK, "type:example.com/webapp/internal/mail.SMTPMailer", nil},
		{"flow", []string{"flow", "POST /{lang}/notes"}, ExitOK,
			"sink.sql (*sql.Tx).QueryRowContext \"INSERT INTO notes (owner_id, title, body, created_at) VALUES ($1, $2, $3, $4) RETURNING id\"", nil},
		{name: "flow method", flags: []string{"-method", "GET"}, args: []string{"flow", "POST /{lang}/sign-in"}, code: ExitOK, out: "table sessions (select"},
		{name: "flow bad prune", flags: []string{"-prune", "bogus"}, args: []string{"flow", "POST /{lang}/notes"}, code: ExitUsage, out: `invalid -prune "bogus"`},
		{"routes", []string{"routes"}, ExitOK,
			"POST /{lang}/admin/reindex\tadmin\texample.com/webapp/internal/web.requireAdmin > (*example.com/webapp/internal/web.adminHandler).reindex\tinternal/web/router.go:", nil},
		{"ambiguous node", []string{"callees", "Create"}, ExitError, "matches several nodes; use an ID", nil},
		{"unknown node", []string{"callees", "nosuchthing"}, ExitError, `no node matches "nosuchthing"`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append(append(append([]string{"query"}, tt.flags...), dir), tt.args...)
			code, stdout, stderr := run(t, args...)
			if code != tt.code {
				t.Fatalf("exit code = %d, want %d; stderr: %s", code, tt.code, stderr)
			}
			if out := stdout + stderr; !strings.Contains(out, tt.out) {
				t.Errorf("output missing %q:\n%s", tt.out, out)
			}
		})
	}
}
