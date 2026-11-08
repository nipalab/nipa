#!/usr/bin/env bash
# Packs addons/nipa (with whatever bin/<platform>/ libraries exist) into a
# reproducible zip: nipa-godot-v<version>/addons/nipa/..., sorted entries,
# fixed timestamps and modes, no build leftovers.
# Usage: pack.sh <version> <output.zip>
set -euo pipefail

version="${1:?usage: pack.sh <version> <output.zip>}"
version="${version#v}"
out="${2:?usage: pack.sh <version> <output.zip>}"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
name="nipa-godot-v${version}"

mkdir -p "$(dirname "$out")"
out="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT

mkdir -p "$stage/$name/addons"
cp -RL "$here/addons/nipa" "$stage/$name/addons/nipa"
find "$stage" \( -name '*.lib' -o -name '*.exp' -o -name '*.pdb' -o -name '*.ilk' -o -name '*.tmp' \
  -o -name '.DS_Store' -o -name '.gitignore' \) -exec rm -rf {} +

find "$stage" -type d -exec chmod 755 {} +
find "$stage" -type f -exec chmod 644 {} +
find "$stage" -type f \( -name nipa -o -name nipa.exe \) -exec chmod 755 {} +
TZ=UTC find "$stage" -exec touch -h -t 198001010000.00 {} +

platforms="$(cd "$stage/$name/addons/nipa" && find bin -mindepth 1 -maxdepth 1 -type d 2>/dev/null | LC_ALL=C sort | tr '\n' ' ')"
echo "packing $name with: ${platforms:-no editor libraries}"

rm -f "$out"
(cd "$stage" && find "$name" -type f | LC_ALL=C sort | TZ=UTC zip -X -q -@ "$out")
echo "$out"
