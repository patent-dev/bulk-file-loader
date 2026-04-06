package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/downloader"
)

type downloadItem struct {
	ID   string
	Name string
}

func runDownloads(dl *downloader.Downloader, files []downloadItem) error {
	ctx := context.Background()
	var failed, succeeded int
	for _, f := range files {
		if !flagQuiet {
			_, _ = fmt.Fprintf(os.Stderr, "Downloading: %s (%s)\n", f.Name, f.ID)
		}

		stop := showProgress(dl, f.ID)
		dlErr := dl.Download(ctx, f.ID)
		stop()

		if dlErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Error: %s: %v\n", f.ID, dlErr)
			failed++
			continue
		}

		succeeded++
		if flagQuiet {
			fmt.Println(f.ID)
		} else {
			_, _ = fmt.Fprintf(os.Stderr, "Done: %s\n", f.Name)
		}
	}

	if failed > 0 && succeeded > 0 {
		return partialError{}
	}
	if failed > 0 {
		return fmt.Errorf("download failed")
	}
	return nil
}

func showProgress(dl *downloader.Downloader, fileID string) func() {
	if flagQuiet {
		return func() {}
	}

	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				clearProgressLine()
				return
			case <-ticker.C:
				p := dl.GetProgress(fileID)
				if p == nil {
					continue
				}
				printProgress(p)
			}
		}
	}()

	return func() {
		close(done)
		<-exited
	}
}

func printProgress(p *downloader.DownloadProgress) {
	if p.TotalBytes > 0 {
		pct := p.BytesWritten * 100 / p.TotalBytes
		_, _ = fmt.Fprintf(os.Stderr, "\r  %s / %s  %d%%  %s/s",
			formatBytes(p.BytesWritten),
			formatBytes(p.TotalBytes),
			pct,
			formatBytes(int64(p.Speed)),
		)
	} else if p.BytesWritten > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "\r  %s downloaded  %s/s",
			formatBytes(p.BytesWritten),
			formatBytes(int64(p.Speed)),
		)
	}
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
