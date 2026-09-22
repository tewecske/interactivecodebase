// Command icb analyzes a Go web codebase and serves an interactive view of its
// routes, call flows, sinks and database schema.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/tewecske/interactivecodebase/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
