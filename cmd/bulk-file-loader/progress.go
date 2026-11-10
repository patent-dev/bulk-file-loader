package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/core"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
	"github.com/patent-dev/bulk-file-loader/internal/service"
	"golang.org/x/term"
)

type downloadItem struct {
	ID   string
	Name string
}

// progressInterval is how often progress is printed when stderr is not a
// terminal (a log file, a pipe, docker logs).
const progressInterval = 30 * time.Second

// stderrIsTerminal reports whether progress can be redrawn in place.
var stderrIsTerminal = term.IsTerminal(int(os.Stderr.Fd()))

// downloadWait makes download commands follow a running server's progress
// until their files are done, instead of returning once they are queued.
var downloadWait bool

// errInterrupted is returned when the user interrupts a download command.
var errInterrupted = errors.New("interrupted: unfinished files stay queued for a server or the next download command")

// runDownloads queues the files. With a running server it returns (or, with
// --wait, follows them); otherwise it runs workers over these files itself.
// The error reflects the end state: nil when all files were downloaded,
// partialError when some were not.
func runDownloads(ctx context.Context, c *core.Core, files []downloadItem) error {
	if len(files) == 0 {
		return nil
	}
	names := make(map[string]string, len(files))
	ids := make([]string, len(files))
	for i, f := range files {
		ids[i], names[f.ID] = f.ID, f.Name
	}
	if _, err := c.Downloader.Enqueue(ids, "cli"); err != nil {
		return err
	}

	if srv := c.Service.Server(); srv != nil {
		if !flagQuiet {
			_, _ = fmt.Fprintf(os.Stderr, "Queued %s; the running server (%s) downloads them. Follow with 'bulk-file-loader status'.\n", plural(len(ids), "file"), srv.Owner)
		}
		if !downloadWait {
			return nil
		}
		return followServer(ctx, c, ids, names)
	}
	return downloadHere(ctx, c, ids, names)
}

// downloadHere runs this process's workers over the files.
func downloadHere(ctx context.Context, c *core.Core, ids []string, names map[string]string) error {
	if !flagQuiet {
		_, _ = fmt.Fprintf(os.Stderr, "Downloading %s, %d at a time\n", plural(len(ids), "file"), c.Config.MaxConcurrent)
	}
	c.Downloader.OnAttempt = func(r downloader.AttemptResult) { printAttempt(c, names[r.FileID], r) }
	stop := showProgress(c.Downloader)
	c.Downloader.RunWorkers(ctx, service.Owner(), ids)
	stop()
	if ctx.Err() != nil {
		return errInterrupted
	}
	return batchOutcome(c, ids)
}

// printAttempt prints the outcome of one attempt.
func printAttempt(c *core.Core, name string, r downloader.AttemptResult) {
	if r.Outcome == downloader.OutcomeDone && flagQuiet {
		fmt.Println(r.FileID)
		return
	}
	var size int64
	if file, err := c.Service.GetFile(r.FileID); err == nil {
		size = deref(file.FileSize, 0)
		name = file.FileName
	}
	switch r.Outcome {
	case downloader.OutcomeDone:
		_, _ = fmt.Fprintf(os.Stderr, "Done: %s (%s)\n", name, transferSummary(size, r.Duration))
	case downloader.OutcomeRetrying:
		_, _ = fmt.Fprintf(os.Stderr, "Retrying: %s (attempt %d of %d failed, next at %s): %v\n",
			name, r.Attempt, r.MaxAttempts, r.NextAttemptAt.Format(time.TimeOnly), r.Err)
	case downloader.OutcomeFailed:
		_, _ = fmt.Fprintf(os.Stderr, "Failed: %s after %s: %v\n", name, plural(r.Attempt, "attempt"), r.Err)
	case downloader.OutcomeCancelled:
		_, _ = fmt.Fprintf(os.Stderr, "Cancelled: %s\n", name)
	}
}

// followServer waits until the server has finished the files, printing the
// queue state periodically. If the server stops meanwhile, this process
// downloads the rest.
func followServer(ctx context.Context, c *core.Core, ids []string, names map[string]string) error {
	printEvery := 2 * time.Second
	if !stderrIsTerminal {
		printEvery = progressInterval
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	var lastPrint time.Time
	for {
		active, err := c.Downloader.ActiveCount(ids)
		if err != nil {
			return err
		}
		if active == 0 {
			return batchOutcome(c, ids)
		}
		if c.Service.Server() == nil {
			_, _ = fmt.Fprintln(os.Stderr, "The server stopped; downloading the remaining files here.")
			return downloadHere(ctx, c, ids, names)
		}
		if !flagQuiet && time.Since(lastPrint) >= printEvery {
			_, _ = fmt.Fprintf(os.Stderr, "  %d of %d files still queued or downloading\n", active, len(ids))
			lastPrint = time.Now()
		}
		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintln(os.Stderr, "Stopped following; the server keeps downloading.")
			return errInterrupted
		case <-t.C:
		}
	}
}

// batchOutcome turns the files' final states into the command's result.
// Files neither downloaded nor given up (still queued, cancelled) count as
// not done.
func batchOutcome(c *core.Core, ids []string) error {
	statuses, err := c.Service.FileStatuses(ids)
	if err != nil {
		return err
	}
	var done, failed, unfinished int
	for _, id := range ids {
		switch statuses[id] {
		case "downloaded":
			done++
		case "failed":
			failed++
		default:
			unfinished++
		}
	}
	if !flagQuiet {
		line := fmt.Sprintf("%d downloaded, %d failed of %d", done, failed, len(ids))
		if unfinished > 0 {
			line += fmt.Sprintf(", %d not finished (cancelled or still queued)", unfinished)
		}
		_, _ = fmt.Fprintln(os.Stderr, line)
	}
	switch {
	case done == len(ids):
		return nil
	case done > 0:
		return partialError{}
	default:
		return fmt.Errorf("download failed")
	}
}

// transferSummary describes a finished transfer: size, duration and rate.
func transferSummary(size int64, d time.Duration) string {
	d = max(d, time.Millisecond)
	rate := int64(float64(size) / d.Seconds())
	return fmt.Sprintf("%s in %s, %s/s", formatBytes(size), d.Round(time.Second), formatBytes(rate))
}

// showProgress prints the active downloads of this process: redrawn in
// place on a terminal when one file is running, otherwise one line per file
// at most every progressInterval.
func showProgress(dl *downloader.Downloader) func() {
	if flagQuiet {
		return func() {}
	}
	interval := progressInterval
	inPlace := stderrIsTerminal && dl.MaxConcurrent() == 1
	if stderrIsTerminal {
		interval = time.Second
	}
	if inPlace {
		interval = 500 * time.Millisecond
	}

	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				if inPlace {
					clearProgressLine()
				}
				return
			case <-ticker.C:
				for _, p := range dl.ActiveDownloads() {
					if inPlace {
						_, _ = fmt.Fprintf(os.Stderr, "\r  %s", progressText(&p))
					} else {
						_, _ = fmt.Fprintf(os.Stderr, "  %s: %s\n", p.FileName, progressText(&p))
					}
				}
			}
		}
	}()

	return func() {
		close(done)
		<-exited
	}
}

func progressText(p *downloader.DownloadProgress) string {
	if p.TotalBytes > 0 {
		pct := p.BytesWritten * 100 / p.TotalBytes
		return fmt.Sprintf("%s / %s  %d%%  %s/s",
			formatBytes(p.BytesWritten), formatBytes(p.TotalBytes), pct, formatBytes(int64(p.Speed)))
	}
	return fmt.Sprintf("%s downloaded  %s/s", formatBytes(p.BytesWritten), formatBytes(int64(p.Speed)))
}

func clearProgressLine() {
	_, _ = fmt.Fprintf(os.Stderr, "\r%80s\r", "")
}

func formatBytes(b int64) string {
	if b == 0 {
		return "0 B"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	units := "KMGTPE"
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < len(units)-1; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), units[exp])
}

// plural formats a count with a singular or plural noun.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
