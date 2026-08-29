#!/usr/bin/env bash
set -euo pipefail

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
scaffold="$repo_root/scripts/new-extension.sh"

fail() {
  printf 'new-extension test failed: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  local needle=$1 file=$2
  grep -Fq -- "$needle" "$file" || fail "missing expected text in $(basename -- "$file")"
}

test_root=$(mktemp -d "${TMPDIR:-/tmp}/hserver-new-extension.XXXXXX")
cleanup() {
  find "$test_root" -xdev -depth -delete
}
trap cleanup EXIT INT TERM

bash -n "$scaffold" "$BASH_SOURCE"

created_output=$(
  "$scaffold" create example.health \
    --output-root "$test_root/extensions" \
    --display-name 'Example Health' \
    --purpose 'Observe a bounded service without provider assumptions' \
    --class provider_adapter \
    --target local_host
)
scaffold_dir="$test_root/extensions/example.health"
[[ -d "$scaffold_dir" ]] || fail 'create did not make the requested directory'
[[ "$(find "$scaffold_dir" -maxdepth 1 -type f | wc -l)" -eq 3 ]] ||
  fail 'create wrote an unexpected file count'
assert_contains 'scaffold created:' <(printf '%s\n' "$created_output")
assert_contains 'https://example.com/health' "$scaffold_dir/README.md"
assert_contains 'empty optional configuration' "$scaffold_dir/README.md"
assert_contains '"non_secret_keys": []' "$scaffold_dir/catalog-entry.json"
assert_contains '"secret_key_names": []' "$scaffold_dir/catalog-entry.json"
assert_contains '"secret_file_refs": []' "$scaffold_dir/catalog-entry.json"
"$scaffold" check "$scaffold_dir" >/dev/null

# The documented ID shorthand and exact destination form must produce the same
# checked packet without selecting a provider or writing into the repository.
"$scaffold" example.client --output-root "$test_root/extensions" >/dev/null
"$scaffold" "$test_root/direct/example.direct" --display-name 'Example Direct' >/dev/null
"$scaffold" check "$test_root/extensions/example.client" >/dev/null
"$scaffold" check "$test_root/direct/example.direct" >/dev/null

if "$scaffold" create example.health --output-root "$test_root/extensions" >/dev/null 2>&1; then
  fail 'create overwrote an existing scaffold'
fi
if "$scaffold" create 'Example.Health' --output-root "$test_root/extensions" >/dev/null 2>&1; then
  fail 'create accepted an invalid extension ID'
fi
[[ ! -e "$test_root/extensions/Example.Health" ]] || fail 'invalid ID left a destination behind'

# The check must reject dangerous additions without printing their contents.
"$scaffold" create example.loader --output-root "$test_root/extensions" >/dev/null
printf '\nplugin.Open("example")\n' >>"$test_root/extensions/example.loader/README.md"
if "$scaffold" check "$test_root/extensions/example.loader" >/dev/null 2>&1; then
  fail 'check accepted a runtime loader marker'
fi

"$scaffold" create example.secret --output-root "$test_root/extensions" >/dev/null
printf '\nTOKEN=sk_live_scaffold_must_not_pass\n' >>"$test_root/extensions/example.secret/README.md"
if "$scaffold" check "$test_root/extensions/example.secret" >/dev/null 2>&1; then
  fail 'check accepted secret-like material'
fi

"$scaffold" create example.binary --output-root "$test_root/extensions" >/dev/null
: >"$test_root/extensions/example.binary/.env"
if "$scaffold" check "$test_root/extensions/example.binary" >/dev/null 2>&1; then
  fail 'check accepted a credential file name'
fi

"$scaffold" create example.managed \
  --output-root "$test_root/extensions" \
  --class managed_node_capability \
  --target managed_node >/dev/null
assert_contains 'TaskExtensionRead' "$test_root/extensions/example.managed/catalog-entry.json"
"$scaffold" check "$test_root/extensions/example.managed" >/dev/null

printf '%s\n' 'new-extension scaffold and fail-closed checks: OK'
