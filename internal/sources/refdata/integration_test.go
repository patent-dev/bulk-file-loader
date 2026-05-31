package refdata

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// These tests drive the adapter through the real net/http stack against a local
// httptest server, exercising discovery, file building, plain downloads and the
// HTML crawler end to end without reaching the public internet.

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// Date-probe source: only the 202603 edition exists.
	mux.HandleFunc("/cpc/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "202603") {
			w.Header().Set("Last-Modified", "Wed, 12 Mar 2026 00:00:00 GMT")
			_, _ = io.WriteString(w, "ZIP:"+r.URL.Path)
			return
		}
		http.NotFound(w, r)
	})

	// Page-link source: landing page advertises the current edition.
	mux.HandleFunc("/mpep/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			_, _ = io.WriteString(w, "MPEP-BYTES")
			return
		}
		_, _ = io.WriteString(w, `<a href="e9r-01-2024.zip">current</a>`)
	})

	// Stable-URL source.
	mux.HandleFunc("/euipo.pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", "Thu, 01 May 2025 10:00:00 GMT")
		_, _ = io.WriteString(w, "PDF-BYTES")
	})

	// Crawl source: an index linking two sections (plus an off-prefix link).
	mux.HandleFunc("/manual", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", "Mon, 02 Feb 2026 00:00:00 GMT")
		_, _ = io.WriteString(w, `<a href="/manual/sec1">1</a><a href="/manual/sec2">2</a><a href="/elsewhere">x</a>`)
	})
	mux.HandleFunc("/manual/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>"+r.URL.Path+"</html>")
	})

	// JSON-API source (EUIPO-style): publications -> toc tree -> page content.
	mux.HandleFunc("/api/publications", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[
			{"Id":"800","Title":"Trade mark guidelines","Language":"en","EntryIntoForce":"2020-01-01T00:00:00","IsPubObsolete":"true"},
			{"Id":"900","Title":"Trade mark guidelines","Language":"en","EntryIntoForce":"2025-05-01T00:00:00","IsPubObsolete":null}
		]`)
	})
	mux.HandleFunc("/api/toc/900", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"Id":"t1","Title":"Trade mark guidelines","HasChildNodes":true,"Url":null}]`)
	})
	mux.HandleFunc("/api/toc/900/t1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"Id":"t1-k1","Title":"1 Introduction","HasChildNodes":false,"Url":"/900/100/trade-mark-guidelines/1-introduction"}]`)
	})
	mux.HandleFunc("/api/page/900/100", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"Regions":[{"Entities":[{"topicBody":{"Fragments":[{"Html":"<p>Intro body.</p>"}]}}]}]}`)
	})

	// Token-fetch source (EPO-Unitary-style): landing carries the year, the
	// year's index embeds section tokens, each section page is server HTML.
	mux.HandleFunc("/up", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/up/2026/index">guidelines-up/2026/</a>`)
	})
	mux.HandleFunc("/up/2026/index", func(w http.ResponseWriter, r *http.Request) {
		// Tokens appear in embedded data, with duplicates, not as plain links.
		_, _ = io.WriteString(w, `{"nav":["section_1","section_1_1","section_2","section_1"]}`)
	})
	mux.HandleFunc("/up/2026/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>"+r.URL.Path+"</html>")
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func downloadOne(t *testing.T, a *Adapter, productID string) (filename string, data []byte) {
	t.Helper()
	ctx := context.Background()

	dels, err := a.FetchDeliveries(ctx, productID)
	if err != nil {
		t.Fatalf("FetchDeliveries(%s): %v", productID, err)
	}
	if len(dels) != 1 {
		t.Fatalf("FetchDeliveries(%s) returned %d deliveries, want 1", productID, len(dels))
	}
	files, err := a.FetchFiles(ctx, productID, dels[0].ExternalID)
	if err != nil {
		t.Fatalf("FetchFiles(%s): %v", productID, err)
	}
	if len(files) == 0 {
		t.Fatalf("FetchFiles(%s) returned no files", productID)
	}
	var buf bytes.Buffer
	if err := a.DownloadFile(ctx, files[0], &buf, nil); err != nil {
		t.Fatalf("DownloadFile(%s): %v", productID, err)
	}
	return files[0].FileName, buf.Bytes()
}

func TestIntegrationDateProbeDownload(t *testing.T) {
	srv := newServer(t)
	ds := dataset{
		ID:   "cpc",
		Name: "CPC",
		Files: []datasetFile{
			{Role: "scheme", URLTemplate: srv.URL + "/cpc/CPCSchemeXML{ed}.zip", NameTemplate: "CPCSchemeXML{ed}.zip"},
			{Role: "definitions", URLTemplate: srv.URL + "/cpc/FullCPCDefinitionXML{ed}.zip", NameTemplate: "defs-{ed}.zip"},
		},
		Discover: discoverSpec{Kind: discoverDateProbe, Step: stepMonthly, MaxBack: 6, ProbeRole: "scheme"},
	}
	a := newAdapter("test", "Test", []dataset{ds})
	a.now = fixedClock(time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC))

	name, data := downloadOne(t, a, "cpc")
	if name != "CPCSchemeXML202603.zip" {
		t.Errorf("filename = %q, want CPCSchemeXML202603.zip", name)
	}
	if !strings.HasPrefix(string(data), "ZIP:") {
		t.Errorf("unexpected body %q", data)
	}
}

func TestIntegrationPageLinkDownload(t *testing.T) {
	srv := newServer(t)
	ds := dataset{
		ID:    "mpep",
		Name:  "MPEP",
		Files: []datasetFile{{Role: "manual", URLTemplate: srv.URL + "/mpep/{ed}.zip", NameTemplate: "mpep-{ed}.zip"}},
		Discover: discoverSpec{
			Kind: discoverPageLink, PageURL: srv.URL + "/mpep/", EditionRe: `(e\d+r-[0-9-]+)\.zip`,
		},
	}
	a := newAdapter("test", "Test", []dataset{ds})

	name, data := downloadOne(t, a, "mpep")
	if name != "mpep-e9r-01-2024.zip" {
		t.Errorf("filename = %q, want mpep-e9r-01-2024.zip", name)
	}
	if string(data) != "MPEP-BYTES" {
		t.Errorf("body = %q", data)
	}
}

func TestIntegrationStableURLDownload(t *testing.T) {
	srv := newServer(t)
	ds := dataset{
		ID:       "euipo-tm",
		Name:     "EUIPO",
		Files:    []datasetFile{{Role: "guidelines", URLTemplate: srv.URL + "/euipo.pdf", NameTemplate: "euipo-{ed}.pdf"}},
		Discover: discoverSpec{Kind: discoverStableURL},
	}
	a := newAdapter("test", "Test", []dataset{ds})

	name, data := downloadOne(t, a, "euipo-tm")
	if name != "euipo-20250501.pdf" {
		t.Errorf("filename = %q, want euipo-20250501.pdf (from Last-Modified)", name)
	}
	if string(data) != "PDF-BYTES" {
		t.Errorf("body = %q", data)
	}
}

func TestIntegrationJSONAPIProducesZip(t *testing.T) {
	srv := newServer(t)
	ds := dataset{
		ID:    "euipo-tm",
		Name:  "EUIPO",
		Files: []datasetFile{{Role: "manual", NameTemplate: "euipo-tm-{ed}.zip"}},
		Discover: discoverSpec{
			Kind: discoverJSONAPI,
			API: &apiSpec{
				BaseURL:       srv.URL,
				Language:      "en",
				TitleContains: "trade mark guidelines",
				MaxNodes:      100,
				ZipName:       "euipo-tm-{ed}.zip",
			},
		},
	}
	a := newAdapter("test", "Test", []dataset{ds})

	// Edition is the current (non-obsolete) publication's entry-into-force date.
	name, data := downloadOne(t, a, "euipo-tm")
	if name != "euipo-tm-20250501.zip" {
		t.Errorf("filename = %q, want euipo-tm-20250501.zip", name)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("output is not a valid zip: %v", err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[f.Name] = string(b)
	}
	if _, ok := got["toc.json"]; !ok {
		t.Errorf("zip missing toc.json; has %v", slices.Sorted(maps.Keys(got)))
	}
	if !strings.Contains(got["toc.json"], `"pageId":"100"`) || !strings.Contains(got["toc.json"], `"1 Introduction"`) {
		t.Errorf("toc.json missing expected node: %s", got["toc.json"])
	}
	if !strings.Contains(got["page-100.json"], "Intro body") {
		t.Errorf("page-100.json missing body: %q", got["page-100.json"])
	}
}

func TestIntegrationTokenFetchProducesZip(t *testing.T) {
	srv := newServer(t)
	ds := dataset{
		ID:    "epo-up",
		Name:  "EPO UP",
		Files: []datasetFile{{Role: "manual", NameTemplate: "epo-up-{ed}.zip"}},
		Discover: discoverSpec{
			Kind: discoverTokenFetch,
			Token: &tokenSpec{
				BaseURL:   srv.URL + "/up",
				YearRe:    `guidelines-up/(\d{4})/`,
				SeedToken: "index",
				TokenRe:   `section_[0-9_]+`,
				MaxPages:  100,
				ZipName:   "epo-up-{ed}.zip",
			},
		},
	}
	a := newAdapter("test", "Test", []dataset{ds})

	name, data := downloadOne(t, a, "epo-up")
	if name != "epo-up-2026.zip" {
		t.Errorf("filename = %q, want epo-up-2026.zip (year from landing)", name)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("output is not a valid zip: %v", err)
	}
	var entries []string
	for _, f := range zr.File {
		entries = append(entries, f.Name)
	}
	sort.Strings(entries)
	want := []string{"section_1.html", "section_1_1.html", "section_2.html"}
	if !slices.Equal(entries, want) {
		t.Errorf("zip entries = %v, want %v (deduped)", entries, want)
	}
}

func TestIntegrationCrawlProducesZip(t *testing.T) {
	srv := newServer(t)
	ds := dataset{
		ID:    "ipa-patent",
		Name:  "IPA",
		Files: []datasetFile{{Role: "manual", NameTemplate: "ipa-{ed}.zip"}},
		Discover: discoverSpec{
			Kind: discoverCrawl,
			Crawl: &crawlSpec{
				IndexURL: srv.URL + "/manual", LinkPrefix: "/manual/", MaxPages: 100,
			},
		},
	}
	a := newAdapter("test", "Test", []dataset{ds})

	name, data := downloadOne(t, a, "ipa-patent")
	if !strings.HasPrefix(name, "ipa-") || !strings.HasSuffix(name, ".zip") {
		t.Errorf("filename = %q", name)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("output is not a valid zip: %v", err)
	}
	var entries []string
	for _, f := range zr.File {
		entries = append(entries, f.Name)
	}
	sort.Strings(entries)
	want := []string{"manual.html", "manual/sec1.html", "manual/sec2.html"}
	if strings.Join(entries, ",") != strings.Join(want, ",") {
		t.Fatalf("zip entries = %v, want %v", entries, want)
	}

	// Verify a section page's content round-tripped into the archive.
	for _, f := range zr.File {
		if f.Name == "manual/sec1.html" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			if !strings.Contains(string(b), "/manual/sec1") {
				t.Errorf("sec1 content = %q", b)
			}
		}
	}
}

func TestIntegrationCrawlContinuesPastBrokenSection(t *testing.T) {
	// Index links a section that 404s; the crawl should still archive the
	// index and the working section.
	mux := http.NewServeMux()
	mux.HandleFunc("/m", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/m/ok">ok</a><a href="/m/broken">broken</a>`)
	})
	mux.HandleFunc("/m/ok", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "OK PAGE")
	})
	mux.HandleFunc("/m/broken", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ds := dataset{
		ID:    "ipa-patent",
		Files: []datasetFile{{Role: "manual", NameTemplate: "m-{ed}.zip"}},
		Discover: discoverSpec{
			Kind:  discoverCrawl,
			Crawl: &crawlSpec{IndexURL: srv.URL + "/m", LinkPrefix: "/m/", MaxPages: 100},
		},
	}
	a := newAdapter("test", "Test", []dataset{ds})
	_, data := downloadOne(t, a, "ipa-patent")

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 2 { // index + ok page; broken skipped
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		t.Errorf("zip has %d entries (%v), want 2 (broken section should be skipped)", len(zr.File), names)
	}
}

func TestIntegrationCrawlFollowsNestedLinks(t *testing.T) {
	// The index links sec1; sec1 links a deeper sec1a not on the index. BFS must
	// discover sec1a (a single-level crawl would miss it).
	mux := http.NewServeMux()
	mux.HandleFunc("/m", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/m/sec1">1</a>`)
	})
	mux.HandleFunc("/m/sec1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `deeper: <a href="/m/sec1a">1a</a>`)
	})
	mux.HandleFunc("/m/sec1a", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "leaf")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ds := dataset{
		ID:       "ipa-patent",
		Files:    []datasetFile{{Role: "manual", NameTemplate: "m-{ed}.zip"}},
		Discover: discoverSpec{Kind: discoverCrawl, Crawl: &crawlSpec{IndexURL: srv.URL + "/m", LinkPrefix: "/m/", MaxPages: 100}},
	}
	a := newAdapter("test", "Test", []dataset{ds})
	_, data := downloadOne(t, a, "ipa-patent")

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 3 { // index + sec1 + sec1a
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		t.Errorf("zip has %d entries (%v), want 3 (nested sec1a must be discovered)", len(zr.File), names)
	}
}

func TestIntegrationCrawlSkipsAndScrapesEdition(t *testing.T) {
	// Index links a TOC (followed), an archived /old/ page and a binary .zip
	// (both skipped), and advertises an edition string scraped via EditionRe.
	mux := http.NewServeMux()
	mux.HandleFunc("/m", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `current e9r-01-2024.zip
			<a href="/m/toc">toc</a>
			<a href="/m/old/v1">old</a>
			<a href="/m/dump.zip">zip</a>`)
	})
	mux.HandleFunc("/m/toc", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/m/sec1">1</a>`)
	})
	mux.HandleFunc("/m/sec1", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "S1") })
	mux.HandleFunc("/m/old/v1", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "OLD") })
	mux.HandleFunc("/m/dump.zip", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "BINARY") })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ds := dataset{
		ID:    "mpep",
		Files: []datasetFile{{Role: "manual", NameTemplate: "m-{ed}.zip"}},
		Discover: discoverSpec{
			Kind: discoverCrawl,
			Crawl: &crawlSpec{
				IndexURL:   srv.URL + "/m",
				LinkPrefix: "/m/",
				SkipRe:     `(?i)(/old/|\.zip$)`,
				EditionRe:  `(e\d+r-[0-9-]+)\.zip`,
				MaxPages:   100,
			},
		},
	}
	a := newAdapter("test", "Test", []dataset{ds})

	// Edition is scraped from the index page.
	dels, err := a.FetchDeliveries(context.Background(), "mpep")
	if err != nil {
		t.Fatal(err)
	}
	if dels[0].ExternalID != "e9r-01-2024" {
		t.Errorf("edition = %q, want e9r-01-2024", dels[0].ExternalID)
	}

	_, data := downloadOne(t, a, "mpep")
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	// index + toc + sec1; old/ and the .zip must be skipped.
	if len(zr.File) != 3 {
		t.Fatalf("zip entries = %v, want 3 (old/ + .zip skipped)", names)
	}
	for _, n := range names {
		if strings.Contains(n, "old") || strings.HasSuffix(n, ".zip.html") || strings.Contains(n, "dump") {
			t.Errorf("skipped resource leaked into archive: %s", n)
		}
	}
}

func TestIntegrationCrawlResolvesRelativeLinksPerPage(t *testing.T) {
	// The index links into a sub-directory; the sub-page's RELATIVE link must
	// resolve against the sub-page's URL, not the index URL (the EPO bug).
	mux := http.NewServeMux()
	mux.HandleFunc("/a/index", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/a/sub/p1.html">p1</a>`)
	})
	mux.HandleFunc("/a/sub/p1.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="p2.html">p2</a>`) // relative -> /a/sub/p2.html
	})
	mux.HandleFunc("/a/sub/p2.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "leaf p2")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ds := dataset{
		ID:       "epo-epc",
		Files:    []datasetFile{{Role: "manual", NameTemplate: "m-{ed}.zip"}},
		Discover: discoverSpec{Kind: discoverCrawl, Crawl: &crawlSpec{IndexURL: srv.URL + "/a/index", LinkPrefix: "/a/", MaxPages: 100}},
	}
	a := newAdapter("test", "Test", []dataset{ds})
	_, data := downloadOne(t, a, "epo-epc")

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 3 { // index + p1 + p2 (p2 only reached via per-page relative resolution)
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		t.Fatalf("zip entries = %v, want 3 (relative link must resolve per-page)", names)
	}
}

func TestIntegrationCrawlFailsWhenIndexUnreachable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/m", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ds := dataset{
		ID:       "ipa-patent",
		Files:    []datasetFile{{Role: "manual", NameTemplate: "m-{ed}.zip"}},
		Discover: discoverSpec{Kind: discoverCrawl, Crawl: &crawlSpec{IndexURL: srv.URL + "/m", LinkPrefix: "/m/", MaxPages: 100}},
	}
	a := newAdapter("test", "Test", []dataset{ds})

	files, err := a.FetchFiles(context.Background(), "ipa-patent", "20260101")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := a.DownloadFile(context.Background(), files[0], &buf, nil); err == nil {
		t.Error("crawl should fail when the index yields no pages, got nil error")
	}
}
