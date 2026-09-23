// Package watch notices changes to a module's source files by polling
// their size and modification time, and reports them after a quiet
// period so a burst of saves (or a branch switch) triggers one reaction.
package watch

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Options tunes Run.
type Options struct {
	Interval time.Duration // how often to scan; default 1s
	Quiet    time.Duration // how long changes must settle; default 500ms
}

// relevant reports whether a file can change the analysis.
func relevant(name string) bool {
	switch filepath.Ext(name) {
	case ".go", ".html", ".tmpl", ".gohtml", ".sql":
		return true
	}
	return name == "go.mod" || name == "go.sum"
}

// skipDir reports whether a directory is never analyzed.
func skipDir(name string) bool {
	return name == "node_modules" || name == "vendor" || name == "testdata" || (strings.HasPrefix(name, ".") && name != ".")
}

type fileState struct {
	size int64
	mod  time.Time
}

// snapshot fingerprints the relevant files under dir.
func snapshot(dir string) map[string]fileState {
	out := map[string]fileState{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // vanished while walking
		}
		if d.IsDir() {
			if path != dir && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !relevant(d.Name()) {
			return nil
		}
		if info, err := d.Info(); err == nil {
			out[path] = fileState{size: info.Size(), mod: info.ModTime()}
		}
		return nil
	})
	// A branch switch shows as changed sources anyway; HEAD also catches
	// a checkout whose files happen to keep their size and mtime.
	head := filepath.Join(dir, ".git", "HEAD")
	if info, err := os.Stat(head); err == nil {
		out[head] = fileState{size: info.Size(), mod: info.ModTime()}
	}
	return out
}

// diff lists paths added, removed or changed between two snapshots.
func diff(a, b map[string]fileState) []string {
	var out []string
	for p, s := range b {
		if old, ok := a[p]; !ok || old != s {
			out = append(out, p)
		}
	}
	for p := range a {
		if _, ok := b[p]; !ok {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// Run scans dir until ctx ends, calling changed with the changed paths
// once they have been quiet for opts.Quiet. changed runs on Run's
// goroutine; changes made while it runs are reported afterwards.
func Run(ctx context.Context, dir string, opts Options, changed func(paths []string)) {
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if opts.Quiet <= 0 {
		opts.Quiet = 500 * time.Millisecond
	}
	last := snapshot(dir)
	var pending []string
	var settleAt time.Time
	tick := time.NewTicker(min(opts.Interval, opts.Quiet))
	defer tick.Stop()
	nextScan := time.Now().Add(opts.Interval)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			// Scan every Interval, and once more when pending changes
			// should have settled, to confirm they have.
			if now.Before(nextScan) && (len(pending) == 0 || now.Before(settleAt)) {
				continue
			}
			cur := snapshot(dir)
			if d := diff(last, cur); len(d) > 0 {
				for _, p := range d {
					if !slices.Contains(pending, p) {
						pending = append(pending, p)
					}
				}
				settleAt = now.Add(opts.Quiet)
				last = cur
			}
			nextScan = now.Add(opts.Interval)
			if len(pending) > 0 && !now.Before(settleAt) {
				changed(pending)
				pending = nil
			}
		}
	}
}
