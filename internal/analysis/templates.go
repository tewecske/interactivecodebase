package analysis

import (
	"cmp"
	"go/constant"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template/parse"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

// Template is one parsed template file.
type Template struct {
	// Name is the file's base name, File its path relative to the module.
	Name, File string
	// Defines are the templates the file defines, including Name itself.
	Defines []string
	// Refs are the URLs its HTML references.
	Refs []URLRef
	// uses maps each defined template to the templates it includes.
	uses map[string][]string
}

// URLRef is one URL-valued attribute in a template.
type URLRef struct {
	// Define is the defined template containing the attribute.
	Define string
	// Element and Attr are the HTML tag and attribute, e.g. "form", "hx-post".
	Element, Attr string
	// Method is the HTTP method a request uses (forms and hx-*).
	Method string
	// Raw is the attribute value as written; Values are its possible
	// resolved values, with Unknown for what could not be determined.
	Raw    string
	Values []string
	Line   int
}

// Kind classifies a reference: "request" (form or hx-*), "asset"
// (script, stylesheet, image), or "link" (a navigation link).
func (u URLRef) Kind() string {
	switch {
	case strings.HasPrefix(u.Attr, "hx-") || (u.Element == "form" && u.Attr == "action"):
		return "request"
	case u.Element == "script" || u.Element == "img" || u.Element == "link" || u.Element == "source":
		return "asset"
	case u.Element == "a" && u.Attr == "href":
		return "link"
	}
	return ""
}

var (
	tagRE    = regexp.MustCompile(`<([a-zA-Z][a-zA-Z0-9-]*)\b((?:[^>"']|"[^"]*"|'[^']*')*)>`)
	attrRE   = regexp.MustCompile(`([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	actionRE = regexp.MustCompile(`\{\{-?\s*(.*?)\s*-?\}\}`)
	fieldRE  = regexp.MustCompile(`^\$?((?:\.[A-Za-z_][A-Za-z0-9_]*)+)$`)
)

// urlAttrs are the attributes whose values are URLs.
var urlAttrs = []string{"href", "src", "action", "hx-get", "hx-post", "hx-put", "hx-patch", "hx-delete"}

// findTemplateFiles lists the template files the module parses, from its
// template.ParseFS / ParseGlob / ParseFiles sinks.
func findTemplateFiles(r *Result) []string {
	dirs := packageDirs(r.Packages)
	var files []string
	for _, s := range r.Sinks {
		var base string
		switch s.Callee {
		case "html/template.ParseFS", "text/template.ParseFS":
			call, ok := s.Instr.(ssa.CallInstruction)
			if !ok || len(call.Common().Args) == 0 {
				continue
			}
			g := embedGlobal(call.Common().Args[0])
			if g == nil {
				continue
			}
			base = dirs[g.Pkg.Pkg.Path()]
		case "html/template.ParseGlob", "text/template.ParseGlob", "html/template.ParseFiles", "text/template.ParseFiles":
			base = r.Dir
		default:
			continue
		}
		for _, pattern := range s.Values {
			if base == "" || strings.Contains(pattern, Unknown) {
				continue
			}
			matches, _ := filepath.Glob(filepath.Join(base, pattern))
			for _, m := range matches {
				if !slices.Contains(files, m) {
					files = append(files, m)
				}
			}
		}
	}
	slices.Sort(files)
	return files
}

// embedGlobal finds the package variable an embed.FS argument comes from.
func embedGlobal(v ssa.Value) *ssa.Global {
	for range maxEvalDepth {
		switch x := v.(type) {
		case *ssa.Global:
			return x
		case *ssa.UnOp:
			v = x.X
		case *ssa.MakeInterface:
			v = x.X
		case *ssa.ChangeType:
			v = x.X
		default:
			return nil
		}
	}
	return nil
}

// packageDirs maps import paths of loaded packages to their directories.
func packageDirs(pkgs []*packages.Package) map[string]string {
	dirs := map[string]string{}
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		if len(p.GoFiles) > 0 {
			dirs[p.PkgPath] = filepath.Dir(p.GoFiles[0])
		}
	})
	return dirs
}

// parseTemplates parses template files and extracts their URL references.
func parseTemplates(r *Result, files []string) []*Template {
	var out []*Template
	for _, file := range files {
		b, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		text := string(b)
		name := filepath.Base(file)
		t := parse.New(name)
		t.Mode = parse.SkipFuncCheck
		trees := map[string]*parse.Tree{}
		if _, err := t.Parse(text, "", "", trees); err != nil {
			continue
		}
		rel, _ := filepath.Rel(r.Dir, file)
		tmpl := &Template{Name: name, File: filepath.ToSlash(rel), uses: map[string][]string{}}
		defines := make([]string, 0, len(trees))
		for def := range trees {
			defines = append(defines, def)
		}
		slices.Sort(defines)
		for _, def := range defines {
			tree := trees[def]
			if def != name {
				tmpl.Defines = append(tmpl.Defines, def)
			}
			var html strings.Builder
			var uses []string
			flatten(tree.Root, &html, &uses)
			tmpl.uses[def] = uses
			tmpl.Refs = append(tmpl.Refs, extractRefs(def, html.String(), text)...)
		}
		tmpl.Defines = append([]string{name}, tmpl.Defines...)
		out = append(out, tmpl)
	}
	return out
}

// flatten renders a template tree to HTML with actions kept as {{...}},
// following both branches of conditionals, and records {{template}} uses.
func flatten(n parse.Node, sb *strings.Builder, uses *[]string) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			flatten(c, sb, uses)
		}
	case *parse.TextNode:
		sb.Write(n.Text)
	case *parse.ActionNode:
		sb.WriteString(n.String())
	case *parse.IfNode:
		flatten(n.List, sb, uses)
		flatten(n.ElseList, sb, uses)
	case *parse.RangeNode:
		flatten(n.List, sb, uses)
		flatten(n.ElseList, sb, uses)
	case *parse.WithNode:
		flatten(n.List, sb, uses)
		flatten(n.ElseList, sb, uses)
	case *parse.TemplateNode:
		*uses = append(*uses, n.Name)
	}
}

// extractRefs finds URL attributes in flattened HTML. Lines are looked up
// in the original file text.
func extractRefs(define, html, fileText string) []URLRef {
	var refs []URLRef
	for _, tag := range tagRE.FindAllStringSubmatch(html, -1) {
		element := strings.ToLower(tag[1])
		attrs := map[string]string{}
		var order []string
		for _, a := range attrRE.FindAllStringSubmatch(tag[2], -1) {
			name := strings.ToLower(a[1])
			attrs[name] = a[2] + a[3]
			order = append(order, name)
		}
		for _, name := range order {
			if !slices.Contains(urlAttrs, name) {
				continue
			}
			value := attrs[name]
			if value == "" || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "javascript:") {
				continue
			}
			ref := URLRef{Define: define, Element: element, Attr: name, Raw: value, Line: attrLine(fileText, name, value)}
			switch {
			case strings.HasPrefix(name, "hx-"):
				ref.Method = strings.ToUpper(strings.TrimPrefix(name, "hx-"))
			case element == "form" && name == "action":
				ref.Method = strings.ToUpper(cmp.Or(attrs["method"], "GET"))
			case element == "a":
				ref.Method = "GET"
			}
			if element == "link" && !strings.Contains(strings.ToLower(attrs["rel"]), "stylesheet") &&
				!strings.Contains(strings.ToLower(attrs["rel"]), "icon") {
				continue
			}
			refs = append(refs, ref)
		}
	}
	return refs
}

func attrLine(text, attr, value string) int {
	for _, q := range []string{`"`, `'`} {
		if i := strings.Index(text, attr+"="+q+value); i >= 0 {
			return strings.Count(text[:i], "\n") + 1
		}
	}
	return 0
}

// templateResolver resolves {{.Field}} parts of template URLs from the
// values the module stores into struct fields with that name.
type templateResolver struct {
	ev     *evaluator
	stores map[string][]fieldStore // field name -> stores
	cache  map[string][]string
}

type fieldStore struct {
	fn  *ssa.Function
	val ssa.Value
}

func newTemplateResolver(r *Result, own []*ssa.Function) *templateResolver {
	tr := &templateResolver{ev: r.evaluator(), stores: map[string][]fieldStore{}, cache: map[string][]string{}}
	for _, fn := range own {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				st, ok := instr.(*ssa.Store)
				if !ok {
					continue
				}
				fa, ok := st.Addr.(*ssa.FieldAddr)
				if !ok {
					continue
				}
				if name := fieldName(fa); name != "" {
					tr.stores[name] = append(tr.stores[name], fieldStore{fn: fn, val: st.Val})
				}
			}
		}
	}
	return tr
}

func fieldName(fa *ssa.FieldAddr) string {
	t := fa.X.Type()
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	if st, ok := t.Underlying().(*types.Struct); ok && fa.Field < st.NumFields() {
		return st.Field(fa.Field).Name()
	}
	return ""
}

// resolve returns the possible values of a template attribute. Field
// values come from stores in the functions in scope (a page's handler and
// what it calls); without any there, from stores anywhere in the module.
func (tr *templateResolver) resolve(raw string, scope map[*ssa.Function]bool) []string {
	out := []string{""}
	last := 0
	for _, m := range actionRE.FindAllStringSubmatchIndex(raw, -1) {
		out = concat(out, []string{raw[last:m[0]]})
		out = concat(out, tr.action(raw[m[2]:m[3]], scope))
		last = m[1]
	}
	return concat(out, []string{raw[last:]})
}

func (tr *templateResolver) action(expr string, scope map[*ssa.Function]bool) []string {
	m := fieldRE.FindStringSubmatch(expr)
	if m == nil {
		return []string{Unknown}
	}
	parts := strings.Split(m[1], ".")
	name := parts[len(parts)-1]
	var local []string
	for _, st := range tr.stores[name] {
		if scope[st.fn] {
			local = union(local, urlValues(tr.ev, st.val))
		}
	}
	if len(local) > 0 {
		return limit(local)
	}
	if vals, ok := tr.cache[name]; ok {
		return vals
	}
	var vals []string
	for _, st := range tr.stores[name] {
		vals = union(vals, urlValues(tr.ev, st.val))
	}
	vals = limit(vals)
	if len(vals) == 0 {
		vals = []string{Unknown}
	}
	tr.cache[name] = vals
	return vals
}

// urlValues evaluates a URL-valued expression. When the value cannot be
// folded but comes from a call given a constant path, such as
// h.path(language, "/groups") building "/en/groups" at run time, it is
// taken as that path under an unknown prefix: "{?}/groups".
func urlValues(ev *evaluator, v ssa.Value) []string {
	vals := ev.strings(v)
	if len(vals) != 1 || vals[0] != Unknown {
		return vals
	}
	call, ok := v.(*ssa.Call)
	if !ok {
		return vals
	}
	var hinted []string
	for _, arg := range call.Common().Args {
		if b, ok := arg.Type().Underlying().(*types.Basic); !ok || b.Info()&types.IsString == 0 {
			continue
		}
		paths := ev.strings(arg)
		if slices.ContainsFunc(paths, func(p string) bool { return !strings.HasPrefix(p, "/") || strings.Contains(p, Unknown) }) {
			continue
		}
		for _, p := range paths {
			hinted = union(hinted, []string{Unknown + p})
		}
	}
	if len(hinted) == 0 {
		return vals
	}
	return hinted
}

// mentionedTemplates returns the template names (files or defined
// templates) used as string constants in blocks.
func mentionedTemplates(blocks []*ssa.BasicBlock, defines map[string]*Template) []string {
	var out []string
	for _, b := range blocks {
		for _, instr := range b.Instrs {
			var ops []*ssa.Value
			for _, op := range instr.Operands(ops) {
				k, ok := (*op).(*ssa.Const)
				if !ok || k.Value == nil || k.Value.Kind() != constant.String {
					continue
				}
				if name := constant.StringVal(k.Value); defines[name] != nil && !slices.Contains(out, name) {
					out = append(out, name)
				}
			}
		}
	}
	return out
}
