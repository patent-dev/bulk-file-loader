package dpma

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	dpmaconnect "github.com/patent-dev/dpma-connect-plus"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

// mockClient implements dpmaClient for testing.
type mockClient struct {
	versionErr   error
	streamData   []byte
	streamErr    error
	calledMethod string
	calledYear   int
	calledWeek   int
}

func (m *mockClient) GetVersion(_ context.Context, _ string) (string, error) {
	return "1.0", m.versionErr
}

func (m *mockClient) recordCall(method string, year, week int, dst io.Writer) error {
	m.calledMethod = method
	m.calledYear = year
	m.calledWeek = week
	if m.streamErr != nil {
		return m.streamErr
	}
	if m.streamData != nil {
		_, err := dst.Write(m.streamData)
		return err
	}
	return nil
}

func (m *mockClient) GetDisclosureDocumentsXMLStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("disclosure-xml", y, w, dst)
}
func (m *mockClient) GetDisclosureDocumentsPDFStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("disclosure-pdf", y, w, dst)
}
func (m *mockClient) GetPatentSpecificationsXMLStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("specifications-xml", y, w, dst)
}
func (m *mockClient) GetPatentSpecificationsPDFStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("specifications-pdf", y, w, dst)
}
func (m *mockClient) GetUtilityModelsXMLStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("utility-xml", y, w, dst)
}
func (m *mockClient) GetUtilityModelsPDFStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("utility-pdf", y, w, dst)
}
func (m *mockClient) GetEuropeanPatentSpecificationsXMLStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("ep-specifications-xml", y, w, dst)
}
func (m *mockClient) GetEuropeanPatentSpecificationsPDFStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("ep-specifications-pdf", y, w, dst)
}
func (m *mockClient) GetPublicationDataXMLStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("publication-data-xml", y, w, dst)
}
func (m *mockClient) GetApplicantCitationsXMLStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("citations-xml", y, w, dst)
}
func (m *mockClient) GetDesignBibliographicDataXMLStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("bibdata-xml", y, w, dst)
}
func (m *mockClient) GetDesignImagesStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("images", y, w, dst)
}
func (m *mockClient) GetTrademarkBibDataAppliedStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("applied", y, w, dst)
}
func (m *mockClient) GetTrademarkBibDataRegisteredStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("registered", y, w, dst)
}
func (m *mockClient) GetTrademarkBibDataRejectedStream(ctx context.Context, y, w int, dst io.Writer) error {
	return m.recordCall("rejected", y, w, dst)
}

func TestFetchProducts(t *testing.T) {
	a := New()
	prods, err := a.FetchProducts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prods) != 3 {
		t.Fatalf("FetchProducts() returned %d products, want 3", len(prods))
	}

	expected := map[string]bool{"dpma-patent": false, "dpma-design": false, "dpma-trademark": false}
	for _, p := range prods {
		if _, ok := expected[p.ExternalID]; !ok {
			t.Errorf("unexpected product ID: %s", p.ExternalID)
		}
		expected[p.ExternalID] = true
		if p.Name == "" {
			t.Errorf("product %s has empty name", p.ExternalID)
		}
		if p.CheckSchedule == "" {
			t.Errorf("product %s has empty schedule", p.ExternalID)
		}
	}
	for id, found := range expected {
		if !found {
			t.Errorf("missing product: %s", id)
		}
	}
}

func TestFetchDeliveries(t *testing.T) {
	a := New()
	a.weeksToFetch = 4

	// Fix time for deterministic tests
	origFunc := currentISOWeek
	currentISOWeek = func() (int, int) { return 2026, 14 }
	defer func() { currentISOWeek = origFunc }()

	deliveries, err := a.FetchDeliveries(context.Background(), "dpma-patent")
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 4 {
		t.Fatalf("got %d deliveries, want 4", len(deliveries))
	}

	// Should start from week 13 (current week 14 excluded)
	expectedWeeks := []string{"202613", "202612", "202611", "202610"}
	for i, d := range deliveries {
		if d.ExternalID != expectedWeeks[i] {
			t.Errorf("delivery[%d].ExternalID = %q, want %q", i, d.ExternalID, expectedWeeks[i])
		}
		if d.PublishedAt.IsZero() {
			t.Errorf("delivery[%d].PublishedAt is zero", i)
		}
	}
}

func TestFetchDeliveriesYearBoundary(t *testing.T) {
	a := New()
	a.weeksToFetch = 4

	// Set to week 2 of 2025 to test crossing into 2024 (2024 has 52 weeks)
	origFunc := currentISOWeek
	currentISOWeek = func() (int, int) { return 2025, 2 }
	defer func() { currentISOWeek = origFunc }()

	deliveries, err := a.FetchDeliveries(context.Background(), "dpma-patent")
	if err != nil {
		t.Fatal(err)
	}

	expectedWeeks := []string{"202501", "202452", "202451", "202450"}
	for i, d := range deliveries {
		if d.ExternalID != expectedWeeks[i] {
			t.Errorf("delivery[%d].ExternalID = %q, want %q", i, d.ExternalID, expectedWeeks[i])
		}
	}
}

func TestFetchDeliveriesUnknownProduct(t *testing.T) {
	a := New()
	_, err := a.FetchDeliveries(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown product")
	}
}

func TestFetchFiles(t *testing.T) {
	a := New()

	tests := []struct {
		productID string
		wantCount int
	}{
		{"dpma-patent", 10},
		{"dpma-design", 2},
		{"dpma-trademark", 3},
	}

	for _, tt := range tests {
		files, err := a.FetchFiles(context.Background(), tt.productID, "202613")
		if err != nil {
			t.Fatalf("FetchFiles(%s) error: %v", tt.productID, err)
		}
		if len(files) != tt.wantCount {
			t.Errorf("FetchFiles(%s) returned %d files, want %d", tt.productID, len(files), tt.wantCount)
		}
		for _, f := range files {
			if f.FileName == "" {
				t.Error("file has empty FileName")
			}
			if f.FileSize != 0 {
				t.Errorf("FileSize = %d, want 0 (unknown)", f.FileSize)
			}
			if f.DownloadURI == "" {
				t.Error("file has empty DownloadURI")
			}
			if f.ReleasedAt.IsZero() {
				t.Error("file has zero ReleasedAt")
			}
		}
	}
}

func TestFetchFilesUnknownProduct(t *testing.T) {
	a := New()
	_, err := a.FetchFiles(context.Background(), "nonexistent", "202613")
	if err == nil {
		t.Fatal("expected error for unknown product")
	}
}

func TestDownloadFileDispatch(t *testing.T) {
	mock := &mockClient{streamData: []byte("test data")}
	a := &Adapter{client: mock, weeksToFetch: DefaultWeeksToFetch}

	tests := []struct {
		uri        string
		wantMethod string
		wantYear   int
		wantWeek   int
	}{
		{"dpma-patent:disclosure-xml:202613", "disclosure-xml", 2026, 13},
		{"dpma-patent:specifications-pdf:202501", "specifications-pdf", 2025, 1},
		{"dpma-design:images:202610", "images", 2026, 10},
		{"dpma-trademark:applied:202608", "applied", 2026, 8},
		{"dpma-trademark:rejected:202612", "rejected", 2026, 12},
	}

	for _, tt := range tests {
		var buf bytes.Buffer
		file := sources.FileInfo{DownloadURI: tt.uri}
		err := a.DownloadFile(context.Background(), file, &buf, nil)
		if err != nil {
			t.Errorf("DownloadFile(%s) error: %v", tt.uri, err)
			continue
		}
		if mock.calledMethod != tt.wantMethod {
			t.Errorf("DownloadFile(%s) called method %q, want %q", tt.uri, mock.calledMethod, tt.wantMethod)
		}
		if mock.calledYear != tt.wantYear || mock.calledWeek != tt.wantWeek {
			t.Errorf("DownloadFile(%s) called year=%d week=%d, want year=%d week=%d",
				tt.uri, mock.calledYear, mock.calledWeek, tt.wantYear, tt.wantWeek)
		}
		if buf.String() != "test data" {
			t.Errorf("DownloadFile(%s) wrote %q, want %q", tt.uri, buf.String(), "test data")
		}
	}
}

func TestDownloadFileInvalidURI(t *testing.T) {
	mock := &mockClient{}
	a := &Adapter{client: mock, weeksToFetch: DefaultWeeksToFetch}

	file := sources.FileInfo{DownloadURI: "bad-uri"}
	err := a.DownloadFile(context.Background(), file, io.Discard, nil)
	if err == nil {
		t.Fatal("expected error for invalid URI")
	}
}

func TestDownloadFileUnknownMethod(t *testing.T) {
	mock := &mockClient{}
	a := &Adapter{client: mock, weeksToFetch: DefaultWeeksToFetch}

	file := sources.FileInfo{DownloadURI: "dpma-patent:nonexistent:202613"}
	err := a.DownloadFile(context.Background(), file, io.Discard, nil)
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
}

func TestDownloadFileDataNotAvailable(t *testing.T) {
	mock := &mockClient{streamErr: &dpmaconnect.DataNotAvailableError{}}
	a := &Adapter{client: mock, weeksToFetch: DefaultWeeksToFetch}

	file := sources.FileInfo{DownloadURI: "dpma-patent:disclosure-xml:200101"}
	err := a.DownloadFile(context.Background(), file, io.Discard, nil)
	if err == nil {
		t.Fatal("expected error for unavailable data")
	}
	var adapterErr *sources.AdapterError
	if !errors.As(err, &adapterErr) {
		t.Fatalf("expected AdapterError, got %T: %v", err, err)
	}
	if adapterErr.Code != sources.ErrCodeNotFound {
		t.Errorf("error code = %q, want %q", adapterErr.Code, sources.ErrCodeNotFound)
	}
}

func TestProgressWriter(t *testing.T) {
	var buf bytes.Buffer
	var lastWritten int64

	pw := &progressWriter{
		dst: &buf,
		progressFunc: func(bytesWritten, totalBytes int64) {
			lastWritten = bytesWritten
			if totalBytes != 0 {
				t.Errorf("totalBytes = %d, want 0 (unknown)", totalBytes)
			}
		},
	}

	_, _ = pw.Write([]byte("hello"))
	if lastWritten != 5 {
		t.Errorf("after first write: bytesWritten = %d, want 5", lastWritten)
	}

	_, _ = pw.Write([]byte(" world"))
	if lastWritten != 11 {
		t.Errorf("after second write: bytesWritten = %d, want 11", lastWritten)
	}

	if buf.String() != "hello world" {
		t.Errorf("buffer = %q, want %q", buf.String(), "hello world")
	}
}

func TestProgressWriterNilFunc(t *testing.T) {
	var buf bytes.Buffer
	pw := &progressWriter{dst: &buf}

	// Should not panic with nil progressFunc
	_, err := pw.Write([]byte("data"))
	if err != nil {
		t.Errorf("Write() error = %v", err)
	}
	if buf.String() != "data" {
		t.Errorf("buffer = %q, want %q", buf.String(), "data")
	}
}

func TestWeekGeneration(t *testing.T) {
	origFunc := currentISOWeek
	defer func() { currentISOWeek = origFunc }()

	currentISOWeek = func() (int, int) { return 2026, 14 }

	weeks := generateWeeks(3)
	if len(weeks) != 3 {
		t.Fatalf("generateWeeks(3) returned %d weeks, want 3", len(weeks))
	}

	// First week should be 2026-W13 (current week 14 excluded)
	if weeks[0].YYYYWW != "202613" {
		t.Errorf("weeks[0].YYYYWW = %q, want %q", weeks[0].YYYYWW, "202613")
	}
	if weeks[0].Year != 2026 || weeks[0].Week != 13 {
		t.Errorf("weeks[0] = {%d, %d}, want {2026, 13}", weeks[0].Year, weeks[0].Week)
	}

	// Verify PublishedAt is a Thursday
	if weeks[0].PublishedAt.Weekday() != time.Thursday {
		t.Errorf("PublishedAt weekday = %s, want Thursday", weeks[0].PublishedAt.Weekday())
	}
}

func TestWeekGenerationWeek53(t *testing.T) {
	origFunc := currentISOWeek
	defer func() { currentISOWeek = origFunc }()

	// 2026 has 53 ISO weeks
	if w := isoWeeksInYear(2026); w != 53 {
		t.Fatalf("isoWeeksInYear(2026) = %d, want 53", w)
	}

	// Start at 2027-W02 and walk back past week 53 of 2026
	currentISOWeek = func() (int, int) { return 2027, 2 }
	weeks := generateWeeks(3)

	expected := []string{"202701", "202653", "202652"}
	for i, w := range weeks {
		if w.YYYYWW != expected[i] {
			t.Errorf("weeks[%d].YYYYWW = %q, want %q", i, w.YYYYWW, expected[i])
		}
	}
}

func TestIsoWeekThursday(t *testing.T) {
	tests := []struct {
		year, week int
		wantDate   string
	}{
		{2026, 1, "2026-01-01"},
		{2026, 13, "2026-03-26"},
		{2020, 53, "2020-12-31"}, // 2020 has 53 ISO weeks; W53 Thursday is Dec 31
	}

	for _, tt := range tests {
		got := isoWeekThursday(tt.year, tt.week)
		gotStr := got.Format("2006-01-02")
		if gotStr != tt.wantDate {
			t.Errorf("isoWeekThursday(%d, %d) = %s, want %s", tt.year, tt.week, gotStr, tt.wantDate)
		}
		if got.Weekday() != time.Thursday {
			t.Errorf("isoWeekThursday(%d, %d) weekday = %s, want Thursday", tt.year, tt.week, got.Weekday())
		}
	}
}

func TestParseYYYYWW(t *testing.T) {
	tests := []struct {
		input    string
		wantYear int
		wantWeek int
		wantErr  bool
	}{
		{"202613", 2026, 13, false},
		{"202553", 2025, 53, false},
		{"202501", 2025, 1, false},
		{"bad", 0, 0, true},
		{"202500", 0, 0, true}, // week 0 invalid
		{"202554", 0, 0, true}, // week 54 invalid
	}

	for _, tt := range tests {
		year, week, err := parseYYYYWW(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseYYYYWW(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			continue
		}
		if !tt.wantErr {
			if year != tt.wantYear || week != tt.wantWeek {
				t.Errorf("parseYYYYWW(%q) = (%d, %d), want (%d, %d)",
					tt.input, year, week, tt.wantYear, tt.wantWeek)
			}
		}
	}
}

func TestValidateCredentialsMissing(t *testing.T) {
	a := New()
	err := a.ValidateCredentials(context.Background())
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestConfigurableAdapterInterface(t *testing.T) {
	a := New()
	var _ sources.ConfigurableAdapter = a
}
