package service

import (
	"context"
	"testing"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/database"
)

func syncedCPC(t *testing.T) (*Service, string, []string) {
	t.Helper()
	svc := setupTestService(t)
	productID := seedCatalogueProduct(t, svc, cpcCatalogue())
	res, err := svc.SyncProduct(context.Background(), productID)
	if err != nil {
		t.Fatal(err)
	}
	return svc, productID, res.NewFileIDs
}

func TestFileStatusFollowsDownloadJob(t *testing.T) {
	svc, _, ids := syncedCPC(t)
	now := time.Now()
	next := now.Add(4 * time.Minute)
	for _, j := range []database.DownloadJob{
		{FileID: ids[0], State: database.JobQueued, MaxAttempts: 5, NextAttemptAt: now},
		{FileID: ids[1], State: database.JobRetrying, Attempts: 2, MaxAttempts: 5, NextAttemptAt: next, LastError: "connection reset"},
		{FileID: ids[2], State: database.JobFailed, Attempts: 5, MaxAttempts: 5, NextAttemptAt: now, LastError: "not found (404)"},
	} {
		if err := svc.DB.Create(&j).Error; err != nil {
			t.Fatal(err)
		}
	}

	want := map[string]string{ids[0]: "queued", ids[1]: "retrying", ids[2]: "failed"}
	for id, status := range want {
		f, err := svc.GetFile(id)
		if err != nil {
			t.Fatal(err)
		}
		if string(f.Status) != status {
			t.Errorf("%s status = %s, want %s", id, f.Status, status)
		}
	}
	retrying, _ := svc.GetFile(ids[1])
	if deref(retrying.Attempts) != 2 || retrying.NextAttemptAt == nil || deref(retrying.ErrorMessage) != "connection reset" {
		t.Errorf("retrying file = attempts %v next %v error %v", retrying.Attempts, retrying.NextAttemptAt, retrying.ErrorMessage)
	}
	if res, err := svc.ListFilesFiltered(FileFilter{Status: "queued"}, 0, 0); err != nil || res.Total != 1 {
		t.Errorf("queued filter = %d, %v; want 1", res.Total, err)
	}
}

func TestQueueRequestsAndRemovals(t *testing.T) {
	svc, _, ids := syncedCPC(t)
	jobState := func(id string) string {
		var j database.DownloadJob
		if svc.DB.Where("file_id = ?", id).Limit(1).Find(&j).RowsAffected == 0 {
			return "none"
		}
		return j.State
	}

	for _, id := range ids {
		if err := svc.DownloadFile(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.CancelDownload(ids[0]); err != nil || jobState(ids[0]) != "none" {
		t.Errorf("cancel: err %v, job %s; want the job removed", err, jobState(ids[0]))
	}
	if err := svc.SkipFile(ids[1]); err != nil || jobState(ids[1]) != "none" {
		t.Errorf("skip: err %v, job %s; want the job removed", err, jobState(ids[1]))
	}
	if err := svc.CancelDownload(ids[0]); err == nil {
		t.Error("cancelling a file that is not queued must report not found")
	}

	// A given-up file starts over when requested again.
	svc.DB.Model(&database.DownloadJob{}).Where("file_id = ?", ids[2]).
		Updates(map[string]any{"state": database.JobFailed, "attempts": 5})
	if err := svc.DownloadFile(ids[2]); err != nil || jobState(ids[2]) != database.JobQueued {
		t.Errorf("re-request: err %v, job %s; want queued", err, jobState(ids[2]))
	}
}

func TestScheduledSyncQueuesNewFiles(t *testing.T) {
	svc := setupTestService(t)
	productID := seedCatalogueProduct(t, svc, cpcCatalogue())
	svc.DB.Model(&database.Product{}).Where("id = ?", productID).Update("auto_download", true)

	svc.RunScheduledSync(productID)

	var jobs []database.DownloadJob
	svc.DB.Find(&jobs)
	if len(jobs) != 3 || jobs[0].RequestedBy != "schedule" {
		t.Errorf("jobs = %+v, want the 3 new files queued by the schedule", jobs)
	}
}

func TestServerHeartbeat(t *testing.T) {
	svc := setupTestService(t)
	if svc.Server() != nil {
		t.Fatal("no server has run yet")
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc.SetContext(ctx)
	svc.RunServer()
	deadline := time.Now().Add(2 * time.Second)
	for svc.Server() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	hb := svc.Server()
	if hb == nil || hb.Owner != Owner() {
		t.Fatalf("heartbeat = %+v, want this process", hb)
	}

	cancel()
	svc.Wait()
	if svc.Server() != nil {
		t.Error("the heartbeat must be removed on shutdown")
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
