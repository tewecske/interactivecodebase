package flow

import (
	"fmt"
	"io"
	"strings"

	"github.com/tewecske/interactivecodebase/internal/graph"
)

// WriteText renders the tree with box-drawing branches, one step per line.
func WriteText(w io.Writer, root *Step) error {
	var err error
	var write func(s *Step, prefix string, last, isRoot bool)
	write = func(s *Step, prefix string, last, isRoot bool) {
		if err != nil {
			return
		}
		branch, next := "", ""
		if !isRoot {
			branch, next = "├─ ", "│  "
			if last {
				branch, next = "└─ ", "   "
			}
		}
		_, err = fmt.Fprintf(w, "%s%s%s\n", prefix, branch, label(s))
		for i, c := range s.Children {
			write(c, prefix+next, i == len(s.Children)-1, false)
		}
	}
	write(root, "", true, true)
	return err
}

// label is a one-line description of a step.
func label(s *Step) string {
	n := s.Node
	var b strings.Builder
	switch {
	case n.Kind == graph.KindRoute:
		b.WriteString(n.Name)
	case n.Kind == graph.KindEntry:
		fmt.Fprintf(&b, "%s %s", n.Attrs["entryKind"], n.Name)
	case n.Kind == graph.KindSQLTable:
		fmt.Fprintf(&b, "table %s (%s", n.Name, s.Edge.Attrs["op"])
		if cols := s.Edge.Attrs["columns"]; cols != "" {
			fmt.Fprintf(&b, ": %s", cols)
		}
		b.WriteString(")")
	case isSink(n):
		fmt.Fprintf(&b, "%s %s", n.Kind, n.Name)
		if d := summary(n.Detail); d != "" {
			fmt.Fprintf(&b, " %q", d)
		}
	case n.Kind == graph.KindInterfaceCall:
		fmt.Fprintf(&b, "interface %s", n.Name)
	default:
		b.WriteString(shortName(n))
	}
	if s.Edge.Kind == graph.EdgeDispatchesTo {
		b.WriteString(" [impl]")
	}
	if s.Calls > 1 {
		fmt.Fprintf(&b, " ×%d", s.Calls)
	}
	if _, branch := Parallel(s); branch != "" {
		fmt.Fprintf(&b, " ∥%s", branch)
	}
	if p := n.Pos; p.File != "" && n.Kind != graph.KindSQLTable {
		fmt.Fprintf(&b, "  %s:%d", p.File, p.StartLine)
	}
	switch {
	case s.Cycle:
		b.WriteString("  (recursive)")
	case s.Ref:
		b.WriteString("  (see above)")
	case s.Truncated:
		b.WriteString("  (depth limit)")
	}
	return b.String()
}

// shortName is the node's name qualified with the last package segment.
func shortName(n graph.Node) string {
	pkg := n.Package[strings.LastIndex(n.Package, "/")+1:]
	if pkg == "" {
		return n.Name
	}
	if rest, ok := strings.CutPrefix(n.Name, "(*"); ok {
		return "(*" + pkg + "." + rest
	}
	if rest, ok := strings.CutPrefix(n.Name, "("); ok {
		return "(" + pkg + "." + rest
	}
	return pkg + "." + n.Name
}

// summary collapses whitespace (SQL spans lines) and truncates.
func summary(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const maxLen = 90
	if len(s) > maxLen {
		s = s[:maxLen] + "…"
	}
	return s
}
