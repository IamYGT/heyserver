#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
mkdir -p "$tmp/internal/api" "$tmp/docs"
cp "$repo_root/internal/api/routes_manifest.go" "$tmp/internal/api/routes_manifest.go"
cp "$repo_root/docs/api-routes.md" "$tmp/docs/api-routes.md"
cp "$repo_root/docs/openapi.json" "$tmp/docs/openapi.json"

if ! cmp -s "$repo_root/docs/openapi.json" "$repo_root/cmd/hserver/web/dist/openapi.json"; then
  printf '%s\n' "embedded OpenAPI contract differs from docs/openapi.json" >&2
  exit 1
fi
grep -q '^COPY docs/openapi.json /app/docs/openapi.json$' "$repo_root/Dockerfile"
grep -q '^docs/\*$' "$repo_root/.dockerignore"
grep -q '^!docs/openapi.json$' "$repo_root/.dockerignore"

HSERVER_ROOT=$tmp go run "$repo_root/scripts/gen-api-routes/main.go" -check >"$tmp/check.log"
grep -q 'API route inventory is current' "$tmp/check.log"

printf '\n' >>"$tmp/docs/openapi.json"
if HSERVER_ROOT=$tmp go run "$repo_root/scripts/gen-api-routes/main.go" -check >"$tmp/stale.log" 2>&1; then
  printf '%s\n' "stale OpenAPI document unexpectedly passed" >&2
  exit 1
fi
grep -q 'docs/openapi.json is stale' "$tmp/stale.log"

HSERVER_ROOT=$tmp go run "$repo_root/scripts/gen-api-routes/main.go" >"$tmp/generate.log"
HSERVER_ROOT=$tmp go run "$repo_root/scripts/gen-api-routes/main.go" -check >/dev/null
expected_routes=$(grep -Ec '^\s*\{"[A-Z]+", "/' "$tmp/internal/api/routes_manifest.go")
grep -q '"openapi": "3.1.0"' "$tmp/docs/openapi.json"
grep -q '"x-hserver-contract-version": 71' "$tmp/docs/openapi.json"
grep -q "\"x-hserver-route-count\": $expected_routes" "$tmp/docs/openapi.json"
grep -q '"x-hserver-schema-count": 321' "$tmp/docs/openapi.json"
grep -q '"panelBearer"' "$tmp/docs/openapi.json"
grep -q '"agentBearer"' "$tmp/docs/openapi.json"
grep -q '"cronSecret"' "$tmp/docs/openapi.json"
grep -q '"x-hserver-access": "Admin"' "$tmp/docs/openapi.json"
grep -q '"x-hserver-loopback-only": true' "$tmp/docs/openapi.json"
python3 - "$tmp/docs/openapi.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    document = json.load(handle)

schemas = document["components"]["schemas"]
webhook = document["paths"]["/api/deploy/webhook/{targetId}"]["post"]
assert "GitHub or GitLab" in webhook["description"]
webhook_headers = {item["name"] for item in webhook["parameters"] if item["in"] == "header"}
assert {
    "X-GitHub-Event", "X-GitHub-Delivery", "X-Hub-Signature-256",
    "X-Gitlab-Event", "webhook-id", "webhook-timestamp", "webhook-signature",
} <= webhook_headers
assert {"200", "202", "400", "401", "403", "409", "413", "503"} <= set(webhook["responses"])

ensure = document["paths"]["/api/nodes/{node_id}/deploy/{target_id}/domains/{domain}"]["put"]
assert ensure["x-hserver-access"] == "Admin"
assert ensure["security"] == [{"panelBearer": []}, {"panelSession": []}]
assert ensure["parameters"] == [
    {"in": "path", "name": "node_id", "required": True, "schema": {"type": "string"}},
    {"in": "path", "name": "target_id", "required": True, "schema": {"type": "string"}},
    {"in": "path", "name": "domain", "required": True, "schema": {"type": "string"}},
]
assert ensure["requestBody"]["required"] is True
assert ensure["requestBody"]["content"]["application/json"]["schema"] == {
    "$ref": "#/components/schemas/EnsureRemoteDeployDomainRequest"
}
assert set(ensure["responses"]) == {"200", "400", "409", "422", "502", "504", "default"}
assert ensure["responses"]["200"]["content"]["application/json"]["schema"] == {
    "$ref": "#/components/schemas/EnsureRemoteDeployDomainResponse"
}
for status in ("400", "409", "422", "502", "504", "default"):
    assert ensure["responses"][status]["content"]["application/json"]["schema"] == {
        "$ref": "#/components/schemas/ErrorResponse"
    }
ensure_description = ensure["description"].lower()
for phrase in (
    "revision-aware",
    "compare-and-swap",
    "idempotent no-op",
    "nginx content",
    "upstream urls",
    "shell commands",
):
    assert phrase in ensure_description, phrase
for status, phrase in {
    "409": "conflict",
    "422": "invalid",
    "502": "invalid",
    "504": "75-second",
}.items():
    assert phrase in ensure["responses"][status]["description"].lower(), (status, phrase)

ensure_request = schemas["EnsureRemoteDeployDomainRequest"]
assert ensure_request["type"] == "object"
assert ensure_request["additionalProperties"] is False
assert ensure_request["required"] == ["expected_revision", "confirmed"]
assert set(ensure_request["properties"]) == {"expected_revision", "confirmed"}
assert ensure_request["properties"]["expected_revision"] == {
    "type": "string",
    "pattern": "^(?:absent|[0-9a-f]{64})$",
    "description": "Current lowercase SHA-256 observation revision, or absent when the mapping is expected not to exist.",
}
assert ensure_request["properties"]["confirmed"] == {
    "type": "boolean",
    "const": True,
    "description": "Must be true to authorize this managed mutation.",
}

ensure_response = schemas["EnsureRemoteDeployDomainResponse"]
assert ensure_response["type"] == "object"
assert ensure_response["additionalProperties"] is False
assert ensure_response["required"] == ["changed", "observation"]
assert set(ensure_response["properties"]) == {"changed", "observation"}
assert ensure_response["properties"]["changed"]["type"] == "boolean"
assert ensure_response["properties"]["observation"] == {"$ref": "#/components/schemas/RemoteDeployDomain"}

managed_domain = schemas["RemoteDeployDomain"]
assert managed_domain["type"] == "object"
assert managed_domain["additionalProperties"] is False
assert managed_domain["required"] == [
    "target_id", "domain", "host_port", "desired_host_port", "upstream", "status",
    "message", "tls_status", "tls_message", "enabled", "revision",
]
assert set(managed_domain["properties"]) == set(managed_domain["required"]) | {
    "tls_expires_at", "tls_days_remaining", "updated_at",
}
assert managed_domain["properties"]["upstream"]["pattern"] == r"^http://127\.0\.0\.1:[1-9][0-9]{0,4}$"
assert managed_domain["properties"]["status"] == {"type": "string", "const": "active"}
assert managed_domain["properties"]["enabled"] == {"type": "boolean", "const": True}
assert managed_domain["properties"]["revision"] == {"type": "string", "pattern": r"^[0-9a-f]{64}$"}
assert "arbitrary upstream" in managed_domain["properties"]["upstream"]["description"].lower()

required_schemas = {
    "ActionMessage",
	"AuthLoginRequest",
	"AuthLoginResponse",
	"AuthLoginResult",
	"AuthLogoutResponse",
	"AuthRecoveryRequest",
	"AuthTOTPCodeRequest",
	"AuthTOTPDisabledResponse",
	"AuthTOTPEnabledResponse",
	"AuthTOTPLoginRequest",
	"AuthTOTPRequiredResponse",
	"AuthTOTPSetup",
	"AuthTOTPStatus",
	"AlertHistory",
	"AlertHistoryPage",
	"AlertRule",
	"AlertRuleCreateRequest",
	"AlertRuleUpdateRequest",
    "CloudflareEmailRouting",
    "CloudflareMailDNSReconcileResult",
    "CloudflareMailDNSRecordAction",
    "CloudflarePlan",
    "CloudflareProxyRequest",
    "CloudflarePurgeReceipt",
    "CloudflareRecord",
    "CloudflareRecordMutationRequest",
    "CloudflareZone",
	"ComposeService",
	"ComposeServiceActionResult",
	"ComposeServiceList",
	"ComposeServiceLogs",
    "AgentHeartbeatRequest",
    "AgentHeartbeatResponse",
    "AgentTask",
    "AgentTaskList",
    "AgentTaskPollResponse",
    "AgentTaskRequest",
    "AgentTaskResultRequest",
    "BackupArtifact",
    "BackupCreateRequest",
    "BackupListResponse",
    "BackupSchedule",
    "BackupScheduleDeleteRequest",
    "BackupScheduleListResponse",
    "BackupScheduleMutationResult",
    "BackupScheduleSetRequest",
    "BackupStorageSummary",
	"BindAddRecordRequest",
	"BindCheckResult",
	"BindCreateZoneRequest",
	"BindDeleteRecordRequest",
	"BindLookupQuery",
	"BindLookupResponse",
	"BindLookupResult",
	"BindRecord",
	"BindSOA",
	"BindSOAUpdateRequest",
	"BindServiceStatus",
	"BindUpdateRecordRequest",
	"BindZone",
	"BindZoneCheckResult",
	"BindZoneDetail",
    "CronJobCreateRequest",
    "CronJob",
    "CronJobDeleteResult",
    "CronJobListResponse",
    "CronJobMutationResult",
    "CronJobUpdateRequest",
    "CronServiceStatus",
    "CronSystemFile",
    "CronSystemFileListResponse",
    "AsyncJobAccepted",
    "RestoreValidation",
    "ErrorResponse",
    "FirewallRuleCreateRequest",
    "FirewallRule",
    "FirewallRuleDeleteResult",
    "FirewallRuleListResponse",
    "FirewallStatus",
    "FirewallToggleResult",
    "FirewallToggleRequest",
	"FileCreateRequest",
	"FileCreateResponse",
	"FileDeleteResponse",
	"FileEntry",
	"FileListResponse",
	"FileReadResponse",
	"FileRenameRequest",
	"FileRenameResponse",
	"FileWriteRequest",
	"FileWriteResponse",
	"GDriveConnectionTestResult",
	"GDriveMutationResult",
	"GDriveOAuthAppUpdateRequest",
	"GDriveOAuthCompleteRequest",
	"GDriveOAuthStartResponse",
	"GDriveRestoreAccepted",
	"GDriveRestoreRequest",
	"GDriveSettingsUpdateRequest",
    "HostActionStatus",
	"HealthStatus",
	"MailAccount",
	"MailAlias",
	"MailServiceListener",
	"MailServiceOverview",
	"MailServiceOverviewSources",
	"MailServiceSource",
	"MailServiceStatus",
	"MailServiceStatusSource",
	"MailServiceStorage",
	"MailServiceVersion",
	"NotificationChannel",
	"OnboardingSaveResult",
	"OnboardingSetRequest",
	"OnboardingState",
	"PortableSettingsBundle",
	"PortableSettingsBundleRequest",
	"PortableSettingsChange",
	"PortableSettingsImportRequest",
	"PortableSettingsPreview",
	"SettingValueResponse",
	"SettingsDeleteResult",
	"SettingsSaveResult",
	"SettingsUpdateRequest",
	"SettingsValues",
	"ReleaseArtifact",
	"ReleaseUpdateInstallRequest",
	"ReleaseUpdateStage",
	"ReleaseUpdateStageResponse",
	"ReleaseUpdateStatus",
	"SystemCPUStats",
	"SystemDiskStats",
	"SystemInfo",
	"SystemMemoryStats",
	"SystemNetworkInterface",
	"SystemNetworkStats",
	"SystemServiceLogEntry",
	"SystemServiceLogsResponse",
	"SystemServiceStatus",
	"SystemStats",
    "DiskCleanupExecuteRequest",
	"DomainCheckRequest",
	"DomainCheckResult",
	"DomainCreateRequest",
	"DomainCreateResult",
	"DomainDeleteResult",
	"DomainDNSCapability",
	"DomainDNSRecordAction",
	"DomainDNSResult",
	"DomainProvisioningCapabilities",
    "DomainToggleRequest",
	"DomainToggleResult",
	"LocalDomain",
	"LocalDomainDetail",
	"LocalDomainList",
	"DeleteStatus",
    "DiskCleanupResult",
    "DiskCleanupTarget",
	"DeployRevisionComparison",
	"CreateDeployTargetRequest",
	"CreateDeployStagingRequest",
	"DeployEnvironment",
	"DeployEnvironmentSetRequest",
	"DeployEnvironmentVariable",
	"CreateDeployDomainRequest",
	"DeployDomain",
	"DeployDomainHealth",
	"DeployDomainList",
	"EnableDeployDomainTLSRequest",
	"EnsureRemoteDeployDomainRequest",
	"EnsureRemoteDeployDomainResponse",
	"DeployPreflight",
	"DeployPreflightCheck",
	"DeployQueueResult",
	"DeployRun",
	"DeployRunList",
	"DeployRunLogs",
	"DeployStagingReceipt",
	"DeployTarget",
	"DeployTargetList",
	"DeployTemplate",
	"DeployTemplateInventory",
	"DeployTemplateIssue",
	"UpdateDeployTargetRequest",
    "Database",
    "DatabaseCreateRequest",
    "DatabaseCreateResult",
    "DatabaseDropRequest",
    "DatabaseDropResult",
    "DatabaseInventory",
    "DatabaseQueryRequest",
    "DatabaseQueryResponse",
    "DatabaseQueryResult",
    "DatabaseSourceStatus",
    "DatabaseSources",
    "DatabaseTable",
    "DatabaseTableInventory",
    "DatabaseUser",
    "DatabaseUserInventory",
    "PGMBackup",
    "PGMBackupFileList",
    "PGMBackupList",
    "PGMCredential",
    "PGMCredentialList",
    "PGMRestoreRequest",
    "PGMRestoreResult",
    "DockerImagePullRequest",
    "DockerContainer",
    "DockerContainerActionResult",
    "DockerContainerList",
    "DockerContainerLogs",
    "DockerImage",
    "DockerImageList",
    "DockerImageMutationResult",
    "DockerStatus",
    "LocalDiskCleanupExecution",
    "LocalDiskCleanupTarget",
    "ManagedDiskCleanupExecuteRequest",
    "ManagedDiskCleanupExecution",
    "ManagedNode",
    "ManagedNodeCompatibility",
    "ManagedNodeDiskMount",
    "ManagedNodeInventory",
    "ManagedNodeList",
    "ManagedNodeProcess",
    "ManagedNodeRecord",
    "ManagedNodeRegisterRequest",
    "ManagedNodeMetrics",
    "ManagedNodeMetricsCPU",
    "ManagedNodeMetricsFilesystem",
    "ManagedNodeMetricsLoad",
    "ManagedNodeMetricsMemory",
    "ManagedNodeMetricsNetwork",
	"ManagedNodeRegistration",
	"ManagedNodeServiceState",
	"RemoteDeployDomain",
	"AgentProfile",
	"AgentProfileApplyRequest",
	"AgentProfileApplyResponse",
	"AgentProfileDesiredResponse",
	"AgentProfileObservation",
	"AgentProfileObservedResponse",
	"AgentProfilePutRequest",
	"AgentProfileResponse",
	"NginxConfigArchiveReceipt",
	"NginxConfigArchiveRequest",
	"NginxConfigArchive",
	"NginxConfigArchiveList",
	"NginxConfigArchiveRestoreReceipt",
	"NginxConfigBackup",
	"NginxConfigBackupList",
	"NginxConfigBackupRestoreRequest",
	"NginxConfigBackupRestoreReceipt",
	"NginxConfigContent",
	"NginxConfigCreateRequest",
	"NginxConfigReplaceReceipt",
	"NginxConfigReplaceRequest",
	"NginxConfigStateReceipt",
	"NginxConfigStateRequest",
    "PM2DeployRequest",
    "PM2ControlResult",
    "PM2DeployResult",
    "PM2LogsResponse",
    "PM2Process",
    "PM2ProcessList",
    "PM2SaveResult",
    "PHPPoolConfigContent",
    "PHPPoolConfigReplaceReceipt",
    "PHPPoolConfigReplaceRequest",
    "ProcessSignalRequest",
    "RebootStatus",
    "ServiceControlRequest",
    "SnapshotPurgeRepositoryRequest",
	"ResticSnapshot",
	"SnapshotListResponse",
	"SnapshotManifestEntry",
	"SnapshotRepoStats",
    "SnapshotRestoreAccepted",
    "SnapshotRestoreRequest",
    "SnapshotRunAccepted",
    "SnapshotSettings",
    "SnapshotSettingsMutationResult",
    "SnapshotSettingsUpdateRequest",
	"SnapshotStatus",
	"IntegrationAgent",
	"IntegrationAgentEvidence",
	"IntegrationCatalog",
	"IntegrationCatalogDocumentation",
	"IntegrationCatalogEntry",
	"IntegrationConfiguration",
	"IntegrationEvidence",
	"IntegrationEvidenceItem",
	"IntegrationRawStateMapping",
	"IntegrationStatus",
	"IntegrationStatusResponse",
	"IntegrationStatusResult",
	"IntegrationStatusTarget",
	"ManagedIntegrationStatusResponse",
	"ManagedIntegrationStatusResult",
	"ManagedIntegrationStatusTarget",
	"ManagedContainer",
	"ManagedContainerList",
	"ManagedPHPFPMPool",
	"ManagedPHPFPMVersion",
	"ManagedPHPFPMVersionList",
	"ManagedPM2Process",
	"ManagedPM2ProcessList",
	"UptimeMonitor",
	"UptimeMonitorCreateRequest",
	"UptimeMonitorUpdateRequest",
	"UptimeSettings",
	"UptimeSettingsUpdateRequest",
	"UptimeStatusPage",
	"UptimeStatusPageCreateRequest",
	"UptimeStatusPageMonitor",
	"UptimeStatusPageMonitorRequest",
	"UptimeStatusPageUpdateRequest",
	"User",
	"UserCreateRequest",
	"UserListResponse",
	"UserUpdateRequest",
}
if set(schemas) != required_schemas:
    raise SystemExit(f"unexpected promoted schemas: {sorted(schemas)}")

def verify_refs(value):
    if isinstance(value, dict):
        ref = value.get("$ref")
        if ref is not None:
            prefix = "#/components/schemas/"
            if not isinstance(ref, str) or not ref.startswith(prefix) or ref[len(prefix):] not in schemas:
                raise SystemExit(f"unresolved local schema reference: {ref}")
        for nested in value.values():
            verify_refs(nested)
    elif isinstance(value, list):
        for nested in value:
            verify_refs(nested)

verify_refs(document)

for path, schema_name in (("/api/mail/accounts", "MailAccount"), ("/api/mail/aliases", "MailAlias")):
    operation = document["paths"][path]["get"]
    assert operation["x-hserver-access"] == "Authenticated"
    assert operation["security"] == [{"panelBearer": []}, {"panelSession": []}]
    assert operation["parameters"] == [{
        "name": "domain",
        "in": "query",
        "required": False,
        "description": "Optional exact mail domain filter.",
        "schema": {"type": "string", "maxLength": 253},
    }]
    assert set(operation["responses"]) == {"200", "502", "503", "default"}
    for status in ("200", "502", "503", "default"):
        response = operation["responses"][status]
        response_schema = response["content"]["application/json"]["schema"]
        if status == "200":
            assert response_schema["type"] == "array"
            assert response_schema["items"].get("$ref") == f"#/components/schemas/{schema_name}"
        else:
            assert response_schema.get("$ref") == "#/components/schemas/ErrorResponse"
    schema = schemas[schema_name]
    assert schema["type"] == "object"
    assert schema["additionalProperties"] is False
    assert not {"password", "secret", "secrets", "hash", "body"} & set(schema["properties"])

integration_status = document["paths"]["/api/integrations/status"]["get"]
integration_status_description = integration_status.get("description", "").lower()
for phrase in (
    "every local integration entry",
    "fifteen required core probes",
    "reviewed additive catalog entries",
    "unprobed",
):
    if phrase not in integration_status_description:
        raise SystemExit(f"integration status description omits {phrase}")
if integration_status["x-hserver-access"] != "Authenticated":
    raise SystemExit("integration status authentication boundary is missing")
if set(integration_status["responses"]) != {"200", "401", "500"}:
    raise SystemExit("integration status response boundary is incomplete")
status_responses = integration_status["responses"]
for response_code, schema_name in (("200", "IntegrationStatusResponse"), ("401", "ErrorResponse"), ("500", "ErrorResponse")):
    if status_responses[response_code]["content"]["application/json"]["schema"].get("$ref") != f"#/components/schemas/{schema_name}":
        raise SystemExit(f"integration status {response_code} response schema is missing")
status_response = schemas["IntegrationStatusResponse"]
if status_response["properties"]["schema_version"].get("const") != 1:
    raise SystemExit("integration status schema version is not fixed to v1")
if status_response["properties"]["observed_at"].get("format") != "date-time":
    raise SystemExit("integration status observed_at is not RFC3339 date-time")
if status_response["properties"]["target"].get("$ref") != "#/components/schemas/IntegrationStatusTarget":
    raise SystemExit("integration status target schema is missing")
if status_response["properties"]["results"]["items"].get("$ref") != "#/components/schemas/IntegrationStatusResult":
    raise SystemExit("integration status result schema is missing")
if "maxItems" in status_response["properties"]["results"]:
    raise SystemExit("integration status local result cardinality must follow the catalog")
if "additive" not in status_response["properties"]["results"].get("description", "").lower():
    raise SystemExit("integration status result schema omits additive catalog support")
if "maxItems" in status_response["properties"]["unprobed"]:
    raise SystemExit("integration status unprobed cardinality must follow the catalog")
if status_response["properties"]["unprobed"].get("uniqueItems") is not True:
    raise SystemExit("integration status unprobed IDs are not unique")
if "additive" not in status_response["properties"]["unprobed"].get("description", "").lower():
    raise SystemExit("integration status unprobed schema omits additive entries")
if "enum" in status_response["properties"]["unprobed"].get("items", {}):
    raise SystemExit("integration status empty unprobed item schema has a stale enum")
if status_response["properties"]["partial"].get("type") != "boolean":
    raise SystemExit("integration status partial field is not boolean")
if schemas["IntegrationStatusTarget"]["properties"]["scope"].get("const") != "local_host":
    raise SystemExit("integration status target scope is not local_host")
if set(schemas["IntegrationStatusResult"]["properties"]["state"].get("enum", [])) != {"not_configured", "unavailable", "healthy"}:
    raise SystemExit("integration status canonical states are incomplete")
status_result = schemas["IntegrationStatusResult"]
if status_result["properties"]["id"].get("pattern") != r"^[a-z0-9]+(?:[._-][a-z0-9]+)*$":
    raise SystemExit("integration status result IDs do not use catalog syntax")
if "enum" in status_result["properties"]["id"]:
    raise SystemExit("integration status result IDs use a stale static enum")
if "current catalog" not in status_result["properties"]["id"].get("description", "").lower():
    raise SystemExit("integration status result IDs omit catalog membership boundary")
if status_result["properties"]["probe"].get("pattern") != r"^[a-z][a-z0-9_]*$":
    raise SystemExit("integration status probe identifiers are not safely bounded")
if "enum" in status_result["properties"]["probe"]:
    raise SystemExit("integration status probe identifiers use a stale static enum")
if "additive" not in status_result["properties"]["probe"].get("description", "").lower():
    raise SystemExit("integration status probe schema omits additive support")

managed_integration_status = document["paths"]["/api/nodes/{id}/integrations/status"]["get"]
if managed_integration_status["x-hserver-access"] != "Admin":
    raise SystemExit("managed integration status administrator boundary is missing")
if managed_integration_status["security"] != [{"panelBearer": []}, {"panelSession": []}]:
    raise SystemExit("managed integration status authentication boundary is incomplete")
if set(managed_integration_status["responses"]) != {"200", "404", "409", "502", "504", "default"}:
    raise SystemExit("managed integration status response boundary is incomplete")
managed_status_responses = managed_integration_status["responses"]
for response_code, schema_name in (("200", "ManagedIntegrationStatusResponse"), ("404", "ErrorResponse"), ("409", "ErrorResponse"), ("502", "ErrorResponse"), ("504", "ErrorResponse"), ("default", "ErrorResponse")):
    if managed_status_responses[response_code]["content"]["application/json"]["schema"].get("$ref") != f"#/components/schemas/{schema_name}":
        raise SystemExit(f"managed integration status {response_code} response schema is missing")
if "no task is created" not in managed_status_responses["409"]["description"].lower():
    raise SystemExit("managed integration status capability/offline no-task boundary is missing")
if "managed_status_failed" not in managed_status_responses["502"]["description"]:
    raise SystemExit("managed integration status safe 502 boundary is missing")
if "managed_status_timeout" not in managed_status_responses["504"]["description"]:
    raise SystemExit("managed integration status bounded 504 boundary is missing")
managed_status_response = schemas["ManagedIntegrationStatusResponse"]
if managed_status_response["additionalProperties"] is not False:
    raise SystemExit("managed integration status response is not strict")
if managed_status_response["required"] != ["schema_version", "observed_at", "target", "results", "partial"]:
    raise SystemExit("managed integration status response fields are incomplete")
if managed_status_response["properties"]["schema_version"].get("const") != 1:
    raise SystemExit("managed integration status schema version is not fixed to v1")
if managed_status_response["properties"]["observed_at"].get("format") != "date-time":
    raise SystemExit("managed integration status observed_at is not RFC3339 date-time")
if managed_status_response["properties"]["results"].get("minItems") != 2 or managed_status_response["properties"]["results"].get("maxItems") != 2:
    raise SystemExit("managed integration status result cardinality is not exact")
if managed_status_response["properties"]["target"].get("$ref") != "#/components/schemas/ManagedIntegrationStatusTarget":
    raise SystemExit("managed integration status target schema is missing")
if managed_status_response["properties"]["results"]["items"].get("$ref") != "#/components/schemas/ManagedIntegrationStatusResult":
    raise SystemExit("managed integration status result schema is missing")
managed_status_target = schemas["ManagedIntegrationStatusTarget"]
if managed_status_target["additionalProperties"] is not False or managed_status_target["required"] != ["scope", "node_id"]:
    raise SystemExit("managed integration status target is not strict")
if managed_status_target["properties"]["scope"].get("const") != "managed_node":
    raise SystemExit("managed integration status target scope is not managed_node")
managed_status_result = schemas["ManagedIntegrationStatusResult"]
if managed_status_result["additionalProperties"] is not False:
    raise SystemExit("managed integration status result is not strict")
if managed_status_result["properties"]["id"].get("enum") != ["process.pm2", "container.docker"]:
    raise SystemExit("managed integration status result IDs are incomplete")
if managed_status_result["properties"]["probe"].get("enum") != ["pm2_inventory", "docker_info"]:
    raise SystemExit("managed integration status probes are incomplete")
if set(managed_status_result["properties"]["state"].get("enum", [])) != {"not_configured", "unavailable", "healthy"}:
    raise SystemExit("managed integration status canonical states are incomplete")
if set(managed_status_result["properties"]["error_code"].get("enum", [])) != {"not_configured", "probe_failed", "timeout"}:
    raise SystemExit("managed integration status safe error codes are incomplete")
if managed_status_result["properties"]["duration_ms"].get("minimum") != 0:
    raise SystemExit("managed integration status duration bound is missing")
if "integration.status" not in schemas["AgentHeartbeatRequest"]["properties"]["capabilities"]["items"]["enum"]:
    raise SystemExit("managed integration status capability is missing from heartbeat schema")

profile_get = document["paths"]["/api/nodes/{id}/profile"]["get"]
profile_put = document["paths"]["/api/nodes/{id}/profile"]["put"]
profile_apply = document["paths"]["/api/nodes/{id}/profile/apply"]["post"]
for profile_operation in (profile_get, profile_put, profile_apply):
    if profile_operation["x-hserver-access"] != "Admin":
        raise SystemExit("managed profile administrator boundary is missing")
    if profile_operation["security"] != [{"panelBearer": []}, {"panelSession": []}]:
        raise SystemExit("managed profile authentication boundary is incomplete")
    if profile_operation["parameters"] != [{
        "in": "path", "name": "id", "required": True, "schema": {"type": "string"},
    }]:
        raise SystemExit("managed profile path parameter is incomplete")
if set(profile_get["responses"]) != {"200", "404", "default"}:
    raise SystemExit("managed profile GET response boundary is incomplete")
if set(profile_put["responses"]) != {"200", "400", "404", "409", "429", "default"}:
    raise SystemExit("managed profile PUT response boundary is incomplete")
if set(profile_apply["responses"]) != {"200", "202", "400", "404", "409", "429", "default"}:
    raise SystemExit("managed profile apply response boundary is incomplete")
if profile_put["requestBody"]["required"] is not True or profile_put["requestBody"]["content"]["application/json"]["schema"].get("$ref") != "#/components/schemas/AgentProfilePutRequest":
    raise SystemExit("managed profile PUT request schema is missing")
if profile_apply["requestBody"]["required"] is not True or profile_apply["requestBody"]["content"]["application/json"]["schema"].get("$ref") != "#/components/schemas/AgentProfileApplyRequest":
    raise SystemExit("managed profile apply request schema is missing")
for operation, expected_statuses in (
    (profile_get, ("200", "404", "default")),
    (profile_put, ("200", "400", "404", "409", "429", "default")),
    (profile_apply, ("200", "202", "400", "404", "409", "429", "default")),
):
    for status in expected_statuses:
        expected_schema = "AgentProfileResponse" if status in {"200", "202"} else "ErrorResponse"
        if operation["responses"][status]["content"]["application/json"]["schema"].get("$ref") != f"#/components/schemas/{expected_schema}":
            raise SystemExit(f"managed profile {status} response schema is missing")
profile = schemas["AgentProfile"]
if profile["additionalProperties"] is not False or profile["required"] != [
    "allowDeployRead", "allowDeployActions", "allowDeployDomainRead",
    "allowDeployDomainActions", "deployPlansFile", "deployAcmeWebroot", "deployWriteRoots",
]:
    raise SystemExit("managed profile fixed field contract is incomplete")
roots = profile["properties"]["deployWriteRoots"]
if roots.get("type") != "array" or roots.get("maxItems") != 16 or roots.get("uniqueItems") is not True:
    raise SystemExit("managed profile write-root array contract is incomplete")
if roots["items"].get("type") != "string" or roots["items"].get("minLength") != 1:
    raise SystemExit("managed profile write-root item contract is incomplete")
desired_profile = schemas["AgentProfileDesiredResponse"]
if desired_profile["properties"]["revision"].get("minimum") != 0:
    raise SystemExit("managed profile desired revision is not non-negative")
if desired_profile["properties"]["profile"].get("oneOf") != [
    {"$ref": "#/components/schemas/AgentProfile"}, {"type": "null"},
]:
    raise SystemExit("managed profile desired profile is not nullable")
observed_profile = schemas["AgentProfileObservedResponse"]
if observed_profile.get("required") != ["capabilities", "online", "lastSeenAt", "agentVersion", "protocolVersion", "profileState", "profileRevision", "profileErrorCode"]:
    raise SystemExit("managed profile observed response fields are incomplete")
if observed_profile["properties"]["capabilities"].get("type") != ["array", "null"] or "enum" in observed_profile["properties"]["capabilities"]["items"]:
    raise SystemExit("managed profile capabilities are not a raw array")
if set(observed_profile["properties"]["profileState"].get("enum", [])) != {"not_reported", "not_configured", "pending_restart", "applied", "failed"}:
    raise SystemExit("managed profile observed states are incomplete")
if observed_profile["properties"]["profileRevision"].get("type") != ["integer", "null"] or observed_profile["properties"]["profileRevision"].get("minimum") != 0:
    raise SystemExit("managed profile observed revision nullability or bound is incomplete")
if observed_profile["properties"]["profileErrorCode"].get("type") != ["string", "null"]:
    raise SystemExit("managed profile observed error nullability is incomplete")
apply_schema = schemas["AgentProfileApplyResponse"]
if set(apply_schema["properties"]["state"].get("enum", [])) != {"manual_required", "not_requested", "queued", "running", "awaiting_heartbeat", "applied", "failed", "drifted"}:
    raise SystemExit("managed profile apply states are incomplete")
if not {"self_apply_not_supported", "profile_revision_drift", ""} <= set(apply_schema["properties"]["reason"].get("enum", [])):
    raise SystemExit("managed profile apply safe reasons are incomplete")
for field in ("desiredRevision", "taskId", "observedRevision", "observedState"):
    if field not in apply_schema["properties"] or "null" not in apply_schema["properties"][field].get("type", []):
        raise SystemExit(f"managed profile apply nullable field is missing: {field}")
apply_request = schemas["AgentProfileApplyRequest"]
if apply_request.get("additionalProperties") is not False or apply_request.get("required") != ["expectedRevision", "confirmed"]:
    raise SystemExit("managed profile apply request is not strict")
if apply_request["properties"]["expectedRevision"].get("minimum") != 1 or apply_request["properties"]["confirmed"].get("const") is not True:
    raise SystemExit("managed profile apply request bounds are incomplete")
observation = schemas["AgentProfileObservation"]
if observation.get("additionalProperties") is not False or set(observation["properties"]["state"].get("enum", [])) != {"not_configured", "pending_restart", "applied", "failed"}:
    raise SystemExit("managed profile heartbeat observation schema is incomplete")
if schemas["AgentHeartbeatRequest"]["properties"]["profile"].get("oneOf", [{}])[0].get("$ref") != "#/components/schemas/AgentProfileObservation":
    raise SystemExit("managed profile heartbeat observation reference is missing")
if "agent.profile.apply" not in schemas["AgentHeartbeatRequest"]["properties"]["capabilities"]["items"]["enum"]:
    raise SystemExit("managed profile apply capability is missing from heartbeat schema")
if "agent.profile.apply" in schemas["AgentTaskRequest"]["properties"]["kind"]["enum"]:
    raise SystemExit("dedicated profile apply task is exposed on generic task creation")
if "agent.profile.apply" not in schemas["AgentTask"]["properties"]["kind"]["enum"]:
    raise SystemExit("managed profile apply task is missing from task history schema")

health = document["paths"]["/api/health"]["get"]
if health["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/HealthStatus":
    raise SystemExit("health response schema is missing")
if health["responses"]["503"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ErrorResponse":
    raise SystemExit("health uninitialized boundary is missing")
if schemas["HealthStatus"]["properties"]["status"].get("const") != "ok":
    raise SystemExit("health success state is not exact")

onboarding_get = document["paths"]["/api/onboarding"]["get"]
onboarding_set = document["paths"]["/api/onboarding"]["post"]
if onboarding_get["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/OnboardingState":
    raise SystemExit("onboarding state response schema is missing")
if onboarding_set["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/OnboardingSetRequest":
    raise SystemExit("onboarding request schema is missing")
if set(schemas["OnboardingSetRequest"]["required"]) != {"completed", "step"}:
    raise SystemExit("onboarding complete-state fields are not required")
if schemas["OnboardingSetRequest"]["properties"]["step"].get("maximum") != 5:
    raise SystemExit("onboarding step bound is missing")
if "413" not in onboarding_set["responses"] or onboarding_set["x-hserver-access"] != "Admin":
    raise SystemExit("onboarding body-size or administrator boundary is missing")

login = document["paths"]["/api/auth/login"]["post"]
if login["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AuthLoginRequest":
    raise SystemExit("login request schema is missing")
if login["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AuthLoginResult":
    raise SystemExit("login result union is missing")
if login["security"] != [] or not {"400", "401", "413", "429"}.issubset(login["responses"]):
    raise SystemExit("login public or failure boundaries are incomplete")
login_options = {item["$ref"] for item in schemas["AuthLoginResult"]["oneOf"]}
if login_options != {"#/components/schemas/AuthLoginResponse", "#/components/schemas/AuthTOTPRequiredResponse"}:
    raise SystemExit("login result alternatives are incomplete")
if schemas["AuthLoginRequest"]["properties"]["password"].get("writeOnly") is not True:
    raise SystemExit("login password is not write-only")

totp_login = document["paths"]["/api/auth/totp-verify"]["post"]
if totp_login["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AuthTOTPLoginRequest":
    raise SystemExit("TOTP login request schema is missing")
if schemas["AuthTOTPLoginRequest"]["properties"]["code"].get("pattern") != "^[0-9]{6}$":
    raise SystemExit("TOTP login code is not exactly six digits")

recovery = document["paths"]["/api/auth/2fa/recovery"]["post"]
if recovery["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AuthRecoveryRequest":
    raise SystemExit("2FA recovery request schema is missing")
recovery_properties = schemas["AuthRecoveryRequest"]["properties"]
if "recovery_code" not in recovery_properties or "code" in recovery_properties:
    raise SystemExit("2FA recovery field does not match the frontend contract")
if set(schemas["AuthRecoveryRequest"]["required"]) != {"email", "password", "recovery_code"}:
    raise SystemExit("2FA recovery identity fields are incomplete")

logout = document["paths"]["/api/auth/logout"]["post"]
if "requestBody" in logout or logout["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AuthLogoutResponse":
    raise SystemExit("logout empty-body or response contract is invalid")

current_user = document["paths"]["/api/auth/me"]["get"]
if current_user["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/User":
    raise SystemExit("current-user response schema is missing")

totp_status = document["paths"]["/api/auth/2fa/status"]["get"]
if totp_status["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AuthTOTPStatus":
    raise SystemExit("2FA status response schema is missing")
if set(schemas["AuthTOTPStatus"]["required"]) != {"enabled", "setup_pending"}:
    raise SystemExit("2FA status fields are incomplete")

totp_setup = document["paths"]["/api/auth/2fa/setup"]["post"]
if "requestBody" in totp_setup or totp_setup["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AuthTOTPSetup":
    raise SystemExit("2FA setup empty-body or response contract is invalid")
if schemas["AuthTOTPSetup"]["properties"]["recoveryCodes"].get("minItems") != 8:
    raise SystemExit("2FA recovery-code inventory is not fixed to eight")

for path, response_schema in {
    "/api/auth/2fa/verify": "AuthTOTPEnabledResponse",
    "/api/auth/2fa/disable": "AuthTOTPDisabledResponse",
}.items():
    operation = document["paths"][path]["post"]
    if operation["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AuthTOTPCodeRequest":
        raise SystemExit(f"2FA code request schema is missing for {path}")
    if operation["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != f"#/components/schemas/{response_schema}":
        raise SystemExit(f"2FA mutation response schema is missing for {path}")
    if "413" not in operation["responses"]:
        raise SystemExit(f"2FA body-size boundary is missing for {path}")

settings_get = document["paths"]["/api/settings"]["get"]
settings_put = document["paths"]["/api/settings"]["put"]
if settings_get["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SettingsValues":
    raise SystemExit("editable settings response schema is missing")
if settings_put["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SettingsUpdateRequest":
    raise SystemExit("editable settings request schema is missing")
if not {"400", "413", "503"}.issubset(settings_put["responses"]):
    raise SystemExit("editable settings mutation failures are incomplete")
editable = schemas["SettingsUpdateRequest"]
expected_setting_keys = {
    "hostnameDisplay", "adminEmail", "notifyOnLogin", "notifyOnError",
    "notifyOnDeployment", "webmail_url", "mail_admin_url", "mail_server_host",
    "mail_imap_port", "mail_smtp_starttls_port", "mail_smtp_ssl_port", "timezone",
}
if editable.get("additionalProperties") is not False or set(editable["properties"]) != expected_setting_keys:
    raise SystemExit("generic settings are not closed to the editable allowlist")
if editable.get("minProperties") != 1 or editable.get("maxProperties") != 12:
    raise SystemExit("editable settings request cardinality is not bounded")
if schemas["SettingsValues"].get("additionalProperties") is not False:
    raise SystemExit("settings inventory can expose undeclared internal records")

for method in ("get", "delete"):
    operation = document["paths"]["/api/settings/{key}"][method]
    parameter = next(item for item in operation["parameters"] if item["name"] == "key")
    if set(parameter["schema"].get("enum", [])) != expected_setting_keys:
        raise SystemExit(f"editable settings key allowlist is missing for {method}")
    if "404" not in operation["responses"]:
        raise SystemExit(f"internal settings not-found boundary is missing for {method}")
if "requestBody" in document["paths"]["/api/settings/{key}"]["delete"]:
    raise SystemExit("settings deletion must retain an empty-body contract")

portable_export = document["paths"]["/api/settings/portable"]["get"]
portable_preview = document["paths"]["/api/settings/portable/preview"]["post"]
portable_import = document["paths"]["/api/settings/portable/import"]["post"]
if portable_export["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PortableSettingsBundle":
    raise SystemExit("portable settings export schema is missing")
if portable_preview["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PortableSettingsBundleRequest":
    raise SystemExit("portable settings preview request schema is missing")
if portable_import["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PortableSettingsImportRequest":
    raise SystemExit("portable settings import request schema is missing")
if schemas["PortableSettingsImportRequest"]["properties"]["confirmed"].get("const") is not True:
    raise SystemExit("portable settings import confirmation is not explicit")
if "413" not in portable_preview["responses"] or not {"413", "429"}.issubset(portable_import["responses"]):
    raise SystemExit("portable settings body-size or mutation-rate boundary is missing")
if schemas["PortableSettingsBundleRequest"]["properties"]["settings"]["$ref"] != "#/components/schemas/SettingsUpdateRequest":
    raise SystemExit("portable settings input does not share the editable allowlist")

database_operations = {
    ("/api/databases", "get"),
    ("/api/databases", "post"),
    ("/api/databases/credentials", "get"),
    ("/api/databases/credentials/{name}", "get"),
    ("/api/databases/pgm-backup-files/{name}", "get"),
    ("/api/databases/pgm-backups", "get"),
    ("/api/databases/pgm-credentials", "get"),
    ("/api/databases/pgm-restore", "post"),
    ("/api/databases/users", "get"),
    ("/api/databases/{engine}/{name}", "delete"),
    ("/api/databases/{engine}/{name}/query", "post"),
    ("/api/databases/{engine}/{name}/tables", "get"),
}
for path, method in database_operations:
    operation = document["paths"][path][method]
    if operation["description"].startswith("Generated route and access contract"):
        raise SystemExit(f"database operation remains unpromoted: {method} {path}")
    if operation["responses"].get("default", {}).get("description", "").startswith("Endpoint response"):
        raise SystemExit(f"database operation retains the generic response: {method} {path}")

database_create = document["paths"]["/api/databases"]["post"]
if database_create["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DatabaseCreateRequest":
    raise SystemExit("database creation request schema is missing")
if "413" not in database_create["responses"] or schemas["DatabaseCreateRequest"].get("additionalProperties") is not False:
    raise SystemExit("database creation strict-body contract is incomplete")
if set(schemas["DatabaseCreateRequest"]["required"]) != {"engine", "name"}:
    raise SystemExit("database creation required fields drifted")

database_drop = document["paths"]["/api/databases/{engine}/{name}"]["delete"]
if database_drop["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DatabaseDropRequest":
    raise SystemExit("database drop confirmation schema is missing")
if schemas["DatabaseDropRequest"].get("additionalProperties") is not False or "413" not in database_drop["responses"]:
    raise SystemExit("database drop strict-body contract is incomplete")

database_query = document["paths"]["/api/databases/{engine}/{name}/query"]["post"]
query_schema = schemas["DatabaseQueryRequest"]
if query_schema["properties"]["query"].get("maxLength") != 65536:
    raise SystemExit("database query text limit is missing")
if query_schema["properties"]["write_mode"].get("const") is not False:
    raise SystemExit("database write_mode must remain explicitly false")
if "413" not in database_query["responses"]:
    raise SystemExit("database query body limit response is missing")

credential = schemas["PGMCredential"]["properties"]
for field in ("dbPassword", "connectionString"):
    if credential[field].get("x-sensitive") is not True:
        raise SystemExit(f"{field} must be marked as secret-bearing")
for path in ("/api/databases/credentials", "/api/databases/credentials/{name}", "/api/databases/pgm-credentials"):
    if document["paths"][path]["get"]["x-hserver-access"] != "Admin":
        raise SystemExit(f"credential route is not admin-only: {path}")

restore = document["paths"]["/api/databases/pgm-restore"]["post"]
if restore["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PGMRestoreRequest":
    raise SystemExit("PGM restore request schema is missing")
if not {"400", "404", "413", "503"} <= set(restore["responses"]):
    raise SystemExit("PGM restore failure contract is incomplete")

for path, method in (
    ("/api/databases/pgm-backups", "get"),
    ("/api/databases/pgm-backup-files/{name}", "get"),
    ("/api/databases/pgm-restore", "post"),
):
    if "503" not in document["paths"][path][method]["responses"]:
        raise SystemExit(f"PGM backup-root availability contract is missing: {method} {path}")

for schema_name in ("PGMCredentialList", "PGMBackupList", "PGMBackupFileList"):
    if schemas[schema_name].get("type") != "array":
        raise SystemExit(f"{schema_name} must remain a non-null array")

table_parameters = {item["name"]: item for item in document["paths"]["/api/databases/{engine}/{name}/tables"]["get"]["parameters"]}
if table_parameters["engine"]["schema"].get("enum") != ["postgres", "postgresql", "mariadb", "mysql"]:
    raise SystemExit("database engine path aliases are incomplete")
if table_parameters["name"]["schema"].get("maxLength") != 64:
    raise SystemExit("database name path bound is missing")
backup_parameters = document["paths"]["/api/databases/pgm-backup-files/{name}"]["get"]["parameters"]
if backup_parameters[0]["schema"].get("maxLength") != 128:
    raise SystemExit("PGM backup name path bound is missing")

users = document["paths"]["/api/users"]
user_list = users["get"]
if user_list["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/UserListResponse":
    raise SystemExit("panel-user inventory response schema is missing")
user_list_query = {item["name"]: item for item in user_list["parameters"]}
if set(user_list_query) != {"limit", "offset"}:
    raise SystemExit("panel-user pagination query contract is incomplete")
if user_list_query["limit"]["schema"].get("maximum") != 200:
    raise SystemExit("panel-user pagination maximum must remain 200")
user_create = users["post"]
if user_create["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/UserCreateRequest":
    raise SystemExit("panel-user creation request schema is missing")
if user_create["responses"]["201"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/User":
    raise SystemExit("panel-user creation response schema is missing")
user_item = document["paths"]["/api/users/{id}"]
user_update = user_item["put"]
if user_update["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/UserUpdateRequest":
    raise SystemExit("panel-user update request schema is missing")
if user_update["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/User":
    raise SystemExit("panel-user update response schema is missing")
if "409" not in user_update["responses"] or "final administrator" not in user_update["responses"]["409"]["description"]:
    raise SystemExit("panel-user update must publish the final-administrator conflict")
for schema in ("UserCreateRequest", "UserUpdateRequest"):
    if schemas[schema].get("additionalProperties") is not False:
        raise SystemExit(f"{schema} must reject unknown fields")
if schemas["UserUpdateRequest"].get("minProperties") != 1:
    raise SystemExit("panel-user update must require at least one supplied field")
for schema in ("UserCreateRequest", "UserUpdateRequest"):
    password = schemas[schema]["properties"]["password"]
    if password.get("minLength") != 8 or password.get("maxLength") != 128 or password.get("writeOnly") is not True:
        raise SystemExit(f"{schema} password constraints are incomplete")
user_delete = user_item["delete"]
if "204" not in user_delete["responses"] or "content" in user_delete["responses"]["204"]:
    raise SystemExit("panel-user deletion must return an empty 204 response")
if "409" not in user_delete["responses"] or "final administrator" not in user_delete["responses"]["409"]["description"]:
    raise SystemExit("panel-user deletion must publish the final-administrator conflict")

cron = document["paths"]
cron_status = cron["/api/cron/status"]["get"]
if cron_status["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CronServiceStatus":
    raise SystemExit("cron readiness response schema is missing")
cron_jobs = cron["/api/cron/jobs"]
if cron_jobs["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CronJobListResponse":
    raise SystemExit("cron job inventory response schema is missing")
if "503" not in cron_jobs["get"]["responses"]:
    raise SystemExit("cron inventory must publish the unavailable-client response")
cron_create = cron_jobs["post"]
if cron_create["responses"]["201"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CronJobMutationResult":
    raise SystemExit("cron creation response schema is missing")
cron_item = cron["/api/cron/jobs/{id}"]
for method in ("put", "delete"):
    query = {item["name"]: item for item in cron_item[method]["parameters"] if item["in"] == "query"}
    if set(query) != {"user"} or query["user"]["schema"].get("default") != "root":
        raise SystemExit(f"cron {method} owner query contract is incomplete")
if cron_item["put"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CronJobMutationResult":
    raise SystemExit("cron update response schema is missing")
if cron_item["delete"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CronJobDeleteResult":
    raise SystemExit("cron deletion response schema is missing")
cron_system = cron["/api/cron/system"]["get"]
if cron_system["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CronSystemFileListResponse":
    raise SystemExit("system cron-file inventory response schema is missing")
for schema in ("CronServiceStatus", "CronJob", "CronJobListResponse", "CronJobMutationResult", "CronJobDeleteResult", "CronSystemFile", "CronSystemFileListResponse"):
    if schemas[schema].get("additionalProperties") is not False:
        raise SystemExit(f"{schema} must reject undocumented response fields")

docker = document["paths"]
if docker["/api/docker/status"]["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DockerStatus":
    raise SystemExit("Docker readiness response schema is missing")
if docker["/api/docker/containers"]["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DockerContainerList":
    raise SystemExit("Docker container inventory response schema is missing")
docker_logs = docker["/api/docker/containers/{id}/logs"]["get"]
tail = {item["name"]: item for item in docker_logs["parameters"] if item["in"] == "query"}
if set(tail) != {"tail"} or tail["tail"]["schema"] != {"type": "integer", "minimum": 1, "maximum": 1000, "default": 200}:
    raise SystemExit("Docker container log tail contract is incomplete")
if docker_logs["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DockerContainerLogs":
    raise SystemExit("Docker container log response schema is missing")
docker_action = docker["/api/docker/containers/{id}/{action}"]["post"]
action = {item["name"]: item for item in docker_action["parameters"]}["action"]
if action["schema"].get("enum") != ["start", "stop", "restart", "pause", "unpause", "remove"]:
    raise SystemExit("Docker container action enum is incomplete")
if docker_action["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DockerContainerActionResult":
    raise SystemExit("Docker container action response schema is missing")
if docker["/api/docker/images"]["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DockerImageList":
    raise SystemExit("Docker image inventory response schema is missing")
docker_pull = docker["/api/docker/images/pull"]["post"]
docker_delete = docker["/api/docker/images/{id}"]["delete"]
for operation, label in ((docker_pull, "pull"), (docker_delete, "deletion")):
    if operation["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DockerImageMutationResult":
        raise SystemExit(f"Docker image {label} response schema is missing")
    if "400" not in operation["responses"] or "502" not in operation["responses"]:
        raise SystemExit(f"Docker image {label} failure contract is incomplete")
for schema in ("DockerStatus", "DockerContainer", "DockerContainerLogs", "DockerContainerActionResult", "DockerImage", "DockerImageMutationResult"):
    if schemas[schema].get("additionalProperties") is not False:
        raise SystemExit(f"{schema} must reject undocumented response fields")

pm2 = document["paths"]
pm2_list = pm2["/api/pm2/processes"]["get"]
if pm2_list["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PM2ProcessList":
    raise SystemExit("PM2 process inventory response schema is missing")
if "500" not in pm2_list["responses"] or "503" not in pm2_list["responses"]:
    raise SystemExit("PM2 process inventory failure contract is incomplete")
pm2_item = pm2["/api/pm2/processes/{id}"]["get"]
if pm2_item["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PM2Process":
    raise SystemExit("PM2 process detail response schema is missing")
pm2_logs = pm2["/api/pm2/processes/{id}/logs"]["get"]
lines = {item["name"]: item for item in pm2_logs["parameters"] if item["in"] == "query"}
if set(lines) != {"lines"} or lines["lines"]["schema"] != {"type": "integer", "minimum": 1, "maximum": 5000, "default": 100}:
    raise SystemExit("PM2 log line query contract is incomplete")
if pm2_logs["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PM2LogsResponse" or "400" not in pm2_logs["responses"]:
    raise SystemExit("PM2 log response contract is incomplete")
pm2_control = pm2["/api/pm2/processes/{id}/{action}"]["post"]
pm2_action = {item["name"]: item for item in pm2_control["parameters"]}["action"]
if pm2_action["schema"].get("enum") != ["start", "stop", "restart", "reload", "delete"]:
    raise SystemExit("PM2 lifecycle action enum is incomplete")
if pm2_control["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PM2ControlResult":
    raise SystemExit("PM2 lifecycle action response schema is missing")
pm2_deploy = pm2["/api/pm2/deploy"]["post"]
if pm2_deploy["responses"]["201"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PM2DeployResult":
    raise SystemExit("PM2 deploy response schema is missing")
pm2_save = pm2["/api/pm2/save"]["post"]
if pm2_save["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PM2SaveResult":
    raise SystemExit("PM2 save response schema is missing")
for schema in ("PM2Process", "PM2LogsResponse", "PM2ControlResult", "PM2DeployResult", "PM2SaveResult"):
    if schemas[schema].get("additionalProperties") is not False:
        raise SystemExit(f"{schema} must reject undocumented response fields")

firewall = document["paths"]
firewall_status = firewall["/api/firewall/status"]["get"]
if firewall_status["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/FirewallStatus":
    raise SystemExit("firewall readiness response schema is missing")
firewall_rules = firewall["/api/firewall/rules"]
if firewall_rules["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/FirewallRuleListResponse":
    raise SystemExit("firewall rule inventory response schema is missing")
if firewall_rules["post"]["responses"]["201"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ActionMessage":
    raise SystemExit("firewall rule creation response schema is missing")
firewall_delete = firewall["/api/firewall/rules/{number}"]["delete"]
if firewall_delete["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/FirewallRuleDeleteResult":
    raise SystemExit("firewall rule deletion response schema is missing")
if "403" not in firewall_delete["responses"] or "500" not in firewall_delete["responses"]:
    raise SystemExit("firewall rule deletion failure contract is incomplete")
firewall_toggle = firewall["/api/firewall/toggle"]["post"]
if firewall_toggle["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/FirewallToggleResult":
    raise SystemExit("firewall toggle response schema is missing")
for schema in ("FirewallRule", "FirewallStatus", "FirewallRuleListResponse", "FirewallRuleDeleteResult", "FirewallToggleResult"):
    if schemas[schema].get("additionalProperties") is not False:
        raise SystemExit(f"{schema} must reject undocumented response fields")
if schemas["FirewallStatus"]["properties"]["rules"].get("type") != "array":
    raise SystemExit("firewall status rule inventory must remain a non-null array")

dns = document["paths"]
if dns["/api/dns/status"]["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/BindServiceStatus":
    raise SystemExit("BIND readiness response schema is missing")
if dns["/api/dns/zones"]["post"]["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/BindCreateZoneRequest":
    raise SystemExit("BIND zone creation request schema is missing")
record_routes = dns["/api/dns/zones/{domain}/records"]
for method, schema in (("post", "BindAddRecordRequest"), ("put", "BindUpdateRecordRequest"), ("delete", "BindDeleteRecordRequest")):
    ref = record_routes[method]["requestBody"]["content"]["application/json"]["schema"]["$ref"]
    if ref != f"#/components/schemas/{schema}":
        raise SystemExit(f"BIND {method} record request schema is missing")
if record_routes["delete"]["requestBody"].get("required") is not False:
    raise SystemExit("BIND record deletion body must remain optional for exact query compatibility")
delete_query = {item["name"] for item in record_routes["delete"]["parameters"] if item["in"] == "query"}
if delete_query != {"name", "type", "value", "autoReload"}:
    raise SystemExit(f"BIND record deletion query contract is incomplete: {sorted(delete_query)}")
check = dns["/api/dns/check"]["post"]
if "requestBody" in check or check["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/BindCheckResult":
    raise SystemExit("BIND diagnostic contract must use an empty request and structured result")
lookup_query = {item["name"]: item for item in dns["/api/dns/lookup"]["get"]["parameters"]}
if set(lookup_query) != {"domain", "type"} or lookup_query["domain"].get("required") is not True:
    raise SystemExit("BIND lookup query contract is incomplete")
if schemas["BindServiceStatus"]["properties"]["state"].get("enum") != ["healthy", "not-installed", "not-configured", "stopped", "unavailable"]:
    raise SystemExit("BIND readiness states are incomplete")
for schema in ("BindCreateZoneRequest", "BindAddRecordRequest", "BindUpdateRecordRequest", "BindDeleteRecordRequest", "BindSOAUpdateRequest"):
    if schemas[schema].get("additionalProperties") is not False:
        raise SystemExit(f"{schema} must reject unknown fields")

deploy_templates = document["paths"]["/api/deploy/templates"]["get"]
if deploy_templates.get("x-hserver-access") != "Admin":
    raise SystemExit("deployment template inventory must remain admin-only")
if deploy_templates["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployTemplateInventory":
    raise SystemExit("deployment template inventory response schema is missing")
if schemas["DeployTemplateInventory"].get("additionalProperties") is not False:
    raise SystemExit("deployment template inventory must be a closed schema")
if schemas["DeployTemplateInventory"]["properties"]["status"].get("enum") != ["not_configured", "healthy", "unavailable"]:
    raise SystemExit("deployment template inventory states are incomplete")
if schemas["DeployTemplate"].get("additionalProperties") is not False:
    raise SystemExit("deployment templates must not accept undeclared credential or path fields")

deploy_paths = document["paths"]
deploy_targets = deploy_paths["/api/deploy/targets"]
if deploy_targets["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployTargetList":
    raise SystemExit("deployment target inventory response schema is missing")
if deploy_targets["post"]["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CreateDeployTargetRequest":
    raise SystemExit("deployment target create request schema is missing")
if deploy_targets["post"]["responses"]["201"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployTarget":
    raise SystemExit("deployment target create response schema is missing")

deploy_target = deploy_paths["/api/deploy/targets/{id}"]
if deploy_target["put"]["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/UpdateDeployTargetRequest":
    raise SystemExit("deployment target update request schema is missing")
if deploy_target["delete"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ActionMessage":
    raise SystemExit("deployment target delete response schema is missing")
for operation in (deploy_target["put"], deploy_target["delete"]):
    target_id = next((item for item in operation["parameters"] if item["name"] == "id"), None)
    if target_id is None or target_id["schema"] != {"type": "integer", "minimum": 1}:
        raise SystemExit("deployment target identity must be a positive integer")

for schema_name in ("CreateDeployTargetRequest", "UpdateDeployTargetRequest"):
    schema = schemas[schema_name]
    expected_required = {"name", "projectDir", "expectedUpdatedAt"} if schema_name == "UpdateDeployTargetRequest" else {"name", "projectDir"}
    if schema.get("additionalProperties") is not False or set(schema["required"]) != expected_required:
        raise SystemExit(f"{schema_name} strict required fields are incomplete")
    token = schema["properties"]["webhookToken"]
    if token.get("writeOnly") is not True or token.get("maxLength") != 4096:
        raise SystemExit(f"{schema_name} webhook token must remain bounded and write-only")
    if schema["properties"]["deployScript"].get("maxLength") != 65536:
        raise SystemExit(f"{schema_name} deploy script size bound is missing")

update_schema = schemas["UpdateDeployTargetRequest"]
if update_schema["properties"]["expectedUpdatedAt"].get("format") != "date-time":
    raise SystemExit("deployment target update observation is not a date-time")
if update_schema["properties"]["clearWebhookToken"].get("type") != "boolean":
    raise SystemExit("deployment target explicit webhook-token clearing is missing")
if "409" not in deploy_target["put"]["responses"]:
    raise SystemExit("stale deployment target updates must publish a conflict response")

deploy_preflight = deploy_paths["/api/deploy/targets/{id}/preflight"]["get"]
if deploy_preflight["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployPreflight":
    raise SystemExit("deployment preflight response schema is missing")
if set(schemas["DeployPreflightCheck"]["properties"]["status"]["enum"]) != {"pass", "pending", "fail"}:
    raise SystemExit("deployment preflight check states are incomplete")

for path in ("/api/deploy/manual/{targetId}", "/api/deploy/rollback/{targetId}"):
    operation = deploy_paths[path]["post"]
    if operation["responses"]["202"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployQueueResult":
        raise SystemExit(f"{path} queue receipt schema is missing")
    target_id = next((item for item in operation["parameters"] if item["name"] == "targetId"), None)
    if target_id is None or target_id["schema"] != {"type": "integer", "minimum": 1}:
        raise SystemExit(f"{path} target identity must be a positive integer")
if set(schemas["DeployQueueResult"]["properties"]["message"]["enum"]) != {"deployment queued", "rollback queued"}:
    raise SystemExit("deployment queue result messages are incomplete")

deploy_history = deploy_paths["/api/deploy/history"]["get"]
if deploy_history["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployRunList":
    raise SystemExit("deployment history response schema is missing")
history_parameters = {item["name"]: item for item in deploy_history["parameters"]}
if history_parameters["targetId"]["schema"] != {"type": "integer", "minimum": 1}:
    raise SystemExit("deployment history target filter is not bounded")
if history_parameters["limit"]["schema"] != {"type": "integer", "minimum": 1, "maximum": 500, "default": 50}:
    raise SystemExit("deployment history limit is not bounded")
if "logs" in schemas["DeployRun"]["properties"]:
    raise SystemExit("deployment history must not expose log bodies")

deploy_logs = deploy_paths["/api/deploy/history/{id}/logs"]["get"]
if deploy_logs["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployRunLogs":
    raise SystemExit("deployment run log response schema is missing")
run_id = next((item for item in deploy_logs["parameters"] if item["name"] == "id"), None)
if run_id is None or run_id["schema"] != {"type": "integer", "minimum": 1}:
    raise SystemExit("deployment run log identity must be a positive integer")

compose_services = deploy_paths["/api/deploy/targets/{id}/services"]["get"]
if compose_services["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ComposeServiceList":
    raise SystemExit("Compose service inventory response schema is missing")
if schemas["ComposeService"].get("additionalProperties") is not False:
    raise SystemExit("Compose service inventory must be a closed schema")

compose_logs = deploy_paths["/api/deploy/targets/{id}/services/{service}/logs"]["get"]
if compose_logs["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ComposeServiceLogs":
    raise SystemExit("Compose service log response schema is missing")
compose_log_parameters = {item["name"]: item for item in compose_logs["parameters"]}
if compose_log_parameters["tail"]["schema"] != {"type": "integer", "minimum": 1, "maximum": 1000, "default": 200}:
    raise SystemExit("Compose service log tail is not bounded")
if compose_log_parameters["service"]["schema"].get("pattern") != r"^(?!.*\.\.)[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$":
    raise SystemExit("Compose service identity is not bounded")
if schemas["ComposeServiceLogs"]["properties"]["logs"].get("maxLength") != 1048576:
    raise SystemExit("Compose service log response size is not bounded")

compose_action = deploy_paths["/api/deploy/targets/{id}/services/{service}/{action}"]["post"]
if "requestBody" in compose_action:
    raise SystemExit("Compose service actions must not accept a request body")
if compose_action["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ComposeServiceActionResult":
    raise SystemExit("Compose service action response schema is missing")
compose_action_parameters = {item["name"]: item for item in compose_action["parameters"]}
if set(compose_action_parameters["action"]["schema"].get("enum", [])) != {"start", "stop", "restart", "recreate"}:
    raise SystemExit("Compose service action enum is incomplete")

deploy_environment_path = deploy_paths["/api/deploy/targets/{id}/environment"]
for method in ("get", "put"):
    if deploy_environment_path[method]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployEnvironment":
        raise SystemExit(f"Compose environment {method} response schema is missing")
environment_put = deploy_environment_path["put"]
if environment_put["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployEnvironmentSetRequest":
    raise SystemExit("Compose environment write request schema is missing")
if "413" not in environment_put["responses"]:
    raise SystemExit("Compose environment request size failure is missing")
environment_request = schemas["DeployEnvironmentSetRequest"]
if environment_request.get("additionalProperties") is not False or set(environment_request["required"]) != {"key", "value"}:
    raise SystemExit("Compose environment write request is not strict")
if environment_request["properties"]["value"].get("writeOnly") is not True:
    raise SystemExit("Compose environment value must remain write-only")
if "value" in schemas["DeployEnvironmentVariable"]["properties"]:
    raise SystemExit("Compose environment inventory must not expose values")

environment_delete = deploy_paths["/api/deploy/targets/{id}/environment/{key}"]["delete"]
if environment_delete["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployEnvironment":
    raise SystemExit("Compose environment delete response schema is missing")
environment_key = next((item for item in environment_delete["parameters"] if item["name"] == "key"), None)
if environment_key is None or environment_key["schema"].get("pattern") != r"^[A-Za-z_][A-Za-z0-9_]{0,127}$":
    raise SystemExit("Compose environment key path is not bounded")

deploy_domains = deploy_paths["/api/deploy/targets/{id}/domains"]
if deploy_domains["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployDomainList":
    raise SystemExit("project domain inventory response schema is missing")
if deploy_domains["post"]["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CreateDeployDomainRequest":
    raise SystemExit("project domain create request schema is missing")
if deploy_domains["post"]["responses"]["201"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployDomain":
    raise SystemExit("project domain create response schema is missing")
if "413" not in deploy_domains["post"]["responses"]:
    raise SystemExit("project domain request size failure is missing")
domain_request = schemas["CreateDeployDomainRequest"]
if domain_request.get("additionalProperties") is not False or set(domain_request["required"]) != {"domain", "service", "hostPort"}:
    raise SystemExit("project domain create request is not strict")
if domain_request["properties"]["hostPort"].get("minimum") != 1 or domain_request["properties"]["hostPort"].get("maximum") != 65535:
    raise SystemExit("project domain host port is not bounded")

domain_schema = schemas["DeployDomain"]
if domain_schema.get("additionalProperties") is not False or "tlsEnabled" in domain_schema["properties"]:
    raise SystemExit("project domain response schema exposes an internal field")
if set(domain_schema["properties"]["tlsStatus"]["enum"]) != {"not_configured", "healthy", "expiring", "expired", "unavailable"}:
    raise SystemExit("project domain TLS states are incomplete")
if domain_schema["properties"]["upstream"].get("pattern") != r"^http://127\.0\.0\.1:[1-9][0-9]{0,4}$":
    raise SystemExit("project domain upstream is not fixed to loopback")

domain_item_path = deploy_paths["/api/deploy/targets/{id}/domains/{domainId}"]["delete"]
if "requestBody" in domain_item_path or set(domain_item_path["responses"]) != {"204", "400", "404", "409", "500", "503"}:
    raise SystemExit("project domain delete empty-body contract is incomplete")
domain_id = next((item for item in domain_item_path["parameters"] if item["name"] == "domainId"), None)
if domain_id is None or domain_id["schema"] != {"type": "integer", "minimum": 1}:
    raise SystemExit("project domain identity must be a positive integer")

domain_health = deploy_paths["/api/deploy/targets/{id}/domains/{domainId}/health"]["get"]
if domain_health["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployDomainHealth":
    raise SystemExit("project domain health response schema is missing")
if set(schemas["DeployDomainHealth"]["properties"]["status"]["enum"]) != {"healthy", "unhealthy", "unavailable"}:
    raise SystemExit("project domain health states are incomplete")

domain_tls_path = deploy_paths["/api/deploy/targets/{id}/domains/{domainId}/tls"]
domain_tls_enable = domain_tls_path["post"]
if domain_tls_enable["requestBody"].get("required") is not False:
    raise SystemExit("project domain TLS body must remain optional")
if domain_tls_enable["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/EnableDeployDomainTLSRequest":
    raise SystemExit("project domain TLS request schema is missing")
if domain_tls_enable["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployDomain" or "413" not in domain_tls_enable["responses"]:
    raise SystemExit("project domain TLS enable responses are incomplete")
tls_request = schemas["EnableDeployDomainTLSRequest"]
if tls_request.get("additionalProperties") is not False or tls_request.get("required"):
    raise SystemExit("project domain TLS request must allow only an optional email")
if tls_request["properties"]["email"].get("maxLength") != 254:
    raise SystemExit("project domain TLS email is not bounded")
domain_tls_disable = domain_tls_path["delete"]
if "requestBody" in domain_tls_disable or domain_tls_disable["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployDomain":
    raise SystemExit("project domain TLS disable contract is incomplete")

deploy_staging = document["paths"]["/api/deploy/targets/{id}/staging"]["post"]
if deploy_staging.get("x-hserver-access") != "Admin":
    raise SystemExit("staging creation must remain admin-only")
if deploy_staging["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CreateDeployStagingRequest":
    raise SystemExit("staging request schema is missing")
if deploy_staging["responses"]["201"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployStagingReceipt":
    raise SystemExit("staging receipt schema is missing")
if schemas["CreateDeployStagingRequest"].get("additionalProperties") is not False:
    raise SystemExit("staging request must reject undeclared copy controls")
for field in ("environmentValuesCopied", "webhookSecretCopied", "domainsCopied", "dnsConfigured"):
    if schemas["DeployStagingReceipt"]["properties"][field].get("const") is not False:
        raise SystemExit(f"staging receipt {field} must prove no implicit copy")
if schemas["DeployTarget"]["properties"]["webhookToken"].get("maxLength") != 0:
    raise SystemExit("deployment target response must not expose webhook signing material")

deploy_revision = document["paths"]["/api/deploy/targets/{id}/revision"]["get"]
if deploy_revision["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DeployRevisionComparison":
    raise SystemExit("deployment revision comparison response schema is missing")
if schemas["DeployRevisionComparison"].get("additionalProperties") is not False:
    raise SystemExit("deployment revision comparison must be a closed schema")
if schemas["DeployRevisionComparison"]["properties"]["state"].get("enum") != ["not_deployed", "ready", "unavailable"]:
    raise SystemExit("deployment revision comparison states are incomplete")
for field in ("currentCommit", "deployedCommit", "rollbackCommit"):
    if schemas["DeployRevisionComparison"]["properties"][field].get("pattern") != "^[a-f0-9]{40,64}$":
        raise SystemExit(f"deployment revision field {field} is not constrained")

php_config = document["paths"]["/api/php/pools/{version}/{domain}/config"]
if php_config["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PHPPoolConfigContent":
    raise SystemExit("local PHP-FPM pool read response schema is missing")
php_replace = php_config["put"]
if php_replace["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PHPPoolConfigReplaceRequest":
    raise SystemExit("local PHP-FPM pool replacement request schema is missing")
if php_replace["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PHPPoolConfigReplaceReceipt":
    raise SystemExit("local PHP-FPM pool replacement receipt schema is missing")
if schemas["PHPPoolConfigReplaceRequest"].get("additionalProperties") is not False:
    raise SystemExit("local PHP-FPM pool replacement must reject unknown fields")
if schemas["PHPPoolConfigReplaceRequest"]["properties"]["checksum"].get("pattern") != "^[a-f0-9]{64}$":
    raise SystemExit("local PHP-FPM pool replacement checksum lock is missing")
for status in ("409", "413", "422", "502"):
    if status not in php_replace["responses"]:
        raise SystemExit(f"local PHP-FPM pool replacement response {status} is missing")

nginx_config = document["paths"]["/api/nginx/configs/{filename}"]
if nginx_config["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigContent":
    raise SystemExit("local Nginx configuration read response schema is missing")
nginx_replace = nginx_config["put"]
if nginx_replace["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigReplaceRequest":
    raise SystemExit("local Nginx replacement request schema is missing")
if nginx_replace["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigReplaceReceipt":
    raise SystemExit("local Nginx replacement receipt schema is missing")
if schemas["NginxConfigReplaceRequest"].get("additionalProperties") is not False:
    raise SystemExit("local Nginx replacement must reject unknown fields")
if schemas["NginxConfigReplaceRequest"]["properties"]["checksum"].get("pattern") != "^[a-f0-9]{64}$":
    raise SystemExit("local Nginx replacement checksum lock is missing")
for status in ("409", "413", "422", "503"):
    if status not in nginx_replace["responses"]:
        raise SystemExit(f"local Nginx replacement response {status} is missing")

nginx_archive = nginx_config["delete"]
if nginx_archive["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigArchiveRequest":
    raise SystemExit("local Nginx archive request schema is missing")
if nginx_archive["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigArchiveReceipt":
    raise SystemExit("local Nginx archive receipt schema is missing")
if schemas["NginxConfigArchiveRequest"].get("additionalProperties") is not False:
    raise SystemExit("local Nginx archive must reject unknown fields")
if schemas["NginxConfigArchiveRequest"].get("required") != ["checksum"]:
    raise SystemExit("local Nginx archive must require checksum")
if schemas["NginxConfigArchiveRequest"]["properties"]["checksum"].get("pattern") != "^[a-f0-9]{64}$":
    raise SystemExit("local Nginx archive checksum lock is missing")
for status in ("400", "404", "409", "413", "422", "503"):
    if status not in nginx_archive["responses"]:
        raise SystemExit(f"local Nginx archive response {status} is missing")

nginx_archive_list = document["paths"]["/api/nginx/archives"]["get"]
if nginx_archive_list["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigArchiveList":
    raise SystemExit("local Nginx archive inventory schema is missing")
if schemas["NginxConfigArchiveList"]["items"].get("$ref") != "#/components/schemas/NginxConfigArchive":
    raise SystemExit("local Nginx archive inventory item schema is missing")
nginx_restore = document["paths"]["/api/nginx/archives/{archive}/restore"]["post"]
if nginx_restore["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigArchiveRequest":
    raise SystemExit("local Nginx archive restore request schema is missing")
if nginx_restore["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigArchiveRestoreReceipt":
    raise SystemExit("local Nginx archive restore receipt schema is missing")
if schemas["NginxConfigArchiveRestoreReceipt"]["properties"]["isEnabled"].get("const") is not False:
    raise SystemExit("local Nginx archive restore must remain disabled")
for status in ("400", "404", "409", "413", "422", "503"):
    if status not in nginx_restore["responses"]:
        raise SystemExit(f"local Nginx archive restore response {status} is missing")

nginx_backup_list = document["paths"]["/api/nginx/backups"]["get"]
if nginx_backup_list["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigBackupList":
    raise SystemExit("local Nginx edit-backup inventory schema is missing")
if schemas["NginxConfigBackupList"]["items"].get("$ref") != "#/components/schemas/NginxConfigBackup":
    raise SystemExit("local Nginx edit-backup inventory item schema is missing")
nginx_rollback = document["paths"]["/api/nginx/backups/{backup}/restore"]["post"]
if nginx_rollback["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigBackupRestoreRequest":
    raise SystemExit("local Nginx edit-backup rollback request schema is missing")
if nginx_rollback["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigBackupRestoreReceipt":
    raise SystemExit("local Nginx edit-backup rollback receipt schema is missing")
if set(schemas["NginxConfigBackupRestoreRequest"].get("required", [])) != {"backupChecksum", "currentChecksum"}:
    raise SystemExit("local Nginx edit-backup rollback requires both checksum locks")
if schemas["NginxConfigBackupRestoreRequest"].get("additionalProperties") is not False:
    raise SystemExit("local Nginx edit-backup rollback must reject unknown fields")
for status in ("400", "404", "409", "413", "422", "503"):
    if status not in nginx_rollback["responses"]:
        raise SystemExit(f"local Nginx edit-backup rollback response {status} is missing")

nginx_create = document["paths"]["/api/nginx/configs"]["post"]
if nginx_create["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigCreateRequest":
    raise SystemExit("local Nginx create request schema is missing")
if nginx_create["responses"]["201"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigContent":
    raise SystemExit("local Nginx create response schema is missing")
if schemas["NginxConfigCreateRequest"].get("additionalProperties") is not False:
    raise SystemExit("local Nginx create must reject unknown fields")
if schemas["NginxConfigCreateRequest"]["properties"]["type"].get("enum") != ["php", "static", "proxy", "redirect"]:
    raise SystemExit("local Nginx create type allowlist is missing")
for status in ("409", "422", "503"):
    if status not in nginx_create["responses"]:
        raise SystemExit(f"local Nginx create response {status} is missing")

nginx_state = document["paths"]["/api/nginx/configs/{filename}/state"]["put"]
if nginx_state["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigStateRequest":
    raise SystemExit("local Nginx state request schema is missing")
if nginx_state["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigStateReceipt":
    raise SystemExit("local Nginx state receipt schema is missing")
if schemas["NginxConfigStateRequest"].get("additionalProperties") is not False:
    raise SystemExit("local Nginx state request must reject unknown fields")
if schemas["NginxConfigStateRequest"].get("required") != ["enabled"]:
    raise SystemExit("local Nginx state request must require enabled")
for status in ("400", "404", "503"):
    if status not in nginx_state["responses"]:
        raise SystemExit(f"local Nginx state response {status} is missing")
legacy_nginx_state = document["paths"]["/api/nginx/configs/{filename}/toggle"]["post"]
if legacy_nginx_state.get("deprecated") is not True:
    raise SystemExit("legacy local Nginx toggle alias is not deprecated")
if legacy_nginx_state["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/NginxConfigStateRequest":
    raise SystemExit("legacy local Nginx toggle alias lost its desired-state body")

php_action = document["paths"]["/api/php/versions/{version}/actions/{action}"]["post"]
action_parameter = next(parameter for parameter in php_action["parameters"] if parameter["name"] == "action")
if action_parameter["schema"].get("enum") != ["test", "reload", "restart"]:
    raise SystemExit("local PHP-FPM action allowlist is missing")
if php_action["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ActionMessage":
    raise SystemExit("local PHP-FPM action receipt schema is missing")
for status in ("400", "422", "502"):
    if status not in php_action["responses"]:
        raise SystemExit(f"local PHP-FPM action response {status} is missing")
for alias_path in ("/api/php/versions/{version}/restart", "/api/php/restart/{version}"):
    alias = document["paths"][alias_path]["post"]
    if alias.get("deprecated") is not True:
        raise SystemExit(f"legacy PHP-FPM restart route is not marked deprecated: {alias_path}")
    if alias["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ActionMessage":
        raise SystemExit(f"legacy PHP-FPM restart receipt schema is missing: {alias_path}")
    for status in ("400", "422", "502"):
        if status not in alias["responses"]:
            raise SystemExit(f"legacy PHP-FPM restart response {status} is missing: {alias_path}")

create = document["paths"]["/api/backups"]["post"]
if create["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/BackupCreateRequest":
    raise SystemExit("backup create request schema is missing")
if schemas["BackupCreateRequest"].get("additionalProperties") is not False:
    raise SystemExit("backup create request must reject unknown fields")
backup_create_schema = schemas["BackupCreateRequest"]
if "filesRoot" in backup_create_schema["properties"]:
    raise SystemExit("backup create must not expose a caller-selected filesystem root")
if backup_create_schema["properties"]["vhosts"].get("maxItems") != 16:
    raise SystemExit("backup create vhost selector bound is missing")
if backup_create_schema["properties"]["vhosts"].get("uniqueItems") is not True:
    raise SystemExit("backup create vhost identities must be unique")
if backup_create_schema["properties"]["vhosts"]["items"].get("pattern") != r"^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$":
    raise SystemExit("backup create vhost identity pattern is missing")
if create["responses"]["202"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AsyncJobAccepted":
    raise SystemExit("backup create accepted response schema is missing")

gdrive_settings = document["paths"]["/api/backups/gdrive/settings"]["put"]
if gdrive_settings["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/GDriveSettingsUpdateRequest":
    raise SystemExit("Google Drive settings request schema is missing")
gdrive_settings_schema = schemas["GDriveSettingsUpdateRequest"]
if gdrive_settings_schema.get("additionalProperties") is not False:
    raise SystemExit("Google Drive settings request must reject unknown fields")
if set(gdrive_settings_schema["required"]) != {"folder", "autoUpload", "remoteRetentionDays", "notifyOnSuccess", "notifyOnFailure"}:
    raise SystemExit("Google Drive settings replacement fields are incomplete")
if gdrive_settings_schema["properties"]["remoteRetentionDays"].get("minimum") != 0 or gdrive_settings_schema["properties"]["remoteRetentionDays"].get("maximum") != 365:
    raise SystemExit("Google Drive retention bounds are missing")

gdrive_restore = document["paths"]["/api/backups/gdrive/restore"]["post"]
if gdrive_restore["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/GDriveRestoreRequest":
    raise SystemExit("Google Drive restore request schema is missing")
if set(schemas["GDriveRestoreRequest"]["required"]) != {"fileName"} or schemas["GDriveRestoreRequest"].get("additionalProperties") is not False:
    raise SystemExit("Google Drive restore must use one exact observed filename")

gdrive_complete = document["paths"]["/api/backups/gdrive/oauth/complete"]["post"]
if gdrive_complete["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/GDriveOAuthCompleteRequest":
    raise SystemExit("Google Drive OAuth completion request schema is missing")

gdrive_app = document["paths"]["/api/backups/gdrive/oauth-app"]["put"]
if gdrive_app["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/GDriveOAuthAppUpdateRequest":
    raise SystemExit("Google Drive OAuth app request schema is missing")
if schemas["GDriveOAuthAppUpdateRequest"]["properties"]["clientSecret"].get("writeOnly") is not True:
    raise SystemExit("Google Drive OAuth client secret must be write-only")

for path in (
    "/api/backups/gdrive/oauth/start",
    "/api/backups/gdrive/disconnect",
    "/api/backups/gdrive/dismiss-error",
    "/api/backups/gdrive/test",
):
    if "requestBody" in document["paths"][path]["post"]:
        raise SystemExit(f"{path} must not advertise an ignored request body")

validation = document["paths"]["/api/backups/restore/{id}/validate"]["get"]
if validation["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/RestoreValidation":
    raise SystemExit("restore validation response schema is missing")
required = set(schemas["RestoreValidation"]["required"])
if not {"databaseRecovery", "filesRollback", "includesDatabase", "includesFiles"}.issubset(required):
    raise SystemExit("restore capability fields are not required")

schedule_list = document["paths"]["/api/backups/schedules"]["get"]
if schedule_list["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/BackupScheduleListResponse":
    raise SystemExit("backup schedule list response schema is missing")
schedule_response_required = set(schemas["BackupSchedule"]["required"])
if "frequency" in schedule_response_required or "time" in schedule_response_required:
    raise SystemExit("custom cron responses must not require a misleading preset label")
schedule_set = document["paths"]["/api/backups/schedules"]["post"]
if schedule_set["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/BackupScheduleSetRequest":
    raise SystemExit("backup schedule set request schema is missing")
schedule_set_schema = schemas["BackupScheduleSetRequest"]
if schedule_set_schema.get("additionalProperties") is not False:
    raise SystemExit("backup schedule set request must reject unknown fields")
if len(schedule_set_schema.get("oneOf", [])) != 2:
    raise SystemExit("backup schedule source alternatives are not explicit")
for field in ("retention_count", "retentionCount", "retention_days"):
    if schedule_set_schema["properties"][field].get("maximum") != 365:
        raise SystemExit(f"backup schedule retention bound is missing for {field}")
schedule_delete = document["paths"]["/api/backups/schedules"]["delete"]
if schedule_delete["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/BackupScheduleDeleteRequest":
    raise SystemExit("backup schedule delete request schema is missing")
if set(schemas["BackupScheduleDeleteRequest"]["required"]) != {"rawLine"}:
    raise SystemExit("backup schedule delete must require exact observed rawLine")
if schedule_delete["responses"]["404"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ErrorResponse":
    raise SystemExit("backup schedule delete stale-target response is missing")

snapshot_settings = document["paths"]["/api/backups/snapshot/settings"]["put"]
if snapshot_settings["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SnapshotSettingsUpdateRequest":
    raise SystemExit("snapshot settings update request schema is missing")
snapshot_settings_schema = schemas["SnapshotSettingsUpdateRequest"]
if snapshot_settings_schema.get("additionalProperties") is not False:
    raise SystemExit("snapshot settings update must reject unknown fields")
if set(snapshot_settings_schema["required"]) != {"destination", "repoFolder", "enabledPaths", "keepDaily", "keepWeekly", "keepMonthly", "passwordAcknowledged"}:
    raise SystemExit("snapshot settings replacement fields are incomplete")
if set(snapshot_settings_schema["properties"]["destination"].get("enum", [])) != {"gdrive", "s3"}:
    raise SystemExit("snapshot destination allowlist is incomplete")
if snapshot_settings_schema["properties"]["enabledPaths"].get("uniqueItems") is not True:
    raise SystemExit("snapshot manifest identities are not unique")
if snapshot_settings_schema["properties"]["keepMonthly"].get("maximum") != 120:
    raise SystemExit("snapshot monthly retention bound is missing")

snapshot_status = document["paths"]["/api/backups/snapshot/status"]["get"]
if snapshot_status["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SnapshotStatus":
    raise SystemExit("snapshot destination status response schema is missing")
if set(schemas["SnapshotStatus"]["properties"]["destinationStatus"].get("enum", [])) != {"not_configured", "unavailable", "healthy"}:
    raise SystemExit("snapshot destination health states are incomplete")
if "canPurgeRepository" not in schemas["SnapshotStatus"]["required"]:
    raise SystemExit("snapshot provider capability receipt is missing")

snapshot_list = document["paths"]["/api/backups/snapshot/list"]["get"]
if snapshot_list["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SnapshotListResponse":
    raise SystemExit("snapshot inventory response schema is missing")

snapshot_run = document["paths"]["/api/backups/snapshot/run"]["post"]
if "requestBody" in snapshot_run:
    raise SystemExit("snapshot run must not advertise an ignored request body")
if snapshot_run["responses"]["202"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SnapshotRunAccepted":
    raise SystemExit("snapshot run accepted response schema is missing")

snapshot_restore = document["paths"]["/api/backups/snapshot/restore"]["post"]
if snapshot_restore["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SnapshotRestoreRequest":
    raise SystemExit("snapshot restore request schema is missing")
snapshot_restore_schema = schemas["SnapshotRestoreRequest"]
if set(snapshot_restore_schema["required"]) != {"snapshotId"} or {"target", "includes"}.intersection(snapshot_restore_schema["properties"]):
    raise SystemExit("snapshot restore must use observed identity and a fixed staging target")
if snapshot_restore_schema["properties"]["vhosts"].get("maxItems") != 16:
    raise SystemExit("snapshot restore vhost bound is missing")
if "root-crontab" in snapshot_restore_schema["properties"]["manifestIds"]["items"].get("enum", []):
    raise SystemExit("non-path root crontab export must not be a direct restore selector")

snapshot_purge = document["paths"]["/api/backups/snapshot/purge-repo"]["post"]
if snapshot_purge["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SnapshotPurgeRepositoryRequest":
    raise SystemExit("snapshot purge request schema is missing")
snapshot_purge_schema = schemas["SnapshotPurgeRepositoryRequest"]
if snapshot_purge_schema.get("additionalProperties") is not False:
    raise SystemExit("snapshot purge must reject unknown fields")
if set(snapshot_purge_schema["required"]) != {"repoFolder", "confirmation"}:
    raise SystemExit("snapshot purge identity and confirmation are required")
if snapshot_purge_schema["properties"]["confirmation"].get("const") != "purge-snapshot-repository":
    raise SystemExit("snapshot purge confirmation is not fixed")
if snapshot_purge_schema["properties"]["repoFolder"].get("pattern") != snapshot_settings_schema["properties"]["repoFolder"].get("pattern"):
    raise SystemExit("snapshot purge repository identity does not match settings policy")
if not {"200", "400", "409", "422", "503"}.issubset(snapshot_purge["responses"]):
    raise SystemExit("snapshot purge response boundary is incomplete")

alert_create = document["paths"]["/api/notify/rules"]["post"]
if alert_create["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AlertRuleCreateRequest":
    raise SystemExit("alert rule create request schema is missing")
alert_types = set(schemas["AlertRule"]["properties"]["type"]["enum"])
if alert_types != {"cpu_usage", "memory_usage", "disk_usage", "ssl_expiry", "service_down", "failed_logins"}:
    raise SystemExit("canonical alert rule type allowlist is incomplete")
if schemas["AlertRuleUpdateRequest"].get("minProperties") != 1 or schemas["AlertRuleUpdateRequest"].get("additionalProperties") is not False:
    raise SystemExit("strict partial alert rule update contract is incomplete")
if len(schemas["AlertRuleCreateRequest"].get("oneOf", [])) != 6:
    raise SystemExit("type-specific alert rule creation contracts are incomplete")

alert_history = document["paths"]["/api/notify/history"]["get"]
history_parameters = {item["name"]: item["schema"] for item in alert_history["parameters"] if item["in"] == "query"}
if history_parameters.get("limit", {}).get("maximum") != 200 or history_parameters.get("offset", {}).get("minimum") != 0:
    raise SystemExit("alert history pagination bounds are missing")
if alert_history["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AlertHistoryPage":
    raise SystemExit("alert history response schema is missing")

cloudflare_zones = document["paths"]["/api/cloudflare/zones"]["get"]
if cloudflare_zones["responses"]["200"]["content"]["application/json"]["schema"]["items"]["$ref"] != "#/components/schemas/CloudflareZone":
    raise SystemExit("Cloudflare zone inventory schema is missing")
cloudflare_create = document["paths"]["/api/cloudflare/zones/{zoneId}/records"]["post"]
if cloudflare_create["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CloudflareRecordMutationRequest":
    raise SystemExit("Cloudflare record create request schema is missing")
cloudflare_record_request = schemas["CloudflareRecordMutationRequest"]
if cloudflare_record_request.get("additionalProperties") is not False:
    raise SystemExit("Cloudflare record mutation must reject unknown fields")
if set(cloudflare_record_request.get("required", [])) != {"type", "name", "content", "ttl", "proxied"}:
    raise SystemExit("Cloudflare full record mutation fields are incomplete")
ttl_contract = cloudflare_record_request["properties"]["ttl"].get("oneOf", [])
if len(ttl_contract) != 2 or ttl_contract[0].get("const") != 1 or ttl_contract[1].get("minimum") != 30 or ttl_contract[1].get("maximum") != 86400:
    raise SystemExit("Cloudflare record TTL contract is incomplete")
cloudflare_update = document["paths"]["/api/cloudflare/zones/{zoneId}/records/{recordId}"]["put"]
if cloudflare_update["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CloudflareRecordMutationRequest":
    raise SystemExit("Cloudflare record replacement request schema is missing")
cloudflare_delete = document["paths"]["/api/cloudflare/zones/{zoneId}/records/{recordId}"]["delete"]
if "204" not in cloudflare_delete["responses"] or "requestBody" in cloudflare_delete:
    raise SystemExit("Cloudflare record deletion contract is not empty-body no-content")
cloudflare_proxy = document["paths"]["/api/cloudflare/zones/{zoneId}/records/{recordId}/proxy"]["put"]
if cloudflare_proxy["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CloudflareProxyRequest":
    raise SystemExit("Cloudflare proxy request schema is missing")
if schemas["CloudflareProxyRequest"].get("required") != ["proxied"] or schemas["CloudflareProxyRequest"].get("additionalProperties") is not False:
    raise SystemExit("Cloudflare proxy request must be one strict explicit boolean")
cloudflare_purge = document["paths"]["/api/cloudflare/zones/{zoneId}/purge"]["post"]
if "requestBody" in cloudflare_purge or cloudflare_purge["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CloudflarePurgeReceipt":
    raise SystemExit("Cloudflare complete cache purge contract is incomplete")
cloudflare_mail = document["paths"]["/api/cloudflare/mail-autofix/{domain}"]["post"]
if cloudflare_mail["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CloudflareMailDNSReconcileResult":
    raise SystemExit("Cloudflare mail DNS reconciliation receipt is missing")

agent_heartbeat = document["paths"]["/api/agent/v1/heartbeat"]["post"]
if agent_heartbeat["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AgentHeartbeatRequest":
    raise SystemExit("agent heartbeat request schema is missing")
if agent_heartbeat["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AgentHeartbeatResponse":
    raise SystemExit("agent heartbeat response schema is missing")
agent_headers = {item["name"]: item for item in agent_heartbeat["parameters"] if item["in"] == "header"}
if set(agent_headers) != {"X-HServer-Node-ID"} or agent_headers["X-HServer-Node-ID"]["required"] is not True:
    raise SystemExit("agent heartbeat identity header is missing")
heartbeat_request = schemas["AgentHeartbeatRequest"]
if set(heartbeat_request["required"]) != {"protocol_version", "node_id", "sent_at"}:
    raise SystemExit("agent heartbeat required fields do not match validation")
if heartbeat_request["properties"]["protocol_version"].get("const") != "v1":
    raise SystemExit("agent heartbeat protocol is not fixed to v1")
capabilities = heartbeat_request["properties"]["capabilities"]
if capabilities.get("uniqueItems") is not True or "terminal" not in capabilities["items"].get("enum", []):
    raise SystemExit("agent heartbeat capability contract is incomplete")
inventory = schemas["ManagedNodeInventory"]
if inventory["properties"]["arch"].get("maxLength") != 32 or "arch" in inventory.get("required", []):
    raise SystemExit("managed-node architecture must be bounded and legacy-compatible")

agent_poll = document["paths"]["/api/agent/v1/tasks/poll"]["post"]
if "requestBody" in agent_poll:
    raise SystemExit("agent task poll unexpectedly declares a request body")
if agent_poll["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AgentTaskPollResponse":
    raise SystemExit("agent task poll response schema is missing")
if "required" in schemas["AgentTaskPollResponse"]:
    raise SystemExit("empty agent task poll receipt must remain valid")

agent_result = document["paths"]["/api/agent/v1/tasks/{id}/result"]["post"]
if agent_result["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AgentTaskResultRequest":
    raise SystemExit("agent task result request schema is missing")
if agent_result["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AgentTask":
    raise SystemExit("agent task result receipt schema is missing")
result_request = schemas["AgentTaskResultRequest"]
if set(result_request["properties"]["status"]["enum"]) != {"completed", "failed"}:
    raise SystemExit("agent task result terminal status enum is invalid")
if result_request["properties"]["result"].get("maxProperties") != 100:
    raise SystemExit("agent task result map bound is missing")
if result_request["properties"]["result"]["additionalProperties"].get("maxLength") != 5 << 20:
    raise SystemExit("agent task result value bound is missing")
failed_result = result_request["allOf"][0]
if failed_result["if"]["properties"]["status"].get("const") != "failed" or "error" not in failed_result["then"]["required"]:
    raise SystemExit("failed agent task result does not require failure text")

node_list = document["paths"]["/api/nodes"]["get"]
if node_list["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ManagedNodeList":
    raise SystemExit("managed-node list response schema is missing")
node_create = document["paths"]["/api/nodes"]["post"]
if node_create["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ManagedNodeRegisterRequest":
    raise SystemExit("managed-node enrollment request schema is missing")
if node_create["responses"]["201"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ManagedNodeRegistration":
    raise SystemExit("managed-node enrollment response schema is missing")

managed_node_extension = schemas["ManagedNode"]["allOf"][1]
if not {"compatibility", "online"}.issubset(set(managed_node_extension["required"])):
    raise SystemExit("managed-node server-computed fields are not required")
if managed_node_extension["properties"]["online"]["type"] != "boolean":
    raise SystemExit("managed-node online field is not boolean")

task_create = document["paths"]["/api/nodes/{id}/tasks"]["post"]
if task_create["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AgentTaskRequest":
    raise SystemExit("managed-node task request schema is missing")
if task_create["responses"]["201"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/AgentTask":
    raise SystemExit("managed-node task response schema is missing")
if task_create["responses"]["409"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ErrorResponse":
    raise SystemExit("managed-node offline conflict schema is missing")

task_kind = schemas["AgentTaskRequest"]["properties"]["kind"]["enum"]
if "agent.update.status" not in task_kind or "shell" in task_kind:
    raise SystemExit("managed-node task kind enum is not the fixed allowlist")
if schemas["AgentTaskRequest"]["properties"]["payload"]["maxProperties"] != 6:
    raise SystemExit("managed-node task payload bound is missing")
task_request = schemas["AgentTaskRequest"]
if task_request["required"] != ["kind", "confirmed"]:
    raise SystemExit("managed-node task confirmation is not required")
if task_request["properties"]["confirmed"] != {"type": "boolean", "const": True}:
    raise SystemExit("managed-node task confirmation must be const true")

task_list = document["paths"]["/api/nodes/{id}/tasks"]["get"]
limit = next((item for item in task_list["parameters"] if item["name"] == "limit" and item["in"] == "query"), None)
if limit is None or limit["schema"].get("minimum") != 1 or limit["schema"].get("default") != 20:
    raise SystemExit("managed-node task history limit contract is missing")

host_status = document["paths"]["/api/system/actions/status"]["get"]
if host_status["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/HostActionStatus":
    raise SystemExit("local maintenance status schema is missing")
if "disk-cleanup" not in schemas["HostActionStatus"]["properties"]["action"]["enum"]:
    raise SystemExit("disk cleanup is missing from maintenance status actions")

swap_reset = document["paths"]["/api/system/actions/swap-reset"]["post"]
if swap_reset["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ActionMessage":
    raise SystemExit("local host action response schema is missing")
if swap_reset["responses"]["409"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ErrorResponse":
    raise SystemExit("local host action conflict schema is missing")

service_action = document["paths"]["/api/system/actions/service"]["post"]
if service_action["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ServiceControlRequest":
    raise SystemExit("service control request schema is missing")
if set(schemas["ServiceControlRequest"]["properties"]["action"]["enum"]) != {"start", "stop", "restart"}:
    raise SystemExit("service control action enum is not bounded")

process_action = document["paths"]["/api/system/actions/process"]["post"]
if process_action["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ProcessSignalRequest":
    raise SystemExit("process signal request schema is missing")
if set(schemas["ProcessSignalRequest"]["required"]) != {"pid", "startTime", "signal"}:
    raise SystemExit("process signal identity fields are not required")
if set(schemas["ProcessSignalRequest"]["properties"]["signal"]["enum"]) != {"term", "kill"}:
    raise SystemExit("process signal enum is not bounded")

system_info = document["paths"]["/api/system/info"]["get"]
if system_info["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SystemInfo":
    raise SystemExit("system information response schema is missing")
if schemas["SystemInfo"]["properties"]["php"]["type"] != "array" or schemas["SystemInfo"]["properties"]["interfaces"]["type"] != "array":
    raise SystemExit("system information collections are nullable")
if schemas["SystemNetworkInterface"]["properties"]["addrs"]["type"] != "array":
    raise SystemExit("system network addresses are nullable")

system_stats = document["paths"]["/api/system/stats"]["get"]
if system_stats["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SystemStats":
    raise SystemExit("system metrics response schema is missing")
if schemas["SystemStats"]["properties"]["load"].get("minItems") != 3 or schemas["SystemStats"]["properties"]["load"].get("maxItems") != 3:
    raise SystemExit("system load tuple is not fixed to three values")
if schemas["SystemStats"]["properties"]["disk"]["type"] != "array" or schemas["SystemStats"]["properties"]["network"]["type"] != "array":
    raise SystemExit("system metric collections are nullable")

system_services = document["paths"]["/api/system/services"]["get"]
if system_services["responses"]["200"]["content"]["application/json"]["schema"]["items"]["$ref"] != "#/components/schemas/SystemServiceStatus":
    raise SystemExit("system service inventory schema is missing")

service_logs = document["paths"]["/api/system/services/{service}/logs"]["get"]
service_parameter = next((item for item in service_logs["parameters"] if item["name"] == "service" and item["in"] == "path"), None)
lines_parameter = next((item for item in service_logs["parameters"] if item["name"] == "lines" and item["in"] == "query"), None)
if service_parameter is None or "pm2-" not in service_parameter["schema"].get("pattern", ""):
    raise SystemExit("system service log allowlist pattern is missing")
if lines_parameter is None or lines_parameter["schema"] != {"type": "integer", "minimum": 1, "maximum": 500, "default": 100}:
    raise SystemExit("system service log line bounds are missing")
if service_logs["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/SystemServiceLogsResponse":
    raise SystemExit("system service log response schema is missing")

release_status = document["paths"]["/api/system/update"]["get"]
if release_status["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ReleaseUpdateStatus":
    raise SystemExit("release discovery response schema is missing")
if set(schemas["ReleaseUpdateStatus"]["properties"]["status"]["enum"]) != {"not_configured", "unavailable", "healthy"}:
    raise SystemExit("release discovery states are not explicit")
if set(schemas["ReleaseUpdateStatus"]["properties"]["signature_status"]["enum"]) != {"not_configured", "unavailable", "verified"}:
    raise SystemExit("release signature states are not explicit")

release_stage_status = document["paths"]["/api/system/update/stage"]["get"]
if release_stage_status["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ReleaseUpdateStageResponse":
    raise SystemExit("release stage status response schema is missing")
stage_options = schemas["ReleaseUpdateStageResponse"]["properties"]["stage"]["oneOf"]
if {option.get("$ref", option.get("type")) for option in stage_options} != {"#/components/schemas/ReleaseUpdateStage", "null"}:
    raise SystemExit("release stage empty state is not explicit")

release_stage = document["paths"]["/api/system/update/stage"]["post"]
if "requestBody" in release_stage or release_stage["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ReleaseUpdateStage":
    raise SystemExit("release staging empty-body or response contract is invalid")

release_install = document["paths"]["/api/system/update/install"]["post"]
if release_install["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ReleaseUpdateInstallRequest":
    raise SystemExit("release install request schema is missing")
if set(schemas["ReleaseUpdateInstallRequest"]["required"]) != {"stage_id", "version", "confirmed"}:
    raise SystemExit("release install confirmation identity is incomplete")
if schemas["ReleaseUpdateInstallRequest"]["properties"]["confirmed"].get("const") is not True:
    raise SystemExit("release install explicit confirmation is missing")
if release_install["responses"]["413"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ErrorResponse":
    raise SystemExit("release install body-size boundary is missing")

local_domain_paths = document["paths"]
local_domains = local_domain_paths["/api/domains"]
if local_domains["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/LocalDomainList":
    raise SystemExit("local domain inventory response schema is missing")
if set(local_domains["get"]["responses"]) != {"200", "503", "default"}:
    raise SystemExit("local domain inventory availability contract is incomplete")
if schemas["LocalDomainList"]["properties"]["domains"].get("type") != "array":
    raise SystemExit("local domain inventory must always be an array")
if schemas["LocalDomain"]["properties"]["id"].get("type") != "string":
    raise SystemExit("local domain identity must be a stable string")
if set(schemas["LocalDomain"]["properties"]["type"].get("enum", [])) != {"php", "proxy", "static"}:
    raise SystemExit("local domain runtime types are incomplete")

local_domain_create = local_domains["post"]
if local_domain_create["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DomainCreateRequest":
    raise SystemExit("local domain create request schema is missing")
if set(local_domain_create["responses"]) != {"201", "207", "400", "409", "413", "429", "503", "default"}:
    raise SystemExit("local domain create outcome contract is incomplete")
if local_domain_create["responses"]["207"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DomainCreateResult":
    raise SystemExit("local domain partial activation receipt is missing")
local_domain_create_schema = schemas["DomainCreateRequest"]
if local_domain_create_schema.get("additionalProperties") is not False or set(local_domain_create_schema["required"]) != {"domain"}:
    raise SystemExit("local domain create request is not strict")
if set(local_domain_create_schema["properties"]["phpVersion"].get("enum", [])) != {"7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"}:
    raise SystemExit("local domain PHP version allowlist is incomplete")
for port in ("proxyPort", "pm2_port"):
    if local_domain_create_schema["properties"][port].get("maximum") != 65535:
        raise SystemExit(f"local domain {port} bound is missing")

domain_name_pattern = r"^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$"
local_domain_item = local_domain_paths["/api/domains/{id}"]
for method in ("get", "delete"):
    domain_id = next((item for item in local_domain_item[method]["parameters"] if item["name"] == "id"), None)
    if domain_id is None or domain_id["schema"].get("pattern") != domain_name_pattern:
        raise SystemExit(f"local domain {method} identity is not hostname-bound")
if local_domain_item["get"]["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/LocalDomainDetail":
    raise SystemExit("local domain detail response schema is missing")
if set(local_domain_item["get"]["responses"]) != {"200", "400", "404", "503", "default"}:
    raise SystemExit("local domain detail failure contract is incomplete")
if schemas["LocalDomainDetail"]["properties"]["serverNames"].get("type") != "array":
    raise SystemExit("local domain aliases must always be an array")

local_domain_delete = local_domain_item["delete"]
if "requestBody" in local_domain_delete:
    raise SystemExit("local domain delete must require an empty body")
delete_files = next((item for item in local_domain_delete["parameters"] if item["name"] == "deleteFiles"), None)
if delete_files is None or delete_files["schema"] != {"type": "boolean", "default": False}:
    raise SystemExit("local domain file deletion must be one explicit boolean query")
if set(local_domain_delete["responses"]) != {"200", "400", "404", "429", "503", "default"}:
    raise SystemExit("local domain delete outcome contract is incomplete")
if local_domain_delete["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DomainDeleteResult":
    raise SystemExit("local domain delete receipt is missing")

domain_check = local_domain_paths["/api/domains/check"]["post"]
if domain_check["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DomainCheckRequest":
    raise SystemExit("local domain preflight request schema is missing")
if set(domain_check["responses"]) != {"200", "400", "413"}:
    raise SystemExit("local domain preflight outcome contract is incomplete")
if schemas["DomainCheckRequest"].get("additionalProperties") is not False:
    raise SystemExit("local domain preflight request is not strict")
for field in ("dns_zones", "conflicts"):
    if schemas["DomainCheckResult"]["properties"][field].get("type") != "array":
        raise SystemExit(f"local domain preflight {field} must always be an array")

domain_provisioning = local_domain_paths["/api/domains/provisioning"]["get"]
if domain_provisioning["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DomainProvisioningCapabilities":
    raise SystemExit("local domain provisioning capability schema is missing")
if set(schemas["DomainDNSCapability"]["properties"]["status"].get("enum", [])) != {"not_configured", "unavailable", "healthy"}:
    raise SystemExit("local domain DNS capability states are incomplete")

domain_toggle = local_domain_paths["/api/domains/{id}/toggle"]["post"]
if domain_toggle["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DomainToggleRequest":
    raise SystemExit("domain toggle request schema is missing")
if set(schemas["DomainToggleRequest"]["required"]) != {"active"}:
    raise SystemExit("domain toggle desired state is not required")
if set(domain_toggle["responses"]) != {"200", "400", "404", "413", "429", "503", "default"}:
    raise SystemExit("domain toggle outcome contract is incomplete")
if domain_toggle["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DomainToggleResult":
    raise SystemExit("domain toggle receipt is missing")
toggle_id = next((item for item in domain_toggle["parameters"] if item["name"] == "id"), None)
if toggle_id is None or toggle_id["schema"].get("pattern") != domain_name_pattern:
    raise SystemExit("domain toggle identity is not hostname-bound")

cron_create = document["paths"]["/api/cron/jobs"]["post"]
if cron_create["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CronJobCreateRequest":
    raise SystemExit("cron create request schema is missing")
if set(schemas["CronJobCreateRequest"]["required"]) != {"schedule", "command"}:
    raise SystemExit("cron create required fields are incorrect")

cron_update = document["paths"]["/api/cron/jobs/{id}"]["put"]
if cron_update["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/CronJobUpdateRequest":
    raise SystemExit("cron update request schema is missing")
if set(schemas["CronJobUpdateRequest"]["required"]) != {"schedule", "command", "description", "isActive"}:
    raise SystemExit("cron update replacement fields are not required")

firewall_add = document["paths"]["/api/firewall/rules"]["post"]
if firewall_add["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/FirewallRuleCreateRequest":
    raise SystemExit("firewall add request schema is missing")
if set(schemas["FirewallRuleCreateRequest"]["properties"]["action"]["enum"]) != {"allow", "deny", "reject", "limit"}:
    raise SystemExit("firewall action enum is not bounded")
if "source" in schemas["FirewallRuleCreateRequest"]["properties"] or "from" not in schemas["FirewallRuleCreateRequest"]["properties"]:
    raise SystemExit("firewall source field does not match the backend from field")

firewall_delete = document["paths"]["/api/firewall/rules/{number}"]["delete"]
rule_number = next((item for item in firewall_delete["parameters"] if item["name"] == "number" and item["in"] == "path"), None)
if rule_number is None or rule_number["schema"] != {"type": "integer", "minimum": 1}:
    raise SystemExit("firewall delete rule number is not a positive integer")

firewall_toggle = document["paths"]["/api/firewall/toggle"]["post"]
if firewall_toggle["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/FirewallToggleRequest":
    raise SystemExit("firewall toggle request schema is missing")
if set(schemas["FirewallToggleRequest"]["required"]) != {"enable"}:
    raise SystemExit("firewall desired state is not required")

pm2_deploy = document["paths"]["/api/pm2/deploy"]["post"]
if pm2_deploy["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/PM2DeployRequest":
    raise SystemExit("PM2 deploy request schema is missing")
if set(schemas["PM2DeployRequest"]["required"]) != {"name", "script"}:
    raise SystemExit("PM2 deploy required fields are incorrect")
if schemas["PM2DeployRequest"]["properties"]["instances"].get("maximum") != 64:
    raise SystemExit("PM2 deploy instance bound is missing")
if set(schemas["PM2DeployRequest"]["properties"]["exec_mode"]["enum"]) != {"fork", "cluster"}:
    raise SystemExit("PM2 deploy mode enum is not bounded")
if set(schemas["PM2DeployRequest"]["properties"]["node_env"]["enum"]) != {"production", "development"}:
    raise SystemExit("PM2 deploy Node environment enum is not bounded")

pm2_action = document["paths"]["/api/pm2/processes/{id}/{action}"]["post"]
pm2_action_parameter = next((item for item in pm2_action["parameters"] if item["name"] == "action" and item["in"] == "path"), None)
if pm2_action_parameter is None or set(pm2_action_parameter["schema"].get("enum", [])) != {"start", "stop", "restart", "reload", "delete"}:
    raise SystemExit("local PM2 action enum is not bounded")

remote_pm2_action = document["paths"]["/api/nodes/{id}/pm2/{name}/actions/{action}"]["post"]
remote_pm2_action_parameter = next((item for item in remote_pm2_action["parameters"] if item["name"] == "action" and item["in"] == "path"), None)
if remote_pm2_action_parameter is None or set(remote_pm2_action_parameter["schema"].get("enum", [])) != {"start", "stop", "restart", "reload"}:
    raise SystemExit("managed-node PM2 action enum is not bounded")

docker_pull = document["paths"]["/api/docker/images/pull"]["post"]
if docker_pull["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DockerImagePullRequest":
    raise SystemExit("Docker pull request schema is missing")
if set(schemas["DockerImagePullRequest"]["required"]) != {"name"}:
    raise SystemExit("Docker pull image name is not required")

docker_action = document["paths"]["/api/docker/containers/{id}/{action}"]["post"]
docker_action_parameter = next((item for item in docker_action["parameters"] if item["name"] == "action" and item["in"] == "path"), None)
if docker_action_parameter is None or set(docker_action_parameter["schema"].get("enum", [])) != {"start", "stop", "restart", "pause", "unpause", "remove"}:
    raise SystemExit("local Docker action enum is not bounded")

remote_docker_action = document["paths"]["/api/nodes/{id}/containers/{container}/actions/{action}"]["post"]
remote_docker_action_parameter = next((item for item in remote_docker_action["parameters"] if item["name"] == "action" and item["in"] == "path"), None)
if remote_docker_action_parameter is None or set(remote_docker_action_parameter["schema"].get("enum", [])) != {"start", "stop", "restart"}:
    raise SystemExit("managed-node Docker action enum is not bounded")

node_action = document["paths"]["/api/nodes/{id}/actions/{action}"]["post"]
action_parameter = next((item for item in node_action["parameters"] if item["name"] == "action" and item["in"] == "path"), None)
expected_actions = {"memory-optimize", "swap-reset", "temp-clean", "reboot", "reboot-cancel"}
if action_parameter is None or set(action_parameter["schema"].get("enum", [])) != expected_actions:
    raise SystemExit("managed-node host action enum is not the fixed allowlist")
if node_action["responses"]["409"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ErrorResponse":
    raise SystemExit("managed-node action conflict schema is missing")

local_scan = document["paths"]["/api/disk/cleanup/scan"]["get"]
if local_scan["responses"]["200"]["content"]["application/json"]["schema"]["items"]["$ref"] != "#/components/schemas/LocalDiskCleanupTarget":
    raise SystemExit("local disk cleanup scan schema is missing")
local_cleanup = document["paths"]["/api/disk/cleanup/execute"]["post"]
if local_cleanup["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/DiskCleanupExecuteRequest":
    raise SystemExit("local disk cleanup request schema is missing")
local_targets = schemas["DiskCleanupExecuteRequest"]["properties"]["targets"]
if local_targets.get("maxItems") != 20 or local_targets.get("uniqueItems") is not True:
    raise SystemExit("local disk cleanup selection bounds are missing")
if local_cleanup["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/LocalDiskCleanupExecution":
    raise SystemExit("local disk cleanup receipt schema is missing")

managed_scan = document["paths"]["/api/nodes/{id}/disk/cleanup"]["get"]
if managed_scan["responses"]["200"]["content"]["application/json"]["schema"]["items"]["$ref"] != "#/components/schemas/DiskCleanupTarget":
    raise SystemExit("managed disk cleanup scan schema is missing")
managed_cleanup = document["paths"]["/api/nodes/{id}/disk/cleanup"]["post"]
if managed_cleanup["requestBody"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ManagedDiskCleanupExecuteRequest":
    raise SystemExit("managed disk cleanup request schema is missing")
managed_request = schemas["ManagedDiskCleanupExecuteRequest"]
if managed_request["properties"]["confirmed"].get("const") is not True or managed_request["properties"]["targets"].get("maxItems") != 4:
    raise SystemExit("managed disk cleanup confirmation or selection bound is missing")
if managed_cleanup["responses"]["200"]["content"]["application/json"]["schema"]["$ref"] != "#/components/schemas/ManagedDiskCleanupExecution":
    raise SystemExit("managed disk cleanup receipt schema is missing")
if set(schemas["DiskCleanupResult"]["properties"]["status"]["enum"]) != {"ok", "error"}:
    raise SystemExit("disk cleanup result status enum is invalid")
PY

printf '%s\n' "OpenAPI documentation test: OK ($expected_routes routes)"
