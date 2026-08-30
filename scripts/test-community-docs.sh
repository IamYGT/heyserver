#!/usr/bin/env sh
set -eu

repo_root=${HSERVER_ROOT:-$(CDPATH=; export CDPATH; cd -- "$(dirname -- "$0")/.." && pwd)}

fail() {
  printf '%s\n' "community docs check failed: $*" >&2
  exit 1
}

for required in \
  SUPPORT.md \
  CODE_OF_CONDUCT.md \
  SECURITY.md \
  GOVERNANCE.md \
  MAINTAINERS.md \
  CONTRIBUTING.md \
  .github/ISSUE_TEMPLATE/config.yml \
  .github/CODEOWNERS \
  .github/ISSUE_TEMPLATE/bug_report.yml \
  .github/ISSUE_TEMPLATE/feature_request.yml \
  .github/ISSUE_TEMPLATE/support_question.yml \
  .github/ISSUE_TEMPLATE/integration_proposal.yml \
  docs/installation-guide.md \
  docs/troubleshooting.md \
  docs/extension-boundary.md
 do
  [ -f "$repo_root/$required" ] || fail "missing referenced repository file: $required"
done

# Every relative Markdown link in the owned community documents must resolve to
# a file in this checkout. External URLs and fragment-only links are intentionally
# excluded; the issue-form checks below validate those direct targets separately.
for document in \
  SUPPORT.md \
  CODE_OF_CONDUCT.md \
  SECURITY.md \
  GOVERNANCE.md \
  MAINTAINERS.md
 do
  while IFS= read -r target; do
    case "$target" in
      ''|'#'*|http://*|https://*|mailto:*|tel:*) continue ;;
    esac
    target=${target%%\#*}
    target=${target%%\?*}
    [ -n "$target" ] || continue
    case "$target" in
      /*) continue ;;
    esac
    base_dir=$(dirname -- "$document")
    candidate=$repo_root/$base_dir/$target
    [ -e "$candidate" ] || fail "$document references missing repository target: $target"
  done <<LINKS
$(grep -oE '\]\([^)]*\)' "$repo_root/$document" | sed -e 's/^](//' -e 's/)$//' || true)
LINKS
done

# The support guide must point directly to every existing issue form rather than
# telling contributors to search for a template manually.
support_doc=$repo_root/SUPPORT.md
for form in bug_report.yml feature_request.yml support_question.yml integration_proposal.yml; do
  form_path=.github/ISSUE_TEMPLATE/$form
  [ -f "$repo_root/$form_path" ] || fail "missing issue form target: $form_path"
  grep -Fq "($form_path)" "$support_doc" || fail "support guide does not link issue form source: $form_path"
  grep -Fq "https://github.com/IamYGT/heyserver/issues/new?template=$form" "$support_doc" || \
    fail "support guide does not link direct issue form: $form"
done

# Keep the private escalation routes explicit and mutually consistent.
security_advisory=https://github.com/IamYGT/heyserver/security/advisories/new
for document in SUPPORT.md CODE_OF_CONDUCT.md SECURITY.md MAINTAINERS.md; do
  grep -Fq "$security_advisory" "$repo_root/$document" || \
    fail "$document is missing the private security advisory route"
done
for document in SUPPORT.md SECURITY.md GOVERNANCE.md MAINTAINERS.md; do
  grep -Fq 'CODE_OF_CONDUCT.md' "$repo_root/$document" || \
    fail "$document is missing the Code of Conduct escalation reference"
done

# Role ownership must remain role-based rather than a fabricated personal roster.
grep -Fiq 'role and area map' "$repo_root/MAINTAINERS.md" || fail 'maintainer area map heading is missing'
grep -Fq 'without inventing identities' "$repo_root/MAINTAINERS.md" || fail 'maintainer map identity boundary is missing'
grep -Fq "GitHub's repository permissions" "$repo_root/GOVERNANCE.md" || fail 'governance membership authority is missing'

grep -Fq 'Security → Report a vulnerability' "$repo_root/SECURITY.md" || \
  fail 'security advisory UI path is missing'
grep -Fq 'public issue' "$repo_root/CODE_OF_CONDUCT.md" || \
  fail 'Code of Conduct public-report boundary is missing'

grep -Fq 'blank_issues_enabled: false' "$repo_root/.github/ISSUE_TEMPLATE/config.yml" || \
  fail 'issue-form configuration no longer disables unstructured public issues'

grep -Fq 'https://github.com/IamYGT/heyserver/security/advisories/new' \
  "$repo_root/.github/ISSUE_TEMPLATE/config.yml" || \
  fail 'issue-form configuration security route differs from policy docs'

grep -Fxq '* @IamYGT' "$repo_root/.github/CODEOWNERS" || \
  fail 'CODEOWNERS must assign global ownership to @IamYGT'

printf '%s\n' 'community docs and issue-form link check: OK'
