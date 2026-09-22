package mermaid

import (
	"fmt"
	"strings"

	"github.com/tewecske/interactivecodebase/internal/flow"
	"github.com/tewecske/interactivecodebase/internal/graph"
)

// sinkLanes names the participant for each sink kind.
var sinkLanes = map[graph.NodeKind]string{
	graph.KindSinkSQL:  "Database",
	graph.KindSinkFile: "Files",
	graph.KindSinkHTTP: "External API",
	graph.KindSinkSMTP: "Mail server",
	graph.KindSinkExec: "Process",
	graph.KindSinkEnv:  "Environment",
}

// Sequence draws a route's flow as a sequence diagram: one participant per
// package (the layers: handler, service, store) plus one per kind of
// external system its sinks reach.
func Sequence(root *flow.Step) Diagram {
	s := &sequence{b: newBuilder("sequenceDiagram", "p"), lanes: map[string]string{}}
	s.b.line("  actor Browser")
	s.b.line("  autonumber")
	for _, h := range root.Children {
		s.message("Browser", h, root.Node.Name)
		s.walk(h)
	}
	// Participants are declared implicitly by first use, in order.
	return s.b.diagram()
}

type sequence struct {
	b     *builder
	lanes map[string]string // lane title -> alias
}

func (s *sequence) lane(title string) string {
	if alias, ok := s.lanes[title]; ok {
		return alias
	}
	alias := ident(title)
	for _, used := range s.lanes {
		if used == alias {
			alias = fmt.Sprintf("%s_%d", alias, len(s.lanes))
		}
	}
	s.lanes[title] = alias
	s.b.line("  participant %s as %s", alias, label(title))
	return alias
}

// laneOf is the participant a step runs in.
func (s *sequence) laneOf(n graph.Node) string {
	if title, ok := sinkLanes[n.Kind]; ok {
		return s.lane(title)
	}
	pkg := n.Package[strings.LastIndex(n.Package, "/")+1:]
	if pkg == "" {
		pkg = "external"
	}
	return s.lane(pkg)
}

// walk emits the calls a step makes, in order.
func (s *sequence) walk(step *flow.Step) {
	from := s.laneOf(step.Node)
	for _, c := range step.Children {
		switch {
		case c.Node.Kind == graph.KindInterfaceCall:
			// Show dispatch as a call to each implementation, labelled
			// with the interface method.
			if len(c.Children) == 0 {
				s.message(from, c, "interface "+c.Node.Name)
				continue
			}
			for _, impl := range c.Children {
				s.message(from, impl, c.Node.Name+" → "+impl.Node.Name)
				s.walk(impl)
			}
		case strings.HasPrefix(string(c.Node.Kind), "sink."):
			s.sink(from, c)
		default:
			s.message(from, c, c.Node.Name)
			s.walk(c)
		}
	}
}

func (s *sequence) message(from string, step *flow.Step, text string) {
	to := from
	if step.Node.Kind != graph.KindRoute {
		to = s.laneOf(step.Node)
	}
	switch {
	case step.Cycle:
		text += " (recursive)"
	case step.Ref:
		text += " (as above)"
	case step.Truncated:
		text += " (…)"
	}
	if step.Calls > 1 {
		text += fmt.Sprintf(" ×%d", step.Calls)
	}
	s.b.line("  %s->>%s: %s", from, to, label(text))
}

// sink emits the call to an external system, naming the tables for SQL.
func (s *sequence) sink(from string, step *flow.Step) {
	to := s.laneOf(step.Node)
	text := step.Node.Name
	var tables []string
	for _, t := range step.Children {
		tables = append(tables, strings.ToUpper(t.Edge.Attrs["op"])+" "+t.Node.Name)
	}
	switch {
	case len(tables) > 0:
		text = strings.Join(tables, ", ")
	case step.Node.Detail != "":
		text += " " + summary(step.Node.Detail)
	}
	s.b.line("  %s->>%s: %s", from, to, label(text))
}

// summary collapses whitespace and truncates.
func summary(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 60 {
		s = s[:60] + "…"
	}
	return s
}
