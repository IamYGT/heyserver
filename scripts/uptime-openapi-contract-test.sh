#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
cleanup() {
	find "$tmp" -xdev -depth -delete >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

# The generator writes both generated documents. Use an isolated root so this
# focused contract check never regenerates or changes the repository's docs.
mkdir -p "$tmp/internal/api" "$tmp/docs"
cp "$repo_root/internal/api/routes_manifest.go" "$tmp/internal/api/routes_manifest.go"
HSERVER_ROOT="$tmp" go run "$repo_root/scripts/gen-api-routes/main.go" >"$tmp/generate.log"

python3 - "$tmp/docs/openapi.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    document = json.load(handle)

assert document["openapi"] == "3.1.0"
assert document["x-hserver-contract-version"] >= 52
schemas = document["components"]["schemas"]
paths = document["paths"]

def body_schema(path, method):
    return paths[path][method]["requestBody"]["content"]["application/json"]["schema"]

def response_schema(path, method, status):
    return paths[path][method]["responses"][status]["content"]["application/json"]["schema"]

def assert_ref(schema, name):
    assert schema == {"$ref": f"#/components/schemas/{name}"}, schema

monitor_create = schemas["UptimeMonitorCreateRequest"]
assert monitor_create["type"] == "object"
assert monitor_create["additionalProperties"] is False
assert monitor_create["required"] == ["name", "type"]
assert {branch["properties"]["type"]["const"] for branch in monitor_create["oneOf"]} == {"http", "tcp", "ping", "dns"}
assert monitor_create["properties"]["interval_secs"]["minimum"] == 10
assert monitor_create["properties"]["interval_secs"]["maximum"] == 86400
assert monitor_create["properties"]["timeout_secs"]["maximum"] == 300
assert monitor_create["properties"]["retries"]["maximum"] == 10
assert monitor_create["properties"]["retry_interval"]["maximum"] == 3600
assert monitor_create["properties"]["alert_channel_ids"]["maxItems"] == 128
assert monitor_create["properties"]["req_headers"]["maxLength"] == 32768
assert monitor_create["properties"]["req_body"]["maxLength"] == 65536

monitor_update = schemas["UptimeMonitorUpdateRequest"]
assert monitor_update["additionalProperties"] is False
assert monitor_update["minProperties"] == 1
assert "required" not in monitor_update
assert "Omitted fields remain unchanged" in paths["/api/uptime/monitors/{id}"]["put"]["description"]
assert "null" in monitor_update["properties"]["group_id"]["type"]

settings = schemas["UptimeSettings"]
assert settings["additionalProperties"] is False
assert set(settings["required"]) == {
    "uptime_retention_days", "uptime_compact_after_days",
    "uptime_default_interval", "uptime_default_timeout", "uptime_default_channels",
}
assert schemas["UptimeSettingsUpdateRequest"]["minProperties"] == 1
assert schemas["UptimeSettingsUpdateRequest"]["additionalProperties"] is False
assert settings["properties"]["uptime_retention_days"]["pattern"]
assert settings["properties"]["uptime_compact_after_days"]["pattern"]
assert settings["properties"]["uptime_default_interval"]["pattern"]
assert settings["properties"]["uptime_default_timeout"]["pattern"]
assert settings["properties"]["uptime_default_channels"]["maxLength"] == 4096

status_page = schemas["UptimeStatusPage"]
assert status_page["additionalProperties"] is False
assert {"id", "slug", "title", "theme", "is_public", "history_days", "created_at"} <= set(status_page["required"])
status_create = schemas["UptimeStatusPageCreateRequest"]
assert status_create["required"] == ["slug", "title"]
assert status_create["properties"]["history_days"]["default"] == 90
assert status_create["properties"]["theme"]["default"] == "auto"
assert status_create["properties"]["monitors"]["maxItems"] == 128
status_update = schemas["UptimeStatusPageUpdateRequest"]
assert status_update["additionalProperties"] is False
assert status_update["minProperties"] == 1
assert "required" not in status_update
assert "omission preserves" in status_update["properties"]["monitors"]["description"]
assert "Omitted fields" in paths["/api/uptime/status-pages/{id}"]["put"]["description"]

for path in (
    "/api/uptime/monitors/{id}",
    "/api/uptime/status-pages/{id}",
):
    id_parameter = next(item for item in paths[path]["put"]["parameters"] if item["name"] == "id")
    assert id_parameter["schema"] == {"type": "integer", "format": "int64", "minimum": 1}

assert_ref(body_schema("/api/uptime/monitors", "post"), "UptimeMonitorCreateRequest")
assert_ref(body_schema("/api/uptime/monitors/{id}", "put"), "UptimeMonitorUpdateRequest")
assert_ref(response_schema("/api/uptime/monitors", "post", "201"), "UptimeMonitor")
assert_ref(response_schema("/api/uptime/monitors/{id}", "put", "200"), "UptimeMonitor")
assert_ref(response_schema("/api/uptime/settings", "get", "200"), "UptimeSettings")
assert_ref(body_schema("/api/uptime/settings", "put"), "UptimeSettingsUpdateRequest")
assert_ref(response_schema("/api/uptime/settings", "put", "200"), "UptimeSettings")
assert_ref(response_schema("/api/uptime/status-pages", "get", "200")["items"], "UptimeStatusPage")
assert_ref(body_schema("/api/uptime/status-pages", "post"), "UptimeStatusPageCreateRequest")
assert_ref(body_schema("/api/uptime/status-pages/{id}", "put"), "UptimeStatusPageUpdateRequest")
assert_ref(response_schema("/api/uptime/status-pages", "post", "201"), "UptimeStatusPage")
assert_ref(response_schema("/api/uptime/status-pages/{id}", "put", "200"), "UptimeStatusPage")
assert_ref(response_schema("/api/uptime/status-pages/{id}", "delete", "200"), "DeleteStatus")

print("uptime OpenAPI contract checks passed")
PY
