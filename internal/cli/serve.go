package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/tewecske/interactivecodebase/internal/server"
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
	r, release, err := openAnalysis(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           server.New(r, uiHandler()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(e.stdout, "icb: %s analyzed in %v; serving http://%s\n", r.Module, round(r.Stats.Total()), ln.Addr())
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

// uiHandler serves the embedded web UI; nil until the UI is built in (#14).
func uiHandler() http.Handler { return nil }
