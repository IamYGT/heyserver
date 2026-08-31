#!/usr/bin/env sh
set -eu

root_dir=${HSERVER_ROOT:-$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)}
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT INT TERM

version=v0.0.0-metadata-test
commit=abc1234
build_date=2026-08-26T00:00:00Z
module=github.com/IamYGT/heyserver

(
  cd "$root_dir"
  CGO_ENABLED=1 go build \
    -ldflags "-X $module/internal/config.Version=$version -X $module/internal/config.BuildCommit=$commit -X $module/internal/config.BuildDate=$build_date" \
    -o "$work_dir/hserver-panel" ./cmd/hserver
  CGO_ENABLED=0 go build \
    -ldflags "-X main.agentVersion=$version" \
    -o "$work_dir/hserver-agent" ./cmd/hserver-agent
)

panel_output=$($work_dir/hserver-panel --version)
agent_output=$($work_dir/hserver-agent --version)

[ "$panel_output" = "hserver-panel $version (commit $commit, built $build_date)" ]
[ "$agent_output" = "hserver-agent $version" ]

full_commit=$(git -C "$root_dir" rev-parse HEAD)
default_commit=$(
  make -s --no-print-directory -C "$root_dir" -f Makefile -f - print-build-commit <<'MAKE'
.PHONY: print-build-commit
print-build-commit:
	@printf '%s\n' '$(BUILD_COMMIT)'
MAKE
)
if [ "$default_commit" != "$full_commit" ]; then
  # A shared checkout may receive a commit between the two probes; retry once
  # so the assertion compares values from the same source revision.
  full_commit=$(git -C "$root_dir" rev-parse HEAD)
  default_commit=$(
    make -s --no-print-directory -C "$root_dir" -f Makefile -f - print-build-commit <<'MAKE'
.PHONY: print-build-commit
print-build-commit:
	@printf '%s\n' '$(BUILD_COMMIT)'
MAKE
  )
fi
[ "$default_commit" = "$full_commit" ] || {
  echo "default build commit is not the full source commit" >&2
  exit 1
}

# A frontend build must be usable without copying over the tracked embed tree.
# Build once from a clean fixture with an added asset and then repeat after a
# tracked source edit. The first binary must retain clean Git metadata; the
# second must preserve the dirty marker rather than hiding it.
clean_root="$work_dir/clean-root"
mkdir -p "$clean_root"
git -C "$root_dir" archive --format=tar "$full_commit" | tar -xf - -C "$clean_root"
install -m 0755 "$root_dir/scripts/go-build-with-frontend.sh" \
  "$clean_root/scripts/go-build-with-frontend.sh"
git -C "$clean_root" init -q --initial-branch=main
git -C "$clean_root" config user.name "Heyserver Metadata Test"
git -C "$clean_root" config user.email "metadata-test@example.com"
git -C "$clean_root" add .
git -C "$clean_root" commit -qm "seed build metadata fixture"

clean_commit=$(git -C "$clean_root" rev-parse HEAD)
frontend_dist="$clean_root/web/dist"
mkdir -p "$frontend_dist" "$clean_root/bin"
cp -R "$clean_root/cmd/hserver/web/dist/." "$frontend_dist/"
printf '%s\n' '<!-- overlay fixture -->' >>"$frontend_dist/index.html"
printf '%s\n' 'overlay fixture asset' >"$frontend_dist/assets/build-metadata-overlay.js"

HSERVER_FRONTEND_DIST="$frontend_dist" CGO_ENABLED=0 \
  "$clean_root/scripts/go-build-with-frontend.sh" \
  -ldflags "-X $module/internal/config.Version=$version -X $module/internal/config.BuildCommit=$clean_commit -X $module/internal/config.BuildDate=$build_date" \
  -o "$clean_root/bin/hserver-panel" ./cmd/hserver

[ "$($clean_root/bin/hserver-panel --version)" = "hserver-panel $version (commit $clean_commit, built $build_date)" ] || {
  echo "clean frontend overlay build changed explicit release identity" >&2
  exit 1
}
[ -z "$(git -C "$clean_root" status --porcelain)" ] || {
  echo "clean frontend overlay build changed its source checkout" >&2
  git -C "$clean_root" status --short >&2
  exit 1
}
clean_build_info=$(go version -m "$clean_root/bin/hserver-panel")
printf '%s\n' "$clean_build_info" | grep -Eq '^[[:space:]]+build[[:space:]]+vcs\.revision='"$clean_commit"'$' || {
  echo "clean frontend overlay build lost its source revision" >&2
  exit 1
}
printf '%s\n' "$clean_build_info" | grep -Eq '^[[:space:]]+build[[:space:]]+vcs\.modified=false$' || {
  echo "clean frontend overlay build is not marked clean" >&2
  exit 1
}
if printf '%s\n' "$clean_build_info" | grep -Eq '^[[:space:]]+mod[[:space:]]+.*\+dirty([[:space:]]|$)'; then
  echo "clean frontend overlay build has a dirty module version" >&2
  exit 1
fi
grep -aFq 'overlay fixture asset' "$clean_root/bin/hserver-panel" || {
  echo "clean frontend overlay asset was not embedded" >&2
  exit 1
}

printf '%s\n' 'fixture dirty source' >>"$clean_root/README.md"
HSERVER_FRONTEND_DIST="$frontend_dist" CGO_ENABLED=0 \
  "$clean_root/scripts/go-build-with-frontend.sh" \
  -ldflags "-X $module/internal/config.Version=$version -X $module/internal/config.BuildCommit=$clean_commit -X $module/internal/config.BuildDate=$build_date" \
  -o "$clean_root/bin/hserver-panel-dirty" ./cmd/hserver
dirty_build_info=$(go version -m "$clean_root/bin/hserver-panel-dirty")
printf '%s\n' "$dirty_build_info" | grep -Eq '^[[:space:]]+build[[:space:]]+vcs\.modified=true$' || {
  echo "dirty frontend overlay build lost its dirty marker" >&2
  exit 1
}
printf '%s\n' "$dirty_build_info" | grep -Eq '^[[:space:]]+mod[[:space:]]+.*\+dirty([[:space:]]|$)' || {
  echo "dirty frontend overlay build is missing its dirty module version" >&2
  exit 1
}

python3 - "$root_dir/.github/workflows/ci.yml" "$root_dir/Makefile" <<'PY'
from pathlib import Path
import sys

workflow = Path(sys.argv[1]).read_text(encoding="utf-8")
makefile = Path(sys.argv[2]).read_text(encoding="utf-8")
if workflow.count('echo "commit=$(git rev-parse HEAD)"') != 2:
    raise SystemExit("CI build/release commit outputs are not full source commits")
if "BUILD_COMMIT=${GITHUB_SHA}" not in workflow:
    raise SystemExit("native upgrade acceptance does not embed the full source commit")
for shortened in ("git rev-parse --short HEAD", "BUILD_COMMIT=${GITHUB_SHA::7}"):
    if shortened in workflow:
        raise SystemExit(f"CI still embeds shortened release provenance: {shortened}")
for required in (
    "build: frontend",
    "scripts/go-build-with-frontend.sh",
    "release: release-check",
    "git status --porcelain=v1 --untracked-files=all",
):
    if required not in makefile:
        raise SystemExit(f"Makefile is missing clean frontend build provenance: {required}")
if "build: sync-dist" in makefile:
    raise SystemExit("canonical build still rewrites the tracked frontend embed tree")
PY

printf '%s\n' 'build metadata injection test: OK'
