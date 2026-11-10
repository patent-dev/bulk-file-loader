package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/core"
	"github.com/patent-dev/bulk-file-loader/internal/service"
)

// syncSummary prints sync progress on stderr, one line per product, and
// totals the outcome.
type syncSummary struct {
	start                             time.Time
	products, files, newFiles, failed int
}

func newSyncSummary() *syncSummary { return &syncSummary{start: time.Now()} }

func (s *syncSummary) progress(ev service.SyncEvent) {
	s.products++
	s.files += ev.Files
	s.newFiles += ev.NewFiles
	if ev.Err != nil {
		s.failed++
	}
	if flagQuiet {
		return
	}
	if ev.Err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "  %s: %d files (%d new), incomplete: %v\n", ev.ProductName, ev.Files, ev.NewFiles, ev.Err)
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "  %s: %d files (%d new)\n", ev.ProductName, ev.Files, ev.NewFiles)
}

func (s *syncSummary) print() {
	if flagQuiet {
		return
	}
	line := fmt.Sprintf("Synced %d products, %d files (%d new) in %s", s.products, s.files, s.newFiles, time.Since(s.start).Round(time.Second))
	if s.failed > 0 {
		line += fmt.Sprintf("; %d products incomplete", s.failed)
	}
	_, _ = fmt.Fprintln(os.Stderr, line)
}

// syncSource refreshes a source's product list and file catalogue.
func syncSource(ctx context.Context, c *core.Core, sourceID string, sum *syncSummary) error {
	if !flagQuiet {
		_, _ = fmt.Fprintf(os.Stderr, "Syncing source %s...\n", sourceID)
	}
	if err := c.Service.SyncSourceProducts(sourceID); err != nil {
		return fmt.Errorf("failed to sync products for source %s: %w", sourceID, err)
	}
	// Per-product failures are counted in sum and printed as they happen.
	// An error before any product was synced (cancelled, database) aborts.
	before := sum.products
	if err := c.Service.SyncSourceFiles(ctx, sourceID, sum.progress); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if sum.products == before {
			return fmt.Errorf("failed to sync files for source %s: %w", sourceID, err)
		}
	}
	return nil
}

// result turns the summary into the command's outcome: nil when every
// product synced, partialError (exit 3) when some did, and an error when
// none did.
func (s *syncSummary) result() error {
	switch {
	case s.failed == 0:
		return nil
	case s.failed < s.products:
		return partialError{}
	default:
		return fmt.Errorf("sync failed for all %d products", s.products)
	}
}
