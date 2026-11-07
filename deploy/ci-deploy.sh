#!/usr/bin/env bash
# Remote side of .github/workflows/server-deploy.yml: ships a nipad-ee build to a
# host over SSH and runs the root-owned upgrade script there (see docs/DEPLOY.md,
# "CI deploys").
#
#   deploy/ci-deploy.sh deploy <binary> <sha256> <version>
#   deploy/ci-deploy.sh rollback
#
# Environment:
#   DEPLOY_HOST, DEPLOY_USER       target host and SSH user (required)
#   DEPLOY_SSH_KEY                 private key, OpenSSH format (required)
#   DEPLOY_KNOWN_HOSTS             pinned host key line(s) (required)
#   DEPLOY_PORT                    SSH port (default 22)
#   DEPLOY_URL                     public origin to verify /version (optional)
#   DEPLOY_UPGRADE_SCRIPT          /opt/nipa/upgrade.sh
#   DEPLOY_KEEP_RELEASES           5 uploaded binaries kept in ~/releases
#   DEPLOY_VERIFY_ATTEMPTS         30 (2 s apart)
set -euo pipefail

log() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*"; }
fail() {
	log "ERROR: $*" >&2
	exit 1
}

: "${DEPLOY_HOST:?DEPLOY_HOST is required}"
: "${DEPLOY_USER:?DEPLOY_USER is required}"
: "${DEPLOY_SSH_KEY:?DEPLOY_SSH_KEY is required}"
: "${DEPLOY_KNOWN_HOSTS:?DEPLOY_KNOWN_HOSTS is required}"
DEPLOY_PORT="${DEPLOY_PORT:-22}"
DEPLOY_URL="${DEPLOY_URL:-}"
UPGRADE="${DEPLOY_UPGRADE_SCRIPT:-/opt/nipa/upgrade.sh}"
KEEP="${DEPLOY_KEEP_RELEASES:-5}"
ATTEMPTS="${DEPLOY_VERIFY_ATTEMPTS:-30}"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
printf '%s\n' "$DEPLOY_SSH_KEY" >"$work/key"
chmod 600 "$work/key"
printf '%s\n' "$DEPLOY_KNOWN_HOSTS" >"$work/known_hosts"

ssh_opts=(-i "$work/key" -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=15
	-o StrictHostKeyChecking=yes -o "UserKnownHostsFile=$work/known_hosts")
target="$DEPLOY_USER@$DEPLOY_HOST"

remote() {
	ssh "${ssh_opts[@]}" -p "$DEPLOY_PORT" "$target" "$1"
}

verify() {
	local expected="$1" got=""
	if [[ -z "$DEPLOY_URL" ]]; then
		log "DEPLOY_URL not set; skipping the /version check"
		return 0
	fi
	for ((i = 1; i <= ATTEMPTS; i++)); do
		got="$(curl -fsS --max-time 5 "${DEPLOY_URL%/}/version" 2>/dev/null | jq -r '.version' 2>/dev/null || true)"
		if [[ -n "$got" && (-z "$expected" || "$got" == "$expected") ]]; then
			log "$DEPLOY_URL serves $got"
			return 0
		fi
		sleep 2
	done
	fail "$DEPLOY_URL/version answered \"$got\", expected \"${expected:-any version}\""
}

cmd="${1:-}"
case "$cmd" in
deploy)
	[[ $# -eq 4 ]] || fail "usage: $0 deploy <binary> <sha256> <version>"
	binary="$2"
	checksum="$3"
	version="$4"
	[[ -f "$binary" ]] || fail "binary $binary not found"
	[[ "$checksum" =~ ^[0-9a-f]{64}$ ]] || fail "invalid sha256 \"$checksum\""
	name="nipad-ee-$(printf '%s' "$version" | tr -c 'A-Za-z0-9._-' '_')"

	log "uploading $name to $target"
	remote "mkdir -p releases"
	scp "${ssh_opts[@]}" -P "$DEPLOY_PORT" -q "$binary" "$target:releases/$name.part"

	log "running $UPGRADE on $DEPLOY_HOST"
	remote "set -e
mv -f releases/$name.part releases/$name
sudo -n $UPGRADE \"\$HOME/releases/$name\" $checksum
cd releases && ls -1t | tail -n +$((KEEP + 1)) | xargs -r rm -f --"

	verify "$version"
	;;
rollback)
	[[ $# -eq 1 ]] || fail "usage: $0 rollback"
	log "rolling back on $DEPLOY_HOST"
	remote "sudo -n $UPGRADE --rollback"
	verify ""
	;;
*)
	fail "usage: $0 deploy <binary> <sha256> <version> | rollback"
	;;
esac
