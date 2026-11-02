// Package dbtest provides database configurations for tests.
//
// By default tests use a SQLite file in a temporary directory. Set
// BULK_LOADER_TEST_DB_DRIVER (postgres or mysql) and BULK_LOADER_TEST_DB_DSN
// to run against a server instead; every test then gets its own freshly
// created database, which is dropped when the test ends.
package dbtest

import (
	"crypto/rand"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/patent-dev/bulk-file-loader/config"
)

// Config returns a config pointing at an empty database for this test.
func Config(t *testing.T) *config.Config {
	t.Helper()

	cfg := &config.Config{
		DataDir:  t.TempDir(),
		DBDriver: os.Getenv("BULK_LOADER_TEST_DB_DRIVER"),
	}
	dsn := os.Getenv("BULK_LOADER_TEST_DB_DSN")
	name := "bfl_test_" + strings.ToLower(rand.Text()[:12])

	switch cfg.DBDriver {
	case "", "sqlite":
		cfg.DBDriver = "sqlite"
	case "mysql":
		mc, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatalf("parse mysql dsn: %v", err)
		}
		mc.DBName = ""
		createDatabase(t, "mysql", mc.FormatDSN(), name, "DROP DATABASE "+name)
		mc.DBName = name
		cfg.DBDSN = mc.FormatDSN()
	case "postgres":
		u, err := url.Parse(dsn)
		if err != nil || u.Scheme == "" {
			t.Fatalf("postgres dsn must be a URL (postgres://...): %q", dsn)
		}
		createDatabase(t, "pgx", u.String(), name, "DROP DATABASE "+name+" WITH (FORCE)")
		u.Path = "/" + name
		cfg.DBDSN = u.String()
	default:
		t.Fatalf("unsupported BULK_LOADER_TEST_DB_DRIVER %q", cfg.DBDriver)
	}
	return cfg
}

func createDatabase(t *testing.T, driver, serverDSN, name, dropStmt string) {
	t.Helper()
	db, err := sql.Open(driver, serverDSN)
	if err != nil {
		t.Fatalf("open %s: %v", driver, err)
	}
	if _, err := db.Exec("CREATE DATABASE " + name); err != nil {
		_ = db.Close()
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		defer func() { _ = db.Close() }()
		if _, err := db.Exec(dropStmt); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})
}
