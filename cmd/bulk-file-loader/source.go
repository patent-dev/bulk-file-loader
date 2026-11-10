package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/spf13/cobra"
)

var (
	sourceUsername      string
	sourcePassword      string
	sourceAPIKey        string
	sourcePasswordStdin bool
	sourceAPIKeyStdin   bool
	sourceSync          bool
)

func addCredentialFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&sourceUsername, "username", "", "username credential")
	cmd.Flags().StringVar(&sourcePassword, "password", "", "password credential (deprecated: visible in ps and shell history)")
	cmd.Flags().StringVar(&sourceAPIKey, "api-key", "", "API key credential (deprecated: visible in ps and shell history)")
	cmd.Flags().BoolVar(&sourcePasswordStdin, "password-stdin", false, "read the password from stdin")
	cmd.Flags().BoolVar(&sourceAPIKeyStdin, "api-key-stdin", false, "read the API key from stdin")
}

func init() {
	addCredentialFlags(sourceEnableCmd)
	addCredentialFlags(sourceTestCmd)
	sourceEnableCmd.Flags().BoolVar(&sourceSync, "sync", false, "also sync the file catalogue (can take minutes)")

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
				s.Id, s.Name, boolToStr(s.Enabled), credentialStatus(s), fmtTime(s.LastSyncAt),
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
			{"Has Credentials", credentialStatus(src)},
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

		current, err := c.Service.GetSource(args[0])
		if err != nil {
			return err
		}
		in := defaultCredentialInput()
		creds, err := credentialsGiven(in, args[0])
		if err != nil {
			return err
		}
		// Given credentials (flags, stdin, environment) are completed by a
		// prompt; without any, a source that has stored credentials keeps them.
		if len(creds) > 0 || !current.HasCredentials {
			if err := completeCredentials(in, args[0], current.CredentialFields, creds); err != nil {
				return err
			}
		}
		var credsPtr *map[string]string
		if len(creds) > 0 {
			credsPtr = &creds
		}

		enabled := true
		ctx, cancel := context.WithTimeout(cmd.Context(), sourceTimeout())
		defer cancel()

		src, err := c.Service.UpdateSource(ctx, args[0], &enabled, credsPtr)
		if err != nil {
			return err
		}

		if flagQuiet {
			fmt.Println(src.Id)
		} else {
			sourceID := src.Id
			products, _ := c.Service.ListProducts(&sourceID)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source %s enabled (%d products)\n", src.Id, len(products))
			if !sourceSync {
				_, _ = fmt.Fprintf(os.Stderr, "Run 'bulk-file-loader product sync --all' or pass --sync to fetch the file catalogue.\n")
			}
		}

		if sourceSync {
			sum := newSyncSummary()
			if err := syncSource(cmd.Context(), c, src.Id, sum); err != nil {
				return err
			}
			sum.print()
			return sum.result()
		}
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

		in := defaultCredentialInput()
		creds, err := credentialsGiven(in, args[0])
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), sourceTimeout())
		defer cancel()

		// Without credentials given (flags, stdin, environment), test the
		// stored ones.
		what := "Stored credentials"
		if len(creds) == 0 {
			err = c.Service.TestStoredCredentials(ctx, args[0])
		} else {
			what = "Credentials"
			src, gerr := c.Service.GetSource(args[0])
			if gerr != nil {
				return gerr
			}
			if err := completeCredentials(in, args[0], src.CredentialFields, creds); err != nil {
				return err
			}
			err = c.Service.TestCredentials(ctx, args[0], creds)
		}
		if err != nil {
			return fmt.Errorf("credential test failed: %w", err)
		}

		if !flagQuiet {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s are valid\n", what)
		}
		return nil
	},
}

func sourceTimeout() time.Duration {
	if flagTimeout > 0 {
		return time.Duration(flagTimeout) * time.Second
	}
	return 60 * time.Second
}

// credentialStatus describes a source's credentials; public sources have no
// credential fields and need none.
func credentialStatus(s generated.Source) string {
	if len(s.CredentialFields) == 0 {
		return "not needed"
	}
	return boolToStr(s.HasCredentials)
}
