# Building Republic Mod Loader v1.0.0

## Requirements

Build on 64-bit Windows with:

- Go capable of building `windows/amd64`.
- Visual Studio C++ x64 build tools.
- CMake 3.24 or newer.
- Python 3 available as `python`.

No external Go modules are required.

## Complete build

From PowerShell in the `Source` directory:

```powershell
.\build-all.ps1
```

The scripts read `VERSION`, build both binaries and write:

```text
build\RepublicModLoader.exe
build\rml_runtime.dll
```

Both binaries receive Windows version metadata for Republic Mod Loader v1.0.0 and are finalized with a nonzero PE checksum. The launcher also receives `assets/RepublicModLoader.ico`.

## Launcher only

```powershell
.\build-launcher.ps1
```

The launcher is built for `windows/amd64`. Its compiled version string is set from `VERSION` and the PE resource metadata is applied after linking.

## Native runtime only

```powershell
.\build-native.ps1
```

CMake configures an x64 Visual Studio build from the sources listed in `CMakeLists.txt`. `RML_BUILD_VERSION` is generated from `VERSION`, so the runtime log/API version and PE product metadata stay aligned.

The `runtime/` directory is the implementation of `rml_runtime.dll`; it is required source.

## Runtime package assembly

Place the two built binaries beside the source-provided runtime files/directories:

```text
RepublicModLoader.exe
rml_runtime.dll
Exports/
rml/
  config.json
  assets/
    brand_logo.png
  plugins/
  logs/
  state/
    sessions/
  crash-archives/
```

The writable runtime directories are also created automatically when needed.
