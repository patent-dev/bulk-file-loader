package refdata

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

// userAgent identifies the loader to upstream servers.
const userAgent = "bulk-file-loader (+https://github.com/patent-dev/bulk-file-loader)"

// resolveEdition discovers the current edition of a dataset and its publication
// date. Metadata access is bounded by metadataTimeout.
func (a *Adapter) resolveEdition(ctx context.Context, d *dataset) (string, time.Time, error) {
	mctx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()

	switch d.Discover.Kind {
	case discoverDateProbe:
		return a.resolveByDateProbe(mctx, d)
	case discoverPageLink:
		return a.resolveByPageLink(mctx, d)
	case discoverCrawl:
		return a.resolveCrawlEdition(mctx, d)
	case discoverJSONAPI:
		_, edition, err := a.resolveEUIPOPublication(mctx, d.Discover.API)
		return edition, time.Time{}, err
	case discoverTokenFetch:
		edition, err := a.resolveTokenEdition(mctx, d.Discover.Token)
		return edition, time.Time{}, err
	case discoverIndexListing:
		return a.resolveByIndexListing(mctx, d)
	default:
		return "", time.Time{}, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "unknown discovery kind", nil)
	}
}

// resolveByDateProbe walks dated editions newest-first and returns the first
// that exists.
func (a *Adapter) resolveByDateProbe(ctx context.Context, d *dataset) (string, time.Time, error) {
	pf := fileByRole(d, d.Discover.ProbeRole)
	if pf == nil {
		return "", time.Time{}, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "probe file not found for "+d.ID, nil)
	}

	for _, ed := range a.dateCandidates(d.Discover.Step, d.Discover.MaxBack) {
		ok, lastmod, err := a.probe(ctx, subst(pf.URLTemplate, ed))
		if err != nil {
			return "", time.Time{}, err
		}
		if ok {
			if lastmod.IsZero() {
				lastmod = editionDate(ed)
			}
			return ed, lastmod, nil
		}
	}
	return "", time.Time{}, sources.NewAdapterError(sources.ErrCodeNotFound, "no published edition found for "+d.ID, nil)
}

// resolveByIndexListing walks dated editions newest-first (annual, Jan 1) and
// returns the first whose MasterFiles directory index exists.
func (a *Adapter) resolveByIndexListing(ctx context.Context, d *dataset) (string, time.Time, error) {
	if d.Discover.Index == nil {
		return "", time.Time{}, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "missing index spec for "+d.ID, nil)
	}
	step := d.Discover.Step
	maxBack := d.Discover.MaxBack
	if maxBack == 0 {
		maxBack = 6
	}
	for _, ed := range a.dateCandidates(step, maxBack) {
		ok, lastmod, err := a.probe(ctx, subst(d.Discover.Index.DirURLTemplate, ed))
		if err != nil {
			return "", time.Time{}, err
		}
		if ok {
			if lastmod.IsZero() {
				lastmod = editionDate(ed)
			}
			return ed, lastmod, nil
		}
	}
	return "", time.Time{}, sources.NewAdapterError(sources.ErrCodeNotFound, "no published edition found for "+d.ID, nil)
}

// resolveByPageLink scrapes a landing page for the current edition string.
func (a *Adapter) resolveByPageLink(ctx context.Context, d *dataset) (string, time.Time, error) {
	body, err := a.getBody(ctx, d.Discover.PageURL, maxPageBytes)
	if err != nil {
		return "", time.Time{}, err
	}
	re, err := regexp.Compile(d.Discover.EditionRe)
	if err != nil {
		return "", time.Time{}, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "bad edition regexp for "+d.ID, err)
	}
	m := re.FindSubmatch(body)
	if len(m) < 2 {
		return "", time.Time{}, sources.NewAdapterError(sources.ErrCodeNotFound, "edition link not found on page for "+d.ID, nil)
	}
	return string(m[1]), time.Time{}, nil
}

// resolveCrawlEdition derives an edition for a crawled manual from the index
// page's Last-Modified header, falling back to the current month.
func (a *Adapter) resolveCrawlEdition(ctx context.Context, d *dataset) (string, time.Time, error) {
	// Prefer an edition scraped from the index page (e.g. MPEP's e9r-01-2024)
	// over the index's Last-Modified date.
	if re := d.Discover.Crawl.EditionRe; re != "" {
		body, err := a.getBody(ctx, d.Discover.Crawl.IndexURL, maxPageBytes)
		if err != nil {
			return "", time.Time{}, err
		}
		m := regexp.MustCompile(re).FindSubmatch(body)
		if len(m) < 2 {
			return "", time.Time{}, sources.NewAdapterError(sources.ErrCodeNotFound, "edition not found on index page for "+d.ID, nil)
		}
		return string(m[1]), time.Time{}, nil
	}

	ok, lastmod, err := a.probe(ctx, d.Discover.Crawl.IndexURL)
	if err != nil {
		return "", time.Time{}, err
	}
	if !ok {
		return "", time.Time{}, sources.NewAdapterError(sources.ErrCodeNotFound, "manual index not available for "+d.ID, nil)
	}
	if !lastmod.IsZero() {
		return lastmod.UTC().Format("20060102"), lastmod, nil
	}
	return a.now().UTC().Format("200601"), time.Time{}, nil
}

// dateCandidates lists edition tokens newest-first for the given cadence.
func (a *Adapter) dateCandidates(step stepKind, maxBack int) []string {
	now := a.now().UTC()
	out := make([]string, 0, maxBack)
	switch step {
	case stepMonthly:
		y, m := now.Year(), int(now.Month())
		for range maxBack {
			out = append(out, fmt.Sprintf("%04d%02d", y, m))
			if m--; m == 0 {
				m, y = 12, y-1
			}
		}
	case stepYearlyJan1:
		y := now.Year()
		for range maxBack {
			out = append(out, fmt.Sprintf("%04d0101", y))
			y--
		}
	}
	return out
}

// probe issues a single-byte ranged GET to test whether a URL exists without
// downloading it, returning the parsed Last-Modified time when present. A 200 or
// 206 means it exists; 404/410 mean it genuinely does not. Other 2xx statuses
// (a WAF's 202 challenge) prove nothing and are surfaced as errors. A 403/401 is NOT
// treated as "absent": WAFs (EPO/WIPO/CPC) return 403 to bots, and silently
// bucketing that as not-found makes date-probe discovery walk back to a stale
// edition with no error. Such statuses, and anything unexpected, are surfaced.
func (a *Adapter) probe(ctx context.Context, url string) (bool, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, time.Time{}, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "bad URL: "+url, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Range", "bytes=0-0")

	resp, err := a.client.Do(req)
	if err != nil {
		return false, time.Time{}, sources.NewAdapterError(sources.ErrCodeNetwork, "probe failed for "+url, err)
	}
	defer drainClose(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return true, parseLastModified(resp.Header.Get("Last-Modified")), nil
	case http.StatusNotFound, http.StatusGone:
		return false, time.Time{}, nil
	case http.StatusForbidden, http.StatusUnauthorized:
		return false, time.Time{}, sources.NewAdapterError(sources.ErrCodeAuth,
			fmt.Sprintf("access denied (%d) probing %s (likely a WAF block); not treating as a missing edition", resp.StatusCode, url), nil)
	default:
		return false, time.Time{}, sources.NewAdapterError(sources.ErrCodeNetwork,
			fmt.Sprintf("unexpected status %d probing %s", resp.StatusCode, url), nil)
	}
}

// fileByRole returns the dataset file with the given role, or the first file
// when role is empty.
func fileByRole(d *dataset, role string) *datasetFile {
	if role == "" {
		if len(d.Files) == 0 {
			return nil
		}
		return &d.Files[0]
	}
	for i := range d.Files {
		if d.Files[i].Role == role {
			return &d.Files[i]
		}
	}
	return nil
}

// editionDate parses a YYYYMM or YYYYMMDD edition token into a time.
func editionDate(ed string) time.Time {
	switch len(ed) {
	case 6:
		if t, err := time.Parse("200601", ed); err == nil {
			return t
		}
	case 8:
		if t, err := time.Parse("20060102", ed); err == nil {
			return t
		}
	}
	return time.Time{}
}

// parseLastModified parses an HTTP Last-Modified header, returning the zero
// time when absent or malformed.
func parseLastModified(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	if t, err := http.ParseTime(v); err == nil {
		return t
	}
	return time.Time{}
}
