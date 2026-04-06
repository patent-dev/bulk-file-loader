package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/spf13/cobra"
)

var (
	webhookEvents       string
	webhookUpdateName   string
	webhookUpdateURL    string
	webhookUpdateEvents string
	webhookEnabled      bool
	webhookEnabledSet   bool
)

func init() {
	webhookAddCmd.Flags().StringVar(&webhookEvents, "events", "", "comma-separated list of event types")

	webhookUpdateCmd.Flags().StringVar(&webhookUpdateName, "name", "", "webhook name")
	webhookUpdateCmd.Flags().StringVar(&webhookUpdateURL, "url", "", "webhook URL")
	webhookUpdateCmd.Flags().StringVar(&webhookUpdateEvents, "events", "", "comma-separated list of event types")
	webhookUpdateCmd.Flags().BoolVar(&webhookEnabled, "enabled", false, "enable or disable webhook")
	// Track whether --enabled was explicitly set
	webhookUpdateCmd.PreRunE = func(cmd *cobra.Command, args []string) error {
		webhookEnabledSet = cmd.Flags().Changed("enabled")
		return nil
	}

	webhookCmd.AddCommand(webhookLsCmd, webhookAddCmd, webhookUpdateCmd, webhookRmCmd)
	rootCmd.AddCommand(webhookCmd)
}

var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage webhooks",
}

var webhookLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List webhooks",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		webhooks, err := c.Service.ListWebhooks()
		if err != nil {
			return err
		}

		if flagQuiet {
			for _, wh := range webhooks {
				fmt.Println(wh.Id)
			}
			return nil
		}

		headers := []string{"ID", "Name", "URL", "Events", "Enabled", "Created"}
		rows := make([][]string, 0, len(webhooks))
		for _, wh := range webhooks {
			created := fmtTime(wh.CreatedAt)
			rows = append(rows, []string{
				fmt.Sprintf("%d", wh.Id),
				wh.Name,
				wh.Url,
				strings.Join(wh.Events, ","),
				boolToStr(wh.Enabled),
				created,
			})
		}

		return formatOutput(headers, rows, webhooks)
	},
}

var webhookAddCmd = &cobra.Command{
	Use:   "add <name> <url>",
	Short: "Add a webhook",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		events := parseCSVFlag(webhookEvents)

		wh, err := c.Service.CreateWebhook(args[0], args[1], events)
		if err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(wh.Id)
			return nil
		}

		if flagFormat == "json" {
			return printJSON(wh)
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Webhook created: %d\n", wh.Id)
		return nil
	},
}

var webhookUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update a webhook",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid webhook ID: %s", args[0])
		}

		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		req := generated.UpdateWebhookRequest{}
		if webhookUpdateName != "" {
			req.Name = &webhookUpdateName
		}
		if webhookUpdateURL != "" {
			req.Url = &webhookUpdateURL
		}
		if webhookUpdateEvents != "" {
			events := parseCSVFlag(webhookUpdateEvents)
			req.Events = &events
		}
		if webhookEnabledSet {
			req.Enabled = &webhookEnabled
		}

		wh, err := c.Service.UpdateWebhook(id, req)
		if err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(wh.Id)
			return nil
		}

		if flagFormat == "json" {
			return printJSON(wh)
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Webhook updated: %d\n", wh.Id)
		return nil
	},
}

var webhookRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Remove a webhook",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid webhook ID: %s", args[0])
		}

		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		if err := c.Service.DeleteWebhook(id); err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(id)
			return nil
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Webhook deleted: %d\n", id)
		return nil
	},
}

func parseCSVFlag(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
