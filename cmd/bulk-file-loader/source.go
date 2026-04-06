package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var (
	sourceUsername string
	sourcePassword string
	sourceAPIKey   string
)

func init() {
	sourceEnableCmd.Flags().StringVar(&sourceUsername, "username", "", "username credential")
	sourceEnableCmd.Flags().StringVar(&sourcePassword, "password", "", "password credential")
	sourceEnableCmd.Flags().StringVar(&sourceAPIKey, "api-key", "", "API key credential")

	sourceTestCmd.Flags().StringVar(&sourceUsername, "username", "", "username credential")
	sourceTestCmd.Flags().StringVar(&sourcePassword, "password", "", "password credential")
	sourceTestCmd.Flags().StringVar(&sourceAPIKey, "api-key", "", "API key credential")

	sourceCmd.AddCommand(sourceLsCmd, sourceShowCmd, sourceEnableCmd, sourceDisableCmd, sourceTestCmd)
	rootCmd.AddCommand(sourceCmd)
}

var sourceCmd = &cobra.Command{
	Use:   "source",
	Short: "Manage data sources",
}

var sourceLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all sources",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		srcs, err := c.Service.ListSources()
		if err != nil {
			return err
		}

		if flagQuiet {
			for _, s := range srcs {
				fmt.Println(s.Id)
			}
			return nil
		}

		headers := []string{"ID", "Name", "Enabled", "Has Credentials", "Last Sync"}
		rows := make([][]string, 0, len(srcs))
		for _, s := range srcs {
			rows = append(rows, []string{
				s.Id, s.Name, boolToStr(s.Enabled), boolToStr(s.HasCredentials), fmtTime(s.LastSyncAt),
			})
		}

		return formatOutput(headers, rows, srcs)
	},
}

var sourceShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show source details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		src, err := c.Service.GetSource(args[0])
		if err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(src.Id)
			return nil
		}

		if flagFormat == "json" {
			return printJSON(src)
		}

		headers := []string{"Field", "Value"}
		lastSync := fmtTime(src.LastSyncAt)
		rows := [][]string{
			{"ID", src.Id},
			{"Name", src.Name},
			{"Enabled", boolToStr(src.Enabled)},
			{"Has Credentials", boolToStr(src.HasCredentials)},
			{"Last Sync", lastSync},
		}
		for _, cf := range src.CredentialFields {
			required := ""
			if cf.Required {
				required = " (required)"
			}
			rows = append(rows, []string{"Credential: " + cf.Label, cf.Key + required})
		}

		return formatOutput(headers, rows, src)
	},
}

var sourceEnableCmd = &cobra.Command{
	Use:   "enable <id>",
	Short: "Enable a source with optional credentials",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		creds := buildCredentials()
		var credsPtr *map[string]string
		if len(creds) > 0 {
			credsPtr = &creds
		}

		enabled := true
		ctx, cancel := context.WithTimeout(context.Background(), sourceTimeout())
		defer cancel()

		src, err := c.Service.UpdateSource(ctx, args[0], &enabled, credsPtr)
		if err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(src.Id)
			return nil
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source %s enabled\n", src.Id)
		return nil
	},
}

var sourceDisableCmd = &cobra.Command{
	Use:   "disable <id>",
	Short: "Disable a source",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		enabled := false
		ctx, cancel := context.WithTimeout(context.Background(), sourceTimeout())
		defer cancel()

		src, err := c.Service.UpdateSource(ctx, args[0], &enabled, nil)
		if err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(src.Id)
			return nil
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source %s disabled\n", src.Id)
		return nil
	},
}

var sourceTestCmd = &cobra.Command{
	Use:   "test <id>",
	Short: "Test credentials for a source",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		creds := buildCredentials()
		if len(creds) == 0 {
			return fmt.Errorf("at least one credential flag is required (--username, --password, --api-key)")
		}

		ctx, cancel := context.WithTimeout(context.Background(), sourceTimeout())
		defer cancel()

		if err := c.Service.TestCredentials(ctx, args[0], creds); err != nil {
			return fmt.Errorf("credential test failed: %w", err)
		}

		if !flagQuiet {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Credentials are valid")
		}
		return nil
	},
}

func buildCredentials() map[string]string {
	creds := make(map[string]string)
	if sourceUsername != "" {
		creds["username"] = sourceUsername
	}
	if sourcePassword != "" {
		creds["password"] = sourcePassword
	}
	if sourceAPIKey != "" {
		creds["api_key"] = sourceAPIKey
	}

	return creds
}

func sourceTimeout() time.Duration {
	if flagTimeout > 0 {
		return time.Duration(flagTimeout) * time.Second
	}
	return 60 * time.Second
}
