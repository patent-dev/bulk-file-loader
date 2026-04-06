package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func init() {
	rootCmd.AddCommand(setupCmd)
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set up the encryption passphrase",
	Long:  "Configure the passphrase used to encrypt source credentials. Reads from BULK_LOADER_PASSPHRASE environment variable or prompts interactively.",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := requireCore()
		if err != nil {
			return fmt.Errorf("initialization failed: %w", err)
		}
		defer c.Close()

		if c.Auth.IsConfigured() {
			if !flagQuiet {
				if os.Getenv("BULK_LOADER_PASSPHRASE") != "" {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Passphrase configured via BULK_LOADER_PASSPHRASE environment variable.")
				} else {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Passphrase is already configured.")
				}
			}
			return nil
		}

		passphrase, readErr := readPassphrase()
		if readErr != nil {
			return readErr
		}
		if passphrase == "" {
			return fmt.Errorf("passphrase cannot be empty")
		}

		if err := c.Auth.Setup(passphrase); err != nil {
			return fmt.Errorf("setup failed: %w", err)
		}

		if !flagQuiet {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Passphrase configured successfully.")
		}
		return nil
	},
}

func readPassphrase() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Enter passphrase: ")
		pw, err := term.ReadPassword(fd)
		_, _ = fmt.Fprintln(os.Stderr) // newline after hidden input
		if err != nil {
			return "", fmt.Errorf("failed to read passphrase: %w", err)
		}

		fmt.Fprint(os.Stderr, "Confirm passphrase: ")
		pw2, err := term.ReadPassword(fd)
		_, _ = fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("failed to read confirmation: %w", err)
		}

		if string(pw) != string(pw2) {
			return "", fmt.Errorf("passphrases do not match")
		}

		return string(pw), nil
	}

	// Not a terminal; read a single line from stdin
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read passphrase from stdin: %w", err)
	}
	return strings.TrimSpace(line), nil
}
