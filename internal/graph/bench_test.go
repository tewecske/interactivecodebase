package graph

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// syntheticGraph builds a layered call graph of n funcs and m call edges,
// mostly calling "downward" with 10% arbitrary (often backward) edges.
func syntheticGraph(b *testing.B, n, m int) *Graph {
	b.Helper()
	g, err := Open(b.Context())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { g.Close() })
	r := rand.New(rand.NewPCG(1, 2))
	id := func(i int) string { return fmt.Sprintf("func:f%d", i) }
	err = g.Write(b.Context(), func(w *Writer) error {
		for i := range n {
			if err := w.AddNode(Node{ID: id(i), Kind: KindFunc, Name: fmt.Sprintf("Func%d", i), Package: "example/pkg"}); err != nil {
				return err
			}
		}
		for i := range m {
			from := r.IntN(n)
			to := min(n-1, from+1+r.IntN(200))
			if i%10 == 0 {
				to = r.IntN(n)
			}
			if err := w.AddEdge(Edge{From: id(from), To: id(to), Kind: EdgeCalls, Pos: Pos{StartLine: i}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	return g
}

func BenchmarkPaths(b *testing.B) {
	g := syntheticGraph(b, 30_000, 100_000)
	for _, depth := range []int{8, 10, 12} {
		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			for b.Loop() {
				if _, err := g.Paths(b.Context(), "func:f10", "func:f400", PathOptions{MaxDepth: depth}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSearch(b *testing.B) {
	g := syntheticGraph(b, 30_000, 100_000)
	for b.Loop() {
		if _, err := g.Search(b.Context(), "func123", SearchOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
