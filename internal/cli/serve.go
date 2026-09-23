package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/live"
	"github.com/tewecske/interactivecodebase/internal/mcpserver"
	"github.com/tewecske/interactivecodebase/internal/server"
	"github.com/tewecske/interactivecodebase/internal/watch"
	"github.com/tewecske/interactivecodebase/internal/webui"
)

// shutdownTimeout bounds graceful shutdown.
const shutdownTimeout = 5 * time.Second

func runServe(ctx context.Context, e *env, args []string) (err error) {
	fs := newFlagSet(e, "serve", "icb serve [flags] <dir>")
	addr := fs.String("addr", "127.0.0.1:8080", "listen address; use 0.0.0.0:8080 to serve other machines")
	watch := fs.Bool("watch", false, "re-analyze when source files change")
	token := fs.String("token", os.Getenv("ICB_TOKEN"), "access token (default $ICB_TOKEN, else a random one is printed)")
	noAuth := fs.Bool("insecure-no-auth", false, "serve without authentication (only allowed on a loopback address)")
	certFile := fs.String("tls-cert", "", "TLS certificate file (with -tls-key) to serve HTTPS")
	keyFile := fs.String("tls-key", "", "TLS private key file")
	cfgPath := configFlag(fs)
	if err := parse(fs, args, 1); err != nil {
		return err
	}
	if (*certFile == "") != (*keyFile == "") {
		return errors.New("-tls-cert and -tls-key go together")
	}
	if *noAuth && !isLoopback(*addr) {
		return fmt.Errorf("-insecure-no-auth is only allowed on a loopback address, not %s: icb serves your source code", *addr)
	}
	cur, err := openLive(ctx, fs.Arg(0), *cfgPath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, cur.Close()) }()
	var module, took, root string
	_ = cur.With(func(p *analysis.Project) error {
		module, took, root = p.Module, round(p.Stats.Total()).String(), p.Dir
		return nil
	})

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	gopls := goplsFor(cur)
	defer func() { err = errors.Join(err, gopls.Close()) }()
	mux := http.NewServeMux()
	mcpSrv := mcpserver.New(cur, gopls)
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil))
	mux.Handle("/", server.NewLive(cur, uiHandler(), gopls))
	var handler http.Handler = mux
	generated := false
	if !*noAuth {
		if *token == "" {
			*token, generated = rand.Text(), true
		}
		handler = server.RequireToken(server.AuthConfig{Token: *token, Secure: *certFile != ""}, handler)
	}
	srv := &http.Server{
		Handler:           server.SecurityHeaders(handler),
		ReadHeaderTimeout: 10 * time.Second,
		// Requests end with ctx, so open event streams do not hold up
		// shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	if *watch {
		go watchAndReanalyze(ctx, e, cur, root)
		fmt.Fprintf(e.stdout, "icb: watching %s for changes\n", root)
	}

	scheme := "http"
	if *certFile != "" {
		scheme = "https"
	}
	base := fmt.Sprintf("%s://%s", scheme, displayAddr(ln.Addr()))
	fmt.Fprintf(e.stdout, "icb: %s analyzed in %s; serving %s (MCP at /mcp)\n", module, took, base)
	switch {
	case *noAuth:
		fmt.Fprintln(e.stdout, "icb: authentication is OFF (-insecure-no-auth)")
	case generated:
		fmt.Fprintf(e.stdout, "icb: open %s/?token=%s\nicb: API and MCP clients send \"Authorization: Bearer %s\"\n", base, *token, *token)
	default:
		fmt.Fprintln(e.stdout, "icb: sign in with your token; API and MCP clients send it as a Bearer token")
	}
	serveErr := make(chan error, 1)
	go func() {
		if *certFile != "" {
			serveErr <- srv.ServeTLS(ln, *certFile, *keyFile)
		} else {
			serveErr <- srv.Serve(ln)
		}
	}()
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

// isLoopback reports whether a listen address only accepts local
// connections. An empty host (":8080") listens everywhere.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// displayAddr shows a listener address, naming a wildcard host localhost.
func displayAddr(a net.Addr) string {
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

// uiHandler serves the embedded web UI, or nil if it was not built in.
func uiHandler() http.Handler { return webui.Handler() }

// watchAndReanalyze re-analyzes the module whenever its sources change,
// until ctx ends. A failed analysis is reported and the last good one
// stays in service.
func watchAndReanalyze(ctx context.Context, e *env, cur *live.Current, root string) {
	watch.Run(ctx, root, watch.Options{}, func(paths []string) {
		fmt.Fprintf(e.stdout, "icb: %d file(s) changed (%s); re-analyzing\n", len(paths), relTo(root, paths[0]))
		start := time.Now()
		if err := cur.Reanalyze(ctx); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(e.stderr, "icb: re-analysis failed, still serving the previous analysis: %v\n", err)
			}
			return
		}
		fmt.Fprintf(e.stdout, "icb: re-analyzed in %s (generation %d)\n", round(time.Since(start)), cur.Generation())
	})
}

// relTo shows path relative to root when it can.
func relTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}
