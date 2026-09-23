// Package worker runs named jobs on an interval.
package worker

import (
	"context"
	"time"
)

// Job is a named task.
type Job struct {
	Name string
	Run  func(context.Context) error
}

// Worker runs jobs until its context ends.
type Worker struct {
	interval time.Duration
	jobs     []Job
}

func New(interval time.Duration, jobs ...Job) *Worker { return &Worker{interval: interval, jobs: jobs} }

func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, job := range w.jobs {
				_ = job.Run(ctx)
			}
		}
	}
}
