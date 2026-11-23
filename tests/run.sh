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
RATELIMIT_PORT="${NIPA_TEST_RATELIMIT_PORT:-6749}"
MFAOFF_PORT="${NIPA_TEST_MFAOFF_PORT:-6751}"
INVITEONLY_PORT="${NIPA_TEST_INVITEONLY_PORT:-6753}"
REDIS_PORT="${NIPA_TEST_REDIS_PORT:-6379}"
REDIS_ADDR="${NIPA_TEST_REDIS_ADDR:-}"
S3_BUCKET="${NIPA_TEST_S3_BUCKET:-nipa}"
BYO_BUCKET="${NIPA_TEST_BYO_S3_BUCKET:-nipa-byo}"
S3_ACCESS_KEY="${NIPA_TEST_S3_ACCESS_KEY:-minioadmin}"
S3_SECRET_KEY="${NIPA_TEST_S3_SECRET_KEY:-minioadmin}"

DOCKER="${NIPA_TEST_DOCKER:-docker}"
EMAIL_TRANSPORT="${NIPA_TEST_EMAIL_TRANSPORT:-sendgrid}"
STORAGE_LEDGER="${NIPA_TEST_STORAGE_LEDGER-cloud}"
SAAS_MODE="${NIPA_TEST_SAAS-1}"
RATELIMIT_PROFILE="${NIPA_TEST_RATELIMIT:-0}"
# CHUNK_PACKS on nipad-ee (with CHUNK_UPLOAD_MODE=proxy): bundles are written
# as pack objects and clones read them with Range requests.
CHUNK_PACKS="${NIPA_TEST_CHUNK_PACKS:-0}"
# Deleted projects stay restorable this many days on nipad-ee (its purge
# retention); the free server never purges and ignores the setting.
PURGE_RETENTION_DAYS="${NIPA_TEST_PURGE_RETENTION_DAYS:-1}"
# Seals the secrets of bring-your-own storage servers (ee with SaaS + ledger).
BYO_KEY="Y2xpZW50LWUyZS1ieW8tc3RvcmFnZS1rZXktMDEyMzQ1Njc="
SERVER_PID=""
EXTRA_PIDS=()
RATELIMIT_HOST=""
MFAOFF_HOST=""
INVITEONLY_HOST=""
BYO_ENABLED=""
INVITEONLY_TERMS_VERSION="2026-01-01"
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
  NIPA_TEST_EMAIL_TRANSPORT Email transport exercised (sendgrid, scaleway or http)
  NIPA_TEST_STORAGE_LEDGER  Enterprise storage ledger (default: cloud; set to
                            empty to disable, so the storage spec is skipped)
  NIPA_TEST_SAAS            SaaS onboarding routes (default: 1; set to empty
                            to disable, so the onboarding spec is skipped).
                            Inert on the free server: no enterprise package is
                            wired, which the spec asserts as 404s
  NIPA_TEST_GINKGO_ARGS     Extra Ginkgo flags (e.g. "--ginkgo.v")
  NIPA_TEST_PG_PORT=55432   Postgres host port
  NIPA_TEST_S3_PORT=55900   MinIO host port
  NIPA_TEST_POSTGRES_DSN    External postgres DSN (with NIPA_TEST_S3_ENDPOINT)
  NIPA_TEST_S3_ENDPOINT     External S3 endpoint (with NIPA_TEST_POSTGRES_DSN)
  NIPA_TEST_USER/PASS       Seeded super admin credentials
  NIPA_TEST_RATELIMIT=1     Rate-limit profile (both editions): also start a
                            server of the edition under test with low
                            per-scope limits on NIPA_TEST_RATELIMIT_PORT
                            (default 6749) and focus the run on the rate
                            limiting specs unless --filter is given
  NIPA_TEST_MFAOFF_PORT=6751 Every run: a second server of the edition under
                            test with MFA_ENABLED=false for the two-factor-off
                            spec
  NIPA_TEST_INVITEONLY_PORT=6753 Every ee run with NIPA_TEST_SAAS: a nipad-ee
                            with REGISTRATION_ENABLED=false and a terms
                            version for the waitlist/invite/terms specs
  NIPA_TEST_PURGE_RETENTION_DAYS=1 nipad-ee STORAGE_PURGE_RETENTION_DAYS (the
                            restore window the project lifecycle spec checks)
  NIPA_TEST_BYO_S3_BUCKET=nipa-byo Second MinIO bucket the ee run registers as
                            a bring-your-own storage server
  NIPA_TEST_CHUNK_PACKS=1   nipad-ee with CHUNK_UPLOAD_MODE=proxy and
                            CHUNK_PACKS=on (bundles stored as packs, clones
                            by range GETs); inert on the free server

The extra nipad-ee instances share the postgres server, MinIO and redis but
each gets its own database (tests/cmd/pgdatabase), S3 prefix and rate-limit
redis prefix, so their background jobs never compete with the main server.
EOF
}

log() { printf '\n==> %s\n' "$*"; }

stop_server() {
	if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
		kill "$SERVER_PID" 2>/dev/null || true
		wait "$SERVER_PID" 2>/dev/null || true
	fi
	SERVER_PID=""
	local pid
	for pid in "${EXTRA_PIDS[@]}"; do
		if kill -0 "$pid" 2>/dev/null; then
			kill "$pid" 2>/dev/null || true
			wait "$pid" 2>/dev/null || true
		fi
	done
	EXTRA_PIDS=()
	RATELIMIT_HOST=""
	MFAOFF_HOST=""
	INVITEONLY_HOST=""
	BYO_ENABLED=""
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
	local edition="$1" port="$2" dir="$3" profile="${4:-}" profile_dsn="${5:-}"
	mkdir -p "$dir"

	# Extra nipad-ee instances mail through the suite's receiver of the main
	# server, so the invite-only waitlist/sign-up emails can be asserted.
	local email_port
	if [[ "$edition" == "ee" && -n "$profile" ]]; then
		email_port="$(email_port_for "$EE_PORT")"
	else
		email_port="$(email_port_for "$port")"
	fi

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
	scaleway)
		email_lines=$(cat <<EOF
EMAIL_SENDER: scaleway
EMAIL_SCALEWAY_API_KEY: 'client-e2e-scaleway-key'
EMAIL_SCALEWAY_PROJECT_ID: '11111111-2222-3333-4444-555555555555'
EMAIL_SCALEWAY_ENDPOINT: 'http://127.0.0.1:$email_port'
EOF
)
		;;
	http)
		# The generic REST sender is configured to emit the same SendGrid v3
		# shape the in-suite receiver decodes, so every transport exercises the
		# same scenarios.
		email_lines=$(cat <<EOF
EMAIL_SENDER: http
EMAIL_HTTP_ENDPOINT: 'http://127.0.0.1:$email_port/email'
EMAIL_HTTP_BODY_TEMPLATE: '{"personalizations":[{"to":[{"email":{{json (index .To 0)}}}],"headers":{"Message-ID":{{json .MessageID}},"In-Reply-To":{{json .InReplyTo}}}}],"references":{{json .References}},"from":{"email":{{json .FromEmail}}},"subject":{{json .Subject}},"content":[{"type":"text/plain","value":{{json .Text}}},{"type":"text/html","value":{{json .HTML}}}]}'
EOF
)
		;;
	*)
		echo "unknown NIPA_TEST_EMAIL_TRANSPORT $EMAIL_TRANSPORT (expected sendgrid, scaleway or http)" >&2
		exit 1
		;;
	esac

	local db_dsn chunk_lines
	if [[ "$edition" == "ee" ]]; then
		db_dsn="${profile_dsn:-$PG_DSN}"
		chunk_lines=$(cat <<EOF
CHUNK_STORAGE: s3
CHUNK_S3_ENDPOINT: '$S3_ENDPOINT'
CHUNK_S3_REGION: ''
CHUNK_S3_BUCKET: '$S3_BUCKET'
CHUNK_S3_PREFIX: 'client-e2e${profile:+-$profile}'
CHUNK_S3_ACCESS_KEY_ID: '$S3_ACCESS_KEY'
CHUNK_S3_SECRET_ACCESS_KEY: '$S3_SECRET_KEY'
STORAGE_PURGE_RETENTION_DAYS: $PURGE_RETENTION_DAYS
EOF
)
		if [[ "$CHUNK_PACKS" == "1" ]]; then
			chunk_lines="$chunk_lines
CHUNK_UPLOAD_MODE: proxy
CHUNK_PACKS: 'on'"
		fi
		if [[ -n "$STORAGE_LEDGER" ]]; then
			chunk_lines="$chunk_lines
STORAGE_LEDGER: '$STORAGE_LEDGER'
STORAGE_LEDGER_DEFAULT_QUOTA_BYTES: 0"
			if [[ -n "$SAAS_MODE" && -z "$profile" ]]; then
				# Bring-your-own storage needs SaaS mode and the ledger; the
				# loopback MinIO is a private address, hence ALLOW_PRIVATE.
				chunk_lines="$chunk_lines
BYO_STORAGE_ENCRYPTION_KEY: '$BYO_KEY'
BYO_STORAGE_ALLOW_PRIVATE: true"
				BYO_ENABLED=1
			fi
		fi
	else
		db_dsn="$dir/nipa.db"
		chunk_lines=$(cat <<EOF
CHUNK_STORAGE: local
CHUNK_STORAGE_DIR: '$dir/chunks'
EOF
)
	fi

	# The suite hammers /auth and the API from one address with a handful of
	# identities, so every scope stays generous except auth_id (keyed per email
	# and client IP), which the rate limit spec exhausts with a throwaway email.
	local rate_limit_lines="RATE_LIMIT_AUTH_IP_PER_MINUTE: 600000
RATE_LIMIT_AUTH_IP_BURST: 100000
RATE_LIMIT_AUTH_ID_PER_MINUTE: 1
RATE_LIMIT_AUTH_ID_BURST: 60
RATE_LIMIT_AUTH_EMAIL_PER_MINUTE: 600000
RATE_LIMIT_AUTH_EMAIL_BURST: 100000
RATE_LIMIT_REGISTER_EMAIL_PER_MINUTE: 600000
RATE_LIMIT_REGISTER_EMAIL_BURST: 100000
RATE_LIMIT_API_USER_PER_MINUTE: 600000
RATE_LIMIT_API_USER_BURST: 100000
RATE_LIMIT_CHUNK_IP_PER_MINUTE: 6000000
RATE_LIMIT_CHUNK_IP_BURST: 1000000
RATE_LIMIT_GLOBAL_IP_PER_MINUTE: 6000000
RATE_LIMIT_GLOBAL_IP_BURST: 1000000
RATE_LIMIT_INVITE_USER_PER_MINUTE: 600000
RATE_LIMIT_INVITE_USER_BURST: 100000
RATE_LIMIT_INVITE_EMAIL_PER_MINUTE: 600000
RATE_LIMIT_INVITE_EMAIL_BURST: 100000
RATE_LIMIT_INVITE_TOKEN_PER_MINUTE: 600000
RATE_LIMIT_INVITE_TOKEN_BURST: 100000"
	if [[ "$profile" == "ratelimit" ]]; then
		# Distinct bursts let the specs tell the scopes apart through
		# X-RateLimit-Limit; only 127.0.0.1 is trusted, so a 127.0.0.2 source
		# exercises the untrusted-peer path.
		rate_limit_lines="RATE_LIMIT_TRUSTED_PROXIES: '127.0.0.1/32'
RATE_LIMIT_AUTH_IP_PER_MINUTE: 4
RATE_LIMIT_AUTH_IP_BURST: 4
RATE_LIMIT_AUTH_ID_PER_MINUTE: 0.01
RATE_LIMIT_AUTH_ID_BURST: 3
RATE_LIMIT_AUTH_EMAIL_PER_MINUTE: 0.01
RATE_LIMIT_AUTH_EMAIL_BURST: 6
RATE_LIMIT_API_USER_PER_MINUTE: 6
RATE_LIMIT_API_USER_BURST: 6
RATE_LIMIT_CHUNK_IP_PER_MINUTE: 3
RATE_LIMIT_CHUNK_IP_BURST: 3
RATE_LIMIT_GLOBAL_IP_PER_MINUTE: 10
RATE_LIMIT_GLOBAL_IP_BURST: 10"
	fi
	if [[ "$edition" == "ee" && -n "$REDIS_ADDR" ]]; then
		# Every instance shares the redis server under its own key prefix, so
		# the low-limit buckets never see the main server's traffic.
		rate_limit_lines="$rate_limit_lines
RATE_LIMIT_STORE: redis
RATE_LIMIT_REDIS_ADDR: '$REDIS_ADDR'
RATE_LIMIT_REDIS_PREFIX: 'nipa:rl:client-e2e:${profile:-main}:'"
	fi

	local mfa_lines="MFA_ENABLED: true
MFA_ENCRYPTION_KEY: 'Y2xpZW50LWUyZS1tZmEta2V5LTAxMjM0NTY3ODlhYmM='"
	if [[ "$profile" == "mfaoff" ]]; then
		mfa_lines="MFA_ENABLED: false"
	fi

	local saas_lines=""
	if [[ -n "$SAAS_MODE" ]]; then
		# The free `nipad` wires no enterprise package, so SAAS_MODE is inert
		# there; the onboarding spec verifies the routes stay absent (404) on
		# free and work on ee. No OAuth credentials are configured, so social
		# providers stay disabled and `/auth/capabilities` reports an empty
		# provider list.
		saas_lines="SAAS_MODE: true
SAAS_FREE_QUOTA_BYTES: 16106127360
OAUTH_BASE_URL: 'http://127.0.0.1:$port'"
		if [[ "$profile" == "inviteonly" ]]; then
			# Sign-up limited to invited addresses (others join the waitlist)
			# and a published terms version every sign-up must accept.
			saas_lines="$saas_lines
REGISTRATION_ENABLED: false
SAAS_TERMS_VERSION: '$INVITEONLY_TERMS_VERSION'"
		fi
	fi

	cat > "$dir/config.yaml" <<EOF
SERVER_ADDRESS: 127.0.0.1
SERVER_PORT: $port
DATABASE_DSN: '$db_dsn'
JWT_KEY: 'client-e2e-jwt-secret-0123456789abcdef'
$mfa_lines
LOG_LEVEL: info
SNOWFLAKE_NODE_ID: 0
HASHER_WORKERS: 2
$chunk_lines
CHUNK_URL_SIGNING_KEY: 'client-e2e-chunk-signing-key-0123456789'
CHUNK_PRESIGN_TTL_SECONDS: 3600
CHUNK_MAX_PAGE_SIZE: 1000
WEBHOOK_EGRESS_ALLOWLIST: '127.0.0.1'
INTEGRATIONS_ENCRYPTION_KEY: 'Y2xpZW50LWUyZS1pbnRlZ3JhdGlvbnMta2V5LTAxMjM='
ISSUE_TRACKER_EGRESS_ALLOWLIST: '127.0.0.1'
WEBHOOK_TIMEOUT_SECONDS: 30
$email_lines
EMAIL_FROM: 'Nipa <noreply@example.com>'
EMAIL_BASE_URL: 'http://127.0.0.1:$port'
PUBLIC_BASE_URL: 'http://127.0.0.1:$port'
EMAIL_MAX_ATTEMPTS: 2
EMAIL_RETRY_BACKOFF_SECONDS: 1
EMAIL_POLL_SECONDS: 1
MAINTENANCE_FILE: '$dir/maintenance.json'
MAINTENANCE_POLL_SECONDS: 1
$saas_lines
$rate_limit_lines
EOF
}

ensure_bucket() {
	local bucket="${1:-$S3_BUCKET}"
	log "ensuring s3 bucket $bucket"
	(cd "$ROOT_DIR" && go run ./tests/cmd/s3bucket \
		-endpoint "$S3_ENDPOINT" \
		-bucket "$bucket" \
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
		ensure_bucket "$BYO_BUCKET"
		return
	fi

	log "starting postgres, redis and minio containers"
	[[ -n "$REDIS_ADDR" ]] || REDIS_ADDR="127.0.0.1:$REDIS_PORT"
	"$DOCKER" compose -f "$COMPOSE_FILE" up -d
	COMPOSE_STARTED=1

	log "waiting for postgres"
	local tries=0
	until "$DOCKER" compose -f "$COMPOSE_FILE" exec -T -e PGPASSWORD=nipa postgres pg_isready -h 127.0.0.1 -U nipa -d nipa >/dev/null 2>&1; do
		tries=$((tries + 1))
		if [[ $tries -gt 60 ]]; then
			echo "postgres did not become ready" >&2
			exit 1
		fi
		sleep 1
	done

	# Start from a clean schema every run: `compose down -v` does not remove
	# anonymous volumes on every runtime (rootless podman leaves them behind),
	# and the consolidated migration is edited in place during development.
	log "resetting postgres schema"
	"$DOCKER" compose -f "$COMPOSE_FILE" exec -T -e PGPASSWORD=nipa postgres \
		psql -h 127.0.0.1 -U nipa -d nipa -q -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;' >/dev/null

	log "waiting for redis"
	tries=0
	until "$DOCKER" compose -f "$COMPOSE_FILE" exec -T redis redis-cli ping >/dev/null 2>&1; do
		tries=$((tries + 1))
		if [[ $tries -gt 60 ]]; then
			echo "redis did not become ready" >&2
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
	ensure_bucket "$BYO_BUCKET"
}

# profile_database recreates the database of an extra nipad-ee instance on the
# shared postgres server and prints its DSN.
profile_database() {
	local profile="$1"
	(cd "$ROOT_DIR" && go run ./tests/cmd/pgdatabase -dsn "$PG_DSN" -name "nipa_e2e_$profile")
}

check_port_free() {
	local port="$1"
	if curl -s -o /dev/null --max-time 1 "http://127.0.0.1:$port/api/v1/orgs" 2>/dev/null; then
		echo "port $port is already serving HTTP; stop it or override NIPA_TEST_PORT/NIPA_TEST_EE_PORT" >&2
		exit 1
	fi
}

start_server() {
	local edition="$1" port="$2" profile="${3:-}" binary dir name pid profile_dsn=""
	check_port_free "$port"
	if [[ "$edition" == "ee" ]]; then
		binary="$ROOT_DIR/bin/nipad-ee"
	else
		binary="$ROOT_DIR/bin/nipad"
	fi
	if [[ "$edition" == "ee" && -n "$profile" ]]; then
		log "creating postgres database for the $profile instance"
		profile_dsn="$(profile_database "$profile")"
	else
		check_port_free "$(email_port_for "$port")"
	fi
	name="$edition${profile:+-$profile}"
	dir="$TMP_DIR/server-$name"
	write_config "$edition" "$port" "$dir" "$profile" "$profile_dsn"

	log "starting $name server on 127.0.0.1:$port"
	(
		cd "$dir"
		exec "$binary"
	) >"$LOG_DIR/$name.log" 2>&1 &
	pid=$!
	if [[ -n "$profile" ]]; then
		EXTRA_PIDS+=("$pid")
	else
		SERVER_PID=$pid
	fi

	local tries=0
	until curl -s -o /dev/null --max-time 2 "http://127.0.0.1:$port/api/v1/orgs"; do
		if ! kill -0 "$pid" 2>/dev/null; then
			echo "$name server exited during startup; log tail:" >&2
			tail -n 40 "$LOG_DIR/$name.log" >&2 || true
			exit 1
		fi
		tries=$((tries + 1))
		if [[ $tries -gt 60 ]]; then
			echo "$name server did not become ready; log tail:" >&2
			tail -n 40 "$LOG_DIR/$name.log" >&2 || true
			exit 1
		fi
		sleep 1
	done
	if [[ "$profile" == "ratelimit" ]]; then
		RATELIMIT_HOST="127.0.0.1:$port"
	elif [[ "$profile" == "mfaoff" ]]; then
		MFAOFF_HOST="127.0.0.1:$port"
	elif [[ "$profile" == "inviteonly" ]]; then
		INVITEONLY_HOST="127.0.0.1:$port"
	fi
	log "$name server is ready"
}

run_suite() {
	local edition="$1" port="$2" focus="${3-$FOCUS}"
	export NIPA_TEST_HOST="127.0.0.1:$port"
	export NIPA_TEST_EDITION="$edition"
	export NIPA_TEST_EMAIL_PORT="$(email_port_for "$port")"
	export NIPA_TEST_BINARY="$ROOT_DIR/bin/nipa"
	export NIPA_TEST_API_URL="http://127.0.0.1:$port/api/v1"
	# The suite feature-detects the ledger: on free the storage routes must be
	# absent (404), on ee they are registered when the ledger is enabled.
	export NIPA_TEST_STORAGE_LEDGER="$STORAGE_LEDGER"
	# Same feature detection for the SaaS onboarding routes (registration and
	# social login); they exist on ee only.
	export NIPA_TEST_SAAS="$SAAS_MODE"
	export NIPA_TEST_RATELIMIT=1
	export NIPA_TEST_RATELIMIT_HOST="$RATELIMIT_HOST"
	export NIPA_TEST_MFAOFF_HOST="$MFAOFF_HOST"
	# Invite-only sign-up with a terms version (ee with SaaS only).
	export NIPA_TEST_INVITEONLY_HOST="$INVITEONLY_HOST"
	export NIPA_TEST_INVITEONLY_TERMS_VERSION="$INVITEONLY_TERMS_VERSION"
	# The restore window of deleted projects: nipad-ee only, the free server
	# keeps deleted projects restorable.
	if [[ "$edition" == "ee" ]]; then
		export NIPA_TEST_PURGE_RETENTION_DAYS="$PURGE_RETENTION_DAYS"
	else
		export NIPA_TEST_PURGE_RETENTION_DAYS=""
	fi
	# Bring-your-own storage: a second MinIO bucket the suite registers.
	export NIPA_TEST_BYO="$BYO_ENABLED"
	export NIPA_TEST_BYO_S3_ENDPOINT="$S3_ENDPOINT"
	export NIPA_TEST_BYO_S3_BUCKET="$BYO_BUCKET"
	export NIPA_TEST_BYO_S3_ACCESS_KEY="$S3_ACCESS_KEY"
	export NIPA_TEST_BYO_S3_SECRET_KEY="$S3_SECRET_KEY"
	# The maintenance specs put the server in maintenance through its
	# MAINTENANCE_FILE override (a frozen window cannot be ended over REST).
	export NIPA_TEST_MAINTENANCE_FILE="$TMP_DIR/server-$edition/maintenance.json"
	rm -f "$NIPA_TEST_MAINTENANCE_FILE"
	export NIPA_TEST_USER="${NIPA_TEST_USER:-nipa}"
	export NIPA_TEST_PASS="${NIPA_TEST_PASS:-nipa}"
	export NIPA_TOKEN_FILE="$TMP_DIR/tokens-$edition.json"
	rm -f "$NIPA_TOKEN_FILE"

	local args=(./tests/... -count=1 -v -timeout 20m -args)
	if [[ -n "$focus" ]]; then
		args+=("--ginkgo.focus=$focus")
	fi
	# Extra Ginkgo flags, e.g. NIPA_TEST_GINKGO_ARGS="--ginkgo.v".
	if [[ -n "${NIPA_TEST_GINKGO_ARGS:-}" ]]; then
		local extra
		read -r -a extra <<<"$NIPA_TEST_GINKGO_ARGS"
		args+=("${extra[@]}")
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
	rm -rf "$TMP_DIR"/server-free "$TMP_DIR"/server-free-* "$TMP_DIR"/server-ee "$TMP_DIR"/server-ee-*

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
			local ee_focus="$FOCUS"
			start_compose
			start_server ee "$EE_PORT"
			start_server ee "$MFAOFF_PORT" mfaoff
			if [[ -n "$SAAS_MODE" ]]; then
				start_server ee "$INVITEONLY_PORT" inviteonly
			fi
			if [[ "$RATELIMIT_PROFILE" == "1" ]]; then
				start_server ee "$RATELIMIT_PORT" ratelimit
				[[ -n "$ee_focus" ]] || ee_focus="rate limiting"
			fi
			run_suite ee "$EE_PORT" "$ee_focus"
			stop_server
			stop_compose
		else
			local free_focus="$FOCUS"
			start_server free "$FREE_PORT"
			start_server free "$MFAOFF_PORT" mfaoff
			if [[ "$RATELIMIT_PROFILE" == "1" ]]; then
				start_server free "$RATELIMIT_PORT" ratelimit
				[[ -n "$free_focus" ]] || free_focus="rate limiting"
			fi
			run_suite free "$FREE_PORT" "$free_focus"
			stop_server
		fi
	done

	log "client e2e suite passed ($edition)"
}

main "$@"
