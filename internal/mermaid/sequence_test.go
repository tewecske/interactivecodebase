package mermaid

import (
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/flow"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

func TestSequenceAltForSeveralImplementations(t *testing.T) {
	n := func(id, kind, name, pkg string) graph.Node {
		return graph.Node{ID: id, Kind: graph.NodeKind(kind), Name: name, Package: pkg}
	}
	root := &flow.Step{Node: n("route:POST /x", "route", "POST /x", ""), Children: []*flow.Step{{
		Node: n("method:h", "method", "(*H).post", "example.com/app/web"),
		Children: []*flow.Step{{
			Node: n("interface_call:(example.com/app.Store).Save", "interface_call", "Store.Save", "example.com/app"),
			Children: []*flow.Step{
				{Node: n("method:pg", "method", "(*PG).Save", "example.com/app/postgres")},
				{Node: n("method:mem", "method", "(*Mem).Save", "example.com/app/memory")},
			},
		}},
	}}}
	d := Sequence(root)
	for _, want := range []string{"  alt (*PG).Save\n", "  else (*Mem).Save\n", "  end\n", "web->>postgres: Store.Save → (*PG).Save"} {
		if !strings.Contains(d.Mermaid, want) {
			t.Errorf("missing %q in:\n%s", want, d.Mermaid)
		}
	}
	if d.IDs["1"] != "method:h" || d.IDs["2"] != "method:pg" || d.IDs["3"] != "method:mem" {
		t.Errorf("message ids = %v", d.IDs)
	}
}

func TestSequenceStarter(t *testing.T) {
	for kind, want := range map[string]string{"job": "Scheduler", "command": "CLI", "rpc": "Client", "consumer": "Broker"} {
		root := &flow.Step{Node: graph.Node{ID: "entry:" + kind + " x", Kind: graph.KindEntry, Name: "x", Attrs: map[string]string{"entryKind": kind}}}
		if d := Sequence(root); !strings.Contains(d.Mermaid, "actor "+want) {
			t.Errorf("%s: %s", kind, d.Mermaid)
		}
	}
}
