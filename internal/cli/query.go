package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tewecske/interactivecodebase/internal/graph"
)

const queryHelp = `Commands:
  export               the whole graph as JSON
  search <text>        nodes whose name, package, file, detail or ID contain text
  callees <node>       what a node calls (and interface dispatch targets)
  callers <node>       what calls a node
  paths <from> <to>    shortest call paths between two nodes

A <node> is a node ID ("method:(*example.com/app.T).M"), the ID without its
kind prefix ("(*example.com/app.T).M"), or search text matching one node.`

// maxCandidates bounds how many matches an ambiguous node argument lists.
const maxCandidates = 10

func runQuery(ctx context.Context, e *env, args []string) (err error) {
	fs := newFlagSet(e, "query", "icb query [flags] <dir> <command> [args]")
	asJSON := fs.Bool("json", false, "print results as JSON")
	usage := fs.Usage
	fs.Usage = func() {
		usage()
		fmt.Fprintf(fs.Output(), "\n%s\n", queryHelp)
	}
	if err := parseRange(fs, args, 2, -1); err != nil {
		return err
	}
	cmd, cmdArgs := fs.Arg(1), fs.Args()[2:]
	want := map[string]int{"export": 0, "search": -1, "callees": 1, "callers": 1, "paths": 2}
	n, ok := want[cmd]
	if !ok {
		fmt.Fprintf(fs.Output(), "unknown query command %q\n\n", cmd)
		fs.Usage()
		return errUsage
	}
	if (n >= 0 && len(cmdArgs) != n) || (n < 0 && len(cmdArgs) == 0) {
		fmt.Fprintf(fs.Output(), "wrong number of arguments for %s\n\n", cmd)
		fs.Usage()
		return errUsage
	}

	r, release, err := openAnalysis(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	g := r.Graph
	q := &querier{e: e, g: g, json: *asJSON}

	switch cmd {
	case "export":
		return g.Export(ctx, e.stdout)
	case "search":
		nodes, err := g.Search(ctx, strings.Join(cmdArgs, " "), graph.SearchOptions{})
		if err != nil {
			return err
		}
		return q.nodes(nodes)
	case "callees", "callers":
		node, err := q.resolve(ctx, cmdArgs[0])
		if err != nil {
			return err
		}
		dir := graph.Out
		if cmd == "callers" {
			dir = graph.In
		}
		nbs, err := g.Neighbors(ctx, node.ID, dir)
		if err != nil {
			return err
		}
		return q.neighbors(nbs)
	default: // paths
		from, err := q.resolve(ctx, cmdArgs[0])
		if err != nil {
			return err
		}
		to, err := q.resolve(ctx, cmdArgs[1])
		if err != nil {
			return err
		}
		paths, err := g.Paths(ctx, from.ID, to.ID, graph.PathOptions{})
		if err != nil {
			return err
		}
		return q.paths(from.ID, to.ID, paths)
	}
}

type querier struct {
	e    *env
	g    *graph.Graph
	json bool
}

var errNodeNotFound = errors.New("no node matches")

// resolve finds the node an argument refers to: an exact ID, an ID without
// its kind prefix, or search text matching exactly one node.
func (q *querier) resolve(ctx context.Context, arg string) (graph.Node, error) {
	candidates := []string{arg}
	for _, k := range []graph.NodeKind{graph.KindMethod, graph.KindFunc, graph.KindInterfaceCall, graph.KindType} {
		candidates = append(candidates, graph.NodeID(k, arg))
	}
	for _, id := range candidates {
		n, err := q.g.Node(ctx, id)
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, graph.ErrNotFound) {
			return graph.Node{}, err
		}
	}
	nodes, err := q.g.Search(ctx, arg, graph.SearchOptions{Limit: maxCandidates + 1})
	if err != nil {
		return graph.Node{}, err
	}
	switch len(nodes) {
	case 0:
		return graph.Node{}, fmt.Errorf("%w %q", errNodeNotFound, arg)
	case 1:
		return nodes[0], nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches several nodes; use an ID:", arg)
	for i, n := range nodes {
		if i == maxCandidates {
			b.WriteString("\n  ...")
			break
		}
		b.WriteString("\n  " + n.ID)
	}
	return graph.Node{}, errors.New(b.String())
}

func (q *querier) nodes(nodes []graph.Node) error {
	if q.json {
		return writeJSON(q.e, nonNil(nodes))
	}
	for _, n := range nodes {
		fmt.Fprintf(q.e.stdout, "%s\t%s\n", n.ID, formatPos(n.Pos))
	}
	return nil
}

func (q *querier) neighbors(nbs []graph.Neighbor) error {
	if q.json {
		return writeJSON(q.e, nonNil(nbs))
	}
	for _, nb := range nbs {
		fmt.Fprintf(q.e.stdout, "%s\t%s\t%s\n", nb.Edge.Kind, nb.Node.ID, formatPos(nb.Edge.Pos))
	}
	return nil
}

func (q *querier) paths(from, to string, paths [][]graph.Edge) error {
	if q.json {
		return writeJSON(q.e, nonNil(paths))
	}
	if len(paths) == 0 {
		fmt.Fprintf(q.e.stdout, "no path from %s to %s\n", from, to)
		return nil
	}
	for i, p := range paths {
		if i > 0 {
			fmt.Fprintln(q.e.stdout)
		}
		fmt.Fprintln(q.e.stdout, p[0].From)
		for _, edge := range p {
			fmt.Fprintf(q.e.stdout, "  -%s-> %s\t%s\n", edge.Kind, edge.To, formatPos(edge.Pos))
		}
	}
	return nil
}

func formatPos(p graph.Pos) string {
	if p.File == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", p.File, p.StartLine)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
