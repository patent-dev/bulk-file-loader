package scheduler

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	gormDB.AutoMigrate(
		&database.Source{},
		&database.Product{},
		&database.Delivery{},
		&database.File{},
		&database.DownloadEntry{},
		&database.Webhook{},
	)
	return &database.DB{DB: gormDB}
}

func TestScheduleProduct(t *testing.T) {
	db := setupTestDB(t)
	hooksManager := hooks.New(db)

	scheduler := &Scheduler{
		db:       db,
		hooks:    hooksManager,
		entryIDs: make(map[string]cron.EntryID),
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
	hooksManager := hooks.New(db)

	scheduler := &Scheduler{
		db:       db,
		hooks:    hooksManager,
		entryIDs: make(map[string]cron.EntryID),
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
	hooksManager := hooks.New(db)

	scheduler := &Scheduler{
		db:       db,
		hooks:    hooksManager,
		entryIDs: make(map[string]cron.EntryID),
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
	scheduler.ScheduleProduct(product)

	scheduler.UnscheduleProduct(product.ID)

	if _, ok := scheduler.entryIDs[product.ID]; ok {
		t.Error("Product should be unscheduled")
	}
}

func TestGetNextRun(t *testing.T) {
	db := setupTestDB(t)
	hooksManager := hooks.New(db)

	scheduler := &Scheduler{
		db:       db,
		hooks:    hooksManager,
		entryIDs: make(map[string]cron.EntryID),
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
	scheduler.ScheduleProduct(product)

	nextRun := scheduler.GetNextRun(product.ID)
	if nextRun == nil {
		t.Fatal("GetNextRun should return a time")
	}
	if nextRun.Before(time.Now()) {
		t.Error("Next run should be in the future")
	}
}

func TestGetNextRunNotScheduled(t *testing.T) {
	db := setupTestDB(t)
	hooksManager := hooks.New(db)

	scheduler := &Scheduler{
		db:       db,
		hooks:    hooksManager,
		entryIDs: make(map[string]cron.EntryID),
	}
	scheduler.cron = cron.New()

	nextRun := scheduler.GetNextRun("nonexistent-product")
	if nextRun != nil {
		t.Error("GetNextRun for unscheduled product should return nil")
	}
}

func TestScheduleInvalidCron(t *testing.T) {
	db := setupTestDB(t)
	hooksManager := hooks.New(db)

	scheduler := &Scheduler{
		db:       db,
		hooks:    hooksManager,
		entryIDs: make(map[string]cron.EntryID),
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
	hooksManager := hooks.New(db)

	scheduler := &Scheduler{
		db:       db,
		hooks:    hooksManager,
		entryIDs: make(map[string]cron.EntryID),
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
	scheduler.ScheduleProduct(product)

	oldEntryID := scheduler.entryIDs[product.ID]

	// Reschedule with new cron
	product.CheckWindowStart = "0 12 * * *"
	scheduler.ScheduleProduct(product)

	newEntryID := scheduler.entryIDs[product.ID]

	if oldEntryID == newEntryID {
		t.Error("Rescheduling should create new entry ID")
	}
}

func TestBuildDeliveryID(t *testing.T) {
	id := buildDeliveryID("product-1", "delivery-external-123")
	expected := "product-1:delivery-external-123"
	if id != expected {
		t.Errorf("buildDeliveryID() = %q, want %q", id, expected)
	}
}

func TestBuildFileID(t *testing.T) {
	id := buildFileID("product-1", "delivery-123", "file-456")
	expected := "product-1:delivery-123:file-456"
	if id != expected {
		t.Errorf("buildFileID() = %q, want %q", id, expected)
	}
}
