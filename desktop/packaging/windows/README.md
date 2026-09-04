# Windows packaging

Windows x64 is the currently supported Desktop target. This directory owns the Inno Setup installer definition, the Windows icon fallback, future signing configuration, release icons, and packaging metadata.

The current build generates the PE manifest and version resource into the Go command package because Go requires the generated `.syso` beside that package. The source build entry remains `../../scripts/build.ps1`, and the result is `../../dist/windows-amd64/work-anywhere.exe`.

With Inno Setup 6 installed, build the user-level installer from `desktop/`:

```powershell
.\scripts\build-installer.ps1 -Version 0.1.0
```

The installer output is `desktop/dist/windows-amd64/work-anywhere-setup-0.1.0.exe`. It installs to `%LOCALAPPDATA%\Programs\WorkAnywhere`, invokes `work-anywhere.exe install` to set the current user's `CODEX_CLI_PATH`, backs up the previous value, and displays a prompt to fully restart ChatGPT Desktop. Uninstallation stops the daemon when possible, restores the previous environment variable, and leaves `%LOCALAPPDATA%\WorkAnywhere` intact.

The canonical source icon is the repository-root `waw-app-icon.svg`. The build uses Chrome's native SVG renderer with a transparent page background when available and otherwise uses the checked-in transparent `icon.png` fallback. Both the web favicon/page icon and the Windows tray/PE icon are derived from that branding asset. The tray loads the 48x48 resource for sharper high-DPI scaling; Windows controls the final tray slot size.

Do not store certificates, private signing keys, generated installers, or release binaries in this directory.
