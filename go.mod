module github.com/patent-dev/bulk-file-loader

go 1.27.0

// The UI dependencies ship unrelated Go packages; keep them out of ./...
ignore ./web/ui/node_modules

require (
	github.com/getkin/kin-openapi v0.152.0
	github.com/go-sql-driver/mysql v1.10.1
	github.com/jackc/pgx/v5 v5.12.0
	github.com/ncruces/go-sqlite3/gormlite v0.34.0
	github.com/oapi-codegen/runtime v1.8.0
	github.com/patent-dev/dpma-connect-plus v0.3.7
	github.com/patent-dev/epo-bdds v0.2.3
	github.com/patent-dev/uspto-odp v1.6.11
	github.com/robfig/cron/v3 v3.0.1
	github.com/spf13/cobra v1.10.2
	golang.org/x/crypto v0.58.0
	golang.org/x/term v0.47.0
	gorm.io/driver/mysql v1.6.0
	gorm.io/driver/postgres v1.6.3
	gorm.io/gorm v1.31.2
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/go-openapi/jsonpointer v1.0.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/ncruces/go-sqlite3 v0.36.0 // indirect
	github.com/ncruces/go-sqlite3-wasm/v6 v6.4.35401 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/oasdiff/yaml v0.1.3 // indirect
	github.com/oasdiff/yaml3 v0.0.16 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)
