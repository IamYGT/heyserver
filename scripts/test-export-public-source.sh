#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'find "$tmp" -xdev -depth -delete' EXIT INT TERM
source_tree="$tmp/source"
destination="$tmp/public"

mkdir -p "$source_tree/scripts"
install -m 0755 "$repo_root/scripts/export-public-source.sh" "$source_tree/scripts/"
install -m 0755 "$repo_root/scripts/test-public-docs.sh" "$source_tree/scripts/"
for required in README.md LICENSE SECURITY.md SUPPORT.md AGENTS.md; do
  printf '%s\n' "$required public policy fixture" >"$source_tree/$required"
done

# Keep the fixture focused on the exporter inventory boundary while carrying
# the minimal public documentation and catalog files required by the exported
# source validator. Copying these audited public files avoids weakening that
# validator with test-only exceptions.
install -m 0644 "$repo_root/README.md" "$source_tree/README.md"
install -m 0644 "$repo_root/CONTRIBUTING.md" "$source_tree/CONTRIBUTING.md"
mkdir -p "$source_tree/docs" "$source_tree/extensions" "$source_tree/trust"
install -m 0644 "$repo_root/trust/README.md" "$source_tree/trust/README.md"
install -m 0644 "$repo_root/trust/release-signers.json" "$source_tree/trust/release-signers.json"
public_documents=(
  installation-guide.md \
  api-reference.md \
  cli.md \
  mail-system.md \
  optional-integrations.md \
  release-manifest.md \
  extension-boundary.md
)
for document in "${public_documents[@]}"; do
  install -m 0644 "$repo_root/docs/$document" "$source_tree/docs/$document"
done
telegram_portability_files=(
  integrations/hserver-telegram-bot/README.md
  integrations/hserver-telegram-bot/.env.example
  integrations/hserver-telegram-bot/deploy/hserver-telegram-bot.service
  integrations/hserver-telegram-bot/src/hserver_bot/config.py
  integrations/hserver-telegram-bot/src/hserver_bot/services/digest.py
)
for telegram_file in "${telegram_portability_files[@]}"; do
  mkdir -p "$source_tree/$(dirname "$telegram_file")"
  install -m 0644 "$repo_root/$telegram_file" "$source_tree/$telegram_file"
done
for catalog in catalog.json catalog.schema.json; do
  install -m 0644 "$repo_root/extensions/$catalog" "$source_tree/extensions/$catalog"
done

git -C "$source_tree" init -q --initial-branch=main
git -C "$source_tree" config user.name "HServer CI"
git -C "$source_tree" config user.email "ci@example.com"
printf '%s%s\n' '49.12.' '188.137' >"$source_tree/installation-inventory.txt"
git -C "$source_tree" add .
git -C "$source_tree" commit -qm "seed unsafe fixture"

if "$source_tree/scripts/export-public-source.sh" "$destination" >"$tmp/unsafe.log" 2>&1; then
  echo "public exporter accepted installation-specific inventory" >&2
  exit 1
fi
grep -q 'public source contains installation-specific inventory' "$tmp/unsafe.log"
[[ ! -e "$destination" ]] || {
  echo "failed public export published a destination" >&2
  exit 1
}

git -C "$source_tree" rm -q installation-inventory.txt
git -C "$source_tree" commit -qm "remove unsafe fixture"
"$source_tree/scripts/export-public-source.sh" "$destination" >"$tmp/safe.log"
grep -q 'Public source snapshot created:' "$tmp/safe.log"
[[ -x "$destination/scripts/test-public-docs.sh" && ! -e "$destination/.git" ]]
for document in "${public_documents[@]}"; do
  [[ -f "$destination/docs/$document" ]] || {
    echo "public source export omitted required documentation: docs/$document" >&2
    exit 1
  }
done
for telegram_file in "${telegram_portability_files[@]}"; do
  [[ -f "$destination/$telegram_file" ]] || {
    echo "public source export omitted required Telegram file: $telegram_file" >&2
    exit 1
  }
done
"$destination/scripts/test-public-docs.sh" >/dev/null

printf '%s\n' 'public source export inventory gate: OK'
