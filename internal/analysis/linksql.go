package analysis

import (
	"context"
	"strings"

	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/sqlparse"
)

// LinkSQL does for an imported graph what the Go analysis does for its
// own: it adds the tables, columns and foreign keys the migrations in dirs
// (relative to p.Dir; nil means sqlparse.DefaultMigrationDirs) create, and
// links each SQL sink to the tables its SQL touches. Extractors write
// sink.sql nodes with the SQL text in their detail, "{?}" for what they
// could not read; JDBC's (callee java.sql.*) uses ? placeholders.
func (p *Project) LinkSQL(ctx context.Context, dirs []string) error {
	schema, used, err := sqlparse.LoadMigrations(p.Dir, dirs)
	if err != nil {
		return err
	}
	p.MigrationDirs = used
	nodes, err := p.Graph.Nodes(ctx, graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindSinkSQL}})
	if err != nil {
		return err
	}
	sinks := make([]Sink, len(nodes))
	for i, n := range nodes {
		sinks[i] = Sink{Kind: n.Kind, Rebind: strings.HasPrefix(n.Attrs["callee"], "java.sql.")}
		if n.Detail != "" {
			sinks[i].Values = []string{n.Detail}
		}
	}
	analyzeQueries(sinks, sqlparse.Postgres)
	return p.Graph.Write(ctx, func(w *graph.Writer) error {
		if err := writeSchema(w, schema); err != nil {
			return err
		}
		added := map[string]bool{}
		for i, n := range nodes {
			q := sinks[i].Query
			if q == nil {
				continue
			}
			attrs := map[string]string{}
			for k, v := range n.Attrs {
				attrs[k] = v
			}
			partial := attrs["partial"] == "true"
			queryAttrs(attrs, q)
			if partial {
				attrs["partial"] = "true"
			}
			n.Attrs = attrs
			if err := w.AddNode(n); err != nil {
				return err
			}
			if err := addTableUses(w, schema, added, n.ID, q); err != nil {
				return err
			}
		}
		return nil
	})
}
