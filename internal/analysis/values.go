package analysis

import (
	"cmp"
	"go/constant"
	"go/token"
	"regexp"
	"slices"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"
)

// Unknown stands in for a part of a string that cannot be determined
// statically.
const Unknown = "{?}"

const (
	maxEvalDepth  = 12 // recursion bound when tracing a value
	maxEvalValues = 32 // more possible values than this collapse to Unknown
)

// evaluator folds SSA values into the set of strings they can hold, e.g.
// "GET "+prefix+"/notes" inside a loop over lang.Codes() into
// {"GET /en/notes", "GET /de/notes"}. Parts it cannot determine become
// Unknown, so the result is never empty.
//
// It only looks inside the module's own functions; calls into other
// packages are opaque except for a few string builders it models
// (path.Join, filepath.Join, fmt.Sprintf).
type evaluator struct {
	cg      *callgraph.Graph
	module  string
	visited map[ssa.Value]bool
	// values are configured values by variable, function or env name
	// (Options.Values).
	values map[string][]string
}

func newEvaluator(cg *callgraph.Graph, module string) *evaluator {
	return &evaluator{cg: cg, module: module, visited: map[ssa.Value]bool{}}
}

// evaluator returns an evaluator over the result's call graph and
// configured values.
func (r *Result) evaluator() *evaluator {
	e := newEvaluator(r.CallGraph, r.Module)
	e.values = r.values
	return e
}

func (e *evaluator) own(fn *ssa.Function) bool {
	return len(fn.Blocks) > 0 && inModule(e.module, funcPkg(fn))
}

// strings returns the possible string values of v, deduplicated, in order.
func (e *evaluator) strings(v ssa.Value) []string {
	return e.str(v, 0)
}

func (e *evaluator) str(v ssa.Value, depth int) []string {
	if depth > maxEvalDepth || e.visited[v] {
		return []string{Unknown}
	}
	e.visited[v] = true
	defer delete(e.visited, v)

	switch v := v.(type) {
	case *ssa.Const:
		if v.Value != nil && v.Value.Kind() == constant.String {
			return []string{constant.StringVal(v.Value)}
		}
	case *ssa.BinOp:
		if v.Op == token.ADD {
			return concat(e.str(v.X, depth+1), e.str(v.Y, depth+1))
		}
	case *ssa.Convert:
		return e.str(v.X, depth+1)
	case *ssa.ChangeType:
		return e.str(v.X, depth+1)
	case *ssa.MakeInterface:
		return e.str(v.X, depth+1)
	case *ssa.Phi:
		var out []string
		for _, edge := range v.Edges {
			out = union(out, e.str(edge, depth+1))
		}
		return limit(out)
	case *ssa.UnOp:
		if v.Op == token.MUL {
			return e.load(v.X, depth+1)
		}
	case *ssa.Field:
		// A field of a slice element being ranged over: def.route.
		if load, ok := v.X.(*ssa.UnOp); ok && load.Op == token.MUL {
			if ia, ok := load.X.(*ssa.IndexAddr); ok {
				return e.elemField(ia.X, v.Field, depth+1)
			}
		}
	case *ssa.Parameter:
		return e.param(v, depth+1)
	case *ssa.Extract:
		// Result i of a module function returning a tuple.
		if call, ok := v.Tuple.(*ssa.Call); ok {
			if fn := call.Common().StaticCallee(); fn != nil && e.own(fn) {
				var out []string
				for _, b := range fn.Blocks {
					if ret, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return); ok && v.Index < len(ret.Results) {
						out = union(out, e.str(ret.Results[v.Index], depth+1))
					}
				}
				if len(out) > 0 {
					return limit(dropEmpty(out))
				}
			}
		}
	case *ssa.Call:
		if vals, ok := e.values[callName(v.Common())]; ok {
			return vals
		}
		fn := v.Common().StaticCallee()
		if fn == nil {
			break
		}
		if model := e.model(fn.String(), v.Common().Args, depth+1); model != nil {
			return model
		}
		if e.own(fn) {
			var out []string
			for _, r := range returns(fn) {
				out = union(out, e.str(r, depth+1))
			}
			if len(out) > 0 {
				return limit(dropEmpty(out))
			}
		}
	}
	return []string{Unknown}
}

// load evaluates *addr: an element of a slice being ranged over, or a
// package-level variable.
func (e *evaluator) load(addr ssa.Value, depth int) []string {
	switch a := addr.(type) {
	case *ssa.IndexAddr:
		elems, ok := e.slice(a.X, depth+1)
		if !ok {
			return []string{Unknown}
		}
		if c, isConst := a.Index.(*ssa.Const); isConst {
			if i, exact := constant.Int64Val(c.Value); exact && i >= 0 && int(i) < len(elems) {
				return elems[i]
			}
		}
		var out []string
		for _, el := range elems {
			out = union(out, el)
		}
		return limit(out)
	case *ssa.FieldAddr:
		if ia, ok := a.X.(*ssa.IndexAddr); ok {
			return e.elemField(ia.X, a.Field, depth+1)
		}
		// A field of a local copy of a slice element: the range variable
		// def in `for _, def := range defs { ... def.route ... }`.
		if alloc, ok := a.X.(*ssa.Alloc); ok {
			var out []string
			for _, ref := range *alloc.Referrers() {
				st, ok := ref.(*ssa.Store)
				if !ok || st.Addr != alloc {
					continue
				}
				if load, ok := st.Val.(*ssa.UnOp); ok && load.Op == token.MUL {
					if ia, ok := load.X.(*ssa.IndexAddr); ok {
						out = union(out, e.elemField(ia.X, a.Field, depth+1))
					}
				}
			}
			if len(out) > 0 {
				return limit(out)
			}
		}
	case *ssa.Alloc:
		// A local variable in a heap cell, e.g. one captured by a
		// closure: the values stored into it.
		var out []string
		for _, ref := range *a.Referrers() {
			if st, ok := ref.(*ssa.Store); ok && st.Addr == a {
				out = union(out, e.str(st.Val, depth+1))
			}
		}
		if len(out) > 0 {
			return limit(out)
		}
	case *ssa.Global:
		if vals, ok := e.values[a.String()]; ok {
			return vals
		}
		if vals := globalStores(a); len(vals) > 0 {
			var out []string
			for _, val := range vals {
				out = union(out, e.str(val, depth+1))
			}
			return limit(out)
		}
	}
	return []string{Unknown}
}

// slice evaluates each element of a slice value built from a composite
// literal, possibly returned by a function or stored in a package variable.
func (e *evaluator) slice(v ssa.Value, depth int) ([][]string, bool) {
	if depth > maxEvalDepth {
		return nil, false
	}
	switch v := v.(type) {
	case *ssa.Slice:
		if alloc, ok := v.X.(*ssa.Alloc); ok {
			return e.allocElems(alloc, depth+1)
		}
	case *ssa.Call:
		// A configured list, e.g. provider names from configuration.
		if vals, ok := e.values[callName(v.Common())]; ok {
			elems := make([][]string, len(vals))
			for i, val := range vals {
				elems[i] = []string{val}
			}
			return elems, true
		}
		fn := v.Common().StaticCallee()
		if fn == nil || !e.own(fn) {
			return nil, false
		}
		rets := returns(fn)
		if len(rets) != 1 {
			return nil, false
		}
		return e.slice(rets[0], depth+1)
	case *ssa.UnOp:
		if g, ok := v.X.(*ssa.Global); ok && v.Op == token.MUL {
			if vals := globalStores(g); len(vals) == 1 {
				return e.slice(vals[0], depth+1)
			}
		}
	case *ssa.ChangeType:
		return e.slice(v.X, depth+1)
	}
	return nil, false
}

// elemField evaluates field f of every element of a slice of structs built
// from composite literals, possibly appended to under conditions.
func (e *evaluator) elemField(slice ssa.Value, f, depth int) []string {
	allocs := sliceAllocs(slice, 0)
	if len(allocs) == 0 {
		return []string{Unknown}
	}
	var out []string
	for _, alloc := range allocs {
		for _, ref := range *alloc.Referrers() {
			ia, ok := ref.(*ssa.IndexAddr)
			if !ok {
				continue
			}
			for _, r := range *ia.Referrers() {
				switch r := r.(type) {
				case *ssa.FieldAddr: // t[i].f = v
					if r.Field == f {
						out = union(out, fieldStores(e, r, depth))
					}
				case *ssa.Store: // t[i] = S{f: v}, built in a local first
					if r.Addr != ia {
						continue
					}
					if load, ok := r.Val.(*ssa.UnOp); ok && load.Op == token.MUL {
						if local, ok := load.X.(*ssa.Alloc); ok {
							for _, lr := range *local.Referrers() {
								if fa, ok := lr.(*ssa.FieldAddr); ok && fa.Field == f {
									out = union(out, fieldStores(e, fa, depth))
								}
							}
						}
					}
				}
			}
		}
	}
	if len(out) == 0 {
		return []string{Unknown}
	}
	return limit(out)
}

// fieldStores evaluates the values stored through a field address.
func fieldStores(e *evaluator, fa *ssa.FieldAddr, depth int) []string {
	var out []string
	for _, ref := range *fa.Referrers() {
		if st, ok := ref.(*ssa.Store); ok && st.Addr == fa {
			out = union(out, e.str(st.Val, depth+1))
		}
	}
	return out
}

// sliceAllocs finds the array allocations a slice value's elements live in,
// through slicing, phis and append.
func sliceAllocs(v ssa.Value, depth int) []*ssa.Alloc {
	if depth > maxEvalDepth {
		return nil
	}
	switch v := v.(type) {
	case *ssa.Slice:
		if alloc, ok := v.X.(*ssa.Alloc); ok {
			return []*ssa.Alloc{alloc}
		}
	case *ssa.Phi:
		var out []*ssa.Alloc
		for _, edge := range v.Edges {
			if edge != v {
				out = append(out, sliceAllocs(edge, depth+1)...)
			}
		}
		return out
	case *ssa.Call:
		if b, ok := v.Common().Value.(*ssa.Builtin); ok && b.Name() == "append" {
			var out []*ssa.Alloc
			for _, arg := range v.Common().Args {
				out = append(out, sliceAllocs(arg, depth+1)...)
			}
			return out
		}
	}
	return nil
}

// allocElems evaluates the constant-index stores into an array allocation.
func (e *evaluator) allocElems(alloc *ssa.Alloc, depth int) ([][]string, bool) {
	byIndex := map[int64][]string{}
	var maxIndex int64 = -1
	for _, ref := range *alloc.Referrers() {
		ia, ok := ref.(*ssa.IndexAddr)
		if !ok {
			continue
		}
		c, ok := ia.Index.(*ssa.Const)
		if !ok {
			return nil, false
		}
		i, _ := constant.Int64Val(c.Value)
		for _, r := range *ia.Referrers() {
			if st, ok := r.(*ssa.Store); ok && st.Addr == ia {
				byIndex[i] = e.str(st.Val, depth+1)
				maxIndex = max(maxIndex, i)
			}
		}
	}
	elems := make([][]string, maxIndex+1)
	for i := range elems {
		if vals, ok := byIndex[int64(i)]; ok {
			elems[i] = vals
		} else {
			elems[i] = []string{""}
		}
	}
	return elems, true
}

// param evaluates a function parameter from the arguments of its static
// callers.
func (e *evaluator) param(p *ssa.Parameter, depth int) []string {
	fn := p.Parent()
	if e.cg == nil || !e.own(fn) {
		return []string{Unknown}
	}
	idx := slices.Index(fn.Params, p)
	node := e.cg.Nodes[fn]
	if idx < 0 || node == nil || len(node.In) == 0 {
		return []string{Unknown}
	}
	var out []string
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
			// Interface calls pass the receiver separately, not in Args.
			argIdx--
		}
		if argIdx < 0 || argIdx >= len(common.Args) {
			out = union(out, []string{Unknown})
			continue
		}
		out = union(out, e.str(common.Args[argIdx], depth+1))
	}
	if len(out) == 0 {
		return []string{Unknown}
	}
	return limit(out)
}

// verbRE matches one fmt verb, e.g. %s, %-10d, %%.
var verbRE = regexp.MustCompile(`%[-+# 0]*[0-9*]*(\.[0-9*]+)?[a-zA-Z%]`)

// model evaluates calls to well-known string builders; nil if fn is not one.
func (e *evaluator) model(fn string, args []ssa.Value, depth int) []string {
	switch fn {
	case "os.Getenv":
		if len(args) == 1 && e.values != nil {
			for _, name := range e.str(args[0], depth) {
				if vals, ok := e.values["env:"+name]; ok {
					return vals
				}
			}
		}
		return nil
	case "path.Join", "path/filepath.Join":
		if len(args) != 1 {
			return nil
		}
		elems, ok := e.slice(args[0], depth)
		if !ok {
			return []string{Unknown}
		}
		out := []string{""}
		for i, el := range elems {
			if i > 0 {
				out = concat(out, []string{"/"})
			}
			out = concat(out, el)
		}
		return out
	case "fmt.Sprintf":
		if len(args) != 2 {
			return nil
		}
		formats := e.str(args[0], depth)
		elems, ok := e.slice(args[1], depth)
		var out []string
		for _, format := range formats {
			parts := verbRE.Split(format, -1)
			verbs := verbRE.FindAllString(format, -1)
			res := []string{parts[0]}
			arg := 0
			for i, verb := range verbs {
				switch {
				case verb == "%%":
					res = concat(res, []string{"%"})
				case ok && arg < len(elems):
					res = concat(res, elems[arg])
					arg++
				default:
					res = concat(res, []string{Unknown})
				}
				res = concat(res, []string{parts[i+1]})
			}
			out = union(out, res)
		}
		return limit(out)
	}
	return nil
}

// callName is the go/ssa name of a call's static callee, or of the
// interface method it invokes.
func callName(c *ssa.CallCommon) string {
	if c.IsInvoke() {
		return c.Method.FullName()
	}
	if fn := c.StaticCallee(); fn != nil {
		return fn.String()
	}
	return ""
}

// returns lists the first result of every return statement in fn.
func returns(fn *ssa.Function) []ssa.Value {
	var out []ssa.Value
	for _, b := range fn.Blocks {
		if ret, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return); ok && len(ret.Results) > 0 {
			out = append(out, ret.Results[0])
		}
	}
	return out
}

// globalStores finds the values stored into a package variable by its
// package initializer.
func globalStores(g *ssa.Global) []ssa.Value {
	if g.Pkg == nil {
		return nil
	}
	init := g.Pkg.Func("init")
	if init == nil {
		return nil
	}
	var out []ssa.Value
	for _, b := range init.Blocks {
		for _, instr := range b.Instrs {
			if st, ok := instr.(*ssa.Store); ok && st.Addr == g {
				out = append(out, st.Val)
			}
		}
	}
	return out
}

// dropEmpty removes "" when other values exist: an empty string returned
// from a function is usually the zero value of an error path.
func dropEmpty(xs []string) []string {
	if len(xs) < 2 || !slices.Contains(xs, "") {
		return xs
	}
	return slices.DeleteFunc(slices.Clone(xs), func(x string) bool { return x == "" })
}

func concat(xs, ys []string) []string {
	if len(xs)*len(ys) > maxEvalValues {
		return []string{Unknown}
	}
	out := make([]string, 0, len(xs)*len(ys))
	for _, x := range xs {
		for _, y := range ys {
			out = union(out, []string{x + y})
		}
	}
	return out
}

func union(xs, ys []string) []string {
	for _, y := range ys {
		if !slices.Contains(xs, y) {
			xs = append(xs, y)
		}
	}
	return xs
}

func limit(xs []string) []string {
	if len(xs) > maxEvalValues {
		return []string{Unknown}
	}
	return xs
}
