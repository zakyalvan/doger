package eventsql_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver

	eventsql "github.com/kartaladev/doger/events/sql"
	"github.com/kartaladev/doger/events/sql/mysql"
	"github.com/kartaladev/doger/events/sql/postgres"
)

// Integration tests read their databases from the environment:
// DOGER_SQL_POSTGRES_DSN and DOGER_SQL_MYSQL_DSN each hold one or more
// DSNs separated by whitespace (one per engine version). Unset, the
// integration tests skip, unless DOGER_SQL_REQUIRE=1, which turns a
// missing DSN into a failure. `make test-sql` sets all three.
const (
	envPostgresDSN = "DOGER_SQL_POSTGRES_DSN"
	envMySQLDSN    = "DOGER_SQL_MYSQL_DSN"
	envRequire     = "DOGER_SQL_REQUIRE"
)

// engine is one configured database server.
type engine struct {
	name   string // "postgres17", "mysql84", ...
	kind   string // "postgres" or "mysql"
	driver string // the database/sql driver name
	dsn    string
}

var (
	enginesOnce sync.Once
	allEngines  []engine
	enginesErr  error
)

// configuredEngines returns the engines of the environment, named by
// their server version, probing each once per test binary.
func configuredEngines() ([]engine, error) {
	enginesOnce.Do(func() {
		for dsn := range strings.FieldsSeq(os.Getenv(envPostgresDSN)) {
			e, err := probe(engine{kind: "postgres", driver: "pgx", dsn: dsn})
			if err != nil {
				enginesErr = err
				return
			}
			allEngines = append(allEngines, e)
		}
		for dsn := range strings.FieldsSeq(os.Getenv(envMySQLDSN)) {
			e, err := probe(engine{kind: "mysql", driver: "mysql", dsn: dsn})
			if err != nil {
				enginesErr = err
				return
			}
			allEngines = append(allEngines, e)
		}
	})

	return allEngines, enginesErr
}

// probe names e after its server's major and minor version.
func probe(e engine) (engine, error) {
	db, err := sql.Open(e.driver, e.dsn)
	if err != nil {
		return e, fmt.Errorf("open %s: %w", e.kind, err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var version string
	switch e.kind {
	case "postgres":
		if err := db.QueryRowContext(ctx, "SHOW server_version_num").Scan(&version); err != nil {
			return e, fmt.Errorf("probe %s: %w", e.kind, err)
		}
		n, _ := strconv.Atoi(version)
		e.name = fmt.Sprintf("postgres%d", n/10000)
	default:
		if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
			return e, fmt.Errorf("probe %s: %w", e.kind, err)
		}
		parts := strings.SplitN(version, ".", 3)
		e.name = "mysql" + parts[0] + parts[1]
	}

	return e, nil
}

// testDB is an isolated schema (PostgreSQL) or database (MySQL) of one
// test on one engine, dropped at the end of the test.
type testDB struct {
	engine
	DB     *sql.DB
	schema string
}

// forEachEngine runs fn as a parallel subtest per configured engine, each
// with a fresh testDB. kinds limits the engines ("postgres", "mysql"); none
// means all.
func forEachEngine(t *testing.T, fn func(t *testing.T, db *testDB), kinds ...string) {
	t.Helper()
	engines, err := configuredEngines()
	if err != nil {
		t.Fatalf("integration databases: %v", err)
	}
	if len(engines) == 0 {
		if os.Getenv(envRequire) == "1" {
			t.Fatalf("%s=1 but neither %s nor %s is set", envRequire, envPostgresDSN, envMySQLDSN)
		}
		t.Skipf("set %s or %s to run the integration tests (make test-sql)", envPostgresDSN, envMySQLDSN)
	}
	ran := false
	for _, e := range engines {
		if len(kinds) > 0 && !slices.Contains(kinds, e.kind) {
			continue
		}
		ran = true
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()
			fn(t, newTestDB(t, e))
		})
	}
	if !ran {
		t.Skipf("no configured engine of kinds %v", kinds)
	}
}

// newTestDB creates a schema or database with a random name on e and
// returns a *sql.DB using it.
func newTestDB(t *testing.T, e engine) *testDB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := sql.Open(e.driver, e.dsn)
	if err != nil {
		t.Fatalf("open %s: %v", e.name, err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	var raw [6]byte
	_, _ = rand.Read(raw[:])
	schema := "t_" + hex.EncodeToString(raw[:])
	// schema is a random hex name, never input
	create, drop := "CREATE SCHEMA "+schema, "DROP SCHEMA "+schema+" CASCADE" //nolint:gosec // G202: schema is a random hex name
	if e.kind == "mysql" {
		create, drop = "CREATE DATABASE "+schema, "DROP DATABASE "+schema
	}
	if _, err := admin.ExecContext(ctx, create); err != nil {
		t.Fatalf("%s: %s: %v", e.name, create, err)
	}

	db, err := sql.Open(e.driver, scopedDSN(t, e, schema))
	if err != nil {
		t.Fatalf("open %s: %v", e.name, err)
	}
	db.SetMaxOpenConns(16)
	// registered after the admin cleanup, so it runs first: close the
	// scoped pool, then drop the schema with the admin one
	t.Cleanup(func() {
		_ = db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, drop); err != nil {
			t.Errorf("%s: %s: %v", e.name, drop, err)
		}
	})

	return &testDB{engine: e, DB: db, schema: schema}
}

// scopedDSN returns e's DSN using schema: search_path on PostgreSQL, the
// database name on MySQL.
func scopedDSN(t *testing.T, e engine, schema string) string {
	t.Helper()
	if e.kind == "mysql" {
		cfg, err := gomysql.ParseDSN(e.dsn)
		if err != nil {
			t.Fatalf("parse %s DSN: %v", e.name, err)
		}
		cfg.DBName = schema
		return cfg.FormatDSN()
	}
	if !strings.Contains(e.dsn, "://") { // keyword/value form
		return e.dsn + " search_path=" + schema
	}
	u, err := url.Parse(e.dsn)
	if err != nil {
		t.Fatalf("parse %s DSN: %v", e.name, err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	return u.String()
}

// Rebind turns the ? placeholders of a test query into $n on PostgreSQL.
func (db *testDB) Rebind(query string) string {
	if db.kind != "postgres" {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}

	return b.String()
}

// Exec runs a test query, failing the test on error.
func (db *testDB) Exec(t *testing.T, query string, args ...any) sql.Result {
	t.Helper()
	res, err := db.DB.ExecContext(context.Background(), db.Rebind(query), args...)
	if err != nil {
		t.Fatalf("%s: %s: %v", db.name, query, err)
	}
	return res
}

// Count runs a COUNT query, failing the test on error.
func (db *testDB) Count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.DB.QueryRowContext(context.Background(), db.Rebind(query), args...).Scan(&n); err != nil {
		t.Fatalf("%s: %s: %v", db.name, query, err)
	}
	return n
}

// Dialect returns the dialect of the engine.
func (db *testDB) Dialect() eventsql.Dialect {
	if db.kind == "mysql" {
		return mysql.Dialect()
	}
	return postgres.Dialect()
}

// Transport returns a transport on db, failing the test on error.
func (db *testDB) Transport(t *testing.T, opts ...eventsql.Option) *eventsql.Transport {
	t.Helper()
	tr, err := eventsql.New(db.DB, db.Dialect(), opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr
}

// Now is the SQL of the database time as the transport stores it.
func (db *testDB) Now() string {
	if db.kind == "mysql" {
		return "UTC_TIMESTAMP(6)"
	}
	return "now()"
}

// Interval is the SQL of a duration, to add to or subtract from Now.
func (db *testDB) Interval(d time.Duration) string {
	if db.kind == "mysql" {
		return "INTERVAL " + strconv.FormatInt(d.Microseconds(), 10) + " MICROSECOND"
	}
	return "(" + strconv.FormatInt(d.Microseconds(), 10) + " * INTERVAL '1 microsecond')"
}
