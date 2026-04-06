package cli

import (
	"fmt"
	"os"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/spf13/cobra"
)

var (
	pullSource  string
	pullProduct string
)

func init() {
	pullCmd.Flags().StringVar(&pullSource, "source", "", "filter by source ID")
	pullCmd.Flags().StringVar(&pullProduct, "product", "", "filter by product ID")
	rootCmd.AddCommand(pullCmd)
}

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Sync sources and download all available files",
	Long:  "Sync product catalogs from enabled sources, then download all available files. Combines 'product sync --all' and 'download all'.",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		// Phase 1: sync
		sources, err := c.Service.ListSources()
		if err != nil {
			return err
		}

		// When filtering by product, only sync the source that owns it
		effectiveSource := pullSource
		if effectiveSource == "" && pullProduct != "" {
			products, err := c.Service.ListProducts(nil)
			if err != nil {
				return fmt.Errorf("failed to list products: %w", err)
			}
			for _, p := range products {
				if p.Id == pullProduct {
					effectiveSource = p.SourceId
					break
				}
			}
		}

		for _, src := range sources {
			if !src.Enabled {
				continue
			}
			if effectiveSource != "" && src.Id != effectiveSource {
				continue
			}
			if !flagQuiet {
				_, _ = fmt.Fprintf(os.Stderr, "Syncing source %s...\n", src.Id)
			}
			if err := c.Service.SyncSourceProducts(src.Id); err != nil {
				return fmt.Errorf("failed to sync products for source %s: %w", src.Id, err)
			}
			if err := c.Service.SyncSourceFiles(src.Id); err != nil {
				return fmt.Errorf("failed to sync files for source %s: %w", src.Id, err)
			}
		}

		// Phase 2: download
		var sourcePtr, productPtr *string
		if pullSource != "" {
			sourcePtr = &pullSource
		}
		if pullProduct != "" {
			productPtr = &pullProduct
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
