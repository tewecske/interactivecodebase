package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/mcpserver"
	"github.com/tewecske/interactivecodebase/internal/server"
	"github.com/tewecske/interactivecodebase/internal/webui"
)

// shutdownTimeout bounds graceful shutdown.
const shutdownTimeout = 5 * time.Second

func runServe(ctx context.Context, e *env, args []string) (err error) {
	fs := newFlagSet(e, "serve", "icb serve [flags] <dir>")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address")
	watch := fs.Bool("watch", false, "re-analyze when source files change")
	if err := parse(fs, args, 1); err != nil {
		return err
	}
	if *watch {
		return fmt.Errorf("-watch: %w (see #25)", errNotImplemented)
	}
	cur, err := openLive(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cur.Close()) }()
	var module, took string
	_ = cur.With(func(r *analysis.Result) error {
		module, took = r.Module, round(r.Stats.Total()).String()
		return nil
	})

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mcpSrv := mcpserver.New(cur)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil))
	mux.Handle("/", server.NewLive(cur, uiHandler()))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	fmt.Fprintf(e.stdout, "icb: %s analyzed in %s; serving http://%s (MCP at /mcp)\n", module, took, ln.Addr())
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// uiHandler serves the embedded web UI, or nil if it was not built in.
func uiHandler() http.Handler { return webui.Handler() }
