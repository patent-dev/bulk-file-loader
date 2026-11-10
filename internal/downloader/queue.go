package downloader

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Queue timings; see docs/download-queue.md. Variables so tests can shorten
// them.
var (
	retryBaseDelay = time.Minute
	retryMaxDelay  = time.Hour
	leaseDuration  = 2 * time.Minute
	pollInterval   = 2 * time.Second
)

// ErrShutdown is the cancel cause of a process shutting down: downloads it
// interrupts go back to the queue and are not reported as cancelled.
var ErrShutdown = errors.New("shutting down")

var (
	errLeaseLost       = errors.New("download job taken over by another worker")
	errCancelRequested = errors.New("download cancelled")
)

// queueChunk bounds the IDs per query.
const queueChunk = 500

// now is the queue's clock: UTC, because SQLite compares times as text.
func now() time.Time { return time.Now().UTC() }

// chunks splits ids into slices of at most queueChunk.
func chunks(ids []string) [][]string {
	var out [][]string
	for start := 0; start < len(ids); start += queueChunk {
		out = append(out, ids[start:min(start+queueChunk, len(ids))])
	}
	return out
}

// Enqueue adds jobs for the files. Files already waiting or running keep
// their job; given-up files start over. It returns the number of files newly
// queued or restarted.
func (d *Downloader) Enqueue(fileIDs []string, requestedBy string) (int, error) {
	ids := slices.Compact(slices.Sorted(slices.Values(fileIDs)))
	t := now()
	added := 0
	for _, chunk := range chunks(ids) {
		res := d.db.Model(&database.DownloadJob{}).
			Where("file_id IN ? AND state = ?", chunk, database.JobFailed).
			Updates(map[string]any{
				"state": database.JobQueued, "attempts": 0, "max_attempts": d.cfg.MaxAttempts,
				"next_attempt_at": t, "last_error": "", "requested_by": requestedBy,
				"cancel_requested": false, "version": gorm.Expr("version + 1"),
			})
		if res.Error != nil {
			return added, fmt.Errorf("restart download jobs: %w", res.Error)
		}
		added += int(res.RowsAffected)

		jobs := make([]database.DownloadJob, len(chunk))
		for i, id := range chunk {
			jobs[i] = database.DownloadJob{
				FileID: id, State: database.JobQueued, MaxAttempts: d.cfg.MaxAttempts,
				NextAttemptAt: t, RequestedBy: requestedBy,
			}
		}
		// Another process may queue the same files at the same time.
		res = d.db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(jobs, 100)
		if res.Error != nil {
			return added, fmt.Errorf("queue downloads: %w", res.Error)
		}
		added += int(res.RowsAffected)
	}
	if added > 0 {
		d.progress.IncrementStatusVersion()
	}
	return added, nil
}

// RemoveJobs deletes the jobs of files that are not being downloaded right
// now and reports how many were removed.
func (d *Downloader) RemoveJobs(fileIDs []string) (int64, error) {
	var removed int64
	for _, chunk := range chunks(fileIDs) {
		res := d.db.Where("file_id IN ? AND state <> ?", chunk, database.JobRunning).Delete(&database.DownloadJob{})
		if res.Error != nil {
			return removed, res.Error
		}
		removed += res.RowsAffected
	}
	if removed > 0 {
		d.progress.IncrementStatusVersion()
	}
	return removed, nil
}

// RequestCancel asks the process running a file's download to stop it; the
// worker sees the request within a lease renewal. A running job whose lease
// has expired (its process died) is removed directly. It reports whether
// the file had a running job.
func (d *Downloader) RequestCancel(fileID string) (bool, error) {
	var job database.DownloadJob
	if d.db.Where("file_id = ? AND state = ?", fileID, database.JobRunning).Limit(1).Find(&job).RowsAffected == 0 {
		return false, nil
	}
	if job.LeaseUntil == nil || job.LeaseUntil.Before(now()) {
		return true, d.db.Where("file_id = ? AND version = ?", fileID, job.Version).Delete(&database.DownloadJob{}).Error
	}
	return true, d.db.Model(&database.DownloadJob{}).Where("file_id = ?", fileID).Update("cancel_requested", true).Error
}

// RunWorkers runs MaxConcurrent workers until ctx is cancelled. With only
// set, workers take only those files' jobs and return once none of them is
// waiting or running any more.
func (d *Downloader) RunWorkers(ctx context.Context, owner string, only []string) {
	done := make(chan struct{}, d.cfg.MaxConcurrent)
	for range d.cfg.MaxConcurrent {
		go func() {
			d.work(ctx, owner, only)
			done <- struct{}{}
		}()
	}
	for range d.cfg.MaxConcurrent {
		<-done
	}
}

func (d *Downloader) work(ctx context.Context, owner string, only []string) {
	for ctx.Err() == nil {
		job, err := d.claim(owner, only)
		if err != nil {
			slog.Error("Failed to claim a download job", "error", err)
		}
		if job != nil {
			d.runJob(ctx, job, owner)
			continue
		}
		if only != nil {
			if n, err := d.ActiveCount(only); err == nil && n == 0 {
				return
			}
		}
		sleepCtx(ctx, pollInterval)
	}
}

// claim takes the next due job: queued, retrying and due, or running with
// an expired lease (its process died). The version check makes the claim
// safe across processes.
func (d *Downloader) claim(owner string, only []string) (*database.DownloadJob, error) {
	groups := [][]string{nil}
	if only != nil {
		groups = chunks(only)
	}
	for _, group := range groups {
		t := now()
		q := d.db.Where("state = ? OR (state = ? AND next_attempt_at <= ?) OR (state = ? AND lease_until < ?)",
			database.JobQueued, database.JobRetrying, t, database.JobRunning, t)
		if group != nil {
			q = q.Where("file_id IN ?", group)
		}
		var candidates []database.DownloadJob
		if err := q.Order("next_attempt_at").Order("created_at").Limit(20).Find(&candidates).Error; err != nil {
			return nil, err
		}
		lease := t.Add(leaseDuration)
		for _, c := range candidates {
			if c.CancelRequested {
				// Cancelled while its process was dying.
				d.db.Where("file_id = ? AND version = ?", c.FileID, c.Version).Delete(&database.DownloadJob{})
				continue
			}
			res := d.db.Model(&database.DownloadJob{}).Where("file_id = ? AND version = ?", c.FileID, c.Version).
				Updates(map[string]any{
					"state": database.JobRunning, "claimed_by": owner, "lease_until": lease,
					"attempts": c.Attempts + 1, "version": c.Version + 1,
				})
			if res.Error != nil {
				return nil, res.Error
			}
			if res.RowsAffected == 1 {
				c.State, c.ClaimedBy, c.LeaseUntil = database.JobRunning, owner, &lease
				c.Attempts, c.Version = c.Attempts+1, c.Version+1
				return &c, nil
			}
		}
	}
	return nil, nil
}

// ActiveCount counts the files among ids whose jobs are still waiting or
// running.
func (d *Downloader) ActiveCount(ids []string) (int64, error) {
	var total int64
	for _, chunk := range chunks(ids) {
		var n int64
		if err := d.db.Model(&database.DownloadJob{}).
			Where("file_id IN ? AND state <> ?", chunk, database.JobFailed).Count(&n).Error; err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// Outcomes of a download attempt, as reported to OnAttempt.
const (
	OutcomeDone      = "done"
	OutcomeRetrying  = "retrying"
	OutcomeFailed    = "failed"
	OutcomeCancelled = "cancelled"
	OutcomeRequeued  = "requeued"
)

// AttemptResult describes one download attempt made by a queue worker.
type AttemptResult struct {
	FileID        string
	Outcome       string
	Attempt       int
	MaxAttempts   int
	Err           error
	Duration      time.Duration
	NextAttemptAt time.Time
}

func (d *Downloader) runJob(ctx context.Context, job *database.DownloadJob, owner string) {
	jobCtx, cancelJob := context.WithCancelCause(ctx)
	stopLease := d.renewLease(jobCtx, job.FileID, owner, cancelJob)
	start := time.Now()
	err := d.Download(jobCtx, job.FileID)
	stopLease()
	cause := context.Cause(jobCtx)
	cancelJob(nil)
	result := AttemptResult{FileID: job.FileID, Attempt: job.Attempts, MaxAttempts: job.MaxAttempts, Err: err, Duration: time.Since(start)}

	mine := func() *gorm.DB {
		return d.db.Model(&database.DownloadJob{}).Where("file_id = ? AND claimed_by = ?", job.FileID, owner)
	}
	removeMine := func() {
		d.db.Where("file_id = ? AND claimed_by = ?", job.FileID, owner).Delete(&database.DownloadJob{})
	}
	switch {
	case err == nil:
		removeMine()
		result.Outcome = OutcomeDone
	case ctx.Err() != nil:
		// The worker is shutting down: give the job back, the attempt does
		// not count.
		mine().Updates(map[string]any{"state": database.JobQueued, "attempts": job.Attempts - 1, "lease_until": nil, "claimed_by": ""})
		result.Outcome = OutcomeRequeued
	case errors.Is(cause, errLeaseLost):
		// Another worker owns the job now; leave it to that worker.
		result.Outcome = OutcomeRequeued
	case errors.Is(cause, errCancelRequested), errors.Is(err, context.Canceled):
		// Cancelled by the user, here or in another process.
		removeMine()
		result.Outcome = OutcomeCancelled
	case errors.Is(err, ErrDownloadInProgress):
		// Downloaded outside the queue right now; look again shortly.
		mine().Updates(map[string]any{"state": database.JobRetrying, "attempts": job.Attempts - 1,
			"next_attempt_at": now().Add(pollInterval), "lease_until": nil})
		result.Outcome = OutcomeRequeued
	case permanent(err) || job.Attempts >= job.MaxAttempts:
		mine().Updates(map[string]any{"state": database.JobFailed, "last_error": err.Error(), "lease_until": nil})
		slog.Warn("Download given up", "fileID", job.FileID, "attempts", job.Attempts, "error", err)
		result.Outcome = OutcomeFailed
	default:
		next := now().Add(backoff(job.Attempts))
		mine().Updates(map[string]any{"state": database.JobRetrying, "last_error": err.Error(),
			"next_attempt_at": next, "lease_until": nil})
		slog.Info("Download will be retried", "fileID", job.FileID, "attempt", job.Attempts,
			"maxAttempts", job.MaxAttempts, "nextAttemptAt", next, "error", err)
		result.Outcome, result.NextAttemptAt = OutcomeRetrying, next
	}
	d.progress.IncrementStatusVersion()
	if d.OnAttempt != nil {
		d.OnAttempt(result)
	}
}

// renewLease extends the job's lease while the download runs. It stops the
// download when the job was taken over by another worker, when a cancel was
// requested, or when renewals kept failing until the lease was nearly gone:
// from then on another worker may claim the job, and two must never write
// the same file.
func (d *Downloader) renewLease(ctx context.Context, fileID, owner string, stop context.CancelCauseFunc) func() {
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(leaseDuration / 4)
		defer t.Stop()
		renewed := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
			}
			res := d.db.Model(&database.DownloadJob{}).
				Where("file_id = ? AND claimed_by = ? AND cancel_requested = ?", fileID, owner, false).
				Update("lease_until", now().Add(leaseDuration))
			switch {
			case res.Error != nil:
				if time.Since(renewed) > leaseDuration*3/4 {
					stop(errLeaseLost)
					return
				}
			case res.RowsAffected == 0:
				var job database.DownloadJob
				if d.db.Where("file_id = ? AND claimed_by = ?", fileID, owner).Limit(1).Find(&job).RowsAffected == 1 && job.CancelRequested {
					stop(errCancelRequested)
				} else {
					stop(errLeaseLost)
				}
				return
			default:
				renewed = time.Now()
			}
		}
	}()
	// Waiting keeps a late renewal from writing after the job's outcome.
	return func() { close(done); <-exited }
}

// permanent reports whether retrying cannot help: rejected credentials,
// missing files, invalid configuration.
func permanent(err error) bool {
	if errors.Is(err, ErrFileNotFound) || errors.Is(err, ErrSourceNotFound) {
		return true
	}
	var ae *sources.AdapterError
	if errors.As(err, &ae) {
		switch ae.Code {
		case sources.ErrCodeAuth, sources.ErrCodeNotFound, sources.ErrCodeInvalidConfig:
			return true
		}
	}
	return false
}

// backoff is the wait before attempt n+1: 1, 2, 4 ... minutes, capped at
// an hour, with 20 % jitter so failed downloads do not retry in lockstep.
func backoff(attempt int) time.Duration {
	delay := retryBaseDelay
	for i := 1; i < attempt && delay < retryMaxDelay; i++ {
		delay *= 2
	}
	delay = min(delay, retryMaxDelay)
	jitter := 0.8 + 0.4*rand.Float64()
	return time.Duration(float64(delay) * jitter)
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// QueueCounts is the number of jobs per state.
type QueueCounts struct {
	Queued, Running, Retrying, Failed int
}

// QueueCounts counts the jobs per state.
func (d *Downloader) QueueCounts() (QueueCounts, error) {
	var rows []struct {
		State string
		N     int
	}
	if err := d.db.Model(&database.DownloadJob{}).Select("state, COUNT(*) AS n").Group("state").Scan(&rows).Error; err != nil {
		return QueueCounts{}, err
	}
	var c QueueCounts
	for _, r := range rows {
		switch r.State {
		case database.JobQueued:
			c.Queued = r.N
		case database.JobRunning:
			c.Running = r.N
		case database.JobRetrying:
			c.Retrying = r.N
		case database.JobFailed:
			c.Failed = r.N
		}
	}
	return c, nil
}
