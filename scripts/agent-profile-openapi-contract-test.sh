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
assert document["x-hserver-contract-version"] == 66
assert document["x-hserver-route-count"] == 441
assert document["x-hserver-schema-count"] == 296

paths = document["paths"]
schemas = document["components"]["schemas"]
get_operation = paths["/api/nodes/{id}/profile"]["get"]
put_operation = paths["/api/nodes/{id}/profile"]["put"]
apply_operation = paths["/api/nodes/{id}/profile/apply"]["post"]

for operation in (get_operation, put_operation, apply_operation):
    assert operation["x-hserver-access"] == "Admin"
    assert operation["security"] == [{"panelBearer": []}, {"panelSession": []}]
    assert operation["parameters"] == [{
        "in": "path",
        "name": "id",
        "required": True,
        "schema": {"type": "string"},
    }]
    assert not operation["description"].startswith("Generated route and access contract")

assert set(get_operation["responses"]) == {"200", "404", "default"}
assert set(put_operation["responses"]) == {"200", "400", "404", "409", "429", "default"}
assert set(apply_operation["responses"]) == {"200", "202", "400", "404", "409", "429", "default"}

def response_ref(operation, status, schema_name):
    assert operation["responses"][status]["content"]["application/json"]["schema"] == {
        "$ref": f"#/components/schemas/{schema_name}"
    }

for operation, statuses in (
    (get_operation, ("200", "404", "default")),
    (put_operation, ("200", "400", "404", "409", "429", "default")),
    (apply_operation, ("200", "202", "400", "404", "409", "429", "default")),
):
    for status in statuses:
        response_ref(operation, status, "AgentProfileResponse" if status in {"200", "202"} and operation is apply_operation or status == "200" else "ErrorResponse")

assert put_operation["requestBody"]["required"] is True
assert put_operation["requestBody"]["content"]["application/json"]["schema"] == {
    "$ref": "#/components/schemas/AgentProfilePutRequest"
}
assert apply_operation["requestBody"]["required"] is True
assert apply_operation["requestBody"]["content"]["application/json"]["schema"] == {
    "$ref": "#/components/schemas/AgentProfileApplyRequest"
}

get_description = get_operation["description"].lower()
for phrase in ("admin-only", "desired", "raw", "profile=null", "matching desired.revision", "completed agent.profile.apply task"):
    assert phrase in get_description, phrase
put_description = put_operation["description"].lower()
for phrase in ("strict", "exactly profile and expectedrevision", "compare-and-swap", "no agent task", "profile/apply"):
    assert phrase in put_description, phrase
apply_description = apply_operation["description"].lower()
for phrase in ("admin", "expectedrevision", "at least 1", "confirmed=true", "agent.profile.apply", "secret value", "http 200", "http 202", "transport acknowledgement"):
    assert phrase in apply_description, phrase

profile = schemas["AgentProfile"]
assert profile["type"] == "object"
assert profile["additionalProperties"] is False
assert profile["required"] == [
    "allowDeployRead", "allowDeployActions", "allowDeployDomainRead",
    "allowDeployDomainActions", "deployPlansFile", "deployAcmeWebroot", "deployWriteRoots",
]
assert set(profile["properties"]) == set(profile["required"])
for name in profile["required"][:4]:
    assert profile["properties"][name]["type"] == "boolean"
for name in ("deployPlansFile", "deployAcmeWebroot"):
    path = profile["properties"][name]
    assert path["type"] == "string"
    assert path["maxLength"] == 4096
    assert path["pattern"] == r"^$|^/(?!$)(?!\.{1,2}(?:/|$))(?!.*//)(?!.*\/\.{1,2}(?:/|$))[A-Za-z0-9._+:-]+(?:/[A-Za-z0-9._+:-]+)*$"
roots = profile["properties"]["deployWriteRoots"]
assert roots["type"] == "array"
assert roots["maxItems"] == 16
assert roots["uniqueItems"] is True
assert roots["items"]["type"] == "string"
assert roots["items"]["minLength"] == 1
assert roots["items"]["maxLength"] == 4096
assert roots["items"]["pattern"] == profile["properties"]["deployPlansFile"]["pattern"]

put_request = schemas["AgentProfilePutRequest"]
assert put_request["type"] == "object"
assert put_request["additionalProperties"] is False
assert put_request["required"] == ["profile", "expectedRevision"]
assert put_request["properties"]["profile"] == {"$ref": "#/components/schemas/AgentProfile"}
assert put_request["properties"]["expectedRevision"]["minimum"] == 0

apply_request = schemas["AgentProfileApplyRequest"]
assert apply_request["type"] == "object"
assert apply_request["additionalProperties"] is False
assert apply_request["required"] == ["expectedRevision", "confirmed"]
assert set(apply_request["properties"]) == {"expectedRevision", "confirmed"}
assert apply_request["properties"]["expectedRevision"]["type"] == "integer"
assert apply_request["properties"]["expectedRevision"]["minimum"] == 1
assert apply_request["properties"]["confirmed"] == {
    "const": True,
    "description": "Must be true to authorize this remote mutation.",
    "type": "boolean",
}

desired = schemas["AgentProfileDesiredResponse"]
assert desired["type"] == "object"
assert desired["additionalProperties"] is False
assert desired["required"] == ["state", "revision", "profile"]
assert set(desired["properties"]["state"]["enum"]) == {"configured", "not_configured"}
assert desired["properties"]["revision"]["minimum"] == 0
assert desired["properties"]["profile"]["oneOf"] == [
    {"$ref": "#/components/schemas/AgentProfile"}, {"type": "null"}
]

safe_error_codes = {
    "", "not_configured", "invalid_profile", "permission_denied", "write_failed",
    "restart_failed", "apply_failed", "profile_apply_failed", "unsupported",
    "not_supported", "timeout", "stale_revision", "invalid_revision", "agent_error",
    "unknown", "profile_missing", "profile_corrupt", "profile_state_corrupt",
    "profile_revision_invalid", "profile_payload_invalid", "profile_payload_too_large",
    "profile_apply_unavailable", "profile_schedule_failed", "profile_store_failed",
    "profile_superseded",
}
observation = schemas["AgentProfileObservation"]
assert observation["type"] == "object"
assert observation["additionalProperties"] is False
assert observation["required"] == ["state", "revision"]
assert set(observation["properties"]["state"]["enum"]) == {"not_configured", "pending_restart", "applied", "failed"}
assert observation["properties"]["revision"]["type"] == "integer"
assert observation["properties"]["revision"]["minimum"] == 0
assert set(observation["properties"]["error_code"]["enum"]) == safe_error_codes

observed = schemas["AgentProfileObservedResponse"]
assert observed["type"] == "object"
assert observed["additionalProperties"] is False
assert observed["required"] == [
    "capabilities", "online", "lastSeenAt", "agentVersion", "protocolVersion", "profileState",
    "profileRevision", "profileErrorCode",
]
capabilities = observed["properties"]["capabilities"]
assert capabilities["type"] == ["array", "null"]
assert capabilities["uniqueItems"] is True
assert capabilities["items"] == {"type": "string"}
assert "enum" not in capabilities["items"]
assert set(observed["properties"]["profileState"]["enum"]) == {
    "not_reported", "not_configured", "pending_restart", "applied", "failed",
}
assert observed["properties"]["profileRevision"]["type"] == ["integer", "null"]
assert observed["properties"]["profileRevision"]["minimum"] == 0
assert observed["properties"]["profileErrorCode"]["type"] == ["string", "null"]
assert set(value for value in observed["properties"]["profileErrorCode"]["enum"] if value is not None) == safe_error_codes

apply = schemas["AgentProfileApplyResponse"]
assert apply["type"] == "object"
assert apply["additionalProperties"] is False
assert apply["required"] == ["state", "reason"]
assert set(apply["properties"]["state"]["enum"]) == {
    "manual_required", "not_requested", "queued", "running", "awaiting_heartbeat",
    "applied", "failed", "drifted",
}
assert set(apply["properties"]["reason"]["enum"]) == safe_error_codes | {
    "self_apply_not_supported", "profile_revision_drift",
}
for field in ("desiredRevision", "taskId", "observedRevision", "observedState"):
    assert "null" in apply["properties"][field]["type"]
assert apply["properties"]["desiredRevision"]["minimum"] == 0
assert apply["properties"]["taskId"]["minimum"] == 1
assert apply["properties"]["observedRevision"]["minimum"] == 0
assert set(value for value in apply["properties"]["observedState"]["enum"] if value is not None) == {
    "not_configured", "pending_restart", "applied", "failed",
}

response = schemas["AgentProfileResponse"]
assert response["type"] == "object"
assert response["additionalProperties"] is False
assert response["required"] == ["nodeId", "desired", "observed", "apply"]
assert response["properties"]["nodeId"]["type"] == "string"
assert response["properties"]["desired"] == {"$ref": "#/components/schemas/AgentProfileDesiredResponse"}
assert response["properties"]["observed"] == {"$ref": "#/components/schemas/AgentProfileObservedResponse"}
assert response["properties"]["apply"] == {"$ref": "#/components/schemas/AgentProfileApplyResponse"}

heartbeat_profile = schemas["AgentHeartbeatRequest"]["properties"]["profile"]
assert heartbeat_profile["oneOf"] == [
    {"$ref": "#/components/schemas/AgentProfileObservation"}, {"type": "null"}
]
assert "agent.profile.apply" in schemas["AgentHeartbeatRequest"]["properties"]["capabilities"]["items"]["enum"]
assert "agent.profile.apply" not in schemas["AgentTaskRequest"]["properties"]["kind"]["enum"]
assert "agent.profile.apply" in schemas["AgentTask"]["properties"]["kind"]["enum"]

for schema_name in (
    "AgentProfile", "AgentProfileApplyRequest", "AgentProfileObservation",
    "AgentProfilePutRequest", "AgentProfileDesiredResponse", "AgentProfileObservedResponse",
    "AgentProfileApplyResponse", "AgentProfileResponse",
):
    assert schema_name in schemas

forbidden = {"secret", "secrets", "token", "password", "command", "commands", "environment", "env", "arbitrary", "task", "tasks"}
for schema_name in (
    "AgentProfile", "AgentProfileApplyRequest", "AgentProfileObservation",
    "AgentProfilePutRequest", "AgentProfileDesiredResponse", "AgentProfileObservedResponse",
    "AgentProfileApplyResponse", "AgentProfileResponse",
):
    for key in schemas[schema_name].get("properties", {}):
        assert key.lower() not in forbidden, (schema_name, key)

reference = api_reference.lower()
for heading in (
    "### get /api/nodes/{id}/profile",
    "### post /api/nodes/{id}/profile/apply",
    "### put /api/nodes/{id}/profile",
):
    assert heading in reference, heading
for phrase in (
    "routeadmin", "nodeid", "desired.profile: null", "raw capability array",
    "not_reported", "not_configured", "pending_restart", "awaiting_heartbeat",
    "manual_required", "self_apply_not_supported", "deploywriteroots",
    "at most 16 unique", "expectedrevision", "confirmed", "stale_profile_revision",
    "429 too many requests", "completed `agent.profile.apply` task",
    "`desired.revision`", "no agent task", "environment-file content",
):
    assert phrase in reference, phrase

print("agent profile OpenAPI contract checks passed")
PY
