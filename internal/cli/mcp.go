package cli

import (
	"context"
	"errors"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/live"
	"github.com/tewecske/interactivecodebase/internal/mcpserver"
)

// mcpTransport is the transport icb mcp talks over; tests replace it.
var mcpTransport = func() mcp.Transport { return &mcp.StdioTransport{} }

func runMCP(ctx context.Context, e *env, args []string) (err error) {
	fs := newFlagSet(e, "mcp", "icb mcp [flags] <dir>")
	if err := parse(fs, args, 1); err != nil {
		return err
	}
	cur, err := openLive(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cur.Close()) }()
	// stdout carries the protocol; nothing else may be written to it.
	err = mcpserver.New(cur).Run(ctx, mcpTransport())
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// openLive analyzes dir and returns a holder that can re-analyze it.
func openLive(ctx context.Context, dir string) (*live.Current, error) {
	r, release, err := openAnalysis(ctx, dir)
	if err != nil {
		return nil, err
	}
	return live.New(r, release, func(ctx context.Context) (*analysis.Result, error) {
		return analysis.Analyze(ctx, dir, analysis.Options{})
	}), nil
}
