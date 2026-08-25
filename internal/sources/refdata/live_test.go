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

// TestLiveNiceLocarno exercises the discoverIndexListing path against the real
// WIPO master-file directories: edition discovery, resolving the two zip URLs
// from the autoindex, and downloading the (smaller) top_structure zip.
func TestLiveNiceLocarno(t *testing.T) {
	requireLive(t)
	a := NewClassifications()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	for _, product := range []string{"nice", "locarno"} {
		dels, err := a.FetchDeliveries(ctx, product)
		if err != nil {
			t.Errorf("%s: FetchDeliveries: %v", product, err)
			continue
		}
		ed := dels[0].ExternalID
		if !regexp.MustCompile(`^\d{8}$`).MatchString(ed) {
			t.Errorf("%s: edition %q not YYYYMMDD", product, ed)
		}
		files, err := a.FetchFiles(ctx, product, ed)
		if err != nil {
			t.Errorf("%s: FetchFiles: %v", product, err)
			continue
		}
		if len(files) != 2 {
			t.Errorf("%s: got %d files, want 2 (top_structure + texts)", product, len(files))
		}
		var structFile *struct{ name, uri string }
		for _, f := range files {
			t.Logf("%s %s -> %s", product, f.FileName, f.DownloadURI)
			if regexp.MustCompile(`top_structure`).MatchString(f.FileName) {
				structFile = &struct{ name, uri string }{f.FileName, f.DownloadURI}
			}
		}
		if structFile == nil {
			t.Errorf("%s: no top_structure file resolved", product)
			continue
		}
		// Download the smaller top_structure zip end-to-end.
		for _, f := range files {
			if f.FileName != structFile.name {
				continue
			}
			var buf bytes.Buffer
			if err := a.DownloadFile(ctx, f, &buf, nil); err != nil {
				t.Errorf("%s: download %s: %v", product, f.FileName, err)
				break
			}
			if buf.Len() == 0 || !bytes.HasPrefix(buf.Bytes(), []byte("PK")) {
				t.Errorf("%s: downloaded %d bytes, not a ZIP", product, buf.Len())
			}
			t.Logf("%s downloaded %s: %d bytes", product, f.FileName, buf.Len())
		}
	}
}
