#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
cleanup() {
	find "$tmp" -xdev -depth -delete >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

# Generate from the authoritative route manifest in an isolated root so this
# focused check never rewrites the committed API documents.
mkdir -p "$tmp/internal/api" "$tmp/docs"
cp "$repo_root/internal/api/routes_manifest.go" "$tmp/internal/api/routes_manifest.go"
HSERVER_ROOT="$tmp" go run "$repo_root/scripts/gen-api-routes/main.go" >"$tmp/generate.log"

python3 - "$tmp/docs/openapi.json" "$repo_root/docs/api-reference.md" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    document = json.load(handle)
with open(sys.argv[2], encoding="utf-8") as handle:
    api_reference = handle.read()

assert document["openapi"] == "3.1.0"
assert document["x-hserver-contract-version"] == 62
assert document["x-hserver-route-count"] == 438
assert document["x-hserver-schema-count"] == 286

paths = document["paths"]
schemas = document["components"]["schemas"]
operation = paths["/api/nodes/{id}/integrations/status"]["get"]

assert operation["x-hserver-access"] == "Admin"
assert operation["security"] == [{"panelBearer": []}, {"panelSession": []}]
assert operation["parameters"] == [{
    "in": "path",
    "name": "id",
    "required": True,
    "schema": {"type": "string"},
}]
assert not operation["description"].startswith("Generated route and access contract")
description = operation["description"].lower()
for phrase in (
    "schema-v1",
    "read-only",
    "managed-node",
    "integration.status",
    "one bounded batched task",
    "process.pm2",
    "pm2_inventory",
    "container.docker",
    "docker_info",
    "concurrent requests",
    "coalesce",
    "45 seconds",
    "canonical states",
    "safe error codes",
    "process/container inventory",
    "raw probe or task errors",
    "command output",
    "secrets",
):
    assert phrase in description, phrase

expected_responses = {"200", "404", "409", "502", "504", "default"}
assert set(operation["responses"]) == expected_responses


def response_schema(status):
    return operation["responses"][status]["content"]["application/json"]["schema"]


def assert_ref(schema, name):
    assert schema == {"$ref": f"#/components/schemas/{name}"}, schema


assert_ref(response_schema("200"), "ManagedIntegrationStatusResponse")
for status in ("404", "409", "502", "504", "default"):
    assert_ref(response_schema(status), "ErrorResponse")
assert "no task is created" in operation["responses"]["409"]["description"].lower()
assert "45-second" in operation["responses"]["504"]["description"]
assert "managed_status_failed" in operation["responses"]["502"]["description"]

response = schemas["ManagedIntegrationStatusResponse"]
assert response["type"] == "object"
assert response["additionalProperties"] is False
assert response["required"] == [
    "schema_version", "observed_at", "target", "results", "partial",
]
assert response["properties"]["schema_version"]["const"] == 1
assert response["properties"]["observed_at"] == {
    "description": "UTC RFC3339 timestamp for the managed-node observation.",
    "format": "date-time",
    "type": "string",
}
assert_ref(response["properties"]["target"], "ManagedIntegrationStatusTarget")
assert_ref(response["properties"]["results"]["items"], "ManagedIntegrationStatusResult")
assert response["properties"]["results"]["minItems"] == 2
assert response["properties"]["results"]["maxItems"] == 2
assert response["properties"]["partial"]["type"] == "boolean"

target = schemas["ManagedIntegrationStatusTarget"]
assert target["type"] == "object"
assert target["additionalProperties"] is False
assert target["required"] == ["scope", "node_id"]
assert target["properties"]["scope"]["const"] == "managed_node"
assert target["properties"]["node_id"]["pattern"] == r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$"

result = schemas["ManagedIntegrationStatusResult"]
assert result["type"] == "object"
assert result["additionalProperties"] is False
assert result["required"] == ["id", "state", "probe"]
assert result["properties"]["id"]["enum"] == ["process.pm2", "container.docker"]
assert result["properties"]["state"]["enum"] == ["not_configured", "unavailable", "healthy"]
assert result["properties"]["probe"]["enum"] == ["pm2_inventory", "docker_info"]
assert result["properties"]["error_code"]["enum"] == ["not_configured", "probe_failed", "timeout"]
assert result["properties"]["duration_ms"] == {
    "description": "Optional non-negative wall-clock probe duration in milliseconds.",
    "format": "int64",
    "minimum": 0,
    "type": "integer",
}
assert "inventory" in result["description"].lower()
assert "raw" in result["description"].lower()
assert "secret" in result["description"].lower()

for schema_name in (
    "ManagedIntegrationStatusResponse",
    "ManagedIntegrationStatusTarget",
    "ManagedIntegrationStatusResult",
):
    assert schema_name in schemas

heartbeat_capabilities = schemas["AgentHeartbeatRequest"]["properties"]["capabilities"]["items"]["enum"]
assert "integration.status" in heartbeat_capabilities

assert "### GET /api/nodes/{id}/integrations/status" in api_reference
reference = api_reference.lower()
for phrase in (
    "routeadmin",
    "integration.status",
    "read-only",
    "one bounded batched task",
    "coalesce",
    "45 seconds",
    "managed_node",
    "process.pm2",
    "pm2_inventory",
    "container.docker",
    "docker_info",
    "managed_node_offline",
    "capability_unavailable",
    "no task",
    "managed_status_timeout",
    "managed_status_failed",
    "raw",
    "secret",
    "inventory",
):
    assert phrase in reference, phrase

print("managed integration status OpenAPI contract checks passed")
PY
