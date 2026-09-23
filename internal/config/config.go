// Package config reads icb.yaml, the per-project settings for analysis:
// which packages to load, what to leave out, extra sinks, authentication
// guards, migrations and values the analysis cannot resolve statically.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

// FileName is the file Find looks for in the analyzed directory.
const FileName = "icb.yaml"

// Config is the content of icb.yaml. Every field is optional.
type Config struct {
	// Packages are the package patterns to analyze, relative to the
	// module root; default "./...".
	Packages []string `yaml:"packages"`
	// Tests includes _test.go files.
	Tests bool `yaml:"tests"`
	// Exclude lists package path prefixes left out as call targets, on
	// top of the defaults (fmt, strings, log, ...) unless
	// NoDefaultExclude is set.
	Exclude          []string `yaml:"exclude"`
	NoDefaultExclude bool     `yaml:"noDefaultExclude"`
	// Sinks are extra calls that leave the program, such as the module's
	// own client for a third-party API.
	Sinks []Sink `yaml:"sinks"`
	Auth  Auth   `yaml:"auth"`
	// Migrations are directories with SQL migrations, relative to the
	// module root; default: the usual names (migrations, db/migrations,
	// sql/schema, ...).
	Migrations []string `yaml:"migrations"`
	// Dialect is the SQL dialect; only "postgres" is supported.
	Dialect string `yaml:"dialect"`
	// Values gives the possible values of things the analysis cannot
	// resolve, keyed by the go/ssa name of a package variable or function
	// ("example.com/app/config.BasePath") or "env:NAME" for an environment
	// variable read with os.Getenv. Route prefixes from configuration are
	// the typical use.
	Values map[string][]string `yaml:"values"`
}

// Sink is a custom sink rule.
type Sink struct {
	// Kind is sql, http, file, smtp, exec or env.
	Kind string `yaml:"kind"`
	// Func is the go/ssa name of the function, e.g.
	// "(*example.com/app/stripe.Client).Charge".
	Func string `yaml:"func"`
	// Arg is the index of the argument naming what the sink touches (SQL,
	// URL, file), after the receiver; omit for none.
	Arg *int `yaml:"arg"`
	// Label names what the sink reaches, e.g. "Stripe API".
	Label string `yaml:"label"`
}

// Auth configures access-level detection.
type Auth struct {
	// Guards are functions that check authentication, in addition to
	// request-taking functions named *authenticat*.
	Guards []Guard `yaml:"guards"`
	// RoleFields maps admin and guest to regular expressions matching the
	// names of fields a guard branches on to require that role.
	RoleFields map[string]string `yaml:"roleFields"`
}

// Guard is an authentication check.
type Guard struct {
	// Func is its go/ssa name, e.g. "example.com/app/auth.RequireUser".
	Func string `yaml:"func"`
	// Role is what passing it grants: authenticated (default), admin or
	// guest.
	Role string `yaml:"role"`
}

var sinkKinds = map[string]graph.NodeKind{
	"sql": graph.KindSinkSQL, "http": graph.KindSinkHTTP, "file": graph.KindSinkFile,
	"smtp": graph.KindSinkSMTP, "exec": graph.KindSinkExec, "env": graph.KindSinkEnv,
}

// Parse reads a config, rejecting unknown fields.
func Parse(r io.Reader) (*Config, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) validate() error {
	var errs []error
	if c.Dialect != "" && c.Dialect != "postgres" {
		errs = append(errs, fmt.Errorf("dialect %q is not supported (only postgres)", c.Dialect))
	}
	for i, s := range c.Sinks {
		if _, ok := sinkKinds[s.Kind]; !ok {
			errs = append(errs, fmt.Errorf("sinks[%d]: kind %q is not one of sql, http, file, smtp, exec, env", i, s.Kind))
		}
		if s.Func == "" {
			errs = append(errs, fmt.Errorf("sinks[%d]: func is required", i))
		}
		if s.Arg != nil && *s.Arg < 0 {
			errs = append(errs, fmt.Errorf("sinks[%d]: arg must be >= 0", i))
		}
	}
	for i, g := range c.Auth.Guards {
		if g.Func == "" {
			errs = append(errs, fmt.Errorf("auth.guards[%d]: func is required", i))
		}
		if !slices.Contains([]string{"", analysis.AccessAuthenticated, analysis.AccessAdmin, analysis.AccessGuest}, g.Role) {
			errs = append(errs, fmt.Errorf("auth.guards[%d]: role %q is not one of authenticated, admin, guest", i, g.Role))
		}
	}
	for role := range c.Auth.RoleFields {
		if role != analysis.AccessAdmin && role != analysis.AccessGuest {
			errs = append(errs, fmt.Errorf("auth.roleFields: role %q is not one of admin, guest", role))
		}
	}
	return errors.Join(errs...)
}

// Options converts the config to analysis options.
func (c *Config) Options() analysis.Options {
	var opts analysis.Options
	if c == nil {
		return opts
	}
	opts.Patterns = c.Packages
	opts.Tests = c.Tests
	if c.NoDefaultExclude {
		opts.Exclude = append([]string{}, c.Exclude...)
	} else if len(c.Exclude) > 0 {
		opts.Exclude = append(slices.Clone(analysis.DefaultExclude), c.Exclude...)
	}
	for _, s := range c.Sinks {
		arg := -1
		if s.Arg != nil {
			arg = *s.Arg
		}
		opts.ExtraSinks = append(opts.ExtraSinks, analysis.SinkRule{Kind: sinkKinds[s.Kind], Func: s.Func, Arg: arg, Label: s.Label})
	}
	for _, g := range c.Auth.Guards {
		if g.Role == "" || g.Role == analysis.AccessAuthenticated {
			opts.AuthFuncs = append(opts.AuthFuncs, g.Func)
			continue
		}
		if opts.AuthRoles == nil {
			opts.AuthRoles = map[string]string{}
		}
		opts.AuthRoles[g.Func] = g.Role
	}
	opts.RoleFields = c.Auth.RoleFields
	opts.MigrationDirs = c.Migrations
	opts.Values = c.Values
	return opts
}

// Load reads the config at path, or when path is empty the icb.yaml in
// dir if there is one. It returns the path it read ("" for none).
func Load(dir, path string) (*Config, string, error) {
	explicit := path != ""
	if !explicit {
		path = filepath.Join(dir, FileName)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) && !explicit {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("config: %w", err)
	}
	c, err := Parse(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("config %s: %s", path, strings.ReplaceAll(err.Error(), "\n", "; "))
	}
	return c, path, nil
}
