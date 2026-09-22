package graph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const nodeCols = "n.id, n.kind, n.name, n.package, n.detail, n.file, n.start_line, n.start_col, n.end_line, n.end_col, n.attrs"
const edgeCols = "e.id, e.src, e.dst, e.kind, e.file, e.start_line, e.start_col, e.end_line, e.end_col, e.attrs"

// callSiteOrder sorts edges top-to-bottom through the source.
const callSiteOrder = "e.file, e.start_line, e.start_col, e.id"

type scanner interface{ Scan(dest ...any) error }

func scanNode(s scanner, extra ...any) (Node, error) {
	var n Node
	var attrs string
	p := &n.Pos
	dest := append([]any{&n.ID, &n.Kind, &n.Name, &n.Package, &n.Detail,
		&p.File, &p.StartLine, &p.StartCol, &p.EndLine, &p.EndCol, &attrs}, extra...)
	if err := s.Scan(dest...); err != nil {
		return Node{}, err
	}
	var err error
	n.Attrs, err = decodeAttrs(attrs)
	return n, err
}

func edgeDest(e *Edge, attrs *string) []any {
	p := &e.Pos
	return []any{&e.ID, &e.From, &e.To, &e.Kind, &p.File, &p.StartLine, &p.StartCol, &p.EndLine, &p.EndCol, attrs}
}

// Node returns the node with the given ID, or ErrNotFound.
func (g *Graph) Node(ctx context.Context, id string) (Node, error) {
	row := g.db.QueryRowContext(ctx, "SELECT "+nodeCols+" FROM nodes n WHERE n.id = ?", id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, fmt.Errorf("%w: node %s", ErrNotFound, id)
	}
	if err != nil {
		return Node{}, fmt.Errorf("graph: node %s: %w", id, err)
	}
	return n, nil
}

// NodeFilter selects nodes. Zero fields match everything.
type NodeFilter struct {
	Kinds   []NodeKind
	Package string
}

// Nodes returns the nodes matching f, ordered by ID.
func (g *Graph) Nodes(ctx context.Context, f NodeFilter) ([]Node, error) {
	q := "SELECT " + nodeCols + " FROM nodes n WHERE 1=1"
	var args []any
	if len(f.Kinds) > 0 {
		q += " AND n.kind IN (" + placeholders(len(f.Kinds)) + ")"
		args = appendAll(args, f.Kinds)
	}
	if f.Package != "" {
		q += " AND n.package = ?"
		args = append(args, f.Package)
	}
	return g.queryNodes(ctx, q+" ORDER BY n.id", args...)
}

func (g *Graph) queryNodes(ctx context.Context, q string, args ...any) ([]Node, error) {
	rows, err := g.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: query nodes: %w", err)
	}
	defer rows.Close()
	var nodes []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("graph: scan node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// Direction selects which edges of a node Neighbors follows.
type Direction int

// Edge directions relative to the queried node.
const (
	Out Direction = iota // edges starting at the node (callees)
	In                   // edges ending at the node (callers)
)

// Neighbor is an adjacent node together with the edge that connects it.
type Neighbor struct {
	Edge Edge `json:"edge"`
	Node Node `json:"node"`
}

// Neighbors returns the nodes adjacent to id in direction dir, restricted to
// the given edge kinds (all kinds if none), ordered by edge source position.
func (g *Graph) Neighbors(ctx context.Context, id string, dir Direction, kinds ...EdgeKind) ([]Neighbor, error) {
	self, other := "e.src", "e.dst"
	if dir == In {
		self, other = other, self
	}
	q := "SELECT " + nodeCols + ", " + edgeCols + " FROM edges e JOIN nodes n ON n.id = " + other +
		" WHERE " + self + " = ?"
	args := []any{id}
	if len(kinds) > 0 {
		q += " AND e.kind IN (" + placeholders(len(kinds)) + ")"
		args = appendAll(args, kinds)
	}
	rows, err := g.db.QueryContext(ctx, q+" ORDER BY "+callSiteOrder, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: neighbors of %s: %w", id, err)
	}
	defer rows.Close()
	var out []Neighbor
	for rows.Next() {
		var nb Neighbor
		var attrs string
		if nb.Node, err = scanNode(rows, edgeDest(&nb.Edge, &attrs)...); err != nil {
			return nil, fmt.Errorf("graph: scan neighbor: %w", err)
		}
		if nb.Edge.Attrs, err = decodeAttrs(attrs); err != nil {
			return nil, fmt.Errorf("graph: scan neighbor: %w", err)
		}
		out = append(out, nb)
	}
	return out, rows.Err()
}

// PathOptions bounds a path search.
type PathOptions struct {
	EdgeKinds []EdgeKind // follow only these kinds; all if empty
	MaxDepth  int        // maximum edges per path; default 10
	Limit     int        // maximum paths returned; default 10
}

// Paths returns up to opts.Limit acyclic paths from one node to another,
// shortest first. Each path is the list of edges walked.
func (g *Graph) Paths(ctx context.Context, from, to string, opts PathOptions) ([][]Edge, error) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 10
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	kindFilter := ""
	var kindArgs []any
	if len(opts.EdgeKinds) > 0 {
		kindFilter = " AND e.kind IN (" + placeholders(len(opts.EdgeKinds)) + ")"
		kindArgs = appendAll(nil, opts.EdgeKinds)
	}
	args := append([]any{to, opts.MaxDepth}, kindArgs...)
	args = append(args, from, from, to, opts.MaxDepth)
	args = append(args, kindArgs...)
	args = append(args, to, opts.Limit)
	// Enumerating every walk from `from` is exponential in depth, so first
	// walk backwards from `to` to find each node's distance to it (near), and
	// only extend walks through nodes that can still reach `to` in time.
	//
	// visited holds node IDs wrapped in char(31) so instr() finds exact IDs;
	// edge_ids holds the walked edge IDs, comma-separated.
	q := `
WITH RECURSIVE
back (node, dist) AS (
	SELECT ?, 0
	UNION
	SELECT e.src, b.dist + 1
	FROM back b JOIN edges e ON e.dst = b.node
	WHERE b.dist < ?` + kindFilter + `
),
near (node, dist) AS (SELECT node, min(dist) FROM back GROUP BY node),
walk (node, depth, visited, edge_ids) AS (
	SELECT ?, 0, char(31) || ? || char(31), ''
	UNION ALL
	SELECT e.dst, w.depth + 1, w.visited || e.dst || char(31), w.edge_ids || e.id || ','
	FROM walk w
		JOIN edges e ON e.src = w.node
		JOIN near r ON r.node = e.dst
	WHERE w.node <> ? AND w.depth + 1 + r.dist <= ?
		AND instr(w.visited, char(31) || e.dst || char(31)) = 0` + kindFilter + `
)
SELECT edge_ids FROM walk WHERE node = ? AND depth > 0 ORDER BY depth, edge_ids LIMIT ?`
	rows, err := g.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: paths %s -> %s: %w", from, to, err)
	}
	var idPaths [][]int64
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, fmt.Errorf("graph: scan path: %w", err)
		}
		var ids []int64
		for part := range strings.SplitSeq(strings.TrimSuffix(s, ","), ",") {
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("graph: parse path %q: %w", s, err)
			}
			ids = append(ids, id)
		}
		idPaths = append(idPaths, ids)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: paths %s -> %s: %w", from, to, err)
	}
	return g.resolvePaths(ctx, idPaths)
}

func (g *Graph) resolvePaths(ctx context.Context, idPaths [][]int64) ([][]Edge, error) {
	seen := map[int64]bool{}
	var ids []int64
	for _, p := range idPaths {
		for _, id := range p {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	edges, err := g.queryEdges(ctx, "SELECT "+edgeCols+" FROM edges e WHERE e.id IN ("+placeholders(len(ids))+")", appendAll(nil, ids)...)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Edge, len(edges))
	for _, e := range edges {
		byID[e.ID] = e
	}
	paths := make([][]Edge, len(idPaths))
	for i, p := range idPaths {
		paths[i] = make([]Edge, len(p))
		for j, id := range p {
			paths[i][j] = byID[id]
		}
	}
	return paths, nil
}

func (g *Graph) queryEdges(ctx context.Context, q string, args ...any) ([]Edge, error) {
	rows, err := g.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: query edges: %w", err)
	}
	defer rows.Close()
	var edges []Edge
	for rows.Next() {
		var e Edge
		var attrs string
		if err := rows.Scan(edgeDest(&e, &attrs)...); err != nil {
			return nil, fmt.Errorf("graph: scan edge: %w", err)
		}
		if e.Attrs, err = decodeAttrs(attrs); err != nil {
			return nil, fmt.Errorf("graph: scan edge: %w", err)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// SearchOptions narrows a search.
type SearchOptions struct {
	Kinds []NodeKind // only these kinds; all if empty
	Limit int        // maximum results; default 50
}

// Search finds nodes whose name, package, file or detail contain every
// whitespace-separated term of q (case-insensitive). Name matches rank first.
func (g *Graph) Search(ctx context.Context, q string, opts SearchOptions) ([]Node, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return nil, nil
	}
	kindFilter := ""
	var kindArgs []any
	if len(opts.Kinds) > 0 {
		kindFilter = " AND n.kind IN (" + placeholders(len(opts.Kinds)) + ")"
		kindArgs = appendAll(nil, opts.Kinds)
	}
	if !allTrigrams(terms) {
		// The trigram index cannot match terms shorter than 3 characters.
		var conds []string
		var args []any
		for _, t := range terms {
			conds = append(conds, `(n.name || ' ' || n.package || ' ' || n.file || ' ' || n.detail) LIKE ? ESCAPE '\'`)
			args = append(args, "%"+escapeLike(t)+"%")
		}
		args = append(append(args, kindArgs...), opts.Limit)
		return g.queryNodes(ctx, "SELECT "+nodeCols+" FROM nodes n WHERE "+strings.Join(conds, " AND ")+
			kindFilter+" ORDER BY length(n.name), n.id LIMIT ?", args...)
	}
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	args := append([]any{strings.Join(quoted, " AND ")}, kindArgs...)
	args = append(args, opts.Limit)
	return g.queryNodes(ctx, "SELECT "+nodeCols+" FROM nodes_fts JOIN nodes n ON n.rowid = nodes_fts.rowid"+
		" WHERE nodes_fts MATCH ?"+kindFilter+
		" ORDER BY bm25(nodes_fts, 10.0, 2.0, 1.0, 1.0), length(n.name), n.id LIMIT ?", args...)
}

func allTrigrams(terms []string) bool {
	for _, t := range terms {
		if utf8.RuneCountInString(t) < 3 {
			return false
		}
	}
	return true
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Counts holds the number of nodes and edges per kind.
type Counts struct {
	Nodes map[NodeKind]int `json:"nodes"`
	Edges map[EdgeKind]int `json:"edges"`
}

// Counts returns the number of nodes and edges per kind.
func (g *Graph) Counts(ctx context.Context) (Counts, error) {
	c := Counts{Nodes: map[NodeKind]int{}, Edges: map[EdgeKind]int{}}
	if err := countBy(ctx, g.db, "SELECT kind, count(*) FROM nodes GROUP BY kind", c.Nodes); err != nil {
		return Counts{}, err
	}
	if err := countBy(ctx, g.db, "SELECT kind, count(*) FROM edges GROUP BY kind", c.Edges); err != nil {
		return Counts{}, err
	}
	return c, nil
}

func countBy[K ~string](ctx context.Context, db *sql.DB, q string, into map[K]int) error {
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("graph: counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k K
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return fmt.Errorf("graph: counts: %w", err)
		}
		into[k] = n
	}
	return rows.Err()
}

// Export writes the whole graph as indented JSON: {"nodes": [...], "edges": [...]},
// nodes ordered by ID and edges by insertion.
func (g *Graph) Export(ctx context.Context, w io.Writer) error {
	nodes, err := g.Nodes(ctx, NodeFilter{})
	if err != nil {
		return err
	}
	edges, err := g.queryEdges(ctx, "SELECT "+edgeCols+" FROM edges e ORDER BY e.id")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Nodes []Node `json:"nodes"`
		Edges []Edge `json:"edges"`
	}{nonNil(nodes), nonNil(edges)})
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func appendAll[T any](args []any, vals []T) []any {
	for _, v := range vals {
		args = append(args, v)
	}
	return args
}
