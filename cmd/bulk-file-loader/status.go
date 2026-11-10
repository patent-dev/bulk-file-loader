package cli

import (
	"fmt"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show system status and statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		stats, err := c.Service.GetStats()
		if err != nil {
			return err
		}
		health := c.Service.HealthCheck(cmd.Context())

		if flagQuiet {
			fmt.Printf("%s\n", health.Status)
			if health.Status == generated.Unhealthy {
				return fmt.Errorf("status %s", health.Status)
			}
			return nil
		}

		srv := c.Service.Server()
		transfers, err := c.Service.Transfers()
		if err != nil {
			return err
		}
		queue := deref(health.Queue, generated.QueueCounts{})

		server := "not running (CLI commands download themselves)"
		if srv != nil {
			server = fmt.Sprintf("running (%s, version %s, up %s)", srv.Owner, srv.Version, time.Since(srv.StartedAt).Round(time.Second))
		}
		headers := []string{"Metric", "Value"}
		rows := [][]string{
			{"Status", string(health.Status)},
			{"Version", deref(health.Version, "")},
			{"Database", string(deref(health.Database, ""))},
			{"Server", server},
			{"Enabled Sources", fmt.Sprintf("%d", deref(stats.EnabledSources, 0))},
			{"Total Files", fmt.Sprintf("%d", deref(stats.TotalFiles, 0))},
			{"Downloaded Files", fmt.Sprintf("%d", deref(stats.DownloadedFiles, 0))},
			{"Pending Files", fmt.Sprintf("%d", deref(stats.PendingFiles, 0))},
			{"Queue", fmt.Sprintf("%d queued, %d running, %d retrying, %d failed", queue.Queued, queue.Running, queue.Retrying, queue.Failed)},
			{"Transfers", fmt.Sprintf("%d", len(transfers))},
		}
		for _, t := range transfers {
			progress := formatBytes(t.Bytes)
			if t.TotalBytes > 0 {
				progress = fmt.Sprintf("%s / %s", formatBytes(t.Bytes), formatBytes(t.TotalBytes))
			}
			rows = append(rows, []string{"  " + t.FileName, fmt.Sprintf("%s, since %s", progress, t.StartedAt.Local().Format(time.TimeOnly))})
		}

		jsonData := map[string]any{
			"health":    health,
			"stats":     stats,
			"server":    srv,
			"transfers": transfers,
		}

		return formatOutput(headers, rows, jsonData)
	},
}
