# Heyserver Governance

Heyserver is currently a pre-1.0, maintainer-led open-source project. This file
documents how contributors can earn responsibility, how material decisions are
made, and who has release authority without implying that a larger foundation
or committee already exists.

Start here:

- [Contributing guide](CONTRIBUTING.md) — setup, pull-request, and focused-check
  expectations.
- [Support channels](SUPPORT.md) — direct bug, feature, support, and integration
  forms.
- [Maintainer area map](MAINTAINERS.md) — accountability by project area,
  intentionally without personal identities.
- [Code of Conduct](CODE_OF_CONDUCT.md) — private conduct reporting and
  enforcement.
- [Security Policy](SECURITY.md) — private vulnerability reporting and incident
  escalation.

## Project roles

- **Users** operate Heyserver and provide reproducible feedback.
- **Contributors** submit issues, documentation, tests, designs, or code under
  the project license.
- **Reviewers** are recurring contributors trusted to review an area. Review
  permission does not grant release or repository administration access.
- **Area maintainers** are maintainers accountable for one or more areas in the
  [maintainer area map](MAINTAINERS.md). They coordinate review, compatibility,
  documentation, and focused acceptance evidence for that area.
- **Maintainers** can merge changes in one or more areas and are responsible for
  keeping CI, documentation, migrations, and compatibility boundaries intact.
- **Release maintainers** may prepare and publish an official release when they
  have release permission; they must meet the release gates below.
- **Security response maintainers** receive and coordinate private vulnerability
  reports. They limit access, coordinate fixes, and keep sensitive details
  private until disclosure is safe. This role is held by a maintainer and does
  not grant extra authority to merge unrelated changes.
- The **lead maintainer** owns repository administration, appoints or removes
  maintainers, resolves decisions that cannot reach consensus, coordinates
  conduct appeals, and authorizes official releases.

The current maintainers are the people with repository merge or administration
access. GitHub's repository permissions are the authoritative membership record;
the area map defines responsibilities without inventing or implying personal
identities. If an area has no separately assigned maintainer, the lead
maintainer remains accountable until coverage is recorded.

## Maintainer and area accountability

The [maintainer area map](MAINTAINERS.md) is the canonical role-to-area map. It
covers core panel/API and storage, web and CLI, agent and protocol boundaries,
host/provider integrations, documentation/community workflows, releases, and
private security or conduct escalation. A change may involve more than one
area; all affected maintainers should be included in review.

No area maintainer may approve a change while personally involved in a conduct
report or material conflict. The lead maintainer assigns an uninvolved
maintainer for that decision and records the resulting ownership gap or
recusal. Private security reports follow [SECURITY.md](SECURITY.md), not a
public issue or normal review thread.

## Earning and losing responsibility

A contributor may become a reviewer or area maintainer after a sustained record
of technically sound, provider-neutral contributions and constructive reviews.
Existing maintainers nominate candidates in a public issue or pull request; the
lead maintainer makes the final appointment and records the result publicly.

Maintainers may step down at any time. Access may be removed for prolonged
inactivity, repeated violation of the project contract or Code of Conduct, an
unresolved conflict of interest, or actions that put users' installations at
risk. Except for urgent access removal, the reason and resulting ownership gap
should be documented publicly.

## Decision process

Routine fixes and compatible improvements are decided through normal pull
request review. The author explains the operator outcome, tests the nearest
changed boundary, and updates affected public documentation.

Open a design issue before implementation when a proposal materially changes:

- public APIs, persistent schemas, or upgrade and rollback behavior;
- panel-to-agent protocol, capabilities, or trust boundaries;
- native host permissions, destructive operations, or secret handling;
- supported operating systems or installation lifecycle;
- licensing, governance, or official release policy.

The preferred outcome is rough consensus based on reproducible evidence and the
provider-neutral product contract. When reasonable objections remain, a
maintainer records the alternatives and trade-offs. The lead maintainer makes
the final decision so a disagreement cannot stall the project indefinitely.
That decision can be revisited when new evidence is available.

## Provider and commercial boundaries

No infrastructure provider, managed-service customer, or sponsor receives an
undocumented core capability or private runtime default. Provider-specific
features must sit behind an explicit adapter or capability and degrade honestly
when absent. Maintainers disclose a material financial or employment interest
when reviewing a decision that could favor one provider.

Commercial hosting, support, and managed operations may fund development, but
the Apache-licensed core, public contribution process, and published release
artifacts remain governed here.

## Releases

Only a maintainer with release permission may publish an official Heyserver
release. A release must come from the public repository, use the documented
version contract, pass the required public CI gates, include checksummed
artifacts, and provide upgrade and rollback notes for stateful changes.

Pre-1.0 releases may change quickly, but breaking changes still require an
explicit migration or compatibility note. Security releases follow
[SECURITY.md](SECURITY.md) and may keep vulnerability details private until a
fix is available.

## Changing this document

Governance changes use a public pull request and the material-decision process
above. The pull request must explain whose authority or contribution path
changes. Silent governance changes through unrelated code reviews are not
accepted.
