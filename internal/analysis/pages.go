package analysis

import (
	"strings"

	"golang.org/x/tools/go/ssa"
)

// Page is what a route renders: templates and the URLs they reference.
type Page struct {
	// Renders are the template names (files or defined templates) the
	// route's handler renders.
	Renders []string
	// Refs are the URL references of the rendered templates and the
	// templates they include.
	Refs []PageRef
}

// Trigger names what makes a request: the hx-* attribute, or "form" for
// a plain form submission.
func (u URLRef) Trigger() string {
	if strings.HasPrefix(u.Attr, "hx-") {
		return u.Attr
	}
	if u.Element == "form" {
		return "form"
	}
	return u.Element
}

// PageRef is a template URL reference as seen from one page.
type PageRef struct {
	URLRef
	Template *Template
	// Target is the key of the route the URL resolves to, if any.
	Target string
}

// buildPages finds the templates each route renders and resolves the URLs
// they reference against the routes.
func buildPages(r *Result, own []*ssa.Function) {
	r.Templates = parseTemplates(r, findTemplateFiles(r))
	if len(r.Templates) == 0 {
		return
	}
	resolver := newTemplateResolver(r, own)
	for _, t := range r.Templates {
		for i := range t.Refs {
			t.Refs[i].Values = resolver.resolve(t.Refs[i].Raw, nil)
		}
	}
	defines := map[string]*Template{}
	for _, t := range r.Templates {
		for _, d := range t.Defines {
			defines[d] = t
		}
	}
	w := &authClassifier{r: r, scopes: r.scopes}
	for i := range r.Routes {
		rt := &r.Routes[i]
		if rt.Handler == nil {
			continue
		}
		names := nearestTemplates(w, rt.Handler, rt.Method, defines)
		if len(names) == 0 {
			continue
		}
		page := &Page{Renders: names}
		scope := reachableFuncs(w, rt.Handler, rt.Method)
		for _, def := range includedDefines(names, defines) {
			t := defines[def]
			for _, ref := range t.Refs {
				if ref.Define != def {
					continue
				}
				pr := PageRef{URLRef: ref, Template: t}
				pr.Values = resolver.resolve(ref.Raw, scope)
				if ref.Kind() != "asset" {
					pr.Target = matchRoute(r.Routes, ref.Method, pr.Values)
				}
				page.Refs = append(page.Refs, pr)
			}
		}
		rt.Page = page
	}
}

// nearestTemplates returns the template names mentioned by the handler or,
// failing that, by the closest module functions it calls under method m.
// Stopping at the nearest level keeps shared error pages out of every page.
func nearestTemplates(w *authClassifier, handler *ssa.Function, m string, defines map[string]*Template) []string {
	level := []*ssa.Function{handler}
	seen := map[*ssa.Function]bool{handler: true}
	for range 5 {
		var names []string
		for _, fn := range level {
			names = union(names, mentionedTemplates(w.reachable(fn, m), defines))
		}
		if len(names) > 0 {
			return names
		}
		var next []*ssa.Function
		for _, fn := range level {
			for _, b := range w.reachable(fn, m) {
				for _, instr := range b.Instrs {
					call, ok := instr.(ssa.CallInstruction)
					if !ok {
						continue
					}
					for _, callee := range w.siteCallees(call) {
						if !seen[callee] && inModule(w.r.Module, funcPkg(callee)) {
							seen[callee] = true
							next = append(next, callee)
						}
					}
				}
			}
		}
		level = next
	}
	return nil
}

// reachableFuncs returns the module functions a handler reaches under
// method m, including itself.
func reachableFuncs(w *authClassifier, handler *ssa.Function, m string) map[*ssa.Function]bool {
	seen := map[*ssa.Function]bool{handler: true}
	queue := []*ssa.Function{handler}
	for len(queue) > 0 && len(seen) < 500 {
		fn := queue[0]
		queue = queue[1:]
		for _, b := range w.reachable(fn, m) {
			for _, instr := range b.Instrs {
				if call, ok := instr.(ssa.CallInstruction); ok {
					for _, callee := range w.siteCallees(call) {
						if !seen[callee] && inModule(w.r.Module, funcPkg(callee)) {
							seen[callee] = true
							queue = append(queue, callee)
						}
					}
				}
			}
		}
	}
	return seen
}

// includedDefines follows {{template}} uses from the rendered names.
func includedDefines(names []string, defines map[string]*Template) []string {
	var out []string
	var visit func(name string)
	visit = func(name string) {
		t := defines[name]
		if t == nil || contains(out, name) {
			return
		}
		out = append(out, name)
		for _, used := range t.uses[name] {
			visit(used)
		}
	}
	for _, n := range names {
		visit(n)
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// matchRoute finds the route a request URL goes to: the best-scoring match
// of any of values against the routes for method, where {?} in the URL
// matches any segment. It returns the route key, or "".
func matchRoute(routes []Route, method string, values []string) string {
	best, bestScore := "", 0
	for _, v := range values {
		path := urlPath(v)
		if path == "" {
			continue
		}
		for _, rt := range routes {
			if rt.Method != method && rt.Method != "ANY" && (method != "" || rt.Method != "GET") {
				continue
			}
			for _, pattern := range append([]string{rt.Pattern}, rt.Variants...) {
				if s := matchScore(pattern, path); s > bestScore {
					best, bestScore = rt.Key(), s
				}
			}
		}
	}
	return best
}

// urlPath strips scheme, host, query and fragment; external or empty URLs
// give "".
func urlPath(u string) string {
	// Mask {?} so its "?" is not taken for a query string.
	u = strings.ReplaceAll(u, Unknown, "\x00")
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.ReplaceAll(u, "\x00", Unknown)
	if strings.Contains(u, "://") || strings.HasPrefix(u, "//") {
		return ""
	}
	if strings.HasPrefix(u, Unknown+"/") {
		// An unknown prefix, typically the language: take it as one segment.
		return "/" + u
	}
	if !strings.HasPrefix(u, "/") {
		return "" // relative or fully dynamic
	}
	return u
}

// matchScore scores how well a ServeMux pattern matches a path; 0 is no
// match. Literal matches score highest, unknown URL parts lowest.
func matchScore(pattern, path string) int {
	if pattern == "/" || pattern == "/{$}" {
		if path == "/" {
			return 3
		}
		return 0
	}
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	us := strings.Split(strings.Trim(path, "/"), "/")
	score, literals := 0, 0
	for i, p := range ps {
		wildcard := strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}")
		if wildcard && strings.HasSuffix(p, "...}") {
			if i >= len(us) || literals == 0 {
				return 0
			}
			return score + 1
		}
		if i >= len(us) {
			return suffixScore(ps, us)
		}
		u := us[i]
		switch {
		case u == p:
			score += 3
			literals++
		case wildcard && u != "":
			score += 2
		case strings.Contains(u, Unknown):
			score++
		default:
			return suffixScore(ps, us)
		}
	}
	// Without a literal segment in common, a match would be a guess.
	if len(us) != len(ps) || literals == 0 {
		return suffixScore(ps, us)
	}
	return score
}

// suffixScore matches a path whose first segment is unknown and may stand
// for several pattern segments ("{?}/rename" for a base URL built at run
// time) against the end of the pattern. It scores below exact matches.
func suffixScore(ps, us []string) int {
	if len(us) < 2 || us[0] != Unknown || len(ps) < len(us) {
		return 0
	}
	tail := ps[len(ps)-len(us)+1:]
	literals := 0
	for i, u := range us[1:] {
		p := tail[i]
		wildcard := strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}")
		switch {
		case u == p:
			literals++
		case wildcard && u != "", strings.Contains(u, Unknown):
		default:
			return 0
		}
	}
	if literals == 0 {
		return 0
	}
	return literals
}
