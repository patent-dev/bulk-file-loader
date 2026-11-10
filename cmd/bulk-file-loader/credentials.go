package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"golang.org/x/term"
)

// credentialEnv maps a source's credential fields to the environment
// variables its client library reads, so one set of variables serves both.
var credentialEnv = map[string]map[string]string{
	"epo-bdds":          {"username": "EPO_BDDS_USERNAME", "password": "EPO_BDDS_PASSWORD"},
	"dpma-connect-plus": {"username": "DPMA_CONNECT_PLUS_USERNAME", "password": "DPMA_CONNECT_PLUS_PASSWORD"},
	"uspto-odp":         {"api_key": "USPTO_API_KEY"},
}

// credentialInput is where credentials are read from besides the flags.
type credentialInput struct {
	stdin   io.Reader
	isTTY   bool
	prompt  func(label string, secret bool) (string, error)
	getenv  func(string) string
	warnOut io.Writer
}

func defaultCredentialInput() credentialInput {
	fd := int(os.Stdin.Fd())
	return credentialInput{
		stdin:   os.Stdin,
		isTTY:   term.IsTerminal(fd),
		prompt:  promptTerminal,
		getenv:  os.Getenv,
		warnOut: os.Stderr,
	}
}

// credentialsFromFlags collects the credentials given explicitly on this
// invocation: flags and --password-stdin / --api-key-stdin.
func credentialsFromFlags(in credentialInput) (map[string]string, error) {
	if sourcePasswordStdin && sourceAPIKeyStdin {
		return nil, fmt.Errorf("--password-stdin and --api-key-stdin cannot be combined")
	}
	creds := make(map[string]string)
	if sourceUsername != "" {
		creds["username"] = sourceUsername
	}
	if sourcePassword != "" {
		_, _ = fmt.Fprintln(in.warnOut, "Warning: --password is visible in the process list and shell history; use --password-stdin or an environment variable instead.")
		creds["password"] = sourcePassword
	}
	if sourceAPIKey != "" {
		_, _ = fmt.Fprintln(in.warnOut, "Warning: --api-key is visible in the process list and shell history; use --api-key-stdin or an environment variable instead.")
		creds["api_key"] = sourceAPIKey
	}
	if sourcePasswordStdin || sourceAPIKeyStdin {
		var secret string
		var err error
		if in.isTTY {
			// A terminal on stdin: read without echo.
			secret, err = in.prompt("Secret", true)
			if err == nil && secret == "" {
				err = fmt.Errorf("no secret entered")
			}
		} else {
			secret, err = readSecret(in.stdin)
		}
		if err != nil {
			return nil, err
		}
		key := "password"
		if sourceAPIKeyStdin {
			key = "api_key"
		}
		creds[key] = secret
	}
	return creds, nil
}

// credentialsGiven collects the credentials given on this invocation: flags
// and stdin first, then the source's environment variables.
func credentialsGiven(in credentialInput, sourceID string) (map[string]string, error) {
	creds, err := credentialsFromFlags(in)
	if err != nil {
		return nil, err
	}
	for key, name := range credentialEnv[sourceID] {
		if creds[key] == "" {
			if v := in.getenv(name); v != "" {
				creds[key] = v
			}
		}
	}
	return creds, nil
}

// completeCredentials fills required fields missing from creds from the
// environment and, on a terminal, by prompting. It returns an error naming
// every way to supply a field that is still missing.
func completeCredentials(in credentialInput, sourceID string, fields []generated.CredentialField, creds map[string]string) error {
	env := credentialEnv[sourceID]
	for _, f := range fields {
		if creds[f.Key] != "" {
			continue
		}
		if name := env[f.Key]; name != "" {
			if v := in.getenv(name); v != "" {
				creds[f.Key] = v
				continue
			}
		}
		if !f.Required {
			continue
		}
		if in.isTTY {
			v, err := in.prompt(f.Label, f.Type == generated.Password)
			if err != nil {
				return err
			}
			if v != "" {
				creds[f.Key] = v
				continue
			}
		}
		return fmt.Errorf("missing %s: %s", f.Label, credentialHint(sourceID, f))
	}
	return nil
}

func credentialHint(sourceID string, f generated.CredentialField) string {
	var ways []string
	switch f.Key {
	case "username":
		ways = append(ways, "--username")
	case "password":
		ways = append(ways, "--password-stdin")
	case "api_key":
		ways = append(ways, "--api-key-stdin")
	}
	if name := credentialEnv[sourceID][f.Key]; name != "" {
		ways = append(ways, "$"+name)
	}
	ways = append(ways, "an interactive prompt")
	return "use " + strings.Join(ways, ", ")
}

// readSecret reads one secret from r, without the trailing newline.
func readSecret(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read secret from stdin: %w", err)
	}
	secret := strings.TrimRight(line, "\r\n")
	if secret == "" {
		return "", fmt.Errorf("no secret on stdin")
	}
	return secret, nil
}

func promptTerminal(label string, secret bool) (string, error) {
	_, _ = fmt.Fprintf(os.Stderr, "%s: ", label)
	fd := int(os.Stdin.Fd())
	if secret {
		// Secrets keep their spaces; only the line ending is not part of them.
		b, err := term.ReadPassword(fd)
		_, _ = fmt.Fprintln(os.Stderr)
		return strings.TrimRight(string(b), "\r\n"), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}
