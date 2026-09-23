// Package flow extracts the call flow of a route from the code graph: the
// handler, what it calls in source order, interface dispatch, and the
// sinks and tables it reaches.
package flow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tewecske/interactivecodebase/internal/graph"
)

// Prune selects how much of the call tree to keep.
type Prune int

// Pruning levels.
const (
	// PruneSinks keeps only paths that end in a sink.
	PruneSinks Prune = iota
	// PruneModule keeps module code and sinks, dropping calls into other
	// packages that are not sinks (r.Context(), http.Error, ...).
	PruneModule
	// PruneNone keeps every call in the graph.
	PruneNone
)

// Options bounds flow extraction.
type Options struct {
	// Method is the request method the flow is for; calls in branches for
	// other methods are left out. Empty means the route's own method.
	Method   string
	MaxDepth int // default 16
	Prune    Prune
}

// Step is one node of a flow tree.
type Step struct {
	Node graph.Node `json:"node"`
	// Edge is how the parent reaches this step (zero for the root).
	Edge     graph.Edge `json:"edge"`
	Children []*Step    `json:"children,omitempty"`
	// Cycle marks a call back into a function already on the path.
	Cycle bool `json:"cycle,omitempty"`
	// Ref marks a function already expanded elsewhere in the tree.
	Ref bool `json:"ref,omitempty"`
	// Truncated marks a step cut off by MaxDepth.
	Truncated bool `json:"truncated,omitempty"`
	// Calls counts the call sites in the parent that reach this node, when
	// more than one; the step appears once.
	Calls int `json:"calls,omitempty"`
}

// ErrNotRoute is returned when the start node is not a route.
var ErrNotRoute = errors.New("flow: not a route")

// Build returns the flow tree of a route node.
func Build(ctx context.Context, g *graph.Graph, routeID string, opts Options) (*Step, error) {
	route, err := g.Node(ctx, routeID)
	if err != nil {
		return nil, err
	}
	if route.Kind != graph.KindRoute && route.Kind != graph.KindEntry {
		return nil, fmt.Errorf("%w: %s", ErrNotRoute, routeID)
	}
	if opts.Method == "" {
		opts.Method = route.Attrs["method"]
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 16
	}
	b := &builder{ctx: ctx, g: g, opts: opts, expanded: map[string]bool{}, leads: map[string]bool{}}
	root := &Step{Node: route}
	handlers, err := g.Neighbors(ctx, route.ID, graph.Out, graph.EdgeHandledBy)
	if err != nil {
		return nil, err
	}
	for _, h := range handlers {
		child, err := b.expand(h.Node, h.Edge, []string{route.ID}, 1)
		if err != nil {
			return nil, err
		}
		if child != nil {
			root.Children = append(root.Children, child)
		}
	}
	return root, nil
}

type builder struct {
	ctx      context.Context
	g        *graph.Graph
	opts     Options
	expanded map[string]bool
	// leads records expanded nodes whose subtree reaches a sink.
	leads map[string]bool
}

func (b *builder) expand(n graph.Node, in graph.Edge, path []string, depth int) (*Step, error) {
	step := &Step{Node: n, Edge: in}
	switch {
	case slices.Contains(path, n.ID):
		// The node is still being expanded, so whether it reaches a sink is
		// not known yet; its subtree above shows that anyway.
		step.Cycle = true
		if b.opts.Prune == PruneSinks {
			return nil, nil
		}
		return step, nil
	case isSink(n):
		return step, b.addTables(step)
	case n.Attrs["external"] == "true" && n.Kind != graph.KindInterfaceCall:
		if b.opts.Prune != PruneNone {
			return nil, nil
		}
		return step, nil
	case b.expanded[n.ID]:
		step.Ref = true
		if b.opts.Prune == PruneSinks && !b.leads[n.ID] {
			return nil, nil
		}
		return step, nil
	case depth >= b.opts.MaxDepth:
		step.Truncated = true
		return step, nil
	}
	b.expanded[n.ID] = true
	nbs, err := b.g.Neighbors(b.ctx, n.ID, graph.Out, graph.EdgeCalls, graph.EdgeDispatchesTo)
	if err != nil {
		return nil, err
	}
	path = append(path, n.ID)
	for _, nb := range nbs {
		if !b.runs(n, nb.Edge) {
			continue
		}
		if i := slices.IndexFunc(step.Children, func(c *Step) bool { return c.Node.ID == nb.Node.ID }); i >= 0 {
			step.Children[i].Calls = max(step.Children[i].Calls, 1) + 1
			continue
		}
		child, err := b.expand(nb.Node, nb.Edge, path, depth+1)
		if err != nil {
			return nil, err
		}
		if child != nil {
			step.Children = append(step.Children, child)
			if isSink(child.Node) || b.leads[child.Node.ID] {
				b.leads[n.ID] = true
			}
		}
	}
	if b.opts.Prune == PruneSinks && !b.leads[n.ID] {
		return nil, nil
	}
	return step, nil
}

// runs reports whether a call edge executes for the flow's method, given
// the methods the calling function branches on.
func (b *builder) runs(caller graph.Node, e graph.Edge) bool {
	methods := e.Attrs["methods"]
	if methods == "" || b.opts.Method == "" || b.opts.Method == "ANY" {
		return true
	}
	list := strings.Split(methods, ",")
	if slices.Contains(list, b.opts.Method) {
		return true
	}
	compared := strings.Split(caller.Attrs["methodBranches"], ",")
	return slices.Contains(list, "*") && !slices.Contains(compared, b.opts.Method)
}

func (b *builder) addTables(s *Step) error {
	nbs, err := b.g.Neighbors(b.ctx, s.Node.ID, graph.Out, graph.EdgeQueries)
	if err != nil {
		return err
	}
	for _, nb := range nbs {
		s.Children = append(s.Children, &Step{Node: nb.Node, Edge: nb.Edge})
	}
	return nil
}

func isSink(n graph.Node) bool { return strings.HasPrefix(string(n.Kind), "sink.") }

// Walk calls fn for every step, depth first, with its depth.
func Walk(s *Step, fn func(s *Step, depth int)) {
	var walk func(s *Step, depth int)
	walk = func(s *Step, depth int) {
		fn(s, depth)
		for _, c := range s.Children {
			walk(c, depth+1)
		}
	}
	walk(s, 0)
}
