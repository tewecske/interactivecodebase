package analysis

import (
	"go/constant"
	"go/token"
	"go/types"
	"slices"

	"golang.org/x/tools/go/ssa"
)

// OtherMethod stands for every HTTP method a function does not compare
// against explicitly.
const OtherMethod = "*"

// methodScope says under which request methods the code of a function that
// branches on r.Method runs.
type methodScope struct {
	// compared are the methods the function tests, e.g. [GET POST].
	compared []string
	// reachable maps a block to the methods (compared ones and
	// OtherMethod) under which it can execute.
	reachable map[*ssa.BasicBlock][]string
}

// methods returns the methods under which instr can run, or nil if it runs
// for every method.
func (s *methodScope) methods(instr ssa.Instruction) []string {
	if s == nil {
		return nil
	}
	ms := s.reachable[instr.Block()]
	if len(ms) == len(s.compared)+1 {
		return nil
	}
	return ms
}

// methodScopes finds functions that branch on (*http.Request).Method and
// computes, for each block, the methods it runs under.
func methodScopes(funcs []*ssa.Function) map[*ssa.Function]*methodScope {
	out := map[*ssa.Function]*methodScope{}
	for _, fn := range funcs {
		conds := methodConditions(fn)
		if len(conds) == 0 {
			continue
		}
		var compared []string
		for _, c := range conds {
			if !slices.Contains(compared, c.method) {
				compared = append(compared, c.method)
			}
		}
		scope := &methodScope{compared: compared, reachable: map[*ssa.BasicBlock][]string{}}
		for _, m := range append(slices.Clone(compared), OtherMethod) {
			for _, b := range reachableBlocks(fn, conds, m) {
				scope.reachable[b] = append(scope.reachable[b], m)
			}
		}
		out[fn] = scope
	}
	return out
}

// methodCond is an if whose condition is r.Method == method (or !=).
type methodCond struct {
	ifInstr *ssa.If
	method  string
	equal   bool
}

func methodConditions(fn *ssa.Function) []methodCond {
	var conds []methodCond
	for _, b := range fn.Blocks {
		ifInstr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		bin, ok := ifInstr.Cond.(*ssa.BinOp)
		if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
			continue
		}
		method, ok := comparedMethod(bin.X, bin.Y)
		if !ok {
			method, ok = comparedMethod(bin.Y, bin.X)
		}
		if ok {
			conds = append(conds, methodCond{ifInstr: ifInstr, method: method, equal: bin.Op == token.EQL})
		}
	}
	return conds
}

// comparedMethod reports whether field is a load of r.Method on an
// *http.Request and c a string constant, returning the constant.
func comparedMethod(field, c ssa.Value) (string, bool) {
	k, ok := c.(*ssa.Const)
	if !ok || k.Value == nil || k.Value.Kind() != constant.String {
		return "", false
	}
	load, ok := field.(*ssa.UnOp)
	if !ok || load.Op != token.MUL {
		return "", false
	}
	fa, ok := load.X.(*ssa.FieldAddr)
	if !ok {
		return "", false
	}
	ptr, ok := types.Unalias(fa.X.Type()).(*types.Pointer)
	if !ok || !isNetHTTP(ptr, "Request") {
		return "", false
	}
	st, ok := ptr.Elem().Underlying().(*types.Struct)
	if !ok || st.Field(fa.Field).Name() != "Method" {
		return "", false
	}
	return constant.StringVal(k.Value), true
}

// reachableBlocks walks fn from its entry with every method condition
// decided for request method m.
func reachableBlocks(fn *ssa.Function, conds []methodCond, m string) []*ssa.BasicBlock {
	decided := map[*ssa.If]int{} // successor index taken
	for _, c := range conds {
		match := c.method == m
		if match == c.equal {
			decided[c.ifInstr] = 0 // true branch
		} else {
			decided[c.ifInstr] = 1
		}
	}
	seen := map[*ssa.BasicBlock]bool{}
	var order []*ssa.BasicBlock
	var visit func(b *ssa.BasicBlock)
	visit = func(b *ssa.BasicBlock) {
		if b == nil || seen[b] {
			return
		}
		seen[b] = true
		order = append(order, b)
		if ifInstr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If); ok {
			if i, ok := decided[ifInstr]; ok {
				visit(b.Succs[i])
				return
			}
		}
		for _, s := range b.Succs {
			visit(s)
		}
	}
	if len(fn.Blocks) > 0 {
		visit(fn.Blocks[0])
	}
	if fn.Recover != nil {
		visit(fn.Recover)
	}
	return order
}
