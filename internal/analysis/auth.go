package analysis

import (
	"go/constant"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/go/ssa"
)

// Access levels of a route.
const (
	AccessPublic        = "public"
	AccessAuthenticated = "authenticated"
	AccessAdmin         = "admin"
	AccessGuest         = "guest"
)

// accessRank orders access levels from weakest to strongest.
var accessRank = map[string]int{AccessPublic: 0, AccessAuthenticated: 1, AccessGuest: 2, AccessAdmin: 3}

// GuardUse is evidence for a route's access level: a call to a guard.
type GuardUse struct {
	Guard *ssa.Function
	// Pos is the call site in the handler or middleware.
	Pos token.Pos
	// Role is the access the guard enforces here, or "" when the route
	// only consults it (optional authentication).
	Role string
}

var (
	// authNameRE matches functions treated as authentication primitives.
	authNameRE = regexp.MustCompile(`(?i)authenticat`)
	// denialNameRE matches calls that answer a rejected request.
	denialNameRE = regexp.MustCompile(`(?i)redirect|error|deny|denied|forbid|unauthori|sign.?in|login|reject|abort|notfound|alert`)
	adminFieldRE = regexp.MustCompile(`(?i)admin`)
	guestFieldRE = regexp.MustCompile(`(?i)guest`)
)

// authClassifier decides the access level of routes.
type authClassifier struct {
	r      *Result
	scopes map[*ssa.Function]*methodScope
	// guards maps guard functions to the role they enforce on success.
	guards map[*ssa.Function]string
	// work are module functions that can reach a sink.
	work map[*ssa.Function]bool
}

func newAuthClassifier(r *Result, own []*ssa.Function, scopes map[*ssa.Function]*methodScope, extra []string) *authClassifier {
	c := &authClassifier{r: r, scopes: scopes, guards: map[*ssa.Function]string{}}
	c.findGuards(own, extra)
	c.work = reachesSink(r)
	return c
}

// findGuards seeds guards with authentication primitives, then adds every
// request-taking function returning bool or error that calls a guard.
func (c *authClassifier) findGuards(own []*ssa.Function, extra []string) {
	for _, fn := range own {
		if (authNameRE.MatchString(fn.Name()) && requestParam(fn) >= 0) || slices.Contains(extra, fn.String()) {
			c.guards[fn] = roleOf(fn, AccessAuthenticated)
		}
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range own {
			if _, ok := c.guards[fn]; ok || requestParam(fn) < 0 || okResult(fn.Signature) < 0 {
				continue
			}
			role := ""
			for _, callee := range c.callees(fn) {
				if r, ok := c.guards[callee]; ok && accessRank[r] >= accessRank[role] {
					role = r
				}
			}
			if role != "" {
				c.guards[fn] = roleOf(fn, role)
				changed = true
			}
		}
	}
}

// roleOf raises role when fn branches on an admin or guest field.
func roleOf(fn *ssa.Function, role string) string {
	for _, b := range fn.Blocks {
		if ifInstr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If); ok {
			if r := fieldRole(ifInstr.Cond); accessRank[r] > accessRank[role] {
				role = r
			}
		}
	}
	return role
}

// fieldRole reports the role a condition checks: a load of a field whose
// name mentions admin or guest.
func fieldRole(v ssa.Value) string {
	role := ""
	var visit func(v ssa.Value, depth int)
	visit = func(v ssa.Value, depth int) {
		if depth > 4 {
			return
		}
		switch v := v.(type) {
		case *ssa.UnOp:
			if fa, ok := v.X.(*ssa.FieldAddr); ok {
				role = maxRole(role, fieldNameRole(fa.X.Type(), fa.Field))
			}
			visit(v.X, depth+1)
		case *ssa.Field:
			role = maxRole(role, fieldNameRole(v.X.Type(), v.Field))
		case *ssa.BinOp:
			visit(v.X, depth+1)
			visit(v.Y, depth+1)
		}
	}
	visit(v, 0)
	return role
}

func maxRole(a, b string) string {
	if accessRank[b] > accessRank[a] {
		return b
	}
	return a
}

func fieldNameRole(t types.Type, field int) string {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		t = p.Elem()
	}
	st, ok := t.Underlying().(*types.Struct)
	if !ok || field >= st.NumFields() {
		return ""
	}
	switch name := st.Field(field).Name(); {
	case adminFieldRE.MatchString(name):
		return AccessAdmin
	case guestFieldRE.MatchString(name):
		return AccessGuest
	}
	return ""
}

// classify sets the access level of each route.
func (c *authClassifier) classify(routes []Route) {
	for i := range routes {
		rt := &routes[i]
		rt.Access = AccessPublic
		var fns []*ssa.Function
		for _, mw := range rt.Middleware {
			fns = append(fns, middlewareClosures(mw)...)
		}
		if rt.Handler != nil {
			fns = append(fns, rt.Handler)
		}
		for _, fn := range fns {
			if use, ok := c.required(fn, rt.Method, 0); ok {
				rt.Guards = append(rt.Guards, use)
				rt.Access = maxRole(rt.Access, use.Role)
			}
		}
		if rt.Access != AccessPublic {
			continue
		}
		for _, fn := range fns {
			if use, ok := c.consults(fn, rt.Method, map[*ssa.Function]bool{}, 0); ok {
				rt.OptionalAuth = true
				rt.Guards = append(rt.Guards, use)
				break
			}
		}
	}
}

// required reports whether fn rejects the request unless a guard passes,
// for request method m, and which guard call enforces it.
func (c *authClassifier) required(fn *ssa.Function, m string, depth int) (GuardUse, bool) {
	if depth > 3 || len(fn.Blocks) == 0 {
		return GuardUse{}, false
	}
	blocks := c.reachable(fn, m)
	var best GuardUse
	found := false
	for _, b := range blocks {
		for _, instr := range b.Instrs {
			call, ok := instr.(*ssa.Call)
			if !ok {
				continue
			}
			for _, callee := range c.siteCallees(call) {
				role, isGuard := c.guards[callee]
				var use GuardUse
				switch {
				case isGuard:
					r, ok := c.guardEnforced(blocks, call, role)
					if !ok {
						continue
					}
					use = GuardUse{Guard: callee, Pos: call.Pos(), Role: r}
				case inModule(c.r.Module, funcPkg(callee)) && requestParam(callee) >= 0 && passesRequest(fn, call):
					inner, ok := c.required(callee, m, depth+1)
					if !ok || !c.dominatesWork(blocks, call, nil) {
						continue
					}
					use = GuardUse{Guard: inner.Guard, Pos: call.Pos(), Role: inner.Role}
				default:
					continue
				}
				if !found || accessRank[use.Role] > accessRank[best.Role] {
					best, found = use, true
				}
			}
		}
	}
	return best, found
}

// guardEnforced checks that the result of guard call is tested and the
// failing branch denies the request, and returns the enforced role.
func (c *authClassifier) guardEnforced(blocks []*ssa.BasicBlock, call *ssa.Call, role string) (string, bool) {
	okVal := resultValue(call)
	if okVal == nil {
		return "", false
	}
	for _, b := range blocks {
		ifInstr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		deny, ok := denySuccessor(ifInstr, okVal)
		if !ok {
			continue
		}
		allow := b.Succs[1-deny]
		denyBlock := b.Succs[deny]
		region, exits := exitRegion(denyBlock, allow)
		if !exits || !isDenial(region) || c.hasWork(region) {
			continue
		}
		if !c.dominatesWork(blocks, call, region) {
			continue
		}
		// Other conditions leading to the same denial may check a role:
		// err != nil || !u.IsAdmin.
		for _, other := range blocks {
			if oi, ok := other.Instrs[len(other.Instrs)-1].(*ssa.If); ok && slices.Contains(other.Succs, denyBlock) {
				role = maxRole(role, fieldRole(oi.Cond))
			}
		}
		return role, true
	}
	return "", false
}

// denySuccessor returns the index of the If successor taken when okVal
// reports failure, if cond tests okVal.
func denySuccessor(ifInstr *ssa.If, okVal ssa.Value) (int, bool) {
	switch cond := ifInstr.Cond.(type) {
	case *ssa.UnOp:
		if cond.Op == token.NOT && cond.X == okVal {
			return 0, true
		}
	case *ssa.BinOp:
		isNil := func(v ssa.Value) bool { k, ok := v.(*ssa.Const); return ok && k.IsNil() }
		if (cond.X == okVal && isNil(cond.Y)) || (cond.Y == okVal && isNil(cond.X)) {
			switch cond.Op {
			case token.NEQ:
				return 0, true
			case token.EQL:
				return 1, true
			}
		}
	default:
		if ifInstr.Cond == okVal {
			return 1, true
		}
	}
	return 0, false
}

// exitRegion collects the blocks reachable from start without passing
// through allow; exits is false if the region rejoins the allowed path.
// The allowed path is followed up to start, since chained checks
// (err != nil || !u.IsAdmin) also lead into the denial.
func exitRegion(start, allow *ssa.BasicBlock) (region []*ssa.BasicBlock, exits bool) {
	allowed := map[*ssa.BasicBlock]bool{}
	var mark func(b *ssa.BasicBlock)
	mark = func(b *ssa.BasicBlock) {
		if allowed[b] || b == start {
			return
		}
		allowed[b] = true
		for _, s := range b.Succs {
			mark(s)
		}
	}
	mark(allow)
	seen := map[*ssa.BasicBlock]bool{}
	var visit func(b *ssa.BasicBlock) bool
	visit = func(b *ssa.BasicBlock) bool {
		if allowed[b] {
			return false
		}
		if seen[b] {
			return true
		}
		seen[b] = true
		region = append(region, b)
		for _, s := range b.Succs {
			if !visit(s) {
				return false
			}
		}
		return true
	}
	return region, visit(start)
}

// isDenial reports whether a region answers a rejected request: it only
// returns (the guard already responded), or it redirects, errors, or
// sends 401/403.
func isDenial(region []*ssa.BasicBlock) bool {
	calls := 0
	for _, b := range region {
		for _, instr := range b.Instrs {
			ci, ok := instr.(ssa.CallInstruction)
			if !ok {
				continue
			}
			calls++
			common := ci.Common()
			name := ""
			if fn := common.StaticCallee(); fn != nil {
				name = fn.Name()
			} else if common.Method != nil {
				name = common.Method.Name()
			}
			if denialNameRE.MatchString(name) {
				return true
			}
			for _, arg := range common.Args {
				if k, ok := arg.(*ssa.Const); ok && k.Value != nil && k.Value.Kind() == constant.Int {
					if v, _ := constant.Int64Val(k.Value); v == 401 || v == 403 {
						return true
					}
				}
			}
		}
	}
	return calls == 0
}

// hasWork reports whether any call in region can reach a sink.
func (c *authClassifier) hasWork(region []*ssa.BasicBlock) bool {
	for _, b := range region {
		for _, instr := range b.Instrs {
			if call, ok := instr.(ssa.CallInstruction); ok {
				for _, callee := range c.siteCallees(call) {
					if _, guard := c.guards[callee]; !guard && c.work[callee] {
						return true
					}
				}
			}
		}
	}
	return false
}

// dominatesWork reports whether every call that can reach a sink in blocks
// (other than guard itself and calls in the excluded region) runs after it.
func (c *authClassifier) dominatesWork(blocks []*ssa.BasicBlock, guard *ssa.Call, exclude []*ssa.BasicBlock) bool {
	gb := guard.Block()
	for _, b := range blocks {
		if slices.Contains(exclude, b) {
			continue
		}
		for i, instr := range b.Instrs {
			call, ok := instr.(ssa.CallInstruction)
			if !ok || call == ssa.CallInstruction(guard) {
				continue
			}
			isWork := false
			for _, callee := range c.siteCallees(call) {
				if _, guard := c.guards[callee]; !guard && c.work[callee] {
					isWork = true
				}
			}
			if !isWork {
				continue
			}
			if b == gb {
				if i < slices.Index(b.Instrs, ssa.Instruction(guard)) {
					return false
				}
				continue
			}
			if !gb.Dominates(b) {
				return false
			}
		}
	}
	return true
}

// consults reports whether fn, under method m, calls a guard directly or
// through module functions.
func (c *authClassifier) consults(fn *ssa.Function, m string, seen map[*ssa.Function]bool, depth int) (GuardUse, bool) {
	if depth > 6 || seen[fn] || len(fn.Blocks) == 0 {
		return GuardUse{}, false
	}
	seen[fn] = true
	for _, b := range c.reachable(fn, m) {
		for _, instr := range b.Instrs {
			call, ok := instr.(ssa.CallInstruction)
			if !ok {
				continue
			}
			for _, callee := range c.siteCallees(call) {
				if _, ok := c.guards[callee]; ok {
					return GuardUse{Guard: callee, Pos: call.Pos()}, true
				}
				targets := []*ssa.Function{callee}
				if !inModule(c.r.Module, funcPkg(callee)) {
					// Through library code back into the module, e.g.
					// HandlerFunc.ServeHTTP calling a handler closure.
					targets = c.moduleCallees(callee)
				}
				for _, t := range targets {
					if use, ok := c.consults(t, m, seen, depth+1); ok {
						return GuardUse{Guard: use.Guard, Pos: call.Pos()}, true
					}
				}
			}
		}
	}
	return GuardUse{}, false
}

// reachable returns fn's blocks that run for request method m.
func (c *authClassifier) reachable(fn *ssa.Function, m string) []*ssa.BasicBlock {
	scope := c.scopes[fn]
	if scope == nil || m == "" || m == "ANY" {
		return fn.Blocks
	}
	key := m
	if !slices.Contains(scope.compared, m) {
		key = OtherMethod
	}
	var out []*ssa.BasicBlock
	for _, b := range fn.Blocks {
		if slices.Contains(scope.reachable[b], key) {
			out = append(out, b)
		}
	}
	return out
}

// siteCallees returns the functions a call site may invoke.
func (c *authClassifier) siteCallees(call ssa.CallInstruction) []*ssa.Function {
	if fn := call.Common().StaticCallee(); fn != nil {
		return []*ssa.Function{origin(fn)}
	}
	node := c.r.CallGraph.Nodes[call.Parent()]
	if node == nil {
		return nil
	}
	var out []*ssa.Function
	for _, e := range node.Out {
		if e.Site == call && !slices.Contains(out, origin(e.Callee.Func)) {
			out = append(out, origin(e.Callee.Func))
		}
	}
	return out
}

// moduleCallees returns the module functions an external function calls.
func (c *authClassifier) moduleCallees(fn *ssa.Function) []*ssa.Function {
	var out []*ssa.Function
	for _, callee := range c.callees(fn) {
		if inModule(c.r.Module, funcPkg(callee)) && !slices.Contains(out, callee) {
			out = append(out, callee)
		}
	}
	return out
}

func (c *authClassifier) callees(fn *ssa.Function) []*ssa.Function {
	node := c.r.CallGraph.Nodes[fn]
	if node == nil {
		return nil
	}
	var out []*ssa.Function
	for _, e := range node.Out {
		out = append(out, origin(e.Callee.Func))
	}
	return out
}

// reachesSink returns the functions that can reach a sink call.
func reachesSink(r *Result) map[*ssa.Function]bool {
	out := map[*ssa.Function]bool{}
	var queue []*ssa.Function
	for _, s := range r.Sinks {
		if !out[s.Caller] {
			out[s.Caller] = true
			queue = append(queue, s.Caller)
		}
	}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		node := r.CallGraph.Nodes[fn]
		if node == nil {
			continue
		}
		for _, e := range node.In {
			caller := origin(e.Caller.Func)
			if !out[caller] {
				out[caller] = true
				queue = append(queue, caller)
			}
		}
	}
	return out
}

// requestParam returns the index of fn's *http.Request parameter, or -1.
func requestParam(fn *ssa.Function) int {
	for i, p := range fn.Params {
		if isNetHTTP(p.Type(), "Request") {
			if _, ok := p.Type().(*types.Pointer); ok {
				return i
			}
		}
	}
	return -1
}

// passesRequest reports whether call passes fn's request parameter on.
func passesRequest(fn *ssa.Function, call *ssa.Call) bool {
	i := requestParam(fn)
	if i < 0 {
		return false
	}
	return slices.ContainsFunc(call.Common().Args, func(arg ssa.Value) bool { return isParam(arg, fn.Params[i]) })
}

// isParam reports whether v is p, directly or reloaded from the heap cell
// Go uses when a closure captures the parameter.
func isParam(v ssa.Value, p *ssa.Parameter) bool {
	if v == p {
		return true
	}
	load, ok := v.(*ssa.UnOp)
	if !ok || load.Op != token.MUL {
		return false
	}
	alloc, ok := load.X.(*ssa.Alloc)
	if !ok {
		return false
	}
	for _, ref := range *alloc.Referrers() {
		if st, ok := ref.(*ssa.Store); ok && st.Addr == alloc && st.Val == p {
			return true
		}
	}
	return false
}

// okResult returns the index of the last bool or error result, or -1.
func okResult(sig *types.Signature) int {
	res := sig.Results()
	for i := res.Len() - 1; i >= 0; i-- {
		t := res.At(i).Type()
		if b, ok := t.Underlying().(*types.Basic); ok && b.Kind() == types.Bool {
			return i
		}
		if types.Identical(t, types.Universe.Lookup("error").Type()) {
			return i
		}
	}
	return -1
}

// resultValue returns the SSA value of a call's bool or error result.
func resultValue(call *ssa.Call) ssa.Value {
	i := okResult(call.Common().Signature())
	if i < 0 {
		return nil
	}
	if call.Common().Signature().Results().Len() == 1 {
		return call
	}
	for _, ref := range *call.Referrers() {
		if ex, ok := ref.(*ssa.Extract); ok && ex.Index == i {
			return ex
		}
	}
	return nil
}

// middlewareClosures returns the closures a middleware factory returns.
func middlewareClosures(fn *ssa.Function) []*ssa.Function {
	var out []*ssa.Function
	for _, ret := range returns(fn) {
		v := ret
		for {
			switch x := v.(type) {
			case *ssa.MakeInterface:
				v = x.X
				continue
			case *ssa.ChangeType:
				v = x.X
				continue
			case *ssa.Convert:
				v = x.X
				continue
			}
			break
		}
		if mc, ok := v.(*ssa.MakeClosure); ok {
			out = append(out, mc.Fn.(*ssa.Function))
		}
	}
	if len(out) == 0 {
		out = append(out, fn)
	}
	return out
}

// accessEvidence describes the guards for a route node attribute.
func accessEvidence(rt Route) string {
	var parts []string
	for _, g := range rt.Guards {
		name := origin(g.Guard).String()
		if g.Role == "" {
			parts = append(parts, "consults "+name)
		} else {
			parts = append(parts, g.Role+" via "+name)
		}
	}
	return strings.Join(parts, "; ")
}
