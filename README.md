# Republic Mod Loader

**Version 1.0.1 — RML API 1**

Republic Mod Loader (RML) is a standalone Windows x64 mod loader and mod manager for **Workers & Resources: Soviet Republic**. It launches WRSR with selected DLL plugins and Workshop content, provides a native **RML API 1**, and includes compatibility support for Tesmio API 3/4 plugins.

RML v1.0.1 has been tested against WRSR **1.1.1.9**.

- Steam Workshop: https://steamcommunity.com/sharedfiles/filedetails/?id=3787969749
- Source repository: https://github.com/Ultimate-Universe/WRSR-RepublicModLoader
- WRSR Workshop collection: https://steamcommunity.com/workshop/filedetails/?id=3778286965

## Requirements

- 64-bit Windows.
- Workers & Resources: Soviet Republic installed through Steam.
- Steam running when using subscribed Workshop content or Steam Workshop operations.

## Installation and launching

The normal installation is the Steam Workshop item. After subscribing, run:

```text
<Steam library>\steamapps\workshop\content\784150\3787969749\RepublicModLoader.exe
```

RML can also be launched in the following ways:

- **Copied EXE:** `RepublicModLoader.exe` may be copied to the Desktop, another folder, or another drive without copying `rml_runtime.dll`. If the runtime is not beside the EXE, RML searches the Steam libraries configured in `libraryfolders.vdf` for Workshop item `3787969749` and uses the runtime/configuration files there.
- **Steam non-Steam game:** the EXE can be added to the Steam library as a non-Steam game and launched from Steam.
- **Windows shortcut or taskbar pin:** create a shortcut to the EXE or pin it normally.

If you copy the EXE by itself, keep the RML Workshop item subscribed and installed because that Workshop installation remains the runtime home.

## Using RML

1. Run `RepublicModLoader.exe`.
2. Confirm the detected WRSR installation if prompted.
3. Choose the active playlist.
4. Enable the DLL plugins, Workshop items and Development DLL payloads you want to use.
5. Launch WRSR through RML.

The Mod Manager has three content views:

- **Plugins** — standalone DLLs and DLLs embedded in Workshop or Development projects.
- **Workshop** — subscribed WRSR Workshop items.
- **Development** — local projects under `media_soviet/workshop_wip`.

Development checkboxes control whether RML loads a project's DLL payload. Disabling a Development DLL does not hide that project from WRSR's in-game Workshop/Development authoring menu.

## Playlists

`Default` is the live default configuration. User-created playlists can be saved, renamed, deleted, exported and imported.

- `Default` cannot be renamed, deleted or exported.
- Exports are JSON files and default to the `Exports` folder in the RML runtime home.
- Import accepts a selected or drag-and-dropped playlist JSON and requests Steam subscriptions for Workshop items listed in the playlist.
- A launch configuration can contain up to **40,000 enabled Workshop items**.

## Plugin compatibility

RML supports:

- native RML API 1 plugins;
- Tesmio API 3/4 plugins through the built-in compatibility host;
- structurally valid native DLLs that do not expose either supported lifecycle.

RML validates DLL structure before launch, maintains an exact-build Symbol Cache for known WRSR targets, and tracks supported shared-hook chains. Detailed Diagnostic Logging adds lifecycle, hook-chain and executable-change evidence for troubleshooting.

## Troubleshooting

If RML aborts a launch, use **Detailed Diagnostic Logging** and reproduce the problem. RML records launcher/runtime logs and can create a support archive containing the relevant reports and configuration.

If a copied EXE reports that the runtime cannot be found, verify that Workshop item `3787969749` is subscribed and fully downloaded in one of Steam's configured libraries. Running the EXE directly from the Workshop folder is the simplest baseline test.

For plugin-development requirements and the native API, see `Source/docs/PLUGIN_SDK.md`.

## Building

The complete source required to rebuild the release is in `Source/`.

- `Source/docs/BUILD.md` — toolchain and build instructions.
- `Source/docs/ARCHITECTURE.md` — launcher/runtime architecture and important constraints.
- `Source/docs/PLUGIN_SDK.md` — DLL discovery, admission and RML API 1.
- `Source/docs/TESMIO_COMPATIBILITY.md` — Tesmio API 3/4 compatibility behavior.

The public native interface is `Source/include/rml_api.h`.

## Repository layout

```text
README.md
CHANGELOG.md
LICENSE
ModFiles/   # files distributed through Steam Workshop
Source/     # buildable source and developer documentation
```

## Licence

Republic Mod Loader is distributed under the **GNU General Public License version 3**. See `LICENSE`.

Workers & Resources: Soviet Republic, Steam, TesmioLoader and their respective names/assets belong to their respective owners. This project is not affiliated with or endorsed by 3DIVISION, Hooded Horse, Valve, or the TesmioLoader author.
