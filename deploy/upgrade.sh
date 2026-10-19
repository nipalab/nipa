#!/usr/bin/env bash
# Zero-downtime blue/green upgrade for nipad-ee behind Caddy on a single host.
#
#   deploy/upgrade.sh /path/to/nipad-ee [sha256]
#   deploy/upgrade.sh --rollback
#
# The script installs the new binary into the idle color, waits for its
# /readyz, flips the Caddy upstream, waits for the previous color to drain and
# stops it. Any failure before the flip leaves production running untouched.
#
# Environment overrides:
#   NIPA_BASE            /opt/nipa          installation root (per-color dirs)
#   NIPA_CONFIG_DIR      /etc/nipa          config.yaml, .env, <color>.env
#   NIPA_STATE_DIR       /var/lib/nipa      active-color state file
#   NIPA_CADDYFILE       /etc/caddy/Caddyfile
#   NIPA_BLUE_PORT       8001
#   NIPA_GREEN_PORT      8002
#   NIPA_READY_TIMEOUT   90                 seconds to wait for /readyz
#   NIPA_DRAIN_TIMEOUT   60                 seconds to wait for old connections
#   NIPA_DRAIN_GRACE     5                  fallback sleep when ss is missing
set -euo pipefail

NIPA_BASE="${NIPA_BASE:-/opt/nipa}"
NIPA_CONFIG_DIR="${NIPA_CONFIG_DIR:-/etc/nipa}"
NIPA_STATE_DIR="${NIPA_STATE_DIR:-/var/lib/nipa}"
CADDYFILE="${NIPA_CADDYFILE:-/etc/caddy/Caddyfile}"
BLUE_PORT="${NIPA_BLUE_PORT:-8001}"
GREEN_PORT="${NIPA_GREEN_PORT:-8002}"
READY_TIMEOUT="${NIPA_READY_TIMEOUT:-90}"
DRAIN_TIMEOUT="${NIPA_DRAIN_TIMEOUT:-60}"
DRAIN_GRACE="${NIPA_DRAIN_GRACE:-5}"

log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*"; }
fail() {
	log "ERROR: $*" >&2
	exit 1
}

usage() {
	sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
}

rollback=false
artifact=""
checksum=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--rollback)
		rollback=true
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		if [[ -z "$artifact" ]]; then
			artifact="$1"
		elif [[ -z "$checksum" ]]; then
			checksum="$1"
		else
			fail "unexpected argument $1"
		fi
		shift
		;;
	esac
done

[[ $EUID -eq 0 ]] || fail "run as root (sudo $0 ...)"
command -v systemctl >/dev/null || fail "systemctl is required"
command -v curl >/dev/null || fail "curl is required"

color_port() {
	case "$1" in
	blue) printf '%s\n' "$BLUE_PORT" ;;
	green) printf '%s\n' "$GREEN_PORT" ;;
	*) return 1 ;;
	esac
}

other_color() {
	if [[ "$1" == blue ]]; then
		printf 'green\n'
	else
		printf 'blue\n'
	fi
}

active_color=""
if [[ -f "$NIPA_STATE_DIR/active-color" ]]; then
	active_color="$(tr -d '[:space:]' <"$NIPA_STATE_DIR/active-color")"
fi
if [[ -z "$active_color" && -f "$CADDYFILE" ]]; then
	port="$(grep -oE 'reverse_proxy 127\.0\.0\.1:[0-9]+' "$CADDYFILE" | head -1 | grep -oE '[0-9]+$' || true)"
	case "$port" in
	"$BLUE_PORT") active_color=blue ;;
	"$GREEN_PORT") active_color=green ;;
	esac
fi
if [[ -z "$active_color" ]]; then
	log "no active color state found; assuming blue"
	active_color=blue
fi
[[ "$active_color" == blue || "$active_color" == green ]] || fail "invalid active color \"$active_color\" in $NIPA_STATE_DIR/active-color"
idle_color="$(other_color "$active_color")"
active_port="$(color_port "$active_color")"
idle_port="$(color_port "$idle_color")"
idle_unit="nipad-ee@$idle_color"
active_unit="nipad-ee@$active_color"

env_file="$NIPA_CONFIG_DIR/$idle_color.env"
[[ -f "$env_file" ]] || fail "missing $env_file (see deploy/nipad-ee.env.sample)"
grep -qE "^SERVER_PORT=$idle_port\$" "$env_file" || fail "$env_file must set SERVER_PORT=$idle_port"
[[ -f "$CADDYFILE" ]] || fail "missing $CADDYFILE (see deploy/caddy/Caddyfile)"

idle_started=false
caddy_dirty=false
flipped=false
cleanup() {
	status=$?
	if [[ "$status" -ne 0 ]]; then
		if [[ "$caddy_dirty" == true && "$flipped" == false ]]; then
			log "restoring the Caddyfile"
			mv -f "$CADDYFILE.bak" "$CADDYFILE" || true
			systemctl reload caddy || true
		fi
		if [[ "$idle_started" == true && "$flipped" == false ]]; then
			log "stopping the idle color $idle_color; production stays on $active_color"
			systemctl stop "$idle_unit" || true
		fi
		exit "$status"
	fi
	rm -f "$CADDYFILE.bak"
	exit 0
}
trap cleanup EXIT

if [[ "$rollback" == true ]]; then
	log "rollback: switching from $active_color back to $idle_color"
	[[ -x "$NIPA_BASE/$idle_color/nipad-ee" ]] || fail "no binary at $NIPA_BASE/$idle_color/nipad-ee to roll back to"
else
	[[ -n "$artifact" ]] || fail "usage: $0 /path/to/nipad-ee [sha256] | --rollback"
	[[ -f "$artifact" ]] || fail "artifact $artifact not found"
	if [[ -n "$checksum" ]]; then
		log "verifying checksum"
		printf '%s  %s\n' "$checksum" "$artifact" | sha256sum -c - >/dev/null || fail "sha256 mismatch"
	fi
	install -d -m 0755 "$NIPA_BASE/$idle_color"
	log "installing $artifact into $NIPA_BASE/$idle_color/nipad-ee"
	install -m 0755 "$artifact" "$NIPA_BASE/$idle_color/nipad-ee.new"
	mv -f "$NIPA_BASE/$idle_color/nipad-ee.new" "$NIPA_BASE/$idle_color/nipad-ee"
fi

install -d -m 0755 "$NIPA_STATE_DIR"

log "starting $idle_color on port $idle_port"
if systemctl is-active --quiet "$idle_unit"; then
	systemctl restart "$idle_unit"
else
	systemctl start "$idle_unit"
fi
idle_started=true

deadline=$((SECONDS + READY_TIMEOUT))
until curl -fsS --max-time 3 "http://127.0.0.1:$idle_port/readyz" >/dev/null 2>&1; do
	if ((SECONDS >= deadline)); then
		fail "$idle_color did not become ready within ${READY_TIMEOUT}s (journalctl -u $idle_unit)"
	fi
	sleep 1
done
curl -fsS --max-time 5 "http://127.0.0.1:$idle_port/version" >/dev/null || fail "$idle_color /version smoke check failed"
log "$idle_color is ready"

log "flipping Caddy to $idle_color (port $idle_port)"
cp -a "$CADDYFILE" "$CADDYFILE.bak"
sed -i -E "s#^([[:space:]]*reverse_proxy 127\.0\.0\.1:)[0-9]+#\1$idle_port#" "$CADDYFILE"
caddy_dirty=true
if ! grep -qE "^[[:space:]]*reverse_proxy 127\.0\.0\.1:$idle_port " "$CADDYFILE"; then
	fail "failed to update the reverse_proxy line in $CADDYFILE"
fi
if command -v caddy >/dev/null; then
	caddy validate --config "$CADDYFILE" >/dev/null 2>&1 || fail "invalid Caddyfile after the flip"
fi
systemctl reload caddy
flipped=true
rm -f "$CADDYFILE.bak"
caddy_dirty=false
printf '%s\n' "$idle_color" >"$NIPA_STATE_DIR/active-color"
log "traffic now goes to $idle_color"

if command -v ss >/dev/null; then
	log "waiting for $active_color connections to drain (timeout ${DRAIN_TIMEOUT}s)"
	deadline=$((SECONDS + DRAIN_TIMEOUT))
	while ((SECONDS < deadline)); do
		conns="$(ss -Htn state established "( sport = :$active_port )" 2>/dev/null | wc -l || true)"
		if [[ "$conns" -eq 0 ]]; then
			break
		fi
		sleep 1
	done
else
	sleep "$DRAIN_GRACE"
fi

log "stopping $active_color (graceful, SIGTERM)"
systemctl stop "$active_unit"

action="upgrade"
if [[ "$rollback" == true ]]; then
	action="rollback"
fi
log "$action complete: active color is $idle_color (port $idle_port)"
log "roll back with: $0 --rollback"
