package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
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
	return parseRange(fs, args, positional, positional)
}

// parseRange parses args and checks that there are between minArgs and
// maxArgs positional arguments; maxArgs < 0 means no upper bound.
func parseRange(fs *flag.FlagSet, args []string, minArgs, maxArgs int) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errUsage
	}
	if n := fs.NArg(); n < minArgs || (maxArgs >= 0 && n > maxArgs) {
		want := strconv.Itoa(minArgs)
		switch {
		case maxArgs < 0:
			want = "at least " + want
		case maxArgs != minArgs:
			want += "-" + strconv.Itoa(maxArgs)
		}
		fmt.Fprintf(fs.Output(), "expected %s argument(s), got %d\n\n", want, n)
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

func runVersion(_ context.Context, e *env, args []string) error {
	fs := newFlagSet(e, "version", "icb version")
	if err := parse(fs, args, 0); err != nil {
		return err
	}
	_, err := fmt.Fprintf(e.stdout, "icb %s\n", Version)
	return err
}
