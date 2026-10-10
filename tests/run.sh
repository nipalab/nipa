#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TESTS_DIR="$ROOT_DIR/tests"
TMP_DIR="${NIPA_TEST_TMP_DIR:-$TESTS_DIR/.tmp}"
LOG_DIR="$TMP_DIR/logs"
COMPOSE_FILE="$TESTS_DIR/docker-compose.ee.yml"

FREE_PORT="${NIPA_TEST_PORT:-6745}"
EE_PORT="${NIPA_TEST_EE_PORT:-6747}"
PG_PORT="${NIPA_TEST_PG_PORT:-55432}"
S3_PORT="${NIPA_TEST_S3_PORT:-55900}"
S3_BUCKET="${NIPA_TEST_S3_BUCKET:-nipa}"
S3_ACCESS_KEY="${NIPA_TEST_S3_ACCESS_KEY:-minioadmin}"
S3_SECRET_KEY="${NIPA_TEST_S3_SECRET_KEY:-minioadmin}"

DOCKER="${NIPA_TEST_DOCKER:-docker}"
EMAIL_TRANSPORT="${NIPA_TEST_EMAIL_TRANSPORT:-sendgrid}"
SERVER_PID=""
COMPOSE_STARTED=0
PG_DSN=""
S3_ENDPOINT=""

usage() {
	cat <<'EOF'
Usage: tests/run.sh [free|ee|both] [options]

Runs the Ginkgo client e2e suite against a real nipad server (default: free).

Options:
  --keep             Keep postgres/minio containers running after an ee run
  --filter <focus>   Ginkgo focus regex passed to the suite
  -h, --help         Show this help

Environment:
  SKIP_BUILD=1              Reuse existing binaries in bin/
  NIPA_TEST_PORT=6745       Free server port
  NIPA_TEST_EE_PORT=6747    Enterprise server port
  NIPA_TEST_EMAIL_PORT      Email receiver port (default: server port + 1)
  NIPA_TEST_EMAIL_TRANSPORT Email transport exercised (sendgrid or http)
  NIPA_TEST_PG_PORT=55432   Postgres host port
  NIPA_TEST_S3_PORT=55900   MinIO host port
  NIPA_TEST_POSTGRES_DSN    External postgres DSN (with NIPA_TEST_S3_ENDPOINT)
  NIPA_TEST_S3_ENDPOINT     External S3 endpoint (with NIPA_TEST_POSTGRES_DSN)
  NIPA_TEST_USER/PASS       Seeded super admin credentials
EOF
}

log() { printf '\n==> %s\n' "$*"; }

stop_server() {
	if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
		kill "$SERVER_PID" 2>/dev/null || true
		wait "$SERVER_PID" 2>/dev/null || true
	fi
	SERVER_PID=""
}

stop_compose() {
	if [[ "$COMPOSE_STARTED" == "1" && "$KEEP" != "1" ]]; then
		"$DOCKER" compose -f "$COMPOSE_FILE" down -v >/dev/null 2>&1 || true
	fi
	COMPOSE_STARTED=0
}

cleanup() {
	stop_server
	stop_compose
}
trap cleanup EXIT

build_binaries() {
	local edition="$1"
	log "building client and $edition server"
	(cd "$ROOT_DIR" && go build -o bin/nipa ./cmd/nipa)
	if [[ "$edition" == "ee" ]]; then
		(cd "$ROOT_DIR" && go build -o bin/nipad-ee ./ee/cmd/nipad)
	else
		(cd "$ROOT_DIR" && go build -o bin/nipad ./cmd/nipad)
	fi
}

email_port_for() {
	echo "${NIPA_TEST_EMAIL_PORT:-$(( $1 + 1 ))}"
}

write_config() {
	local edition="$1" port="$2" dir="$3"
	mkdir -p "$dir"

	local email_port
	email_port="$(email_port_for "$port")"

	local email_lines
	case "$EMAIL_TRANSPORT" in
	sendgrid)
		email_lines=$(cat <<EOF
EMAIL_SENDER: sendgrid
EMAIL_SENDGRID_API_KEY: 'client-e2e-sendgrid-key'
EMAIL_SENDGRID_ENDPOINT: 'http://127.0.0.1:$email_port'
EOF
)
		;;
	http)
		# The generic REST sender is configured to emit the same SendGrid v3
		# shape the in-suite receiver decodes, so both transports exercise the
		# same scenarios.
		email_lines=$(cat <<EOF
EMAIL_SENDER: http
EMAIL_HTTP_ENDPOINT: 'http://127.0.0.1:$email_port/email'
EMAIL_HTTP_BODY_TEMPLATE: '{"personalizations":[{"to":[{"email":{{json (index .To 0)}}}],"headers":{"Message-ID":{{json .MessageID}},"In-Reply-To":{{json .InReplyTo}}}}],"from":{"email":{{json .FromEmail}}},"subject":{{json .Subject}},"content":[{"type":"text/plain","value":{{json .Text}}},{"type":"text/html","value":{{json .HTML}}}]}'
EOF
)
		;;
	*)
		echo "unknown NIPA_TEST_EMAIL_TRANSPORT $EMAIL_TRANSPORT (expected sendgrid or http)" >&2
		exit 1
		;;
	esac

	local db_dsn chunk_lines
	if [[ "$edition" == "ee" ]]; then
		db_dsn="$PG_DSN"
		chunk_lines=$(cat <<EOF
CHUNK_STORAGE: s3
CHUNK_S3_ENDPOINT: '$S3_ENDPOINT'
CHUNK_S3_REGION: ''
CHUNK_S3_BUCKET: '$S3_BUCKET'
CHUNK_S3_PREFIX: 'client-e2e'
CHUNK_S3_ACCESS_KEY_ID: '$S3_ACCESS_KEY'
CHUNK_S3_SECRET_ACCESS_KEY: '$S3_SECRET_KEY'
EOF
)
	else
		db_dsn="$dir/nipa.db"
		chunk_lines=$(cat <<EOF
CHUNK_STORAGE: local
CHUNK_STORAGE_DIR: '$dir/chunks'
EOF
)
	fi

	cat > "$dir/config.yaml" <<EOF
SERVER_ADDRESS: 127.0.0.1
SERVER_PORT: $port
DATABASE_DSN: '$db_dsn'
JWT_KEY: 'client-e2e-secret'
LOG_LEVEL: info
SNOWFLAKE_NODE_ID: 0
HASHER_WORKERS: 2
$chunk_lines
CHUNK_URL_SIGNING_KEY: 'client-e2e-chunk-signing-key'
CHUNK_PRESIGN_TTL_SECONDS: 3600
CHUNK_MAX_PAGE_SIZE: 1000
WEBHOOK_EGRESS_ALLOWLIST: '127.0.0.1'
WEBHOOK_TIMEOUT_SECONDS: 30
$email_lines
EMAIL_FROM: 'Nipa <noreply@example.com>'
EMAIL_BASE_URL: 'http://127.0.0.1:$port'
EMAIL_MAX_ATTEMPTS: 2
EMAIL_RETRY_BACKOFF_SECONDS: 1
EMAIL_POLL_SECONDS: 1
EOF
}

ensure_bucket() {
	log "ensuring s3 bucket $S3_BUCKET"
	(cd "$ROOT_DIR" && go run ./tests/cmd/s3bucket \
		-endpoint "$S3_ENDPOINT" \
		-bucket "$S3_BUCKET" \
		-access-key "$S3_ACCESS_KEY" \
		-secret-key "$S3_SECRET_KEY")
}

start_compose() {
	if [[ -n "${NIPA_TEST_POSTGRES_DSN:-}" || -n "${NIPA_TEST_S3_ENDPOINT:-}" ]]; then
		if [[ -z "${NIPA_TEST_POSTGRES_DSN:-}" || -z "${NIPA_TEST_S3_ENDPOINT:-}" ]]; then
			echo "NIPA_TEST_POSTGRES_DSN and NIPA_TEST_S3_ENDPOINT must be set together" >&2
			exit 1
		fi
		PG_DSN="$NIPA_TEST_POSTGRES_DSN"
		S3_ENDPOINT="$NIPA_TEST_S3_ENDPOINT"
		log "using external postgres and s3"
		ensure_bucket
		return
	fi

	log "starting postgres and minio containers"
	"$DOCKER" compose -f "$COMPOSE_FILE" up -d
	COMPOSE_STARTED=1

	log "waiting for postgres"
	local tries=0
	until "$DOCKER" compose -f "$COMPOSE_FILE" exec -T postgres pg_isready -U nipa -d nipa >/dev/null 2>&1; do
		tries=$((tries + 1))
		if [[ $tries -gt 60 ]]; then
			echo "postgres did not become ready" >&2
			exit 1
		fi
		sleep 1
	done

	log "waiting for minio"
	tries=0
	until curl -fsS "http://127.0.0.1:$S3_PORT/minio/health/live" >/dev/null 2>&1; do
		tries=$((tries + 1))
		if [[ $tries -gt 60 ]]; then
			echo "minio did not become ready" >&2
			exit 1
		fi
		sleep 1
	done

	PG_DSN="postgres://nipa:nipa@127.0.0.1:$PG_PORT/nipa?sslmode=disable"
	S3_ENDPOINT="http://127.0.0.1:$S3_PORT"

	ensure_bucket
}

check_port_free() {
	local port="$1"
	if curl -s -o /dev/null --max-time 1 "http://127.0.0.1:$port/api/v1/orgs" 2>/dev/null; then
		echo "port $port is already serving HTTP; stop it or override NIPA_TEST_PORT/NIPA_TEST_EE_PORT" >&2
		exit 1
	fi
}

start_server() {
	local edition="$1" port="$2" binary dir
	check_port_free "$port"
	check_port_free "$(email_port_for "$port")"
	if [[ "$edition" == "ee" ]]; then
		binary="$ROOT_DIR/bin/nipad-ee"
	else
		binary="$ROOT_DIR/bin/nipad"
	fi
	dir="$TMP_DIR/server-$edition"
	write_config "$edition" "$port" "$dir"

	log "starting $edition server on 127.0.0.1:$port"
	(
		cd "$dir"
		exec "$binary"
	) >"$LOG_DIR/$edition.log" 2>&1 &
	SERVER_PID=$!

	local tries=0
	until curl -s -o /dev/null --max-time 2 "http://127.0.0.1:$port/api/v1/orgs"; do
		if ! kill -0 "$SERVER_PID" 2>/dev/null; then
			echo "$edition server exited during startup; log tail:" >&2
			tail -n 40 "$LOG_DIR/$edition.log" >&2 || true
			exit 1
		fi
		tries=$((tries + 1))
		if [[ $tries -gt 60 ]]; then
			echo "$edition server did not become ready; log tail:" >&2
			tail -n 40 "$LOG_DIR/$edition.log" >&2 || true
			exit 1
		fi
		sleep 1
	done
	log "$edition server is ready"
}

run_suite() {
	local edition="$1" port="$2"
	export NIPA_TEST_HOST="127.0.0.1:$port"
	export NIPA_TEST_EDITION="$edition"
	export NIPA_TEST_EMAIL_PORT="$(email_port_for "$port")"
	export NIPA_TEST_BINARY="$ROOT_DIR/bin/nipa"
	export NIPA_TEST_API_URL="http://127.0.0.1:$port/api/v1"
	export NIPA_TEST_USER="${NIPA_TEST_USER:-nipa}"
	export NIPA_TEST_PASS="${NIPA_TEST_PASS:-nipa}"
	export NIPA_TOKEN_FILE="$TMP_DIR/tokens-$edition.json"
	rm -f "$NIPA_TOKEN_FILE"

	local args=(./tests/... -count=1 -v -timeout 20m)
	if [[ -n "$FOCUS" ]]; then
		args+=(-args "--ginkgo.focus=$FOCUS")
	fi

	log "running client e2e suite against $edition"
	(cd "$ROOT_DIR" && go test "${args[@]}")
}

main() {
	local edition="free"
	if [[ $# -gt 0 && ( "$1" == "free" || "$1" == "ee" || "$1" == "both" ) ]]; then
		edition="$1"
		shift
	fi

	KEEP=0
	FOCUS=""
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--keep) KEEP=1; shift ;;
			--filter) FOCUS="${2:-}"; shift 2 ;;
			--filter=*) FOCUS="${1#*=}"; shift ;;
			-h | --help)
				usage
				exit 0
				;;
			*)
				echo "unknown option: $1" >&2
				usage >&2
				exit 1
				;;
		esac
	done

	command -v go >/dev/null || {
		echo "go is required" >&2
		exit 1
	}
	command -v curl >/dev/null || {
		echo "curl is required" >&2
		exit 1
	}

	mkdir -p "$LOG_DIR"
	rm -rf "$TMP_DIR/server-free" "$TMP_DIR/server-ee"

	local editions=()
	case "$edition" in
		free) editions=(free) ;;
		ee) editions=(ee) ;;
		both) editions=(free ee) ;;
	esac

	if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
		local e
		for e in "${editions[@]}"; do
			build_binaries "$e"
		done
	fi
	[[ -x "$ROOT_DIR/bin/nipa" ]] || {
		echo "bin/nipa is missing; run without SKIP_BUILD=1" >&2
		exit 1
	}

	local e
	for e in "${editions[@]}"; do
		if [[ "$e" == "ee" ]]; then
			command -v "$DOCKER" >/dev/null || {
				echo "$DOCKER is required for the ee run" >&2
				exit 1
			}
			start_compose
			start_server ee "$EE_PORT"
			run_suite ee "$EE_PORT"
			stop_server
			stop_compose
		else
			start_server free "$FREE_PORT"
			run_suite free "$FREE_PORT"
			stop_server
		fi
	done

	log "client e2e suite passed ($edition)"
}

main "$@"
