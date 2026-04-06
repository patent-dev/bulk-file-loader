package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn while capturing os.Stdout and returns the output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w

	fn()

	_ = w.Close()
	os.Stdout = orig

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	return buf.String()
}

func TestFormatOutputTable(t *testing.T) {
	flagFormat = "table"
	headers := []string{"ID", "Name", "Status"}
	rows := [][]string{
		{"1", "alpha", "ok"},
		{"2", "beta", "fail"},
	}

	output := captureStdout(t, func() {
		if err := formatOutput(headers, rows, nil); err != nil {
			t.Fatal(err)
		}
	})

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 3 { // header + 2 rows
		t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), output)
	}

	if !strings.Contains(lines[0], "ID") || !strings.Contains(lines[0], "Name") {
		t.Errorf("header line missing expected columns: %s", lines[0])
	}

	if !strings.Contains(lines[1], "alpha") {
		t.Errorf("expected first data row to contain 'alpha', got: %s", lines[1])
	}
}

func TestFormatOutputJSON(t *testing.T) {
	flagFormat = "json"
	headers := []string{"ID", "Name"}
	rows := [][]string{{"1", "test"}}
	jsonData := []map[string]string{{"id": "1", "name": "test"}}

	output := captureStdout(t, func() {
		if err := formatOutput(headers, rows, jsonData); err != nil {
			t.Fatal(err)
		}
	})

	var parsed []map[string]string
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}
	if len(parsed) != 1 || parsed[0]["id"] != "1" {
		t.Errorf("unexpected JSON content: %v", parsed)
	}
}

func TestFormatOutputCSV(t *testing.T) {
	flagFormat = "csv"
	headers := []string{"ID", "Name", "Status"}
	rows := [][]string{
		{"1", "alpha", "ok"},
		{"2", "beta", "fail"},
	}

	output := captureStdout(t, func() {
		if err := formatOutput(headers, rows, nil); err != nil {
			t.Fatal(err)
		}
	})

	reader := csv.NewReader(strings.NewReader(output))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("failed to parse CSV: %v", err)
	}

	if len(records) != 3 { // header + 2 data rows
		t.Fatalf("expected 3 CSV records, got %d", len(records))
	}

	if records[0][0] != "ID" || records[0][1] != "Name" || records[0][2] != "Status" {
		t.Errorf("unexpected CSV headers: %v", records[0])
	}
	if records[1][0] != "1" || records[1][1] != "alpha" {
		t.Errorf("unexpected first data row: %v", records[1])
	}
}

func TestFormatOutputInvalidFormat(t *testing.T) {
	flagFormat = "yaml"
	err := formatOutput([]string{"A"}, [][]string{{"1"}}, nil)
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
	if !strings.Contains(err.Error(), "unknown output format") {
		t.Errorf("expected 'unknown output format' in error, got: %v", err)
	}
}
