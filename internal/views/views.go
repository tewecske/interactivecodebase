// Package views computes derived views over the code graph that several
// front ends share (HTTP API, MCP): the ER diagram around a table and the
// routes whose flows touch each table.
package views

import (
	"cmp"
	"context"
	"slices"

	"github.com/tewecske/interactivecodebase/internal/flow"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/mermaid"
)

// ER draws a table and the tables within depth foreign keys of it, or the
// whole schema when table is "". It returns graph.ErrNotFound for an
// unknown table.
func ER(ctx context.Context, g *graph.Graph, table string, depth int) (mermaid.Diagram, error) {
	all, err := g.Nodes(ctx, graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindSQLTable}})
	if err != nil {
		return mermaid.Diagram{}, err
	}
	include := map[string]bool{}
	if table != "" {
		start := graph.NodeID(graph.KindSQLTable, table)
		if _, err := g.Node(ctx, start); err != nil {
			return mermaid.Diagram{}, err
		}
		include[start] = true
		frontier := []string{start}
		for range depth {
			var next []string
			for _, id := range frontier {
				for _, dir := range []graph.Direction{graph.Out, graph.In} {
					nbs, err := g.Neighbors(ctx, id, dir, graph.EdgeFK)
					if err != nil {
						return mermaid.Diagram{}, err
					}
					for _, nb := range nbs {
						if !include[nb.Node.ID] {
							include[nb.Node.ID] = true
							next = append(next, nb.Node.ID)
						}
					}
				}
			}
			frontier = next
		}
	} else {
		for _, t := range all {
			include[t.ID] = true
		}
	}
	var tables []mermaid.Table
	var fks []graph.Edge
	for _, t := range all {
		if !include[t.ID] {
			continue
		}
		cols, err := g.Neighbors(ctx, t.ID, graph.Out, graph.EdgeHasColumn)
		if err != nil {
			return mermaid.Diagram{}, err
		}
		mt := mermaid.Table{Node: t}
		for _, c := range cols {
			mt.Columns = append(mt.Columns, c.Node)
		}
		tables = append(tables, mt)
		out, err := g.Neighbors(ctx, t.ID, graph.Out, graph.EdgeFK)
		if err != nil {
			return mermaid.Diagram{}, err
		}
		for _, nb := range out {
			if include[nb.Node.ID] {
				fks = append(fks, nb.Edge)
			}
		}
	}
	return mermaid.ER(tables, fks), nil
}

// TableRoute is a route whose flow touches a table.
type TableRoute struct {
	Route string `json:"route"`
	Op    string `json:"op"`
}

// TableRoutes maps each table to the routes whose flows reach it, with the
// operation, in route registration order.
func TableRoutes(ctx context.Context, g *graph.Graph) (map[string][]TableRoute, error) {
	nodes, err := g.Nodes(ctx, graph.NodeFilter{Kinds: []graph.NodeKind{graph.KindRoute}})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(nodes, func(a, b graph.Node) int {
		return cmp.Or(cmp.Compare(a.Pos.File, b.Pos.File), cmp.Compare(a.Pos.StartLine, b.Pos.StartLine), cmp.Compare(a.ID, b.ID))
	})
	out := map[string][]TableRoute{}
	for _, n := range nodes {
		tree, err := flow.Build(ctx, g, n.ID, flow.Options{})
		if err != nil {
			return nil, err
		}
		seen := map[[2]string]bool{} // table, op
		flow.Walk(tree, func(st *flow.Step, _ int) {
			if st.Node.Kind != graph.KindSQLTable {
				return
			}
			op := st.Edge.Attrs["op"]
			if key := [2]string{st.Node.Name, op}; !seen[key] {
				seen[key] = true
				out[st.Node.Name] = append(out[st.Node.Name], TableRoute{Route: n.Name, Op: op})
			}
		})
	}
	return out, nil
}
