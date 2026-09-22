// Package graph holds the code graph produced by analysis: typed nodes and
// edges stored in an in-memory SQLite database with full-text search.
//
// A Graph is populated once through Write and then queried concurrently.
// Re-analysis builds a new Graph rather than mutating a live one.
package graph

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrNotFound is returned when a node does not exist.
var ErrNotFound = errors.New("graph: not found")

//go:embed schema.sql
var schema string

// Pos is a source range. Lines and columns are 1-based; zero means unknown.
type Pos struct {
	File      string `json:"file,omitempty"`
	StartLine int    `json:"startLine,omitempty"`
	StartCol  int    `json:"startCol,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	EndCol    int    `json:"endCol,omitempty"`
}

// Node is a vertex in the code graph.
type Node struct {
	// ID is stable across analyses of the same code, conventionally
	// NodeID(kind, qualifiedName).
	ID   string   `json:"id"`
	Kind NodeKind `json:"kind"`
	// Name is the short display name, e.g. "CreateGroup" or "GET /{lang}/groups".
	Name string `json:"name"`
	// Package is the Go import path the node belongs to, if any.
	Package string `json:"package,omitempty"`
	// Detail is searchable descriptive text: a signature, SQL text, a route pattern.
	Detail string            `json:"detail,omitempty"`
	Pos    Pos               `json:"pos"`
	Attrs  map[string]string `json:"attrs,omitempty"`
}

// Edge is a directed, typed relation between two nodes.
type Edge struct {
	ID   int64    `json:"id"`
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
	// Pos is where the relation originates, e.g. the call site.
	Pos   Pos               `json:"pos"`
	Attrs map[string]string `json:"attrs,omitempty"`
}

// NodeID builds the conventional node ID for kind and a qualified name.
func NodeID(kind NodeKind, name string) string {
	return string(kind) + ":" + name
}

// Graph is a code graph backed by an in-memory SQLite database.
type Graph struct {
	db *sql.DB
	// pin keeps one connection open: an in-memory database is freed when
	// its last connection closes, and database/sql may close idle ones.
	pin *sql.Conn
}

var dbSeq atomic.Uint64

// Open creates a new, empty graph. Each graph is an isolated database.
func Open(ctx context.Context) (*Graph, error) {
	dsn := fmt.Sprintf("file:/icb-graph-%d?vfs=memdb&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", dbSeq.Add(1))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("graph: open: %w", err)
	}
	pin, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("graph: open: %w", err)
	}
	if _, err := pin.ExecContext(ctx, schema); err != nil {
		pin.Close()
		db.Close()
		return nil, fmt.Errorf("graph: create schema: %w", err)
	}
	return &Graph{db: db, pin: pin}, nil
}

// Close releases the graph and its data.
func (g *Graph) Close() error {
	return errors.Join(g.pin.Close(), g.db.Close())
}

// Write runs fn in a single transaction. If fn returns an error nothing is
// written.
func (g *Graph) Write(ctx context.Context, fn func(w *Writer) error) (err error) {
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("graph: begin: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	w := &Writer{ctx: ctx}
	if w.node, err = tx.PrepareContext(ctx, upsertNodeSQL); err != nil {
		return fmt.Errorf("graph: prepare: %w", err)
	}
	if w.edge, err = tx.PrepareContext(ctx, insertEdgeSQL); err != nil {
		return fmt.Errorf("graph: prepare: %w", err)
	}
	if err = fn(w); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("graph: commit: %w", err)
	}
	return nil
}

// Writer adds nodes and edges inside a Write transaction.
type Writer struct {
	ctx  context.Context
	node *sql.Stmt
	edge *sql.Stmt
}

const upsertNodeSQL = `
INSERT INTO nodes (id, kind, name, package, detail, file, start_line, start_col, end_line, end_col, attrs)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
	kind = excluded.kind, name = excluded.name, package = excluded.package, detail = excluded.detail,
	file = excluded.file, start_line = excluded.start_line, start_col = excluded.start_col,
	end_line = excluded.end_line, end_col = excluded.end_col, attrs = excluded.attrs`

const insertEdgeSQL = `
INSERT INTO edges (src, dst, kind, file, start_line, start_col, end_line, end_col, attrs)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING`

// AddNode inserts n, replacing any existing node with the same ID.
func (w *Writer) AddNode(n Node) error {
	if n.ID == "" {
		return errors.New("graph: node has empty ID")
	}
	if !n.Kind.Valid() {
		return fmt.Errorf("graph: node %s: unknown kind %q", n.ID, n.Kind)
	}
	attrs, err := encodeAttrs(n.Attrs)
	if err != nil {
		return fmt.Errorf("graph: node %s: %w", n.ID, err)
	}
	p := n.Pos
	_, err = w.node.ExecContext(w.ctx, n.ID, n.Kind, n.Name, n.Package, n.Detail,
		p.File, p.StartLine, p.StartCol, p.EndLine, p.EndCol, attrs)
	if err != nil {
		return fmt.Errorf("graph: add node %s: %w", n.ID, err)
	}
	return nil
}

// AddEdge inserts e. Both endpoints must already exist. An edge identical in
// endpoints, kind and position to an existing one is ignored. e.ID is ignored.
func (w *Writer) AddEdge(e Edge) error {
	if !e.Kind.Valid() {
		return fmt.Errorf("graph: edge %s -> %s: unknown kind %q", e.From, e.To, e.Kind)
	}
	attrs, err := encodeAttrs(e.Attrs)
	if err != nil {
		return fmt.Errorf("graph: edge %s -> %s: %w", e.From, e.To, err)
	}
	p := e.Pos
	_, err = w.edge.ExecContext(w.ctx, e.From, e.To, e.Kind,
		p.File, p.StartLine, p.StartCol, p.EndLine, p.EndCol, attrs)
	if err != nil {
		return fmt.Errorf("graph: add edge %s -[%s]-> %s: %w", e.From, e.Kind, e.To, err)
	}
	return nil
}

func encodeAttrs(attrs map[string]string) (string, error) {
	if len(attrs) == 0 {
		return "", nil
	}
	b, err := json.Marshal(attrs)
	return string(b), err
}

func decodeAttrs(s string) (map[string]string, error) {
	if s == "" {
		return nil, nil
	}
	var attrs map[string]string
	err := json.Unmarshal([]byte(s), &attrs)
	return attrs, err
}
