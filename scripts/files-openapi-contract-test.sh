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

def response_schema(path, method, status):
    return paths[path][method]["responses"][status]["content"]["application/json"]["schema"]

def assert_ref(schema, name):
    assert schema == {"$ref": f"#/components/schemas/{name}"}, schema

def assert_error_response(operation):
    assert_ref(operation["responses"]["400"]["content"]["application/json"]["schema"], "ErrorResponse")

list_operation = paths["/api/files"]["get"]
assert list_operation["x-hserver-access"] == "Authenticated"
assert list_operation["parameters"] == [{
    "name": "path",
    "in": "query",
    "required": False,
    "description": "Installation-owned file or directory path.",
    "schema": {"type": "string"},
}]
assert_ref(response_schema("/api/files", "get", "200"), "FileListResponse")
assert_error_response(list_operation)
assert "configured file roots" in list_operation["description"]

read_operation = paths["/api/files/read"]["get"]
assert read_operation["parameters"][0]["name"] == "path"
assert read_operation["parameters"][0]["in"] == "query"
assert read_operation["parameters"][0]["required"] is True
assert read_operation["parameters"][0]["schema"] == {"type": "string", "minLength": 1}
assert_ref(response_schema("/api/files/read", "get", "200"), "FileReadResponse")
assert_error_response(read_operation)

write_operation = paths["/api/files/write"]["put"]
assert_ref(write_operation["requestBody"]["content"]["application/json"]["schema"], "FileWriteRequest")
assert write_operation["requestBody"]["required"] is True
assert_ref(response_schema("/api/files/write", "put", "200"), "FileWriteResponse")
assert_error_response(write_operation)

create_operation = paths["/api/files/create"]["post"]
assert_ref(create_operation["requestBody"]["content"]["application/json"]["schema"], "FileCreateRequest")
assert_ref(response_schema("/api/files/create", "post", "201"), "FileCreateResponse")
assert_error_response(create_operation)

delete_operation = paths["/api/files"]["delete"]
assert delete_operation["parameters"][0]["required"] is True
assert delete_operation["parameters"][0]["schema"] == {"type": "string", "minLength": 1}
assert_ref(response_schema("/api/files", "delete", "200"), "FileDeleteResponse")
assert_error_response(delete_operation)

rename_operation = paths["/api/files/rename"]["post"]
assert_ref(rename_operation["requestBody"]["content"]["application/json"]["schema"], "FileRenameRequest")
assert_ref(response_schema("/api/files/rename", "post", "200"), "FileRenameResponse")
assert_error_response(rename_operation)
assert "src/dst" in rename_operation["description"]
assert "old_path/new_path" in rename_operation["description"]

entry = schemas["FileEntry"]
assert entry["type"] == "object"
assert entry["additionalProperties"] is False
assert entry["required"] == ["name", "path", "type", "size", "permissions", "owner", "group", "modified"]
assert entry["properties"]["type"]["enum"] == ["file", "directory", "symlink"]
assert entry["properties"]["size"] == {"type": "integer", "format": "int64", "minimum": 0}
assert entry["properties"]["modified"] == {"type": "string", "format": "date-time"}
assert "target" in entry["properties"] and "target" not in entry["required"]

listing = schemas["FileListResponse"]
assert listing["type"] == "object"
assert listing["additionalProperties"] is False
assert listing["required"] == ["entries"]
assert listing["properties"]["entries"] == {"type": "array", "items": {"$ref": "#/components/schemas/FileEntry"}}
assert listing["properties"]["roots"]["type"] == "array"
assert listing["properties"]["path"]["type"] == "string"

write_request = schemas["FileWriteRequest"]
assert write_request["required"] == ["path"]
assert "content" not in write_request["required"]
assert write_request["properties"]["content"]["type"] == "string"
assert write_request.get("additionalProperties") is not False

create_request = schemas["FileCreateRequest"]
assert create_request["required"] == ["path"]
assert "type" not in create_request["required"]
assert create_request["properties"]["type"]["enum"] == ["file", "directory", "dir"]
assert create_request["properties"]["type"]["default"] == "file"
assert create_request.get("additionalProperties") is not False

rename_request = schemas["FileRenameRequest"]
assert rename_request.get("additionalProperties") is not False
assert set(rename_request["properties"]) == {"src", "dst", "old_path", "new_path"}
assert {tuple(branch["required"]) for branch in rename_request["anyOf"]} == {
    ("src", "dst"),
    ("old_path", "new_path"),
    ("src", "new_path"),
    ("old_path", "dst"),
}

for name, status in (
    ("FileWriteResponse", "ok"),
    ("FileCreateResponse", "created"),
    ("FileDeleteResponse", "deleted"),
):
    schema = schemas[name]
    assert schema["required"] == ["status", "path"]
    assert schema["properties"]["status"]["const"] == status

rename_response = schemas["FileRenameResponse"]
assert rename_response["required"] == ["status", "src", "dst"]
assert rename_response["properties"]["status"]["const"] == "renamed"

read_response = schemas["FileReadResponse"]
assert read_response["required"] == ["path", "content"]

for schema_name in (
    "FileEntry", "FileListResponse", "FileReadResponse", "FileWriteRequest",
    "FileWriteResponse", "FileCreateRequest", "FileCreateResponse",
    "FileDeleteResponse", "FileRenameRequest", "FileRenameResponse",
):
    assert schema_name in schemas

assert "### GET /api/files" in api_reference
assert "### PUT /api/files/write" in api_reference
assert "### POST /api/files/rename" in api_reference
assert "old_path" in api_reference and "new_path" in api_reference
assert "compatibility alias `type=dir`" in api_reference

print("files OpenAPI contract checks passed")
PY
