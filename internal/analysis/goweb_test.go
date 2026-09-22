//go:build goweb

package analysis

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

func TestGowebCallGraph(t *testing.T) {
	dir, exp := fixture.Goweb(t)
	start := time.Now()
	r := analyzeT(t, dir, Options{})
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("analysis took %v, want < 30s", elapsed)
	}
	t.Logf("stats %+v", r.Stats)
	ctx := t.Context()

	t.Run("routes", func(t *testing.T) {
		compareRoutes(t, exp.Routes, r.Routes)
	})

	t.Run("handlers have nodes", func(t *testing.T) {
		for _, route := range exp.Routes {
			if !strings.Contains(route.Handler, exp.Module) {
				continue
			}
			kind := graph.KindFunc
			if strings.HasPrefix(route.Handler, "(") {
				kind = graph.KindMethod
			}
			if _, err := r.Graph.Node(ctx, graph.NodeID(kind, route.Handler)); err != nil {
				t.Errorf("route %s: %v", route.Key(), err)
			}
		}
	})

	t.Run("service interfaces resolve", func(t *testing.T) {
		ifaces, err := r.Graph.Nodes(ctx, graph.NodeFilter{
			Kinds:   []graph.NodeKind{graph.KindInterfaceCall},
			Package: exp.Module + "/internal/service",
		})
		if err != nil {
			t.Fatal(err)
		}
		toPostgres := 0
		for _, iface := range ifaces {
			nbs, err := r.Graph.Neighbors(ctx, iface.ID, graph.Out, graph.EdgeDispatchesTo)
			if err != nil {
				t.Fatal(err)
			}
			if len(nbs) == 0 {
				t.Errorf("%s dispatches nowhere", iface.ID)
			}
			if slices.ContainsFunc(nbs, func(nb graph.Neighbor) bool {
				return strings.HasPrefix(nb.Node.Package, exp.Module+"/internal/store/postgres")
			}) {
				toPostgres++
			}
		}
		t.Logf("%d service interface methods called, %d dispatch to postgres", len(ifaces), toPostgres)
		if toPostgres == 0 {
			t.Error("no service interface dispatches to internal/store/postgres")
		}
	})

	t.Run("implementations", func(t *testing.T) {
		for _, impl := range exp.Implementations {
			from := graph.NodeID(graph.KindType, strings.TrimPrefix(impl.Concrete, "*"))
			nbs, err := r.Graph.Neighbors(ctx, from, graph.Out, graph.EdgeImplements)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(nbs, func(nb graph.Neighbor) bool { return nb.Node.ID == graph.NodeID(graph.KindType, impl.Interface) }) {
				t.Errorf("%s does not implement %s", impl.Concrete, impl.Interface)
			}
		}
	})

	t.Run("group create reaches postgres", func(t *testing.T) {
		paths, err := r.Graph.Paths(ctx,
			graph.NodeID(graph.KindMethod, "(*"+exp.Module+"/internal/adapter/http.groupHandler).create"),
			graph.NodeID(graph.KindMethod, "(*"+exp.Module+"/internal/store/postgres.GroupMembershipRepository).CreateGroupWithAdmin"),
			graph.PathOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) == 0 {
			t.Fatal("no path from groupHandler.create to GroupMembershipRepository.CreateGroupWithAdmin")
		}
	})
}
