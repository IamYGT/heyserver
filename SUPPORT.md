# HServer Support

HServer is a community-maintained, self-hosted project. The fastest support
request includes a reproducible symptom and the smallest relevant diagnostic
output.

## Before opening an issue

1. Confirm the host matches the current native-install target in the
   [installation guide](docs/installation-guide.md).
2. For release installations, run `sudo ./doctor.sh installed`. From a source
   checkout, run `make doctor`. For authenticated panel or managed-node
   connectivity, create a protected report with
   `hserverctl doctor --output ./hserver-doctor.json`.
3. Check the [troubleshooting guide](docs/troubleshooting.md) and existing
   issues.
4. Reproduce the problem on the latest tagged release when practical.

## Choose the right channel

Use the public form that matches the outcome you need. These links open the
repository's actual issue forms; their checked-in definitions are linked in the
last column so a contributor can inspect the required fields before submitting.

| Need | Action | Form definition |
| --- | --- | --- |
| A reproducible product defect | [Open a bug report](https://github.com/IamYGT/heyserver/issues/new?template=bug_report.yml) | [bug_report.yml](.github/ISSUE_TEMPLATE/bug_report.yml) |
| A provider-neutral workflow or capability | [Request a feature](https://github.com/IamYGT/heyserver/issues/new?template=feature_request.yml) | [feature_request.yml](.github/ISSUE_TEMPLATE/feature_request.yml) |
| Installation or usage help | [Ask a support question](https://github.com/IamYGT/heyserver/issues/new?template=support_question.yml) | [support_question.yml](.github/ISSUE_TEMPLATE/support_question.yml) |
| A provider adapter or bounded server-management integration | [Propose an integration](https://github.com/IamYGT/heyserver/issues/new?template=integration_proposal.yml) | [integration_proposal.yml](.github/ISSUE_TEMPLATE/integration_proposal.yml) |

Read the [community extension boundary](docs/extension-boundary.md) before
using the integration form. A feature request is the right channel when the
proposal is not an in-tree provider adapter or bounded integration.

Do **not** use a public issue, pull request, or support form for a vulnerability,
suspected credential exposure, or real server inventory. Use the [private
security advisory](https://github.com/IamYGT/heyserver/security/advisories/new)
following [SECURITY.md](SECURITY.md). For a conduct concern, use the private
route in the [Code of Conduct](CODE_OF_CONDUCT.md).

## Safe diagnostic output

Include HServer version, Ubuntu version, CPU architecture, install method,
affected page or API route, exact error text, and the relevant service state.
Do not include secrets or full production inventory. The doctor is designed to
report installation health without printing configuration values.

Useful commands:

```bash
sudo ./doctor.sh installed
hserverctl doctor --output ./hserver-doctor.json
sudo systemctl status hserver --no-pager
sudo journalctl -u hserver --since "10 minutes ago" --no-pager
```

For managed nodes, also include the agent version, advertised capability name,
and task status. Use `--node NODE --require-capability NAME` with the CLI doctor
when practical. The generated file is mode `0600` and omits account name,
account email, and bearer-token data, but retains the selected server URL and
node identity; review those operational values before uploading it. Do not
include the enrollment token or agent token file.

## Participation

Support discussions, issue forms, and pull requests follow the project's
[Code of Conduct](CODE_OF_CONDUCT.md). Maintainer responsibilities and
escalation ownership are documented in [GOVERNANCE.md](GOVERNANCE.md) and the
[maintainer area map](MAINTAINERS.md).
