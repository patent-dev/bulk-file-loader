package cli

import (
	"errors"
	"io/fs"
	"os"
	"strconv"

	"github.com/patent-dev/bulk-file-loader/internal/core"
	"github.com/spf13/cobra"
)

var Version = "dev" // overridden by -ldflags at build time

var WebAssets fs.FS

var rootCmd = &cobra.Command{
	Use:   "bulk-file-loader",
	Short: "Bulk patent file downloader and manager",
	Long:  "Download, manage, and sync patent bulk data files from EPO, USPTO, DPMA, and other sources.",
}

// Global flags available to all subcommands.
var (
	flagDataDir       string
	flagDBDriver      string
	flagDBDSN         string
	flagTimeout       int
	flagMaxConcurrent int
	flagFormat        string
	flagQuiet         bool
	flagNoWebhooks    bool
)

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flagDataDir, "data-dir", "", "data directory (env: BULK_LOADER_DATA_DIR, default: ./data)")
	pf.StringVar(&flagDBDriver, "db-driver", "", "database driver (env: BULK_LOADER_DB_DRIVER, default: sqlite)")
	pf.StringVar(&flagDBDSN, "db-dsn", "", "database DSN (env: BULK_LOADER_DB_DSN)")
	pf.IntVar(&flagTimeout, "timeout", 0, "download timeout in seconds (env: BULK_LOADER_DOWNLOAD_TIMEOUT, default: 3600)")
	pf.IntVar(&flagMaxConcurrent, "max-concurrent", 0, "max concurrent downloads (env: BULK_LOADER_MAX_CONCURRENT, default: 3)")
	pf.StringVar(&flagFormat, "format", "table", "output format: table, json, csv")
	pf.BoolVarP(&flagQuiet, "quiet", "q", false, "suppress non-essential output")
	pf.BoolVar(&flagNoWebhooks, "no-webhooks", false, "disable webhook notifications")
}

func ApplyFlagsToEnv() {
	setEnvIfFlag("BULK_LOADER_DATA_DIR", flagDataDir)
	setEnvIfFlag("BULK_LOADER_DB_DRIVER", flagDBDriver)
	setEnvIfFlag("BULK_LOADER_DB_DSN", flagDBDSN)
	if flagTimeout > 0 {
		setEnvIfFlag("BULK_LOADER_DOWNLOAD_TIMEOUT", strconv.Itoa(flagTimeout))
	}
	if flagMaxConcurrent > 0 {
		setEnvIfFlag("BULK_LOADER_MAX_CONCURRENT", strconv.Itoa(flagMaxConcurrent))
	}
}

func setEnvIfFlag(key, val string) {
	if val != "" {
		_ = os.Setenv(key, val)
	}
}

func requireCore(opts ...core.Options) (*core.Core, error) {
	ApplyFlagsToEnv()
	o := core.Options{
		NoWebhooks: flagNoWebhooks,
		Version:    Version,
	}
	if len(opts) > 0 {
		o = opts[0]
	}
	return core.New(o)
}

func Execute() error {
	return rootCmd.Execute()
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var pe partialError
	if errors.As(err, &pe) {
		return 3
	}
	return 1
}
