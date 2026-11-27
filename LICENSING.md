# Licensing

This repository is the Everstack Community Edition. It is open source under the
[Apache License 2.0](./LICENSE), and that licence covers every file here. No
path in this repository is under different terms.

Third-party components keep their own licences. See [NOTICE](./NOTICE).

## What is not in this repository

Everstack Cloud (the managed service) and the Enterprise features are separate
commercial offerings. Their source is not part of this repository and the
Apache License 2.0 here does not apply to them. That covers:

- the hosted identity, billing, licensing and fleet-management services;
- the Everstack Cloud dashboard;
- the enterprise build of the server, which adds licence activation and
  enforcement and the wiring for managed instances.

[EDITIONS.md](./EDITIONS.md) describes how the editions differ. Commercial
questions can be sent to [sales@everstack.ai](mailto:sales@everstack.ai).

## How this is checked

`python3 .github/scripts/validate-public-repo.py .` fails if a commercial path,
an enterprise or development build variant, or a commercial licence text
appears in this repository. This repository's CI runs it on every push and pull
request to `master`, together with
`python3 .github/scripts/validate-public-repo.py --go-deps-only .`, which
checks that the Community Edition binaries build from the files that are here.

## Contributions

Contributions are accepted under the Apache License 2.0. See
[CONTRIBUTING.md](./CONTRIBUTING.md).
