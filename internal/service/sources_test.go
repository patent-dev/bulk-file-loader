package service

import (
	"context"
	"io"
	"testing"

	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

// stubAdapter is a minimal adapter for testing source listing and updates.
type stubAdapter struct {
	id   string
	name string
}

func (a *stubAdapter) ID() string                                  { return a.id }
func (a *stubAdapter) Name() string                                { return a.name }
func (a *stubAdapter) CredentialFields() []sources.CredentialField { return nil }
func (a *stubAdapter) SetCredentials(map[string]string)            {}
func (a *stubAdapter) ValidateCredentials(context.Context) error   { return nil }
func (a *stubAdapter) FetchProducts(context.Context) ([]sources.ProductInfo, error) {
	return nil, nil
}
func (a *stubAdapter) FetchDeliveries(context.Context, string) ([]sources.DeliveryInfo, error) {
	return nil, nil
}
func (a *stubAdapter) FetchFiles(context.Context, string, string) ([]sources.FileInfo, error) {
	return nil, nil
}
func (a *stubAdapter) DownloadFile(context.Context, sources.FileInfo, io.Writer, sources.ProgressFunc) error {
	return nil
}

func TestListSources(t *testing.T) {
	svc := setupTestService(t)

	svc.Registry.Register(&stubAdapter{id: "test-src", name: "Test Source"})
	svc.Registry.Register(&stubAdapter{id: "other-src", name: "Other Source"})

	result, err := svc.ListSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(result))
	}

	// Verify sorted by name
	if result[0].Name != "Other Source" || result[1].Name != "Test Source" {
		t.Errorf("sources not sorted by name: got %q, %q", result[0].Name, result[1].Name)
	}
}

func TestUpdateSourcePreservesEnabled(t *testing.T) {
	svc := setupTestService(t)

	svc.Registry.Register(&stubAdapter{id: "s1", name: "Source One"})

	// Pre-populate the DB row as enabled
	svc.DB.Create(&database.Source{ID: "s1", Name: "Source One", Enabled: true})

	// Call UpdateSource with enabled=nil (should preserve the current state)
	_, err := svc.UpdateSource(context.Background(), "s1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the source is still enabled
	var src database.Source
	svc.DB.First(&src, "id = ?", "s1")
	if !src.Enabled {
		t.Error("expected source to remain enabled when enabled=nil, but it was disabled")
	}
}
