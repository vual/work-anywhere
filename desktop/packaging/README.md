# Desktop packaging

Platform-specific installers, signing configuration, icons, manifests, and release metadata belong under this directory. Runtime behavior remains in `internal/platform`; packaging directories must not contain alternate copies of shared Bridge or daemon business logic.

- [`windows`](windows/README.md): current supported release target.
- [`macos`](macos/README.md): reserved for future signed/notarized app packaging.
- [`linux`](linux/README.md): reserved for future desktop package formats and user-service integration.

The current Windows build is produced by `../scripts/build.ps1`. Generated binaries remain under `../dist/` and are not source assets.
