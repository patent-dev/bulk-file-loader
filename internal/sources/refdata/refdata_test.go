package refdata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

// doerFunc adapts a function to the httpDoer interface for tests.
type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

// mkResp builds an *http.Response with the given status, body and headers.
func mkResp(status int, body string, headers map[string]string) *http.Response {
	h := make(http.Header)
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode:    status,
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

// fixedClock returns a clock function pinned to t.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestSubst(t *testing.T) {
	got := subst("a/{ed}/b-{ed}.zip", "202605")
	want := "a/202605/b-202605.zip"
	if got != want {
		t.Errorf("subst = %q, want %q", got, want)
	}
	if subst("no-token", "x") != "no-token" {
		t.Error("subst altered a template without a token")
	}
}

func TestParseExternalID(t *testing.T) {
	d, r, e, err := parseExternalID("cpc:scheme:202605")
	if err != nil || d != "cpc" || r != "scheme" || e != "202605" {
		t.Fatalf("parseExternalID = (%q,%q,%q,%v)", d, r, e, err)
	}
	if _, _, _, err := parseExternalID("bad-id"); err == nil {
		t.Error("expected error for malformed external ID")
	}
}

func TestFileByRole(t *testing.T) {
	d := &dataset{Files: []datasetFile{{Role: "scheme"}, {Role: "definitions"}}}
	if f := fileByRole(d, "definitions"); f == nil || f.Role != "definitions" {
		t.Error("fileByRole did not find definitions")
	}
	if f := fileByRole(d, ""); f == nil || f.Role != "scheme" {
		t.Error("fileByRole with empty role should return the first file")
	}
	if f := fileByRole(d, "missing"); f != nil {
		t.Error("fileByRole should return nil for an unknown role")
	}
}

func TestDateCandidatesMonthly(t *testing.T) {
	a := &Adapter{now: fixedClock(time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC))}
	got := a.dateCandidates(stepMonthly, 4)
	want := []string{"202605", "202604", "202603", "202602"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("monthly candidates = %v, want %v", got, want)
	}
}

func TestDateCandidatesMonthlyYearBoundary(t *testing.T) {
	a := &Adapter{now: fixedClock(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))}
	got := a.dateCandidates(stepMonthly, 3)
	want := []string{"202602", "202601", "202512"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("monthly candidates across year = %v, want %v", got, want)
	}
}

func TestDateCandidatesYearly(t *testing.T) {
	a := &Adapter{now: fixedClock(time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC))}
	got := a.dateCandidates(stepYearlyJan1, 3)
	want := []string{"20260101", "20250101", "20240101"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("yearly candidates = %v, want %v", got, want)
	}
}

func TestEditionDate(t *testing.T) {
	if d := editionDate("202605"); d.Year() != 2026 || d.Month() != time.May {
		t.Errorf("editionDate(202605) = %v", d)
	}
	if d := editionDate("20250101"); d.Year() != 2025 || d.Day() != 1 {
		t.Errorf("editionDate(20250101) = %v", d)
	}
	if d := editionDate("nonsense"); !d.IsZero() {
		t.Error("editionDate should be zero for bad input")
	}
}

func TestParseLastModified(t *testing.T) {
	got := parseLastModified("Wed, 21 Oct 2015 07:28:00 GMT")
	if got.Year() != 2015 || got.Month() != time.October || got.Day() != 21 {
		t.Errorf("parseLastModified = %v", got)
	}
	if !parseLastModified("").IsZero() {
		t.Error("empty Last-Modified should parse to zero time")
	}
	if !parseLastModified("garbage").IsZero() {
		t.Error("invalid Last-Modified should parse to zero time")
	}
}

func TestStatusError(t *testing.T) {
	cases := []struct {
		status int
		code   string // "" means nil error
	}{
		{200, ""},
		{206, ""},
		{404, sources.ErrCodeNotFound},
		{410, sources.ErrCodeNotFound},
		{401, sources.ErrCodeAuth},
		{403, sources.ErrCodeAuth},
		{429, sources.ErrCodeRateLimit},
		{500, sources.ErrCodeNetwork},
	}
	for _, c := range cases {
		err := statusError(c.status, "http://x")
		if c.code == "" {
			if err != nil {
				t.Errorf("status %d: expected nil, got %v", c.status, err)
			}
			continue
		}
		var ae *sources.AdapterError
		if !errors.As(err, &ae) || ae.Code != c.code {
			t.Errorf("status %d: got %v, want code %s", c.status, err, c.code)
		}
	}
}

func TestZipEntryName(t *testing.T) {
	cases := map[string]string{
		"https://h/patent/2.3-foo":   "patent/2.3-foo.html",
		"https://h/patent":           "patent.html",
		"https://h/patent/":          "patent/index.html",
		"https://h/patent/x.htm":     "patent/x.htm",
		"https://h/a/../../etc/pass": "a/_/_/etc/pass.html",
	}
	for in, want := range cases {
		if got := zipEntryName(in); got != want {
			t.Errorf("zipEntryName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchProducts(t *testing.T) {
	cl := NewClassifications()
	cps, _ := cl.FetchProducts(context.Background())
	if len(cps) != 3 {
		t.Fatalf("classifications: got %d products, want 3", len(cps))
	}
	want := map[string]bool{"cpc": false, "ipc": false, "jp-fterm": false}
	for _, p := range cps {
		if _, ok := want[p.ExternalID]; !ok {
			t.Errorf("unexpected classification product %q", p.ExternalID)
		}
		want[p.ExternalID] = true
		if p.Name == "" || p.CheckSchedule == "" {
			t.Errorf("product %q missing name/schedule", p.ExternalID)
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("missing classification product %q", id)
		}
	}

	man := NewManuals()
	mps, _ := man.FetchProducts(context.Background())
	if len(mps) != 9 {
		t.Fatalf("manuals: got %d products, want 9", len(mps))
	}
}

func TestFetchFilesCPC(t *testing.T) {
	a := NewClassifications()
	files, err := a.FetchFiles(context.Background(), "cpc", "202605")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3 (scheme, definitions, notice)", len(files))
	}
	scheme := files[0]
	if scheme.ExternalID != "cpc:scheme:202605" {
		t.Errorf("ExternalID = %q", scheme.ExternalID)
	}
	if scheme.FileName != "CPCSchemeXML202605.zip" {
		t.Errorf("FileName = %q", scheme.FileName)
	}
	if !strings.HasSuffix(scheme.DownloadURI, "/CPCSchemeXML202605.zip") {
		t.Errorf("DownloadURI = %q", scheme.DownloadURI)
	}
	if scheme.ReleasedAt.Year() != 2026 || scheme.ReleasedAt.Month() != time.May {
		t.Errorf("ReleasedAt = %v, want May 2026 (from edition)", scheme.ReleasedAt)
	}
	// definitions (descriptions) must be present, not only the scheme tree
	if files[1].ExternalID != "cpc:definitions:202605" {
		t.Errorf("definitions file missing, got %q", files[1].ExternalID)
	}
}

func TestFetchFilesIPCEditionSubstitution(t *testing.T) {
	a := NewClassifications()
	files, err := a.FetchFiles(context.Background(), "ipc", "20250101")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("got %d files, want 4", len(files))
	}
	for _, f := range files {
		if !strings.Contains(f.DownloadURI, "//20250101/MasterFiles/") {
			t.Errorf("IPC URL did not preserve the dated master-files path: %q", f.DownloadURI)
		}
	}
}

func TestFetchFilesErrors(t *testing.T) {
	a := NewClassifications()
	if _, err := a.FetchFiles(context.Background(), "nope", "202605"); err == nil {
		t.Error("expected error for unknown product")
	}
	if _, err := a.FetchFiles(context.Background(), "cpc", ""); err == nil {
		t.Error("expected error for empty edition")
	}
}

func TestFetchDeliveriesUnknownProduct(t *testing.T) {
	a := NewManuals()
	if _, err := a.FetchDeliveries(context.Background(), "ghost"); err == nil {
		t.Error("expected error for unknown product")
	}
}

func TestCredentialLessInterface(t *testing.T) {
	a := NewManuals()
	if fields := a.CredentialFields(); fields != nil {
		t.Errorf("expected no credential fields, got %v", fields)
	}
	a.SetCredentials(map[string]string{"x": "y"}) // must not panic
	if err := a.ValidateCredentials(context.Background()); err != nil {
		t.Errorf("ValidateCredentials should succeed, got %v", err)
	}
	var _ sources.Adapter = a
	var _ sources.ConfigurableAdapter = a
}
