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
assert document["x-hserver-contract-version"] == 67
assert document["x-hserver-route-count"] == 441
assert document["x-hserver-schema-count"] == 297

paths = document["paths"]
schemas = document["components"]["schemas"]


def response_schema(operation, status):
    return operation["responses"][status]["content"]["application/json"]["schema"]


def assert_ref(schema, name):
    assert schema == {"$ref": f"#/components/schemas/{name}"}, schema


list_operation = paths["/api/notify/channels"]["get"]
assert list_operation["x-hserver-access"] == "Authenticated"
assert list_operation["security"] == [{"panelBearer": []}, {"panelSession": []}]
assert not list_operation["description"].startswith("Generated route and access contract")
assert set(list_operation["responses"]) == {"200", "503", "default"}
assert response_schema(list_operation, "200") == {
    "type": "array",
    "items": {"$ref": "#/components/schemas/NotificationChannel"},
}
for status in ("503", "default"):
    assert_ref(response_schema(list_operation, status), "ErrorResponse")

detail_operation = paths["/api/notify/channels/{id}"]["get"]
assert detail_operation["x-hserver-access"] == "Authenticated"
assert detail_operation["parameters"] == [{
    "in": "path",
    "name": "id",
    "required": True,
    "schema": {"type": "string"},
}]
assert set(detail_operation["responses"]) == {"200", "400", "404", "503", "default"}
assert_ref(response_schema(detail_operation, "200"), "NotificationChannel")
for status in ("400", "404", "503", "default"):
    assert_ref(response_schema(detail_operation, status), "ErrorResponse")

channel = schemas["NotificationChannel"]
assert channel["type"] == "object"
assert channel["additionalProperties"] is False
assert channel["required"] == [
    "id", "name", "type", "config", "enabled", "createdAt", "updatedAt", "state", "detail",
]
assert set(channel["properties"]) == set(channel["required"])
assert channel["properties"]["id"] == {
    "format": "int64", "minimum": 1, "type": "integer",
}
assert channel["properties"]["type"]["enum"] == ["email", "telegram", "discord", "slack"]
assert channel["properties"]["state"]["enum"] == ["not_configured", "unavailable", "healthy"]
assert channel["properties"]["detail"]["enum"] == [
    "not_configured", "config_unavailable", "configured_disabled", "degraded",
    "probe_unverified", "delivery_confirmed", "delivery_failed", "delivery_stale",
]
assert channel["properties"]["config"]["type"] == "string"
assert "secret_configured" in channel["properties"]["config"]["description"]
assert channel["properties"]["createdAt"] == {"format": "date-time", "type": "string"}
assert channel["properties"]["updatedAt"] == {"format": "date-time", "type": "string"}

description = (list_operation["description"] + " " + detail_operation["description"]).lower()
for phrase in ("credential-free", "redacted", "secret_configured", "smtp password", "bot token", "webhook url"):
    assert phrase in description, phrase

print("notification channels OpenAPI contract checks passed")
PY
