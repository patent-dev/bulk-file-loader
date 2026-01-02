package database

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(gormDB); err != nil {
		t.Fatal(err)
	}
	return &DB{DB: gormDB}
}

func TestSettings(t *testing.T) {
	db := setupTestDB(t)

	// Test SetSetting and GetSetting
	if err := db.SetSetting("test_key", "test_value"); err != nil {
		t.Fatal(err)
	}

	value, err := db.GetSetting("test_key")
	if err != nil {
		t.Fatal(err)
	}
	if value != "test_value" {
		t.Errorf("GetSetting() = %q, want test_value", value)
	}

	// Test HasSetting
	if !db.HasSetting("test_key") {
		t.Error("HasSetting() = false, want true")
	}
	if db.HasSetting("nonexistent_key") {
		t.Error("HasSetting(nonexistent) = true, want false")
	}

	// Test GetSetting for nonexistent key
	_, err = db.GetSetting("nonexistent_key")
	if err == nil {
		t.Error("GetSetting(nonexistent) should return error")
	}
}

func TestSettingUpdate(t *testing.T) {
	db := setupTestDB(t)

	if err := db.SetSetting("key", "value1"); err != nil {
		t.Fatal(err)
	}

	if err := db.SetSetting("key", "value2"); err != nil {
		t.Fatal(err)
	}

	value, err := db.GetSetting("key")
	if err != nil {
		t.Fatal(err)
	}
	if value != "value2" {
		t.Errorf("GetSetting() = %q, want value2", value)
	}
}

func TestSourceCRUD(t *testing.T) {
	db := setupTestDB(t)

	source := &Source{
		ID:      "test-source",
		Name:    "Test Source",
		Enabled: true,
	}
	if err := db.Create(source).Error; err != nil {
		t.Fatal(err)
	}

	// Read back
	var retrieved Source
	if err := db.First(&retrieved, "id = ?", "test-source").Error; err != nil {
		t.Fatal(err)
	}
	if retrieved.Name != "Test Source" {
		t.Errorf("Name = %q, want Test Source", retrieved.Name)
	}
	if !retrieved.Enabled {
		t.Error("Enabled = false, want true")
	}

	// Update
	db.Model(&retrieved).Update("enabled", false)
	db.First(&retrieved, "id = ?", "test-source")
	if retrieved.Enabled {
		t.Error("Enabled = true after update, want false")
	}

	// Delete
	db.Delete(&Source{}, "id = ?", "test-source")
	var count int64
	db.Model(&Source{}).Where("id = ?", "test-source").Count(&count)
	if count != 0 {
		t.Errorf("Source count after delete = %d, want 0", count)
	}
}

func TestProductWithDeliveries(t *testing.T) {
	db := setupTestDB(t)

	source := &Source{ID: "s1", Name: "Source 1"}
	db.Create(source)

	product := &Product{
		ID:           "p1",
		SourceID:     "s1",
		Name:         "Product 1",
		AutoDownload: true,
	}
	db.Create(product)

	delivery := &Delivery{
		ID:        "d1",
		ProductID: "p1",
		Name:      "Delivery 1",
	}
	db.Create(delivery)

	// Test preload
	var loadedProduct Product
	if err := db.Preload("Deliveries").First(&loadedProduct, "id = ?", "p1").Error; err != nil {
		t.Fatal(err)
	}
	if len(loadedProduct.Deliveries) != 1 {
		t.Errorf("Deliveries count = %d, want 1", len(loadedProduct.Deliveries))
	}
	if loadedProduct.Deliveries[0].Name != "Delivery 1" {
		t.Errorf("Delivery name = %q, want Delivery 1", loadedProduct.Deliveries[0].Name)
	}
}

func TestFileWithDownloadEntries(t *testing.T) {
	db := setupTestDB(t)

	source := &Source{ID: "s1", Name: "Source 1"}
	db.Create(source)

	product := &Product{ID: "p1", SourceID: "s1", Name: "Product 1"}
	db.Create(product)

	delivery := &Delivery{ID: "d1", ProductID: "p1", Name: "Delivery 1"}
	db.Create(delivery)

	file := &File{
		ID:         "f1",
		DeliveryID: "d1",
		ProductID:  "p1",
		SourceID:   "s1",
		FileName:   "test.zip",
		FileSize:   1024,
	}
	db.Create(file)

	entry := &DownloadEntry{
		FileID: "f1",
		Status: DownloadStatusPending,
	}
	db.Create(entry)

	// Test preload
	var loadedFile File
	if err := db.Preload("DownloadEntries").First(&loadedFile, "id = ?", "f1").Error; err != nil {
		t.Fatal(err)
	}
	if len(loadedFile.DownloadEntries) != 1 {
		t.Errorf("DownloadEntries count = %d, want 1", len(loadedFile.DownloadEntries))
	}
	if loadedFile.DownloadEntries[0].Status != DownloadStatusPending {
		t.Errorf("Entry status = %q, want pending", loadedFile.DownloadEntries[0].Status)
	}
}

func TestWebhookCRUD(t *testing.T) {
	db := setupTestDB(t)

	webhook := &Webhook{
		Name:    "Test Webhook",
		URL:     "https://example.com/hook",
		Events:  `["download.completed"]`,
		Enabled: true,
	}
	if err := db.Create(webhook).Error; err != nil {
		t.Fatal(err)
	}

	if webhook.ID == 0 {
		t.Error("Webhook ID should be auto-generated")
	}

	var retrieved Webhook
	db.First(&retrieved, webhook.ID)
	if retrieved.Name != "Test Webhook" {
		t.Errorf("Name = %q, want Test Webhook", retrieved.Name)
	}
	if retrieved.URL != "https://example.com/hook" {
		t.Errorf("URL = %q, want https://example.com/hook", retrieved.URL)
	}
}
