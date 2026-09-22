package sqlparse

import (
	"fmt"
	"slices"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
)

// Schema is the database schema that results from applying DDL in order.
type Schema struct {
	// Tables in creation order.
	Tables []*Table
	// Errors are statements that could not be parsed, with their location.
	Errors []error
}

// Table is one table.
type Table struct {
	Name        string
	Columns     []Column
	PrimaryKey  []string
	ForeignKeys []ForeignKey
	Indexes     []Index
	Pos         Pos // CREATE TABLE statement
}

// Column is one table column.
type Column struct {
	Name    string
	Type    string
	NotNull bool
	Pos     Pos
}

// ForeignKey references another table.
type ForeignKey struct {
	Name       string
	Columns    []string
	RefTable   string
	RefColumns []string
	// OnDelete is "cascade", "set null", "set default", "restrict", or ""
	// for the default NO ACTION.
	OnDelete string
	Pos      Pos
}

// Index is a secondary index.
type Index struct {
	Name    string
	Columns []string
	Unique  bool
	Pos     Pos
}

// Pos is a location in a migration file.
type Pos struct {
	File string
	Line int
}

// Table returns the named table, or nil.
func (s *Schema) Table(name string) *Table {
	i := slices.IndexFunc(s.Tables, func(t *Table) bool { return t.Name == name })
	if i < 0 {
		return nil
	}
	return s.Tables[i]
}

// Column returns the named column, or nil.
func (t *Table) Column(name string) *Column {
	i := slices.IndexFunc(t.Columns, func(c Column) bool { return c.Name == name })
	if i < 0 {
		return nil
	}
	return &t.Columns[i]
}

// Apply parses sql from file and applies its DDL to the schema. Statements
// that are not DDL are ignored. A parse error is recorded in s.Errors.
func (s *Schema) Apply(file, sql string) {
	stmts, _, err := parse(sql)
	if err != nil {
		s.Errors = append(s.Errors, fmt.Errorf("%s: %w", file, err))
		return
	}
	for _, st := range stmts {
		pos := Pos{File: file, Line: lineAt(sql, st.offset+leadingSpace(sql[min(st.offset, len(sql)):]))}
		switch n := st.node.GetNode().(type) {
		case *pg.Node_CreateStmt:
			s.create(n.CreateStmt, pos)
		case *pg.Node_AlterTableStmt:
			s.alter(n.AlterTableStmt, pos)
		case *pg.Node_DropStmt:
			s.drop(n.DropStmt)
		case *pg.Node_RenameStmt:
			s.rename(n.RenameStmt)
		case *pg.Node_IndexStmt:
			s.index(n.IndexStmt, pos)
		}
	}
}

// leadingSpace counts whitespace and comment lines before a statement so
// its position points at the statement, not the preceding blank lines.
func leadingSpace(s string) int {
	n := 0
	for n < len(s) {
		switch {
		case s[n] == ' ' || s[n] == '\t' || s[n] == '\n' || s[n] == '\r':
			n++
		case strings.HasPrefix(s[n:], "--"):
			end := strings.IndexByte(s[n:], '\n')
			if end < 0 {
				return len(s)
			}
			n += end + 1
		default:
			return n
		}
	}
	return n
}

func (s *Schema) create(c *pg.CreateStmt, pos Pos) {
	name := relName(c.GetRelation())
	if s.Table(name) != nil {
		return // CREATE TABLE IF NOT EXISTS on an existing table
	}
	t := &Table{Name: name, Pos: pos}
	for _, el := range c.GetTableElts() {
		switch {
		case el.GetColumnDef() != nil:
			t.addColumn(el.GetColumnDef(), pos)
		case el.GetConstraint() != nil:
			t.addConstraint(el.GetConstraint(), nil, pos)
		}
	}
	s.Tables = append(s.Tables, t)
}

func (t *Table) addColumn(cd *pg.ColumnDef, pos Pos) {
	col := Column{Name: cd.GetColname(), Type: typeName(cd.GetTypeName()), NotNull: cd.GetIsNotNull(), Pos: pos}
	for _, c := range cd.GetConstraints() {
		con := c.GetConstraint()
		if con == nil {
			continue
		}
		switch con.GetContype() {
		case pg.ConstrType_CONSTR_NOTNULL:
			col.NotNull = true
		case pg.ConstrType_CONSTR_PRIMARY:
			col.NotNull = true
		}
		t.addConstraint(con, []string{col.Name}, pos)
	}
	t.Columns = append(t.Columns, col)
}

// addConstraint records primary keys, foreign keys and unique constraints.
// columns are the owning columns for a column constraint.
func (t *Table) addConstraint(con *pg.Constraint, columns []string, pos Pos) {
	switch con.GetContype() {
	case pg.ConstrType_CONSTR_PRIMARY:
		if keys := strs(con.GetKeys()); len(keys) > 0 {
			columns = keys
		}
		t.PrimaryKey = columns
	case pg.ConstrType_CONSTR_UNIQUE:
		if keys := strs(con.GetKeys()); len(keys) > 0 {
			columns = keys
		}
		t.Indexes = append(t.Indexes, Index{Name: con.GetConname(), Columns: columns, Unique: true, Pos: pos})
	case pg.ConstrType_CONSTR_FOREIGN:
		if attrs := strs(con.GetFkAttrs()); len(attrs) > 0 {
			columns = attrs
		}
		t.ForeignKeys = append(t.ForeignKeys, ForeignKey{
			Name:       con.GetConname(),
			Columns:    columns,
			RefTable:   relName(con.GetPktable()),
			RefColumns: strs(con.GetPkAttrs()),
			OnDelete:   fkAction(con.GetFkDelAction()),
			Pos:        pos,
		})
	}
}

func (s *Schema) alter(a *pg.AlterTableStmt, pos Pos) {
	t := s.Table(relName(a.GetRelation()))
	if t == nil {
		return
	}
	for _, c := range a.GetCmds() {
		cmd := c.GetAlterTableCmd()
		if cmd == nil {
			continue
		}
		switch cmd.GetSubtype() {
		case pg.AlterTableType_AT_AddColumn:
			if cd := cmd.GetDef().GetColumnDef(); cd != nil && t.Column(cd.GetColname()) == nil {
				t.addColumn(cd, pos)
			}
		case pg.AlterTableType_AT_DropColumn:
			t.Columns = slices.DeleteFunc(t.Columns, func(c Column) bool { return c.Name == cmd.GetName() })
			t.ForeignKeys = slices.DeleteFunc(t.ForeignKeys, func(fk ForeignKey) bool { return slices.Contains(fk.Columns, cmd.GetName()) })
		case pg.AlterTableType_AT_AddConstraint:
			if con := cmd.GetDef().GetConstraint(); con != nil {
				t.addConstraint(con, nil, pos)
			}
		case pg.AlterTableType_AT_DropConstraint:
			t.ForeignKeys = slices.DeleteFunc(t.ForeignKeys, func(fk ForeignKey) bool { return fk.Name == cmd.GetName() })
			t.Indexes = slices.DeleteFunc(t.Indexes, func(ix Index) bool { return ix.Name == cmd.GetName() })
		}
	}
}

func (s *Schema) drop(d *pg.DropStmt) {
	switch d.GetRemoveType() {
	case pg.ObjectType_OBJECT_TABLE:
		for _, obj := range d.GetObjects() {
			name := strings.Join(strs(obj.GetList().GetItems()), ".")
			s.Tables = slices.DeleteFunc(s.Tables, func(t *Table) bool { return t.Name == name })
		}
	case pg.ObjectType_OBJECT_INDEX:
		for _, obj := range d.GetObjects() {
			items := strs(obj.GetList().GetItems())
			if len(items) == 0 {
				continue
			}
			name := items[len(items)-1]
			for _, t := range s.Tables {
				t.Indexes = slices.DeleteFunc(t.Indexes, func(ix Index) bool { return ix.Name == name })
			}
		}
	}
}

func (s *Schema) rename(r *pg.RenameStmt) {
	t := s.Table(relName(r.GetRelation()))
	if t == nil {
		return
	}
	switch r.GetRenameType() {
	case pg.ObjectType_OBJECT_TABLE:
		old := t.Name
		t.Name = r.GetNewname()
		for _, other := range s.Tables {
			for i := range other.ForeignKeys {
				if other.ForeignKeys[i].RefTable == old {
					other.ForeignKeys[i].RefTable = t.Name
				}
			}
		}
	case pg.ObjectType_OBJECT_COLUMN:
		if c := t.Column(r.GetSubname()); c != nil {
			c.Name = r.GetNewname()
		}
	}
}

func (s *Schema) index(ix *pg.IndexStmt, pos Pos) {
	t := s.Table(relName(ix.GetRelation()))
	if t == nil {
		return
	}
	var cols []string
	for _, p := range ix.GetIndexParams() {
		if el := p.GetIndexElem(); el != nil && el.GetName() != "" {
			cols = append(cols, el.GetName())
		}
	}
	t.Indexes = append(t.Indexes, Index{Name: ix.GetIdxname(), Columns: cols, Unique: ix.GetUnique(), Pos: pos})
}

// typeName renders a column type, dropping the pg_catalog qualifier.
func typeName(tn *pg.TypeName) string {
	names := strs(tn.GetNames())
	if len(names) > 1 && names[0] == "pg_catalog" {
		names = names[1:]
	}
	name := strings.Join(names, ".")
	if len(tn.GetArrayBounds()) > 0 {
		name += "[]"
	}
	return name
}

func fkAction(a string) string {
	switch a {
	case "c":
		return "cascade"
	case "n":
		return "set null"
	case "d":
		return "set default"
	case "r":
		return "restrict"
	}
	return ""
}
