package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	pullSource  string
	pullProduct string
)

func init() {
	pullCmd.Flags().StringVar(&pullSource, "source", "", "filter by source ID")
	pullCmd.Flags().StringVar(&pullProduct, "product", "", "filter by product ID")
	pullCmd.Flags().BoolVar(&downloadWait, "wait", false, "with a running server, wait until the files are downloaded")
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

		sum := newSyncSummary()
		for _, src := range sources {
			if !src.Enabled {
				continue
			}
			if effectiveSource != "" && src.Id != effectiveSource {
				continue
			}
			if err := syncSource(cmd.Context(), c, src.Id, sum); err != nil {
				return err
			}
		}
		sum.print()

		// Phase 2: download
		allFiles, err := waitingFiles(c, pullSource, pullProduct)
		if err != nil {
			return err
		}

		if len(allFiles) == 0 {
			if !flagQuiet {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No available files to download")
			}
			return sum.result()
		}

		items := make([]downloadItem, len(allFiles))
		for i, f := range allFiles {
			items[i] = downloadItem{ID: f.Id, Name: f.FileName}
		}
		if err := runDownloads(cmd.Context(), c, items); err != nil {
			return err
		}
		// The downloads succeeded, but the catalogue may be missing files.
		if sum.failed > 0 {
			return partialError{}
		}
		return nil
	},
}
