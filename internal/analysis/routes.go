package analysis

import (
	"cmp"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// Route is one method + pattern registered on a net/http ServeMux.
type Route struct {
	// Method is the HTTP method, or "ANY" when the pattern has none.
	Method string
	// Pattern is the path pattern. Registrations that differ only in a
	// language-code first segment collapse into "/{lang}/..."; parts that
	// cannot be determined statically are Unknown ("{?}").
	Pattern string
	// Variants are the concrete patterns a collapsed route stands for.
	Variants []string
	// Handler is the function serving the route, nil if unresolved.
	Handler *ssa.Function
	// Middleware wrapping only this route, outermost first.
	Middleware []*ssa.Function
	// MuxMiddleware wraps the whole mux the route is registered on.
	MuxMiddleware []*ssa.Function
	// Static routes serve files (http.FileServer).
	Static bool
	// Pos is the registration call.
	Pos token.Pos
}

// Key identifies the route as "METHOD pattern".
func (r Route) Key() string { return r.Method + " " + r.Pattern }

// Conditional reports whether part of the pattern depends on runtime
// configuration.
func (r Route) Conditional() bool { return strings.Contains(r.Pattern, Unknown) }

// HandlerName is the handler's go/ssa name, or Unknown.
func (r Route) HandlerName() string {
	if r.Handler == nil {
		return Unknown
	}
	return origin(r.Handler).String()
}

// registration methods and functions of net/http, by go/ssa name. The value
// is the index of the pattern argument.
var registrations = map[string]int{
	"(*net/http.ServeMux).Handle":     1,
	"(*net/http.ServeMux).HandleFunc": 1,
	"net/http.Handle":                 0,
	"net/http.HandleFunc":             0,
}

// staticHandlers are standard-library constructors of file-serving handlers.
var staticHandlers = []string{"net/http.FileServer", "net/http.FileServerFS"}

// langSegment matches a first path segment that looks like a language code.
var langSegment = regexp.MustCompile(`^[a-z]{2}(-[A-Za-z]{2,4})?$`)

// discoverRoutes finds every route registration in the module.
func discoverRoutes(r *Result, funcs []*ssa.Function) []Route {
	var routes []Route
	for _, fn := range funcs {
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
				patternArg, ok := registrations[callee.String()]
				if !ok {
					continue
				}
				routes = append(routes, registrationRoutes(r, call, patternArg)...)
			}
		}
	}
	slices.SortFunc(routes, func(a, b Route) int {
		return cmp.Or(cmp.Compare(a.Pos, b.Pos), cmp.Compare(a.Key(), b.Key()))
	})
	return mergeDuplicates(routes)
}

// registrationRoutes evaluates one Handle/HandleFunc call.
func registrationRoutes(r *Result, call *ssa.Call, patternArg int) []Route {
	args := call.Common().Args
	patterns := newEvaluator(r.CallGraph).strings(args[patternArg])
	h := resolveHandler(args[patternArg+1], 0)
	var muxMW []*ssa.Function
	if patternArg == 1 {
		muxMW = muxMiddleware(args[0])
	}

	// Group the concrete patterns by method and by the path with a
	// language-like first segment replaced, so loops over languages
	// collapse into one route.
	type group struct {
		method, pattern string
		variants        []string
	}
	var groups []*group
	for _, p := range patterns {
		method, path := splitPattern(p)
		collapsed, isLang := collapseLang(path)
		if !isLang {
			collapsed = path
		}
		i := slices.IndexFunc(groups, func(g *group) bool { return g.method == method && g.pattern == collapsed })
		if i < 0 {
			groups = append(groups, &group{method: method, pattern: collapsed})
			i = len(groups) - 1
		}
		if isLang {
			groups[i].variants = append(groups[i].variants, path)
		}
	}
	var out []Route
	for _, g := range groups {
		route := Route{
			Method:        g.method,
			Pattern:       g.pattern,
			Handler:       h.handler,
			Middleware:    h.middleware,
			MuxMiddleware: muxMW,
			Static:        h.static,
			Pos:           call.Pos(),
		}
		// A single language-looking segment is just a path, e.g. "/go".
		if len(g.variants) > 1 {
			route.Variants = g.variants
		} else if len(g.variants) == 1 {
			route.Pattern = g.variants[0]
		}
		out = append(out, route)
	}
	return out
}

// splitPattern separates "GET /path" into method and path.
func splitPattern(p string) (method, path string) {
	if m, rest, ok := strings.Cut(p, " "); ok && !strings.HasPrefix(m, "/") {
		return m, strings.TrimLeft(rest, " ")
	}
	return "ANY", p
}

// collapseLang replaces a language-code first segment with {lang}.
func collapseLang(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/")
	if !ok {
		return path, false
	}
	seg, tail, _ := strings.Cut(rest, "/")
	if !langSegment.MatchString(seg) {
		return path, false
	}
	if tail == "" && !strings.HasSuffix(rest, "/") {
		return "/{lang}", true
	}
	return "/{lang}/" + tail, true
}

// mergeDuplicates folds routes with the same key (e.g. registered from two
// call sites) into the first one.
func mergeDuplicates(routes []Route) []Route {
	var out []Route
	seen := map[string]int{}
	for _, rt := range routes {
		if i, ok := seen[rt.Key()]; ok {
			out[i].Variants = union(out[i].Variants, rt.Variants)
			continue
		}
		seen[rt.Key()] = len(out)
		out = append(out, rt)
	}
	return out
}

type resolvedHandler struct {
	handler    *ssa.Function
	middleware []*ssa.Function
	static     bool
}

// resolveHandler finds the function behind a handler value: a function or
// method value, a closure returned by a factory, or a handler wrapped in
// middleware.
func resolveHandler(v ssa.Value, depth int) resolvedHandler {
	if depth > maxEvalDepth {
		return resolvedHandler{}
	}
	switch v := v.(type) {
	case *ssa.Function:
		return resolvedHandler{handler: v}
	case *ssa.MakeClosure:
		return resolvedHandler{handler: closureTarget(v.Fn.(*ssa.Function))}
	case *ssa.MakeInterface:
		return resolveHandler(v.X, depth+1)
	case *ssa.ChangeType:
		return resolveHandler(v.X, depth+1)
	case *ssa.Convert:
		return resolveHandler(v.X, depth+1)
	case *ssa.Call:
		return resolveFactory(v, depth+1)
	}
	return resolvedHandler{}
}

// resolveFactory resolves a call that produces a handler: middleware taking
// a handler argument, or a factory returning a closure.
func resolveFactory(call *ssa.Call, depth int) resolvedHandler {
	fn := call.Common().StaticCallee()
	if fn == nil {
		return resolvedHandler{}
	}
	if slices.Contains(staticHandlers, fn.String()) {
		return resolvedHandler{handler: fn, static: true}
	}
	// Middleware: an argument that is itself a handler.
	for _, arg := range call.Common().Args {
		if !isHandlerType(arg.Type()) {
			continue
		}
		inner := resolveHandler(arg, depth+1)
		if inner.handler == nil {
			continue
		}
		if fn.Pkg != nil && fn.Pkg.Pkg.Path() != "net/http" {
			inner.middleware = append([]*ssa.Function{fn}, inner.middleware...)
		}
		return inner
	}
	// Factory: returns a closure or function value.
	for _, ret := range returns(fn) {
		if h := resolveHandler(ret, depth+1); h.handler != nil {
			return h
		}
	}
	return resolvedHandler{}
}

// closureTarget returns the method behind a bound method value
// (auth.signIn), or the single function a thin closure forwards the request
// to (func(w, r) { h.oauthCallback(w, r, name) }); otherwise the closure.
func closureTarget(fn *ssa.Function) *ssa.Function {
	var calls []*ssa.CallCommon
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			if c, ok := instr.(ssa.CallInstruction); ok {
				calls = append(calls, c.Common())
			}
		}
	}
	if len(calls) != 1 {
		return fn
	}
	callee := calls[0].StaticCallee()
	if callee == nil {
		return fn
	}
	if fn.Synthetic != "" {
		return callee // bound method wrapper
	}
	if len(fn.Params) == 2 && slices.Contains(calls[0].Args, ssa.Value(fn.Params[0])) &&
		slices.Contains(calls[0].Args, ssa.Value(fn.Params[1])) {
		return callee
	}
	return fn
}

// muxMiddleware lists module functions the mux is passed to that return a
// handler, e.g. withRequestID(mux).
func muxMiddleware(mux ssa.Value) []*ssa.Function {
	var out []*ssa.Function
	var visit func(v ssa.Value, depth int)
	visit = func(v ssa.Value, depth int) {
		refs := v.Referrers()
		if refs == nil || depth > 4 {
			return
		}
		for _, ref := range *refs {
			switch ref := ref.(type) {
			case *ssa.MakeInterface:
				visit(ref, depth+1)
			case *ssa.ChangeType:
				visit(ref, depth+1)
			case *ssa.Call:
				fn := ref.Common().StaticCallee()
				if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg.Path() == "net/http" {
					continue
				}
				if res := fn.Signature.Results(); res.Len() > 0 && isHandlerType(res.At(0).Type()) && !slices.Contains(out, fn) {
					out = append(out, fn)
				}
			}
		}
	}
	visit(mux, 0)
	return out
}

// isHandlerType reports whether t is http.Handler, http.HandlerFunc, a
// func(http.ResponseWriter, *http.Request), or a type implementing
// http.Handler such as *http.ServeMux.
func isHandlerType(t types.Type) bool {
	if named, ok := types.Unalias(t).(*types.Named); ok {
		obj := named.Obj()
		if obj.Pkg() != nil && obj.Pkg().Path() == "net/http" {
			switch obj.Name() {
			case "Handler", "HandlerFunc", "ServeMux":
				return true
			}
		}
	}
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok {
		return isHandlerType(ptr.Elem())
	}
	sig, ok := t.Underlying().(*types.Signature)
	if !ok || sig.Params().Len() != 2 || sig.Results().Len() != 0 {
		return false
	}
	return isNetHTTP(sig.Params().At(0).Type(), "ResponseWriter") && isNetHTTP(sig.Params().At(1).Type(), "Request")
}

func isNetHTTP(t types.Type, name string) bool {
	if ptr, ok := types.Unalias(t).(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "net/http" && named.Obj().Name() == name
}
