#!/bin/bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
go test ./internal/api/... -run TestRouteManifest -count=1 -timeout 60s
