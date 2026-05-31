package refdata

import (
	"archive/zip"
	"context"
	"io"
	"log/slog"
	"regexp"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

// The EPO Unitary Patent Guidelines are a Next.js app: the table of contents is
// rendered client-side, but each section's content page
// (/{year}/section_N_M_K) is server-rendered HTML. The full set of section
// tokens is embedded in the seed page's data, so we scrape those tokens and
// fetch each section page into a ZIP (one file per token).

// resolveTokenEdition scrapes the current edition year from the landing page.
func (a *Adapter) resolveTokenEdition(ctx context.Context, spec *tokenSpec) (string, error) {
	body, err := a.getBody(ctx, spec.BaseURL, maxPageBytes)
	if err != nil {
		return "", err
	}
	re, err := regexp.Compile(spec.YearRe)
	if err != nil {
		return "", sources.NewAdapterError(sources.ErrCodeInvalidConfig, "bad year regexp", err)
	}
	m := re.FindSubmatch(body)
	if len(m) < 2 {
		return "", sources.NewAdapterError(sources.ErrCodeNotFound, "edition year not found on "+spec.BaseURL, nil)
	}
	return string(m[1]), nil
}

// fetchTokenPages resolves the year, reads the section tokens from the seed
// page, then fetches each section's HTML page into the ZIP at dst.
func (a *Adapter) fetchTokenPages(ctx context.Context, spec *tokenSpec, dst io.Writer, progress sources.ProgressFunc) error {
	year, err := a.resolveTokenEdition(ctx, spec)
	if err != nil {
		return err
	}
	base := spec.BaseURL + "/" + year + "/"

	seed, err := a.getBodyTimed(ctx, base+spec.SeedToken, maxPageBytes)
	if err != nil {
		return err
	}
	tokenRe, err := regexp.Compile(spec.TokenRe)
	if err != nil {
		return sources.NewAdapterError(sources.ErrCodeInvalidConfig, "bad token regexp", err)
	}
	tokens := dedupeStrings(tokenRe.FindAllString(string(seed), -1))
	if len(tokens) == 0 {
		return sources.NewAdapterError(sources.ErrCodeNotFound, "no section tokens found on seed page", nil)
	}

	zw := zip.NewWriter(dst)
	var fetched, skipped int
	for i, tok := range tokens {
		if i >= spec.MaxPages {
			break
		}
		if err := ctx.Err(); err != nil {
			_ = zw.Close()
			return err
		}
		body, err := a.getBodyTimed(ctx, base+tok, maxPageBytes)
		if err != nil {
			// Tolerate an individual unreachable page rather than failing the
			// whole manual, matching the HTML crawler.
			skipped++
			slog.Warn("token-fetch: skipping unreachable section", "source", a.id, "token", tok, "error", err)
			continue
		}
		if err := zipWriteBytes(zw, tok+".html", body); err != nil {
			_ = zw.Close()
			return err
		}
		fetched++
		report(progress, int64(i+1), int64(len(tokens)))
		sleepCtx(ctx, crawlDelay)
	}
	if fetched == 0 {
		_ = zw.Close()
		return sources.NewAdapterError(sources.ErrCodeNetwork, "no section pages fetched", nil)
	}
	if skipped > 0 {
		slog.Info("token-fetch: completed with skipped sections", "source", a.id, "fetched", fetched, "skipped", skipped)
	}
	if err := zw.Close(); err != nil {
		return sources.NewAdapterError(sources.ErrCodeNetwork, "finalize Unitary archive", err)
	}
	return nil
}

// dedupeStrings returns the input with duplicates removed, preserving order.
func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
