// Package scala analyzes sbt projects of Scala 3 code. sbt compiles the
// project and reports each project's classpath; icb-scala, the extractor
// in extractors/scala, reads the TASTy of the project's own classes and
// writes the code graph as JSON (docs/graph.schema.json), which is
// imported as an analysis.Project, whose SQL sinks are then linked to the
// tables of the project's migrations (Flyway's by default).
//
// Both run on the JVM. The machine may be shared, so each gets a capped
// heap: sbt through -J-Xmx, the extractor through its launcher script.
package scala

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/sqlparse"
)

// DefaultHeap caps sbt's heap.
const DefaultHeap = "1500m"

// ExtractorEnv names the environment variable with the icb-scala command.
const ExtractorEnv = "ICB_SCALA"

// Options configure a Scala analysis. The zero value analyzes every
// project the sbt build aggregates.
type Options struct {
	// Extractor is the icb-scala command, relative paths resolved against
	// the project directory; default $ICB_SCALA, then icb-scala on PATH.
	Extractor string
	// SBT is the sbt command; default sbt.
	SBT string
	// Projects are the sbt projects to analyze, e.g. backend; default all.
	Projects []string
	// MigrationDirs are the directories with SQL migrations, relative to
	// the project directory; default sqlparse.DefaultMigrationDirs and the
	// Flyway directories (see sqlparse.FlywayDirs).
	MigrationDirs []string
	// Guards maps aspects and middleware by name
	// ("app.http.RouteSupport.authenticated") to the access they enforce
	// on the routes they wrap: authenticated, admin or guest.
	Guards map[string]string
}

// IsProject reports whether dir holds an sbt build.
func IsProject(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "build.sbt"))
	return err == nil
}

// Open compiles the sbt project in dir, extracts its code graph and
// imports it. Stats.Load is the sbt run, Stats.CallGraph the extractor
// and Stats.Write the import.
func Open(ctx context.Context, dir string, opts Options) (*analysis.Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	sbt, extractor, err := commands(abs, opts)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	out, err := run(ctx, abs, sbt, sbtArgs(opts.Projects)...)
	if err != nil {
		return nil, fmt.Errorf("scala: sbt: %w%s", err, tail(out))
	}
	mods := modules(abs, strings.Split(string(out), "\n"))
	if len(mods) == 0 {
		return nil, fmt.Errorf("scala: sbt reported no class directories under %s%s", abs, tail(out))
	}
	load := time.Since(start)

	start = time.Now()
	tmp, err := os.CreateTemp("", "icb-scala-*.json")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if out, err := run(ctx, abs, extractor, extractorArgs(abs, name, opts.Guards, mods)...); err != nil {
		return nil, fmt.Errorf("scala: %s: %w%s", extractor, err, tail(out))
	}
	extract := time.Since(start)

	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	p, err := analysis.ImportProject(ctx, analysis.LangScala, filepath.Base(abs), abs, f)
	if err != nil {
		return nil, fmt.Errorf("scala: importing the extractor's graph: %w", err)
	}
	p.Stats.Load, p.Stats.CallGraph = load, extract
	if err := linkSQL(ctx, p, opts.MigrationDirs); err != nil {
		return nil, errors.Join(err, p.Close())
	}
	if err := count(ctx, p); err != nil {
		return nil, errors.Join(err, p.Close())
	}
	return p, nil
}

// commands finds sbt, java and the extractor, or says how to get them.
func commands(dir string, opts Options) (sbt, extractor string, err error) {
	sbt, err = exec.LookPath(cmp.Or(opts.SBT, "sbt"))
	if err != nil {
		return "", "", fmt.Errorf("scala: sbt is needed to analyze a Scala project: %w", err)
	}
	if _, err := exec.LookPath("java"); err != nil {
		return "", "", fmt.Errorf("scala: a JDK (java) is needed to analyze a Scala project: %w", err)
	}
	extractor = cmp.Or(opts.Extractor, os.Getenv(ExtractorEnv), "icb-scala")
	if strings.ContainsRune(extractor, filepath.Separator) && !filepath.IsAbs(extractor) {
		extractor = filepath.Join(dir, extractor)
	}
	path, err := exec.LookPath(extractor)
	if err != nil {
		return "", "", fmt.Errorf("scala: extractor %s not found (%w): build it with `make scala-extractor`, "+
			"then set %s or scala.extractor in icb.yaml to extractors/scala/target/icb-scala, or put it on PATH as icb-scala",
			extractor, err, ExtractorEnv)
	}
	return sbt, path, nil
}

// sbtArgs compiles the projects and prints their classpaths. export
// prints one classpath line per project, after the task's key when it
// aggregates; -error hides everything but errors.
func sbtArgs(projects []string) []string {
	args := []string{"-batch", "-no-colors", "-error", "-J-Xmx" + DefaultHeap, "-Dsbt.supershell=false"}
	if len(projects) == 0 {
		return append(args, "export Compile/fullClasspath")
	}
	for _, p := range projects {
		args = append(args, "export "+p+"/Compile/fullClasspath")
	}
	return args
}

// module is what the extractor reads for one sbt project: its class
// directories (its own and those of the projects it depends on in the
// build) and the libraries they compile against.
type module struct {
	classes   []string
	classpath []string
}

// modules parses sbt's export output. A classpath entry inside root is
// the build's own classes; anything else is a library. A project whose
// classes another project already includes (shared, when backend depends
// on it) is left out, so each class is read once where possible.
func modules(root string, lines []string) []module {
	var mods []module
	for _, line := range lines {
		line = strings.TrimSpace(line)
		entries := filepath.SplitList(line)
		if line == "" || slices.ContainsFunc(entries, isRelative) {
			continue
		}
		var m module
		for _, e := range entries {
			if rel, err := filepath.Rel(root, e); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				if !strings.HasSuffix(e, ".jar") {
					m.classes = append(m.classes, e)
					continue
				}
			}
			m.classpath = append(m.classpath, e)
		}
		if len(m.classes) > 0 {
			mods = append(mods, m)
		}
	}
	var out []module
	for i, m := range mods {
		covered := slices.ContainsFunc(mods, func(o module) bool {
			return len(o.classes) > len(m.classes) && subset(m.classes, o.classes)
		})
		dup := slices.ContainsFunc(mods[:i], func(o module) bool { return slices.Equal(o.classes, m.classes) })
		if !covered && !dup {
			out = append(out, m)
		}
	}
	return out
}

func isRelative(p string) bool { return !filepath.IsAbs(p) }

func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

func extractorArgs(root, out string, guards map[string]string, mods []module) []string {
	args := []string{"--root", root, "-o", out}
	for _, name := range slices.Sorted(maps.Keys(guards)) {
		args = append(args, "--guard", name+"="+guards[name])
	}
	for _, m := range mods {
		args = append(args, "--classpath", strings.Join(m.classpath, string(filepath.ListSeparator)))
		args = append(args, m.classes...)
	}
	return args
}

// run runs name in dir and returns its combined output.
func run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.Bytes(), err
}

// tail is the end of a command's output, for an error message.
func tail(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	s := strings.Join(lines, "\n")
	if s == "" {
		return ""
	}
	return ":\n" + s
}

// linkSQL adds the schema from the migrations in dirs, by default the usual
// directories and Flyway's, and links the SQL sinks to its tables.
func linkSQL(ctx context.Context, p *analysis.Project, dirs []string) error {
	if dirs == nil {
		flyway, err := sqlparse.FlywayDirs(p.Dir)
		if err != nil {
			return err
		}
		dirs = append(slices.Clone(sqlparse.DefaultMigrationDirs), flyway...)
	}
	if err := p.LinkSQL(ctx, dirs); err != nil {
		return fmt.Errorf("scala: linking SQL to the migrations: %w", err)
	}
	return nil
}

// count fills in the stats the analyze command prints: packages and
// functions.
func count(ctx context.Context, p *analysis.Project) error {
	nodes, err := p.Graph.Nodes(ctx, graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindFunc, graph.KindMethod}})
	if err != nil {
		return err
	}
	pkgs := map[string]bool{}
	for _, n := range nodes {
		pkgs[n.Package] = true
	}
	p.Stats.Packages, p.Stats.Functions, p.Stats.ModuleFunctions = len(pkgs), len(nodes), len(nodes)
	return nil
}
