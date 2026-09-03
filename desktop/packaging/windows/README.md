# Windows packaging

Windows x64 is the currently supported Desktop target. This directory owns future installer definitions, signing configuration, release icons, and packaging metadata.

The current build generates the PE manifest and version resource into the Go command package because Go requires the generated `.syso` beside that package. The source build entry remains `../../scripts/build.ps1`, and the result is `../../dist/windows-amd64/work-anywhere.exe`.

Do not store certificates, private signing keys, generated installers, or release binaries in this directory.
