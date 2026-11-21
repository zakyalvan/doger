module github.com/kartaladev/doger/events/sql

go 1.27

require (
	github.com/go-sql-driver/mysql v1.10.2
	github.com/jackc/pgx/v5 v5.13.0
	github.com/kartaladev/doger v0.0.0-00010101000000-000000000000
	github.com/kartaladev/doger/events/sqltx v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.12.1
	go.uber.org/goleak v1.3.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	github.com/samber/go-type-to-string v1.8.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/kartaladev/doger => ../../

replace github.com/kartaladev/doger/events/sqltx => ../sqltx
