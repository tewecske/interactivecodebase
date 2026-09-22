package mermaid

import (
	"strings"

	"github.com/tewecske/interactivecodebase/internal/graph"
)

// Table is a table with its columns, for an ER diagram.
type Table struct {
	Node    graph.Node
	Columns []graph.Node
}

// ER draws tables with their columns and foreign keys. fks are fk edges
// between the given tables (child -> parent).
func ER(tables []Table, fks []graph.Edge) Diagram {
	b := newBuilder("erDiagram", "t")
	names := map[string]string{}
	for _, t := range tables {
		b.id(t.Node.ID)
		name := ident(t.Node.Name)
		names[t.Node.ID] = name
		fkCols := map[string]bool{}
		for _, fk := range fks {
			if fk.From == t.Node.ID {
				for _, c := range strings.Split(fk.Attrs["columns"], ",") {
					fkCols[c] = true
				}
			}
		}
		b.line("  %s {", name)
		for _, c := range t.Columns {
			var keys []string
			if c.Attrs["primaryKey"] == "true" {
				keys = append(keys, "PK")
			}
			if fkCols[c.Name] {
				keys = append(keys, "FK")
			}
			typ := ident(strings.ReplaceAll(c.Detail, "[]", "_array"))
			b.line("    %s %s %s", typ, ident(c.Name), strings.Join(keys, ","))
		}
		b.line("  }")
	}
	for _, fk := range fks {
		child, okc := names[fk.From]
		parent, okp := names[fk.To]
		if !okc || !okp {
			continue
		}
		text := fk.Attrs["columns"]
		if od := fk.Attrs["onDelete"]; od != "" {
			text += " on delete " + od
		}
		b.line("  %s }o--|| %s : \"%s\"", child, parent, label(text))
	}
	return b.diagram()
}
