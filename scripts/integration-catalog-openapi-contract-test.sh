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

paths = document["paths"]
schemas = document["components"]["schemas"]
operation = paths["/api/integrations/catalog"]["get"]

assert operation["x-hserver-access"] == "Authenticated"
assert operation["security"] == [{"panelBearer": []}, {"panelSession": []}]
assert "live health" in operation["description"].lower()
assert "does not perform live health probes" in operation["description"]
assert not operation["description"].startswith("Generated route and access contract")
assert "default" not in operation["responses"]

def response_schema(status):
    return operation["responses"][status]["content"]["application/json"]["schema"]

assert response_schema("200") == {"$ref": "#/components/schemas/IntegrationCatalog"}
assert response_schema("401") == {"$ref": "#/components/schemas/ErrorResponse"}

expected_nested = {
    "IntegrationCatalogDocumentation",
    "IntegrationCatalogEntry",
    "IntegrationConfiguration",
    "IntegrationStatus",
    "IntegrationRawStateMapping",
    "IntegrationAgent",
    "IntegrationAgentEvidence",
    "IntegrationEvidence",
    "IntegrationEvidenceItem",
}
assert expected_nested <= schemas.keys()

def verify_refs(value):
    if isinstance(value, dict):
        ref = value.get("$ref")
        if ref is not None:
            prefix = "#/components/schemas/"
            assert isinstance(ref, str) and ref.startswith(prefix), ref
            assert ref[len(prefix):] in schemas, ref
        for nested in value.values():
            verify_refs(nested)
    elif isinstance(value, list):
        for nested in value:
            verify_refs(nested)

verify_refs(document)

catalog = schemas["IntegrationCatalog"]
assert catalog["required"] == ["schema_version", "documentation", "entries"]
assert catalog["properties"]["schema_version"]["const"] == 1
assert catalog["properties"]["entries"]["items"] == {"$ref": "#/components/schemas/IntegrationCatalogEntry"}
assert catalog["properties"]["entries"]["minItems"] == 15
assert "maxItems" not in catalog["properties"]["entries"]
assert "at least 15 required core entries; additive entries allowed" in catalog["properties"]["entries"]["description"]
assert "at least 15 required core entries; additive entries allowed" in operation["responses"]["200"]["description"]
schema_marker = catalog["properties"]["$schema"]
assert schema_marker["const"] == "./catalog.schema.json"
assert "local marker" in schema_marker["description"]
assert "dereferenceable URI" in schema_marker["description"]

entry = schemas["IntegrationCatalogEntry"]
assert entry["properties"]["requirement"]["enum"] == ["optional", "feature_specific"]
assert entry["properties"]["classes"]["items"]["enum"] == [
    "local_capability", "managed_node_capability", "provider_adapter", "client_surface",
]
assert entry["properties"]["targets"]["items"]["enum"] == ["local_host", "managed_node"]
for name in ("configuration", "status", "agent", "evidence"):
    assert entry["properties"][name]["$ref"].startswith("#/components/schemas/"), name

configuration = schemas["IntegrationConfiguration"]
assert set(configuration["properties"]) == {
    "non_secret_keys", "secret_key_names", "secret_file_refs", "boundary",
}
assert "secret values are never returned" in configuration["properties"]["secret_key_names"]["description"]
assert "file contents are never returned" in configuration["properties"]["secret_file_refs"]["description"]

status = schemas["IntegrationStatus"]
canonical = status["properties"]["canonical_states"]
assert canonical["const"] == ["not_configured", "unavailable", "healthy"]
assert canonical["items"]["enum"] == ["not_configured", "unavailable", "healthy"]
assert schemas["IntegrationRawStateMapping"]["properties"]["canonical"]["enum"] == [
    "not_configured", "unavailable", "healthy",
]

assert "### GET /api/integrations/catalog" in api_reference
assert "at least 15 required core entries; additive entries allowed" in api_reference
assert "not** run provider probes" in api_reference
assert "dereferenceable URI" in api_reference

print("integration catalog OpenAPI contract checks passed")
PY
