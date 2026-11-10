package cli

import (
	"testing"
	"time"
)

func TestParseDateFlag(t *testing.T) {
	got, err := parseDateFlag("--released-before", "2026-01-01")
	if err != nil || !got.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("date = %v, %v", got, err)
	}
	got, err = parseDateFlag("--released-before", "2026-01-01T12:00:00+01:00")
	if err != nil || got.UTC().Hour() != 11 {
		t.Errorf("RFC 3339 = %v, %v", got, err)
	}
	if got, err := parseDateFlag("--released-before", ""); got != nil || err != nil {
		t.Errorf("empty = %v, %v; want no filter", got, err)
	}
	if _, err := parseDateFlag("--released-before", "01.01.2026"); err == nil {
		t.Error("an unsupported format must be rejected")
	}
}

func TestSelectFilesRejectsIDsWithFilters(t *testing.T) {
	t.Cleanup(func() { fileProductFilter = "" })
	fileProductFilter = "epo-bdds:3"
	if _, err := selectFiles(nil, []string{"some-file"}); err == nil {
		t.Error("IDs and filters together must be rejected")
	}
	fileProductFilter = ""
	if _, err := selectFiles(nil, nil); err == nil {
		t.Error("neither IDs nor filters must be rejected")
	}
}
