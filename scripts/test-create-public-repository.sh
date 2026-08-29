#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp=$(mktemp -d)
trap 'find "$tmp" -xdev -depth -delete' EXIT INT TERM
source_tree="$tmp/source"
destination="$tmp/public"

mkdir -p "$source_tree"
# Seed the private fixture from the canonical committed tree so its public
# documentation and Telegram portability files cannot drift from the exporter.
git -C "$repo_root" archive --format=tar HEAD | tar -xf - -C "$source_tree"

git -C "$source_tree" init -q --initial-branch=main
git -C "$source_tree" config user.name "Private Fixture"
git -C "$source_tree" config user.email "private-fixture@example.com"
git -C "$source_tree" add .
git -C "$source_tree" commit -qm "seed private fixture"

printf '%s\n' dirty >>"$source_tree/README.md"
if "$source_tree/scripts/create-public-repository.sh" "$destination" \
  --author-name "HServer Maintainers" --author-email "maintainers@example.com" \
  >"$tmp/dirty.log" 2>&1; then
  echo "public repository creator accepted a dirty source tree" >&2
  exit 1
fi
grep -q 'Refusing to export a dirty worktree' "$tmp/dirty.log"
[[ ! -e "$destination" ]]
git -C "$source_tree" restore README.md

if "$source_tree/scripts/create-public-repository.sh" "$source_tree/public" \
  --author-name "HServer Maintainers" --author-email "maintainers@example.com" \
  >"$tmp/inside.log" 2>&1; then
  echo "public repository creator wrote inside the private source tree" >&2
  exit 1
fi
grep -q 'inside the private source tree' "$tmp/inside.log"
[[ ! -e "$source_tree/public" ]]

mkdir "$destination"
if "$source_tree/scripts/create-public-repository.sh" "$destination" \
  --author-name "HServer Maintainers" --author-email "maintainers@example.com" \
  >"$tmp/existing.log" 2>&1; then
  echo "public repository creator overwrote an existing destination" >&2
  exit 1
fi
grep -q 'Refusing to overwrite existing destination' "$tmp/existing.log"
rmdir "$destination"

"$source_tree/scripts/create-public-repository.sh" "$destination" \
  --author-name "HServer Maintainers" --author-email "maintainers@example.com" \
  >"$tmp/success.log"

grep -q 'Public repository created:' "$tmp/success.log"
grep -q 'Remote configured: no' "$tmp/success.log"
[[ -d "$destination/.git" ]]
[[ $(git -C "$destination" rev-list --all --count) == 1 ]]
[[ $(git -C "$destination" rev-list --max-parents=0 --all --count) == 1 ]]
[[ $(git -C "$destination" branch --show-current) == main ]]
[[ -z "$(git -C "$destination" remote)" ]]
[[ -z "$(git -C "$destination" status --porcelain)" ]]
[[ $(git -C "$destination" log -1 --format=%s) == "Publish HServer community source" ]]
[[ $(git -C "$destination" log -1 --format='%an <%ae>') == "HServer Maintainers <maintainers@example.com>" ]]
git -C "$destination" fsck --strict --no-dangling >/dev/null
"$destination/scripts/test-public-docs.sh" >/dev/null

if find "$tmp" -maxdepth 1 -type d -name '.hserver-public-repository.*' -print -quit | grep -q .; then
  echo "public repository creator left a staging directory behind" >&2
  exit 1
fi

printf '%s\n' 'clean public repository creation: OK'
