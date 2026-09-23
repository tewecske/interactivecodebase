package cli

import (
	"context"
	"errors"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/live"
	"github.com/tewecske/interactivecodebase/internal/lsp"
	"github.com/tewecske/interactivecodebase/internal/mcpserver"
)

// mcpTransport is the transport icb mcp talks over; tests replace it.
var mcpTransport = func() mcp.Transport { return &mcp.StdioTransport{} }

func runMCP(ctx context.Context, e *env, args []string) (err error) {
	fs := newFlagSet(e, "mcp", "icb mcp [flags] <dir>")
	cfgPath := configFlag(fs)
	if err := parse(fs, args, 1); err != nil {
		return err
	}
	cur, err := openLive(ctx, fs.Arg(0), *cfgPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cur.Close()) }()
	gopls := goplsFor(cur)
	defer func() { err = errors.Join(err, gopls.Close()) }()
	// stdout carries the protocol; nothing else may be written to it.
	err = mcpserver.New(cur, gopls).Run(ctx, mcpTransport())
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// goplsFor returns a gopls client for the analyzed Go module; it starts
// gopls only when first used and does nothing if gopls is missing. It
// returns nil for a project in another language.
func goplsFor(cur *live.Current) *lsp.Client {
	var dir string
	_ = cur.With(func(p *analysis.Project) error {
		if p.Go != nil {
			dir = p.Dir
		}
		return nil
	})
	if dir == "" {
		return nil
	}
	return lsp.New(dir)
}

// openLive analyzes dir and returns a holder that can re-analyze it,
// reading the config again each time.
func openLive(ctx context.Context, dir, cfgPath string) (*live.Current, error) {
	opts, err := loadOptions(dir, cfgPath)
	if err != nil {
		return nil, err
	}
	p, release, err := openAnalysis(ctx, dir, opts)
	if err != nil {
		return nil, err
	}
	return live.New(p, release, func(ctx context.Context) (*analysis.Project, error) {
		opts, err := loadOptions(dir, cfgPath)
		if err != nil {
			return nil, err
		}
		r, err := analysis.Analyze(ctx, dir, opts)
		if err != nil {
			return nil, err
		}
		return r.Project(), nil
	}), nil
}
