package worker

import (
	"context"
	"testing"
	"time"
)

// TestWorkerDrainsAllJobsOnShutdown proves the full producer→queue→consumer
// path: everything enqueued is processed, and Shutdown waits for the drain.
func TestWorkerDrainsAllJobsOnShutdown(t *testing.T) {
	w := New(10)
	w.Start()

	const n = 5
	for i := 1; i <= n; i++ {
		ok := w.Enqueue(Job{Type: JobWelcomeEmail, UserID: i, Name: "U", Email: "u@example.com"})
		if !ok {
			t.Fatalf("enqueue %d: unexpectedly dropped", i)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := w.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown did not drain in time: %v", err)
	}

	stats := w.Stats()
	if stats.Processed != n {
		t.Errorf("expected %d processed, got %d", n, stats.Processed)
	}
	if stats.Dropped != 0 {
		t.Errorf("expected 0 dropped, got %d", stats.Dropped)
	}
	if stats.Pending != 0 {
		t.Errorf("expected an empty queue after drain, got %d pending", stats.Pending)
	}
}

// TestEnqueueDropsWhenQueueIsFull checks the backpressure decision: with no
// consumer running and the buffer full, Enqueue must refuse instead of block.
func TestEnqueueDropsWhenQueueIsFull(t *testing.T) {
	w := New(1) // deliberately never Start()ed — nothing consumes

	if ok := w.Enqueue(Job{Type: JobWelcomeEmail, UserID: 1}); !ok {
		t.Fatal("first enqueue should fit in the buffer")
	}

	// The buffer holds one job and nobody is taking it off. A blocking send
	// would hang this test forever; the select/default must return false fast.
	if ok := w.Enqueue(Job{Type: JobWelcomeEmail, UserID: 2}); ok {
		t.Fatal("second enqueue should have been dropped")
	}

	stats := w.Stats()
	if stats.Queued != 1 || stats.Dropped != 1 {
		t.Errorf("expected 1 queued / 1 dropped, got %d / %d", stats.Queued, stats.Dropped)
	}
}
