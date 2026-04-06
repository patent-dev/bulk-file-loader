package cli

import (
	"fmt"
	"os"

	"github.com/patent-dev/bulk-file-loader/api/generated"
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
	fileLsCmd.Flags().StringVar(&fileProductFilter, "product", "", "filter by product ID")
	fileLsCmd.Flags().StringVar(&fileSourceFilter, "source", "", "filter by source ID")
	fileLsCmd.Flags().StringVar(&fileStatusFilter, "status", "", "filter by status (available, downloading, downloaded, failed, skipped, cancelled)")
	fileLsCmd.Flags().IntVar(&fileLimit, "limit", 50, "maximum number of results")
	fileLsCmd.Flags().IntVar(&fileOffset, "offset", 0, "offset for pagination")

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

		if fileLimit < 0 {
			fileLimit = 0
		}
		if fileOffset < 0 {
			fileOffset = 0
		}

		var sourcePtr, productPtr *string
		if fileSourceFilter != "" {
			sourcePtr = &fileSourceFilter
		}
		if fileProductFilter != "" {
			productPtr = &fileProductFilter
		}
		var statusPtr *generated.ListFilesParamsStatus
		if fileStatusFilter != "" {
			s := generated.ListFilesParamsStatus(fileStatusFilter)
			statusPtr = &s
		}

		result, err := c.Service.ListFiles(sourcePtr, productPtr, statusPtr, fileOffset, fileLimit)
		if err != nil {
			return err
		}

		if flagQuiet {
			for _, f := range result.Files {
				fmt.Println(f.Id)
			}
			return nil
		}

		headers := []string{"ID", "Name", "Status", "Size", "Product", "Released"}
		rows := make([][]string, 0, len(result.Files))
		for _, f := range result.Files {
			released := fmtTime(f.ReleasedAt)
			size := ""
			if f.FileSize != nil {
				size = formatBytes(*f.FileSize)
			}
			rows = append(rows, []string{
				f.Id,
				f.FileName,
				string(f.Status),
				size,
				deref(f.ProductId, ""),
				released,
			})
		}

		if flagFormat == "json" {
			return printJSON(map[string]any{
				"files": result.Files,
				"total": result.Total,
			})
		}

		if !flagQuiet {
			_, _ = fmt.Fprintf(os.Stderr, "Showing %d of %d files\n", len(result.Files), result.Total)
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
	Use:   "download <id>...",
	Short: "Download one or more files",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		items := make([]downloadItem, len(args))
		for i, id := range args {
			items[i] = downloadItem{ID: id, Name: id}
		}
		return runDownloads(c.Downloader, items)
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
	Use:   "skip <id>...",
	Short: "Mark files as skipped",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBulkFileAction(cmd, args, "skip", func(c *core.Core, id string) error {
			return c.Service.SkipFile(id)
		})
	},
}

var fileUnskipCmd = &cobra.Command{
	Use:   "unskip <id>...",
	Short: "Remove skip flag from files",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBulkFileAction(cmd, args, "unskip", func(c *core.Core, id string) error {
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
