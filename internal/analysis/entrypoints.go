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

// Entry point kinds: ways into the program other than HTTP routes.
const (
	EntryWorker   = "worker"   // goroutine started from package main
	EntryJob      = "job"      // function a worker runs through a job table
	EntryCommand  = "command"  // CLI command (cobra)
	EntryRPC      = "rpc"      // gRPC service method
	EntryConsumer = "consumer" // message subscription callback
)

// EntryPoint is a non-HTTP entry point.
type EntryPoint struct {
	Kind string
	// Name identifies the entry point for people: the job or command
	// name, "Service/Method", the subject, or the function started.
	Name string
	// Func is the function that runs.
	Func *ssa.Function
	// Parent is the name of the worker running a job, or of the parent
	// command.
	Parent string
	// Detail says more, e.g. the queue group of a consumer.
	Detail string
	Pos    token.Pos
}

// Key identifies the entry point as "kind name".
func (e EntryPoint) Key() string { return e.Kind + " " + e.Name }

// discoverEntryPoints finds workers, their jobs, commands, gRPC services
// and message consumers.
func discoverEntryPoints(r *Result, own []*ssa.Function) []EntryPoint {
	d := &entryFinder{r: r, own: own, ev: r.evaluator()}
	for _, fn := range own {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				switch instr := instr.(type) {
				case *ssa.Go:
					if inMain(fn) {
						d.worker(instr)
					}
				case *ssa.Call:
					d.call(fn, instr)
				case *ssa.Store:
					d.store(instr)
				}
			}
		}
	}
	d.commandEntries()
	slices.SortFunc(d.out, func(a, b EntryPoint) int {
		return cmp.Or(cmp.Compare(a.Pos, b.Pos), cmp.Compare(a.Key(), b.Key()))
	})
	// The same entry registered twice (e.g. from two call paths) is one.
	return slices.CompactFunc(d.out, func(a, b EntryPoint) bool { return a.Key() == b.Key() && a.Func == b.Func })
}

type entryFinder struct {
	r   *Result
	own []*ssa.Function
	ev  *evaluator
	out []EntryPoint
	// commands are cobra.Command literals by the value holding them.
	commands []*cobraCommand
}

// mine reports whether fn is a module function with a body.
func (d *entryFinder) mine(fn *ssa.Function) bool {
	return len(fn.Blocks) > 0 && inModule(d.r.Module, funcPkg(fn))
}

// inMain reports whether fn (or the function it is nested in) belongs to
// a main package.
func inMain(fn *ssa.Function) bool {
	for fn.Parent() != nil {
		fn = fn.Parent()
	}
	return fn.Pkg != nil && fn.Pkg.Pkg.Name() == "main"
}

// worker records a goroutine and the jobs it runs.
func (d *entryFinder) worker(g *ssa.Go) {
	target := d.goTarget(g.Common())
	if target == nil {
		return
	}
	name := shortFuncName(origin(target).String())
	d.out = append(d.out, EntryPoint{Kind: EntryWorker, Name: name, Func: target, Pos: g.Pos()})
	d.jobs(target, name)
}

// goTarget is the function a go statement runs: the callee, or for a
// closure the first module function it calls (go func() { w.Run(ctx) }()).
func (d *entryFinder) goTarget(c *ssa.CallCommon) *ssa.Function {
	fn := c.StaticCallee()
	if fn == nil {
		return nil
	}
	if fn.Parent() == nil || fn.Synthetic != "" {
		return closureTarget(fn)
	}
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			call, ok := instr.(ssa.CallInstruction)
			if !ok {
				continue
			}
			for _, callee := range d.callees(call) {
				if d.mine(callee) {
					return callee
				}
			}
		}
	}
	return fn
}

// callees are the functions a call site may run, unwrapping bound-method
// wrappers.
func (d *entryFinder) callees(call ssa.CallInstruction) []*ssa.Function {
	if fn := call.Common().StaticCallee(); fn != nil {
		return []*ssa.Function{closureTarget(fn)}
	}
	node := d.r.CallGraph.Nodes[call.Parent()]
	if node == nil {
		return nil
	}
	var out []*ssa.Function
	for _, e := range node.Out {
		if e.Site == call && !slices.Contains(out, e.Callee.Func) {
			out = append(out, e.Callee.Func)
		}
	}
	return sortFuncs(out)
}

// jobs finds the functions a worker runs through a function-valued struct
// field (job.Run(ctx) over a table of {Name, Run} jobs), naming each by
// the string field of the literal that holds it.
func (d *entryFinder) jobs(worker *ssa.Function, workerName string) {
	seen := map[*ssa.Function]bool{}
	queue := []*ssa.Function{worker}
	for len(queue) > 0 && len(seen) < 64 {
		fn := queue[0]
		queue = queue[1:]
		if seen[fn] || !d.mine(fn) {
			continue
		}
		seen[fn] = true
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				call, ok := instr.(ssa.CallInstruction)
				if !ok {
					continue
				}
				if field, ok := funcField(call.Common()); ok {
					d.jobCallees(call, field, workerName)
					continue
				}
				queue = append(queue, d.callees(call)...)
			}
		}
	}
}

// fieldRef is a struct field.
type fieldRef struct {
	t     types.Type
	index int
}

// funcField reports whether a call calls a function stored in a struct
// field: job.Run(ctx).
func funcField(c *ssa.CallCommon) (fieldRef, bool) {
	if c.IsInvoke() || c.StaticCallee() != nil {
		return fieldRef{}, false
	}
	switch v := c.Value.(type) {
	case *ssa.Field:
		return fieldRef{v.X.Type(), v.Field}, true
	case *ssa.UnOp:
		if fa, ok := v.X.(*ssa.FieldAddr); ok && v.Op == token.MUL {
			return fieldRef{fa.X.Type().Underlying().(*types.Pointer).Elem(), fa.Field}, true
		}
	}
	return fieldRef{}, false
}

func (d *entryFinder) jobCallees(call ssa.CallInstruction, field fieldRef, workerName string) {
	for _, callee := range d.callees(call) {
		if !d.mine(callee) {
			continue
		}
		// A bound method value runs through a wrapper; the job is the
		// method.
		target := closureTarget(callee)
		name := d.jobName(callee, field)
		if name == "" {
			name = shortFuncName(origin(target).String())
		}
		d.out = append(d.out, EntryPoint{Kind: EntryJob, Name: name, Func: target, Parent: workerName, Pos: target.Pos()})
	}
}

// jobName finds the struct literal storing fn into the field and returns
// its name: the value of a string field called Name, ID or Kind, or else
// its first string field.
func (d *entryFinder) jobName(fn *ssa.Function, field fieldRef) string {
	for _, own := range d.own {
		for _, b := range own.Blocks {
			for _, instr := range b.Instrs {
				st, ok := instr.(*ssa.Store)
				if !ok {
					continue
				}
				fa, ok := st.Addr.(*ssa.FieldAddr)
				if !ok || fa.Field != field.index || !types.Identical(fa.X.Type().Underlying().(*types.Pointer).Elem(), field.t) {
					continue
				}
				if !slices.Contains(valueFuncs(st.Val, 0), fn) {
					continue
				}
				if name := d.literalName(fa.X, field.t); name != "" {
					return name
				}
			}
		}
	}
	return ""
}

var nameFieldRE = regexp.MustCompile(`(?i)^(name|id|kind|use)$`)

// literalName evaluates the name-like string field stored through the
// same struct address.
func (d *entryFinder) literalName(x ssa.Value, t types.Type) string {
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return ""
	}
	best := -1
	for i := range st.NumFields() {
		f := st.Field(i)
		if b, ok := f.Type().Underlying().(*types.Basic); !ok || b.Kind() != types.String {
			continue
		}
		if nameFieldRE.MatchString(f.Name()) {
			best = i
			break
		}
		if best < 0 {
			best = i
		}
	}
	if best < 0 || x.Referrers() == nil {
		return ""
	}
	for _, ref := range *x.Referrers() {
		fa, ok := ref.(*ssa.FieldAddr)
		if !ok || fa.Field != best {
			continue
		}
		if vals := fieldStores(d.ev, fa, 0); len(vals) > 0 && !slices.Contains(vals, Unknown) {
			return strings.Join(vals, "|")
		}
	}
	return ""
}

// valueFuncs resolves a function value: a function, a closure or bound
// method, or a module factory returning one.
func valueFuncs(v ssa.Value, depth int) []*ssa.Function {
	if depth > 4 {
		return nil
	}
	switch v := v.(type) {
	case *ssa.Function:
		return []*ssa.Function{v}
	case *ssa.MakeClosure:
		return []*ssa.Function{v.Fn.(*ssa.Function), closureTarget(v.Fn.(*ssa.Function))}
	case *ssa.ChangeType:
		return valueFuncs(v.X, depth+1)
	case *ssa.MakeInterface:
		return valueFuncs(v.X, depth+1)
	case *ssa.Call:
		fn := v.Common().StaticCallee()
		if fn == nil {
			return nil
		}
		var out []*ssa.Function
		for _, ret := range returns(fn) {
			out = append(out, valueFuncs(ret, depth+1)...)
		}
		return out
	}
	return nil
}

// consumers are message subscription methods: the index of the subject
// argument, the callback argument and an optional queue group argument
// (after the receiver; -1 for none).
var consumers = map[string]struct{ subject, callback, queue int }{
	"(*github.com/nats-io/nats.go.Conn).Subscribe":        {0, 1, -1},
	"(*github.com/nats-io/nats.go.Conn).QueueSubscribe":   {0, 2, 1},
	"(*github.com/nats-io/nats.go.EncodedConn).Subscribe": {0, 1, -1},
	"(*cloud.google.com/go/pubsub.Subscription).Receive":  {-1, 1, -1},
	"(*cloud.google.com/go/pubsub/v2.Subscriber).Receive": {-1, 1, -1},
}

// grpcRegister matches generated gRPC service registration functions.
var grpcRegister = regexp.MustCompile(`^Register(\w+)Server$`)

// call records consumers and gRPC services registered by a call.
func (d *entryFinder) call(fn *ssa.Function, call *ssa.Call) {
	callee := call.Common().StaticCallee()
	if callee == nil {
		return
	}
	args := call.Common().Args
	if callee.Signature.Recv() != nil {
		args = args[1:]
	}
	if c, ok := consumers[callee.String()]; ok && c.callback < len(args) {
		name := shortFuncName(callee.String())
		if c.subject >= 0 {
			name = strings.Join(d.ev.strings(args[c.subject]), "|")
		}
		detail := ""
		if c.queue >= 0 {
			detail = "queue " + strings.Join(d.ev.strings(args[c.queue]), "|")
		}
		for _, target := range valueFuncs(args[c.callback], 0) {
			if d.mine(target) && target.Synthetic == "" {
				d.out = append(d.out, EntryPoint{Kind: EntryConsumer, Name: name, Func: target, Detail: detail, Pos: call.Pos()})
				break
			}
		}
		return
	}
	m := grpcRegister.FindStringSubmatch(callee.Name())
	if m == nil || len(args) != 2 || callee.Signature.Recv() != nil {
		return
	}
	iface, ok := callee.Signature.Params().At(1).Type().Underlying().(*types.Interface)
	if !ok {
		return
	}
	impl := args[1]
	if mi, ok := impl.(*ssa.MakeInterface); ok {
		impl = mi.X
	}
	prog := fn.Prog
	mset := prog.MethodSets.MethodSet(impl.Type())
	service := callee.Pkg.Pkg.Name() + "." + m[1]
	for i := range iface.NumMethods() {
		method := iface.Method(i)
		if !method.Exported() {
			continue // mustEmbedUnimplemented...
		}
		sel := mset.Lookup(method.Pkg(), method.Name())
		if sel == nil {
			continue
		}
		target := prog.MethodValue(sel)
		if target == nil || !d.mine(target) || target.Synthetic != "" {
			continue // promoted from the embedded Unimplemented stub
		}
		d.out = append(d.out, EntryPoint{Kind: EntryRPC, Name: service + "/" + method.Name(), Func: target, Pos: call.Pos()})
	}
}

// cobraCommand is a cobra.Command literal.
type cobraCommand struct {
	addr   ssa.Value // the address the literal is built at
	use    string
	run    []*ssa.Function
	pos    token.Pos
	parent *cobraCommand
}

const cobraCommandType = "github.com/spf13/cobra.Command"

// store collects the fields of cobra.Command literals.
func (d *entryFinder) store(st *ssa.Store) {
	fa, ok := st.Addr.(*ssa.FieldAddr)
	if !ok {
		return
	}
	ptr, ok := fa.X.Type().Underlying().(*types.Pointer)
	if !ok {
		return
	}
	named, ok := types.Unalias(ptr.Elem()).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path()+"."+named.Obj().Name() != cobraCommandType {
		return
	}
	i := slices.IndexFunc(d.commands, func(c *cobraCommand) bool { return c.addr == fa.X })
	if i < 0 {
		d.commands = append(d.commands, &cobraCommand{addr: fa.X, pos: fa.Pos()})
		i = len(d.commands) - 1
	}
	c := d.commands[i]
	switch named.Underlying().(*types.Struct).Field(fa.Field).Name() {
	case "Use":
		use := d.ev.strings(st.Val)
		if len(use) > 0 {
			c.use, _, _ = strings.Cut(use[0], " ")
		}
	case "Run", "RunE":
		for _, fn := range valueFuncs(st.Val, 0) {
			if d.mine(fn) && fn.Synthetic == "" {
				c.run = append(c.run, fn)
				break
			}
		}
	}
	if c.pos == token.NoPos {
		c.pos = st.Pos()
	}
}

// commandEntries links commands to their parents through AddCommand and
// records the ones that run something.
func (d *entryFinder) commandEntries() {
	if len(d.commands) == 0 {
		return
	}
	byValue := func(v ssa.Value) *cobraCommand {
		for _, c := range d.commands {
			if c.addr == v {
				return c
			}
		}
		// A command returned by a constructor: newServeCmd().
		if call, ok := v.(*ssa.Call); ok {
			if fn := call.Common().StaticCallee(); fn != nil {
				for _, ret := range returns(fn) {
					for _, c := range d.commands {
						if c.addr == ret {
							return c
						}
					}
				}
			}
		}
		return nil
	}
	for _, fn := range d.own {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				call, ok := instr.(*ssa.Call)
				if !ok {
					continue
				}
				callee := call.Common().StaticCallee()
				if callee == nil || callee.String() != "(*"+cobraCommandType+").AddCommand" {
					continue
				}
				parent := byValue(call.Common().Args[0])
				for _, child := range sliceValues(call.Common().Args[1]) {
					if c := byValue(child); c != nil && parent != nil && c != parent {
						c.parent = parent
					}
				}
			}
		}
	}
	for _, c := range d.commands {
		if len(c.run) == 0 {
			continue
		}
		var path []string
		for p := c; p != nil && len(path) < 8; p = p.parent {
			path = append([]string{cmp.Or(p.use, Unknown)}, path...)
		}
		parent := ""
		if len(path) > 1 {
			parent = strings.Join(path[:len(path)-1], " ")
		}
		d.out = append(d.out, EntryPoint{Kind: EntryCommand, Name: strings.Join(path, " "), Func: c.run[0], Parent: parent, Pos: c.pos})
	}
}
