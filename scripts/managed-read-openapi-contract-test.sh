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

python3 - "$tmp/docs/openapi.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    document = json.load(handle)

assert document["openapi"] == "3.1.0"
assert document["x-hserver-contract-version"] == 71
assert document["x-hserver-route-count"] == 443
assert document["x-hserver-schema-count"] == 321

paths = document["paths"]
schemas = document["components"]["schemas"]
expected_responses = {"200", "404", "409", "502", "504", "default"}


def response_schema(operation, status):
    return operation["responses"][status]["content"]["application/json"]["schema"]


def assert_ref(schema, name):
    assert schema == {"$ref": f"#/components/schemas/{name}"}, schema


def assert_auth(operation):
    assert operation["x-hserver-access"] == "Admin"
    assert operation["security"] == [{"panelBearer": []}, {"panelSession": []}]
    assert operation["parameters"] == [{
        "in": "path",
        "name": "id",
        "required": True,
        "schema": {"type": "string"},
    }]
    assert "requestBody" not in operation
    assert not operation["description"].startswith("Generated route and access contract")


routes = {
    "/api/nodes/{id}/php": {
        "schema": "ManagedPHPFPMVersionList",
        "capability": "php.read",
        "task": "php.inventory",
        "wait": "35 seconds",
        "kind": "PHP-FPM",
        "success": "version and pool",
    },
    "/api/nodes/{id}/pm2": {
        "schema": "ManagedPM2ProcessList",
        "capability": "pm2.read",
        "task": "pm2.list",
        "wait": "35 seconds",
        "kind": "PM2",
        "success": "process",
    },
    "/api/nodes/{id}/containers": {
        "schema": "ManagedContainerList",
        "capability": "container.read",
        "task": "container.list",
        "wait": "30 seconds",
        "kind": "Docker",
        "success": "container",
    },
}

for path, expected in routes.items():
    operation = paths[path]["get"]
    assert_auth(operation)
    assert set(operation["responses"]) == expected_responses
    assert_ref(response_schema(operation, "200"), expected["schema"])
    for status in ("404", "409", "502", "504", "default"):
        assert_ref(response_schema(operation, status), "ErrorResponse")

    description = operation["description"].lower()
    for phrase in (
        "read-only",
        "managed node",
        "credential-free",
        expected["capability"],
        expected["task"],
        expected["wait"],
        "typed",
        "no arbitrary provider",
        "task envelope",
        "command output",
        "secret",
    ):
        assert phrase in description, (path, phrase)

    assert "does not exist" in operation["responses"]["404"]["description"].lower()
    assert "no " in operation["responses"]["409"]["description"].lower()
    assert "task is created" in operation["responses"]["409"]["description"].lower()
    assert "failed" in operation["responses"]["502"]["description"].lower()
    assert "invalid typed" in operation["responses"]["502"]["description"].lower()
    wait_phrase = expected["wait"].replace(" seconds", "-second")
    assert wait_phrase in operation["responses"]["504"]["description"]
    assert "could not be read" in operation["responses"]["default"]["description"].lower()

metrics_operation = paths["/api/nodes/{id}/metrics"]["get"]
assert_auth(metrics_operation)
assert set(metrics_operation["responses"]) == expected_responses
assert_ref(response_schema(metrics_operation, "200"), "ManagedNodeMetrics")
for status in ("404", "409", "502", "504", "default"):
    assert_ref(response_schema(metrics_operation, status), "ErrorResponse")

metrics_description = metrics_operation["description"].lower()
for phrase in (
    "fresh",
    "read-only",
    "typed metrics snapshot",
    "managed node",
    "metrics.read",
    "empty-payload",
    "45 seconds",
    "task envelopes",
    "command output",
    "paths",
    "secrets",
):
    assert phrase in metrics_description, phrase
assert "does not exist" in metrics_operation["responses"]["404"]["description"].lower()
assert "metrics.read" in metrics_operation["responses"]["409"]["description"]
assert "no metrics task is created" in metrics_operation["responses"]["409"]["description"]
assert "failed" in metrics_operation["responses"]["502"]["description"].lower()
assert "invalid typed" in metrics_operation["responses"]["502"]["description"].lower()
assert "45-second" in metrics_operation["responses"]["504"]["description"]
assert "could not be read" in metrics_operation["responses"]["default"]["description"].lower()

ensure_operation = paths["/api/nodes/{node_id}/deploy/{target_id}/domains/{domain}"]["put"]
assert ensure_operation["x-hserver-access"] == "Admin"
assert ensure_operation["security"] == [{"panelBearer": []}, {"panelSession": []}]
assert ensure_operation["parameters"] == [
    {"in": "path", "name": "node_id", "required": True, "schema": {"type": "string"}},
    {"in": "path", "name": "target_id", "required": True, "schema": {"type": "string"}},
    {"in": "path", "name": "domain", "required": True, "schema": {"type": "string"}},
]
assert ensure_operation["requestBody"]["required"] is True
assert ensure_operation["requestBody"]["content"]["application/json"]["schema"] == {
    "$ref": "#/components/schemas/EnsureRemoteDeployDomainRequest"
}
assert set(ensure_operation["responses"]) == {"200", "400", "409", "422", "502", "504", "default"}
assert ensure_operation["responses"]["200"]["content"]["application/json"]["schema"] == {
    "$ref": "#/components/schemas/EnsureRemoteDeployDomainResponse"
}
for status in ("400", "409", "422", "502", "504", "default"):
    assert ensure_operation["responses"][status]["content"]["application/json"]["schema"] == {
        "$ref": "#/components/schemas/ErrorResponse"
    }
ensure_description = ensure_operation["description"].lower()
for phrase in (
    "admin-only", "revision-aware", "compare-and-swap", "idempotent no-op",
    "nginx content", "upstream urls", "shell commands",
):
    assert phrase in ensure_description, phrase
assert "75-second" in ensure_operation["responses"]["504"]["description"]

ensure_request = schemas["EnsureRemoteDeployDomainRequest"]
assert ensure_request["type"] == "object"
assert ensure_request["additionalProperties"] is False
assert ensure_request["required"] == ["expected_revision", "confirmed"]
assert set(ensure_request["properties"]) == {"expected_revision", "confirmed"}
assert ensure_request["properties"]["expected_revision"]["pattern"] == r"^(?:absent|[0-9a-f]{64})$"
assert ensure_request["properties"]["confirmed"] == {
    "type": "boolean", "const": True,
    "description": "Must be true to authorize this managed mutation.",
}

ensure_response = schemas["EnsureRemoteDeployDomainResponse"]
assert ensure_response["type"] == "object"
assert ensure_response["additionalProperties"] is False
assert ensure_response["required"] == ["changed", "observation"]
assert set(ensure_response["properties"]) == {"changed", "observation"}
assert ensure_response["properties"]["observation"] == {"$ref": "#/components/schemas/RemoteDeployDomain"}

managed_domain = schemas["RemoteDeployDomain"]
assert managed_domain["type"] == "object"
assert managed_domain["additionalProperties"] is False
assert managed_domain["required"] == [
    "target_id", "domain", "host_port", "desired_host_port", "upstream", "status",
    "message", "tls_status", "tls_message", "enabled", "revision",
]
assert managed_domain["properties"]["upstream"]["pattern"] == r"^http://127\.0\.0\.1:[1-9][0-9]{0,4}$"
assert managed_domain["properties"]["status"] == {"type": "string", "const": "active"}
assert managed_domain["properties"]["enabled"] == {"type": "boolean", "const": True}
assert managed_domain["properties"]["revision"] == {"type": "string", "pattern": r"^[0-9a-f]{64}$"}
assert "arbitrary upstream" in managed_domain["properties"]["upstream"]["description"].lower()

metrics_schema = schemas["ManagedNodeMetrics"]
assert metrics_schema["type"] == "object"
assert metrics_schema["additionalProperties"] is False
assert metrics_schema["required"] == [
    "observed_at", "cpu", "load", "memory", "network", "root_disk",
]
assert set(metrics_schema["properties"]) == set(metrics_schema["required"])
assert metrics_schema["properties"]["observed_at"] == {
    "type": "string", "format": "date-time", "description": "UTC RFC3339 observation timestamp.",
}
for property_name, schema_name in {
    "cpu": "ManagedNodeMetricsCPU",
    "load": "ManagedNodeMetricsLoad",
    "memory": "ManagedNodeMetricsMemory",
    "network": "ManagedNodeMetricsNetwork",
    "root_disk": "ManagedNodeMetricsFilesystem",
}.items():
    assert_ref(metrics_schema["properties"][property_name], schema_name)

metric_object_contracts = {
    "ManagedNodeMetricsCPU": {
        "usage_percent": {"type": "number", "minimum": 0, "maximum": 100},
        "core_count": {"type": "integer", "minimum": 1, "maximum": 65536},
    },
    "ManagedNodeMetricsLoad": {
        "one": {"type": "number", "minimum": 0, "maximum": 1000000},
        "five": {"type": "number", "minimum": 0, "maximum": 1000000},
        "fifteen": {"type": "number", "minimum": 0, "maximum": 1000000},
    },
    "ManagedNodeMetricsMemory": {
        "total_bytes": {"type": "integer", "minimum": 1},
        "used_bytes": {"type": "integer", "minimum": 0},
        "available_bytes": {"type": "integer", "minimum": 0},
        "usage_percent": {"type": "number", "minimum": 0, "maximum": 100},
    },
    "ManagedNodeMetricsNetwork": {
        "rx_bytes": {"type": "integer", "minimum": 0},
        "tx_bytes": {"type": "integer", "minimum": 0},
    },
    "ManagedNodeMetricsFilesystem": {
        "total_bytes": {"type": "integer", "minimum": 1},
        "used_bytes": {"type": "integer", "minimum": 0},
        "available_bytes": {"type": "integer", "minimum": 0},
        "usage_percent": {"type": "number", "minimum": 0, "maximum": 100},
    },
}
for name, properties in metric_object_contracts.items():
    schema = schemas[name]
    assert schema["type"] == "object", name
    assert schema["additionalProperties"] is False, name
    assert set(schema["properties"]) == set(properties), name
    assert schema["required"] == list(properties), name
    assert schema["properties"] == properties, name
    assert "credential-free" in schema["description"].lower(), name


list_contracts = {
    "ManagedPHPFPMVersionList": (32, "ManagedPHPFPMVersion"),
    "ManagedPM2ProcessList": (512, "ManagedPM2Process"),
    "ManagedContainerList": (256, "ManagedContainer"),
}
for name, (max_items, item_name) in list_contracts.items():
    schema = schemas[name]
    assert schema["type"] == "array", name
    assert schema["maxItems"] == max_items, name
    assert_ref(schema["items"], item_name)

strict_items = {
    "ManagedPHPFPMPool": {
        "name", "path", "user", "group", "listen", "pm", "max_children",
    },
    "ManagedPHPFPMVersion": {
        "version", "unit", "active", "enabled", "masked", "binary", "pools",
    },
    "ManagedPM2Process": {
        "id", "name", "status", "pid", "cpu", "memory", "uptime", "restarts",
        "mode", "cwd", "script", "version",
    },
    "ManagedContainer": {"id", "name", "image", "state", "status", "ports"},
}
for name, property_names in strict_items.items():
    schema = schemas[name]
    assert schema["type"] == "object", name
    assert schema["additionalProperties"] is False, name
    assert set(schema["properties"]) == property_names, name
    assert set(schema["required"]) <= property_names, name
    assert "credential-free" in schema["description"].lower(), name
    assert not {
        "password", "secret", "secrets", "token", "tokens", "credential", "credentials",
        "environment", "env", "raw",
    } & set(schema["properties"]), name

assert schemas["ManagedPHPFPMPool"]["required"] == ["name", "path"]
assert schemas["ManagedPHPFPMVersion"]["required"] == [
    "version", "unit", "active", "enabled", "masked", "pools",
]
assert schemas["ManagedPM2Process"]["required"] == [
    "id", "name", "status", "pid", "cpu", "memory", "uptime", "restarts",
    "mode", "cwd", "script", "version",
]
assert schemas["ManagedContainer"]["required"] == [
    "id", "name", "image", "state", "status", "ports",
]

assert schemas["ManagedPHPFPMVersion"]["properties"]["pools"] == {
    "type": "array",
    "maxItems": 512,
    "items": {"$ref": "#/components/schemas/ManagedPHPFPMPool"},
}
assert schemas["ManagedPHPFPMVersion"]["properties"]["binary"] == {
    "type": "string", "minLength": 1,
}
assert schemas["ManagedPM2Process"]["properties"]["memory"] == {
    "type": "integer", "format": "int64", "minimum": 0,
}
assert schemas["ManagedPM2Process"]["properties"]["uptime"] == {
    "type": "integer", "format": "int64", "minimum": 0,
}

heartbeat_capabilities = schemas["AgentHeartbeatRequest"]["properties"]["capabilities"]["items"]["enum"]
for capability in ("php.read", "pm2.read", "container.read"):
    assert capability in heartbeat_capabilities, capability

print("managed read OpenAPI contract checks passed")
PY
