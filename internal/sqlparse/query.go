package sqlparse

import (
	"regexp"
	"slices"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Operations on a table.
const (
	OpSelect = "select"
	OpInsert = "insert"
	OpUpdate = "update"
	OpDelete = "delete"
	OpMerge  = "merge"
	OpDDL    = "ddl"
	OpOther  = "other"
)

// Query is what one SQL text does.
type Query struct {
	// Ops are the statement operations in order, e.g. ["insert"].
	Ops []string
	// Tables are the tables touched, in order of first appearance.
	Tables []TableUse
	// Partial is set when part of the SQL was unknown ({?}) or it could
	// only be read heuristically after a parse error.
	Partial bool
	// Err is the parse error, if any; Tables then come from a regex scan.
	Err error
}

// TableUse is one table a query touches.
type TableUse struct {
	Name string
	// Op is the write operation for a statement's target table, else
	// "select" for tables it only reads.
	Op string
	// Columns read or written, "*" for all.
	Columns []string
}

// Dialect analyzes queries of one SQL dialect.
type Dialect interface {
	Name() string
	Query(sql string) Query
}

// Postgres is the PostgreSQL dialect.
var Postgres Dialect = postgres{}

type postgres struct{}

func (postgres) Name() string { return "postgres" }

// Query analyzes sql, which may contain several statements.
func (postgres) Query(sql string) Query {
	stmts, partial, err := parse(sql)
	if err != nil {
		q := scanTables(sql)
		q.Partial, q.Err = true, err
		return q
	}
	q := Query{Partial: partial}
	for _, st := range stmts {
		q.Ops = append(q.Ops, stmtOp(st.node))
		analyzeStmt(&q, st.node)
	}
	return q
}

func stmtOp(n *pg.Node) string {
	switch n.GetNode().(type) {
	case *pg.Node_SelectStmt:
		return OpSelect
	case *pg.Node_InsertStmt:
		return OpInsert
	case *pg.Node_UpdateStmt:
		return OpUpdate
	case *pg.Node_DeleteStmt:
		return OpDelete
	case *pg.Node_MergeStmt:
		return OpMerge
	case *pg.Node_CreateStmt, *pg.Node_AlterTableStmt, *pg.Node_DropStmt, *pg.Node_IndexStmt,
		*pg.Node_CreateSchemaStmt, *pg.Node_RenameStmt, *pg.Node_TruncateStmt:
		return OpDDL
	}
	return OpOther
}

// analyzeStmt records the tables and columns one statement touches.
func analyzeStmt(q *Query, root *pg.Node) {
	targets := map[*pg.RangeVar]string{} // write targets and their op
	ctes := map[string]bool{}
	aliases := map[string]string{} // alias or name -> table
	var rels []*pg.RangeVar
	var colRefs []*pg.ColumnRef
	written := map[*pg.RangeVar][]string{}

	walk(root, func(m proto.Message) {
		switch n := m.(type) {
		case *pg.InsertStmt:
			targets[n.GetRelation()] = OpInsert
			for _, c := range n.GetCols() {
				written[n.GetRelation()] = append(written[n.GetRelation()], c.GetResTarget().GetName())
			}
		case *pg.UpdateStmt:
			targets[n.GetRelation()] = OpUpdate
			for _, c := range n.GetTargetList() {
				written[n.GetRelation()] = append(written[n.GetRelation()], c.GetResTarget().GetName())
			}
		case *pg.DeleteStmt:
			targets[n.GetRelation()] = OpDelete
		case *pg.MergeStmt:
			targets[n.GetRelation()] = OpMerge
		case *pg.CreateStmt:
			targets[n.GetRelation()] = OpDDL
		case *pg.AlterTableStmt:
			targets[n.GetRelation()] = OpDDL
		case *pg.TruncateStmt:
			for _, r := range n.GetRelations() {
				targets[r.GetRangeVar()] = OpDDL
			}
		case *pg.CommonTableExpr:
			ctes[n.GetCtename()] = true
		case *pg.RangeVar:
			rels = append(rels, n)
		case *pg.ColumnRef:
			colRefs = append(colRefs, n)
		}
	})

	for _, rv := range rels {
		name := relName(rv)
		if rv.GetSchemaname() == "" && ctes[name] {
			continue
		}
		op, ok := targets[rv]
		if !ok {
			op = OpSelect
		}
		aliases[name] = name
		if a := rv.GetAlias().GetAliasname(); a != "" {
			aliases[a] = name
		}
		use := q.use(name, op)
		use.Columns = union(use.Columns, written[rv]...)
	}

	// Column references: qualified ones by alias, bare ones only when the
	// statement touches a single table.
	var tables []string
	for _, rv := range rels {
		if name := relName(rv); !ctes[name] && !slices.Contains(tables, name) {
			tables = append(tables, name)
		}
	}
	for _, ref := range colRefs {
		fields := ref.GetFields()
		col := ""
		if last := fields[len(fields)-1]; last.GetAStar() != nil {
			col = "*"
		} else {
			col = last.GetString_().GetSval()
		}
		table := ""
		switch {
		case len(fields) >= 2:
			table = aliases[fields[len(fields)-2].GetString_().GetSval()]
		case len(tables) == 1:
			table = tables[0]
		}
		if table == "" || col == "" || col == unknownIdent {
			continue
		}
		if i := slices.IndexFunc(q.Tables, func(u TableUse) bool { return u.Name == table }); i >= 0 {
			q.Tables[i].Columns = union(q.Tables[i].Columns, col)
		}
	}
}

// use returns the TableUse for name, adding it if needed. A write op
// replaces an earlier select.
func (q *Query) use(name, op string) *TableUse {
	for i := range q.Tables {
		if q.Tables[i].Name == name {
			if q.Tables[i].Op == OpSelect && op != OpSelect {
				q.Tables[i].Op = op
			}
			return &q.Tables[i]
		}
	}
	q.Tables = append(q.Tables, TableUse{Name: name, Op: op})
	return &q.Tables[len(q.Tables)-1]
}

// walk calls fn for every protobuf message in the tree, depth first.
func walk(n *pg.Node, fn func(proto.Message)) {
	if n == nil {
		return
	}
	walkMsg(n.ProtoReflect(), fn)
}

func walkMsg(m protoreflect.Message, fn func(proto.Message)) {
	if !m.IsValid() {
		return
	}
	fn(m.Interface())
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsList() && fd.Message() != nil:
			list := v.List()
			for i := range list.Len() {
				walkMsg(list.Get(i).Message(), fn)
			}
		case fd.Message() != nil && !fd.IsMap():
			walkMsg(v.Message(), fn)
		}
		return true
	})
}

func union(xs []string, ys ...string) []string {
	for _, y := range ys {
		if y != "" && !slices.Contains(xs, y) {
			xs = append(xs, y)
		}
	}
	return xs
}

var (
	firstWordRE = regexp.MustCompile(`(?is)^\s*(?:with\b.*?\)\s*)?(select|insert|update|delete|merge|create|alter|drop|truncate)\b`)
	tableRE     = regexp.MustCompile(`(?i)\b(from|join|into|update|table)\s+("?[A-Za-z_][\w$]*"?(?:\."?[A-Za-z_][\w$]*"?)?)`)
)

// scanTables is the fallback for SQL that does not parse: the statement
// keyword and every identifier after FROM, JOIN, INTO, UPDATE or TABLE.
func scanTables(sql string) Query {
	var q Query
	op := OpOther
	if m := firstWordRE.FindStringSubmatch(sql); m != nil {
		switch w := strings.ToLower(m[1]); w {
		case "create", "alter", "drop", "truncate":
			op = OpDDL
		default:
			op = w
		}
	}
	q.Ops = []string{op}
	for _, m := range tableRE.FindAllStringSubmatch(sql, -1) {
		name := strings.ReplaceAll(m[2], `"`, "")
		if name == unknownIdent || strings.EqualFold(name, "select") {
			continue
		}
		tableOp := OpSelect
		switch strings.ToLower(m[1]) {
		case "into":
			tableOp = op
		case "update":
			tableOp = OpUpdate
		case "table":
			tableOp = OpDDL
		case "from":
			if op == OpDelete {
				tableOp = OpDelete
			}
		}
		q.use(name, tableOp)
	}
	return q
}
