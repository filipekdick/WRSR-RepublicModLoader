# Changelog

## Republic Mod Loader v1.0.0

Initial public release.

### Loader and runtime

- Standalone Windows x64 launcher and native runtime for Workers & Resources: Soviet Republic.
- Public **RML API 1** for native plugins.
- Built-in Tesmio API 3/4 compatibility host for existing Tesmio plugins.
- Fail-closed two-phase PREPARE/COMMIT bootstrap: partial or failed RML attachment is not silently treated as a successful modded launch.
- PE validation and plugin admission checks before launch.
- Exact-build WRSR symbol resolution with a validated persistent Symbol Cache.
- Host-mediated shared-hook tracking and compatible hook chaining.
- Recognition of validated external hook chains where safe.
- Normal and Detailed Diagnostic launch profiles.
- Runtime reports, session journals, compatibility evidence and optional crash/support archives.

### Mod Manager

- Plugin, Workshop and Development content views.
- Enable/disable controls for standalone plugins and Workshop content.
- Development DLL payload controls without hiding local `workshop_wip` projects from WRSR's in-game authoring/update menu.
- Automatic and manual plugin load ordering.
- Per-plugin Force mode for attempting unsupported plugins.
- Workshop/plugin dependency linking for hybrid Workshop items containing DLL payloads.
- Workshop update/support scanning and Workshop open-folder/open-page controls.
- Workshop unsubscribe and force-update operations.

### Playlists

- `Default` live configuration plus user-created named playlists.
- Save, rename and delete controls for user playlists.
- Portable JSON playlist export.
- Playlist import through file selection or drag-and-drop.
- Imported Workshop IDs are submitted to Steam for subscription.
- `Default` cannot be renamed, deleted or exported.

### Compatibility and diagnostics

- Pre-launch target-overlap analysis.
- Runtime hook-chain verification with plugin attribution.
- Detailed lifecycle timing and executable-page change attribution.
- Detection of unmanaged plugin code changes without automatically treating them as failures.
- Degraded-state reporting when a plugin loads but one or more individual hook requests fail.
- Exact WRSR executable SHA and plugin DLL SHA tracking in diagnostic evidence.