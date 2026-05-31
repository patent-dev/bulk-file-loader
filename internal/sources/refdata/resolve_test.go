package refdata

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testAdapter builds an Adapter with a single dataset, an injected client and a
// pinned clock.
func testAdapter(d dataset, client httpDoer, now time.Time) *Adapter {
	a := newAdapter("test", "Test", []dataset{d})
	a.client = client
	a.now = fixedClock(now)
	return a
}

func TestResolveByDateProbe(t *testing.T) {
	// Newest two editions 404; the third exists with a Last-Modified header.
	client := doerFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "202605"):
			return mkResp(http.StatusNotFound, "", nil), nil
		case strings.Contains(r.URL.Path, "202604"):
			return mkResp(http.StatusNotFound, "", nil), nil
		case strings.Contains(r.URL.Path, "202603"):
			return mkResp(http.StatusPartialContent, "x", map[string]string{
				"Last-Modified": "Wed, 12 Mar 2026 00:00:00 GMT",
			}), nil
		default:
			return mkResp(http.StatusNotFound, "", nil), nil
		}
	})
	d := dataset{
		ID:    "cpc",
		Files: []datasetFile{{Role: "scheme", URLTemplate: "https://x/CPCSchemeXML{ed}.zip"}},
		Discover: discoverSpec{
			Kind: discoverDateProbe, Step: stepMonthly, MaxBack: 6, ProbeRole: "scheme",
		},
	}
	a := testAdapter(d, client, time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))

	ed, published, err := a.resolveEdition(context.Background(), &a.datasets[0])
	if err != nil {
		t.Fatal(err)
	}
	if ed != "202603" {
		t.Errorf("edition = %q, want 202603", ed)
	}
	if published.Month() != time.March {
		t.Errorf("publishedAt = %v, want March 2026", published)
	}
}

func TestResolveByDateProbeNotFound(t *testing.T) {
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		return mkResp(http.StatusNotFound, "", nil), nil
	})
	d := dataset{
		ID:       "cpc",
		Files:    []datasetFile{{Role: "scheme", URLTemplate: "https://x/{ed}.zip"}},
		Discover: discoverSpec{Kind: discoverDateProbe, Step: stepMonthly, MaxBack: 3, ProbeRole: "scheme"},
	}
	a := testAdapter(d, client, time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))
	if _, _, err := a.resolveEdition(context.Background(), &a.datasets[0]); err == nil {
		t.Error("expected not-found error when no edition exists")
	}
}

func TestResolveByPageLink(t *testing.T) {
	page := `<a href="/web/offices/pac/mpep/e9r-01-2024.zip">MPEP current</a>`
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		return mkResp(http.StatusOK, page, nil), nil
	})
	d := dataset{
		ID:    "mpep",
		Files: []datasetFile{{Role: "manual", URLTemplate: "https://x/{ed}.zip"}},
		Discover: discoverSpec{
			Kind: discoverPageLink, PageURL: "https://x/mpep/", EditionRe: `(e\d+r-[0-9-]+)\.zip`,
		},
	}
	a := testAdapter(d, client, time.Now())
	ed, _, err := a.resolveEdition(context.Background(), &a.datasets[0])
	if err != nil {
		t.Fatal(err)
	}
	if ed != "e9r-01-2024" {
		t.Errorf("edition = %q, want e9r-01-2024", ed)
	}
}

func TestResolveByPageLinkNoMatch(t *testing.T) {
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		return mkResp(http.StatusOK, "<html>nothing here</html>", nil), nil
	})
	d := dataset{
		ID:       "mpep",
		Discover: discoverSpec{Kind: discoverPageLink, PageURL: "https://x/", EditionRe: `href="(e\d+r-[0-9-]+)\.zip"`},
	}
	a := testAdapter(d, client, time.Now())
	if _, _, err := a.resolveEdition(context.Background(), &a.datasets[0]); err == nil {
		t.Error("expected error when the edition link is absent")
	}
}

func TestResolveByStableURL(t *testing.T) {
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		return mkResp(http.StatusOK, "x", map[string]string{
			"Last-Modified": "Thu, 01 May 2025 10:00:00 GMT",
		}), nil
	})
	d := dataset{
		ID:       "euipo-tm",
		Files:    []datasetFile{{Role: "guidelines", URLTemplate: "https://x/g.pdf"}},
		Discover: discoverSpec{Kind: discoverStableURL},
	}
	a := testAdapter(d, client, time.Now())
	ed, published, err := a.resolveEdition(context.Background(), &a.datasets[0])
	if err != nil {
		t.Fatal(err)
	}
	if ed != "20250501" {
		t.Errorf("edition = %q, want 20250501", ed)
	}
	if published.IsZero() {
		t.Error("publishedAt should be set from Last-Modified")
	}
}

func TestResolveByStableURLNoLastModified(t *testing.T) {
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		return mkResp(http.StatusOK, "x", nil), nil
	})
	d := dataset{
		ID:       "euipo-tm",
		Files:    []datasetFile{{Role: "guidelines", URLTemplate: "https://x/g.pdf"}},
		Discover: discoverSpec{Kind: discoverStableURL},
	}
	a := testAdapter(d, client, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	ed, _, err := a.resolveEdition(context.Background(), &a.datasets[0])
	if err != nil {
		t.Fatal(err)
	}
	if ed != "20260102" {
		t.Errorf("fallback edition = %q, want today's date 20260102", ed)
	}
}

func TestProbePropagatesServerError(t *testing.T) {
	client := doerFunc(func(*http.Request) (*http.Response, error) {
		return mkResp(http.StatusInternalServerError, "", nil), nil
	})
	a := testAdapter(dataset{}, client, time.Now())
	if ok, _, err := a.probe(context.Background(), "https://x/y"); ok || err == nil {
		t.Error("a 500 status should surface as an error, not a clean not-found")
	}
}

func TestExtractLinks(t *testing.T) {
	base, _ := url.Parse("https://h/patent")
	body := []byte(`
		<a href="/patent/a">A</a>
		<a href="/patent/b#section">B</a>
		<a href="/patent/a">dup A</a>
		<a href="/patent/c?x=1">C with query</a>
		<a href="/other/d">other path</a>
		<a href="https://elsewhere/patent/e">other host</a>
		<a href="/patent">index itself</a>
	`)
	got := extractLinks(body, base, "/patent/", 0)
	want := []string{"https://h/patent/a", "https://h/patent/b", "https://h/patent/c"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("extractLinks = %v, want %v", got, want)
	}
}

func TestExtractLinksCap(t *testing.T) {
	base, _ := url.Parse("https://h/patent")
	body := []byte(`<a href="/patent/a">a</a><a href="/patent/b">b</a><a href="/patent/c">c</a>`)
	// max=3 reserves one slot for the index, so at most 2 section links.
	got := extractLinks(body, base, "/patent/", 3)
	if len(got) != 2 {
		t.Errorf("with cap 3, got %d links, want 2", len(got))
	}
}
