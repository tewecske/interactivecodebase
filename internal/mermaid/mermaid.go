// Package mermaid renders code-graph views as Mermaid diagrams: a site map
// flowchart, a flow sequence diagram, a class diagram and an ER diagram.
//
// Each diagram comes with the graph node IDs behind its Mermaid node IDs,
// so a UI can make diagram nodes clickable.
package mermaid

import (
	"fmt"
	"regexp"
	"strings"
)

// Diagram is Mermaid source plus the graph node each Mermaid ID stands for.
type Diagram struct {
	Mermaid string            `json:"mermaid"`
	IDs     map[string]string `json:"ids"`
}

// builder accumulates diagram lines and assigns short Mermaid IDs.
type builder struct {
	lines  []string
	ids    map[string]string // mermaid id -> graph id
	byNode map[string]string // graph id -> mermaid id
	prefix string
}

func newBuilder(header, prefix string) *builder {
	return &builder{lines: []string{header}, ids: map[string]string{}, byNode: map[string]string{}, prefix: prefix}
}

func (b *builder) line(format string, args ...any) {
	b.lines = append(b.lines, fmt.Sprintf(format, args...))
}

// id returns the Mermaid ID for a graph node, assigning one on first use.
func (b *builder) id(graphID string) string {
	if id, ok := b.byNode[graphID]; ok {
		return id
	}
	id := fmt.Sprintf("%s%d", b.prefix, len(b.byNode))
	b.byNode[graphID] = id
	b.ids[id] = graphID
	return id
}

func (b *builder) diagram() Diagram {
	return Diagram{Mermaid: strings.Join(b.lines, "\n") + "\n", IDs: b.ids}
}

// label escapes text for a quoted Mermaid label.
func label(s string) string {
	r := strings.NewReplacer(`"`, "#quot;", "\n", " ", "<", "#lt;", ">", "#gt;")
	return r.Replace(s)
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// ident turns text into a Mermaid-safe identifier.
func ident(s string) string {
	s = nonWord.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "x"
	}
	if s[0] >= '0' && s[0] <= '9' {
		return "_" + s
	}
	return s
}
