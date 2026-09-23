package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

func TestParseAndOptions(t *testing.T) {
	c, err := Parse(strings.NewReader(`
packages: [./cmd/..., ./internal/...]
tests: true
exclude: [log/slog]
sinks:
  - {kind: http, func: "(*example.com/app/stripe.Client).Charge", arg: 1, label: Stripe}
  - {kind: smtp, func: "(example.com/app.Mailer).Send"}
auth:
  guards:
    - func: example.com/app/auth.RequireUser
    - {func: example.com/app/auth.RequireAdmin, role: admin}
  roleFields: {admin: "(?i)superuser"}
migrations: [db/schema]
dialect: postgres
values:
  env:BASE_PATH: [/app]
`))
	if err != nil {
		t.Fatal(err)
	}
	opts := c.Options()
	if !slices.Equal(opts.Patterns, []string{"./cmd/...", "./internal/..."}) || !opts.Tests {
		t.Errorf("patterns/tests = %v %v", opts.Patterns, opts.Tests)
	}
	if !slices.Contains(opts.Exclude, "fmt") || opts.Exclude[len(opts.Exclude)-1] != "log/slog" {
		t.Errorf("exclude should extend the defaults: %v", opts.Exclude)
	}
	want := []analysis.SinkRule{
		{Kind: graph.KindSinkHTTP, Func: "(*example.com/app/stripe.Client).Charge", Arg: 1, Label: "Stripe"},
		{Kind: graph.KindSinkSMTP, Func: "(example.com/app.Mailer).Send", Arg: -1},
	}
	if !reflect.DeepEqual(opts.ExtraSinks, want) {
		t.Errorf("sinks = %+v", opts.ExtraSinks)
	}
	if !slices.Equal(opts.AuthFuncs, []string{"example.com/app/auth.RequireUser"}) ||
		opts.AuthRoles["example.com/app/auth.RequireAdmin"] != analysis.AccessAdmin {
		t.Errorf("guards = %v %v", opts.AuthFuncs, opts.AuthRoles)
	}
	if opts.RoleFields["admin"] != "(?i)superuser" || opts.MigrationDirs[0] != "db/schema" || opts.Values["env:BASE_PATH"][0] != "/app" {
		t.Errorf("options = %+v", opts)
	}

	c, err = Parse(strings.NewReader("noDefaultExclude: true\nexclude: [x]\n"))
	if err != nil || !slices.Equal(c.Options().Exclude, []string{"x"}) {
		t.Errorf("noDefaultExclude: %v %v", c.Options().Exclude, err)
	}
	if c, err := Parse(strings.NewReader("")); err != nil || c.Options().Exclude != nil {
		t.Errorf("empty config: %+v %v", c, err)
	}
}

func TestParseRejects(t *testing.T) {
	for _, c := range []struct{ yaml, want string }{
		{"pacakges: [./...]", "field pacakges not found"},
		{"dialect: mysql", `dialect "mysql" is not supported`},
		{"sinks: [{kind: ftp, func: f}]", `kind "ftp" is not one of`},
		{"sinks: [{kind: http}]", "sinks[0]: func is required"},
		{"sinks: [{kind: http, func: f, arg: -1}]", "arg must be >= 0"},
		{"auth: {guards: [{func: f, role: root}]}", `role "root" is not one of`},
		{"auth: {roleFields: {owner: x}}", `role "owner" is not one of admin, guest`},
	} {
		_, err := Parse(strings.NewReader(c.yaml))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %v, want %q", c.yaml, err, c.want)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if c, path, err := Load(dir, ""); c != nil || path != "" || err != nil {
		t.Errorf("no config: %v %q %v", c, path, err)
	}
	if _, _, err := Load(dir, filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("an explicit missing config should fail")
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("tests: true\ndialect: sqlite\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(dir, ""); err == nil || !strings.Contains(err.Error(), FileName) {
		t.Errorf("invalid config error should name the file: %v", err)
	}
}

// TestExampleAndSchema checks that the committed example parses and that
// the JSON schema describes exactly the fields Config has.
func TestExampleAndSchema(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, _, err := Load("", filepath.Join(root, "examples", "goweb", FileName)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "icb.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Items      struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	check := func(what string, typ reflect.Type, props map[string]json.RawMessage) {
		t.Helper()
		var fields []string
		for f := range typ.Fields() {
			fields = append(fields, strings.Split(f.Tag.Get("yaml"), ",")[0])
		}
		slices.Sort(fields)
		var keys []string
		for k := range props {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(fields, keys) {
			t.Errorf("%s: schema properties %v, struct fields %v", what, keys, fields)
		}
	}
	check("config", reflect.TypeFor[Config](), func() map[string]json.RawMessage {
		out := map[string]json.RawMessage{}
		for k := range schema.Properties {
			out[k] = nil
		}
		return out
	}())
	check("sinks", reflect.TypeFor[Sink](), schema.Properties["sinks"].Items.Properties)
	check("auth", reflect.TypeFor[Auth](), schema.Properties["auth"].Properties)
}
