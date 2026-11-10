package cli

import (
	"testing"
	"time"
)

func TestTransferSummary(t *testing.T) {
	got := transferSummary(1536*1024*1024, 2*time.Minute)
	if want := "1.5 GB in 2m0s, 12.8 MB/s"; got != want {
		t.Errorf("transferSummary = %q, want %q", got, want)
	}
	if got := transferSummary(0, 0); got != "0 B in 0s, 0 B/s" {
		t.Errorf("empty transfer = %q", got)
	}
}

func TestSyncSummaryResult(t *testing.T) {
	if err := (&syncSummary{products: 3}).result(); err != nil {
		t.Errorf("all synced = %v, want nil", err)
	}
	if err := (&syncSummary{products: 3, failed: 1}).result(); ExitCode(err) != 3 {
		t.Errorf("some failed = exit %d, want 3", ExitCode(err))
	}
	if err := (&syncSummary{products: 3, failed: 3}).result(); ExitCode(err) != 1 {
		t.Errorf("all failed = exit %d, want 1", ExitCode(err))
	}
}
