package refdata

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

const (
	// maxPageBytes caps the size of an HTML page read during discovery or
	// crawling. Landing pages and manual sections are well under this.
	maxPageBytes = 16 << 20 // 16 MiB
)

// hrefRe extracts href attribute values from HTML, tolerating single or double
// quotes. Using a regexp avoids a dependency on golang.org/x/net/html.
// Fragments are kept in the capture and stripped later via URL parsing.
var hrefRe = regexp.MustCompile(`(?i)href=["']([^"']+)["']`)

// downloadURL streams a single file into dst, reporting progress against the
// Content-Length when the server provides it.
func (a *Adapter) downloadURL(ctx context.Context, fileURL string, dst io.Writer, progress sources.ProgressFunc) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return sources.NewAdapterError(sources.ErrCodeInvalidConfig, "bad URL: "+fileURL, err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return sources.NewAdapterError(sources.ErrCodeNetwork, "download failed for "+fileURL, err)
	}
	defer drainClose(resp.Body)

	if err := statusError(resp.StatusCode, fileURL); err != nil {
		return err
	}

	pw := &progressWriter{dst: dst, total: resp.ContentLength, fn: progress}
	if _, err := io.Copy(pw, resp.Body); err != nil {
		return sources.NewAdapterError(sources.ErrCodeNetwork, "download interrupted for "+fileURL, err)
	}
	// Integrity: when the server declared a length, the body must match it. A
	// truncated transfer that ends in a clean close otherwise passes silently and
	// a partial zip is archived as if complete.
	if resp.ContentLength >= 0 && pw.written != resp.ContentLength {
		return sources.NewAdapterError(sources.ErrCodeNetwork,
			fmt.Sprintf("truncated download for %s: got %d of %d bytes", fileURL, pw.written, resp.ContentLength), nil)
	}
	return nil
}

const (
	// crawlDelay is a courtesy pause between page fetches so a manual crawl does
	// not hammer the source server (and risk being blocked).
	crawlDelay = 100 * time.Millisecond
	// perPageTimeout bounds a single page fetch so one slow/hung page cannot
	// stall the whole crawl up to the outer download timeout.
	perPageTimeout = 60 * time.Second
)

// crawl breadth-first fetches an HTML manual starting at the index, following
// links within LinkPrefix (so sections reachable only via deeper pages are
// included, not just those on the index), and writes each page as an entry in a
// ZIP streamed to dst. Broken pages are skipped and counted; if the index
// yields nothing the crawl fails rather than producing an empty archive.
func (a *Adapter) crawl(ctx context.Context, spec *crawlSpec, dst io.Writer, progress sources.ProgressFunc) error {
	if spec == nil {
		return sources.NewAdapterError(sources.ErrCodeInvalidConfig, "missing crawl spec", nil)
	}
	base, err := url.Parse(spec.IndexURL)
	if err != nil {
		return sources.NewAdapterError(sources.ErrCodeInvalidConfig, "bad index URL: "+spec.IndexURL, err)
	}
	var skip *regexp.Regexp
	if spec.SkipRe != "" {
		if skip, err = regexp.Compile(spec.SkipRe); err != nil {
			return sources.NewAdapterError(sources.ErrCodeInvalidConfig, "bad crawl skip regexp for "+spec.IndexURL, err)
		}
	}

	// Progress is the archive bytes written; the final size is unknown up front.
	zw := zip.NewWriter(&progressWriter{dst: dst, total: -1, fn: progress})
	writePage := func(pageURL string, body []byte) error {
		w, err := zw.Create(zipEntryName(pageURL))
		if err != nil {
			return err
		}
		_, err = w.Write(body)
		return err
	}

	queue := []string{spec.IndexURL}
	seen := map[string]struct{}{spec.IndexURL: {}}
	var fetched, skipped int
	truncated := false

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			_ = zw.Close()
			return err
		}
		pageURL := queue[0]
		queue = queue[1:]

		body, err := a.getBodyTimed(ctx, pageURL, maxPageBytes)
		if err != nil {
			// A single broken page must not abort the whole manual.
			skipped++
			slog.Warn("crawl: skipping unreachable page", "source", a.id, "url", pageURL, "error", err)
			continue
		}
		if err := writePage(pageURL, body); err != nil {
			_ = zw.Close()
			return sources.NewAdapterError(sources.ErrCodeNetwork, "failed writing page to archive", err)
		}
		fetched++

		// Resolve this page's relative links against the page's own URL (not the
		// fixed index URL): pages can live in a different directory than the
		// index (e.g. EPO sections under /2026/).
		pageBase := base
		if pb, perr := url.Parse(pageURL); perr == nil {
			pageBase = pb
		}
		for _, link := range extractLinks(body, pageBase, spec.LinkPrefix) {
			if _, ok := seen[link]; ok {
				continue
			}
			if skip != nil && skip.MatchString(link) {
				continue // archived editions, binaries, assets, …
			}
			if spec.MaxPages > 0 && len(seen) >= spec.MaxPages {
				truncated = true
				break
			}
			seen[link] = struct{}{}
			queue = append(queue, link)
		}
		sleepCtx(ctx, crawlDelay)
	}

	if fetched == 0 {
		_ = zw.Close()
		return sources.NewAdapterError(sources.ErrCodeNetwork, "crawl produced no pages from "+spec.IndexURL, nil)
	}
	// A partial crawl must fail loudly rather than ship an incomplete manual as
	// if complete. The page caps are set above each manual's real size, so
	// hitting one means the manual grew unexpectedly (or a link explosion), not
	// something to silently truncate. Likewise a large fraction of unreachable
	// pages signals systematic breakage (WAF, moved section), not the odd
	// transient 404.
	if truncated {
		_ = zw.Close()
		return sources.NewAdapterError(sources.ErrCodeNetwork,
			fmt.Sprintf("crawl hit the %d-page cap for %s (fetched %d): manual is incomplete", spec.MaxPages, spec.IndexURL, fetched), nil)
	}
	if skipped > maxSkippedPages(fetched) {
		_ = zw.Close()
		return sources.NewAdapterError(sources.ErrCodeNetwork,
			fmt.Sprintf("crawl skipped %d of %d pages for %s: manual likely incomplete", skipped, fetched+skipped, spec.IndexURL), nil)
	}
	if skipped > 0 {
		slog.Info("crawl: completed with a few skipped pages", "source", a.id, "fetched", fetched, "skipped", skipped)
	}

	if err := zw.Close(); err != nil {
		return sources.NewAdapterError(sources.ErrCodeNetwork, "failed finalizing archive", err)
	}
	return nil
}

// maxSkippedPages is the tolerated number of unreachable pages in a crawl: a few
// transient failures are fine, but more than 5% (and at least 5) means the
// manual is probably being shipped with holes.
func maxSkippedPages(fetched int) int {
	if n := fetched / 20; n > 5 {
		return n
	}
	return 5
}

// getBodyTimed fetches a page under a per-page timeout derived from ctx.
func (a *Adapter) getBodyTimed(ctx context.Context, pageURL string, limit int64) ([]byte, error) {
	pctx, cancel := context.WithTimeout(ctx, perPageTimeout)
	defer cancel()
	return a.getBody(pctx, pageURL, limit)
}

// sleepCtx pauses for d, or returns early if ctx is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// getBody fetches a URL and returns its body. A body larger than limit is an
// error rather than silently truncated, so a cut-off page is never archived.
func (a *Adapter) getBody(ctx context.Context, pageURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "bad URL: "+pageURL, err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNetwork, "request failed for "+pageURL, err)
	}
	defer drainClose(resp.Body)

	if err := statusError(resp.StatusCode, pageURL); err != nil {
		return nil, err
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNetwork, "read failed for "+pageURL, err)
	}
	if int64(len(body)) > limit {
		return nil, sources.NewAdapterError(sources.ErrCodeNetwork,
			fmt.Sprintf("page %s exceeds the %d-byte limit", pageURL, limit), nil)
	}
	return body, nil
}

// extractLinks returns absolute, deduplicated URLs from HTML whose path begins
// with prefix and that share the index host.
func extractLinks(body []byte, base *url.URL, prefix string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 64)
	for _, m := range hrefRe.FindAllSubmatch(body, -1) {
		ref, err := url.Parse(strings.TrimSpace(string(m[1])))
		if err != nil {
			continue
		}
		abs := base.ResolveReference(ref)
		abs.Fragment = ""
		abs.RawQuery = ""
		if abs.Host != base.Host || !strings.HasPrefix(abs.Path, prefix) {
			continue
		}
		// Skip the index path itself; it is added separately.
		if abs.Path == base.Path {
			continue
		}
		key := abs.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

// zipEntryName converts a page URL into a stable, safe archive entry name.
func zipEntryName(pageURL string) string {
	u, err := url.Parse(pageURL)
	name := ""
	if err == nil {
		name = strings.TrimPrefix(u.Path, "/")
	}
	if name == "" || strings.HasSuffix(name, "/") {
		name += "index"
	}
	// Defend against path traversal and keep entries within the archive root.
	name = strings.ReplaceAll(name, "..", "_")
	if !strings.HasSuffix(strings.ToLower(name), ".html") && !strings.HasSuffix(strings.ToLower(name), ".htm") {
		name += ".html"
	}
	return name
}

// statusError maps an HTTP status to an adapter error, or nil for 200 and 206.
// Other 2xx statuses carry no usable content: WAFs answer bot traffic with a 202
// challenge page, which must not be archived as the real document.
func statusError(status int, target string) error {
	switch status {
	case http.StatusOK, http.StatusPartialContent:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return sources.NewAdapterError(sources.ErrCodeAuth, fmt.Sprintf("access denied (%d) for %s", status, target), nil)
	case http.StatusNotFound, http.StatusGone:
		return sources.NewAdapterError(sources.ErrCodeNotFound, fmt.Sprintf("not found (%d): %s", status, target), nil)
	case http.StatusTooManyRequests:
		return sources.NewAdapterError(sources.ErrCodeRateLimit, "rate limited by "+target, nil)
	default:
		return sources.NewAdapterError(sources.ErrCodeNetwork, fmt.Sprintf("unexpected status %d for %s", status, target), nil)
	}
}

// drainClose discards any remaining body and closes it so the connection can be
// reused.
func drainClose(rc io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, 4<<10))
	_ = rc.Close()
}

// progressWriter forwards byte counts to a progress callback as data is written.
type progressWriter struct {
	dst     io.Writer
	total   int64
	written int64
	fn      sources.ProgressFunc
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.dst.Write(p)
	pw.written += int64(n)
	if pw.fn != nil {
		// total < 0 means unknown length; report 0 like other adapters.
		pw.fn(pw.written, max(pw.total, 0))
	}
	return n, err
}
