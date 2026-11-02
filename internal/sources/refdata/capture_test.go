package refdata

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The replay tests run the adapter against HTTP exchanges recorded from the
// real sources and stored under testdata/<scenario>/. Recording is opt-in:
//
//	REFDATA_CAPTURE=1 go test ./internal/sources/refdata/ -run TestCapture -v
//
// re-runs every scenario against the live servers and rewrites testdata.

// recordedHeaders are the response headers kept in a capture.
var recordedHeaders = []string{"Content-Type", "Content-Length", "Content-Range", "Last-Modified"}

type exchange struct {
	Method string            `json:"method"`
	URL    string            `json:"url"`
	Range  string            `json:"range,omitempty"`
	Status int               `json:"status"`
	Header map[string]string `json:"header,omitempty"`
	Body   string            `json:"body,omitempty"` // gzip file name under bodies/
}

type capture struct {
	CapturedAt time.Time  `json:"capturedAt"`
	Exchanges  []exchange `json:"exchanges"`
}

func exchangeKey(method, url, rng string) string { return method + " " + url + " " + rng }

// recorder performs real requests and records every exchange into a staging
// directory; save moves it over testdata/<scenario> only once the scenario
// behaved as expected, so a blocked or broken capture never replaces good data.
type recorder struct {
	client *http.Client
	dir    string // staging directory

	mu  sync.Mutex
	cap capture
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bodies"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &recorder{client: &http.Client{}, dir: dir, cap: capture{CapturedAt: time.Now().UTC()}}
}

func (r *recorder) Do(req *http.Request) (*http.Response, error) {
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}

	ex := exchange{
		Method: req.Method,
		URL:    req.URL.String(),
		Range:  req.Header.Get("Range"),
		Status: resp.StatusCode,
		Header: map[string]string{},
	}
	for _, h := range recordedHeaders {
		if v := resp.Header.Get(h); v != "" {
			ex.Header[h] = v
		}
	}
	if len(body) > 0 {
		sum := sha256.Sum256(body)
		ex.Body = hex.EncodeToString(sum[:8]) + bodyExt(resp.Header.Get("Content-Type")) + ".gz"
		var gz bytes.Buffer
		zw := gzip.NewWriter(&gz)
		_, _ = zw.Write(body)
		if err := zw.Close(); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(r.dir, "bodies", ex.Body), gz.Bytes(), 0o644); err != nil {
			return nil, err
		}
	}
	r.mu.Lock()
	r.cap.Exchanges = append(r.cap.Exchanges, ex)
	r.mu.Unlock()

	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

func (r *recorder) save(t *testing.T, scenario string) {
	t.Helper()
	data, err := json.MarshalIndent(r.cap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "exchanges.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join("testdata", scenario)
	if err := os.RemoveAll(dest); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dest, os.DirFS(r.dir)); err != nil {
		t.Fatal(err)
	}
}

func bodyExt(contentType string) string {
	mt, _, _ := mime.ParseMediaType(contentType)
	switch {
	case mt == "text/html":
		return ".html"
	case mt == "application/json", strings.HasSuffix(mt, "+json"):
		return ".json"
	case mt == "application/zip", mt == "application/x-zip-compressed":
		return ".zip"
	}
	return ".bin"
}

// replayer serves recorded exchanges. Requests that were not recorded fail the
// test. Individual URLs can be overridden to inject faults (an outage, a WAF
// block) on top of the real data.
type replayer struct {
	t     *testing.T
	dir   string
	cap   capture
	byKey map[string]exchange

	mu        sync.Mutex
	overrides map[string]func(exchange, []byte) (int, []byte, int64)
}

func loadReplay(t *testing.T, scenario string) *replayer {
	t.Helper()
	dir := filepath.Join("testdata", scenario)
	data, err := os.ReadFile(filepath.Join(dir, "exchanges.json"))
	if err != nil {
		t.Fatalf("missing capture %s (record with REFDATA_CAPTURE=1): %v", scenario, err)
	}
	rp := &replayer{t: t, dir: dir, byKey: map[string]exchange{}, overrides: map[string]func(exchange, []byte) (int, []byte, int64){}}
	if err := json.Unmarshal(data, &rp.cap); err != nil {
		t.Fatal(err)
	}
	for _, ex := range rp.cap.Exchanges {
		rp.byKey[exchangeKey(ex.Method, ex.URL, ex.Range)] = ex
	}
	return rp
}

// override replaces the response for url. fn receives the recorded exchange
// and body and returns the status, body and declared Content-Length to serve.
func (rp *replayer) override(url string, fn func(ex exchange, body []byte) (status int, out []byte, contentLength int64)) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	rp.overrides[url] = fn
}

// failWith makes url answer with status and an empty body.
func (rp *replayer) failWith(url string, status int) {
	rp.override(url, func(exchange, []byte) (int, []byte, int64) { return status, nil, 0 })
}

func (rp *replayer) body(ex exchange) []byte {
	if ex.Body == "" {
		return nil
	}
	f, err := os.Open(filepath.Join(rp.dir, "bodies", ex.Body))
	if err != nil {
		rp.t.Errorf("open body %s: %v", ex.Body, err)
		return nil
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		rp.t.Errorf("read body %s: %v", ex.Body, err)
		return nil
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		rp.t.Errorf("read body %s: %v", ex.Body, err)
	}
	return b
}

func (rp *replayer) Do(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	ex, ok := rp.byKey[exchangeKey(req.Method, req.URL.String(), req.Header.Get("Range"))]
	if !ok {
		rp.t.Errorf("unrecorded request: %s %s (range %q)", req.Method, req.URL, req.Header.Get("Range"))
		return nil, fmt.Errorf("unrecorded request %s", req.URL)
	}
	body := rp.body(ex)
	status := ex.Status
	// Without a recorded Content-Length the server streamed the body
	// (chunked); replay it the same way.
	contentLength := int64(-1)
	if v, err := strconv.ParseInt(ex.Header["Content-Length"], 10, 64); err == nil {
		contentLength = v
	}

	rp.mu.Lock()
	fn := rp.overrides[ex.URL]
	rp.mu.Unlock()
	if fn != nil {
		status, body, contentLength = fn(ex, body)
	}

	h := make(http.Header)
	for k, v := range ex.Header {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode:    status,
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: contentLength,
		Request:       req,
	}, nil
}

// replayAdapter returns an adapter over datasets whose HTTP traffic and clock
// come from the named capture.
func replayAdapter(t *testing.T, scenario string, datasets []dataset) (*Adapter, *replayer) {
	t.Helper()
	rp := loadReplay(t, scenario)
	a := newAdapter("test", "Test", datasets)
	a.client = rp
	a.now = fixedClock(rp.cap.CapturedAt)
	return a, rp
}

// fixedClock returns a clock function pinned to t.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// scenario is one recorded interaction with the real sources. run must make
// the same requests on every call given the same clock, so that a replay
// reproduces the recording exactly. wantErr marks scenarios that record a
// failure on purpose (an exceeded cap); any other outcome aborts the capture.
type scenario struct {
	name     string
	datasets func() []dataset
	run      func(ctx context.Context, a *Adapter) error
	wantErr  bool
}

// TestCapture records every scenario against the live sources.
func TestCapture(t *testing.T) {
	if os.Getenv("REFDATA_CAPTURE") == "" {
		t.Skip("set REFDATA_CAPTURE=1 to record testdata from the live sources")
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			rec := newRecorder(t)
			a := newAdapter("test", "Test", sc.datasets())
			a.client = rec
			a.now = fixedClock(rec.cap.CapturedAt)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			err := sc.run(ctx, a)
			if (err != nil) != sc.wantErr {
				t.Fatalf("scenario error = %v, wantErr %v; testdata/%s left unchanged", err, sc.wantErr, sc.name)
			}
			rec.save(t, sc.name)
		})
	}
}
