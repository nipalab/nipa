SQLC       ?= go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest
MIGRATE_CMD := go run ./cmd/migrate
MIGRATE_EE_CMD := go run ./ee/cmd/migrate
DSN        ?= nipa.db
MIGRATIONS_DIR := db/migrations
POSTGRES_MIGRATIONS_DIR := ee/db/migrations/postgres
mb         ?= 256

.PHONY: sqlc mock migrate-up migrate-down migrate-up-postgres migrate-down-postgres migrate-create build build-all build-server build-server-ee build-client test test-ee test-client test-client-ee test-client-both lint web web-dev web-install bench-upload

GOLANGCI_LINT_IMAGE ?= docker.io/golangci/golangci-lint:latest

## Generate Go code from SQL queries via sqlc.
sqlc:
	$(SQLC) generate

## Regenerate all gomock mocks declared via //go:generate directives.
mock:
	go generate ./...

## Apply all pending sqlite migrations. Override DSN=... to point at another database file.
migrate-up:
	$(MIGRATE_CMD) -dialect sqlite3 -dsn $(DSN) -action up

## Revert all applied sqlite migrations. Override DSN=... to point at another database file.
migrate-down:
	$(MIGRATE_CMD) -dialect sqlite3 -dsn $(DSN) -action down

## Apply all pending postgres migrations, e.g. `make migrate-up-postgres DSN=postgres://...`.
migrate-up-postgres:
	$(MIGRATE_EE_CMD) -dsn "$(DSN)" -action up

## Revert all applied postgres migrations, e.g. `make migrate-down-postgres DSN=postgres://...`.
migrate-down-postgres:
	$(MIGRATE_EE_CMD) -dsn "$(DSN)" -action down

## Create a new pair of up/down migration files for both dialects, e.g. `make migrate-create name=add_users`.
migrate-create:
	@test -n "$(name)" || (echo "usage: make migrate-create name=<migration_name>" && exit 1)
	@ts=$$(date +%Y%m%d%H%M%S); \
	for dir in "$(MIGRATIONS_DIR)/sqlite" "$(POSTGRES_MIGRATIONS_DIR)"; do \
		touch "$$dir/$${ts}_$(name).up.sql" "$$dir/$${ts}_$(name).down.sql"; \
		echo "created $$dir/$${ts}_$(name).up.sql and .down.sql"; \
	done

build-server:
	go build -o bin/nipad ./cmd/nipad

build-server-ee:
	go build -o bin/nipad-ee ./ee/cmd/nipad

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

## Build the free server including the web UI.
build: web build-server build-client

## Build both the free and enterprise servers including the web UI.
build-all: build build-server-ee

proto:
	protoc --go_out=internal/grpc --go-grpc_out=internal/grpc internal/grpc/proto/server.proto
	protoc -I . --go_out=internal/client/grpc --go-grpc_out=internal/client/grpc \
		--go_opt=Minternal/grpc/proto/server.proto=github.com/nipalab/nipa/internal/grpc/pb \
		--go-grpc_opt=Minternal/grpc/proto/server.proto=github.com/nipalab/nipa/internal/grpc/pb \
		internal/client/grpc/proto/daemon.proto

## Run all tests (requires Docker for testcontainers-backed repository tests).
test:
	go test ./... -v

## Run the Enterprise Edition tests (requires Docker for testcontainers).
test-ee:
	go test ./ee/... -v

## Run the client e2e suite against the free server (see tests/README.md).
test-client:
	tests/run.sh free

## Run the client e2e suite against the enterprise server (starts postgres + minio).
test-client-ee:
	tests/run.sh ee

## Run the client e2e suite against both servers, free first.
test-client-both:
	tests/run.sh both

## Measure upload throughput with NIPA_BENCH_MB MiB files (e.g. make bench-upload mb=512).
bench-upload:
	NIPA_BENCH_MB=$(mb) go test ./internal/e2e -run TestEndToEnd_UploadThroughput -v -count=1

## Lint the code via golangci-lint's Docker image.
lint:
	docker run --rm -v $(CURDIR):/app -w /app $(GOLANGCI_LINT_IMAGE) golangci-lint run ./...
