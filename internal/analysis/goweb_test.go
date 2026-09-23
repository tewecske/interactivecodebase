//go:build goweb

package analysis

import (
	"bytes"
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

	t.Run("sinks", func(t *testing.T) {
		checkExpectedSinks(t, r, exp.Sinks)
		var sql, resolved, inPostgres int
		for _, s := range r.Sinks {
			if s.Kind != graph.KindSinkSQL {
				continue
			}
			sql++
			if s.Resolved() {
				resolved++
			}
			if strings.HasPrefix(funcPkg(s.Caller).Path(), exp.Module+"/internal/store/postgres") {
				inPostgres++
			}
		}
		t.Logf("%d SQL sinks, %d fully resolved, %d in store/postgres", sql, resolved, inPostgres)
		if inPostgres == 0 || resolved*100 < sql*85 {
			t.Errorf("SQL sinks: %d total, %d resolved, %d in postgres", sql, resolved, inPostgres)
		}
		templates := slices.ContainsFunc(r.Sinks, func(s Sink) bool {
			return s.Callee == "html/template.ParseFS" && slices.Contains(s.Values, "*.html")
		})
		if !templates {
			t.Error("template.ParseFS(templates.FS, \"*.html\") not detected")
		}
	})

	t.Run("entries", func(t *testing.T) {
		for _, want := range exp.Entries {
			i := slices.IndexFunc(r.Entries, func(e EntryPoint) bool { return e.Kind == want.Kind && e.Name == want.Name })
			if i < 0 {
				t.Errorf("missing entry point %s", want.Key())
				continue
			}
			if e := r.Entries[i]; origin(e.Func).String() != want.Handler || e.Parent != want.Parent {
				t.Errorf("entry %s: handler %s parent %q, want %s %q", want.Key(), origin(e.Func), e.Parent, want.Handler, want.Parent)
			}
		}
	})

	t.Run("groups page requests", func(t *testing.T) {
		var targets []string
		for _, nb := range neighbors(t, r, graph.NodeID(graph.KindRoute, "GET /{lang}/groups"), graph.EdgeRequests) {
			targets = append(targets, nb.Node.Attrs["target"])
		}
		for _, want := range []string{
			"POST /{lang}/groups", "POST /{lang}/groups/join", "POST /{lang}/groups/{id}/rename",
			"POST /{lang}/groups/{id}/invite", "POST /{lang}/groups/{id}/leave",
			"POST /{lang}/groups/{id}/members/{memberID}/role", "POST /{lang}/groups/{id}/members/{memberID}/remove",
		} {
			if !slices.Contains(targets, want) {
				t.Errorf("groups page does not request %s; got %v", want, targets)
			}
		}
		var assets []string
		for _, nb := range neighbors(t, r, graph.NodeID(graph.KindRoute, "GET /{lang}/groups"), graph.EdgeLoads) {
			assets = append(assets, nb.Node.Name)
		}
		if !sameSet(assets, []string{"/static/app.css", "/static/htmx.min.js"}) {
			t.Errorf("groups page assets = %v", assets)
		}
	})

	t.Run("navigation", func(t *testing.T) {
		checkNavigation(t, r, []navCase{
			{"POST /{lang}/sign-in", "GET /{lang}/home", "redirect"},
			{"GET /{lang}/home", "GET /{lang}/groups", "link"},
			{"GET /{lang}/groups", "GET /{lang}/groups/{id}", "link"},
			{"GET /{lang}/groups", "GET /{lang}/account/settings", "link"},
			{"GET /{lang}/admin", "GET /{lang}/admin/users", "link"},
			{"GET /{lang}/admin", "GET /{lang}/admin/audit", "link"},
			{"GET /{lang}/account/settings", "GET /{lang}/sign-in", "redirect"},
		})
	})

	t.Run("deterministic", func(t *testing.T) {
		again := analyzeT(t, dir, Options{})
		var a, b bytes.Buffer
		if err := r.Graph.Export(ctx, &a); err != nil {
			t.Fatal(err)
		}
		if err := again.Graph.Export(ctx, &b); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a.Bytes(), b.Bytes()) {
			t.Error("two analyses of goweb produced different graphs")
		}
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
