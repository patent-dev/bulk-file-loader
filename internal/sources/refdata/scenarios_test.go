package refdata

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

// datasetByID returns a copy of a production dataset.
func datasetByID(id string) dataset {
	for _, d := range append(classificationDatasets(), manualDatasets()...) {
		if d.ID == id {
			return d
		}
	}
	panic("unknown dataset " + id)
}

// Narrowed copies of production datasets keep the recordings small while still
// exercising the real servers and page formats.
func ipaChapter3() dataset {
	d := datasetByID("ipa-patent")
	d.Discover.Crawl = &crawlSpec{
		IndexURL:   "https://manuals.ipaustralia.gov.au/patent/3.1-quality",
		LinkPrefix: "/patent/3.",
		MaxPages:   50,
	}
	return d
}

func unitarySection12() dataset {
	d := datasetByID("epo-up")
	spec := *d.Discover.Token
	spec.TokenRe = `section_1_2[0-9_]*`
	d.Discover.Token = &spec
	return d
}

// euipoCapped stops the EUIPO walk after a few nodes; the full guidelines are
// too large to record.
func euipoCapped() dataset {
	d := datasetByID("euipo-tm")
	spec := *d.Discover.API
	spec.MaxNodes = 5
	d.Discover.API = &spec
	return d
}

// fetchAndDownload resolves the product's edition and file list, then
// downloads the files whose role is listed.
func fetchAndDownload(ctx context.Context, a *Adapter, product string, roles ...string) error {
	dels, err := a.FetchDeliveries(ctx, product)
	if err != nil {
		return err
	}
	files, err := a.FetchFiles(ctx, product, dels[0].ExternalID)
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range files {
		for _, role := range roles {
			if strings.Contains(f.ExternalID, ":"+role+":") {
				errs = append(errs, a.DownloadFile(ctx, f, io.Discard, nil))
			}
		}
	}
	return errors.Join(errs...)
}

var scenarios = []scenario{
	{
		name:     "cpc",
		datasets: func() []dataset { return []dataset{datasetByID("cpc")} },
		run: func(ctx context.Context, a *Adapter) error {
			return fetchAndDownload(ctx, a, "cpc", "notice")
		},
	},
	{
		name:     "ipc",
		datasets: func() []dataset { return []dataset{datasetByID("ipc")} },
		run: func(ctx context.Context, a *Adapter) error {
			return fetchAndDownload(ctx, a, "ipc", "concordance")
		},
	},
	{
		name:     "nice-locarno",
		datasets: func() []dataset { return []dataset{datasetByID("nice"), datasetByID("locarno")} },
		run: func(ctx context.Context, a *Adapter) error {
			return errors.Join(fetchAndDownload(ctx, a, "nice"), fetchAndDownload(ctx, a, "locarno"))
		},
	},
	{
		name:     "editions",
		datasets: func() []dataset { return append([]dataset{datasetByID("jp-fterm")}, manualDatasets()...) },
		run: func(ctx context.Context, a *Adapter) error {
			var errs []error
			for _, d := range a.datasets {
				_, err := a.FetchDeliveries(ctx, d.ID)
				errs = append(errs, err)
			}
			return errors.Join(errs...)
		},
	},
	{
		name:     "crawl-ipa",
		datasets: func() []dataset { return []dataset{ipaChapter3()} },
		run: func(ctx context.Context, a *Adapter) error {
			return fetchAndDownload(ctx, a, "ipa-patent", "manual")
		},
	},
	{
		name:     "token-unitary",
		datasets: func() []dataset { return []dataset{unitarySection12()} },
		run: func(ctx context.Context, a *Adapter) error {
			return fetchAndDownload(ctx, a, "epo-up", "manual")
		},
	},
	{
		name:     "euipo",
		datasets: func() []dataset { return []dataset{euipoCapped()} },
		run: func(ctx context.Context, a *Adapter) error {
			return fetchAndDownload(ctx, a, "euipo-tm", "manual")
		},
	},
}

// recorded returns the exchanges of a capture matching method and a URL
// predicate, in recording order.
func (rp *replayer) recorded(method string, match func(u string) bool) []exchange {
	var out []exchange
	for _, ex := range rp.cap.Exchanges {
		if ex.Method == method && match(ex.URL) {
			out = append(out, ex)
		}
	}
	return out
}

func download(t *testing.T, a *Adapter, f sources.FileInfo) ([]byte, error) {
	t.Helper()
	var buf bytes.Buffer
	err := a.DownloadFile(context.Background(), f, &buf, nil)
	return buf.Bytes(), err
}

func editionAndFiles(t *testing.T, a *Adapter, product string) (sources.DeliveryInfo, []sources.FileInfo) {
	t.Helper()
	dels, err := a.FetchDeliveries(context.Background(), product)
	if err != nil {
		t.Fatalf("FetchDeliveries(%s): %v", product, err)
	}
	if len(dels) != 1 {
		t.Fatalf("FetchDeliveries(%s) = %d deliveries, want 1", product, len(dels))
	}
	files, err := a.FetchFiles(context.Background(), product, dels[0].ExternalID)
	if err != nil {
		t.Fatalf("FetchFiles(%s): %v", product, err)
	}
	return dels[0], files
}

func fileWithRole(t *testing.T, files []sources.FileInfo, role string) sources.FileInfo {
	t.Helper()
	for _, f := range files {
		if strings.Contains(f.ExternalID, ":"+role+":") {
			return f
		}
	}
	t.Fatalf("no %s file in %v", role, files)
	return sources.FileInfo{}
}

func zipEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("zip entry %s: %v", f.Name, err)
		}
		out[f.Name] = b
	}
	return out
}

func wantAdapterError(t *testing.T, err error, code string) {
	t.Helper()
	var ae *sources.AdapterError
	if !errors.As(err, &ae) {
		t.Fatalf("got %v, want an AdapterError %s", err, code)
	}
	if ae.Code != code {
		t.Errorf("error code = %s (%v), want %s", ae.Code, err, code)
	}
}

func TestReplayCPCDateProbe(t *testing.T) {
	a, rp := replayAdapter(t, "cpc", []dataset{datasetByID("cpc")})
	del, files := editionAndFiles(t, a, "cpc")

	// The edition is the newest month whose scheme probe succeeded; every newer
	// candidate was a real 404.
	probes := rp.recorded(http.MethodGet, func(u string) bool { return strings.Contains(u, "CPCSchemeXML") })
	last := probes[len(probes)-1]
	if last.Status != http.StatusPartialContent || !strings.Contains(last.URL, del.ExternalID) {
		t.Fatalf("edition %s does not match the successful probe %s (%d)", del.ExternalID, last.URL, last.Status)
	}
	for _, p := range probes[:len(probes)-1] {
		if p.Status != http.StatusNotFound {
			t.Errorf("newer candidate %s recorded %d, want 404", p.URL, p.Status)
		}
	}
	if del.PublishedAt.IsZero() {
		t.Error("PublishedAt should come from the probe's Last-Modified")
	}
	if len(files) != 3 {
		t.Fatalf("got %d files, want scheme, definitions and notice", len(files))
	}

	notice := fileWithRole(t, files, "notice")
	data, err := download(t, a, notice)
	if err != nil {
		t.Fatal(err)
	}
	if len(zipEntries(t, data)) == 0 {
		t.Error("notice of change zip is empty")
	}
}

func TestReplayCPCDiscoveryRejectsInconclusiveProbes(t *testing.T) {
	// A WAF block or challenge on the newest candidate must fail discovery,
	// not walk back to an older edition.
	cases := []struct {
		status int
		code   string
	}{
		{http.StatusForbidden, sources.ErrCodeAuth},
		{http.StatusUnauthorized, sources.ErrCodeAuth},
		{http.StatusAccepted, sources.ErrCodeNetwork},
		{http.StatusInternalServerError, sources.ErrCodeNetwork},
	}
	for _, c := range cases {
		t.Run(http.StatusText(c.status), func(t *testing.T) {
			a, rp := replayAdapter(t, "cpc", []dataset{datasetByID("cpc")})
			newest := rp.recorded(http.MethodGet, func(u string) bool { return strings.Contains(u, "CPCSchemeXML") })[0]
			rp.failWith(newest.URL, c.status)
			_, err := a.FetchDeliveries(context.Background(), "cpc")
			wantAdapterError(t, err, c.code)
		})
	}
}

func TestReplayDownloadDetectsTruncation(t *testing.T) {
	a, rp := replayAdapter(t, "cpc", []dataset{datasetByID("cpc")})
	_, files := editionAndFiles(t, a, "cpc")
	notice := fileWithRole(t, files, "notice")

	// The server declares the full length but the connection ends halfway.
	rp.override(notice.DownloadURI, func(ex exchange, body []byte) (int, []byte, int64) {
		return ex.Status, body[:len(body)/2], int64(len(body))
	})
	if _, err := download(t, a, notice); err == nil {
		t.Fatal("a truncated download must fail")
	}
}

func TestReplayDownloadRejectsChallengePage(t *testing.T) {
	a, rp := replayAdapter(t, "cpc", []dataset{datasetByID("cpc")})
	_, files := editionAndFiles(t, a, "cpc")
	notice := fileWithRole(t, files, "notice")
	rp.failWith(notice.DownloadURI, http.StatusAccepted)
	_, err := download(t, a, notice)
	wantAdapterError(t, err, sources.ErrCodeNetwork)
}

func TestReplayIPC(t *testing.T) {
	a, _ := replayAdapter(t, "ipc", []dataset{datasetByID("ipc")})
	del, files := editionAndFiles(t, a, "ipc")
	if !regexp.MustCompile(`^\d{4}0101$`).MatchString(del.ExternalID) {
		t.Errorf("IPC edition %q is not a 1 January date", del.ExternalID)
	}
	if len(files) != 4 {
		t.Fatalf("got %d files, want scheme, definitions, concordance and catchword", len(files))
	}
	data, err := download(t, a, fileWithRole(t, files, "concordance"))
	if err != nil {
		t.Fatal(err)
	}
	if len(zipEntries(t, data)) == 0 {
		t.Error("concordance zip is empty")
	}
}

func TestReplayNiceLocarnoListing(t *testing.T) {
	a, _ := replayAdapter(t, "nice-locarno", []dataset{datasetByID("nice"), datasetByID("locarno")})
	for _, product := range []string{"nice", "locarno"} {
		del, files := editionAndFiles(t, a, product)
		if len(files) != 2 {
			t.Fatalf("%s: got %d files, want top_structure and texts", product, len(files))
		}
		for _, f := range files {
			// The listing links are root-relative; they must be resolved
			// against the listing URL.
			u, err := url.Parse(f.DownloadURI)
			if err != nil || u.Scheme != "https" || u.Host != "www.wipo.int" {
				t.Errorf("%s: DownloadURI %q is not an absolute wipo.int URL", product, f.DownloadURI)
			}
			if !strings.Contains(u.Path, "/"+del.ExternalID+"/MasterFiles/") || !strings.HasSuffix(u.Path, "/"+f.FileName) {
				t.Errorf("%s: DownloadURI %q does not point at %s in the %s master files", product, f.DownloadURI, f.FileName, del.ExternalID)
			}
		}
	}
}

func TestReplayListingRejectsChallengePage(t *testing.T) {
	a, rp := replayAdapter(t, "nice-locarno", []dataset{datasetByID("nice"), datasetByID("locarno")})
	del, err := a.FetchDeliveries(context.Background(), "nice")
	if err != nil {
		t.Fatal(err)
	}
	rp.failWith(subst(datasetByID("nice").Discover.Index.DirURLTemplate, del[0].ExternalID), http.StatusAccepted)
	_, err = a.FetchFiles(context.Background(), "nice", del[0].ExternalID)
	wantAdapterError(t, err, sources.ErrCodeNetwork)
}

func TestReplayEditions(t *testing.T) {
	datasets := append([]dataset{datasetByID("jp-fterm")}, manualDatasets()...)
	a, _ := replayAdapter(t, "editions", datasets)
	formats := map[string]string{
		"jp-fterm":      `^\d{8}$`,
		"mpep":          `^e\d+r-[0-9-]+$`,
		"tmep":          `^[a-z]+\d{4}$`,
		"euipo-tm":      `^\d{8}$`,
		"wipo-ispe":     `^\d{6}(\d{2})?$`, // Last-Modified date, else the month
		"epo-epc":       `^\d{4}$`,
		"epo-pct":       `^\d{4}$`,
		"epo-up":        `^\d{4}$`,
		"ipa-patent":    `^\d{6}(\d{2})?$`,
		"ipa-trademark": `^\d{6}(\d{2})?$`,
	}
	for _, d := range datasets {
		dels, err := a.FetchDeliveries(context.Background(), d.ID)
		if err != nil {
			t.Errorf("%s: %v", d.ID, err)
			continue
		}
		re, ok := formats[d.ID]
		if !ok {
			t.Errorf("%s: no expected edition format", d.ID)
			continue
		}
		if !regexp.MustCompile(re).MatchString(dels[0].ExternalID) {
			t.Errorf("%s: edition %q does not match %s", d.ID, dels[0].ExternalID, re)
		}
	}
}

func TestReplayCrawl(t *testing.T) {
	a, rp := replayAdapter(t, "crawl-ipa", []dataset{ipaChapter3()})
	_, files := editionAndFiles(t, a, "ipa-patent")
	data, err := download(t, a, files[0])
	if err != nil {
		t.Fatal(err)
	}
	entries := zipEntries(t, data)

	pages := rp.recorded(http.MethodGet, func(string) bool { return true })
	var want, broken int
	for _, p := range pages {
		if p.Range != "" {
			continue // the edition probe
		}
		if p.Status != http.StatusOK {
			// The live manual links pages that no longer exist; they are
			// skipped, not archived.
			broken++
			if _, ok := entries[zipEntryName(p.URL)]; ok {
				t.Errorf("broken page %s (%d) was archived", p.URL, p.Status)
			}
			continue
		}
		want++
		body, ok := entries[zipEntryName(p.URL)]
		if !ok {
			t.Errorf("page %s missing from the archive", p.URL)
			continue
		}
		if !bytes.Equal(body, rp.body(p)) {
			t.Errorf("page %s archived with different content", p.URL)
		}
	}
	if want < 7 {
		t.Fatalf("capture has only %d pages; the skip tests need at least 7", want)
	}
	if len(entries) != want {
		t.Errorf("archive has %d entries, want %d", len(entries), want)
	}
}

// crawlSections returns the recorded section pages (everything but the index
// and the edition probe).
func crawlSections(rp *replayer, index string) []exchange {
	return rp.recorded(http.MethodGet, func(u string) bool { return u != index })
}

func TestReplayCrawlToleratesOneBrokenPage(t *testing.T) {
	d := ipaChapter3()
	a, rp := replayAdapter(t, "crawl-ipa", []dataset{d})
	_, files := editionAndFiles(t, a, "ipa-patent")
	sections := crawlSections(rp, d.Discover.Crawl.IndexURL)
	rp.failWith(sections[0].URL, http.StatusNotFound)

	data, err := download(t, a, files[0])
	if err != nil {
		t.Fatalf("a single broken page must not fail the crawl: %v", err)
	}
	if _, ok := zipEntries(t, data)[zipEntryName(sections[0].URL)]; ok {
		t.Error("the broken page must not be archived")
	}
}

func TestReplayCrawlFailsOnWidespreadOutage(t *testing.T) {
	d := ipaChapter3()
	a, rp := replayAdapter(t, "crawl-ipa", []dataset{d})
	_, files := editionAndFiles(t, a, "ipa-patent")
	for _, s := range crawlSections(rp, d.Discover.Crawl.IndexURL) {
		rp.failWith(s.URL, http.StatusForbidden)
	}
	_, err := download(t, a, files[0])
	wantAdapterError(t, err, sources.ErrCodeNetwork)
}

func TestReplayCrawlFailsOnPageCap(t *testing.T) {
	d := ipaChapter3()
	d.Discover.Crawl.MaxPages = 3
	a, _ := replayAdapter(t, "crawl-ipa", []dataset{d})
	_, files := editionAndFiles(t, a, "ipa-patent")
	_, err := download(t, a, files[0])
	wantAdapterError(t, err, sources.ErrCodeNetwork)
}

func TestReplayCrawlFailsWhenIndexUnreachable(t *testing.T) {
	d := ipaChapter3()
	a, rp := replayAdapter(t, "crawl-ipa", []dataset{d})
	_, files := editionAndFiles(t, a, "ipa-patent")
	rp.failWith(d.Discover.Crawl.IndexURL, http.StatusNotFound)
	_, err := download(t, a, files[0])
	wantAdapterError(t, err, sources.ErrCodeNetwork)
}

func TestReplayExtractLinks(t *testing.T) {
	d := ipaChapter3()
	rp := loadReplay(t, "crawl-ipa")
	index := rp.recorded(http.MethodGet, func(u string) bool { return u == d.Discover.Crawl.IndexURL })
	var page exchange
	for _, ex := range index {
		if ex.Range == "" {
			page = ex
		}
	}
	base, _ := url.Parse(d.Discover.Crawl.IndexURL)
	links := extractLinks(rp.body(page), base, d.Discover.Crawl.LinkPrefix)
	if len(links) == 0 {
		t.Fatal("no links extracted from the real index page")
	}
	seen := map[string]bool{}
	for _, l := range links {
		u, err := url.Parse(l)
		if err != nil {
			t.Fatalf("bad link %q: %v", l, err)
		}
		if u.Host != base.Host || !strings.HasPrefix(u.Path, d.Discover.Crawl.LinkPrefix) {
			t.Errorf("link %q is outside the crawl prefix", l)
		}
		if u.Fragment != "" || u.RawQuery != "" || u.Path == base.Path || seen[l] {
			t.Errorf("link %q should have been normalised or dropped", l)
		}
		seen[l] = true
	}
}

func TestReplayGetBodyRejectsOversizePage(t *testing.T) {
	d := ipaChapter3()
	a, rp := replayAdapter(t, "crawl-ipa", []dataset{d})
	var size int
	for _, ex := range rp.recorded(http.MethodGet, func(u string) bool { return u == d.Discover.Crawl.IndexURL }) {
		if ex.Range == "" {
			size = len(rp.body(ex))
		}
	}
	if _, err := a.getBody(context.Background(), d.Discover.Crawl.IndexURL, int64(size)); err != nil {
		t.Fatalf("a page exactly at the limit must be accepted: %v", err)
	}
	if _, err := a.getBody(context.Background(), d.Discover.Crawl.IndexURL, int64(size-1)); err == nil {
		t.Fatal("a page over the limit must be rejected, not truncated")
	}
}

func TestReplayTokenFetch(t *testing.T) {
	d := unitarySection12()
	a, rp := replayAdapter(t, "token-unitary", []dataset{d})
	del, files := editionAndFiles(t, a, "epo-up")
	data, err := download(t, a, files[0])
	if err != nil {
		t.Fatal(err)
	}
	entries := zipEntries(t, data)

	base := d.Discover.Token.BaseURL + "/" + del.ExternalID + "/"
	sections := rp.recorded(http.MethodGet, func(u string) bool {
		return strings.HasPrefix(u, base) && u != base+d.Discover.Token.SeedToken
	})
	if len(sections) == 0 {
		t.Fatal("capture has no section pages")
	}
	if len(entries) != len(sections) {
		t.Errorf("archive has %d entries, want %d", len(entries), len(sections))
	}
	for _, s := range sections {
		tok := strings.TrimPrefix(s.URL, base)
		if !bytes.Equal(entries[tok+".html"], rp.body(s)) {
			t.Errorf("section %s missing or different in the archive", tok)
		}
	}
}

func TestReplayTokenFetchFailsOnPageCap(t *testing.T) {
	d := unitarySection12()
	d.Discover.Token.MaxPages = 1
	a, _ := replayAdapter(t, "token-unitary", []dataset{d})
	_, files := editionAndFiles(t, a, "epo-up")
	_, err := download(t, a, files[0])
	wantAdapterError(t, err, sources.ErrCodeNetwork)
}

func TestReplayTokenFetchFailsWhenNoSectionArrives(t *testing.T) {
	d := unitarySection12()
	a, rp := replayAdapter(t, "token-unitary", []dataset{d})
	del, files := editionAndFiles(t, a, "epo-up")
	base := d.Discover.Token.BaseURL + "/" + del.ExternalID + "/"
	for _, s := range rp.recorded(http.MethodGet, func(u string) bool {
		return strings.HasPrefix(u, base) && u != base+d.Discover.Token.SeedToken
	}) {
		rp.failWith(s.URL, http.StatusNotFound)
	}
	_, err := download(t, a, files[0])
	wantAdapterError(t, err, sources.ErrCodeNetwork)
}

func TestReplayEUIPO(t *testing.T) {
	a, rp := replayAdapter(t, "euipo", []dataset{euipoCapped()})
	del, files := editionAndFiles(t, a, "euipo-tm")
	if !regexp.MustCompile(`^\d{8}$`).MatchString(del.ExternalID) {
		t.Errorf("EUIPO edition %q is not an entry-into-force date", del.ExternalID)
	}

	// The recording stopped at the node cap; the replay must fail the same way
	// rather than archive the partial tree.
	_, err := download(t, a, files[0])
	wantAdapterError(t, err, sources.ErrCodeNetwork)
	if err == nil || !strings.Contains(err.Error(), "cap") {
		t.Errorf("error %v should report the node cap", err)
	}

	// A failing content page aborts the walk.
	pages := rp.recorded(http.MethodGet, func(u string) bool { return strings.Contains(u, "/api/page/") })
	if len(pages) == 0 {
		t.Fatal("capture has no content pages")
	}
	rp.failWith(pages[0].URL, http.StatusServiceUnavailable)
	if _, err := download(t, a, files[0]); err == nil || strings.Contains(err.Error(), "cap") {
		t.Errorf("a failing page must abort the walk, got %v", err)
	}
}
