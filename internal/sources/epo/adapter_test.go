package epo

import "testing"

func TestParseFileSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		// EPO API returns lowercase "kB"
		{"450.1 kB", 460902},
		{"1.5 GB", 1610612736},
		{"16 MB", 16777216},
		{"318.5 MB", 333971456},
		// Uppercase should also work
		{"500 KB", 512000},
		{"2 TB", 2199023255552},
		// Edge cases
		{"100 B", 100},
		{"0 B", 0},
		{"", 0},
		{"invalid", 0},
	}

	for _, tt := range tests {
		got := parseFileSize(tt.input)
		if got != tt.want {
			t.Errorf("parseFileSize(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestParsFileSizeCaseInsensitive(t *testing.T) {
	// The key fix: EPO returns "kB" not "KB", "Mb" etc.
	// All variations should work.
	kb := parseFileSize("100 kB")
	if kb == 0 {
		t.Error("parseFileSize(\"100 kB\") returned 0, lowercase kB must be recognized")
	}

	gb := parseFileSize("1.5 Gb")
	if gb == 0 {
		t.Error("parseFileSize(\"1.5 Gb\") returned 0, mixed case must be recognized")
	}
}
