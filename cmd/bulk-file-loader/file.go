package cli

import (
	"fmt"
	"os"

	"github.com/patent-dev/bulk-file-loader/internal/core"
	"github.com/spf13/cobra"
)

var (
	fileProductFilter string
	fileSourceFilter  string
	fileStatusFilter  string
	fileLimit         int
	fileOffset        int
)

func init() {
	addFileFilterFlags(fileLsCmd)
	fileLsCmd.Flags().IntVar(&fileLimit, "limit", 50, "maximum number of results, 0 for all")
	fileLsCmd.Flags().IntVar(&fileOffset, "offset", 0, "offset for pagination")
	fileDownloadCmd.Flags().BoolVar(&downloadWait, "wait", false, "with a running server, wait until the files are downloaded")
	for _, cmd := range []*cobra.Command{fileSkipCmd, fileUnskipCmd, fileDownloadCmd} {
		addFileFilterFlags(cmd)
		cmd.Flags().BoolVar(&fileDryRun, "dry-run", false, "list the matching files without acting on them")
	}

	fileCmd.AddCommand(fileLsCmd, fileShowCmd, fileDownloadCmd, fileCancelCmd, fileSkipCmd, fileUnskipCmd, fileResetCmd, fileDeleteCmd)
	rootCmd.AddCommand(fileCmd)
}

var fileCmd = &cobra.Command{
	Use:   "file",
	Short: "Manage files",
}

var fileLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List files",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		if fileLimit < 0 || fileOffset < 0 {
			return fmt.Errorf("--limit and --offset must not be negative (--limit 0 lists all)")
		}
		filter, _, err := fileFilterFromFlags()
		if err != nil {
			return err
		}
		result, err := c.Service.ListFilesFiltered(filter, fileOffset, fileLimit)
		if err != nil {
			return err
		}

		if flagQuiet {
			for _, f := range result.Files {
				fmt.Println(f.Id)
			}
			return nil
		}

		headers := []string{"ID", "Name", "Status", "Attempts", "Size", "Product", "Released", "Error"}
		rows := make([][]string, 0, len(result.Files))
		for _, f := range result.Files {
			released := fmtTime(f.ReleasedAt)
			size := ""
			if f.FileSize != nil {
				size = formatBytes(*f.FileSize)
			}
			attempts := ""
			if f.Attempts != nil && f.MaxAttempts != nil {
				attempts = fmt.Sprintf("%d/%d", *f.Attempts, *f.MaxAttempts)
			}
			rows = append(rows, []string{
				f.Id,
				f.FileName,
				string(f.Status),
				attempts,
				size,
				deref(f.ProductId, ""),
				released,
				truncate(deref(f.ErrorMessage, ""), 60),
			})
		}

		if flagFormat == "json" {
			return printJSON(map[string]any{
				"files": result.Files,
				"total": result.Total,
			})
		}

		if !flagQuiet {
			hint := ""
			if len(result.Files) < result.Total {
				hint = "; --limit 0 for all"
			}
			_, _ = fmt.Fprintf(os.Stderr, "Showing %d of %d files, newest first%s\n", len(result.Files), result.Total, hint)
		}

		return formatOutput(headers, rows, result.Files)
	},
}

var fileShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show file details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		file, err := c.Service.GetFile(args[0])
		if err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(file.Id)
			return nil
		}

		if flagFormat == "json" {
			return printJSON(file)
		}

		headers := []string{"Field", "Value"}
		rows := [][]string{
			{"ID", file.Id},
			{"Name", file.FileName},
			{"Status", string(file.Status)},
			{"Size", formatBytes(deref(file.FileSize, 0))},
			{"Product", deref(file.ProductId, "")},
			{"Source", deref(file.SourceId, "")},
			{"Delivery", deref(file.DeliveryId, "")},
			{"Checksum", deref(file.ExpectedChecksum, "")},
			{"Skipped", boolToStr(deref(file.Skipped, false))},
		}
		if r := fmtTime(file.ReleasedAt); r != "" {
			rows = append(rows, []string{"Released", r})
		}
		if file.Attempts != nil && file.MaxAttempts != nil {
			rows = append(rows, []string{"Attempts", fmt.Sprintf("%d of %d", *file.Attempts, *file.MaxAttempts)})
		}
		if n := fmtTime(file.NextAttemptAt); n != "" {
			rows = append(rows, []string{"Next Attempt", n})
		}
		if file.ErrorMessage != nil {
			rows = append(rows, []string{"Error", *file.ErrorMessage})
		}

		if file.DownloadHistory != nil {
			for i, h := range *file.DownloadHistory {
				prefix := fmt.Sprintf("History[%d]", i)
				rows = append(rows, []string{prefix + " Status", string(h.Status)})
				if h.LocalPath != nil {
					rows = append(rows, []string{prefix + " Path", *h.LocalPath})
				}
				if s := fmtTime(h.StartedAt); s != "" {
					rows = append(rows, []string{prefix + " Started", s})
				}
				if s := fmtTime(h.CompletedAt); s != "" {
					rows = append(rows, []string{prefix + " Completed", s})
				}
			}
		}

		return formatOutput(headers, rows, file)
	},
}

var fileDownloadCmd = &cobra.Command{
	Use:   "download [<id>...]",
	Short: "Download files by ID or by filter",
	Long:  "Download the given files, or every file matching the filters. Without --status, filters skip files that are downloaded, downloading or skipped.",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		files, err := selectFiles(c, args, "downloaded", "downloading", "skipped")
		if err != nil {
			return err
		}
		if fileDryRun {
			printDryRun("download", files)
			return nil
		}
		items := make([]downloadItem, len(files))
		for i, f := range files {
			items[i] = downloadItem{ID: f.Id, Name: f.FileName}
		}
		return runDownloads(cmd.Context(), c, items)
	},
}

var fileCancelCmd = &cobra.Command{
	Use:   "cancel <id>...",
	Short: "Cancel active downloads",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBulkFileAction(cmd, args, "cancel", func(c *core.Core, id string) error {
			return c.Service.CancelDownload(id)
		})
	},
}

var fileSkipCmd = &cobra.Command{
	Use:   "skip [<id>...]",
	Short: "Mark files as skipped, by ID or by filter",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runFilteredFileAction(cmd, args, "skip", func(c *core.Core, id string) error {
			return c.Service.SkipFile(id)
		})
	},
}

var fileUnskipCmd = &cobra.Command{
	Use:   "unskip [<id>...]",
	Short: "Remove the skip flag from files, by ID or by filter",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runFilteredFileAction(cmd, args, "unskip", func(c *core.Core, id string) error {
			return c.Service.UnskipFile(id)
		})
	},
}

var fileResetCmd = &cobra.Command{
	Use:   "reset <id>...",
	Short: "Reset download history for files",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBulkFileAction(cmd, args, "reset", func(c *core.Core, id string) error {
			return c.Service.ResetFile(id)
		})
	},
}

var fileDeleteCmd = &cobra.Command{
	Use:   "delete <id>...",
	Short: "Delete downloaded files",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBulkFileAction(cmd, args, "delete", func(c *core.Core, id string) error {
			return c.Service.DeleteFile(id)
		})
	},
}

func runBulkFileAction(cmd *cobra.Command, ids []string, action string, fn func(*core.Core, string) error) error {
	c, err := requireCore()
	if err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}
	defer c.Close()
	return applyFileAction(cmd, c, ids, action, fn)
}

// runFilteredFileAction applies fn to the files given by ID or selected by
// the filter flags.
func runFilteredFileAction(cmd *cobra.Command, args []string, action string, fn func(*core.Core, string) error) error {
	c, err := requireCore()
	if err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}
	defer c.Close()

	files, err := selectFiles(c, args)
	if err != nil {
		return err
	}
	if fileDryRun {
		printDryRun(action, files)
		return nil
	}
	ids := make([]string, len(files))
	for i, f := range files {
		ids[i] = f.Id
	}
	if len(args) == 0 && !flagQuiet {
		_, _ = fmt.Fprintf(os.Stderr, "%d files match\n", len(ids))
	}
	return applyFileAction(cmd, c, ids, action, fn)
}

func applyFileAction(cmd *cobra.Command, c *core.Core, ids []string, action string, fn func(*core.Core, string) error) error {
	hadError := false
	for _, id := range ids {
		if err := fn(c, id); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Error: %s %s: %v\n", action, id, err)
			hadError = true
			continue
		}
		if flagQuiet {
			fmt.Println(id)
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", action, id)
		}
	}

	if hadError && len(ids) > 1 {
		return partialError{}
	} else if hadError {
		return fmt.Errorf("%s failed", action)
	}
	return nil
}

// truncate shortens s to at most n runes for table output.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}
