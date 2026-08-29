#!/usr/bin/env python3
"""Validate the reviewed optional-integration catalog.

The catalog is deliberately data-only: this verifier checks its structural
contract, required core entries, source references, documentation row mapping,
API prefixes, and the managed-agent names that are actually compiled into
HServer. Additional entries are allowed when they satisfy the same contract.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path
from typing import Any, Iterable


CANONICAL_STATES = ("not_configured", "unavailable", "healthy")
EXPECTED_IDS = (
    "cloudflare.dns",
    "stalwart.mail",
    "mail.access",
    "backup.gdrive",
    "backup.snapshot.restic",
    "process.pm2",
    "web.nginx",
    "runtime.php_fpm",
    "firewall.ufw",
    "tls.certbot",
    "dns.bind9",
    "database.local",
    "container.docker",
    "storage.smartmontools",
    "notification.delivery",
)
MARKER_PREFIX = "optional-integrations:v1:"
MARKER_CONVENTION = (
    "marker_prefix + slug(display_name), where slug lowercases and joins "
    "non-alphanumeric runs with a hyphen"
)
ROUTE_MANIFEST = "internal/api/routes_manifest.go"
AGENT_TYPES = "internal/agenthub/types.go"
AGENT_SERVICE = "internal/agenthub/service.go"
DOC_TABLE = "docs/optional-integrations.md"
# This is the production source map for local integration probes.  Keep the
# verifier anchored to the constructor that the router calls rather than
# searching the repository for an ID: tests and documentation may mention an
# entry without wiring an executable probe into a release binary.
PRODUCTION_REGISTRY = "internal/api/handlers_integrations.go"
PRODUCTION_REGISTRY_FUNCTION = "newIntegrationStatusServiceWithCatalog"
PRODUCTION_REGISTRY_DECLARATION = "probes := []integrationstatus.Probe{"
INTEGRATIONSTATUS_CONSTANTS = "internal/services/integrationstatus/status.go"

_TEST_FILE = re.compile(r"(?:_test\.go|\.test\.(?:ts|tsx|js|jsx)|^test-[^/]+\.(?:sh|py))$")
_ID_PATTERN = re.compile(r"^[a-z0-9]+(?:[._-][a-z0-9]+)*$")
_SECRET_ASSIGNMENT = re.compile(
    r"(?i)(?:api[_-]?key|client[_-]?secret|token|password|secret|credential|webhook(?:url)?)"
    r"\s*(?:[\"']?\s*)[:=]\s*[\"']?([^\s,}\"']+)"
)
_SECRET_PREFIX = re.compile(r"(?i)^(?:bearer\s+|sk_(?:live|test)_|gh[pousr]_\w+|xox[baprs]-|AIza)\S+")
_KEY_NAME = re.compile(r"^[A-Za-z][A-Za-z0-9_.-]*$")


class CatalogError(Exception):
    """A claim-matched catalog validation failure."""


def _skip_go_literal_or_comment(source: str, index: int) -> int:
    """Return the first index after a Go literal or comment."""

    if source.startswith("//", index):
        newline = source.find("\n", index + 2)
        return len(source) if newline < 0 else newline
    if source.startswith("/*", index):
        end = source.find("*/", index + 2)
        if end < 0:
            raise CatalogError("unterminated Go block comment in production registry")
        return end + 2

    quote = source[index]
    if quote == "`":
        end = source.find("`", index + 1)
        if end < 0:
            raise CatalogError("unterminated Go raw string in production registry")
        return end + 1
    if quote not in {"\"", "'"}:
        return index

    escaped = False
    cursor = index + 1
    while cursor < len(source):
        char = source[cursor]
        if escaped:
            escaped = False
        elif char == "\\":
            escaped = True
        elif char == quote:
            return cursor + 1
        cursor += 1
    raise CatalogError("unterminated Go quoted literal in production registry")


def _next_go_code_char(source: str, start: int, wanted: str) -> int:
    """Find a delimiter while ignoring Go literals and comments."""

    cursor = start
    while cursor < len(source):
        if source.startswith("//", cursor) or source.startswith("/*", cursor):
            cursor = _skip_go_literal_or_comment(source, cursor)
            continue
        if source[cursor] in {"`", "\"", "'"}:
            cursor = _skip_go_literal_or_comment(source, cursor)
            continue
        if source[cursor] == wanted:
            return cursor
        cursor += 1
    raise CatalogError(f"production registry is missing delimiter {wanted!r}")


def _balanced_go_block(source: str, opening: int, left: str = "{", right: str = "}") -> int:
    """Return the matching delimiter, respecting Go literals and comments."""

    if opening >= len(source) or source[opening] != left:
        raise CatalogError(f"production registry expected {left!r} at offset {opening}")

    depth = 0
    cursor = opening
    while cursor < len(source):
        if source.startswith("//", cursor) or source.startswith("/*", cursor):
            cursor = _skip_go_literal_or_comment(source, cursor)
            continue
        if source[cursor] in {"`", "\"", "'"}:
            cursor = _skip_go_literal_or_comment(source, cursor)
            continue
        if source[cursor] == left:
            depth += 1
        elif source[cursor] == right:
            depth -= 1
            if depth == 0:
                return cursor
        cursor += 1
    raise CatalogError(f"production registry has an unterminated {left}{right} block")


def _strip_go_comments(source: str) -> str:
    """Replace comments with whitespace while preserving literals and lines."""

    chars = list(source)
    cursor = 0
    while cursor < len(source):
        if source.startswith("//", cursor):
            end = source.find("\n", cursor + 2)
            end = len(source) if end < 0 else end
            for index in range(cursor, end):
                chars[index] = " "
            cursor = end
            continue
        if source.startswith("/*", cursor):
            end = source.find("*/", cursor + 2)
            if end < 0:
                raise CatalogError("unterminated Go block comment in production registry")
            for index in range(cursor, end + 2):
                if chars[index] != "\n":
                    chars[index] = " "
            cursor = end + 2
            continue
        if source[cursor] in {"`", "\"", "'"}:
            cursor = _skip_go_literal_or_comment(source, cursor)
            continue
        cursor += 1
    return "".join(chars)


def _top_level_braced_items(source: str, opening: int, closing: int) -> list[tuple[int, int]]:
    """Extract the top-level composite literals in a Go slice literal."""

    items: list[tuple[int, int]] = []
    cursor = opening + 1
    while cursor < closing:
        while cursor < closing:
            if source[cursor].isspace() or source[cursor] == ",":
                cursor += 1
                continue
            if source.startswith("//", cursor) or source.startswith("/*", cursor):
                cursor = _skip_go_literal_or_comment(source, cursor)
                continue
            break
        if cursor >= closing:
            break
        if source[cursor] != "{":
            raise CatalogError(
                "production registry probe slice contains non-composite-literal content "
                f"at offset {cursor}"
            )
        item_end = _balanced_go_block(source, cursor)
        items.append((cursor, item_end))
        cursor = item_end + 1
    if not items:
        raise CatalogError("production registry probe slice is empty")
    return items


def _parse_go_string_constants(path: Path) -> dict[str, str]:
    """Read string constants and aliases from the canonical integration package."""

    try:
        source = _strip_go_comments(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError) as exc:
        raise CatalogError(f"cannot read integration constants: {exc}") from exc

    constants: dict[str, str] = {}
    # The status package keeps the ID constants in one const block. Restricting
    # extraction to that block avoids treating struct fields or test fixtures
    # as production identity definitions.
    block_match = re.search(r"\bconst\s*\(", source)
    if block_match is None:
        raise CatalogError(f"no canonical integration const block found in {path}")
    block_open = source.find("(", block_match.start(), block_match.end())
    block_close = _balanced_go_block(source, block_open, "(", ")")
    assignment_pattern = re.compile(
        r'^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(?:'
        r'("(?:\\.|[^"\\])*")|([A-Za-z_][A-Za-z0-9_]*))\s*$'
    )
    for line in source[block_open + 1 : block_close].splitlines():
        match = assignment_pattern.fullmatch(line)
        if match is None:
            continue
        symbol, literal, alias = match.groups()
        if literal is not None:
            try:
                constants[symbol] = json.loads(literal)
            except json.JSONDecodeError as exc:
                raise CatalogError(f"invalid Go string constant {symbol} in {path}: {exc}") from exc
        else:
            constants[symbol] = alias

    resolved: dict[str, str] = {}

    def resolve(symbol: str, chain: tuple[str, ...] = ()) -> str:
        if symbol in resolved:
            return resolved[symbol]
        if symbol in chain:
            raise CatalogError(f"cyclic integration constant alias: {' -> '.join(chain + (symbol,))}")
        value = constants.get(symbol)
        if value is None:
            raise CatalogError(f"integration probe references undeclared constant {symbol} in {path}")
        if value in constants:
            value = resolve(value, chain + (symbol,))
        resolved[symbol] = value
        return value

    for symbol in constants:
        resolve(symbol)
    return resolved


def parse_production_registration(root: Path) -> dict[str, str]:
    """Return the ID-to-runner map from the canonical production constructor.

    This intentionally parses one named constructor and its typed probe slice.
    It never searches arbitrary files for catalog IDs, so a mention in a test,
    document, or fixture cannot satisfy a production-registration claim.
    """

    source_path = ensure_repo_file(root, PRODUCTION_REGISTRY, "production registry")
    constants_path = ensure_repo_file(root, INTEGRATIONSTATUS_CONSTANTS, "integration constants")
    try:
        source = source_path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        raise CatalogError(f"cannot read production registry: {exc}") from exc

    function_match = re.search(
        rf"\bfunc\s+{re.escape(PRODUCTION_REGISTRY_FUNCTION)}\s*\(",
        source,
    )
    if function_match is None:
        raise CatalogError(
            f"production registry function not found: {PRODUCTION_REGISTRY}:{PRODUCTION_REGISTRY_FUNCTION}"
        )
    function_open = _next_go_code_char(source, function_match.end(), "{")
    function_close = _balanced_go_block(source, function_open)
    function_source = source[function_open + 1 : function_close]
    declarations = list(
        re.finditer(
            r"\bprobes\s*:=\s*\[\s*\]integrationstatus\.Probe\s*\{",
            _strip_go_comments(function_source),
        )
    )
    if len(declarations) != 1:
        raise CatalogError(
            f"production registry must contain exactly one {PRODUCTION_REGISTRY_DECLARATION!r} "
            f"inside {PRODUCTION_REGISTRY_FUNCTION} (found {len(declarations)})"
        )
    list_open = function_open + 1 + declarations[0].end() - 1
    list_close = _balanced_go_block(source, list_open)
    constants = _parse_go_string_constants(constants_path)

    registrations: dict[str, str] = {}
    for item_index, (item_open, item_close) in enumerate(_top_level_braced_items(source, list_open, list_close)):
        item = _strip_go_comments(source[item_open + 1 : item_close])
        match = re.fullmatch(
            r"\s*ID\s*:\s*integrationstatus\.([A-Za-z_][A-Za-z0-9_]*)\s*,\s*"
            r"Run\s*:\s*((?:[A-Za-z_][A-Za-z0-9_]*\.)*[A-Za-z_][A-Za-z0-9_]*|func\b.*?)(?:,\s*)?\s*",
            item,
            flags=re.DOTALL,
        )
        if match is None:
            raise CatalogError(
                f"production registry item {item_index + 1} must use "
                "{ID: integrationstatus.<ID_CONSTANT>, Run: <code-owned-probe>}"
            )
        symbol, runner = match.groups()
        if runner == "nil":
            raise CatalogError(f"production registry item {item_index + 1} has a nil probe runner")
        entry_id = constants.get(symbol)
        if entry_id is None:
            raise CatalogError(
                f"production registry item {item_index + 1} references undeclared integration ID constant {symbol}"
            )
        if not _ID_PATTERN.fullmatch(entry_id):
            raise CatalogError(
                f"production registry constant {symbol} resolves to an invalid integration ID {entry_id!r}"
            )
        if entry_id in registrations:
            raise CatalogError(f"production registry contains duplicate integration ID {entry_id!r}")
        registrations[entry_id] = runner

    return registrations


def validate_production_registrations(
    root: Path,
    entries: list[dict[str, Any]],
    registrations: dict[str, str] | None = None,
) -> None:
    """Require implementation-bearing catalog entries to be wired in production."""

    if registrations is None:
        registrations = parse_production_registration(root)
    catalog_ids = {entry["id"] for entry in entries}
    orphaned = sorted(set(registrations) - catalog_ids)
    if orphaned:
        raise CatalogError(
            "canonical production registry contains IDs absent from the catalog: " + ", ".join(orphaned)
        )

    missing_core = sorted(set(EXPECTED_IDS) - set(registrations))
    if missing_core:
        raise CatalogError("canonical production registry is missing core probe ID(s): " + ", ".join(missing_core))

    for entry in entries:
        classes = set(entry["classes"])
        implementation_classes = sorted(classes & {"local_capability", "provider_adapter"})
        if not implementation_classes:
            # A client surface (including a metadata-only catalog entry) may be
            # documented before it has a local/provider probe.
            continue
        if entry["id"] not in registrations:
            raise CatalogError(
                f"entries[{entry['id']}] claims {'/'.join(implementation_classes)} implementation but has no "
                f"production registration in {PRODUCTION_REGISTRY}:{PRODUCTION_REGISTRY_FUNCTION}"
            )


def _duplicate_key_guard(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise CatalogError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def load_json(path: Path, label: str) -> Any:
    try:
        with path.open("r", encoding="utf-8") as handle:
            return json.load(handle, object_pairs_hook=_duplicate_key_guard)
    except CatalogError:
        raise
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise CatalogError(f"{label} cannot be read as JSON: {exc}") from exc


def _path_text(path: Iterable[Any]) -> str:
    parts = [str(item) for item in path]
    return ".".join(parts) if parts else "<root>"


def validate_schema(catalog: Any, schema: Any) -> None:
    """Use jsonschema when available, with a dependency-free structural fallback."""

    try:
        import jsonschema  # type: ignore
    except ImportError:
        validate_schema_fallback(catalog, schema)
        return

    try:
        validator_cls = jsonschema.Draft202012Validator
        validator_cls.check_schema(schema)
        errors = sorted(validator_cls(schema).iter_errors(catalog), key=lambda error: list(error.path))
    except Exception as exc:  # pragma: no cover - protects the standalone verifier
        raise CatalogError(f"schema validator could not validate catalog: {exc}") from exc
    if errors:
        first = errors[0]
        raise CatalogError(f"schema validation at {_path_text(first.path)}: {first.message}")


def validate_schema_fallback(catalog: Any, schema: Any) -> None:
    """Cover the catalog's structural contract if jsonschema is unavailable."""

    if not isinstance(schema, dict) or schema.get("$schema") is None:
        raise CatalogError("catalog schema is not a JSON Schema object")
    if not isinstance(catalog, dict):
        raise CatalogError("catalog must be an object")
    if catalog.get("schema_version") != 1:
        raise CatalogError("schema_version must be 1")
    documentation = catalog.get("documentation")
    if not isinstance(documentation, dict):
        raise CatalogError("documentation must be an object")
    if documentation.get("table_path") != DOC_TABLE:
        raise CatalogError(f"documentation.table_path must be {DOC_TABLE}")
    if documentation.get("table_header") != "Integration":
        raise CatalogError("documentation.table_header must be Integration")
    if documentation.get("marker_prefix") != MARKER_PREFIX:
        raise CatalogError(f"documentation.marker_prefix must be {MARKER_PREFIX}")
    if documentation.get("marker_convention") != MARKER_CONVENTION:
        raise CatalogError("documentation.marker_convention is not the v1 convention")
    entries = catalog.get("entries")
    if not isinstance(entries, list) or len(entries) < len(EXPECTED_IDS):
        raise CatalogError(f"entries must contain at least {len(EXPECTED_IDS)} objects")
    entry_ids: list[Any] = []
    for index, entry in enumerate(entries):
        if not isinstance(entry, dict):
            raise CatalogError(f"entries[{index}] must be an object")
        required = {
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
        missing = sorted(required - set(entry))
        if missing:
            raise CatalogError(f"entries[{index}] missing required fields: {', '.join(missing)}")
        for field in ("id", "display_name", "purpose", "docs_row_marker"):
            if not isinstance(entry[field], str) or not entry[field]:
                raise CatalogError(f"entries[{index}].{field} must be a non-empty string")
        if not _ID_PATTERN.fullmatch(entry["id"]):
            raise CatalogError(f"entries[{index}].id does not follow the catalog identifier pattern")
        entry_ids.append(entry["id"])
        if not isinstance(entry["classes"], list) or not entry["classes"]:
            raise CatalogError(f"entries[{index}].classes must be a non-empty array")
        if not isinstance(entry["targets"], list) or not entry["targets"]:
            raise CatalogError(f"entries[{index}].targets must be a non-empty array")
        configuration = entry["configuration"]
        if not isinstance(configuration, dict):
            raise CatalogError(f"entries[{index}].configuration must be an object")
        for field in ("non_secret_keys", "secret_key_names", "secret_file_refs", "boundary"):
            if field not in configuration:
                raise CatalogError(f"entries[{index}].configuration missing {field}")
        status = entry["status"]
        if not isinstance(status, dict):
            raise CatalogError(f"entries[{index}].status must be an object")
        if status.get("canonical_states") != list(CANONICAL_STATES):
            raise CatalogError(f"entries[{index}].status.canonical_states must be the exact canonical trio")
        for field in ("raw_state_mappings", "api_route_prefixes"):
            if not isinstance(status.get(field), list) or not status[field]:
                raise CatalogError(f"entries[{index}].status.{field} must be a non-empty array")
        evidence = entry["evidence"]
        if not isinstance(evidence, dict):
            raise CatalogError(f"entries[{index}].evidence must be an object")
        for field in ("web", "docs", "tests"):
            if not isinstance(evidence.get(field), list) or not evidence[field]:
                raise CatalogError(f"entries[{index}].evidence.{field} must be a non-empty array")
    missing = sorted(set(EXPECTED_IDS) - set(entry_ids))
    if missing:
        raise CatalogError("entries is missing required core id(s): " + ", ".join(missing))


def ensure_repo_file(root: Path, value: Any, label: str) -> Path:
    if not isinstance(value, str) or not value or value.startswith("/"):
        raise CatalogError(f"{label} must be a relative repository path")
    candidate = (root / value).resolve()
    resolved_root = root.resolve()
    if candidate != resolved_root and resolved_root not in candidate.parents:
        raise CatalogError(f"{label} escapes the repository: {value}")
    if not candidate.is_file():
        raise CatalogError(f"{label} does not exist as a file: {value}")
    return candidate


def unique(values: Iterable[Any], label: str) -> None:
    values_list = list(values)
    if len(values_list) != len(set(values_list)):
        raise CatalogError(f"{label} contains duplicate values")


def slug(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "-", value.casefold()).strip("-")


def markdown_cells(line: str) -> list[str]:
    stripped = line.strip()
    if not stripped.startswith("|"):
        return []
    stripped = stripped.strip("|")
    return [cell.strip() for cell in stripped.split("|")]


def read_optional_integration_rows(path: Path) -> list[str]:
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeError) as exc:
        raise CatalogError(f"cannot read documentation table {path}: {exc}") from exc

    header_index = -1
    for index, line in enumerate(lines):
        cells = markdown_cells(line)
        if len(cells) >= 4 and cells[:4] == [
            "Integration",
            "Purpose",
            "Core requirement",
            "Configuration boundary",
        ]:
            header_index = index
            break
    if header_index < 0:
        raise CatalogError("optional-integrations.md table header was not found")

    rows: list[str] = []
    for line in lines[header_index + 2 :]:
        cells = markdown_cells(line)
        if not cells:
            break
        if len(cells) < 4:
            raise CatalogError("optional-integrations.md contains a malformed integration row")
        rows.append(cells[0])
    return rows


def check_evidence_secret_boundary(value: Any, label: str) -> None:
    """Reject credentials accidentally copied into evidence prose or fields."""

    if isinstance(value, dict):
        for key, child in value.items():
            lowered = str(key).casefold()
            if lowered in {
                "secret",
                "secret_value",
                "token",
                "token_value",
                "password",
                "password_value",
                "credential",
                "credential_value",
                "api_key",
                "api_token",
            }:
                raise CatalogError(f"{label}.{key} is a secret value field; evidence may contain paths and claims only")
            check_evidence_secret_boundary(child, f"{label}.{key}")
        return
    if isinstance(value, list):
        for index, child in enumerate(value):
            check_evidence_secret_boundary(child, f"{label}[{index}]")
        return
    if not isinstance(value, str):
        return
    if _SECRET_ASSIGNMENT.search(value):
        raise CatalogError(f"{label} looks like a secret assignment; record only a key name, never its value")
    if _SECRET_PREFIX.search(value):
        raise CatalogError(f"{label} looks like a bearer/API secret value")


def check_evidence_items(root: Path, entries: list[dict[str, Any]]) -> None:
    for entry in entries:
        entry_id = entry["id"]
        evidence = entry["evidence"]
        check_evidence_secret_boundary(evidence, f"entries[{entry_id}].evidence")
        for category in ("web", "docs", "tests"):
            items = evidence[category]
            for index, item in enumerate(items):
                if not isinstance(item, dict) or set(item) != {"path", "claim"}:
                    raise CatalogError(
                        f"entries[{entry_id}].evidence.{category}[{index}] must contain only path and claim"
                    )
                path = ensure_repo_file(root, item["path"], f"entries[{entry_id}].evidence.{category}[{index}].path")
                if category == "tests" and not _TEST_FILE.search(path.name):
                    raise CatalogError(
                        f"entries[{entry_id}].evidence.tests[{index}] is not a recognized test file: {item['path']}"
                    )

        agent = entry.get("agent")
        if agent is None:
            continue
        agent_evidence = agent.get("evidence") if isinstance(agent, dict) else None
        check_evidence_secret_boundary(agent_evidence, f"entries[{entry_id}].agent.evidence")
        for category in ("tasks", "capabilities"):
            for index, item in enumerate(agent_evidence[category]):
                if not isinstance(item, dict) or set(item) != {"path", "claim"}:
                    raise CatalogError(
                        f"entries[{entry_id}].agent.evidence.{category}[{index}] must contain only path and claim"
                    )
                ensure_repo_file(
                    root,
                    item["path"],
                    f"entries[{entry_id}].agent.evidence.{category}[{index}].path",
                )


def parse_api_routes(path: Path) -> set[str]:
    try:
        source = path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        raise CatalogError(f"cannot read route manifest: {exc}") from exc
    routes = set(re.findall(r'\{"(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)",\s*"([^"\n]+)"', source))
    if not routes:
        raise CatalogError(f"no API routes found in {ROUTE_MANIFEST}")
    return routes


def validate_route_prefixes(entries: list[dict[str, Any]], routes: set[str]) -> None:
    for entry in entries:
        for index, prefix in enumerate(entry["status"]["api_route_prefixes"]):
            if not isinstance(prefix, str) or not prefix.startswith("/api"):
                raise CatalogError(f"entries[{entry['id']}] route prefix {prefix!r} is not an /api prefix")
            normalized = prefix.rstrip("/") or "/"
            if not any(route == normalized or route.startswith(normalized + "/") for route in routes):
                raise CatalogError(
                    f"entries[{entry['id']}] status.api_route_prefixes[{index}] is absent from {ROUTE_MANIFEST}: {prefix}"
                )


def parse_agent_constants(path: Path) -> set[str]:
    try:
        source = path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        raise CatalogError(f"cannot read agent type constants: {exc}") from exc
    constants = set(re.findall(r"^\s*((?:Task|Capability)[A-Za-z0-9_]*)\s*=", source, flags=re.MULTILINE))
    if not constants:
        raise CatalogError(f"no task/capability constants found in {AGENT_TYPES}")
    return constants


def validate_agent_declarations(root: Path, entries: list[dict[str, Any]], constants: set[str]) -> None:
    for entry in entries:
        managed = "managed_node_capability" in entry["classes"] or "managed_node" in entry["targets"]
        if not managed:
            if "agent" in entry:
                raise CatalogError(f"entries[{entry['id']}] declares agent data without a managed-node target/class")
            continue

        agent = entry.get("agent")
        if not isinstance(agent, dict):
            raise CatalogError(f"entries[{entry['id']}] claims managed support without an agent declaration")
        tasks = agent.get("tasks")
        capabilities = agent.get("capabilities")
        evidence = agent.get("evidence")
        if not isinstance(tasks, list) or not tasks:
            raise CatalogError(f"entries[{entry['id']}] managed claim has no task evidence/declarations")
        if not isinstance(capabilities, list) or not capabilities:
            raise CatalogError(f"entries[{entry['id']}] managed claim has no capability evidence/declarations")
        if not isinstance(evidence, dict) or not evidence.get("tasks") or not evidence.get("capabilities"):
            raise CatalogError(f"entries[{entry['id']}] managed claim requires both task and capability evidence")
        unique(tasks, f"entries[{entry['id']}].agent.tasks")
        unique(capabilities, f"entries[{entry['id']}].agent.capabilities")
        for task in tasks:
            if not isinstance(task, str) or not task.startswith("Task") or task not in constants:
                raise CatalogError(f"entries[{entry['id']}] references undeclared agent task constant: {task!r}")
        for capability in capabilities:
            if not isinstance(capability, str) or not capability.startswith("Capability") or capability not in constants:
                raise CatalogError(
                    f"entries[{entry['id']}] references undeclared agent capability constant: {capability!r}"
                )


def validate_entries(root: Path, catalog: dict[str, Any]) -> list[dict[str, Any]]:
    entries = catalog["entries"]
    if not isinstance(entries, list) or len(entries) < len(EXPECTED_IDS):
        raise CatalogError(f"catalog must contain at least {len(EXPECTED_IDS)} entries")
    if any(not isinstance(entry, dict) for entry in entries):
        raise CatalogError("every catalog entry must be an object")

    ids = [entry["id"] for entry in entries]
    displays = [entry["display_name"] for entry in entries]
    markers = [entry["docs_row_marker"] for entry in entries]
    unique(ids, "catalog entry ids")
    unique(displays, "catalog display names")
    unique(markers, "catalog documentation row markers")
    missing = sorted(set(EXPECTED_IDS) - set(ids))
    if missing:
        raise CatalogError("catalog is missing required core id(s): " + ", ".join(missing))

    for entry in entries:
        entry_id = entry["id"]
        configuration = entry["configuration"]
        if set(configuration["non_secret_keys"]) & set(configuration["secret_key_names"]):
            raise CatalogError(f"entries[{entry_id}] repeats a key across secret and non-secret boundaries")
        for field in ("non_secret_keys", "secret_key_names", "secret_file_refs"):
            unique(configuration[field], f"entries[{entry_id}].configuration.{field}")
            for value in configuration[field]:
                if not isinstance(value, str) or not value or "=" in value or any(char.isspace() for char in value):
                    raise CatalogError(
                        f"entries[{entry_id}].configuration.{field} must contain names/references, not values"
                    )

        status = entry["status"]
        if tuple(status["canonical_states"]) != CANONICAL_STATES:
            raise CatalogError(f"entries[{entry_id}] must declare the exact canonical state trio")
        mappings = status["raw_state_mappings"]
        unique((mapping["raw"] for mapping in mappings), f"entries[{entry_id}].status.raw_state_mappings")
        mapped_states = {mapping["canonical"] for mapping in mappings}
        if mapped_states != set(CANONICAL_STATES):
            raise CatalogError(
                f"entries[{entry_id}] raw state mappings must cover exactly {', '.join(CANONICAL_STATES)}"
            )
        unique(status["api_route_prefixes"], f"entries[{entry_id}].status.api_route_prefixes")

        expected_marker = MARKER_PREFIX + slug(entry["display_name"])
        if entry["docs_row_marker"] != expected_marker:
            raise CatalogError(
                f"entries[{entry_id}].docs_row_marker must follow the stable slug convention: {expected_marker}"
            )

    check_evidence_items(root, entries)
    return entries


def validate_documentation_mapping(root: Path, catalog: dict[str, Any], entries: list[dict[str, Any]]) -> None:
    documentation = catalog["documentation"]
    if documentation["table_path"] != DOC_TABLE:
        raise CatalogError(f"documentation source must be {DOC_TABLE}")
    if documentation["table_header"] != "Integration":
        raise CatalogError("documentation table header marker must be Integration")
    if documentation["marker_prefix"] != MARKER_PREFIX:
        raise CatalogError(f"documentation marker prefix must be {MARKER_PREFIX}")
    if documentation["marker_convention"] != MARKER_CONVENTION:
        raise CatalogError("documentation marker convention does not match v1")

    rows = read_optional_integration_rows(ensure_repo_file(root, documentation["table_path"], "documentation.table_path"))
    if len(rows) != len(entries):
        raise CatalogError(
            f"optional-integrations.md has {len(rows)} rows; expected exactly {len(entries)} catalog entries"
        )
    by_marker = {entry["docs_row_marker"]: entry for entry in entries}
    matched: set[str] = set()
    for row_index, display_name in enumerate(rows):
        marker = MARKER_PREFIX + slug(display_name)
        entry = by_marker.get(marker)
        if entry is None:
            raise CatalogError(
                f"documentation row {row_index + 1} ({display_name!r}) has no catalog marker {marker}"
            )
        if entry["display_name"] != display_name:
            raise CatalogError(
                f"documentation row {row_index + 1} display name {display_name!r} does not match catalog entry "
                f"{entry['id']} ({entry['display_name']!r})"
            )
        matched.add(entry["id"])
    if matched != {entry["id"] for entry in entries}:
        raise CatalogError("documentation rows and catalog IDs are not a complete one-to-one mapping")


def parse_args() -> argparse.Namespace:
    script_root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=script_root, help="repository root")
    parser.add_argument("--catalog", type=Path, default=script_root / "extensions/catalog.json")
    parser.add_argument("--schema", type=Path, default=script_root / "extensions/catalog.schema.json")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    root = args.root.resolve()
    catalog_path = args.catalog if args.catalog.is_absolute() else root / args.catalog
    schema_path = args.schema if args.schema.is_absolute() else root / args.schema

    try:
        schema = load_json(schema_path, "catalog schema")
        catalog = load_json(catalog_path, "catalog")
        validate_schema(catalog, schema)
        if not isinstance(catalog, dict):
            raise CatalogError("catalog must be an object")
        entries = validate_entries(root, catalog)
        validate_documentation_mapping(root, catalog, entries)
        validate_production_registrations(root, entries)
        routes = parse_api_routes(ensure_repo_file(root, ROUTE_MANIFEST, "route manifest"))
        validate_route_prefixes(entries, routes)
        constants = parse_agent_constants(ensure_repo_file(root, AGENT_TYPES, "agent types"))
        ensure_repo_file(root, AGENT_SERVICE, "agent service")
        validate_agent_declarations(root, entries, constants)
    except CatalogError as exc:
        print(f"extension catalog verification failed: {exc}", file=sys.stderr)
        return 1
    except (KeyError, TypeError, ValueError) as exc:
        print(f"extension catalog verification failed: malformed catalog: {exc}", file=sys.stderr)
        return 1

    print(
        f"extension catalog verified: {len(entries)} integrations, schema, source, and production registration "
        "boundaries are valid"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
