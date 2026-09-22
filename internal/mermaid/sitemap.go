package mermaid

import (
	"cmp"
	"slices"

	"github.com/tewecske/interactivecodebase/internal/graph"
)

// accessOrder lists access groups from most open to most restricted.
var accessOrder = []struct{ key, title string }{
	{"public", "Public"},
	{"optional", "Public, session-aware"},
	{"guest", "Guest"},
	{"authenticated", "Signed in"},
	{"admin", "Admin"},
}

// SiteMapOptions filters the site map.
type SiteMapOptions struct {
	// GETOnly keeps only GET routes (pages and links), dropping form and
	// redirect targets that are not GET.
	GETOnly bool
}

// SiteMap draws routes grouped by access level, with navigation edges:
// solid for links, thick for form posts, dotted for redirects.
func SiteMap(routes []graph.Node, nav []graph.Edge, opts SiteMapOptions) Diagram {
	b := newBuilder("flowchart LR", "r")
	keep := map[string]bool{}
	groups := map[string][]graph.Node{}
	for _, r := range routes {
		if opts.GETOnly && r.Attrs["method"] != "GET" {
			continue
		}
		keep[r.ID] = true
		group := r.Attrs["access"]
		if group == "public" && r.Attrs["optionalAuth"] == "true" {
			group = "optional"
		}
		groups[group] = append(groups[group], r)
	}
	for _, g := range accessOrder {
		members := groups[g.key]
		if len(members) == 0 {
			continue
		}
		slices.SortFunc(members, func(a, b graph.Node) int { return cmp.Compare(a.Name, b.Name) })
		b.line("  subgraph %s[\"%s\"]", g.key, g.title)
		for _, r := range members {
			id := b.id(r.ID)
			if r.Attrs["method"] == "GET" {
				b.line("    %s(\"%s\")", id, label(r.Name))
			} else {
				b.line("    %s[/\"%s\"/]", id, label(r.Name))
			}
		}
		b.line("  end")
	}
	for _, e := range nav {
		if !keep[e.From] || !keep[e.To] || e.From == e.To {
			continue
		}
		from, to := b.byNode[e.From], b.byNode[e.To]
		switch e.Attrs["trigger"] {
		case "form":
			b.line("  %s ==> %s", from, to)
		case "redirect":
			b.line("  %s -.-> %s", from, to)
		case "hx-redirect":
			b.line("  %s -.->|hx| %s", from, to)
		default:
			b.line("  %s --> %s", from, to)
		}
	}
	for _, id := range sortedKeys(b.ids) {
		b.line("  click %s icbClick", id)
	}
	return b.diagram()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(a), len(b)), cmp.Compare(a, b))
	})
	return keys
}
