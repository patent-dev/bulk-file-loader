package scheduler

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/database/dbtest"
)

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.New(dbtest.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestScheduleProduct(t *testing.T) {
	db := setupTestDB(t)

	scheduler := &Scheduler{
		db:       db,
		entryIDs: make(map[string]cron.EntryID),
		specs:    make(map[string]string),
	}
	scheduler.cron = cron.New()
	scheduler.cron.Start()
	defer scheduler.Stop()

	product := &database.Product{
		ID:               "test-product",
		Name:             "Test Product",
		CheckWindowStart: "0 */6 * * *", // Every 6 hours
	}
	db.Create(product)

	err := scheduler.ScheduleProduct(product)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := scheduler.entryIDs[product.ID]; !ok {
		t.Error("Product should be scheduled")
	}
}

func TestScheduleProductEmptySchedule(t *testing.T) {
	db := setupTestDB(t)

	scheduler := &Scheduler{
		db:       db,
		entryIDs: make(map[string]cron.EntryID),
		specs:    make(map[string]string),
	}
	scheduler.cron = cron.New()
	scheduler.cron.Start()
	defer scheduler.Stop()

	product := &database.Product{
		ID:               "test-product",
		Name:             "Test Product",
		CheckWindowStart: "",
	}

	err := scheduler.ScheduleProduct(product)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := scheduler.entryIDs[product.ID]; ok {
		t.Error("Product with empty schedule should not be scheduled")
	}
}

func TestUnscheduleProduct(t *testing.T) {
	db := setupTestDB(t)

	scheduler := &Scheduler{
		db:       db,
		entryIDs: make(map[string]cron.EntryID),
		specs:    make(map[string]string),
	}
	scheduler.cron = cron.New()
	scheduler.cron.Start()
	defer scheduler.Stop()

	product := &database.Product{
		ID:               "test-product",
		Name:             "Test Product",
		CheckWindowStart: "0 6 * * *",
	}
	db.Create(product)
	_ = scheduler.ScheduleProduct(product)

	scheduler.UnscheduleProduct(product.ID)

	if _, ok := scheduler.entryIDs[product.ID]; ok {
		t.Error("Product should be unscheduled")
	}
}

func TestGetNextRun(t *testing.T) {
	db := setupTestDB(t)

	scheduler := &Scheduler{
		db:       db,
		entryIDs: make(map[string]cron.EntryID),
		specs:    make(map[string]string),
	}
	scheduler.cron = cron.New()
	scheduler.cron.Start()
	defer scheduler.Stop()

	product := &database.Product{
		ID:               "test-product",
		Name:             "Test Product",
		CheckWindowStart: "0 6 * * *",
	}
	db.Create(product)
	_ = scheduler.ScheduleProduct(product)

	nextRun := scheduler.GetNextRun(product.ID)
	if nextRun == nil {
		t.Fatal("GetNextRun should return a time")
	} else if nextRun.Before(time.Now()) {
		t.Error("Next run should be in the future")
	}
}

func TestGetNextRunNotScheduled(t *testing.T) {
	db := setupTestDB(t)

	scheduler := &Scheduler{
		db:       db,
		entryIDs: make(map[string]cron.EntryID),
		specs:    make(map[string]string),
	}
	scheduler.cron = cron.New()

	nextRun := scheduler.GetNextRun("nonexistent-product")
	if nextRun != nil {
		t.Error("GetNextRun for unscheduled product should return nil")
	}
}

func TestScheduleInvalidCron(t *testing.T) {
	db := setupTestDB(t)

	scheduler := &Scheduler{
		db:       db,
		entryIDs: make(map[string]cron.EntryID),
		specs:    make(map[string]string),
	}
	scheduler.cron = cron.New()
	scheduler.cron.Start()
	defer scheduler.Stop()

	product := &database.Product{
		ID:               "test-product",
		Name:             "Test Product",
		CheckWindowStart: "invalid cron",
	}

	err := scheduler.ScheduleProduct(product)
	if err == nil {
		t.Error("Scheduling with invalid cron should return error")
	}
}

func TestRescheduleProduct(t *testing.T) {
	db := setupTestDB(t)

	scheduler := &Scheduler{
		db:       db,
		entryIDs: make(map[string]cron.EntryID),
		specs:    make(map[string]string),
	}
	scheduler.cron = cron.New()
	scheduler.cron.Start()
	defer scheduler.Stop()

	product := &database.Product{
		ID:               "test-product",
		Name:             "Test Product",
		CheckWindowStart: "0 6 * * *",
	}
	db.Create(product)
	_ = scheduler.ScheduleProduct(product)

	oldEntryID := scheduler.entryIDs[product.ID]

	// Reschedule with new cron
	product.CheckWindowStart = "0 12 * * *"
	_ = scheduler.ScheduleProduct(product)

	newEntryID := scheduler.entryIDs[product.ID]

	if oldEntryID == newEntryID {
		t.Error("Rescheduling should create new entry ID")
	}
}

func TestNextRun(t *testing.T) {
	from := time.Date(2026, 11, 10, 6, 30, 0, 0, time.UTC)
	next, err := NextRun("0 7 * * 1", from) // Mondays 07:00; 2026-11-10 is a Tuesday
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 16, 7, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Errorf("NextRun = %v, want %v", next, want)
	}
	if _, err := NextRun("every monday", from); err == nil {
		t.Error("an invalid expression must be rejected")
	}
}

// TestReloadPicksUpChangesFromAnotherProcess changes schedules in the
// database, as the CLI does, and checks Reload applies them.
func TestReloadPicksUpChangesFromAnotherProcess(t *testing.T) {
	db := setupTestDB(t)
	s := New(db, func(string) {})
	defer s.Stop()

	db.Create(&database.Source{ID: "src", Name: "Source"})
	if err := db.Create(&database.Product{ID: "p", SourceID: "src", Name: "P", AutoDownload: true, CheckWindowStart: "0 7 * * 1"}).Error; err != nil {
		t.Fatal(err)
	}
	s.Reload()
	first := s.GetNextRun("p")
	if first == nil || first.Weekday() != time.Monday {
		t.Fatalf("next run = %v, want a Monday after the schedule was added", first)
	}

	db.Model(&database.Product{}).Where("id = ?", "p").Update("check_window_start", "0 7 * * 3")
	s.Reload()
	if next := s.GetNextRun("p"); next == nil || next.Weekday() != time.Wednesday {
		t.Errorf("next run = %v, want a Wednesday after the schedule changed", next)
	}

	db.Model(&database.Product{}).Where("id = ?", "p").Update("auto_download", false)
	s.Reload()
	if next := s.GetNextRun("p"); next != nil {
		t.Errorf("next run = %v, want none after auto-download was switched off", next)
	}
}
