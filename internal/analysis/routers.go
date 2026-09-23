package analysis

import (
	"cmp"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"
)

// routerDetector finds the routes registered through one kind of router.
type routerDetector interface {
	routes(r *Result, funcs []*ssa.Function) []Route
}

// routerDetectors run in this order; their routes are merged.
var routerDetectors = []routerDetector{netHTTPDetector{}, frameworkDetector{frameworks}}

// netHTTPDetector finds registrations on net/http's ServeMux.
type netHTTPDetector struct{}

func (netHTTPDetector) routes(r *Result, funcs []*ssa.Function) []Route {
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
				// A framework router mounted on the mux has its routes
				// found by the framework detector.
				if isFrameworkRouter(call.Common().Args[patternArg+1]) {
					continue
				}
				routes = append(routes, registrationRoutes(r, call, patternArg)...)
			}
		}
	}
	return routes
}

// opKind is what a framework method does to a router.
type opKind int

const (
	opRegister opKind = iota + 1 // registers a route
	opDerive                     // returns a router with a prefix, methods or middleware added
	opScope                      // calls a function with such a router (chi Route and Group)
	opMount                      // mounts another router under a prefix
	opUse                        // adds middleware to every route of the router
)

// opSpec describes a framework method. Argument indexes exclude the
// receiver; -1 means none.
type opSpec struct {
	kind opKind
	// method is the HTTP method a registration serves; with methodArg >= 0
	// it comes from that argument instead.
	method    string
	methodArg int
	// patternArg holds the path pattern, or the prefix for derive, scope
	// and mount.
	patternArg int
	// handlerArg holds the handler; with handlersArg >= 0 the handlers
	// are that variadic argument instead, the last one serving the route
	// and the others middleware (gin).
	handlerArg, handlersArg int
	// mwArg is a variadic argument of middleware.
	mwArg int
	// methodsArg is a variadic argument of HTTP methods (gorilla Methods).
	methodsArg int
	// fnArg is the function a scope op calls; subArg the mounted router.
	fnArg, subArg int
	// static marks file-serving registrations; suffix is appended to
	// their pattern.
	static bool
	suffix string
}

func spec(kind opKind) opSpec {
	return opSpec{kind: kind, methodArg: -1, patternArg: -1, handlerArg: -1, handlersArg: -1, mwArg: -1, methodsArg: -1, fnArg: -1, subArg: -1}
}

func (s opSpec) with(f func(*opSpec)) opSpec { f(&s); return s }

// framework describes a router library.
type framework struct {
	name string
	// pkg is the import path; major-version suffixes (/v5) also match.
	pkg string
	// constructors create a root router.
	constructors []string
	// ops maps a method name to what it does. Keys may be qualified by
	// the receiver type ("Route.Path") where types differ.
	ops map[string]opSpec
	// join appends a pattern to a prefix the way the library does.
	join func(prefix, pattern string) string
	// followMethods reads Methods(...) calls on the value a registration
	// returns (gorilla).
	followMethods bool
}

var httpMethods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE"}

// methodOps maps name(method) to registrations of that method.
func methodOps(name func(string) string, base opSpec) map[string]opSpec {
	out := map[string]opSpec{}
	for _, m := range httpMethods {
		out[name(m)] = base.with(func(s *opSpec) { s.method = m })
	}
	return out
}

func titleCase(m string) string { return m[:1] + strings.ToLower(m[1:]) }

var frameworks = []*framework{chiFramework(), ginFramework(), echoFramework(), gorillaFramework()}

func chiFramework() *framework {
	reg := spec(opRegister).with(func(s *opSpec) { s.patternArg, s.handlerArg = 0, 1 })
	ops := methodOps(titleCase, reg)
	ops["Handle"] = reg.with(func(s *opSpec) { s.method = "ANY" })
	ops["HandleFunc"] = ops["Handle"]
	ops["Method"] = reg.with(func(s *opSpec) { s.methodArg, s.patternArg, s.handlerArg = 0, 1, 2 })
	ops["MethodFunc"] = ops["Method"]
	ops["Route"] = spec(opScope).with(func(s *opSpec) { s.patternArg, s.fnArg = 0, 1 })
	ops["Group"] = spec(opScope).with(func(s *opSpec) { s.fnArg = 0 })
	ops["With"] = spec(opDerive).with(func(s *opSpec) { s.mwArg = 0 })
	ops["Use"] = spec(opUse).with(func(s *opSpec) { s.mwArg = 0 })
	ops["Mount"] = spec(opMount).with(func(s *opSpec) { s.patternArg, s.subArg = 0, 1 })
	return &framework{
		name: "chi", pkg: "github.com/go-chi/chi", constructors: []string{"NewRouter", "NewMux"}, ops: ops,
		// A subrouter's "/" serves the prefix itself.
		join: func(prefix, pattern string) string {
			if pattern == "/" && prefix != "" {
				return prefix
			}
			return strings.TrimSuffix(prefix, "/") + pattern
		},
	}
}

func ginFramework() *framework {
	reg := spec(opRegister).with(func(s *opSpec) { s.patternArg, s.handlersArg = 0, 1 })
	ops := methodOps(func(m string) string { return m }, reg)
	delete(ops, "CONNECT")
	delete(ops, "TRACE")
	ops["Any"] = reg.with(func(s *opSpec) { s.method = "ANY" })
	ops["Handle"] = reg.with(func(s *opSpec) { s.methodArg, s.patternArg, s.handlersArg = 0, 1, 2 })
	static := spec(opRegister).with(func(s *opSpec) { s.method, s.patternArg, s.static = "GET", 0, true })
	ops["Static"] = static.with(func(s *opSpec) { s.suffix = "/*filepath" })
	ops["StaticFS"] = ops["Static"]
	ops["StaticFile"] = static
	ops["StaticFileFS"] = static
	ops["Group"] = spec(opDerive).with(func(s *opSpec) { s.patternArg, s.mwArg = 0, 1 })
	ops["Use"] = spec(opUse).with(func(s *opSpec) { s.mwArg = 0 })
	return &framework{
		name: "gin", pkg: "github.com/gin-gonic/gin", constructors: []string{"New", "Default"}, ops: ops,
		// gin joins like path.Join but keeps a trailing slash.
		join: func(prefix, pattern string) string {
			if pattern == "" {
				return cmp.Or(prefix, "/")
			}
			out := strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(pattern, "/")
			return out
		},
	}
}

func echoFramework() *framework {
	reg := spec(opRegister).with(func(s *opSpec) { s.patternArg, s.handlerArg, s.mwArg = 0, 1, 2 })
	ops := methodOps(func(m string) string { return m }, reg)
	ops["Any"] = reg.with(func(s *opSpec) { s.method = "ANY" })
	ops["Add"] = reg.with(func(s *opSpec) { s.methodArg, s.patternArg, s.handlerArg, s.mwArg = 0, 1, 2, 3 })
	static := spec(opRegister).with(func(s *opSpec) { s.method, s.patternArg, s.static = "GET", 0, true })
	ops["Static"] = static.with(func(s *opSpec) { s.suffix = "/*" })
	ops["StaticFS"] = ops["Static"]
	ops["File"] = static
	ops["FileFS"] = static
	ops["Group"] = spec(opDerive).with(func(s *opSpec) { s.patternArg, s.mwArg = 0, 1 })
	ops["Use"] = spec(opUse).with(func(s *opSpec) { s.mwArg = 0 })
	return &framework{
		name: "echo", pkg: "github.com/labstack/echo", constructors: []string{"New"}, ops: ops,
		join: func(prefix, pattern string) string { return prefix + pattern },
	}
}

func gorillaFramework() *framework {
	reg := spec(opRegister).with(func(s *opSpec) { s.method, s.patternArg, s.handlerArg = "ANY", 0, 1 })
	onRoute := spec(opRegister).with(func(s *opSpec) { s.method, s.handlerArg = "ANY", 0 })
	prefix := spec(opDerive).with(func(s *opSpec) { s.patternArg = 0 })
	methods := spec(opDerive).with(func(s *opSpec) { s.methodsArg = 0 })
	pass := spec(opDerive)
	return &framework{
		name: "gorilla/mux", pkg: "github.com/gorilla/mux", constructors: []string{"NewRouter"},
		ops: map[string]opSpec{
			"Router.Handle": reg, "Router.HandleFunc": reg,
			"Route.Handler": onRoute, "Route.HandlerFunc": onRoute,
			"Router.PathPrefix": prefix, "Router.Path": prefix, "Route.PathPrefix": prefix, "Route.Path": prefix,
			"Router.Methods": methods, "Route.Methods": methods,
			"Route.Subrouter": pass, "Router.NewRoute": pass, "Route.Name": pass, "Route.Host": pass,
			"Router.Host": pass, "Route.Schemes": pass, "Router.Schemes": pass, "Route.Headers": pass,
			"Router.Headers": pass, "Route.Queries": pass, "Router.Queries": pass,
			"Router.Use": spec(opUse).with(func(s *opSpec) { s.mwArg = 0 }),
		},
		join:          func(prefix, pattern string) string { return prefix + pattern },
		followMethods: true,
	}
}

// inPkg reports whether path is the framework package or a major version
// of it.
func (f *framework) inPkg(path string) bool {
	rest, ok := strings.CutPrefix(path, f.pkg)
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	v, ok := strings.CutPrefix(rest, "/v")
	return ok && v != "" && strings.Trim(v, "0123456789") == ""
}

// frameworkCall identifies a call as an operation of a known framework.
func frameworkCall(call ssa.CallInstruction) (fw *framework, op opSpec, recv ssa.Value, args []ssa.Value, ok bool) {
	common := call.Common()
	var fn *types.Func
	if common.IsInvoke() {
		fn, recv, args = common.Method, common.Value, common.Args
	} else if callee := common.StaticCallee(); callee != nil {
		fn, _ = callee.Object().(*types.Func)
		if fn == nil || fn.Signature().Recv() == nil || len(common.Args) == 0 {
			return nil, opSpec{}, nil, nil, false
		}
		recv, args = common.Args[0], common.Args[1:]
	}
	if fn == nil || fn.Pkg() == nil {
		return nil, opSpec{}, nil, nil, false
	}
	for _, f := range frameworks {
		if !f.inPkg(fn.Pkg().Path()) {
			continue
		}
		if op, ok := f.ops[recvName(fn)+"."+fn.Name()]; ok {
			return f, op, recv, args, true
		}
		if op, ok := f.ops[fn.Name()]; ok {
			return f, op, recv, args, true
		}
	}
	return nil, opSpec{}, nil, nil, false
}

// recvName is the name of fn's receiver type, without pointer.
func recvName(fn *types.Func) string {
	t := fn.Signature().Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := types.Unalias(t).(*types.Named); ok {
		return n.Obj().Name()
	}
	return ""
}

// constructorOf returns the framework whose root router call creates.
func constructorOf(call *ssa.Call) *framework {
	fn := call.Common().StaticCallee()
	if fn == nil || fn.Pkg == nil || fn.Signature.Recv() != nil {
		return nil
	}
	for _, f := range frameworks {
		if f.inPkg(fn.Pkg.Pkg.Path()) && slices.Contains(f.constructors, fn.Name()) {
			return f
		}
	}
	return nil
}

// isFrameworkRouter reports whether v is (a conversion of) a framework
// router created in place or returned by a module function.
func isFrameworkRouter(v ssa.Value) bool {
	for {
		switch x := v.(type) {
		case *ssa.MakeInterface:
			v = x.X
			continue
		case *ssa.ChangeType:
			v = x.X
			continue
		}
		break
	}
	t := v.Type()
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return slices.ContainsFunc(frameworks, func(f *framework) bool { return f.inPkg(named.Obj().Pkg().Path()) })
}

// routerScope is one way a router value can be reached: the prefix and
// methods its routes get, the middleware wrapping them, and the values
// (routers, groups, closure parameters) from the root down to it.
type routerScope struct {
	fw      *framework
	prefix  string
	methods []string
	chain   []ssa.Value
	// middleware[i] is added where chain[i] is derived (With, Group).
	middleware [][]*ssa.Function
}

func (s routerScope) derive(prefix string, methods []string, mw []*ssa.Function, id ssa.Value) routerScope {
	out := routerScope{
		fw:         s.fw,
		prefix:     s.fw.join(s.prefix, prefix),
		methods:    s.methods,
		chain:      append(slices.Clip(s.chain), id),
		middleware: append(slices.Clip(s.middleware), mw),
	}
	if prefix == "" {
		out.prefix = s.prefix
	}
	if len(methods) > 0 {
		out.methods = methods
	}
	return out
}

// frameworkOp is a framework call found in the module.
type frameworkOp struct {
	fw   *framework
	spec opSpec
	call ssa.CallInstruction
	recv ssa.Value
	args []ssa.Value
}

// frameworkDetector finds routes registered through router libraries by
// tracing each registration's receiver back to the router it was created
// as, collecting prefixes, methods and middleware on the way.
type frameworkDetector struct{ frameworks []*framework }

type routerTracer struct {
	r      *Result
	ev     *evaluator
	ops    []frameworkOp
	scoped map[*ssa.Function][]frameworkOp // scope-op callbacks
	memo   map[ssa.Value][]routerScope
	busy   map[ssa.Value]bool
	// stores maps a struct field or package variable to the values
	// stored in it, for routers kept in a server struct.
	fieldStores  map[fieldKey][]ssa.Value
	globalStores map[*ssa.Global][]ssa.Value
}

type fieldKey struct {
	t     types.Type
	field int
}

func (d frameworkDetector) routes(r *Result, funcs []*ssa.Function) []Route {
	t := &routerTracer{
		r: r, ev: newEvaluator(r.CallGraph, r.Module),
		scoped: map[*ssa.Function][]frameworkOp{}, memo: map[ssa.Value][]routerScope{}, busy: map[ssa.Value]bool{},
		fieldStores: map[fieldKey][]ssa.Value{}, globalStores: map[*ssa.Global][]ssa.Value{},
	}
	for _, fn := range funcs {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				switch instr := instr.(type) {
				case ssa.CallInstruction:
					fw, op, recv, args, ok := frameworkCall(instr)
					if !ok {
						continue
					}
					o := frameworkOp{fw: fw, spec: op, call: instr, recv: recv, args: args}
					t.ops = append(t.ops, o)
					if op.kind == opScope && op.fnArg < len(args) {
						if cb := funcValue(args[op.fnArg]); cb != nil {
							t.scoped[cb] = append(t.scoped[cb], o)
						}
					}
				case *ssa.Store:
					switch addr := instr.Addr.(type) {
					case *ssa.FieldAddr:
						k := fieldKey{addr.X.Type().Underlying().(*types.Pointer).Elem(), addr.Field}
						t.fieldStores[k] = append(t.fieldStores[k], instr.Val)
					case *ssa.Global:
						t.globalStores[addr] = append(t.globalStores[addr], instr.Val)
					}
				}
			}
		}
	}
	if len(t.ops) == 0 {
		return nil
	}
	var routes []Route
	for _, o := range t.ops {
		if o.spec.kind == opRegister {
			routes = append(routes, t.register(o)...)
		}
	}
	return routes
}

// funcValue returns the function a callback argument refers to.
func funcValue(v ssa.Value) *ssa.Function {
	switch v := v.(type) {
	case *ssa.Function:
		return v
	case *ssa.MakeClosure:
		return v.Fn.(*ssa.Function)
	case *ssa.ChangeType:
		return funcValue(v.X)
	case *ssa.MakeInterface:
		return funcValue(v.X)
	}
	return nil
}

// register turns a registration into routes, one per scope, pattern and
// method.
func (t *routerTracer) register(o frameworkOp) []Route {
	scopes := t.scopes(o.recv, 0)
	patterns := []string{""}
	if o.spec.patternArg >= 0 && o.spec.patternArg < len(o.args) {
		patterns = t.ev.strings(o.args[o.spec.patternArg])
	}
	methods := []string{o.spec.method}
	if o.spec.methodArg >= 0 && o.spec.methodArg < len(o.args) {
		methods = t.ev.strings(o.args[o.spec.methodArg])
	}
	if o.fw.followMethods {
		if ms := followMethods(t.ev, o.call); len(ms) > 0 {
			methods = ms
		}
	}
	h, routeMW := t.handler(o)
	var routes []Route
	for _, s := range scopes {
		ms := methods
		if len(s.methods) > 0 && slices.Equal(methods, []string{"ANY"}) {
			ms = s.methods
		}
		mw := append(t.scopeMiddleware(s), routeMW...)
		for _, p := range patterns {
			pattern := s.prefix
			if p != "" {
				pattern = s.fw.join(s.prefix, p)
			}
			pattern += o.spec.suffix
			for _, m := range ms {
				routes = append(routes, Route{
					Method: strings.ToUpper(m), Pattern: cmp.Or(pattern, "/"),
					Handler: h.handler, Middleware: append(mw, h.middleware...),
					Static: h.static || o.spec.static, Pos: o.call.Pos(),
				})
			}
		}
	}
	return routes
}

// handler resolves a registration's handler and the middleware passed with
// it.
func (t *routerTracer) handler(o frameworkOp) (resolvedHandler, []*ssa.Function) {
	var mw []*ssa.Function
	var h resolvedHandler
	switch {
	case o.spec.handlersArg >= 0 && o.spec.handlersArg < len(o.args):
		hs := sliceValues(o.args[o.spec.handlersArg])
		if len(hs) > 0 {
			h = resolveHandler(hs[len(hs)-1], 0)
			for _, v := range hs[:len(hs)-1] {
				mw = append(mw, middlewareFuncs(v)...)
			}
		}
	case o.spec.handlerArg >= 0 && o.spec.handlerArg < len(o.args):
		h = resolveHandler(o.args[o.spec.handlerArg], 0)
	}
	if o.spec.mwArg >= 0 && o.spec.mwArg < len(o.args) {
		for _, v := range sliceValues(o.args[o.spec.mwArg]) {
			mw = append(mw, middlewareFuncs(v)...)
		}
	}
	return h, mw
}

// scopeMiddleware lists the middleware applying to a scope, outermost
// first: at each step from the root, what the step was derived with and
// then what was added with Use.
func (t *routerTracer) scopeMiddleware(s routerScope) []*ssa.Function {
	var out []*ssa.Function
	for i, id := range s.chain {
		out = append(out, s.middleware[i]...)
		for _, o := range t.ops {
			if o.spec.kind != opUse || o.fw != s.fw {
				continue
			}
			if !slices.ContainsFunc(t.scopes(o.recv, 0), func(u routerScope) bool { return len(u.chain) > 0 && u.chain[len(u.chain)-1] == id }) {
				continue
			}
			for _, v := range sliceValues(o.args[o.spec.mwArg]) {
				out = append(out, middlewareFuncs(v)...)
			}
		}
	}
	return slices.Clip(out)
}

// scopes traces a router value back to the roots it can be derived from.
func (t *routerTracer) scopes(v ssa.Value, depth int) []routerScope {
	if s, ok := t.memo[v]; ok {
		return s
	}
	if depth > 2*maxEvalDepth || t.busy[v] {
		return nil
	}
	t.busy[v] = true
	out := t.trace(v, depth)
	delete(t.busy, v)
	if len(out) > maxEvalValues {
		out = out[:maxEvalValues]
	}
	t.memo[v] = out
	return out
}

func (t *routerTracer) trace(v ssa.Value, depth int) []routerScope {
	switch v := v.(type) {
	case *ssa.MakeInterface:
		return t.scopes(v.X, depth+1)
	case *ssa.ChangeType:
		return t.scopes(v.X, depth+1)
	case *ssa.ChangeInterface:
		return t.scopes(v.X, depth+1)
	case *ssa.TypeAssert:
		return t.scopes(v.X, depth+1)
	case *ssa.FieldAddr:
		// &engine.RouterGroup: a method promoted from an embedded router.
		return t.scopes(v.X, depth+1)
	case *ssa.Phi:
		var out []routerScope
		for _, e := range v.Edges {
			out = append(out, t.scopes(e, depth+1)...)
		}
		return out
	case *ssa.UnOp:
		if v.Op != token.MUL {
			return nil
		}
		var stored []ssa.Value
		switch addr := v.X.(type) {
		case *ssa.FieldAddr:
			stored = t.fieldStores[fieldKey{addr.X.Type().Underlying().(*types.Pointer).Elem(), addr.Field}]
		case *ssa.Global:
			stored = t.globalStores[addr]
		case *ssa.Alloc:
			for _, ref := range *addr.Referrers() {
				if st, ok := ref.(*ssa.Store); ok && st.Addr == addr {
					stored = append(stored, st.Val)
				}
			}
		}
		var out []routerScope
		for _, s := range stored {
			out = append(out, t.scopes(s, depth+1)...)
		}
		return out
	case *ssa.FreeVar:
		return t.freeVar(v, depth)
	case *ssa.Parameter:
		return t.param(v, depth)
	case *ssa.Call:
		return t.callResult(v, depth)
	}
	return nil
}

// callResult traces a router returned by a call: a root, a derived router,
// or a module function returning one.
func (t *routerTracer) callResult(call *ssa.Call, depth int) []routerScope {
	if fw := constructorOf(call); fw != nil {
		return t.root(fw, call, depth)
	}
	if fw, op, recv, args, ok := frameworkCall(call); ok {
		switch op.kind {
		case opDerive, opScope: // chi's Route also returns the subrouter
			return t.derived(fw, op, recv, args, call, depth)
		}
		return nil
	}
	fn := call.Common().StaticCallee()
	if fn == nil || !t.ev.own(fn) {
		return nil
	}
	var out []routerScope
	for _, ret := range returns(fn) {
		out = append(out, t.scopes(ret, depth+1)...)
	}
	return out
}

// root is a router created by a constructor: at the top level, or under
// the prefixes it is mounted at.
func (t *routerTracer) root(fw *framework, call *ssa.Call, depth int) []routerScope {
	var out []routerScope
	for _, o := range t.ops {
		if o.spec.kind != opMount || o.spec.subArg >= len(o.args) {
			continue
		}
		if !t.createdBy(o.args[o.spec.subArg], call, depth) {
			continue
		}
		prefixes := t.ev.strings(o.args[o.spec.patternArg])
		for _, parent := range t.scopes(o.recv, depth+1) {
			for _, p := range prefixes {
				out = append(out, parent.derive(p, nil, nil, call))
			}
		}
	}
	if len(out) == 0 {
		out = []routerScope{{fw: fw, chain: []ssa.Value{call}, middleware: [][]*ssa.Function{nil}}}
	}
	return out
}

// createdBy reports whether the router v traces back to the constructor
// call root, without resolving mounts (which would recurse).
func (t *routerTracer) createdBy(v ssa.Value, root *ssa.Call, depth int) bool {
	if depth > 2*maxEvalDepth {
		return false
	}
	switch v := v.(type) {
	case *ssa.Call:
		if v == root {
			return true
		}
		if fn := v.Common().StaticCallee(); fn != nil && t.ev.own(fn) {
			return slices.ContainsFunc(returns(fn), func(r ssa.Value) bool { return t.createdBy(r, root, depth+1) })
		}
	case *ssa.MakeInterface:
		return t.createdBy(v.X, root, depth+1)
	case *ssa.ChangeType:
		return t.createdBy(v.X, root, depth+1)
	case *ssa.Phi:
		return slices.ContainsFunc(v.Edges, func(e ssa.Value) bool { return t.createdBy(e, root, depth+1) })
	}
	return false
}

// derived applies a derive or scope op to its receiver's scopes.
func (t *routerTracer) derived(fw *framework, op opSpec, recv ssa.Value, args []ssa.Value, id ssa.Value, depth int) []routerScope {
	prefixes := []string{""}
	if op.patternArg >= 0 && op.patternArg < len(args) {
		prefixes = t.ev.strings(args[op.patternArg])
	}
	var methods []string
	if op.methodsArg >= 0 && op.methodsArg < len(args) {
		for _, v := range sliceValues(args[op.methodsArg]) {
			methods = append(methods, t.ev.strings(v)...)
		}
	}
	var mw []*ssa.Function
	if op.mwArg >= 0 && op.mwArg < len(args) {
		for _, v := range sliceValues(args[op.mwArg]) {
			mw = append(mw, middlewareFuncs(v)...)
		}
	}
	var out []routerScope
	for _, s := range t.scopes(recv, depth+1) {
		if s.fw != fw {
			continue
		}
		for _, p := range prefixes {
			out = append(out, s.derive(p, methods, mw, id))
		}
	}
	return out
}

// param traces a parameter: the router a scope op passes to its callback,
// or the arguments of the function's callers.
func (t *routerTracer) param(p *ssa.Parameter, depth int) []routerScope {
	fn := p.Parent()
	var out []routerScope
	if uses := t.scoped[fn]; len(uses) > 0 && len(fn.Params) > 0 && fn.Params[0] == p {
		for _, o := range uses {
			out = append(out, t.derived(o.fw, o.spec, o.recv, o.args, p, depth)...)
		}
		return out
	}
	idx := slices.Index(fn.Params, p)
	node := t.r.CallGraph.Nodes[fn]
	if idx < 0 || node == nil || !t.ev.own(fn) {
		return nil
	}
	// Call-graph edges come in map order; sort so results are stable.
	ins := slices.Clone(node.In)
	slices.SortFunc(ins, func(x, y *callgraph.Edge) int {
		return cmp.Or(cmp.Compare(x.Caller.Func.String(), y.Caller.Func.String()), cmp.Compare(edgePos(x), edgePos(y)))
	})
	for _, in := range ins {
		if in.Site == nil {
			continue
		}
		common := in.Site.Common()
		argIdx := idx
		if common.IsInvoke() {
			argIdx--
		}
		if argIdx >= 0 && argIdx < len(common.Args) {
			out = append(out, t.scopes(common.Args[argIdx], depth+1)...)
		}
	}
	return out
}

// freeVar traces a variable captured by a closure to the value bound where
// the closure is made.
func (t *routerTracer) freeVar(fv *ssa.FreeVar, depth int) []routerScope {
	fn := fv.Parent()
	idx := slices.Index(fn.FreeVars, fv)
	if idx < 0 || fn.Parent() == nil {
		return nil
	}
	var out []routerScope
	for _, b := range fn.Parent().Blocks {
		for _, instr := range b.Instrs {
			if mc, ok := instr.(*ssa.MakeClosure); ok && mc.Fn == fn && idx < len(mc.Bindings) {
				out = append(out, t.scopes(mc.Bindings[idx], depth+1)...)
			}
		}
	}
	return out
}

// followMethods reads the HTTP methods set on the route a registration
// returns: r.HandleFunc(p, h).Methods("GET").Name("x").
func followMethods(ev *evaluator, call ssa.CallInstruction) []string {
	c, ok := call.(*ssa.Call)
	if !ok {
		return nil
	}
	var v ssa.Value = c
	for v != nil && v.Referrers() != nil {
		var next ssa.Value
		for _, ref := range *v.Referrers() {
			c, ok := ref.(*ssa.Call)
			if !ok {
				continue
			}
			_, op, recv, args, ok := frameworkCall(c)
			if !ok || recv != v || op.kind != opDerive {
				continue
			}
			if op.methodsArg >= 0 && op.methodsArg < len(args) {
				var out []string
				for _, m := range sliceValues(args[op.methodsArg]) {
					out = append(out, ev.strings(m)...)
				}
				return out
			}
			next = c
		}
		v = next
	}
	return nil
}

// sliceValues returns the elements stored into a variadic argument's
// backing array, in index order.
func sliceValues(v ssa.Value) []ssa.Value {
	var out []ssa.Value
	for _, alloc := range sliceAllocs(v, 0) {
		byIndex := map[int64]ssa.Value{}
		for _, ref := range *alloc.Referrers() {
			ia, ok := ref.(*ssa.IndexAddr)
			if !ok {
				continue
			}
			c, ok := ia.Index.(*ssa.Const)
			if !ok {
				continue
			}
			i, _ := constant.Int64Val(c.Value)
			for _, r := range *ia.Referrers() {
				if st, ok := r.(*ssa.Store); ok && st.Addr == ia {
					byIndex[i] = st.Val
				}
			}
		}
		for i := range int64(len(byIndex)) {
			if v, ok := byIndex[i]; ok {
				out = append(out, v)
			}
		}
	}
	return out
}

// middlewareFuncs resolves a middleware value to the module function
// behind it: a function, a method value, or the factory that built it.
func middlewareFuncs(v ssa.Value) []*ssa.Function {
	switch v := v.(type) {
	case *ssa.Function:
		return []*ssa.Function{v}
	case *ssa.MakeClosure:
		return []*ssa.Function{closureTarget(v.Fn.(*ssa.Function))}
	case *ssa.MakeInterface:
		return middlewareFuncs(v.X)
	case *ssa.ChangeType:
		return middlewareFuncs(v.X)
	case *ssa.Convert:
		return middlewareFuncs(v.X)
	case *ssa.Call:
		if fn := v.Common().StaticCallee(); fn != nil {
			return []*ssa.Function{fn}
		}
	}
	return nil
}
