#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
frontend_dist=${HSERVER_FRONTEND_DIST:-$repo_root/web/dist}
embed_dir=${HSERVER_EMBED_DIR:-$repo_root/cmd/hserver/web/dist}

if [[ ! -d "$frontend_dist" ]]; then
  printf 'Frontend build output is missing: %s\n' "$frontend_dist" >&2
  printf 'Run the frontend build before invoking the Go build wrapper.\n' >&2
  exit 1
fi
if [[ ! -d "$embed_dir" ]]; then
  printf 'Go embed directory is missing: %s\n' "$embed_dir" >&2
  exit 1
fi
if [[ "$#" -eq 0 ]]; then
  printf 'Usage: %s GO_BUILD_ARGUMENT...\n' "$0" >&2
  exit 2
fi

overlay=$(mktemp "${TMPDIR:-/tmp}/hserver-go-overlay.XXXXXX.json")
trap 'rm -f "$overlay"' EXIT INT TERM

python3 - "$embed_dir" "$frontend_dist" "$overlay" <<'PY'
from __future__ import annotations

import json
import sys
from pathlib import Path

embed_dir = Path(sys.argv[1]).resolve()
frontend_dist = Path(sys.argv[2]).resolve()
output = Path(sys.argv[3])

def files_under(root: Path) -> dict[str, Path]:
    files: dict[str, Path] = {}
    for path in root.rglob("*"):
        if path.is_symlink():
            raise SystemExit(f"Frontend overlay contains a non-regular file: {path}")
        if path.is_dir():
            continue
        if not path.is_file():
            raise SystemExit(f"Frontend overlay contains a non-regular file: {path}")
        files[str(path.relative_to(root))] = path
    return files

embedded = files_under(embed_dir)
frontend = files_under(frontend_dist)
replace: dict[str, str] = {}

# Replace every committed embed file with the freshly built asset, and mark
# removed assets for deletion. The latter keeps go:embed's directory listing
# identical to the generated frontend rather than retaining stale chunks.
for relative, path in embedded.items():
    destination = embed_dir / relative
    replacement = frontend.get(relative)
    replace[str(destination)] = str(replacement) if replacement else ""

# Vite content hashes can add or remove chunks. Go overlays support both
# replacement and addition, so the generated directory can be embedded without
# copying over the tracked source checkout.
for relative, path in frontend.items():
    if relative not in embedded:
        replace[str(embed_dir / relative)] = str(path)

output.write_text(json.dumps({"Replace": replace}, sort_keys=True) + "\n", encoding="utf-8")
PY

cd "$repo_root"
exec go build -overlay "$overlay" "$@"
