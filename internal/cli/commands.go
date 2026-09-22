package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
)

// errUsage reports invalid arguments; the flag set has already printed usage.
var errUsage = errors.New("usage error")

func newFlagSet(e *env, name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  %s\n", usage)
		if hasFlags(fs) {
			fmt.Fprintln(fs.Output(), "\nFlags:")
			fs.PrintDefaults()
		}
	}
	return fs
}

func hasFlags(fs *flag.FlagSet) bool {
	found := false
	fs.VisitAll(func(*flag.Flag) { found = true })
	return found
}

// parse parses args and checks the positional argument count.
func parse(fs *flag.FlagSet, args []string, positional int) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errUsage
	}
	if fs.NArg() != positional {
		fmt.Fprintf(fs.Output(), "expected %d argument(s), got %d\n\n", positional, fs.NArg())
		fs.Usage()
		return errUsage
	}
	return nil
}

// checkDir verifies that dir exists and is a directory.
func checkDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}

func runAnalyze(_ context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "analyze", "icb analyze [flags] <dir>")
	fs.Bool("json", false, "print the summary as JSON")
	if err := parse(fs, args, 1); err != nil {
		return err
	}
	if err := checkDir(fs.Arg(0)); err != nil {
		return err
	}
	return fmt.Errorf("%w (see #4)", errNotImplemented)
}

func runServe(_ context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "serve", "icb serve [flags] <dir>")
	fs.String("addr", "127.0.0.1:8080", "listen address")
	fs.Bool("watch", false, "re-analyze when source files change")
	if err := parse(fs, args, 1); err != nil {
		return err
	}
	if err := checkDir(fs.Arg(0)); err != nil {
		return err
	}
	return fmt.Errorf("%w (see #13)", errNotImplemented)
}

func runMCP(_ context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "mcp", "icb mcp [flags] <dir>")
	if err := parse(fs, args, 1); err != nil {
		return err
	}
	if err := checkDir(fs.Arg(0)); err != nil {
		return err
	}
	return fmt.Errorf("%w (see #21)", errNotImplemented)
}

func runQuery(_ context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "query", "icb query [flags] <dir> <query>")
	fs.Bool("json", false, "print results as JSON")
	if err := parse(fs, args, 2); err != nil {
		return err
	}
	if err := checkDir(fs.Arg(0)); err != nil {
		return err
	}
	return fmt.Errorf("%w (see #6)", errNotImplemented)
}

func runVersion(_ context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "version", "icb version")
	if err := parse(fs, args, 0); err != nil {
		return err
	}
	_, err := fmt.Fprintf(e.stdout, "icb %s\n", Version)
	return err
}
