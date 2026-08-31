#!/usr/bin/env bash
set -euo pipefail

# Create a provider-neutral, in-tree extension review scaffold.  The generated
# files are source/docs fixtures only: this script never loads or executes an
# extension, installs a dependency, contacts a provider, or creates a secret.

repo_root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)

usage() {
  cat <<'USAGE'
Usage:
  scripts/new-extension.sh create ID [options]
  scripts/new-extension.sh check PATH
  scripts/new-extension.sh ID [options]

Create or validate a provider-neutral in-tree extension scaffold.

Create options:
  --output-root DIR       Parent directory (default: <repo>/extensions)
  --output DIR            Exact destination directory instead of output-root/ID
  --display-name NAME     Catalog display name (default: Example extension)
  --purpose TEXT          Operator problem summary (default: scaffold prompt)
  --class CLASS           One supported class (default: client_surface)
  --target TARGET         local_host or managed_node (default: local_host)
  --route-prefix PATH     Existing /api prefix (default: /api/extensions/ID)
  -h, --help              Show this help

Supported classes:
  local_capability, managed_node_capability, provider_adapter, client_surface

The create command refuses to overwrite an existing path and writes only
README.md, catalog-entry.json, and integration_test.go.  The check command is
fail-closed: it validates the metadata, empty optional configuration, required
example.com placeholder, and rejects secret-like material, runtime loaders,
eval, and external binary artifacts. It never executes generated files.
USAGE
}

fail() {
  printf 'new-extension: %s\n' "$*" >&2
  exit 2
}

json_escape() {
  local value=$1
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  value=${value//$'\t'/\\t}
  printf '%s' "$value"
}

slugify() {
  LC_ALL=C printf '%s' "$1" |
    LC_ALL=C tr '[:upper:]' '[:lower:]' |
    LC_ALL=C sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//'
}

valid_id() {
  [[ "$1" =~ ^[a-z0-9]+([._-][a-z0-9]+)*$ ]]
}

valid_text() {
  local value=$1
  [[ -n "$value" && "$value" != *$'\n'* && "$value" != *$'\r'* ]]
}

valid_route() {
  local route=$1
  [[ "$route" =~ ^/api(/[A-Za-z0-9._~-]+)+$ ]] &&
    [[ "$route" != *..* && "$route" != *'//'* ]]
}

check_file_name() {
  local name=$1
  case "$name" in
    .env|.env.*|*credential*|*secret*|*.pem|*.key|*.p12|*.pfx|*.so|*.dll|*.dylib|*.exe|id_rsa|id_ed25519)
      return 1
      ;;
  esac
  return 0
}

check_runtime_boundary() {
  local extension_dir=$1
  local unsafe_files
  unsafe_files=$(find "$extension_dir" \( -type l -o -type f \) \( \
    -name '.env' -o -name '.env.*' -o -iname '*credential*' -o -iname '*secret*' \
    -o -iname '*.pem' -o -iname '*.key' -o -iname '*.p12' -o -iname '*.pfx' \
    -o -iname '*.so' -o -iname '*.dll' -o -iname '*.dylib' -o -iname '*.exe' \
  \) -print -quit)
  if [[ -n "$unsafe_files" ]]; then
    fail 'scaffold contains a secret, credential, symlink, or binary artifact'
  fi

  # Print only the generic failure, never a matching line that could contain a
  # credential or provider endpoint.
  if grep -RIlE --exclude-dir=.git -- \
    'plugin[[:space:]]*\.[[:space:]]*Open|os/exec|exec[[:space:]]*\.[[:space:]]*Command|eval[[:space:]]*\(|dlopen|LoadLibrary|/bin/sh[[:space:]]+-c|go:linkname' \
    "$extension_dir" >/dev/null; then
    fail 'scaffold contains runtime loading, eval, or arbitrary command execution'
  fi
  if grep -RIlE --exclude-dir=.git -- \
    '-----BEGIN([[:space:]][A-Z0-9]+)?[[:space:]]PRIVATE KEY-----|https?://[^[:space:]]+:[^[:space:]@]+@|(^|[^A-Za-z])(sk_(live|test)_|gh[pousr]_|xox[baprs]-|AIza[[:alnum:]_-]{20,})' \
    "$extension_dir" >/dev/null; then
    fail 'scaffold contains secret-like material'
  fi
}

check_extension() {
  (($# == 1)) || fail 'check expects one scaffold directory'
  local extension_dir=$1
  [[ -d "$extension_dir" && ! -L "$extension_dir" ]] || fail 'scaffold directory does not exist or is a symlink'

  local id
  id=$(basename -- "$extension_dir")
  valid_id "$id" || fail 'scaffold directory name is not a valid extension ID'

  local required
  for required in README.md catalog-entry.json integration_test.go; do
    [[ -f "$extension_dir/$required" && ! -L "$extension_dir/$required" ]] ||
      fail "scaffold is missing $required"
  done

  local file name
  while IFS= read -r -d '' file; do
    name=$(basename -- "$file")
    check_file_name "$name" || fail 'scaffold contains a forbidden file name'
  done < <(find "$extension_dir" \( -type f -o -type l \) -print0)

  check_runtime_boundary "$extension_dir"
  grep -Fq 'https://example.com/health' "$extension_dir/README.md" ||
    fail 'README is missing the https://example.com/health placeholder'
  for state in not_configured unavailable healthy; do
    grep -Fq "$state" "$extension_dir/README.md" ||
      fail "README is missing the $state state"
  done
  grep -Fq 'catalog-entry.json' "$extension_dir/README.md" ||
    fail 'README is missing the catalog handoff'
  grep -Fq 'empty optional configuration' "$extension_dir/README.md" ||
    fail 'README is missing the empty optional configuration boundary'
  grep -Fq 't.Skip(' "$extension_dir/integration_test.go" ||
    fail 'scaffold test must remain an explicit placeholder until replaced'

  python3 - "$extension_dir/catalog-entry.json" "$extension_dir/README.md" "$extension_dir/integration_test.go" "$id" <<'PY'
from __future__ import annotations

import json
import re
import sys
from pathlib import Path

metadata_path, readme_path, test_path, directory_id = map(Path, sys.argv[1:])


def fail(message: str) -> None:
    print(f"new-extension: catalog scaffold check failed: {message}", file=sys.stderr)
    raise SystemExit(2)


def no_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            fail("catalog metadata contains duplicate keys")
        result[key] = value
    return result


try:
    with metadata_path.open(encoding="utf-8") as handle:
        metadata = json.load(handle, object_pairs_hook=no_duplicates)
except (OSError, UnicodeError, json.JSONDecodeError):
    fail("catalog metadata is not valid UTF-8 JSON")

if not isinstance(metadata, dict):
    fail("catalog metadata must be an object")

expected_fields = {
    "id",
    "display_name",
    "purpose",
    "requirement",
    "docs_row_marker",
    "classes",
    "targets",
    "configuration",
    "status",
    "evidence",
}
if set(metadata) not in (expected_fields, expected_fields | {"agent"}):
    fail("catalog metadata has an unexpected field set")

if metadata.get("id") != directory_id.as_posix():
    fail("catalog ID does not match the scaffold directory")
if not re.fullmatch(r"[a-z0-9]+(?:[._-][a-z0-9]+)*", metadata["id"]):
    fail("catalog ID is not a stable lowercase identifier")

for field in ("display_name", "purpose", "docs_row_marker"):
    if not isinstance(metadata.get(field), str) or not metadata[field].strip():
        fail(f"catalog {field} is empty")

slug = re.sub(r"[^a-z0-9]+", "-", metadata["display_name"].lower()).strip("-")
if not slug or metadata["docs_row_marker"] != f"optional-integrations:v1:{slug}":
    fail("catalog documentation marker does not follow the v1 convention")
if metadata.get("requirement") != "optional":
    fail("scaffold requirement must remain optional")

allowed_classes = {
    "local_capability",
    "managed_node_capability",
    "provider_adapter",
    "client_surface",
}
allowed_targets = {"local_host", "managed_node"}
for field, allowed in (("classes", allowed_classes), ("targets", allowed_targets)):
    values = metadata.get(field)
    if not isinstance(values, list) or not values or len(set(values)) != len(values):
        fail(f"catalog {field} must be a non-empty unique list")
    if any(value not in allowed for value in values):
        fail(f"catalog {field} contains an unsupported value")

configuration = metadata.get("configuration")
if not isinstance(configuration, dict) or set(configuration) != {
    "non_secret_keys",
    "secret_key_names",
    "secret_file_refs",
    "boundary",
}:
    fail("catalog configuration shape is invalid")
for field in ("non_secret_keys", "secret_key_names", "secret_file_refs"):
    if configuration[field] != []:
        fail("scaffold optional configuration must stay empty")
if not isinstance(configuration["boundary"], str) or not configuration["boundary"].strip():
    fail("catalog configuration boundary is empty")

status = metadata.get("status")
if not isinstance(status, dict) or set(status) != {
    "canonical_states",
    "raw_state_mappings",
    "api_route_prefixes",
}:
    fail("catalog status shape is invalid")
if status["canonical_states"] != ["not_configured", "unavailable", "healthy"]:
    fail("catalog status must declare the canonical state trio")
if not isinstance(status["raw_state_mappings"], list) or len(status["raw_state_mappings"]) != 3:
    fail("catalog status must contain three raw mappings")
if any(not isinstance(item, dict) for item in status["raw_state_mappings"]):
    fail("catalog raw mapping must be an object")
if {item.get("canonical") for item in status["raw_state_mappings"]} != {
    "not_configured",
    "unavailable",
    "healthy",
}:
    fail("catalog raw mappings must cover the canonical state trio")
for item in status["raw_state_mappings"]:
    if set(item) != {"raw", "canonical", "meaning"}:
        fail("catalog raw mapping shape is invalid")
    if not all(isinstance(item[field], str) and item[field].strip() for field in ("raw", "canonical", "meaning")):
        fail("catalog raw mapping contains an empty field")

routes = status["api_route_prefixes"]
if not isinstance(routes, list) or len(routes) != 1 or not isinstance(routes[0], str):
    fail("catalog status must contain one API route prefix")
if not re.fullmatch(r"/api(?:/[A-Za-z0-9._~-]+)+", routes[0]) or ".." in routes[0] or "//" in routes[0]:
    fail("catalog API route prefix is not a bounded /api path")

managed = "managed_node" in metadata["targets"] or "managed_node_capability" in metadata["classes"]
if managed:
    agent = metadata.get("agent")
    if not isinstance(agent, dict) or set(agent) != {"tasks", "capabilities", "evidence"}:
        fail("managed scaffold must declare a fixed agent placeholder")
    if agent["tasks"] != ["TaskExtensionRead"] or agent["capabilities"] != ["CapabilityExtensionRead"]:
        fail("managed scaffold must keep the explicit placeholder task/capability")
else:
    if "agent" in metadata:
        fail("local scaffold must not declare managed-agent metadata")

evidence = metadata.get("evidence")
if not isinstance(evidence, dict) or set(evidence) != {"web", "docs", "tests"}:
    fail("catalog evidence shape is invalid")
for category in ("web", "docs", "tests"):
    items = evidence[category]
    if not isinstance(items, list) or len(items) != 1:
        fail("catalog evidence must contain one scaffold reference per category")
    item = items[0]
    if not isinstance(item, dict) or set(item) != {"path", "claim"}:
        fail("catalog evidence item shape is invalid")
    if not isinstance(item["path"], str) or item["path"].startswith("/") or ".." in item["path"]:
        fail("catalog evidence path is not repository-relative")
    if not isinstance(item["claim"], str) or not item["claim"].strip():
        fail("catalog evidence claim is empty")
    expected = f"extensions/{directory_id.as_posix()}/README.md"
    if category in ("web", "docs") and item["path"] != expected:
        fail("catalog scaffold documentation evidence path is wrong")
    if category == "tests" and item["path"] != f"extensions/{directory_id.as_posix()}/integration_test.go":
        fail("catalog scaffold test evidence path is wrong")

if "agent" in metadata:
    agent = metadata["agent"]
    evidence = agent.get("evidence")
    if not isinstance(evidence, dict) or set(evidence) != {"tasks", "capabilities"}:
        fail("managed agent evidence shape is invalid")
    for category in ("tasks", "capabilities"):
        if not isinstance(evidence[category], list) or len(evidence[category]) != 1:
            fail("managed agent evidence must contain one placeholder reference")
        item = evidence[category][0]
        if not isinstance(item, dict) or set(item) != {"path", "claim"}:
            fail("managed agent evidence item shape is invalid")
        if not isinstance(item["path"], str) or not isinstance(item["claim"], str):
            fail("managed agent evidence item fields are invalid")
        if item["path"] != f"extensions/{directory_id.as_posix()}/README.md":
            fail("managed agent evidence path is wrong")

print(f"new-extension: scaffold verified: {directory_id.as_posix()}")
PY
}

create_extension() {
  (($# >= 1)) || { usage >&2; exit 2; }

  local first=$1
  shift
  local id destination output_root="$repo_root/extensions" output_exact=
  local output_root_explicit= output_explicit=
  local display_name='Example extension'
  local purpose='Describe the operator problem this in-tree extension solves.'
  local extension_class=client_surface target=local_host route_prefix=

  if [[ "$first" == */* || "$first" == .* || "$first" == /* ]]; then
    destination=$first
    id=$(basename -- "$destination")
    output_exact=1
  else
    id=$first
  fi

  local option value
  while (($#)); do
    option=$1
    case "$option" in
      --output-root|--output|--display-name|--purpose|--class|--target|--route-prefix)
        (($# >= 2)) || fail "$option requires a value"
        value=$2
        case "$option" in
          --output-root)
            [[ -z "${output_root_explicit:-}" && -z "${output_exact:-}" ]] ||
              fail 'use either --output or --output-root, not both'
            output_root=$value
            output_root_explicit=1
            ;;
          --output)
            [[ -z "${output_explicit:-}" && -z "${output_root_explicit:-}" && -z "${output_exact:-}" ]] ||
              fail 'use either --output or --output-root, not both'
            destination=$value
            output_exact=1
            output_explicit=1
            ;;
          --display-name) display_name=$value ;;
          --purpose) purpose=$value ;;
          --class) extension_class=$value ;;
          --target) target=$value ;;
          --route-prefix) route_prefix=$value ;;
        esac
        shift 2
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        fail "unknown create option: $option"
        ;;
    esac
  done

  valid_id "$id" || fail 'ID must match lowercase catalog form, for example example.health'
  valid_text "$display_name" || fail 'display name must be non-empty and single-line'
  valid_text "$purpose" || fail 'purpose must be non-empty and single-line'
  local display_slug
  display_slug=$(slugify "$display_name")
  [[ -n "$display_slug" ]] || fail 'display name must contain an ASCII letter or digit'
  case "$extension_class" in
    local_capability|managed_node_capability|provider_adapter|client_surface) ;;
    *) fail 'class is not supported' ;;
  esac
  case "$target" in
    local_host|managed_node) ;;
    *) fail 'target must be local_host or managed_node' ;;
  esac

  [[ -z "${output_root_explicit:-}" || -z "${output_exact:-}" ]] ||
    fail 'use either --output or --output-root, not both'
  if [[ -z "${output_exact:-}" ]]; then
    [[ -n "$output_root" ]] || fail 'output root cannot be empty'
    destination=$output_root/$id
  fi
  [[ -n "$destination" ]] || fail 'destination cannot be empty'
  if [[ -n "${output_explicit:-}" ]]; then
    [[ "$(basename -- "$destination")" == "$id" ]] ||
      fail '--output basename must match the extension ID'
  fi
  [[ ! -e "$destination" ]] || fail "refusing to overwrite existing destination: $destination"

  if [[ -z "$route_prefix" ]]; then
    route_prefix=/api/extensions/$(printf '%s' "$id" | LC_ALL=C sed 's/[._]/-/g')
  fi
  valid_route "$route_prefix" || fail 'route prefix must be a bounded /api path'

  local parent_dir package_name json_display json_purpose json_route
  parent_dir=$(dirname -- "$destination")
  mkdir -p -- "$parent_dir"
  mkdir -- "$destination"
  package_name=$(printf '%s' "$id" | LC_ALL=C sed -E 's/[^a-zA-Z0-9]+/_/g')
  [[ "$package_name" =~ ^[a-zA-Z_] ]] || package_name="extension_$package_name"
  package_name=${package_name}_test
  json_display=$(json_escape "$display_name")
  json_purpose=$(json_escape "$purpose")
  json_route=$(json_escape "$route_prefix")

  {
    printf '# %s\n\n' "$display_name"
    cat <<'EOF_README'
This directory is a provider-neutral, in-tree Heyserver extension scaffold for
EOF_README
    printf '`%s`. It is source and review material only. Heyserver v1 compiles accepted\n' "$id"
    cat <<'EOF_README'
extensions into the normal release; it does not load runtime plugins, evaluate
source, run arbitrary hooks, or install an external binary from this directory.

## Starting contract

EOF_README
    printf '%s\n' "- **Operator problem:** $purpose"
    printf -- '- **Target:** `%s`\n- **Class:** `%s`\n' "$target" "$extension_class"
    cat <<'EOF_README'
- **Health placeholder:** [https://example.com/health](https://example.com/health)
- **Optional configuration:** empty optional configuration; no provider,
  endpoint, credential, or secret is selected by this scaffold.

The catalog metadata deliberately starts with empty configuration arrays and
canonical `not_configured`, `unavailable`, and `healthy` states. Replace the
placeholder health endpoint with a real read-only observation in implementation
code; do not treat a URL or installed binary as proof of health.

## Files

- `catalog-entry.json` is a standalone candidate for the reviewed
  `extensions/catalog.json` entry. It is **not** registered automatically.
- `integration_test.go` is an explicit skipped placeholder. Replace it with
  focused absence, unavailable, healthy, authorization, and mutation tests as
  applicable before proposing the extension.
- This README is the contribution checklist and operator-facing boundary.

## Complete before merge

- [ ] Link the accepted Integration proposal issue.
- [ ] Keep provider-specific transport and credentials inside one package or
      adapter; keep generic Heyserver code provider-neutral.
- [ ] Add bounded API or fixed managed-agent task wiring only when needed.
- [ ] Preserve `not_configured`, `unavailable`, and `healthy` as distinct
      observations, with mutations disabled until their preconditions pass.
- [ ] Store credential values only in protected installation-owned environment
      or secret files; add names/references, never values, to the catalog.
EOF_README
    printf -- '- [ ] Replace the placeholder `%s` with a real generated route prefix\n' "$json_route"
    cat <<'EOF_README'
      and update the route/OpenAPI contract when an API is added.
- [ ] Add the matching row marker to `docs/optional-integrations.md` and
      merge this candidate into `extensions/catalog.json` only with source,
      production registration, focused tests, and operator documentation.
- [ ] Describe timeout, retry, audit, failure, recovery, rollback, upgrade,
      removal, and portability behavior.

Run the scaffold check before editing the candidate:

```bash
EOF_README
    printf './scripts/new-extension.sh check extensions/%s\n' "$id"
    cat <<'EOF_README'
```

The check is intentionally fail-closed and never executes generated code. It
rejects secret-like material, credential or binary artifacts, runtime loading,
`eval`, and arbitrary command execution. No secret is generated by this
scaffold.
EOF_README
  } > "$destination/README.md"

  cat > "$destination/integration_test.go" <<EOF_TEST
package $package_name

import "testing"

// Replace this explicit placeholder with focused extension evidence before
// merging catalog-entry.json into the reviewed catalog.
func TestExtensionScaffold(t *testing.T) {
\tt.Skip("replace scaffold test with focused extension coverage")
}
EOF_TEST

  local agent_json=''
  if [[ "$target" == managed_node || "$extension_class" == managed_node_capability ]]; then
    agent_json=$(cat <<'EOF_AGENT'
,
  "agent": {
    "tasks": ["TaskExtensionRead"],
    "capabilities": ["CapabilityExtensionRead"],
    "evidence": {
      "tasks": [{"path": "PLACEHOLDER", "claim": "Replace with task evidence before merge."}],
      "capabilities": [{"path": "PLACEHOLDER", "claim": "Replace with capability evidence before merge."}]
    }
  }
EOF_AGENT
    )
    agent_json=${agent_json//PLACEHOLDER/extensions\/$id\/README.md}
  fi

  cat > "$destination/catalog-entry.json" <<EOF_JSON
{
  "id": "$id",
  "display_name": "$json_display",
  "purpose": "$json_purpose",
  "requirement": "optional",
  "docs_row_marker": "optional-integrations:v1:$display_slug",
  "classes": ["$extension_class"],
  "targets": ["$target"],
  "configuration": {
    "non_secret_keys": [],
    "secret_key_names": [],
    "secret_file_refs": [],
    "boundary": "No optional configuration is selected by this scaffold; add installation-owned names during implementation."
  },
  "status": {
    "canonical_states": ["not_configured", "unavailable", "healthy"],
    "raw_state_mappings": [
      {"raw": "configuration_missing", "canonical": "not_configured", "meaning": "Required installation-owned configuration is absent."},
      {"raw": "read_only_probe_failed", "canonical": "unavailable", "meaning": "The configured dependency could not complete a bounded read-only observation."},
      {"raw": "read_only_probe_succeeded", "canonical": "healthy", "meaning": "A fresh bounded read-only observation proved the dependency usable."}
    ],
    "api_route_prefixes": ["$json_route"]
  },
  "evidence": {
    "web": [{"path": "extensions/$id/README.md", "claim": "Scaffold records the client and health-state boundary."}],
    "docs": [{"path": "extensions/$id/README.md", "claim": "Scaffold records installation, secret, and recovery expectations."}],
    "tests": [{"path": "extensions/$id/integration_test.go", "claim": "Scaffold reserves the focused integration test location."}]
  }$agent_json
}
EOF_JSON

  check_extension "$destination"
  printf 'new-extension: scaffold created: %s\n' "$destination"
  printf 'new-extension: next step: replace placeholders, then merge catalog-entry.json only with source and focused evidence\n'
}

command=${1:-}
case "$command" in
  -h|--help)
    usage
    ;;
  check|--check)
    shift
    check_extension "$@"
    ;;
  create|new)
    shift
    create_extension "$@"
    ;;
  '')
    usage >&2
    exit 2
    ;;
  *)
    create_extension "$@"
    ;;
esac
