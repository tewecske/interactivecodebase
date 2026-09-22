package analysis

import (
	"cmp"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"

	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/sqlparse"
)

// FuncID returns the graph node ID of fn: "func:" or "method:" followed by
// the go/ssa name, e.g. "method:(*example.com/app.T).Save".
func FuncID(fn *ssa.Function) string {
	fn = origin(fn)
	return graph.NodeID(funcKind(fn), fn.String())
}

// InterfaceMethodID returns the graph node ID of an interface method, e.g.
// "interface_call:(example.com/app.Store).Save".
func InterfaceMethodID(m *types.Func) string {
	return graph.NodeID(graph.KindInterfaceCall, m.FullName())
}

// TypeID returns the graph node ID of a named type, e.g. "type:example.com/app.Store".
func TypeID(obj *types.TypeName) string {
	return graph.NodeID(graph.KindType, obj.Pkg().Path()+"."+obj.Name())
}

func funcKind(fn *ssa.Function) graph.NodeKind {
	if fn.Signature.Recv() != nil {
		return graph.KindMethod
	}
	return graph.KindFunc
}

// origin maps a generic instantiation to its generic function so each
// source function is one node.
func origin(fn *ssa.Function) *ssa.Function {
	if o := fn.Origin(); o != nil {
		return o
	}
	return fn
}

func funcPkg(fn *ssa.Function) *types.Package {
	if fn.Pkg != nil {
		return fn.Pkg.Pkg
	}
	if obj := fn.Object(); obj != nil {
		return obj.Pkg()
	}
	return nil
}

type builder struct {
	r           *Result
	own         []*ssa.Function
	scopes      map[*ssa.Function]*methodScope
	exclude     []string
	fset        *token.FileSet
	w           *graph.Writer
	added       map[string]bool
	moduleFuncs int
}

func newBuilder(r *Result, own []*ssa.Function, exclude []string) *builder {
	return &builder{r: r, own: own, scopes: methodScopes(own), exclude: exclude, fset: r.Program.Fset, added: map[string]bool{}}
}

// moduleFunctions returns the module's source functions, sorted by name so
// the graph is deterministic. Instantiations of generic functions stay in:
// their bodies hold the calls, and addFunc maps them onto the generic
// function's node.
func moduleFunctions(r *Result, funcs map[*ssa.Function]bool) []*ssa.Function {
	var own []*ssa.Function
	for fn := range funcs {
		if fn.Synthetic == "" && inModule(r.Module, funcPkg(fn)) {
			own = append(own, fn)
		}
	}
	slices.SortFunc(own, func(x, y *ssa.Function) int { return cmp.Compare(x.String(), y.String()) })
	return own
}

func inModule(module string, pkg *types.Package) bool {
	if pkg == nil {
		return false
	}
	p := pkg.Path()
	return p == module || strings.HasPrefix(p, module+"/")
}

func (b *builder) inModule(pkg *types.Package) bool { return inModule(b.r.Module, pkg) }

// write adds every module function, the calls they make (to module
// functions and to non-excluded external functions, which become leaves),
// interface dispatch, and implements relations between module types.
func (b *builder) write(w *graph.Writer) error {
	b.w = w
	own := b.own
	for _, fn := range own {
		if err := b.addFunc(fn); err != nil {
			return err
		}
	}
	b.moduleFuncs = len(b.added)
	for _, fn := range own {
		if n := b.r.CallGraph.Nodes[fn]; n != nil {
			if err := b.addCalls(n); err != nil {
				return err
			}
		}
	}
	if err := b.addImplements(); err != nil {
		return err
	}
	if err := b.addSinks(); err != nil {
		return err
	}
	if err := b.addSchema(); err != nil {
		return err
	}
	if err := b.addQueries(); err != nil {
		return err
	}
	return b.addRoutes()
}

// addQueries links SQL sinks to the tables they touch. Tables that the
// migrations do not create (e.g. made by code at runtime) get an
// "inferred" table node.
func (b *builder) addQueries() error {
	for _, s := range b.r.Sinks {
		if s.Query == nil {
			continue
		}
		for _, t := range s.Query.Tables {
			id := TableID(t.Name)
			if b.r.Schema.Table(t.Name) == nil && !b.added[id] {
				b.added[id] = true
				err := b.w.AddNode(graph.Node{ID: id, Kind: graph.KindSQLTable, Name: t.Name, Attrs: map[string]string{"inferred": "true"}})
				if err != nil {
					return err
				}
			}
			attrs := map[string]string{"op": t.Op}
			if len(t.Columns) > 0 {
				attrs["columns"] = strings.Join(t.Columns, ",")
			}
			edge := graph.Edge{From: s.ID(b.fset, b.relFile), To: id, Kind: graph.EdgeQueries, Attrs: attrs}
			if err := b.w.AddEdge(edge); err != nil {
				return err
			}
		}
	}
	return nil
}

// TableID returns the graph node ID of a database table.
func TableID(name string) string { return graph.NodeID(graph.KindSQLTable, name) }

// ColumnID returns the graph node ID of a table column.
func ColumnID(table, column string) string {
	return graph.NodeID(graph.KindSQLColumn, table+"."+column)
}

// addSchema adds tables, their columns and foreign keys from the migrations.
func (b *builder) addSchema() error {
	s := b.r.Schema
	for _, t := range s.Tables {
		cols := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			cols[i] = c.Name + " " + c.Type
		}
		attrs := map[string]string{}
		if len(t.PrimaryKey) > 0 {
			attrs["primaryKey"] = strings.Join(t.PrimaryKey, ",")
		}
		err := b.w.AddNode(graph.Node{
			ID: TableID(t.Name), Kind: graph.KindSQLTable, Name: t.Name,
			Detail: strings.Join(cols, ", "), Pos: schemaPos(t.Pos), Attrs: attrs,
		})
		if err != nil {
			return err
		}
		for _, c := range t.Columns {
			cattrs := map[string]string{"table": t.Name, "type": c.Type}
			if c.NotNull {
				cattrs["notNull"] = "true"
			}
			if slices.Contains(t.PrimaryKey, c.Name) {
				cattrs["primaryKey"] = "true"
			}
			id := ColumnID(t.Name, c.Name)
			if err := b.w.AddNode(graph.Node{ID: id, Kind: graph.KindSQLColumn, Name: c.Name, Detail: c.Type, Pos: schemaPos(c.Pos), Attrs: cattrs}); err != nil {
				return err
			}
			if err := b.w.AddEdge(graph.Edge{From: TableID(t.Name), To: id, Kind: graph.EdgeHasColumn}); err != nil {
				return err
			}
		}
	}
	for _, t := range s.Tables {
		for _, fk := range t.ForeignKeys {
			if s.Table(fk.RefTable) == nil {
				continue // references a table the migrations do not create
			}
			attrs := map[string]string{
				"columns":    strings.Join(fk.Columns, ","),
				"references": fk.RefTable + "." + strings.Join(fk.RefColumns, ","),
			}
			if fk.Name != "" {
				attrs["constraint"] = fk.Name
			}
			if fk.OnDelete != "" {
				attrs["onDelete"] = fk.OnDelete
			}
			edge := graph.Edge{From: TableID(t.Name), To: TableID(fk.RefTable), Kind: graph.EdgeFK, Pos: schemaPos(fk.Pos), Attrs: attrs}
			if err := b.w.AddEdge(edge); err != nil {
				return err
			}
		}
	}
	return nil
}

func schemaPos(p sqlparse.Pos) graph.Pos { return graph.Pos{File: p.File, StartLine: p.Line} }

// addSinks adds a node per sink call site, called by its containing function.
func (b *builder) addSinks() error {
	for _, s := range b.r.Sinks {
		id := s.ID(b.fset, b.relFile)
		attrs := map[string]string{"callee": s.Callee, "caller": origin(s.Caller).String(), "resolved": "false"}
		if s.Resolved() {
			attrs["resolved"] = "true"
		}
		detail := strings.Join(s.Values, "\n")
		if q := s.Query; q != nil {
			attrs["op"] = strings.Join(q.Ops, ",")
			if q.Partial {
				attrs["partial"] = "true"
			}
			if q.Err != nil {
				attrs["parseError"] = q.Err.Error()
			}
		}
		if s.FuncValue {
			attrs["funcValue"] = "true"
			detail = s.Callee + " passed as a function value"
		}
		err := b.w.AddNode(graph.Node{
			ID:      id,
			Kind:    s.Kind,
			Name:    shortFuncName(s.Callee),
			Package: funcPkg(s.Caller).Path(),
			Detail:  detail,
			Pos:     b.pos(s.Pos, token.NoPos),
			Attrs:   attrs,
		})
		if err != nil {
			return err
		}
		if err := b.addFunc(s.Caller); err != nil {
			return err
		}
		methods := b.scopes[s.Caller].methods(s.Instr)
		edge := graph.Edge{From: FuncID(s.Caller), To: id, Kind: graph.EdgeCalls, Pos: b.pos(s.Pos, token.NoPos), Attrs: methodAttrs(methods)}
		if err := b.w.AddEdge(edge); err != nil {
			return err
		}
	}
	return nil
}

// shortFuncName drops the package path directories from a go/ssa name:
// "(*database/sql.DB).Query" becomes "(*sql.DB).Query".
func shortFuncName(name string) string {
	prefix := ""
	for _, p := range []string{"(*", "("} {
		if rest, ok := strings.CutPrefix(name, p); ok {
			prefix, name = p, rest
			break
		}
	}
	dot := strings.Index(name, ".")
	if dot < 0 {
		return prefix + name
	}
	if slash := strings.LastIndex(name[:dot], "/"); slash >= 0 {
		name = name[slash+1:]
	}
	return prefix + name
}

// addRoutes adds a route node per registered route, linked to its handler.
func (b *builder) addRoutes() error {
	for _, rt := range b.r.Routes {
		attrs := map[string]string{"method": rt.Method, "pattern": rt.Pattern, "handler": rt.HandlerName()}
		if len(rt.Variants) > 0 {
			attrs["variants"] = strings.Join(rt.Variants, " ")
		}
		if len(rt.Middleware) > 0 {
			attrs["middleware"] = funcNames(rt.Middleware)
		}
		if len(rt.MuxMiddleware) > 0 {
			attrs["muxMiddleware"] = funcNames(rt.MuxMiddleware)
		}
		if rt.Static {
			attrs["static"] = "true"
		}
		if rt.Conditional() {
			attrs["conditional"] = "true"
		}
		id := graph.NodeID(graph.KindRoute, rt.Key())
		err := b.w.AddNode(graph.Node{
			ID:     id,
			Kind:   graph.KindRoute,
			Name:   rt.Key(),
			Detail: strings.Join(rt.Variants, " "),
			Pos:    b.pos(rt.Pos, token.NoPos),
			Attrs:  attrs,
		})
		if err != nil {
			return err
		}
		if rt.Handler == nil {
			continue
		}
		if err := b.addFunc(rt.Handler); err != nil {
			return err
		}
		if err := b.w.AddEdge(graph.Edge{From: id, To: FuncID(rt.Handler), Kind: graph.EdgeHandledBy}); err != nil {
			return err
		}
	}
	return nil
}

// funcNames joins go/ssa names with commas.
func funcNames(fns []*ssa.Function) string {
	names := make([]string, len(fns))
	for i, fn := range fns {
		names[i] = origin(fn).String()
	}
	return strings.Join(names, ",")
}

func (b *builder) addCalls(n *callgraph.Node) error {
	from := FuncID(n.Func)
	scope := b.scopes[n.Func]
	// The call graph's edge order depends on map iteration; sort by call
	// site, then callee, so edge IDs and export order are deterministic.
	out := slices.Clone(n.Out)
	slices.SortFunc(out, func(x, y *callgraph.Edge) int {
		return cmp.Or(cmp.Compare(edgePos(x), edgePos(y)), cmp.Compare(x.Callee.Func.String(), y.Callee.Func.String()))
	})
	for _, e := range out {
		if e.Site == nil || e.Callee.Func == nil {
			continue
		}
		callee := origin(e.Callee.Func)
		common := e.Site.Common()
		pos := b.pos(e.Site.Pos(), token.NoPos)
		methods := scope.methods(e.Site)
		if common.IsInvoke() {
			if err := b.addInvoke(from, common.Method, callee, pos, methods); err != nil {
				return err
			}
			continue
		}
		ok, err := b.addCallee(callee)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		edge := graph.Edge{From: from, To: FuncID(callee), Kind: graph.EdgeCalls, Pos: pos, Attrs: methodAttrs(methods)}
		if common.StaticCallee() == nil {
			edge.Attrs = withAttr(edge.Attrs, "dynamic", "true")
		}
		if err := b.w.AddEdge(edge); err != nil {
			return err
		}
	}
	return nil
}

// addInvoke records a dynamic call through interface method m that may
// dispatch to callee. Interfaces declared outside the module only dispatch
// to module implementations; otherwise io.Closer.Close alone would fan out
// into every closer in the standard library.
func (b *builder) addInvoke(from string, m *types.Func, callee *ssa.Function, pos graph.Pos, methods []string) error {
	ifaceID, ok, err := b.addInterfaceMethod(m)
	if err != nil || !ok {
		return err
	}
	if err := b.w.AddEdge(graph.Edge{From: from, To: ifaceID, Kind: graph.EdgeCalls, Pos: pos, Attrs: methodAttrs(methods)}); err != nil {
		return err
	}
	if !b.inModule(m.Pkg()) && !b.inModule(funcPkg(callee)) {
		return nil
	}
	ok, err = b.addCallee(callee)
	if err != nil || !ok {
		return err
	}
	return b.w.AddEdge(graph.Edge{From: ifaceID, To: FuncID(callee), Kind: graph.EdgeDispatchesTo})
}

func edgePos(e *callgraph.Edge) token.Pos {
	if e.Site == nil {
		return token.NoPos
	}
	return e.Site.Pos()
}

// methodAttrs records the request methods a call is limited to.
func methodAttrs(methods []string) map[string]string {
	if len(methods) == 0 {
		return nil
	}
	return map[string]string{"methods": strings.Join(methods, ",")}
}

func withAttr(attrs map[string]string, k, v string) map[string]string {
	if attrs == nil {
		attrs = map[string]string{}
	}
	attrs[k] = v
	return attrs
}

// addCallee adds fn unless it is excluded; it reports whether fn is in the graph.
func (b *builder) addCallee(fn *ssa.Function) (bool, error) {
	pkg := funcPkg(fn)
	if !b.inModule(pkg) && (pkg == nil || excluded(pkg.Path(), b.exclude)) {
		return false, nil
	}
	return true, b.addFunc(fn)
}

func (b *builder) addFunc(fn *ssa.Function) error {
	fn = origin(fn)
	id := FuncID(fn)
	if b.added[id] {
		return nil
	}
	b.added[id] = true
	pkg := funcPkg(fn)
	n := graph.Node{
		ID:     id,
		Kind:   funcKind(fn),
		Name:   fn.RelString(pkg),
		Detail: types.TypeString(fn.Signature, types.RelativeTo(pkg)),
	}
	if pkg != nil {
		n.Package = pkg.Path()
	}
	if b.inModule(pkg) {
		if syntax := fn.Syntax(); syntax != nil {
			n.Pos = b.pos(syntax.Pos(), syntax.End())
		} else {
			n.Pos = b.pos(fn.Pos(), token.NoPos)
		}
		if scope := b.scopes[fn]; scope != nil {
			n.Attrs = map[string]string{"methodBranches": strings.Join(scope.compared, ",")}
		}
	} else {
		n.Attrs = map[string]string{"external": "true"}
	}
	return b.w.AddNode(n)
}

// addInterfaceMethod adds the node for interface method m, unless its
// package is excluded; it reports whether m is in the graph.
func (b *builder) addInterfaceMethod(m *types.Func) (string, bool, error) {
	pkg := m.Pkg()
	if pkg == nil || (!b.inModule(pkg) && excluded(pkg.Path(), b.exclude)) {
		return "", false, nil
	}
	id := InterfaceMethodID(m)
	if b.added[id] {
		return id, true, nil
	}
	b.added[id] = true
	sig := m.Type().(*types.Signature)
	name := m.Name()
	if named, ok := types.Unalias(sig.Recv().Type()).(*types.Named); ok {
		name = named.Obj().Name() + "." + name
	}
	n := graph.Node{
		ID:      id,
		Kind:    graph.KindInterfaceCall,
		Name:    name,
		Package: pkg.Path(),
		Detail:  types.TypeString(sig, types.RelativeTo(pkg)),
	}
	if b.inModule(pkg) {
		n.Pos = b.pos(m.Pos(), token.NoPos)
	} else {
		n.Attrs = map[string]string{"external": "true"}
	}
	return id, true, b.w.AddNode(n)
}

// addImplements links each concrete named type of the module to each
// non-empty module interface it (or its pointer) implements.
func (b *builder) addImplements() error {
	var ifaces, concretes []*types.TypeName
	for _, p := range b.r.Packages {
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			obj, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || obj.IsAlias() {
				continue
			}
			named, ok := obj.Type().(*types.Named)
			if !ok || named.TypeParams().Len() > 0 {
				continue
			}
			if iface, ok := named.Underlying().(*types.Interface); ok {
				if iface.NumMethods() > 0 {
					ifaces = append(ifaces, obj)
				}
				continue
			}
			concretes = append(concretes, obj)
		}
	}
	for _, c := range concretes {
		for _, i := range ifaces {
			iface := i.Type().Underlying().(*types.Interface)
			var attrs map[string]string
			switch {
			case types.Implements(c.Type(), iface):
			case types.Implements(types.NewPointer(c.Type()), iface):
				attrs = map[string]string{"pointer": "true"}
			default:
				continue
			}
			if err := b.addType(c); err != nil {
				return err
			}
			if err := b.addType(i); err != nil {
				return err
			}
			if err := b.w.AddEdge(graph.Edge{From: TypeID(c), To: TypeID(i), Kind: graph.EdgeImplements, Attrs: attrs}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *builder) addType(obj *types.TypeName) error {
	id := TypeID(obj)
	if b.added[id] {
		return nil
	}
	b.added[id] = true
	kind := "type"
	switch obj.Type().Underlying().(type) {
	case *types.Interface:
		kind = "interface"
	case *types.Struct:
		kind = "struct"
	}
	return b.w.AddNode(graph.Node{
		ID:      id,
		Kind:    graph.KindType,
		Name:    obj.Name(),
		Package: obj.Pkg().Path(),
		Detail:  kind,
		Pos:     b.pos(obj.Pos(), token.NoPos),
	})
}

// pos converts a token range to a graph position with the file relative to
// the module root.
func (b *builder) pos(start, end token.Pos) graph.Pos {
	if !start.IsValid() {
		return graph.Pos{}
	}
	s := b.fset.Position(start)
	p := graph.Pos{File: b.relFile(s.Filename), StartLine: s.Line, StartCol: s.Column}
	if end.IsValid() {
		e := b.fset.Position(end)
		p.EndLine, p.EndCol = e.Line, e.Column
	}
	return p
}

func (b *builder) relFile(name string) string {
	rel, err := filepath.Rel(b.r.Dir, name)
	if err != nil || strings.HasPrefix(rel, "..") {
		return name
	}
	return filepath.ToSlash(rel)
}
