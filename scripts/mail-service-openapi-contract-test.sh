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


def response_schema(operation, status):
    return operation["responses"][status]["content"]["application/json"]["schema"]


def assert_ref(schema, name):
    assert schema == {"$ref": f"#/components/schemas/{name}"}, schema


def assert_auth(operation):
    assert operation["x-hserver-access"] == "Authenticated"
    assert operation["security"] == [{"panelBearer": []}, {"panelSession": []}]
    assert not operation["description"].startswith("Generated route and access contract")


status_operations = [
    paths["/api/mail/service/status"]["get"],
    paths["/api/mail/status"]["get"],
]
for operation in status_operations:
    assert_auth(operation)
    assert set(operation["responses"]) == {"200"}
    assert_ref(response_schema(operation, "200"), "MailServiceStatus")
    assert "not_configured" in operation["description"]

overview = paths["/api/mail/service/overview"]["get"]
assert_auth(overview)
assert set(overview["responses"]) == {"200"}
assert_ref(response_schema(overview, "200"), "MailServiceOverview")
assert "HTTP 200" in overview["description"]
assert "not configured or unavailable" in overview["description"]

config = paths["/api/mail/config"]["get"]
assert_auth(config)
assert set(config["responses"]) == {"200", "500", "503"}
assert "content" not in config["responses"]["200"]
for status in ("500", "503"):
    assert_ref(response_schema(config, status), "ErrorResponse")
assert "raw provider configuration" in config["description"]
assert "redacted contract" in config["description"]

for path, schema_name in (
    ("/api/mail/version", "MailServiceVersion"),
    ("/api/mail/storage", "MailServiceStorage"),
):
    operation = paths[path]["get"]
    assert_auth(operation)
    assert set(operation["responses"]) == {"200", "500", "503"}
    assert_ref(response_schema(operation, "200"), schema_name)
    for status in ("500", "503"):
        assert_ref(response_schema(operation, status), "ErrorResponse")

listeners = paths["/api/mail/listeners"]["get"]
assert_auth(listeners)
assert set(listeners["responses"]) == {"200", "500", "503"}
assert response_schema(listeners, "200") == {
    "type": "array",
    "items": {"$ref": "#/components/schemas/MailServiceListener"},
}
for status in ("500", "503"):
    assert_ref(response_schema(listeners, status), "ErrorResponse")

expected_schemas = {
    "MailServiceListener",
    "MailServiceOverview",
    "MailServiceOverviewSources",
    "MailServiceSource",
    "MailServiceStatus",
    "MailServiceStatusSource",
    "MailServiceStorage",
    "MailServiceVersion",
}
assert expected_schemas <= schemas.keys()

for name in expected_schemas:
    schema = schemas[name]
    assert schema["type"] == "object", name
    assert schema["additionalProperties"] is False, name
    assert set(schema["properties"]) >= set(schema["required"]), name

status = schemas["MailServiceStatus"]
assert status["required"] == ["running", "status"]
assert status["properties"]["status"]["enum"] == [
    "running", "stopped", "failed", "unknown", "not_configured",
]
assert status["properties"]["running"] == {"type": "boolean"}
assert set(status["properties"]) == {"running", "status", "pid", "uptime"}

version = schemas["MailServiceVersion"]
assert version["required"] == ["raw", "version"]
assert set(version["properties"]) == {"raw", "version"}

listener = schemas["MailServiceListener"]
assert listener["required"] == ["id", "protocol", "tls"]
assert set(listener["properties"]) == {"id", "protocol", "bind", "port", "tls"}

storage = schemas["MailServiceStorage"]
assert storage["required"] == ["backend"]
assert set(storage["properties"]) == {"backend", "path", "sizeBytes"}
assert storage["properties"]["sizeBytes"] == {
    "format": "int64", "minimum": 0, "type": "integer",
}

source = schemas["MailServiceSource"]
assert source["required"] == ["available", "state"]
assert source["properties"]["state"]["enum"] == [
    "healthy", "unavailable", "not_configured",
]
status_source = schemas["MailServiceStatusSource"]
assert status_source["properties"]["state"]["enum"] == [
    "running", "stopped", "failed", "unknown", "not_configured",
]

sources = schemas["MailServiceOverviewSources"]
assert sources["required"] == ["status", "version", "listeners", "storage"]
assert sources["properties"]["status"] == {
    "$ref": "#/components/schemas/MailServiceStatusSource",
}
for name in ("version", "listeners", "storage"):
    assert sources["properties"][name] == {
        "$ref": "#/components/schemas/MailServiceSource",
    }

overview_schema = schemas["MailServiceOverview"]
assert overview_schema["required"] == [
    "status", "version", "listeners", "storage", "sources",
]
assert overview_schema["properties"]["status"] == {
    "$ref": "#/components/schemas/MailServiceStatus",
}
assert overview_schema["properties"]["version"] == {
    "$ref": "#/components/schemas/MailServiceVersion",
}
assert overview_schema["properties"]["listeners"] == {
    "type": ["array", "null"],
    "items": {"$ref": "#/components/schemas/MailServiceListener"},
}
assert overview_schema["properties"]["storage"] == {
    "$ref": "#/components/schemas/MailServiceStorage",
}
assert overview_schema["properties"]["sources"] == {
    "$ref": "#/components/schemas/MailServiceOverviewSources",
}

# These promoted read schemas must not advertise secret material or raw
# provider configuration/log bodies. The config route is intentionally
# unmodeled above for that same reason.
for name in expected_schemas:
    properties = set(schemas[name]["properties"])
    assert not {
        "password", "secret", "secrets", "hash", "body", "config", "values", "logs",
    } & properties, name

print("mail service OpenAPI contract checks passed")
PY
