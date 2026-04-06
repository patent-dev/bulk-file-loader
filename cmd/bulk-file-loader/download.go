package cli

import (
	"fmt"
	"os"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/spf13/cobra"
)

var (
	dlHistoryStatus string
	dlHistoryLimit  int
	dlHistoryOffset int
	dlAllProduct    string
	dlAllSource     string
)

func init() {
	downloadHistoryCmd.Flags().StringVar(&dlHistoryStatus, "status", "", "filter by status")
	downloadHistoryCmd.Flags().IntVar(&dlHistoryLimit, "limit", 50, "maximum number of results")
	downloadHistoryCmd.Flags().IntVar(&dlHistoryOffset, "offset", 0, "offset for pagination")

	downloadAllCmd.Flags().StringVar(&dlAllProduct, "product", "", "filter by product ID")
	downloadAllCmd.Flags().StringVar(&dlAllSource, "source", "", "filter by source ID")

	downloadCmd.AddCommand(downloadHistoryCmd, downloadAllCmd)
	rootCmd.AddCommand(downloadCmd)
}

var downloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download management commands",
}

var downloadHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show download history",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		if dlHistoryLimit < 0 {
			dlHistoryLimit = 0
		}
		if dlHistoryOffset < 0 {
			dlHistoryOffset = 0
		}

		var statusPtr *string
		if dlHistoryStatus != "" {
			statusPtr = &dlHistoryStatus
		}

		result, err := c.Service.ListDownloads(statusPtr, dlHistoryOffset, dlHistoryLimit)
		if err != nil {
			return err
		}

		if flagQuiet {
			for _, d := range result.Downloads {
				fmt.Println(d.Id)
			}
			return nil
		}

		if flagFormat == "json" {
			return printJSON(map[string]any{
				"downloads": result.Downloads,
				"total":     result.Total,
			})
		}

		headers := []string{"ID", "File ID", "Status", "Progress", "Started", "Completed"}
		rows := make([][]string, 0, len(result.Downloads))
		for _, d := range result.Downloads {
			started := fmtTime(d.StartedAt)
			completed := fmtTime(d.CompletedAt)
			progress := ""
			if d.Progress != nil && d.TotalBytes != nil && *d.TotalBytes > 0 {
				progress = fmt.Sprintf("%s / %s", formatBytes(*d.Progress), formatBytes(*d.TotalBytes))
			} else if d.Progress != nil {
				progress = formatBytes(*d.Progress)
			}
			rows = append(rows, []string{
				fmt.Sprintf("%d", d.Id),
				d.FileId,
				string(d.Status),
				progress,
				started,
				completed,
			})
		}

		if !flagQuiet {
			_, _ = fmt.Fprintf(os.Stderr, "Showing %d of %d entries\n", len(result.Downloads), result.Total)
		}

		return formatOutput(headers, rows, result.Downloads)
	},
}

var downloadAllCmd = &cobra.Command{
	Use:   "all",
	Short: "Download all available files",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		var sourcePtr, productPtr *string
		if dlAllSource != "" {
			sourcePtr = &dlAllSource
		}
		if dlAllProduct != "" {
			productPtr = &dlAllProduct
		}

		status := generated.ListFilesParamsStatus("available")
		var allFiles []generated.File
		offset := 0
		const pageSize = 500
		for {
			result, err := c.Service.ListFiles(sourcePtr, productPtr, &status, offset, pageSize)
			if err != nil {
				return err
			}
			allFiles = append(allFiles, result.Files...)
			if len(allFiles) >= result.Total || len(result.Files) < pageSize {
				break
			}
			offset += pageSize
		}

		if len(allFiles) == 0 {
			if !flagQuiet {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No available files to download")
			}
			return nil
		}

		if !flagQuiet {
			_, _ = fmt.Fprintf(os.Stderr, "Downloading %d files...\n", len(allFiles))
		}

		items := make([]downloadItem, len(allFiles))
		for i, f := range allFiles {
			items[i] = downloadItem{ID: f.Id, Name: f.FileName}
		}
		return runDownloads(c.Downloader, items)
	},
}
