# Everstack editions

Everstack is open core. This repository is the Apache-2.0 Community Edition.
Everstack Cloud and the Enterprise features are separate commercial offerings,
and their source is not in this repository. [LICENSING.md](./LICENSING.md)
states the licence terms.

## Community Edition

Community Edition is the code in this repository. You can run, modify,
redistribute, and embed it under the terms of the
[Apache 2.0 license](./LICENSE). It includes the core gateway, agent runtime,
workflows, sandboxes, memory, MCP, observability, APIs, CLI, and self-hosted
admin application. The Community Edition server binary is built with
`go build .` with no tags, or with `-tags ce`.

Community Edition is operated by you. Your model-provider credentials are used
to call those providers directly, provider inference is billed by those
providers, and the Everstack model-usage charge is $0.

## Everstack Cloud

Everstack Cloud is the managed service. Everstack operates the control plane,
databases, upgrades, backups, regional infrastructure, and supported runtime
capacity. Its identity, billing, licensing, and fleet-management services are
commercial software and are not included in this repository.

## Enterprise

The enterprise build of the server adds licence activation and enforcement and
the wiring for managed instances. It is commercial software, available under a
commercial agreement with Everstack, and its source is not included in this
repository.

## Feature requests and boundaries

Use [GitHub Discussions](https://github.com/everstacklabs/everstack/discussions)
for questions about edition boundaries. Bugs in Community Edition belong in
[GitHub Issues](https://github.com/everstacklabs/everstack/issues). Commercial
questions can be sent to [sales@everstack.ai](mailto:sales@everstack.ai).
