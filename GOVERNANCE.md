# Governance

Everstack is currently a maintainer-led open-source project.

## Roles

- **Users** run Everstack and participate in issues and discussions.
- **Contributors** submit documentation, tests, fixes, integrations, and design
  proposals.
- **Maintainers** review changes, manage releases, uphold security boundaries,
  and decide what enters the project.

Maintainer status is earned through sustained, constructive contributions and
sound judgment. Existing maintainers invite new maintainers; there is no paid
or automatic path to the role.

## Decisions

Small and reversible changes are decided through pull-request review. Changes
to public APIs, persistence formats, security boundaries, or major architecture
should begin as a GitHub Discussion or design issue before implementation.
Maintainers seek consensus, but retain final responsibility for project scope,
security, compatibility, and release quality.

Commercial roadmap commitments do not override the Apache 2.0 license of code
already published in this repository. Edition boundaries are documented in
[EDITIONS.md](./EDITIONS.md).

## Repository synchronization

During the Community Edition/cloud split, this repository is published from an
explicitly allowlisted core in Everstack's development monorepo. It remains the
normal issue, review, and pull-request surface for Community Edition.
Maintainers port accepted public changes into the shared core before the next
projection. The publication automation compares both trees and fails rather
than overwrite a path changed by the community. Public commits and attribution
remain in this repository's history.

## Conduct and security

Participation is governed by [CODE_OF_CONDUCT.md](./CODE_OF_CONDUCT.md).
Security reports follow [SECURITY.md](./SECURITY.md), not public issue threads.
