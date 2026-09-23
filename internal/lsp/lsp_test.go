package lsp

import (
	"strings"
	"testing"

	"github.com/tewecske/interactivecodebase/internal/fixture"
)

func TestColumns(t *testing.T) {
	line := "x := \"héllo\" // 𝄞 note"
	for byteOff := 0; byteOff <= len(line); byteOff++ {
		if !strings.HasPrefix(line[byteOff:], "") {
			continue
		}
		if u := utf16Col(line, byteOff); byteCol(line, u) != byteOff && isRuneStart(line, byteOff) {
			t.Errorf("byte %d -> utf16 %d -> byte %d", byteOff, u, byteCol(line, u))
		}
	}
	if utf16Col(line, strings.Index(line, "note")) != 19 { // 𝄞 is two UTF-16 units
		t.Errorf("utf16 column of note = %d", utf16Col(line, strings.Index(line, "note")))
	}
}

func isRuneStart(s string, i int) bool { return i == len(s) || s[i]&0xC0 != 0x80 }

// TestGopls runs gopls on the webapp fixture.
func TestGopls(t *testing.T) {
	c := New(fixture.WebappDir())
	if !c.Available() {
		t.Skip("gopls not installed")
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := t.Context()
	const file = "internal/web/handlers.go"
	line, col := findIdent(t, file, "h.auth.Authenticate(req)", "Authenticate")

	hover, err := c.Hover(ctx, file, line, col)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hover, "func (a *Authenticator) Authenticate(req *http.Request)") {
		t.Errorf("hover = %q", hover)
	}
	defs, err := c.Definition(ctx, file, line, col)
	if err != nil || len(defs) != 1 || defs[0].File != "internal/web/auth.go" || !strings.Contains(defs[0].Text, "func (a *Authenticator) Authenticate") {
		t.Errorf("definition = %+v, %v", defs, err)
	}
	refs, err := c.References(ctx, file, line, col)
	if err != nil || len(refs) < 5 {
		t.Errorf("references = %d, %v", len(refs), err)
	}
	for _, r := range refs {
		if r.EndCol-r.StartCol != len("Authenticate") {
			t.Errorf("reference %+v does not span the name", r)
		}
	}

	iline, icol := findIdent(t, "internal/notes/service.go", "type Repository interface", "Repository")
	impls, err := c.Implementations(ctx, "internal/notes/service.go", iline, icol)
	if err != nil || len(impls) != 1 || impls[0].File != "internal/store/postgres/notes.go" {
		t.Errorf("implementations = %+v, %v", impls, err)
	}

	if _, err := c.Hover(ctx, "../go.mod", 1, 1); err == nil {
		t.Error("path outside the module accepted")
	}
}

// findIdent returns the 1-based line and byte column of name within the
// first line of file containing context.
func findIdent(t *testing.T, file, context, name string) (int, int) {
	t.Helper()
	lines := readLines(fixture.WebappDir() + "/" + file)
	for i, l := range lines {
		if j := strings.Index(l, context); j >= 0 {
			return i + 1, j + strings.Index(context, name) + 1
		}
	}
	t.Fatalf("%s: no line with %q", file, context)
	return 0, 0
}
