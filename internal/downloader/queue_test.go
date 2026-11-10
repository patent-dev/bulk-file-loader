package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

// fastQueue shortens the queue timings for the test.
func fastQueue(t *testing.T) {
	t.Helper()
	base, poll, lease := retryBaseDelay, pollInterval, leaseDuration
	retryBaseDelay, pollInterval, leaseDuration = 10*time.Millisecond, 10*time.Millisecond, time.Second
	t.Cleanup(func() { retryBaseDelay, pollInterval, leaseDuration = base, poll, lease })
}

// scriptedAdapter fails the first failures attempts per file with err, then
// writes the content.
type scriptedAdapter struct {
	mockAdapter
	failures int
	err      error
	calls    atomic.Int32
	perFile  map[string]*atomic.Int32
	block    chan struct{} // when set, downloads wait on it or on ctx
}

func (a *scriptedAdapter) DownloadFile(ctx context.Context, file sources.FileInfo, w io.Writer, _ sources.ProgressFunc) error {
	a.calls.Add(1)
	n := a.perFile[file.ExternalID].Add(1)
	if a.block != nil {
		select {
		case <-a.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if int(n) <= a.failures {
		return a.err
	}
	_, err := io.WriteString(w, "PK")
	return err
}

func seedQueueFiles(t *testing.T, db *database.DB, a *scriptedAdapter, n int) []string {
	t.Helper()
	db.Create(&database.Source{ID: "mock", Name: "Mock", Enabled: true})
	db.Create(&database.Product{ID: "prod", SourceID: "mock", Name: "Product"})
	db.Create(&database.Delivery{ID: "del", ProductID: "prod", Name: "Delivery"})
	a.perFile = map[string]*atomic.Int32{}
	var ids []string
	for i := range n {
		id := fmt.Sprintf("file-%d", i)
		a.perFile[id] = &atomic.Int32{}
		db.Create(&database.File{ID: id, DeliveryID: "del", ProductID: "prod", SourceID: "mock", ExternalID: id, FileName: id + ".zip"})
		ids = append(ids, id)
	}
	return ids
}

func runBatch(t *testing.T, d *Downloader, owner string, ids []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d.RunWorkers(ctx, owner, ids)
	if ctx.Err() != nil {
		t.Fatal("workers did not finish the batch")
	}
}

func jobsByState(t *testing.T, db *database.DB) map[string]database.DownloadJob {
	t.Helper()
	var jobs []database.DownloadJob
	db.Find(&jobs)
	out := map[string]database.DownloadJob{}
	for _, j := range jobs {
		out[j.FileID] = j
	}
	return out
}

func TestQueueDownloadsBatch(t *testing.T) {
	fastQueue(t)
	db, registry, hooksManager, cfg := setupTestEnv(t)
	a := &scriptedAdapter{mockAdapter: mockAdapter{}}
	registry.Register(a)
	ids := seedQueueFiles(t, db, a, 5)
	d := New(db, registry, hooksManager, cfg)

	if n, err := d.Enqueue(append(ids, ids[0]), "cli"); err != nil || n != 5 {
		t.Fatalf("Enqueue = %d, %v; want 5 (duplicates queued once)", n, err)
	}
	// Queued again (another command, another process): nothing changes.
	if n, err := d.Enqueue(ids, "api"); err != nil || n != 0 {
		t.Fatalf("second Enqueue = %d, %v; want 0", n, err)
	}
	runBatch(t, d, "worker", ids)

	if jobs := jobsByState(t, db); len(jobs) != 0 {
		t.Errorf("jobs left after success: %v", jobs)
	}
	var completed int64
	db.Model(&database.DownloadEntry{}).Where("status = ?", database.DownloadStatusCompleted).Count(&completed)
	if completed != 5 || a.calls.Load() != 5 {
		t.Errorf("completed = %d, adapter calls = %d; want 5 and 5", completed, a.calls.Load())
	}
}

func TestQueueRetriesTransientErrors(t *testing.T) {
	fastQueue(t)
	db, registry, hooksManager, cfg := setupTestEnv(t)
	a := &scriptedAdapter{failures: 2, err: sources.NewAdapterError(sources.ErrCodeNetwork, "connection reset", nil)}
	registry.Register(a)
	ids := seedQueueFiles(t, db, a, 1)
	d := New(db, registry, hooksManager, cfg)

	if _, err := d.Enqueue(ids, "cli"); err != nil {
		t.Fatal(err)
	}
	runBatch(t, d, "worker", ids)

	if jobs := jobsByState(t, db); len(jobs) != 0 {
		t.Errorf("job left after the third attempt succeeded: %+v", jobs)
	}
	if n := a.calls.Load(); n != 3 {
		t.Errorf("attempts = %d, want 3 (two failures, then success)", n)
	}
}

func TestQueueGivesUp(t *testing.T) {
	fastQueue(t)
	cases := []struct {
		name     string
		err      error
		attempts int32
	}{
		{"permanent error", sources.NewAdapterError(sources.ErrCodeAuth, "credentials rejected", nil), 1},
		{"transient error until the limit", sources.NewAdapterError(sources.ErrCodeNetwork, "timeout", nil), 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, registry, hooksManager, cfg := setupTestEnv(t)
			a := &scriptedAdapter{failures: 99, err: c.err}
			registry.Register(a)
			ids := seedQueueFiles(t, db, a, 1)
			d := New(db, registry, hooksManager, cfg)
			if _, err := d.Enqueue(ids, "cli"); err != nil {
				t.Fatal(err)
			}
			runBatch(t, d, "worker", ids)

			job := jobsByState(t, db)[ids[0]]
			if job.State != database.JobFailed || int32(job.Attempts) != c.attempts || job.LastError == "" {
				t.Errorf("job = %+v, want failed after %d attempts with the error", job, c.attempts)
			}
			if n := a.calls.Load(); n != c.attempts {
				t.Errorf("adapter calls = %d, want %d", n, c.attempts)
			}

			// Requesting the file again starts over.
			if n, err := d.Enqueue(ids, "cli"); err != nil || n != 1 {
				t.Fatalf("re-enqueue = %d, %v; want 1", n, err)
			}
			if job := jobsByState(t, db)[ids[0]]; job.State != database.JobQueued || job.Attempts != 0 {
				t.Errorf("restarted job = %+v, want queued with no attempts", job)
			}
		})
	}
}

func TestQueueClaimsEachJobOnce(t *testing.T) {
	fastQueue(t)
	db, registry, hooksManager, cfg := setupTestEnv(t)
	a := &scriptedAdapter{}
	registry.Register(a)
	ids := seedQueueFiles(t, db, a, 12)
	// Two processes queue the same files at the same time, then drain them
	// concurrently.
	enqueued := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := New(db, registry, hooksManager, cfg).Enqueue(ids, "cli")
			enqueued <- err
		}()
	}
	for range 2 {
		if err := <-enqueued; err != nil {
			t.Fatalf("concurrent Enqueue: %v", err)
		}
	}
	done := make(chan struct{})
	for _, owner := range []string{"process-a", "process-b"} {
		go func() {
			runBatch(t, New(db, registry, hooksManager, cfg), owner, ids)
			done <- struct{}{}
		}()
	}
	<-done
	<-done

	for id, n := range a.perFile {
		if n.Load() != 1 {
			t.Errorf("%s downloaded %d times, want once", id, n.Load())
		}
	}
}

func TestQueueTakesOverExpiredLease(t *testing.T) {
	fastQueue(t)
	db, registry, hooksManager, cfg := setupTestEnv(t)
	a := &scriptedAdapter{}
	registry.Register(a)
	ids := seedQueueFiles(t, db, a, 1)
	// A process claimed the job and died: its lease has run out.
	expired := time.Now().UTC().Add(-time.Minute)
	db.Create(&database.DownloadJob{FileID: ids[0], State: database.JobRunning, Attempts: 1, MaxAttempts: 3,
		RequestedBy: "cli", ClaimedBy: "dead-process", LeaseUntil: &expired, NextAttemptAt: expired})

	runBatch(t, New(db, registry, hooksManager, cfg), "worker", ids)
	if a.calls.Load() != 1 || len(jobsByState(t, db)) != 0 {
		t.Errorf("calls = %d, jobs = %v; want the job taken over and finished", a.calls.Load(), jobsByState(t, db))
	}
}

func TestQueueShutdownReturnsJob(t *testing.T) {
	fastQueue(t)
	db, registry, hooksManager, cfg := setupTestEnv(t)
	a := &scriptedAdapter{block: make(chan struct{})}
	registry.Register(a)
	ids := seedQueueFiles(t, db, a, 1)
	d := New(db, registry, hooksManager, cfg)
	if _, err := d.Enqueue(ids, "server"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	stopped := make(chan struct{})
	go func() { d.RunWorkers(ctx, "server", nil); close(stopped) }()
	for a.calls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel(ErrShutdown)
	<-stopped

	job := jobsByState(t, db)[ids[0]]
	if job.State != database.JobQueued || job.Attempts != 0 {
		t.Errorf("job after shutdown = %+v, want queued again without a counted attempt", job)
	}
	var entry database.DownloadEntry
	db.Where("file_id = ?", ids[0]).First(&entry)
	if entry.ErrorMessage != "interrupted by shutdown" {
		t.Errorf("entry = %+v, want it marked as interrupted by shutdown, not cancelled by a user", entry)
	}
}

// runBlocked starts a worker on one blocked download and returns once the
// download has begun.
func runBlocked(t *testing.T, owner string) (*database.DB, *Downloader, *scriptedAdapter, []string, chan struct{}) {
	t.Helper()
	db, registry, hooksManager, cfg := setupTestEnv(t)
	a := &scriptedAdapter{block: make(chan struct{})}
	registry.Register(a)
	ids := seedQueueFiles(t, db, a, 1)
	d := New(db, registry, hooksManager, cfg)
	if _, err := d.Enqueue(ids, "server"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	finished := make(chan struct{})
	go func() { d.RunWorkers(ctx, owner, ids); close(finished) }()
	// Stop the workers before fastQueue restores the timings they read.
	t.Cleanup(func() { cancel(); <-finished })
	for a.calls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	return db, d, a, ids, finished
}

func TestQueueCancelRequestStopsDownloadInAnotherProcess(t *testing.T) {
	fastQueue(t)
	db, d, _, ids, finished := runBlocked(t, "server")

	// Another process (the CLI or the UI's server) asks for the cancel.
	if running, err := d.RequestCancel(ids[0]); err != nil || !running {
		t.Fatalf("RequestCancel = %v, %v; want the running job flagged", running, err)
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the running download did not stop after the cancel request")
	}
	if jobs := jobsByState(t, db); len(jobs) != 0 {
		t.Errorf("jobs = %v, want the cancelled job removed", jobs)
	}
}

func TestQueueLostLeaseLeavesJobToNewOwner(t *testing.T) {
	fastQueue(t)
	db, _, _, ids, _ := runBlocked(t, "slow-worker")

	// Another worker took the job over (the lease had expired).
	db.Model(&database.DownloadJob{}).Where("file_id = ?", ids[0]).Update("claimed_by", "new-worker")

	// The old worker stops its download without touching the job.
	deadline := time.Now().Add(5 * time.Second)
	var entry database.DownloadEntry
	for time.Now().Before(deadline) {
		db.Where("file_id = ?", ids[0]).First(&entry)
		if entry.Status != database.DownloadStatusDownloading {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if entry.Status != database.DownloadStatusCancelled || entry.ErrorMessage != errLeaseLost.Error() {
		t.Errorf("entry = %+v, want the download stopped because the job was taken over", entry)
	}
	if job := jobsByState(t, db)[ids[0]]; job.ClaimedBy != "new-worker" || job.State != database.JobRunning {
		t.Errorf("job = %+v, want it left to the new owner", job)
	}
}

func TestPermanent(t *testing.T) {
	if !permanent(fmt.Errorf("download: %w", sources.NewAdapterError(sources.ErrCodeNotFound, "gone", nil))) {
		t.Error("a wrapped not-found error is permanent")
	}
	if permanent(sources.NewAdapterError(sources.ErrCodeRateLimit, "slow down", nil)) || permanent(errors.New("EOF")) {
		t.Error("rate limits and unknown errors are transient")
	}
}

func TestBackoff(t *testing.T) {
	for attempt, want := range map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 4: 8 * time.Minute, 10: time.Hour, 29: time.Hour, 30: time.Hour, 64: time.Hour} {
		got := backoff(attempt)
		if got < want*8/10 || got > want*12/10 {
			t.Errorf("backoff(%d) = %v, want %v within 20%%", attempt, got, want)
		}
	}
}
