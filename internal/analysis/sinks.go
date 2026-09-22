package analysis

import (
	"cmp"
	"fmt"
	"go/token"
	"slices"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/ssa"

	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/sqlparse"
)

// SinkRule marks calls to Func as a sink of Kind. Arg is the index of the
// argument that identifies what the sink touches (SQL text, URL, file name,
// program, variable), counting parameters after any receiver; -1 for none.
type SinkRule struct {
	Kind graph.NodeKind
	Func string // go/ssa name, e.g. "(*database/sql.DB).QueryContext"
	Arg  int
}

// DefaultSinks are the built-in sink rules.
var DefaultSinks = defaultSinks()

func defaultSinks() []SinkRule {
	var rules []SinkRule
	add := func(kind graph.NodeKind, arg int, funcs ...string) {
		for _, f := range funcs {
			rules = append(rules, SinkRule{Kind: kind, Func: f, Arg: arg})
		}
	}
	for _, recv := range []string{"(*database/sql.DB)", "(*database/sql.Tx)", "(*database/sql.Conn)"} {
		add(graph.KindSinkSQL, 0, recv+".Query", recv+".QueryRow", recv+".Exec", recv+".Prepare")
		add(graph.KindSinkSQL, 1, recv+".QueryContext", recv+".QueryRowContext", recv+".ExecContext", recv+".PrepareContext")
	}
	add(graph.KindSinkSQL, -1, "(*database/sql.Stmt).Query", "(*database/sql.Stmt).QueryRow", "(*database/sql.Stmt).Exec",
		"(*database/sql.Stmt).QueryContext", "(*database/sql.Stmt).QueryRowContext", "(*database/sql.Stmt).ExecContext")
	for _, recv := range []string{
		"(*github.com/jackc/pgx/v5.Conn)", "(*github.com/jackc/pgx/v5/pgxpool.Pool)", "(*github.com/jackc/pgx/v5/pgxpool.Conn)",
		"(*github.com/jackc/pgx/v5/pgxpool.Tx)", "(*github.com/jackc/pgx/v5.dbTx)",
	} {
		add(graph.KindSinkSQL, 1, recv+".Query", recv+".QueryRow", recv+".Exec")
	}

	add(graph.KindSinkFile, 0, "os.Open", "os.OpenFile", "os.Create", "os.ReadFile", "os.WriteFile", "os.ReadDir",
		"os.Remove", "os.RemoveAll", "os.Mkdir", "os.MkdirAll", "os.Rename", "os.CreateTemp", "os.MkdirTemp",
		"(embed.FS).Open", "(embed.FS).ReadFile", "(embed.FS).ReadDir")
	add(graph.KindSinkFile, 1, "io/fs.ReadFile", "io/fs.ReadDir", "io/fs.Glob", "io/fs.WalkDir",
		"html/template.ParseFS", "text/template.ParseFS")
	add(graph.KindSinkFile, 0, "html/template.ParseFiles", "html/template.ParseGlob", "text/template.ParseFiles", "text/template.ParseGlob")
	add(graph.KindSinkFile, 2, "net/http.ServeFile")

	add(graph.KindSinkHTTP, 0, "(*net/http.Client).Do", "(*net/http.Client).Get", "(*net/http.Client).Head",
		"(*net/http.Client).Post", "(*net/http.Client).PostForm", "net/http.Get", "net/http.Head", "net/http.Post", "net/http.PostForm")

	add(graph.KindSinkSMTP, 0, "net/smtp.SendMail", "net/smtp.Dial")

	add(graph.KindSinkExec, 0, "os/exec.Command")
	add(graph.KindSinkExec, 1, "os/exec.CommandContext")

	add(graph.KindSinkEnv, 0, "os.Getenv", "os.LookupEnv")
	add(graph.KindSinkEnv, -1, "os.Environ")
	return rules
}

// Sink is one call site that reaches outside the program.
type Sink struct {
	Kind   graph.NodeKind
	Caller *ssa.Function
	Callee string // go/ssa name of the called function
	// Values are the possible values of the rule's argument (SQL text, URL,
	// ...); Unknown marks parts that could not be determined.
	Values []string
	// FuncValue marks a sink function passed as a value, e.g.
	// config.Load(os.Getenv); its calls happen elsewhere.
	FuncValue bool
	Pos       token.Pos
	// Query is what an SQL sink's text does, merged over all its Values.
	Query *sqlparse.Query
}

// analyzeQueries parses the SQL of every SQL sink.
func analyzeQueries(sinks []Sink, d sqlparse.Dialect) {
	for i := range sinks {
		s := &sinks[i]
		if s.Kind != graph.KindSinkSQL || len(s.Values) == 0 {
			continue
		}
		merged := &sqlparse.Query{}
		for _, v := range s.Values {
			if strings.TrimSpace(v) == Unknown {
				merged.Partial = true
				continue
			}
			q := d.Query(v)
			merged.Partial = merged.Partial || q.Partial
			if merged.Err == nil {
				merged.Err = q.Err
			}
			for _, op := range q.Ops {
				if !slices.Contains(merged.Ops, op) {
					merged.Ops = append(merged.Ops, op)
				}
			}
			for _, t := range q.Tables {
				i := slices.IndexFunc(merged.Tables, func(u sqlparse.TableUse) bool { return u.Name == t.Name })
				if i < 0 {
					merged.Tables = append(merged.Tables, t)
					continue
				}
				if merged.Tables[i].Op == sqlparse.OpSelect {
					merged.Tables[i].Op = t.Op
				}
				merged.Tables[i].Columns = union(merged.Tables[i].Columns, t.Columns)
			}
		}
		s.Query = merged
	}
}

// ID is the sink's graph node ID.
func (s Sink) ID(fset *token.FileSet, rel func(string) string) string {
	p := fset.Position(s.Pos)
	return graph.NodeID(s.Kind, fmt.Sprintf("%s:%d:%d", rel(p.Filename), p.Line, p.Column))
}

// Resolved reports whether every value was determined completely.
func (s Sink) Resolved() bool {
	return len(s.Values) > 0 && !slices.ContainsFunc(s.Values, func(v string) bool { return strings.Contains(v, Unknown) })
}

// detectSinks finds sink calls in the module functions: static calls, calls
// through interfaces that dispatch to a sink (e.g. a module interface over
// *sql.DB and *sql.Tx), and sink functions passed as values
// (config.Load(os.Getenv)).
func detectSinks(r *Result, own []*ssa.Function, rules []SinkRule) []Sink {
	byFunc := map[string]SinkRule{}
	for _, rule := range rules {
		byFunc[rule.Func] = rule
	}
	ev := newEvaluator(r.CallGraph, r.Module)
	var sinks []Sink
	for _, fn := range own {
		seen := map[ssa.CallInstruction]bool{}
		if node := r.CallGraph.Nodes[fn]; node != nil {
			out := slices.Clone(node.Out)
			slices.SortFunc(out, func(x, y *callgraph.Edge) int {
				return cmp.Or(cmp.Compare(edgePos(x), edgePos(y)), cmp.Compare(x.Callee.Func.String(), y.Callee.Func.String()))
			})
			for _, e := range out {
				if e.Site == nil || seen[e.Site] {
					continue
				}
				rule, ok := byFunc[origin(e.Callee.Func).String()]
				if !ok {
					continue
				}
				seen[e.Site] = true
				sinks = append(sinks, Sink{
					Kind:   rule.Kind,
					Caller: fn,
					Callee: rule.Func,
					Values: sinkValues(ev, e.Site.Common(), rule),
					Pos:    e.Site.Pos(),
				})
			}
		}
		sinks = append(sinks, funcValueSinks(fn, byFunc)...)
	}
	return sinks
}

// sinkValues evaluates the rule's argument at a call site.
func sinkValues(ev *evaluator, common *ssa.CallCommon, rule SinkRule) []string {
	if rule.Arg < 0 {
		return nil
	}
	i := rule.Arg
	// A static method call passes the receiver as Args[0]; an interface
	// call passes it separately.
	if !common.IsInvoke() && common.Signature().Recv() != nil {
		i++
	}
	if i >= len(common.Args) {
		return []string{Unknown}
	}
	arg := common.Args[i]
	if rule.Func == "(*net/http.Client).Do" {
		return requestURL(ev, arg)
	}
	if elems, ok := ev.slice(arg, 0); ok { // variadic patterns, e.g. ParseFS(fsys, "*.html")
		var out []string
		for _, el := range elems {
			out = union(out, el)
		}
		return out
	}
	return ev.strings(arg)
}

// requestURL finds the URL of the request built by http.NewRequest*.
func requestURL(ev *evaluator, req ssa.Value) []string {
	call, ok := req.(*ssa.Extract)
	if !ok {
		return []string{Unknown}
	}
	c, ok := call.Tuple.(*ssa.Call)
	if !ok {
		return []string{Unknown}
	}
	fn := c.Common().StaticCallee()
	if fn == nil {
		return []string{Unknown}
	}
	switch fn.String() {
	case "net/http.NewRequest":
		return ev.strings(c.Common().Args[1])
	case "net/http.NewRequestWithContext":
		return ev.strings(c.Common().Args[2])
	}
	return []string{Unknown}
}

// funcValueSinks finds sink functions used as values rather than called,
// e.g. os.Getenv passed to a config loader.
func funcValueSinks(fn *ssa.Function, byFunc map[string]SinkRule) []Sink {
	var out []Sink
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			var operands []*ssa.Value
			operands = instr.Operands(operands)
			if call, ok := instr.(ssa.CallInstruction); ok && call.Common().StaticCallee() != nil {
				operands = operands[1:] // skip the callee itself
			}
			for _, op := range operands {
				f, ok := (*op).(*ssa.Function)
				if !ok {
					continue
				}
				rule, ok := byFunc[f.String()]
				if !ok {
					continue
				}
				pos := instr.Pos()
				if pos == token.NoPos {
					pos = fn.Pos()
				}
				out = append(out, Sink{Kind: rule.Kind, Caller: fn, Callee: rule.Func, FuncValue: true, Pos: pos})
			}
		}
	}
	return out
}
