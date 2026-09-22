package fixture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Validate checks that everything exp names exists in the module at dir:
// handler and middleware functions (including closures), interfaces and
// concrete types, tables and foreign keys in migrations/*.sql, templates and
// static assets, and that routes, flows and pages are consistent with each
// other. It returns one message per problem.
//
// This keeps hand-written expectations honest before analysis exists; it is
// not a substitute for analysis.
func Validate(dir string, exp Expectations) ([]string, error) {
	src, err := indexSource(dir, exp.Module)
	if err != nil {
		return nil, err
	}
	schema, err := readSchema(filepath.Join(dir, "migrations"))
	if err != nil {
		return nil, err
	}
	v := &validator{src: src, schema: schema, dir: dir, module: exp.Module}
	v.routes(exp)
	v.tables(exp)
	v.implementations(exp)
	v.sinks(exp)
	v.flows(exp)
	v.pages(exp)
	return v.problems, nil
}

type validator struct {
	src      *sourceIndex
	schema   *schema
	dir      string
	module   string
	problems []string
}

func (v *validator) errorf(format string, args ...any) {
	v.problems = append(v.problems, fmt.Sprintf(format, args...))
}

// function checks that name is declared, if it belongs to the module; names
// outside it (the standard library, dependencies) are assumed to exist.
func (v *validator) function(context, name string) {
	if strings.Contains(name, v.module+"/") || strings.Contains(name, v.module+".") {
		if !v.src.hasFunc(name) {
			v.errorf("%s: function %s not found", context, name)
		}
		return
	}
	if !strings.Contains(name, ".") {
		v.errorf("%s: function %s not found", context, name)
	}
}

var validAccess = []string{AccessPublic, AccessAuthenticated, AccessAdmin, AccessGuest}

func (v *validator) routes(exp Expectations) {
	seen := map[string]bool{}
	for _, r := range exp.Routes {
		key := r.Key()
		if seen[key] {
			v.errorf("route %s: duplicate", key)
		}
		seen[key] = true
		if r.Method == "" || !strings.HasPrefix(r.Pattern, "/") {
			v.errorf("route %q: needs a method and a pattern starting with /", key)
		}
		if !slices.Contains(validAccess, r.Access) {
			v.errorf("route %s: unknown access %q", key, r.Access)
		}
		if r.OptionalAuth && r.Access != AccessPublic {
			v.errorf("route %s: optionalAuth only applies to public routes", key)
		}
		if strings.HasPrefix(r.Pattern, "/{lang}/") && len(r.Variants) == 0 {
			v.errorf("route %s: /{lang} route lists no variants", key)
		}
		for _, variant := range r.Variants {
			if !strings.HasSuffix(variant, strings.TrimPrefix(r.Pattern, "/{lang}")) {
				v.errorf("route %s: variant %s does not match the pattern", key, variant)
			}
		}
		if strings.Contains(r.Pattern, "{?}") && !r.Conditional {
			v.errorf("route %s: unresolved {?} part but not marked conditional", key)
		}
		v.function("route "+key, r.Handler)
		for _, m := range r.Middleware {
			v.function("route "+key+" middleware", m)
		}
	}
	for _, m := range exp.Middleware {
		v.function("middleware", m)
	}
}

func (v *validator) tables(exp Expectations) {
	for _, t := range exp.Tables {
		if !v.schema.tables[t] {
			v.errorf("table %s: no CREATE TABLE in migrations", t)
		}
	}
	for t := range v.schema.tables {
		if !slices.Contains(exp.Tables, t) {
			v.errorf("table %s: created in migrations but not expected", t)
		}
	}
	for _, fk := range exp.ForeignKeys {
		if !slices.Contains(v.schema.fks, fk) {
			v.errorf("foreign key %s.%s -> %s (on delete %q): not in migrations", fk.Table, fk.Column, fk.References, fk.OnDelete)
		}
	}
	for _, fk := range v.schema.fks {
		if !slices.Contains(exp.ForeignKeys, fk) {
			v.errorf("foreign key %s.%s -> %s: in migrations but not expected", fk.Table, fk.Column, fk.References)
		}
	}
}

func (v *validator) implementations(exp Expectations) {
	for _, impl := range exp.Implementations {
		if kind, ok := v.src.types[impl.Interface]; !ok || kind != "interface" {
			v.errorf("implementation: interface %s not found", impl.Interface)
		}
		concrete := strings.TrimPrefix(impl.Concrete, "*")
		if kind, ok := v.src.types[concrete]; !ok || kind == "interface" {
			v.errorf("implementation: concrete type %s not found", concrete)
		}
	}
}

func (v *validator) sinks(exp Expectations) {
	for _, s := range exp.Sinks {
		if !strings.HasPrefix(s.Kind, "sink.") {
			v.errorf("sink in %s: kind %q is not a sink kind", s.In, s.Kind)
		}
		v.function("sink "+s.Kind, s.In)
		for _, t := range s.Tables {
			if !v.schema.tables[t] {
				v.errorf("sink in %s: unknown table %s", s.In, t)
			}
		}
	}
}

func (v *validator) flows(exp Expectations) {
	routes := routeKeys(exp)
	for _, f := range exp.Flows {
		if !routes[f.Route] {
			v.errorf("flow %s: no such route", f.Route)
		}
		for _, r := range f.Reaches {
			if (r.Table == "") == (r.Sink == "") {
				v.errorf("flow %s: each reach needs exactly one of table or sink", f.Route)
			}
			if r.Table != "" && !v.schema.tables[r.Table] {
				v.errorf("flow %s: unknown table %s", f.Route, r.Table)
			}
		}
	}
}

func (v *validator) pages(exp Expectations) {
	routes := routeKeys(exp)
	for _, p := range exp.Pages {
		if !routes[p.Route] {
			v.errorf("page %s: no such route", p.Route)
		}
		if _, err := os.Stat(filepath.Join(v.dir, "templates", p.Template)); err != nil {
			v.errorf("page %s: template %s not found", p.Route, p.Template)
		}
		for _, r := range p.Requests {
			if !routes[r.Method+" "+r.URL] {
				v.errorf("page %s: request %s %s matches no route", p.Route, r.Method, r.URL)
			}
		}
		for _, n := range p.NavigatesTo {
			if !routes[n] {
				v.errorf("page %s: navigates to unknown route %s", p.Route, n)
			}
		}
		for _, a := range p.Assets {
			name, ok := strings.CutPrefix(a, "/static/")
			if _, err := os.Stat(filepath.Join(v.dir, "static", name)); !ok || err != nil {
				v.errorf("page %s: asset %s not found under static/", p.Route, a)
			}
		}
	}
}

func routeKeys(exp Expectations) map[string]bool {
	keys := map[string]bool{}
	for _, r := range exp.Routes {
		keys[r.Key()] = true
	}
	return keys
}

// sourceIndex records declared functions and types by their ssa-style names.
type sourceIndex struct {
	funcs map[string]int    // name -> number of top-level closures in its body
	types map[string]string // "pkg.T" -> "interface" or "other"
}

// hasFunc reports whether name is declared. A closure "F$N" exists if F
// has at least N closures.
func (s *sourceIndex) hasFunc(name string) bool {
	base, closure, _ := strings.Cut(name, "$")
	n, ok := s.funcs[base]
	if !ok || closure == "" {
		return ok
	}
	i, err := strconv.Atoi(closure)
	return err == nil && i >= 1 && i <= n
}

func indexSource(dir, module string) (*sourceIndex, error) {
	idx := &sourceIndex{funcs: map[string]int{}, types: map[string]string{}}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := module
		if rel != "." {
			pkg += "/" + filepath.ToSlash(rel)
		}
		idx.addFile(pkg, f)
		return nil
	})
	return idx, err
}

func (s *sourceIndex) addFile(pkg string, f *ast.File) {
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			s.funcs[funcName(pkg, d)] = countClosures(d.Body)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				kind := "other"
				if _, ok := ts.Type.(*ast.InterfaceType); ok {
					kind = "interface"
				}
				s.types[pkg+"."+ts.Name.Name] = kind
			}
		}
	}
}

func funcName(pkg string, d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return pkg + "." + d.Name.Name
	}
	typ := d.Recv.List[0].Type
	star := ""
	if se, ok := typ.(*ast.StarExpr); ok {
		star, typ = "*", se.X
	}
	switch t := typ.(type) {
	case *ast.IndexExpr:
		typ = t.X
	case *ast.IndexListExpr:
		typ = t.X
	}
	recv := "?"
	if id, ok := typ.(*ast.Ident); ok {
		recv = id.Name
	}
	return "(" + star + pkg + "." + recv + ")." + d.Name.Name
}

// countClosures counts function literals directly in body, not nested in
// other literals; go/ssa names these F$1, F$2, ... in source order.
func countClosures(body *ast.BlockStmt) int {
	if body == nil {
		return 0
	}
	n := 0
	ast.Inspect(body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			n++
			return false
		}
		return true
	})
	return n
}

// schema is a regex-level reading of migrations, enough to check
// expectations; the real schema analysis is issue #9.
type schema struct {
	tables map[string]bool
	fks    []ForeignKey
}

var (
	createTableRE = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s*\((.*?)\n\);`)
	inlineFKRE    = regexp.MustCompile(`(?im)^\s*(\w+)\s+[^,\n]*?\bREFERENCES\s+(\w+)\s*\((\w+)\)(?:\s+ON\s+DELETE\s+(CASCADE|SET\s+NULL|RESTRICT|NO\s+ACTION))?`)
	alterFKRE     = regexp.MustCompile(`(?is)ALTER\s+TABLE\s+(\w+)\s+ADD\s+CONSTRAINT\s+\w+\s+FOREIGN\s+KEY\s*\((\w+)\)\s*REFERENCES\s+(\w+)\s*\((\w+)\)(?:\s+ON\s+DELETE\s+(CASCADE|SET\s+NULL|RESTRICT|NO\s+ACTION))?`)
	spaceRE       = regexp.MustCompile(`\s+`)
)

func readSchema(dir string) (*schema, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	s := &schema{tables: map[string]bool{}}
	onDelete := func(v string) string { return strings.ToLower(spaceRE.ReplaceAllString(v, " ")) }
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		sql := string(b)
		for _, m := range createTableRE.FindAllStringSubmatch(sql, -1) {
			s.tables[m[1]] = true
			for _, fk := range inlineFKRE.FindAllStringSubmatch(m[2], -1) {
				s.fks = append(s.fks, ForeignKey{Table: m[1], Column: fk[1], References: fk[2] + "." + fk[3], OnDelete: onDelete(fk[4])})
			}
		}
		for _, m := range alterFKRE.FindAllStringSubmatch(sql, -1) {
			s.fks = append(s.fks, ForeignKey{Table: m[1], Column: m[2], References: m[3] + "." + m[4], OnDelete: onDelete(m[5])})
		}
	}
	return s, nil
}
