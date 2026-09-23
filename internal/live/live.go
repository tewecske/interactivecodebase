// Package live holds the current analysis of a project and replaces it
// when the code is analyzed again, without disturbing readers.
package live

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tewecske/interactivecodebase/internal/analysis"
)

// AnalyzeFunc produces a fresh analysis.
type AnalyzeFunc func(ctx context.Context) (*analysis.Project, error)

// Current is the current analysis. Readers use it through With; Reanalyze
// swaps in a new one once in-flight readers are done, then closes the old.
type Current struct {
	analyze AnalyzeFunc

	mu      sync.RWMutex
	p       *analysis.Project
	release func() error // releases p
	gen     int

	reanalyzing sync.Mutex // one reanalysis at a time

	statusMu sync.Mutex
	status   Status
	subs     map[chan Status]struct{}
}

// Status describes the analysis for clients (UI, API).
type Status struct {
	Generation int       `json:"generation"`
	Analyzing  bool      `json:"analyzing"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// DurationMS is how long the last successful analysis took.
	DurationMS int64 `json:"durationMs"`
	// Error is the last reanalysis failure; the previous analysis stays
	// current until one succeeds.
	Error string `json:"error,omitempty"`
}

// Status returns the current status.
func (c *Current) Status() Status {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	return c.status
}

// Subscribe returns a channel receiving each status change (the latest
// one when the reader falls behind) and a function to unsubscribe.
func (c *Current) Subscribe() (<-chan Status, func()) {
	ch := make(chan Status, 1)
	c.statusMu.Lock()
	if c.subs == nil {
		c.subs = map[chan Status]struct{}{}
	}
	c.subs[ch] = struct{}{}
	c.statusMu.Unlock()
	return ch, func() {
		c.statusMu.Lock()
		delete(c.subs, ch)
		c.statusMu.Unlock()
	}
}

// setStatus updates the status and notifies subscribers without blocking.
func (c *Current) setStatus(update func(*Status)) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	update(&c.status)
	c.status.UpdatedAt = time.Now()
	for ch := range c.subs {
		select {
		case <-ch: // drop a stale unread status
		default:
		}
		ch <- c.status
	}
}

// New returns a holder for p, released with release (nil means p.Close)
// once replaced or closed, re-analyzing with analyze.
func New(p *analysis.Project, release func() error, analyze AnalyzeFunc) *Current {
	if release == nil {
		release = p.Close
	}
	c := &Current{p: p, release: release, analyze: analyze, gen: 1}
	c.status = Status{Generation: 1, UpdatedAt: time.Now(), DurationMS: p.Stats.Total().Milliseconds()}
	return c
}

// With calls fn with the current analysis, which stays valid until fn
// returns.
func (c *Current) With(fn func(p *analysis.Project) error) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.p == nil {
		return ErrClosed
	}
	return fn(c.p)
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
	c.setStatus(func(s *Status) { s.Analyzing = true })
	start := time.Now()
	next, err := c.analyze(ctx)
	if err != nil {
		c.setStatus(func(s *Status) { s.Analyzing, s.Error = false, err.Error() })
		return err
	}
	c.mu.Lock()
	if c.p == nil {
		c.mu.Unlock()
		return errors.Join(ErrClosed, next.Close())
	}
	release := c.release
	c.p, c.release = next, next.Close
	c.gen++
	gen := c.gen
	c.mu.Unlock()
	c.setStatus(func(s *Status) {
		*s = Status{Generation: gen, DurationMS: time.Since(start).Milliseconds()}
	})
	return release()
}

// Close releases the current analysis.
func (c *Current) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.p == nil {
		return nil
	}
	err := c.release()
	c.p, c.release = nil, nil
	return err
}
