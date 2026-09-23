package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/lsp"
)

// PositionIn locates an identifier for the lsp_* tools.
type PositionIn struct {
	File string `json:"file" jsonschema:"path relative to the module root"`
	Line int    `json:"line" jsonschema:"1-based line"`
	Col  int    `json:"col,omitempty" jsonschema:"1-based byte column of the identifier; or give name"`
	Name string `json:"name,omitempty" jsonschema:"the identifier on that line, when col is not known"`
}

// column finds the identifier's column: in.Col, or where in.Name first
// appears as a whole word on the line.
func column(dir string, in PositionIn) (int, error) {
	if in.Col > 0 {
		return in.Col, nil
	}
	if in.Name == "" {
		return 0, fmt.Errorf("give col or name")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	b, err := root.ReadFile(filepath.Clean(filepath.FromSlash(in.File)))
	if err != nil {
		return 0, fmt.Errorf("cannot read %s in the module: %w", in.File, err)
	}
	lines := strings.Split(string(b), "\n")
	if in.Line < 1 || in.Line > len(lines) {
		return 0, fmt.Errorf("%s has %d lines", in.File, len(lines))
	}
	text := lines[in.Line-1]
	for from := 0; ; {
		i := strings.Index(text[from:], in.Name)
		if i < 0 {
			return 0, fmt.Errorf("%q is not on line %d of %s: %s", in.Name, in.Line, in.File, strings.TrimSpace(text))
		}
		i += from
		end := i + len(in.Name)
		if (i == 0 || !isIdent(text[i-1])) && (end == len(text) || !isIdent(text[end])) {
			return i + 1, nil
		}
		from = end
	}
}

func isIdent(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func (s *server) lspHover(ctx context.Context, _ *mcp.CallToolRequest, in PositionIn) (*mcp.CallToolResult, any, error) {
	return s.with(func(r *analysis.Result) (string, error) {
		col, err := column(r.Dir, in)
		if err != nil {
			return "", err
		}
		text, err := s.lsp.Hover(ctx, in.File, in.Line, col)
		if err != nil {
			return "", err
		}
		if text == "" {
			return "no hover information at that position\n", nil
		}
		return text + "\n", nil
	})
}

type locationQuery func(c *lsp.Client, ctx context.Context, file string, line, col int) ([]lsp.Location, error)

func (s *server) lspQuery(q locationQuery) mcp.ToolHandlerFor[PositionIn, any] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in PositionIn) (*mcp.CallToolResult, any, error) {
		return s.with(func(r *analysis.Result) (string, error) {
			col, err := column(r.Dir, in)
			if err != nil {
				return "", err
			}
			locs, err := q(s.lsp, ctx, in.File, in.Line, col)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%d locations\n", len(locs))
			for i, l := range locs {
				if i == maxListLines {
					fmt.Fprintf(&b, "… %d more\n", len(locs)-i)
					break
				}
				fmt.Fprintf(&b, "%s:%d:%d\t%s\n", l.File, l.StartLine, l.StartCol, l.Text)
			}
			return b.String(), nil
		})
	}
}
