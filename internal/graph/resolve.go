package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrAmbiguous is returned by Resolve when text matches several nodes.
var ErrAmbiguous = errors.New("graph: ambiguous node")

// maxCandidates bounds how many matches an ambiguity error lists.
const maxCandidates = 10

// Resolve finds the node a user-supplied reference means: an exact ID; an
// ID without its kind prefix ("(*example.com/app.T).M", "GET /x"); search
// text matching one node; or, among several matches, the one node whose
// ID ends with the text (a qualified name like "sql.Tx).ExecContext").
// Ambiguous text returns ErrAmbiguous listing candidates.
func (g *Graph) Resolve(ctx context.Context, ref string) (Node, error) {
	candidates := []string{ref}
	for _, k := range []NodeKind{KindRoute, KindMethod, KindFunc, KindInterfaceCall, KindType, KindSQLTable} {
		candidates = append(candidates, NodeID(k, ref))
	}
	for _, id := range candidates {
		n, err := g.Node(ctx, id)
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Node{}, err
		}
	}
	nodes, err := g.Search(ctx, ref, SearchOptions{Limit: maxCandidates + 1})
	if err != nil {
		return Node{}, err
	}
	switch len(nodes) {
	case 0:
		return Node{}, fmt.Errorf("%w: no node matches %q", ErrNotFound, ref)
	case 1:
		return nodes[0], nil
	}
	var suffix []Node
	for _, n := range nodes {
		if strings.HasSuffix(n.ID, ref) {
			suffix = append(suffix, n)
		}
	}
	if len(suffix) == 1 {
		return suffix[0], nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches several nodes; use an ID:", ref)
	for i, n := range nodes {
		if i == maxCandidates {
			b.WriteString("\n  ...")
			break
		}
		b.WriteString("\n  " + n.ID)
	}
	return Node{}, fmt.Errorf("%w: %s", ErrAmbiguous, b.String())
}
