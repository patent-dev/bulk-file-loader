package cli

import (
	"fmt"

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
		health := c.Service.HealthCheck()

		if flagQuiet {
			fmt.Printf("%s\n", health.Status)
			return nil
		}

		headers := []string{"Metric", "Value"}
		rows := [][]string{
			{"Status", string(health.Status)},
			{"Version", deref(health.Version, "")},
			{"Uptime", deref(health.Uptime, "")},
			{"Enabled Sources", fmt.Sprintf("%d", deref(stats.EnabledSources, 0))},
			{"Total Files", fmt.Sprintf("%d", deref(stats.TotalFiles, 0))},
			{"Downloaded Files", fmt.Sprintf("%d", deref(stats.DownloadedFiles, 0))},
			{"Pending Files", fmt.Sprintf("%d", deref(stats.PendingFiles, 0))},
			{"Active Downloads", fmt.Sprintf("%d", deref(stats.ActiveDownloads, 0))},
		}

		jsonData := map[string]any{
			"health": health,
			"stats":  stats,
		}

		return formatOutput(headers, rows, jsonData)
	},
}
