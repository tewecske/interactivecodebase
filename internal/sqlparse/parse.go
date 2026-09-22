// Package sqlparse reads PostgreSQL: it builds a schema model from migration
// files and extracts the tables, columns and operation of a query.
//
// Parsing uses libpg_query compiled to WebAssembly (github.com/wasilibs/go-pgquery),
// so it needs no cgo. The first parse compiles the module (about a second);
// call Warm early to do that in the background.
package sqlparse

import (
	"regexp"
	"strings"
	"sync"

	pg "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
)

// Unknown is the analysis placeholder for statically unknown text. Before
// parsing it is replaced by a harmless identifier so partial SQL still parses.
const Unknown = "{?}"

const unknownIdent = "icb_unknown"

var (
	warmOnce       sync.Once
	unknownParamRE = regexp.MustCompile(`\$(\{\?\})+`)
)

// Warm compiles the parser in the background so the first real parse does
// not pay for it.
func Warm() {
	warmOnce.Do(func() {
		go func() { _, _ = pgquery.Parse("SELECT 1") }()
	})
}

// statement is one parsed statement and its byte offset in the input.
type statement struct {
	node   *pg.Node
	offset int
}

// parse parses sql into statements, with Unknown parts replaced by an
// identifier. partial reports whether any were replaced.
func parse(sql string) (stmts []statement, partial bool, err error) {
	partial = strings.Contains(sql, Unknown)
	// "$" + strconv.Itoa(n) leaves "${?}"; it is still a bind parameter.
	sql = unknownParamRE.ReplaceAllString(sql, "$$1")
	res, err := pgquery.Parse(strings.ReplaceAll(sql, Unknown, unknownIdent))
	if err != nil {
		return nil, partial, err
	}
	for _, raw := range res.GetStmts() {
		stmts = append(stmts, statement{node: raw.GetStmt(), offset: int(raw.GetStmtLocation())})
	}
	return stmts, partial, nil
}

// relName returns "schema.table", or "table" when no schema is given.
func relName(rv *pg.RangeVar) string {
	if rv == nil {
		return ""
	}
	if rv.GetSchemaname() != "" {
		return rv.GetSchemaname() + "." + rv.GetRelname()
	}
	return rv.GetRelname()
}

// strs returns the String values of nodes, skipping other node types.
func strs(nodes []*pg.Node) []string {
	var out []string
	for _, n := range nodes {
		if s := n.GetString_(); s != nil {
			out = append(out, s.GetSval())
		}
	}
	return out
}

// lineAt returns the 1-based line of byte offset off in text.
func lineAt(text string, off int) int {
	if off > len(text) {
		off = len(text)
	}
	return strings.Count(text[:off], "\n") + 1
}
