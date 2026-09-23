// Runs sbt and the Scala extractor on gathedge; make test-gathedge runs it.

//go:build gathedge && !race

package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/fixture"
	"github.com/tewecske/interactivecodebase/internal/flow"
	"github.com/tewecske/interactivecodebase/internal/graph"
	"github.com/tewecske/interactivecodebase/internal/scala"
)

// gathedgeUpdateEnv, when set, rewrites testdata/golden/gathedge.json from
// the analysis, keeping its commit and the routes and pages it picks for
// flows and navigation.
const gathedgeUpdateEnv = "ICB_GATHEDGE_UPDATE"

// gathedgeGolden holds facts about gathedge that do not depend on
// positions or on how many functions the compiler generates.
type gathedgeGolden struct {
	Commit string `json:"commit"`
	// Counts are node counts by kind.
	Counts map[string]int `json:"counts"`
	// Routes are the /api routes.
	Routes []gathedgeRoute `json:"routes"`
	// Pages are the frontend pages with their first view and the routes
	// they request ("?" plus method and URL for one no route serves).
	Pages []gathedgePage `json:"pages"`
	// Links are the pages the picked pages navigate to.
	Links []gathedgeLinks `json:"links"`
	// NoInbound are the pages nothing navigates to.
	NoInbound []string `json:"noInbound"`
	// Flows are the tables, as "table:op", the picked routes' flows reach.
	Flows []gathedgeFlow `json:"flows"`
}

type gathedgeRoute struct {
	Route   string `json:"route"`
	Access  string `json:"access"`
	Handler string `json:"handler"`
}

type gathedgePage struct {
	Pattern  string   `json:"pattern"`
	View     string   `json:"view"`
	Requests []string `json:"requests"`
}

type gathedgeLinks struct {
	Page string   `json:"page"`
	To   []string `json:"to"`
}

type gathedgeFlow struct {
	Route  string   `json:"route"`
	Tables []string `json:"tables"`
}

var gathedgeCountKinds = []graph.NodeKind{
	graph.KindRoute, graph.KindPage, graph.KindHTMXCall, graph.KindSQLTable, graph.KindSQLColumn, graph.KindSinkSQL,
}

// TestGathedgeGolden analyzes a gathedge checkout at the pinned commit (see
// fixture.Gathedge) through sbt and icb-scala ($ICB_SCALA, default
// extractors/scala/target/icb-scala) and checks it against
// testdata/golden/gathedge.json. sbt writes only its target directories.
func TestGathedgeGolden(t *testing.T) {
	data, err := os.ReadFile(fixture.GathedgeGolden())
	if err != nil {
		t.Fatal(err)
	}
	var want gathedgeGolden
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	dir := fixture.Gathedge(t, want.Commit)
	for _, cmd := range []string{"sbt", "java"} {
		if _, err := exec.LookPath(cmd); err != nil {
			t.Fatalf("%s not installed", cmd)
		}
	}
	if os.Getenv(scala.ExtractorEnv) == "" {
		t.Setenv(scala.ExtractorEnv, filepath.Join(fixture.RepoRoot(), "extractors", "scala", "target", "icb-scala"))
	}

	ctx := context.Background()
	p, release, err := openProject(ctx, dir, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()
	got, err := gathedgeFacts(ctx, p.Graph, want)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.MarshalIndent(got, "", "  ")
	gotJSON = append(gotJSON, '\n')
	if os.Getenv(gathedgeUpdateEnv) != "" {
		if err := os.WriteFile(fixture.GathedgeGolden(), gotJSON, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	// Compare section by section, for readable failures.
	wantJSON := func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
	for _, s := range []struct {
		name      string
		got, want any
	}{
		{"counts", got.Counts, want.Counts},
		{"routes", got.Routes, want.Routes},
		{"pages", got.Pages, want.Pages},
		{"links", got.Links, want.Links},
		{"noInbound", got.NoInbound, want.NoInbound},
		{"flows", got.Flows, want.Flows},
	} {
		if g, w := wantJSON(s.got), wantJSON(s.want); g != w {
			t.Errorf("%s:\n%s\nwant:\n%s", s.name, g, w)
		}
	}
}

// gathedgeFacts reads the golden's facts from g, for the pages and routes
// sel picks.
func gathedgeFacts(ctx context.Context, g *graph.Graph, sel gathedgeGolden) (gathedgeGolden, error) {
	out := gathedgeGolden{Commit: sel.Commit, Counts: map[string]int{}, Routes: []gathedgeRoute{}, Pages: []gathedgePage{},
		Links: []gathedgeLinks{}, NoInbound: []string{}, Flows: []gathedgeFlow{}}
	all, err := g.Nodes(ctx, graph.NodeFilter{})
	if err != nil {
		return out, err
	}
	neighbors := func(id string, dir graph.Direction, kind graph.EdgeKind) ([]graph.Neighbor, error) {
		return g.Neighbors(ctx, id, dir, kind)
	}
	for _, n := range all {
		if slices.Contains(gathedgeCountKinds, n.Kind) {
			out.Counts[string(n.Kind)]++
		}
		switch {
		case n.Kind == graph.KindRoute && strings.HasPrefix(n.Attrs["pattern"], "/api/"):
			out.Routes = append(out.Routes, gathedgeRoute{Route: n.Name, Access: n.Attrs["access"], Handler: n.Attrs["handler"]})
		case n.Kind == graph.KindPage:
			pg := gathedgePage{Pattern: n.Attrs["pattern"], Requests: []string{}}
			views, err := neighbors(n.ID, graph.Out, graph.EdgeHandledBy)
			if err != nil {
				return out, err
			}
			var ids []string
			for _, v := range views {
				ids = append(ids, v.Node.ID)
			}
			if len(ids) > 0 {
				pg.View = slices.Min(ids)
			}
			reqs, err := neighbors(n.ID, graph.Out, graph.EdgeRequests)
			if err != nil {
				return out, err
			}
			for _, r := range reqs {
				a := r.Node.Attrs
				target := cmp.Or(a["target"], "? "+a["method"]+" "+a["url"])
				if !slices.Contains(pg.Requests, target) {
					pg.Requests = append(pg.Requests, target)
				}
			}
			slices.Sort(pg.Requests)
			out.Pages = append(out.Pages, pg)
			in, err := neighbors(n.ID, graph.In, graph.EdgeNavigatesTo)
			if err != nil {
				return out, err
			}
			if len(in) == 0 {
				out.NoInbound = append(out.NoInbound, n.ID)
			}
		}
	}
	slices.SortFunc(out.Routes, func(a, b gathedgeRoute) int { return cmp.Compare(a.Route, b.Route) })
	slices.SortFunc(out.Pages, func(a, b gathedgePage) int { return cmp.Compare(a.Pattern, b.Pattern) })
	slices.Sort(out.NoInbound)

	for _, l := range sel.Links {
		nbs, err := neighbors(l.Page, graph.Out, graph.EdgeNavigatesTo)
		if err != nil {
			return out, err
		}
		links := gathedgeLinks{Page: l.Page, To: []string{}}
		for _, nb := range nbs {
			if !slices.Contains(links.To, nb.Node.ID) {
				links.To = append(links.To, nb.Node.ID)
			}
		}
		slices.Sort(links.To)
		out.Links = append(out.Links, links)
	}
	for _, f := range sel.Flows {
		tree, err := flow.Build(ctx, g, "route:"+f.Route, flow.Options{})
		if err != nil {
			return out, err
		}
		fl := gathedgeFlow{Route: f.Route, Tables: []string{}}
		flow.Walk(tree, func(st *flow.Step, _ int) {
			if st.Node.Kind != graph.KindSQLTable {
				return
			}
			if key := st.Node.Name + ":" + st.Edge.Attrs["op"]; !slices.Contains(fl.Tables, key) {
				fl.Tables = append(fl.Tables, key)
			}
		})
		slices.Sort(fl.Tables)
		out.Flows = append(out.Flows, fl)
	}
	return out, nil
}
