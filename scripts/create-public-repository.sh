#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
destination=
author_name=
author_email=

usage() {
  cat >&2 <<'EOF'
Usage: create-public-repository.sh DESTINATION --author-name NAME --author-email EMAIL

Create an atomic, one-commit Git repository from HServer's audited public source
snapshot. The command never configures a remote or pushes the repository.
EOF
}

while (($#)); do
  case "$1" in
    --author-name)
      (($# >= 2)) || { usage; exit 2; }
      author_name=$2
      shift 2
      ;;
    --author-email)
      (($# >= 2)) || { usage; exit 2; }
      author_email=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --*)
      printf 'Unknown option: %s\n' "$1" >&2
      usage
      exit 2
      ;;
    *)
      if [[ -n "$destination" ]]; then
        echo "Only one destination may be provided." >&2
        usage
        exit 2
      fi
      destination=$1
      shift
      ;;
  esac
done

if [[ -z "$destination" || -z "$author_name" || -z "$author_email" ]]; then
  usage
  exit 2
fi
if [[ "$author_name" == *$'\n'* || "$author_name" == *$'\r'* ||
      "$author_email" == *$'\n'* || "$author_email" == *$'\r'* ]]; then
  echo "Author identity must not contain line breaks." >&2
  exit 2
fi
if ((${#author_name} > 128)) || [[ ! "$author_name" =~ [^[:space:]] ]]; then
  echo "Author name must contain non-whitespace text and be at most 128 characters." >&2
  exit 2
fi
if ((${#author_email} > 254)) || [[ ! "$author_email" =~ ^[^[:space:]@]+@[^[:space:]@]+$ ]]; then
  echo "Author email must be a single email-like identity of at most 254 characters." >&2
  exit 2
fi
if [[ -e "$destination" ]]; then
  echo "Refusing to overwrite existing destination: $destination" >&2
  exit 1
fi

parent=$(dirname "$destination")
leaf=$(basename "$destination")
if [[ "$leaf" == "." || "$leaf" == ".." || -z "$leaf" ]]; then
  echo "Destination must name a new repository directory." >&2
  exit 2
fi
mkdir -p "$parent"
parent=$(cd "$parent" && pwd -P)
destination="$parent/$leaf"
case "$destination/" in
  "$repo_root/"*)
    echo "Refusing to create the public repository inside the private source tree." >&2
    exit 1
    ;;
esac

for command in git tar; do
  command -v "$command" >/dev/null 2>&1 || {
    printf 'Required command is unavailable: %s\n' "$command" >&2
    exit 1
  }
done

staging=$(mktemp -d "$parent/.hserver-public-repository.XXXXXX")
snapshot="$staging/repository"
cleanup() {
  if [[ -d "$staging" ]]; then
    find "$staging" -xdev -depth -delete
  fi
}
trap cleanup EXIT INT TERM

"$repo_root/scripts/export-public-source.sh" "$snapshot" >"$staging/export.log"
git -C "$snapshot" init -q --initial-branch=main
git -C "$snapshot" config user.name "$author_name"
git -C "$snapshot" config user.email "$author_email"
git -C "$snapshot" add --all
git -C "$snapshot" -c commit.gpgsign=false commit -qm "Publish HServer community source"

if [[ $(git -C "$snapshot" rev-list --all --count) != 1 ||
      $(git -C "$snapshot" rev-list --max-parents=0 --all --count) != 1 ]]; then
  echo "Public repository history is not exactly one root commit." >&2
  exit 1
fi
if [[ $(git -C "$snapshot" branch --show-current) != main ]]; then
  echo "Public repository did not initialize on main." >&2
  exit 1
fi
if [[ -n "$(git -C "$snapshot" remote)" ]]; then
  echo "Public repository unexpectedly contains a remote." >&2
  exit 1
fi
if [[ -n "$(git -C "$snapshot" status --porcelain)" ]]; then
  echo "Public repository is dirty after its initial commit." >&2
  exit 1
fi
git -C "$snapshot" fsck --strict --no-dangling >/dev/null
"$snapshot/scripts/test-public-docs.sh" >/dev/null

commit=$(git -C "$snapshot" rev-parse HEAD)
mv -- "$snapshot" "$destination"
trap - EXIT INT TERM
find "$staging" -xdev -depth -delete

printf 'Public repository created: %s\n' "$destination"
printf 'Initial commit: %s\n' "$commit"
printf 'Remote configured: no\n'

