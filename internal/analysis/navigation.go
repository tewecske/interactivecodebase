package analysis

import (
	"cmp"
	"go/constant"
	"go/token"
	"maps"
	"slices"

	"golang.org/x/tools/go/ssa"
)

// Navigation is a way to get from one route to another: a link or form on
// its page, or a redirect in its handler.
type Navigation struct {
	From, To string // route keys
	// Trigger is "link", "form", "redirect" or "hx-redirect".
	Trigger string
	// Template is the defined template holding a link or form; Via is the
	// function issuing a redirect.
	Template string
	Via      *ssa.Function
	// File and Line locate a link or form; Pos locates a redirect.
	File string
	Line int
	Pos  token.Pos
}

// buildNavigation collects navigation edges from page links and forms and
// from redirects reachable from each route's handler.
func buildNavigation(r *Result, own []*ssa.Function) {
	w := &authClassifier{r: r, scopes: r.scopes}
	ev := newEvaluator(r.CallGraph, r.Module)
	redirectors := redirectingFuncs(own)
	seen := map[[3]string]bool{}
	add := func(n Navigation) {
		key := [3]string{n.From, n.To, n.Trigger}
		if n.To == "" || seen[key] {
			return
		}
		seen[key] = true
		r.Navigation = append(r.Navigation, n)
	}
	for _, rt := range r.Routes {
		if rt.Page != nil {
			for _, ref := range rt.Page.Refs {
				trigger := ""
				switch {
				case ref.Kind() == "link":
					trigger = "link"
				case ref.Element == "form" && ref.Attr == "action":
					trigger = "form"
				default:
					continue
				}
				for _, target := range ref.Targets {
					add(Navigation{From: rt.Key(), To: target, Trigger: trigger, Template: ref.Define, File: ref.Template.File, Line: ref.Line})
				}
			}
		}
		if rt.Handler == nil {
			continue
		}
		// Sorted: map order would make edge order differ between runs.
		funcs := slices.Collect(maps.Keys(reachableFuncs(w, rt.Handler, rt.Method)))
		slices.SortFunc(funcs, func(a, b *ssa.Function) int { return cmp.Compare(a.String(), b.String()) })
		for _, fn := range funcs {
			for _, b := range w.reachable(fn, rt.Method) {
				for _, instr := range b.Instrs {
					call, ok := instr.(*ssa.Call)
					if !ok {
						continue
					}
					trigger, values := redirectTarget(ev, call, redirectors)
					if trigger == "" {
						continue
					}
					for _, target := range matchRoutes(r.Routes, "GET", values) {
						add(Navigation{From: rt.Key(), To: target, Trigger: trigger, Via: fn, Pos: call.Pos()})
					}
				}
			}
		}
	}
}

// redirectTarget reports whether call redirects and where to: a direct
// http.Redirect, an HX-Redirect/HX-Location/Location header, or a module
// function that redirects to a constant path it is given.
func redirectTarget(ev *evaluator, call *ssa.Call, redirectors map[*ssa.Function]bool) (string, []string) {
	common := call.Common()
	fn := common.StaticCallee()
	if fn == nil {
		return "", nil
	}
	switch fn.String() {
	case "net/http.Redirect":
		return "redirect", urlValues(ev, common.Args[2])
	case "(net/http.Header).Set", "(net/http.Header).Add":
		key, ok := constString(common.Args[1])
		if !ok {
			return "", nil
		}
		switch key {
		case "HX-Redirect", "HX-Location":
			return "hx-redirect", urlValues(ev, common.Args[2])
		case "Location":
			return "redirect", urlValues(ev, common.Args[2])
		}
		return "", nil
	}
	if !redirectors[fn] {
		return "", nil
	}
	paths := urlValues(ev, call)
	if len(paths) == 1 && paths[0] == Unknown {
		return "", nil
	}
	return "redirect", paths
}

// redirectingFuncs returns the module functions that issue a redirect,
// directly or through other module functions.
func redirectingFuncs(own []*ssa.Function) map[*ssa.Function]bool {
	out := map[*ssa.Function]bool{}
	for changed := true; changed; {
		changed = false
		for _, fn := range own {
			if out[fn] {
				continue
			}
			for _, b := range fn.Blocks {
				for _, instr := range b.Instrs {
					call, ok := instr.(*ssa.Call)
					if !ok {
						continue
					}
					callee := call.Common().StaticCallee()
					if callee == nil {
						continue
					}
					if callee.String() == "net/http.Redirect" || out[callee] ||
						(slices.Contains([]string{"(net/http.Header).Set", "(net/http.Header).Add"}, callee.String()) && isRedirectHeader(call)) {
						out[fn] = true
						changed = true
					}
				}
			}
		}
	}
	return out
}

func isRedirectHeader(call *ssa.Call) bool {
	key, ok := constString(call.Common().Args[1])
	return ok && (key == "HX-Redirect" || key == "HX-Location" || key == "Location")
}

func constString(v ssa.Value) (string, bool) {
	k, ok := v.(*ssa.Const)
	if !ok || k.Value == nil || k.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(k.Value), true
}
