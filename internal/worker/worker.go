// Package worker is the background job queue: the producer–consumer pattern
// built from a buffered channel and one goroutine.
//
// The restaurant picture:
//
//	handler (waiter)  --Enqueue-->  channel (order rail)  --range-->  goroutine (cook)
//
// The handler clips a ticket onto the rail and immediately goes back to the
// client; the cook works through the rail at its own pace. Neither side ever
// waits for the other unless the rail is full.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

// Job types. Constants instead of free strings so a typo fails loudly in
// process() rather than silently matching nothing.
const (
	JobWelcomeEmail = "welcome_email"
)

// jobTimeout is the deadline each job runs under — the background twin of a
// request's context: a hung send gets cut off instead of blocking the queue.
const jobTimeout = 5 * time.Second

// Job is one ticket on the rail: everything the worker needs to do the work
// later, with no access to the original HTTP request (which is long gone by
// the time the job runs).
type Job struct {
	Type   string
	UserID int
	Name   string
	Email  string
}

// Worker owns the queue and the counters. One instance is created in main()
// and shared by every handler — same lifetime as the *sql.DB pool.
type Worker struct {
	queue   chan Job      // the buffered channel: the order rail
	done    chan struct{} // closed by the consumer goroutine when the rail is drained
	timeout time.Duration // per-job deadline; a field (not the constant) so tests can shrink it

	// Counters are atomic because two goroutines touch them at once:
	// handlers (producers) increment queued/dropped while the worker
	// increments processed. Same data-race rule as the mutex notes —
	// atomics are the lighter tool for a lone integer.
	queued    atomic.Int64
	dropped   atomic.Int64
	processed atomic.Int64
	failed    atomic.Int64
}

// New builds a worker with room for `buffer` queued jobs. The buffer is the
// slack between a fast producer and a slow consumer; size it for the burst
// you want to absorb, not for "infinity".
func New(buffer int) *Worker {
	return &Worker{
		queue:   make(chan Job, buffer),
		done:    make(chan struct{}),
		timeout: jobTimeout,
	}
}

// Start launches the consumer goroutine — the cook clocking in.
//
// `for job := range w.queue` is the whole consumption loop: it sleeps while
// the channel is empty, wakes when a job arrives, and exits cleanly when
// Shutdown closes the channel and the remaining jobs run out.
func (w *Worker) Start() {
	go func() {
		defer close(w.done) // signal "rail fully drained" to Shutdown
		for job := range w.queue {
			// Every job runs under its own deadline — the same discipline as
			// a request carrying c.Request.Context(). The parent is
			// context.Background(), NOT the app's signal context, on purpose:
			// Ctrl+C must not cancel in-flight jobs, it drains them; only a
			// job overrunning its own deadline gets cut off.
			ctx, cancel := context.WithTimeout(context.Background(), w.timeout)
			w.process(ctx, job)
			cancel()
		}
	}()
}

// Enqueue is the producer side, called from handlers. It must NEVER block a
// live HTTP request, so the send is wrapped in a select with a default:
// if the rail is full, the job is dropped and logged instead of making the
// client wait. That trade-off (drop vs block) is the backpressure decision.
func (w *Worker) Enqueue(job Job) bool {
	select {
	case w.queue <- job:
		w.queued.Add(1)
		return true
	default:
		w.dropped.Add(1)
		slog.Warn("worker queue full, job dropped",
			"type", job.Type,
			"user_id", job.UserID)
		return false
	}
}

// process does the actual work for one job, under the job's context. Runs
// only on the consumer goroutine, one job at a time.
func (w *Worker) process(ctx context.Context, job Job) {
	start := time.Now()

	switch job.Type {
	case JobWelcomeEmail:
		// Compose the mail, then "send" it. The timer stands in for the SMTP
		// round-trip — and it races the job's deadline in a select, exactly
		// how a context-aware SQL query races a request's cancellation.
		body := fmt.Sprintf("Hello %s! Your account (id %d) is ready.", job.Name, job.UserID)

		select {
		case <-time.After(100 * time.Millisecond): // simulated send completed
			w.processed.Add(1)
			slog.Info("worker sent welcome email",
				"email", job.Email,
				"bytes", len(body),
				"took", time.Since(start).Round(time.Millisecond))

		case <-ctx.Done(): // deadline hit first — abandon the send
			w.failed.Add(1)
			slog.Warn("worker job cancelled",
				"type", job.Type,
				"user_id", job.UserID,
				"err", ctx.Err())
		}

	default:
		w.failed.Add(1)
		slog.Error("worker received unknown job type", "type", job.Type)
	}
}

// Shutdown closes the queue and waits for the worker to finish the jobs
// already on it — closing time: no new orders, but every ticket on the rail
// still gets cooked.
//
// IMPORTANT: call this only after the HTTP server has stopped. Enqueue on a
// closed channel panics, so producers must be gone first. main() guarantees
// the order: srv.Shutdown(...) first, then jobs.Shutdown(...).
func (w *Worker) Shutdown(ctx context.Context) error {
	close(w.queue)

	select {
	case <-w.done: // drained everything
		return nil
	case <-ctx.Done(): // took too long — give up and report it
		return ctx.Err()
	}
}

// Stats is the JSON shape returned by GET /jobs/stats.
type Stats struct {
	Queued    int64 `json:"queued"`
	Processed int64 `json:"processed"`
	Dropped   int64 `json:"dropped"`
	Failed    int64 `json:"failed"`
	Pending   int   `json:"pending"`
}

// Stats reports the counters plus how many jobs are sitting on the rail right
// now (len on a channel is how many values it currently buffers).
func (w *Worker) Stats() Stats {
	return Stats{
		Queued:    w.queued.Load(),
		Processed: w.processed.Load(),
		Dropped:   w.dropped.Load(),
		Failed:    w.failed.Load(),
		Pending:   len(w.queue),
	}
}
