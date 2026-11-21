.PHONY: build test test-sql test-sql-down watch-test bench watch-bench coverage tools lint lint-fix modules test-all lint-all vuln govulncheck-install audit outdated weight doc

build:
	go build -v ./...

# root module only, for fast local runs; test-all covers the workspace
test:
	go test -race ./...
# integration tests of events/sql against PostgreSQL 17 and 13 and MySQL
# 8.4 and 8.0 in Docker (events/sql/compose.yaml); the containers keep
# running for the next run until make test-sql-down. Point the DSN
# variables at other servers to test those instead.
SQL_COMPOSE := events/sql/compose.yaml
SQL_POSTGRES_DSN ?= postgres://postgres:doger@127.0.0.1:54317/doger?sslmode=disable postgres://postgres:doger@127.0.0.1:54313/doger?sslmode=disable
SQL_MYSQL_DSN ?= root:doger@tcp(127.0.0.1:33084)/doger root:doger@tcp(127.0.0.1:33080)/doger

test-sql:
	docker compose -f $(SQL_COMPOSE) up -d --wait
	cd events/sql && DOGER_SQL_REQUIRE=1 DOGER_SQL_POSTGRES_DSN="$(SQL_POSTGRES_DSN)" DOGER_SQL_MYSQL_DSN="$(SQL_MYSQL_DSN)" go test -race -count=1 ./...
test-sql-down:
	docker compose -f $(SQL_COMPOSE) down -v

watch-test:
	reflex -t 50ms -s -- sh -c 'gotest -race ./...'

bench:
	go test -benchmem -count 3 -bench ./...
watch-bench:
	reflex -t 50ms -s -- sh -c 'go test -benchmem -count 3 -bench ./...'

coverage:
	go test -v -coverprofile=cover.out -covermode=atomic ./...
	go tool cover -html=cover.out -o cover.html

# tools, pinned to exact versions: bump a version here deliberately.
# GOLANGCI_LINT_VERSION matches the version in .github/workflows/lint.yml.
GOLANGCI_LINT_VERSION := v2.15.0
GOVULNCHECK_VERSION := v1.10.0
BENCHSTAT_VERSION := v0.0.0-20261009192801-be2c69fb417e

tools: govulncheck-install
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install golang.org/x/perf/cmd/benchstat@$(BENCHSTAT_VERSION)
	go install github.com/cespare/reflex@v0.3.2
	go install github.com/rakyll/gotest@v0.0.7
	go install github.com/psampaz/go-mod-outdated@v0.9.0
	go install github.com/jondot/goweight@v1.0.5
	go install github.com/sonatype-nexus-community/nancy@v1.2.0
	go install github.com/cespare/prettybench@v0.1.0

lint:
	golangci-lint run --timeout 60s --max-same-issues 50 ./...
lint-fix:
	golangci-lint run --timeout 60s --max-same-issues 50 --fix ./...

# Every workspace module (each go.work use entry). Each loop runs every
# module even after a failure, then names the failing modules and exits
# non-zero. LINT_FLAGS adds golangci-lint flags (for example
# --allow-parallel-runners).
LINT_FLAGS ?=

modules:
	@scripts/modules.sh list

test-all:
	@scripts/modules.sh run go test -race ./...

lint-all:
	@scripts/modules.sh run golangci-lint run --timeout 120s --max-same-issues 50 --config "$(CURDIR)/.golangci.yml" $(LINT_FLAGS) ./...

# fails on a reachable vulnerability unless SECURITY.md accepts it
# (scripts/govulncheck.sh)
vuln:
	@scripts/modules.sh run "$(CURDIR)/scripts/govulncheck.sh"

govulncheck-install:
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

audit:
	go mod tidy
	go list -json -m all | nancy sleuth

outdated:
	go mod tidy
	go list -u -m -json all | go-mod-outdated -update -direct

weight:
	goweight

doc:
	cd docs && npm install && npm start
