package cli

import (
	"fmt"
	"os"
	"time"

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

	productEnableCmd.Flags().StringVar(&productSchedule, "schedule", "", `sync schedule: five-field cron expression ("0 7 * * 1") or descriptor (@daily)`)

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

		headers := []string{"ID", "Source", "Name", "Auto-DL", "Schedule", "Total", "Downloaded", "Failed"}
		rows := make([][]string, 0, len(products))
		for _, p := range products {
			rows = append(rows, []string{
				p.Id,
				p.SourceId,
				p.Name,
				boolToStr(p.AutoDownload),
				deref(p.CheckWindowStart, ""),
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
			{"Schedule", deref(product.CheckWindowStart, "")},
			{"Next Run", nextRunText(product.AutoDownload, product.CheckWindowStart, product.NextRun)},
			{"Last Run", lastChecked},
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

		ctx := cmd.Context()
		sum := newSyncSummary()
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
				if err := syncSource(ctx, c, src.Id, sum); err != nil {
					return err
				}
			}
		} else {
			if !flagQuiet {
				_, _ = fmt.Fprintf(os.Stderr, "Syncing product %s...\n", args[0])
			}
			// Errors of the product itself are counted by the progress
			// callback; anything before it (unknown product, product list
			// unavailable) is returned as is.
			before := sum.products
			if err := c.Service.SyncProductFull(ctx, args[0], sum.progress); err != nil && sum.products == before {
				return err
			}
		}
		sum.print()
		return sum.result()
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
		// An explicitly empty --schedule "" clears the schedule.
		if cmd.Flags().Changed("schedule") {
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

		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Auto-download enabled for %s\n", schedule.ProductId)
		_, _ = fmt.Fprintf(out, "Schedule: %s\n", deref(schedule.CheckWindowStart, "none"))
		_, _ = fmt.Fprintf(out, "Next run: %s\n", nextRunText(true, schedule.CheckWindowStart, schedule.NextRun))
		if n, err := c.Service.PendingFileCount(schedule.ProductId); err == nil && n > 0 {
			_, _ = fmt.Fprintf(os.Stderr, "%d files queued; without a running server, download them with 'bulk-file-loader download all --product %s'.\n", n, schedule.ProductId)
		}
		if !flagQuiet {
			_, _ = fmt.Fprintln(os.Stderr, "A running server applies the schedule within 30 seconds and downloads queued files.")
		}
		return nil
	},
}

// nextRunText describes when a product is synced next.
func nextRunText(autoDownload bool, schedule *string, next *time.Time) string {
	switch {
	case !autoDownload:
		return "never (auto-download off)"
	case schedule == nil || *schedule == "":
		return "never (no schedule)"
	case next == nil:
		return "unknown"
	}
	return fmtTime(next)
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
