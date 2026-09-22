package analysis

import (
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Func returns the function behind a func or method node ID, or nil.
func (r *Result) Func(id string) *ssa.Function {
	r.funcOnce.Do(func() {
		r.funcIndex = map[string]*ssa.Function{}
		for fn := range ssautil.AllFunctions(r.Program) {
			if fn.Synthetic == "" {
				r.funcIndex[FuncID(fn)] = origin(fn)
			}
		}
	})
	return r.funcIndex[id]
}

// TypeName returns the named type behind a type node ID ("type:pkg.T"),
// searching the loaded packages, or nil.
func (r *Result) TypeName(id string) *types.TypeName {
	qualified, ok := strings.CutPrefix(id, "type:")
	if !ok {
		return nil
	}
	dot := strings.LastIndex(qualified, ".")
	if dot < 0 {
		return nil
	}
	pkgPath, name := qualified[:dot], qualified[dot+1:]
	var found *types.TypeName
	packages.Visit(r.Packages, nil, func(p *packages.Package) {
		if found != nil || p.PkgPath != pkgPath || p.Types == nil {
			return
		}
		if tn, ok := p.Types.Scope().Lookup(name).(*types.TypeName); ok {
			found = tn
		}
	})
	return found
}

// InModule reports whether a package path belongs to the analyzed module.
func (r *Result) InModule(pkgPath string) bool {
	return pkgPath == r.Module || strings.HasPrefix(pkgPath, r.Module+"/")
}
