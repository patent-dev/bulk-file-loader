package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/core"
	"github.com/patent-dev/bulk-file-loader/internal/service"
	"github.com/spf13/cobra"
)

var (
	fileReleasedBefore string
	fileReleasedAfter  string
	fileNameGlob       string
	fileDryRun         bool
)

// addFileFilterFlags adds the flags that select files by attribute.
func addFileFilterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&fileProductFilter, "product", "", "filter by product ID")
	cmd.Flags().StringVar(&fileSourceFilter, "source", "", "filter by source ID")
	cmd.Flags().StringVar(&fileStatusFilter, "status", "", "filter by status (available, downloading, downloaded, failed, skipped, cancelled, deleted)")
	cmd.Flags().StringVar(&fileReleasedBefore, "released-before", "", "only files released before this date (YYYY-MM-DD or RFC 3339)")
	cmd.Flags().StringVar(&fileReleasedAfter, "released-after", "", "only files released on or after this date (YYYY-MM-DD or RFC 3339)")
	cmd.Flags().StringVar(&fileNameGlob, "name-glob", "", `only files whose name matches this pattern, e.g. "*_backfile_*.zip"`)
}

// fileFilterFromFlags builds the filter from the flags and reports whether
// any filter was given.
func fileFilterFromFlags() (service.FileFilter, bool, error) {
	f := service.FileFilter{
		SourceID:  fileSourceFilter,
		ProductID: fileProductFilter,
		Status:    fileStatusFilter,
		NameGlob:  fileNameGlob,
	}
	var err error
	if f.ReleasedBefore, err = parseDateFlag("--released-before", fileReleasedBefore); err != nil {
		return f, false, err
	}
	if f.ReleasedAfter, err = parseDateFlag("--released-after", fileReleasedAfter); err != nil {
		return f, false, err
	}
	set := f.SourceID != "" || f.ProductID != "" || f.Status != "" || f.NameGlob != "" ||
		f.ReleasedBefore != nil || f.ReleasedAfter != nil
	return f, set, nil
}

func parseDateFlag(name, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	for _, layout := range []string{time.DateOnly, time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("%s: %q is not a date (use YYYY-MM-DD or RFC 3339)", name, v)
}

// selectFiles returns the files a bulk command acts on: the IDs given, or
// every file matching the filter flags. exclude drops files in these states
// when they were selected by filter without an explicit --status.
func selectFiles(c *core.Core, ids []string, exclude ...string) ([]generated.File, error) {
	f, filtered, err := fileFilterFromFlags()
	if err != nil {
		return nil, err
	}
	switch {
	case filtered && len(ids) > 0:
		return nil, fmt.Errorf("give file IDs or filters, not both")
	case !filtered && len(ids) == 0:
		return nil, fmt.Errorf("give file IDs or at least one filter (--product, --source, --status, --released-before, --released-after, --name-glob)")
	case !filtered:
		files := make([]generated.File, len(ids))
		for i, id := range ids {
			files[i] = generated.File{Id: id, FileName: id}
		}
		return files, nil
	}

	result, err := c.Service.ListFilesFiltered(f, 0, 0)
	if err != nil {
		return nil, err
	}
	if f.Status != "" || len(exclude) == 0 {
		return result.Files, nil
	}
	skip := make(map[string]bool, len(exclude))
	for _, s := range exclude {
		skip[s] = true
	}
	var files []generated.File
	for _, file := range result.Files {
		if !skip[string(file.Status)] {
			files = append(files, file)
		}
	}
	return files, nil
}

// printDryRun lists the files a command would act on.
func printDryRun(action string, files []generated.File) {
	_, _ = fmt.Fprintf(os.Stderr, "Dry run: would %s %d files\n", action, len(files))
	for _, f := range files {
		fmt.Printf("%s\t%s\n", f.Id, f.FileName)
	}
}
