package mermaid

// TypeInfo describes a named type for a class diagram.
type TypeInfo struct {
	ID      string   // graph node ID, for clicks
	Name    string   // display name, e.g. "postgres.NoteRepository"
	Kind    string   // "struct", "interface" or "type"
	Fields  []Member // struct fields
	Methods []Member
	// Implements lists the Names of interfaces in the diagram it satisfies.
	Implements []string
	// Uses lists the Names of other types in the diagram its fields refer to.
	Uses []string
}

// Member is a field or method.
type Member struct {
	Name, Type string
	Exported   bool
}

// Classes draws types with their fields and methods, interface
// implementations and field references.
func Classes(types []TypeInfo) Diagram {
	b := newBuilder("classDiagram", "c")
	names := map[string]string{}
	for _, t := range types {
		alias := ident(t.Name)
		b.ids[alias] = t.ID
		b.byNode[t.ID] = alias
		names[t.Name] = alias
		b.line("  class %s {", alias)
		if t.Kind == "interface" {
			b.line("    <<interface>>")
		}
		for _, f := range t.Fields {
			b.line("    %s%s %s", visibility(f), memberText(f.Type), memberText(f.Name))
		}
		for _, m := range t.Methods {
			b.line("    %s%s%s", visibility(m), memberText(m.Name), memberText(m.Type))
		}
		b.line("  }")
	}
	for _, t := range types {
		for _, iface := range t.Implements {
			if to, ok := names[iface]; ok {
				b.line("  %s ..|> %s", names[t.Name], to)
			}
		}
		for _, used := range t.Uses {
			if to, ok := names[used]; ok && to != names[t.Name] {
				b.line("  %s --> %s", names[t.Name], to)
			}
		}
	}
	for _, id := range sortedKeys(b.ids) {
		b.line("  click %s call icbClick()", id)
	}
	return b.diagram()
}

func visibility(m Member) string {
	if m.Exported {
		return "+"
	}
	return "-"
}

// memberText keeps class members parseable: Mermaid reserves ~ for
// generics and braces for class bodies.
func memberText(s string) string {
	r := []rune(s)
	for i, c := range r {
		switch c {
		case '{', '}':
			r[i] = ' '
		case '~':
			r[i] = '-'
		}
	}
	return string(r)
}
