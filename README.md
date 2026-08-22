# Republic Mod Loader

**Version 1.0.0 — RML API 1**

Republic Mod Loader (RML) is a standalone Windows x64 mod loader and mod manager for **Workers & Resources: Soviet Republic**.

RML launches WRSR through its own fail-closed bootstrap, loads enabled DLL plugins, provides a native **RML API 1**, hosts compatible Tesmio API 3/4 plugins, manages Workshop and Development content, and records compatibility evidence for shared runtime hooks.

The current release has been tested against WRSR **1.1.1.9**.

## Repository layout

- `ModFiles/` — ready-to-run release files. This is identical to the Steam Workshop package.
- `Source/` — complete current source code, build scripts, public API header and technical documentation required to understand and rebuild RML.
- `CHANGELOG.md` — release changelog.
- `STEAM_WORKSHOP_CHANGELOG.md` — copy-ready Steam Workshop update text.
- `LICENSE` — GNU GPL v3.

## Installation

1. Copy the contents of `ModFiles/` to a folder of your choice.
2. Run `RepublicModLoader.exe`.
3. Confirm the detected WRSR installation.
4. Enable the plugins, Workshop items and Development DLL payloads you want to use.
5. Launch WRSR through RML.

Steam must be running for Workshop subscription/update operations.

## Mod Manager

The Mod Manager provides three content views:

- **Plugins** — standalone DLLs and DLLs embedded in Workshop/Development projects.
- **Workshop** — subscribed WRSR Workshop items.
- **Development** — local projects under `media_soviet/workshop_wip`.

Development checkboxes control whether RML loads a project's DLL payload. Disabled Development projects remain visible in WRSR's in-game Workshop/Development authoring menu so they can still be updated through the game.

## Playlists

`Default` is the live default configuration. User-created playlists can be saved, renamed, deleted, exported and imported.

- `Default` cannot be renamed, deleted or exported.
- Exported playlists are JSON files and default to the `Exports` folder beside RML.
- Import accepts a selected or drag-and-dropped playlist JSON, creates a new playlist and requests Steam subscriptions for listed Workshop items.

## Plugin compatibility

RML supports:

- native RML API 1 plugins;
- Tesmio API 3/4 plugins through the built-in compatibility host;
- raw native DLL loading where no supported plugin lifecycle is exposed.

RML validates known hook targets, tracks host-mediated hook ownership and chains compatible shared hooks instead of blindly overwriting an existing owner. Detailed Diagnostic Logging can attribute executable-page changes and audit chains while the game is running.

## Resolver and Symbol Cache

RML keeps a validated exact-build Symbol Cache for known WRSR targets. Cached RVAs are checked against the current `SOVIET64.exe` fingerprint and entry bytes before reuse. This avoids unnecessary repeated resolution while keeping stale addresses from being trusted blindly.

## Building

The complete buildable source is in `Source/`.

See:

- `Source/docs/BUILD.md`
- `Source/docs/ARCHITECTURE.md`
- `Source/docs/PLUGIN_SDK.md`
- `Source/docs/TESMIO_COMPATIBILITY.md`

The public native interface is defined by `Source/include/rml_api.h` and is currently **RML API 1**.

## Licence

Republic Mod Loader is distributed under the **GNU General Public License version 3**. See `LICENSE`.

Workers & Resources: Soviet Republic, Steam, TesmioLoader and their respective names/assets belong to their respective owners. This project is not affiliated with or endorsed by 3DIVISION, Hooded Horse, Valve, or the TesmioLoader author.
