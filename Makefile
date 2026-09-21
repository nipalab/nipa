SQLC       ?= go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest
MIGRATE_CMD := go run ./cmd/migrate
DRIVER     ?= sqlite3
DSN        ?= nipa.db
MIGRATIONS_DIR := db/migrations

.PHONY: sqlc mock migrate-up migrate-down migrate-create build build-all test lint web web-dev web-install

GOLANGCI_LINT_IMAGE ?= docker.io/golangci/golangci-lint:latest

## Generate Go code from SQL queries via sqlc.
sqlc:
	$(SQLC) generate

## Regenerate all gomock mocks declared via //go:generate directives.
mock:
	go generate ./...

## Apply all pending migrations. Override DRIVER=postgres DSN=... for postgres.
migrate-up:
	$(MIGRATE_CMD) -dialect $(DRIVER) -dsn $(DSN) -action up

## Revert all applied migrations. Override DRIVER=postgres DSN=... for postgres.
migrate-down:
	$(MIGRATE_CMD) -dialect $(DRIVER) -dsn $(DSN) -action down

## Create a new pair of up/down migration files, e.g. `make migrate-create name=add_users`.
migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=<migration_name>" && exit 1)
	@ts=$$(date +%Y%m%d%H%M%S); \
	touch "$(MIGRATIONS_DIR)/$${ts}_$(name).up.sql" "$(MIGRATIONS_DIR)/$${ts}_$(name).down.sql"; \
	echo "created $(MIGRATIONS_DIR)/$${ts}_$(name).up.sql and .down.sql"

build:
	go build -o bin/nipad ./cmd/nipad

build-client:
	go build -o bin/nipa ./cmd/nipa

## Install web UI dependencies (first time, or after changes to web/package.json).
web-install:
	cd web && npm install

## Build the web UI into web/server/dist, embedded into the nipad binary via go:embed.
web:
	cd web && npm run build

## Run the Vite dev server: proxies /docs and /api to a running nipad
## (default http://localhost:6745, override with NIPA_SERVER_URL).
web-dev:
	cd web && npm run dev

## Build the server including the web UI.
build-all: web build build-client

proto:
	protoc --go_out=internal/grpc --go-grpc_out=internal/grpc internal/grpc/proto/server.proto

## Run all tests (requires Docker for testcontainers-backed repository tests).
test:
	go test ./... -v

## Lint the code via golangci-lint's Docker image.
lint:
	docker run --rm -v $(CURDIR):/app -w /app $(GOLANGCI_LINT_IMAGE) golangci-lint run ./...
