# straum-console

The web console for Straum is being rebuilt from scratch, together with
Straum itself. This repository holds no code right now, only the build,
release and install setup the new code will use:

- `.github/`: CI (`ci.yml`), the release pipeline (`release.yml`), the
  image pipeline (`image.yml`), the PR body check (`pr-contract.yml`), the
  Issue and PR templates.
- `go.mod`, `Makefile`, `.goreleaser.yaml`, `Dockerfile`: the module, the
  build and check targets, the release archives and the image.
- `install.sh`, `uninstall.sh`, `scripts/test-install.sh`: installing a
  release, and its test.

While there is no Go code and no `web/`, CI skips its steps.

The previous code is kept under the tag
[`archive/main-2026-10-23`](https://github.com/straum-io/straum-console/tree/archive/main-2026-10-23).

Apache License 2.0.
