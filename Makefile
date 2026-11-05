SQLC       ?= go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest
MIGRATE_CMD := go run ./cmd/migrate
MIGRATE_EE_CMD := go run ./ee/cmd/migrate
DSN        ?= nipa.db
MIGRATIONS_DIR := db/migrations
POSTGRES_MIGRATIONS_DIR := ee/db/migrations/postgres
mb         ?= 256
gb         ?= 1
MARKETING_DIR ?= ../nipa-web

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null)
REVISION   ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS    := $(if $(VERSION),-X 'github.com/nipalab/nipa/internal/version.Version=$(VERSION)') \
              $(if $(REVISION),-X 'github.com/nipalab/nipa/internal/version.Revision=$(REVISION)')

.PHONY: sqlc mock migrate-up migrate-down migrate-up-postgres migrate-down-postgres migrate-create build build-all build-server build-server-ee build-client test test-ee test-client test-client-ee test-client-both lint web web-dev web-install desktop desktop-dev desktop-release desktop-test marketing-dist bench-upload bench-download bench-scale bench-scale-files bench-scale-files-ee bench-unit fuzz load-test release

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
	go build -ldflags "$(LDFLAGS)" -o bin/nipad ./cmd/nipad

build-server-ee:
	go build -ldflags "$(LDFLAGS)" -o bin/nipad-ee ./ee/cmd/nipad

build-client:
	go build -ldflags "$(LDFLAGS)" -o bin/nipa ./cmd/nipa

## Install web UI dependencies (first time, or after changes to web/package.json).
web-install:
	cd web && npm install

## Build the Astro marketing site (nipa-web) and sync it into
## ee/web/marketing/dist for the embedded fallback. The production site is
## Netlify behind the Caddy proxy and needs no rebuild.
marketing-dist:
	@test -n "$(MARKETING_DIR)" || (echo "usage: make marketing-dist MARKETING_DIR=/path/to/nipa-web" && exit 1)
	@test -f "$(MARKETING_DIR)/package.json" || (echo "marketing repo not found at $(MARKETING_DIR)" && exit 1)
	cd "$(MARKETING_DIR)" && npm install && MARKETING_BASE=/web/ npm run build
	rsync -a --delete "$(MARKETING_DIR)/dist/" ee/web/marketing/dist/
	@test -f ee/web/marketing/dist/index.html

## Build the web UI into web/server/dist, embedded into the nipad binary via go:embed.
web:
	cd web && npm run build

## Run the Vite dev server: proxies /docs and /api to a running nipad
## (default http://localhost:6745, override with NIPA_SERVER_URL).
web-dev:
	cd web && npm run dev

## Desktop app (desktop/, nested Go module). Needs the wails3 CLI:
## cd desktop && go -C build/tools install tool (add -tags gtk3 on
## Linux hosts that only ship WebKitGTK 4.1). The nipa CLI is copied next to
## the app binary, which is where the app looks for it to start the daemon.
desktop: build-client
	cd desktop && wails3 build
	cp bin/nipa desktop/bin/nipa

## Run the desktop app with hot reload against the freshly built nipa CLI.
desktop-dev: build-client
	cd desktop && NIPA_BIN=$(CURDIR)/bin/nipa wails3 dev

## Release artifacts for this OS into desktop/bin/release (update archive +
## installers); see docs/DESKTOP.md "Packaging and updates".
desktop-release:
	cd desktop && NIPA_RELEASE_VERSION=$(VERSION) wails3 task release

## Desktop Go + frontend tests (typecheck, vitest).
desktop-test:
	cd desktop && wails3 task test

## Build the free server including the web UI.
build: web build-server build-client

## Build both the free and enterprise servers including the web UI.
build-all: build build-server-ee

## Tag a release on a clean HEAD, e.g. `make release version=v0.1.0`.
release:
	@test -n "$(version)" || (echo "usage: make release version=v0.1.0" && exit 1)
	@echo "$(version)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$$' || (echo "version must look like vX.Y.Z (got '$(version)')" && exit 1)
	@git diff-index --quiet HEAD -- || (echo "working tree is dirty; commit or stash first" && exit 1)
	@if git rev-parse -q --verify "refs/tags/$(version)" >/dev/null; then echo "tag $(version) already exists"; exit 1; fi
	git tag -a "$(version)" -m "Release $(version)"
	@echo "created tag $(version); publish it with: git push origin $(version)"

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

## Measure clone/download throughput with NIPA_BENCH_MB MiB files (e.g. make bench-download mb=512).
bench-download:
	NIPA_BENCH_MB=$(mb) go test ./internal/e2e -run TestEndToEnd_CloneThroughput -v -count=1

## Push and clone one NIPA_BENCH_GB GiB file, reporting peak heap against the file size (e.g. make bench-scale gb=2).
bench-scale:
	NIPA_BENCH_GB=$(gb) go test ./internal/e2e -run TestEndToEnd_GBScaleMemoryCeiling -v -count=1

## Wide-tree scale on SQLite: push, manifest, diff, ApplyPush and SaveTree for N files (e.g. make bench-scale-files files=10000).
bench-scale-files:
	NIPA_BENCH_FILES=$(files) go test ./internal/e2e -run TestEndToEnd_WideTreeScale -v -count=1
	NIPA_BENCH_FILES=$(files) go test ./internal/repository/sqlite -run ApplyPushScale -v -count=1
	go test ./internal/client/localrepo -run '^$$' -bench SaveTree -benchtime=5x -benchmem

## Wide-tree scale on postgres + MinIO (needs Docker): push, manifest, diff and ApplyPush for N files.
bench-scale-files-ee:
	NIPA_BENCH_FILES=$(files) go test ./ee/e2e -run TestEnterpriseWideTreeScale -v -count=1
	NIPA_BENCH_FILES=$(files) go test ./ee/repository/postgres -run 'TestPushRepositorySuite/TestApplyPushScale' -v -count=1

## Run the package micro-benchmarks (chunker, diff, treehash).
bench-unit:
	go test ./internal/chunker ./internal/diff ./internal/treehash -run '^$$' -bench=. -benchmem

FUZZTIME ?= 10s

## Run every fuzz target for FUZZTIME each (e.g. make fuzz FUZZTIME=1m). Seed corpora also run in plain `go test`.
fuzz:
	go test ./internal/chunker -run '^$$' -fuzz '^FuzzEncodeDecodeRoundTrip$$' -fuzztime $(FUZZTIME)
	go test ./internal/diff -run '^$$' -fuzz '^FuzzLinesScript$$' -fuzztime $(FUZZTIME)
	go test ./internal/merge -run '^$$' -fuzz '^FuzzMergeTextUnchangedSide$$' -fuzztime $(FUZZTIME)
	go test ./internal/client/ignore -run '^$$' -fuzz '^FuzzCompileAndMatch$$' -fuzztime $(FUZZTIME)
	go test ./internal/client/domain -run '^$$' -fuzz '^FuzzParseNipaUrl$$' -fuzztime $(FUZZTIME)

## Run load scenarios against a running server (e.g. make load-test args='-scenario push -clients 16').
load-test:
	go run ./tests/load $(args)

## Lint the code via golangci-lint's Docker image.
lint:
	docker run --rm -v $(CURDIR):/app -w /app $(GOLANGCI_LINT_IMAGE) golangci-lint run ./...
