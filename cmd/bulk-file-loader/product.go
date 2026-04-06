package cli

import (
	"fmt"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/spf13/cobra"
)

var (
	productSourceFilter string
	productSchedule     string
	productSyncAll      bool
)

func init() {
	productLsCmd.Flags().StringVar(&productSourceFilter, "source", "", "filter by source ID")

	productEnableCmd.Flags().StringVar(&productSchedule, "schedule", "", "check window start (cron expression or time)")

	productSyncCmd.Flags().BoolVar(&productSyncAll, "all", false, "sync all products from enabled sources")

	productCmd.AddCommand(productLsCmd, productShowCmd, productSyncCmd, productEnableCmd, productDisableCmd)
	rootCmd.AddCommand(productCmd)
}

var productCmd = &cobra.Command{
	Use:   "product",
	Short: "Manage products",
}

var productLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List products",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		var sourceFilter *string
		if productSourceFilter != "" {
			sourceFilter = &productSourceFilter
		}

		products, err := c.Service.ListProducts(sourceFilter)
		if err != nil {
			return err
		}

		if flagQuiet {
			for _, p := range products {
				fmt.Println(p.Id)
			}
			return nil
		}

		headers := []string{"ID", "Source", "Name", "Auto-DL", "Total", "Downloaded", "Failed"}
		rows := make([][]string, 0, len(products))
		for _, p := range products {
			rows = append(rows, []string{
				p.Id,
				p.SourceId,
				p.Name,
				boolToStr(p.AutoDownload),
				fmt.Sprintf("%d", deref(p.TotalFiles, 0)),
				fmt.Sprintf("%d", deref(p.DownloadedFiles, 0)),
				fmt.Sprintf("%d", deref(p.FailedFiles, 0)),
			})
		}

		return formatOutput(headers, rows, products)
	},
}

var productShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show product details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		product, err := c.Service.GetProduct(args[0])
		if err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(product.Id)
			return nil
		}

		if flagFormat == "json" {
			return printJSON(product)
		}

		lastChecked := fmtTime(product.LastCheckedAt)

		headers := []string{"Field", "Value"}
		rows := [][]string{
			{"ID", product.Id},
			{"Source", product.SourceId},
			{"Name", product.Name},
			{"Description", deref(product.Description, "")},
			{"Auto-Download", boolToStr(product.AutoDownload)},
			{"Check Window", deref(product.CheckWindowStart, "")},
			{"Last Checked", lastChecked},
			{"Total Files", fmt.Sprintf("%d", deref(product.TotalFiles, 0))},
			{"Downloaded", fmt.Sprintf("%d", deref(product.DownloadedFiles, 0))},
			{"Failed", fmt.Sprintf("%d", deref(product.FailedFiles, 0))},
		}

		if product.Deliveries != nil {
			rows = append(rows, []string{"Deliveries", fmt.Sprintf("%d", len(*product.Deliveries))})
		}

		return formatOutput(headers, rows, product)
	},
}

var productSyncCmd = &cobra.Command{
	Use:   "sync [id]",
	Short: "Sync product files from source",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !productSyncAll && len(args) == 0 {
			return fmt.Errorf("provide a product ID or use --all")
		}

		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		if productSyncAll {
			// Sync all products from enabled sources
			sources, err := c.Service.ListSources()
			if err != nil {
				return err
			}
			for _, src := range sources {
				if !src.Enabled {
					continue
				}
				if !flagQuiet {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Syncing source %s...\n", src.Id)
				}
				if err := c.Service.SyncSourceProducts(src.Id); err != nil {
					return fmt.Errorf("failed to sync products for source %s: %w", src.Id, err)
				}
				if err := c.Service.SyncSourceFiles(src.Id); err != nil {
					return fmt.Errorf("failed to sync files for source %s: %w", src.Id, err)
				}
			}
			if !flagQuiet {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Sync complete")
			}
			return nil
		}

		if !flagQuiet {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Syncing product %s...\n", args[0])
		}

		if err := c.Service.SyncProductFull(args[0]); err != nil {
			return err
		}

		if !flagQuiet {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Sync complete")
		}
		return nil
	},
}

var productEnableCmd = &cobra.Command{
	Use:   "enable <id>",
	Short: "Enable auto-download for a product",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		autoDownload := true
		req := generated.UpdateScheduleRequest{
			AutoDownload: &autoDownload,
		}
		if productSchedule != "" {
			req.CheckWindowStart = &productSchedule
		}

		schedule, err := c.Service.UpdateProductSchedule(args[0], req)
		if err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(schedule.ProductId)
			return nil
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Auto-download enabled for %s\n", schedule.ProductId)
		return nil
	},
}

var productDisableCmd = &cobra.Command{
	Use:   "disable <id>",
	Short: "Disable auto-download for a product",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		autoDownload := false
		req := generated.UpdateScheduleRequest{
			AutoDownload: &autoDownload,
		}

		schedule, err := c.Service.UpdateProductSchedule(args[0], req)
		if err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(schedule.ProductId)
			return nil
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Auto-download disabled for %s\n", schedule.ProductId)
		return nil
	},
}
