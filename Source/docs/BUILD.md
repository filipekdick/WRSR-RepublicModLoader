# Building Republic Mod Loader v1.0.1

## Requirements

Build on 64-bit Windows with:

- Go capable of building `windows/amd64`.
- Visual Studio C++ x64 build tools.
- CMake 3.24 or newer.
- Python 3 available as `python`.

No external Go modules are required.

`Source/VERSION` is the release-version source used by both build scripts.

## Complete build

From PowerShell in the `Source` directory:

```powershell
.\build-all.ps1
```

The scripts produce:

```text
build\RepublicModLoader.exe
build\rml_runtime.dll
```

Both binaries receive Windows version resources and a nonzero PE checksum. The launcher also receives `assets/RepublicModLoader.ico`.

## Launcher only

```powershell
.\build-launcher.ps1
```

The launcher is built for `windows/amd64`. The version string is injected from `VERSION`, then `tools/pe_resources.py` and `tools/pe_finalize.py` apply the release resource/checksum data.

## Native runtime only

```powershell
.\build-native.ps1
```

CMake configures the x64 native runtime from `CMakeLists.txt` and `runtime/`. `RML_BUILD_VERSION` is generated from `VERSION`.

The runtime is linked without the normal CRT and supplies local implementations of `memcpy`, `memset`, `memcmp` and `strlen`. Clang/clang-cl builds must not fold those helpers back into builtin library calls; `CMakeLists.txt` applies `-fno-builtin` automatically when Clang is detected.

## Release assembly

`ModFiles/` is the release layout. To assemble a build, replace its two binaries with the outputs above and retain the supplied runtime configuration/assets:

```text
ModFiles/
  RepublicModLoader.exe
  rml_runtime.dll
  rml/
    config.json
    assets/
      brand_logo.png
```

RML creates writable folders such as logs, state, exports and crash archives when needed; they are not part of a clean release package.
