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
assert document["x-hserver-schema-count"] == 286

paths = document["paths"]
schemas = document["components"]["schemas"]
operation = paths["/api/integrations/status"]["get"]

assert operation["x-hserver-access"] == "Authenticated"
assert operation["security"] == [{"panelBearer": []}, {"panelSession": []}]
assert not operation["description"].startswith("Generated route and access contract")
description = operation["description"].lower()
for phrase in (
    "local-only",
    "read-only",
    "every local integration entry",
    "fifteen required core probes",
    "reviewed additive catalog entries",
    "docker",
    "nginx",
    "firewall.ufw",
    "ufw_readiness",
    "tls.certbot",
    "certbot_readiness",
    "dns.bind9",
    "bind9_readiness",
    "runtime.php_fpm",
    "php_fpm_readiness",
    "database.local",
    "database_readiness",
    "storage.smartmontools",
    "smartmontools_readiness",
    "stalwart.mail",
    "stalwart_readiness",
    "mail.access",
    "mail_access_readiness",
    "backup.gdrive",
    "gdrive_readiness",
    "backup.snapshot.restic",
    "restic_readiness",
    "notification.delivery",
    "notification_readiness",
    "persisted fresh successful delivery receipt",
    "item-level results",
    "unprobed",
    "http 200",
    "managed-node status",
    "raw provider errors",
    "secrets",
):
    assert phrase in description, phrase
assert set(operation["responses"]) == {"200", "401", "500"}


def response_schema(status):
    return operation["responses"][status]["content"]["application/json"]["schema"]


def assert_ref(schema, name):
    assert schema == {"$ref": f"#/components/schemas/{name}"}, schema


assert_ref(response_schema("200"), "IntegrationStatusResponse")
assert_ref(response_schema("401"), "ErrorResponse")
assert_ref(response_schema("500"), "ErrorResponse")

expected_states = ["not_configured", "unavailable", "healthy"]

response = schemas["IntegrationStatusResponse"]
assert response["type"] == "object"
assert response["additionalProperties"] is False
assert response["required"] == [
    "schema_version", "observed_at", "target", "results", "unprobed", "partial",
]
assert response["properties"]["schema_version"]["const"] == 1
assert response["properties"]["observed_at"]["format"] == "date-time"
assert "RFC3339" in response["properties"]["observed_at"]["description"]
assert_ref(response["properties"]["target"], "IntegrationStatusTarget")
assert_ref(response["properties"]["results"]["items"], "IntegrationStatusResult")
assert "maxItems" not in response["properties"]["results"]
assert "additive" in response["properties"]["results"]["description"]
assert "enum" not in response["properties"]["unprobed"]["items"]
assert response["properties"]["unprobed"]["uniqueItems"] is True
assert "maxItems" not in response["properties"]["unprobed"]
assert "additive" in response["properties"]["unprobed"]["description"]
assert response["properties"]["partial"]["type"] == "boolean"

target = schemas["IntegrationStatusTarget"]
assert target["type"] == "object"
assert target["additionalProperties"] is False
assert target["required"] == ["scope"]
assert target["properties"]["scope"]["const"] == "local_host"

result = schemas["IntegrationStatusResult"]
assert result["type"] == "object"
assert result["additionalProperties"] is False
assert result["required"] == ["id", "state", "probe"]
assert result["properties"]["id"]["pattern"] == r"^[a-z0-9]+(?:[._-][a-z0-9]+)*$"
assert "enum" not in result["properties"]["id"]
assert "current catalog" in result["properties"]["id"]["description"]
assert result["properties"]["state"]["enum"] == expected_states
assert result["properties"]["probe"]["pattern"] == r"^[a-z][a-z0-9_]*$"
assert "enum" not in result["properties"]["probe"]
assert "additive" in result["properties"]["probe"]["description"]
assert result["properties"]["error_code"]["enum"] == [
    "not_configured", "probe_failed", "timeout",
]
assert result["properties"]["duration_ms"] == {
    "description": "Optional non-negative wall-clock probe duration in milliseconds.",
    "format": "int64",
    "minimum": 0,
    "type": "integer",
}

for schema_name in (
    "IntegrationStatusResponse",
    "IntegrationStatusTarget",
    "IntegrationStatusResult",
):
    assert schema_name in schemas

assert "### GET /api/integrations/status" in api_reference
reference = api_reference.lower()
for phrase in (
    "local-only",
    "read-only",
    "fifteen required core probes",
    "additive catalog entries",
    "docker",
    "nginx",
    "firewall.ufw",
    "ufw_readiness",
    "tls.certbot",
    "certbot_readiness",
    "dns.bind9",
    "bind9_readiness",
    "runtime.php_fpm",
    "php_fpm_readiness",
    "database.local",
    "database_readiness",
    "storage.smartmontools",
    "smartmontools_readiness",
    "stalwart.mail",
    "stalwart_readiness",
    "mail.access",
    "mail_access_readiness",
    "backup.gdrive",
    "gdrive_readiness",
    "backup.snapshot.restic",
    "restic_readiness",
    "notification.delivery",
    "notification_readiness",
    "persisted fresh successful delivery receipt",
    "item-level",
    "unprobed",
    "per-item",
    "http 200",
    "managed-node status",
    "raw provider errors",
    "secret",
):
    assert phrase in reference, phrase

print("integration status OpenAPI contract checks passed")
PY
