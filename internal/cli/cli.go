// Package cli implements the icb command-line interface.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Exit codes returned by Run.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// Version is the icb version, overridden at build time via -ldflags.
var Version = "dev"

// errNotImplemented marks subcommands whose behaviour lands in a later issue.
var errNotImplemented = errors.New("not implemented yet")

type command struct {
	name    string
	summary string
	usage   string
	run     func(ctx context.Context, env *env, args []string) error
}

type env struct {
	stdout io.Writer
	stderr io.Writer
}

var commands = []command{
	{
		name:    "analyze",
		summary: "Analyze a Go module and print a summary of what was found",
		usage:   "icb analyze [flags] <dir>",
		run:     runAnalyze,
	},
	{
		name:    "serve",
		summary: "Analyze a Go module and serve the web UI, JSON API and MCP endpoint",
		usage:   "icb serve [flags] <dir>",
		run:     runServe,
	},
	{
		name:    "mcp",
		summary: "Analyze a Go module and serve MCP over stdio",
		usage:   "icb mcp [flags] <dir>",
		run:     runMCP,
	},
	{
		name:    "query",
		summary: "Analyze a Go module and run a query against the graph",
		usage:   "icb query [flags] <dir> <command> [args]",
		run:     runQuery,
	},
	{
		name:    "version",
		summary: "Print the icb version",
		usage:   "icb version",
		run:     runVersion,
	},
}

// Run executes icb with args (excluding the program name) and returns the
// process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	e := &env{stdout: stdout, stderr: stderr}
	if len(args) == 0 {
		printUsage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		printUsage(stdout)
		return ExitOK
	}
	i := slices.IndexFunc(commands, func(c command) bool { return c.name == args[0] })
	if i < 0 {
		fmt.Fprintf(stderr, "icb: unknown command %q\n\n", args[0])
		printUsage(stderr)
		return ExitUsage
	}
	cmd := commands[i]
	err := cmd.run(ctx, e, args[1:])
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, flag.ErrHelp):
		return ExitOK
	case errors.Is(err, errUsage):
		return ExitUsage
	default:
		fmt.Fprintf(stderr, "icb %s: %v\n", cmd.name, err)
		return ExitError
	}
}

func printUsage(w io.Writer) {
	var b strings.Builder
	b.WriteString("icb explores Go web codebases: routes, call flows, sinks and SQL schema.\n\n")
	b.WriteString("Usage:\n  icb <command> [flags] [args]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-8s %s\n", c.name, c.summary)
	}
	b.WriteString("\nRun 'icb <command> -h' for command flags.\n")
	io.WriteString(w, b.String())
}
