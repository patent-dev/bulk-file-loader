package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

// catalogueAdapter serves a fixed catalogue. The editions and file names are
// those of the CPC bulk listing recorded in
// internal/sources/refdata/testdata/cpc (edition 202608).
type catalogueAdapter struct {
	stubAdapter
	deliveries   []sources.DeliveryInfo
	files        map[string][]sources.FileInfo
	failDelivery string
	onFetchFiles func()
}

func (a *catalogueAdapter) FetchDeliveries(context.Context, string) ([]sources.DeliveryInfo, error) {
	return a.deliveries, nil
}

func (a *catalogueAdapter) FetchFiles(_ context.Context, _, deliveryID string) ([]sources.FileInfo, error) {
	if a.onFetchFiles != nil {
		a.onFetchFiles()
	}
	if deliveryID == a.failDelivery {
		return nil, errors.New("listing unavailable")
	}
	return a.files[deliveryID], nil
}

func (a *catalogueAdapter) DownloadFile(context.Context, sources.FileInfo, io.Writer, sources.ProgressFunc) error {
	return nil
}

func cpcCatalogue() *catalogueAdapter {
	released := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	file := func(role, name string) sources.FileInfo {
		return sources.FileInfo{ExternalID: "cpc:" + role + ":202608", FileName: name, ReleasedAt: released}
	}
	return &catalogueAdapter{
		stubAdapter: stubAdapter{id: "patent-classifications", name: "Patent Classifications"},
		deliveries: []sources.DeliveryInfo{
			{ExternalID: "202608", Name: "Cooperative Patent Classification (CPC) 202608", PublishedAt: released},
		},
		files: map[string][]sources.FileInfo{"202608": {
			file("scheme", "CPCSchemeXML202608.zip"),
			file("definitions", "FullCPCDefinitionXML202608.zip"),
			file("notice", "CPCNoticeOfChangeXMLArtefact202608.zip"),
		}},
	}
}

func seedCatalogueProduct(t *testing.T, svc *Service, a *catalogueAdapter) string {
	t.Helper()
	svc.Registry.Register(a)
	svc.DB.Create(&database.Source{ID: a.id, Name: a.name, Enabled: true})
	id := a.id + ":cpc"
	svc.DB.Create(&database.Product{ID: id, SourceID: a.id, ExternalID: "cpc", Name: "Cooperative Patent Classification (CPC)"})
	return id
}

func TestSyncProductCreatesThenFindsNothingNew(t *testing.T) {
	svc := setupTestService(t)
	a := cpcCatalogue()
	productID := seedCatalogueProduct(t, svc, a)

	res, err := svc.SyncProduct(context.Background(), productID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deliveries != 1 || res.Files != 3 || len(res.NewFileIDs) != 3 {
		t.Fatalf("first sync = %+v, want 1 delivery, 3 files, 3 new", res)
	}

	// An operator skips a file; a re-sync must keep that and find nothing new.
	skipped := res.NewFileIDs[0]
	svc.DB.Model(&database.File{}).Where("id = ?", skipped).Update("skipped", true)

	res, err = svc.SyncProduct(context.Background(), productID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 3 || len(res.NewFileIDs) != 0 {
		t.Fatalf("re-sync = %+v, want 3 files, none new", res)
	}
	var f database.File
	svc.DB.First(&f, "id = ?", skipped)
	if !f.Skipped {
		t.Error("re-sync cleared the skipped flag")
	}
	var p database.Product
	svc.DB.First(&p, "id = ?", productID)
	if p.LastCheckedAt == nil {
		t.Error("sync did not record its run time")
	}
}

func TestSyncProductReportsIncompleteDelivery(t *testing.T) {
	svc := setupTestService(t)
	a := cpcCatalogue()
	// A second delivery whose file listing fails (injected outage).
	a.deliveries = append(a.deliveries, sources.DeliveryInfo{ExternalID: "listing-outage", Name: "CPC listing outage"})
	a.failDelivery = "listing-outage"
	productID := seedCatalogueProduct(t, svc, a)

	res, err := svc.SyncProduct(context.Background(), productID)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 deliveries incomplete") {
		t.Fatalf("error = %v, want the incomplete delivery reported", err)
	}
	if res.Files != 3 {
		t.Errorf("files = %d, want the 3 files of the complete delivery saved", res.Files)
	}
}

func TestSyncProductKeepsScheduleChangedDuringSync(t *testing.T) {
	svc := setupTestService(t)
	a := cpcCatalogue()
	productID := seedCatalogueProduct(t, svc, a)
	a.onFetchFiles = func() {
		// The operator enables auto-download while the sync is listing files.
		svc.DB.Model(&database.Product{}).Where("id = ?", productID).
			Updates(map[string]any{"auto_download": true, "check_window_start": "0 7 * * 1"})
	}

	if _, err := svc.SyncProduct(context.Background(), productID); err != nil {
		t.Fatal(err)
	}
	var p database.Product
	svc.DB.First(&p, "id = ?", productID)
	if !p.AutoDownload || p.CheckWindowStart != "0 7 * * 1" {
		t.Errorf("schedule changed during the sync was lost: auto=%v schedule=%q", p.AutoDownload, p.CheckWindowStart)
	}
}

// countingAdapter records DownloadFile calls.
type countingAdapter struct {
	catalogueAdapter
	downloads atomic.Int32
}

func (a *countingAdapter) DownloadFile(context.Context, sources.FileInfo, io.Writer, sources.ProgressFunc) error {
	a.downloads.Add(1)
	return nil
}

func TestUpdateProductScheduleWithoutScheduler(t *testing.T) {
	svc := setupTestService(t) // CLI mode: no scheduler
	a := &countingAdapter{catalogueAdapter: *cpcCatalogue()}
	productID := seedCatalogueProduct(t, svc, &a.catalogueAdapter)
	svc.Registry.Register(a)
	if _, err := svc.SyncProduct(context.Background(), productID); err != nil {
		t.Fatal(err)
	}

	bad := "every monday"
	if _, err := svc.UpdateProductSchedule(productID, generated.UpdateScheduleRequest{CheckWindowStart: &bad}); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("error = %v, want ErrInvalidSchedule", err)
	}

	on, schedule := true, "0 7 * * 1"
	got, err := svc.UpdateProductSchedule(productID, generated.UpdateScheduleRequest{AutoDownload: &on, CheckWindowStart: &schedule})
	if err != nil {
		t.Fatal(err)
	}
	if got.NextRun == nil || got.NextRun.Weekday() != time.Monday {
		t.Errorf("NextRun = %v, want the next Monday", got.NextRun)
	}
	product, err := svc.GetProduct(productID)
	if err != nil {
		t.Fatal(err)
	}
	if product.NextRun == nil || !product.NextRun.Equal(*got.NextRun) {
		t.Errorf("product NextRun = %v, want %v", product.NextRun, got.NextRun)
	}
	if n, err := svc.PendingFileCount(productID); err != nil || n != 3 {
		t.Errorf("PendingFileCount = %d, %v; want 3", n, err)
	}

	// Enabling auto-download queues the pending files; nothing downloads in
	// this process (the CLI closes the database when the command returns).
	var jobs []database.DownloadJob
	svc.DB.Find(&jobs)
	if len(jobs) != 3 || jobs[0].RequestedBy != "auto" {
		t.Errorf("jobs = %+v, want the 3 pending files queued", jobs)
	}
	if n := a.downloads.Load(); n != 0 {
		t.Errorf("%d downloads started, want 0", n)
	}

	off := false
	if _, err := svc.UpdateProductSchedule(productID, generated.UpdateScheduleRequest{AutoDownload: &off}); err != nil {
		t.Fatal(err)
	}
	if product, _ := svc.GetProduct(productID); product.NextRun != nil {
		t.Errorf("NextRun = %v with auto-download off, want none", product.NextRun)
	}
}

func TestListFilesFiltered(t *testing.T) {
	svc := setupTestService(t)
	productID := seedCatalogueProduct(t, svc, cpcCatalogue())
	if _, err := svc.SyncProduct(context.Background(), productID); err != nil {
		t.Fatal(err)
	}
	// All three files are released 2026-08-01 00:00 UTC.
	released := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	justAfter := released.Add(time.Second)
	sameInstantCET := released.In(time.FixedZone("CEST", 2*60*60))

	count := func(f FileFilter) int {
		t.Helper()
		res, err := svc.ListFilesFiltered(f, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Files) != res.Total {
			t.Errorf("limit 0 returned %d of %d", len(res.Files), res.Total)
		}
		return res.Total
	}
	cases := []struct {
		name string
		f    FileFilter
		want int
	}{
		{"all", FileFilter{ProductID: productID}, 3},
		{"released before the release date", FileFilter{ReleasedBefore: &released}, 0},
		{"released on or after it", FileFilter{ReleasedAfter: &released}, 3},
		{"same instant with an offset", FileFilter{ReleasedAfter: &sameInstantCET}, 3},
		{"released after it", FileFilter{ReleasedAfter: &justAfter}, 0},
		{"glob", FileFilter{NameGlob: "CPCScheme*.zip"}, 1},
		{"glob is case-sensitive", FileFilter{NameGlob: "cpc*"}, 0},
		{"glob and date", FileFilter{NameGlob: "*CPC*", ReleasedBefore: &justAfter}, 3},
	}
	for _, c := range cases {
		if n := count(c.f); n != c.want {
			t.Errorf("%s: %d files, want %d", c.name, n, c.want)
		}
	}

	res, _ := svc.ListFilesFiltered(FileFilter{NameGlob: "CPCScheme*"}, 0, 0)
	if err := svc.SkipFile(res.Files[0].Id); err != nil {
		t.Fatal(err)
	}
	if n := count(FileFilter{Status: "skipped"}); n != 1 {
		t.Errorf("skipped = %d, want 1", n)
	}
	page, err := svc.ListFilesFiltered(FileFilter{Status: "available"}, 1, 1)
	if err != nil || page.Total != 2 || len(page.Files) != 1 {
		t.Errorf("available page = %d of %d, %v; want 1 of 2", len(page.Files), page.Total, err)
	}

	if _, err := svc.ListFilesFiltered(FileFilter{NameGlob: "[unclosed"}, 0, 0); err == nil {
		t.Error("a malformed pattern must be rejected")
	}
}

// TestLegacyScheduleDoesNotBlockDisabling covers schedules stored before
// validation existed (the CLI accepted "06:00").
func TestLegacyScheduleDoesNotBlockDisabling(t *testing.T) {
	svc := setupTestService(t)
	productID := seedCatalogueProduct(t, svc, cpcCatalogue())
	svc.DB.Model(&database.Product{}).Where("id = ?", productID).
		Updates(map[string]any{"auto_download": true, "check_window_start": "06:00"})

	off := false
	if _, err := svc.UpdateProductSchedule(productID, generated.UpdateScheduleRequest{AutoDownload: &off}); err != nil {
		t.Fatalf("disabling must work with a legacy schedule: %v", err)
	}
	on := true
	if _, err := svc.UpdateProductSchedule(productID, generated.UpdateScheduleRequest{AutoDownload: &on}); !errors.Is(err, ErrInvalidSchedule) {
		t.Errorf("enabling with the legacy schedule = %v, want ErrInvalidSchedule", err)
	}
	cleared := ""
	got, err := svc.UpdateProductSchedule(productID, generated.UpdateScheduleRequest{AutoDownload: &on, CheckWindowStart: &cleared})
	if err != nil {
		t.Fatalf("clearing the schedule must work: %v", err)
	}
	if got.CheckWindowStart != nil || got.NextRun != nil {
		t.Errorf("schedule = %v, next run %v; want both cleared", got.CheckWindowStart, got.NextRun)
	}
}

// TestFileStatusesBeyondOneQueryChunk verifies statuses stay correct when
// more files match than fit into one status query.
func TestFileStatusesBeyondOneQueryChunk(t *testing.T) {
	svc := setupTestService(t)
	productID := seedCatalogueProduct(t, svc, cpcCatalogue())
	if _, err := svc.SyncProduct(context.Background(), productID); err != nil {
		t.Fatal(err)
	}
	// Pad the catalogue past one chunk; the real files sort last by ID.
	var pad []database.File
	for i := range statusQueryChunk + 10 {
		pad = append(pad, database.File{
			ID: fmt.Sprintf("%s:pad:%05d", productID, i), DeliveryID: productID + ":202608",
			ProductID: productID, SourceID: "patent-classifications", FileName: fmt.Sprintf("pad-%05d", i),
		})
	}
	if err := svc.DB.CreateInBatches(pad, 200).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "CPCSchemeXML202608.zip")
	if err := os.WriteFile(path, []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}
	scheme := productID + ":202608:cpc:scheme:202608"
	svc.DB.Create(&database.DownloadEntry{FileID: scheme, Status: database.DownloadStatusCompleted, LocalPath: path})

	res, err := svc.ListFilesFiltered(FileFilter{Status: "downloaded"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || res.Files[0].Id != scheme {
		t.Errorf("downloaded = %d files, want only %s", res.Total, scheme)
	}
}
