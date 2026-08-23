# Republic Mod Loader v1.0.1 architecture

The public native plugin interface is RML API 1.

## Components

RML consists of two binaries:

- `RepublicModLoader.exe` — Windows launcher and Mod Manager, written in Go.
- `rml_runtime.dll` — native x64 runtime loaded into WRSR, written in C++.

The launcher owns discovery, configuration, UI, playlist management, compatibility analysis, Steam Workshop operations, launch planning and process attachment. The runtime owns in-process symbol resolution, plugin lifecycle, Tesmio compatibility, hook-chain tracking, Workshop session filtering and runtime diagnostics.

## Runtime-home resolution

A complete local copy is self-contained when `rml_runtime.dll` is beside the launcher. If a copied EXE has no local runtime, the launcher locates Workshop item `3787969749` by reading Steam's registered client installation and configured `libraryfolders.vdf` entries. The Workshop copy then supplies the runtime DLL, configuration, assets and writable RML state.

No drive letter is assumed. Keep this lookup tied to Steam's configured libraries rather than adding fixed `C:`, `D:` or similar search paths.

## Steam non-Steam shortcuts

Steam assigns generated App/Game/Overlay identity variables to non-Steam shortcuts. Passing those identities unchanged to `SOVIET64.exe` can make WRSR initialize against the shortcut rather than its own Steam application context.

For a detected foreign shortcut identity, the launcher removes only `SteamAppId`, `SteamGameId` and `SteamOverlayGameId` from the WRSR child environment. Other Steam client/session variables remain inherited. Normal direct launches inherit the environment unchanged.

## Launch flow

1. The launcher discovers the WRSR installation and fingerprints `SOVIET64.exe`.
2. Plugins and Workshop/Development content are discovered and validated.
3. The active configuration is ordered and serialized into the runtime plan.
4. WRSR is created suspended so RML can establish the launch transaction before normal game execution proceeds.
5. The primary game thread is resumed for Windows loader initialization.
6. The launcher loads `rml_runtime.dll` into WRSR.
7. `RmlBootstrap` performs PREPARE: plan parsing, Workshop filtering, symbol resolution and plugin initialization.
8. `RmlBootstrapCommit` performs COMMIT: staged Tesmio start work and final chain/integrity validation.
9. On a successful two-phase bootstrap, WRSR continues normally. A failed or partial attach is treated as a launch failure rather than silently continuing with an unknown partial RML state.

## Plugin discovery and loading

RML recognizes three practical DLL categories:

- RML-native plugins using `RmlPluginQuery` and `RmlPluginLoad`.
- Tesmio API 3/4 plugins hosted by the compatibility layer.
- structurally valid raw native DLLs that do not expose either lifecycle.

PE validation occurs before launch. The runtime plan contains the selected DLL path, identity, SHA-256, priority, loader classification and any RML-native required symbols.

RML-native required symbols are fail-closed: if a required target is unavailable, Force does not fabricate an address.

## Resolver and Symbol Cache

The runtime maintains an in-memory symbol registry for the current process. Semantic symbols identify known WRSR functions by stable names such as `wrsr.vehicle.task_advance`.

The launcher maintains an exact-build Symbol Cache. Cache records are keyed to the `SOVIET64.exe` SHA-256 and contain the RVA, entry fingerprint, function size, confidence and semantic metadata needed to validate and reuse a known target. Runtime-observed targets may also be recorded for compatibility attribution on that exact executable.

A cached record that does not validate against the current executable is not supplied as a usable target.

## Hook-chain coexistence

Tesmio compatibility hooks are provenance-tracked by target and plugin. For host-mediated inline hooks RML records the original continuation, effective top detour, installation order and executable publication state. Later compatible owners are chained instead of overwriting an existing known owner.

RML also recognizes a narrow set of externally published detours when the effective path can be validated safely. Unknown target mutations are not followed blindly.

Detailed chain rules are documented in `TESMIO_COMPATIBILITY.md`.

## Workshop and Development behavior

Subscribed Workshop visibility for a launched RML session is allow-list based when the launcher has a valid Workshop inventory. Only Workshop IDs enabled in the active configuration are forwarded to WRSR's subscribed-Workshop namespace for that session. The enabled Workshop list supports up to 40,000 IDs.

Development projects under `media_soviet/workshop_wip` are never hidden by RML. Their Development checkbox controls only whether RML loads DLL payloads discovered in that project. This preserves WRSR's in-game authoring/update workflow even when a Development DLL is disabled.

## Playlists

`Default` stores the default live configuration. User playlists snapshot plugin enable/order state plus enabled Workshop and Development IDs.

Import always creates a new playlist. Workshop IDs in an imported file are submitted to Steam for subscription. Export is available only for a saved user playlist, not `Default`.

## Persistent state

Runtime state is stored beneath `rml/state` in the resolved runtime home. This includes the latest runtime report and per-session journal files.

Compatibility knowledge and the exact-build Symbol Cache are persisted outside the install directory under the user's local application-data area. They are keyed by plugin/game fingerprints rather than mutable filenames alone.

## Diagnostics

Normal mode avoids a resident diagnostic polling loop. Detailed Diagnostic Logging enables the read-only monitor and exhaustive lifecycle/hook/page-change evidence until WRSR closes.

The runtime report is machine-readable JSON. The launcher also produces concise session logs and can create support archives containing logs, configuration and reports without bundling plugin DLL binaries.

## Scope

RML can validate and mediate behavior that passes through its supported host paths. A plugin may still patch executable code privately, alter data/vtables or perform arbitrary native work outside those paths. Detailed diagnostics can attribute many executable changes but cannot make arbitrary third-party native code intrinsically safe.
