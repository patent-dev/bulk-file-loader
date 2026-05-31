package refdata

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"testing"
	"time"
)

// Live tests hit the real public endpoints. They are skipped unless
// REFDATA_LIVE=1 so normal `go test ./...` runs stay offline and deterministic.
//
//	REFDATA_LIVE=1 go test ./internal/sources/refdata/ -run Live -v
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("REFDATA_LIVE") == "" {
		t.Skip("set REFDATA_LIVE=1 to run live network tests")
	}
}

func TestLiveClassificationDiscovery(t *testing.T) {
	requireLive(t)
	a := NewClassifications()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cases := []struct {
		product string
		pattern string
	}{
		{"cpc", `^\d{6}$`}, // YYYYMM
		{"ipc", `^\d{8}$`}, // YYYYMMDD
	}
	for _, c := range cases {
		dels, err := a.FetchDeliveries(ctx, c.product)
		if err != nil {
			t.Errorf("%s: FetchDeliveries: %v", c.product, err)
			continue
		}
		ed := dels[0].ExternalID
		if !regexp.MustCompile(c.pattern).MatchString(ed) {
			t.Errorf("%s: edition %q does not match %s", c.product, ed, c.pattern)
		}
		t.Logf("%s current edition: %s", c.product, ed)
	}
}

func TestLiveIPCConcordanceDownload(t *testing.T) {
	requireLive(t)
	a := NewClassifications()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dels, err := a.FetchDeliveries(ctx, "ipc")
	if err != nil {
		t.Fatal(err)
	}
	files, err := a.FetchFiles(ctx, "ipc", dels[0].ExternalID)
	if err != nil {
		t.Fatal(err)
	}
	concordanceID := "ipc:concordance:" + dels[0].ExternalID
	idx := -1
	for i := range files {
		if files[i].ExternalID == concordanceID {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("concordance file %q not found in IPC file list", concordanceID)
	}

	var buf bytes.Buffer
	if err := a.DownloadFile(ctx, files[idx], &buf, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	// The smallest IPC artifact; still a real ZIP.
	if buf.Len() == 0 || !bytes.HasPrefix(buf.Bytes(), []byte("PK")) {
		t.Errorf("downloaded %d bytes, not a ZIP (PK header missing)", buf.Len())
	}
	t.Logf("downloaded IPC file %d bytes", buf.Len())
}

func TestLiveManualDiscovery(t *testing.T) {
	requireLive(t)
	a := NewManuals()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	for _, product := range []string{"mpep", "tmep", "euipo-tm", "wipo-pct", "epo-guidelines"} {
		dels, err := a.FetchDeliveries(ctx, product)
		if err != nil {
			t.Errorf("%s: FetchDeliveries: %v", product, err)
			continue
		}
		if dels[0].ExternalID == "" {
			t.Errorf("%s: empty edition", product)
		}
		t.Logf("%s current edition: %s", product, dels[0].ExternalID)
	}
}
