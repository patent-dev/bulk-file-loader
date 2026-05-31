package refdata

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestProbeForbiddenSurfaces verifies a 403/401 is reported as an error, not a
// clean "edition absent" — otherwise date-probe discovery walks back to a stale
// edition when a WAF blocks the newest one.
func TestProbeForbiddenSurfaces(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusUnauthorized} {
		client := doerFunc(func(*http.Request) (*http.Response, error) {
			return mkResp(status, "", nil), nil
		})
		a := testAdapter(dataset{}, client, time.Now())
		ok, _, err := a.probe(context.Background(), "https://x/y")
		if ok || err == nil {
			t.Errorf("status %d: want surfaced error, got ok=%v err=%v", status, ok, err)
		}
	}
}

// TestDateProbeDoesNotFallBackOn403 is the behavioural guarantee: a WAF 403 on
// the newest candidate must fail discovery, not silently resolve an older one.
func TestDateProbeDoesNotFallBackOn403(t *testing.T) {
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		return mkResp(http.StatusForbidden, "", nil), nil
	})
	d := dataset{
		ID:    "cpc",
		Files: []datasetFile{{Role: "scheme", URLTemplate: "https://x/s{ed}.zip"}},
		Discover: discoverSpec{
			Kind: discoverDateProbe, Step: stepMonthly, MaxBack: 6, ProbeRole: "scheme",
		},
	}
	a := testAdapter(d, client, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC))
	if _, _, err := a.resolveByDateProbe(context.Background(), &d); err == nil {
		t.Error("a 403 on the newest edition must surface, not fall back to a stale edition")
	}
}

// TestDownloadURLDetectsTruncation verifies a body shorter than the declared
// Content-Length is rejected rather than archived as a complete file.
func TestDownloadURLDetectsTruncation(t *testing.T) {
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		r := mkResp(http.StatusOK, "short", nil)
		r.ContentLength = 1000 // server claims 1000 bytes but sends 5
		return r, nil
	})
	a := testAdapter(dataset{}, client, time.Now())
	var buf bytes.Buffer
	if err := a.downloadURL(context.Background(), "https://x/f.zip", &buf, nil); err == nil {
		t.Error("truncated download (5 of 1000 bytes) should error")
	}
}

// TestCrawlFailsOnPageCap verifies hitting the page cap fails loudly instead of
// shipping a truncated manual.
func TestCrawlFailsOnPageCap(t *testing.T) {
	client := doerFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "index") {
			return mkResp(http.StatusOK, `<a href="/m/a">a</a><a href="/m/b">b</a><a href="/m/c">c</a><a href="/m/d">d</a>`, nil), nil
		}
		return mkResp(http.StatusOK, `<p>content</p>`, nil), nil
	})
	a := testAdapter(dataset{}, client, time.Now())
	spec := &crawlSpec{IndexURL: "https://h/m/index", LinkPrefix: "/m/", MaxPages: 2}
	var buf bytes.Buffer
	if err := a.crawl(context.Background(), spec, &buf, nil); err == nil {
		t.Error("hitting the page cap should fail, not ship a partial manual")
	}
}

// TestCrawlFailsOnExcessiveSkips verifies a crawl where most pages are
// unreachable fails rather than shipping a manual full of holes.
func TestCrawlFailsOnExcessiveSkips(t *testing.T) {
	client := doerFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "index") {
			var b strings.Builder
			for i := range 8 {
				b.WriteString(`<a href="/m/p`)
				b.WriteByte(byte('0' + i))
				b.WriteString(`">x</a>`)
			}
			return mkResp(http.StatusOK, b.String(), nil), nil
		}
		return mkResp(http.StatusNotFound, "", nil), nil // every section page is gone
	})
	a := testAdapter(dataset{}, client, time.Now())
	spec := &crawlSpec{IndexURL: "https://h/m/index", LinkPrefix: "/m/"}
	var buf bytes.Buffer
	if err := a.crawl(context.Background(), spec, &buf, nil); err == nil {
		t.Error("a crawl that skips most pages should fail")
	}
}
