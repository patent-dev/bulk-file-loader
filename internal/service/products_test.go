package service

import (
	"errors"
	"testing"

	"github.com/patent-dev/bulk-file-loader/internal/database"
)

func TestListProducts(t *testing.T) {
	svc := setupTestService(t)

	svc.DB.Create(&database.Source{ID: "src1", Name: "Source 1"})
	svc.DB.Create(&database.Source{ID: "src2", Name: "Source 2"})
	svc.DB.Create(&database.Product{ID: "p1", SourceID: "src1", Name: "Product A"})
	svc.DB.Create(&database.Product{ID: "p2", SourceID: "src1", Name: "Product B"})
	svc.DB.Create(&database.Product{ID: "p3", SourceID: "src2", Name: "Product C"})

	// List all products
	products, err := svc.ListProducts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 3 {
		t.Errorf("expected 3 products, got %d", len(products))
	}

	// Filter by source
	srcFilter := "src1"
	products, err = svc.ListProducts(&srcFilter)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 2 {
		t.Errorf("expected 2 products for src1, got %d", len(products))
	}
	for _, p := range products {
		if p.SourceId != "src1" {
			t.Errorf("expected source src1, got %s", p.SourceId)
		}
	}

	// Filter by source with no products
	srcEmpty := "nonexistent"
	products, err = svc.ListProducts(&srcEmpty)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 0 {
		t.Errorf("expected 0 products for nonexistent source, got %d", len(products))
	}
}

func TestGetProductNotFound(t *testing.T) {
	svc := setupTestService(t)
	_, err := svc.GetProduct("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent product")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
