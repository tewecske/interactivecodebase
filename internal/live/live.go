// Package live holds the current analysis of a module and replaces it
// when the code is analyzed again, without disturbing readers.
package live

import (
	"context"
	"errors"
	"sync"

	"github.com/tewecske/interactivecodebase/internal/analysis"
)

// AnalyzeFunc produces a fresh analysis.
type AnalyzeFunc func(ctx context.Context) (*analysis.Result, error)

// Current is the current analysis. Readers use it through With; Reanalyze
// swaps in a new one once in-flight readers are done, then closes the old.
type Current struct {
	analyze AnalyzeFunc

	mu      sync.RWMutex
	r       *analysis.Result
	release func() error // releases r
	gen     int

	reanalyzing sync.Mutex // one reanalysis at a time
}

// New returns a holder for r, released with release (nil means r.Close)
// once replaced or closed, re-analyzing with analyze.
func New(r *analysis.Result, release func() error, analyze AnalyzeFunc) *Current {
	if release == nil {
		release = r.Close
	}
	return &Current{r: r, release: release, analyze: analyze, gen: 1}
}

// With calls fn with the current analysis, which stays valid until fn
// returns.
func (c *Current) With(fn func(r *analysis.Result) error) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.r == nil {
		return ErrClosed
	}
	return fn(c.r)
}

// Generation counts analyses: 1 for the first, +1 per reanalysis.
func (c *Current) Generation() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gen
}

// ErrClosed is returned after Close.
var ErrClosed = errors.New("live: closed")

// Reanalyze runs a new analysis and makes it current. On failure the
// previous analysis stays current.
func (c *Current) Reanalyze(ctx context.Context) error {
	c.reanalyzing.Lock()
	defer c.reanalyzing.Unlock()
	if c.analyze == nil {
		return errors.New("live: no analyzer configured")
	}
	next, err := c.analyze(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.r == nil {
		c.mu.Unlock()
		return errors.Join(ErrClosed, next.Close())
	}
	release := c.release
	c.r, c.release = next, next.Close
	c.gen++
	c.mu.Unlock()
	return release()
}

// Close releases the current analysis.
func (c *Current) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.r == nil {
		return nil
	}
	err := c.release()
	c.r, c.release = nil, nil
	return err
}
