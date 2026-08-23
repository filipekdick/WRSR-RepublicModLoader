# RML native plugin API

The public ABI is defined by `include/rml_api.h`. The current host API version is **1**.

## Workshop DLL discovery and admission

Place a DLL inside a folder named `plugin` or `plugins` in the Workshop item. RML checks those folders at the Workshop item root and one folder level below it. A sidecar RML manifest is optional; a DLL does not need one simply to be discovered and listed.

A loadable DLL must be a readable native **AMD64/x86-64 Windows PE32+ DLL** and be declared as a DLL in the PE header. RML-specific exports are optional for static admission. RML uses a supported lifecycle when one is detected; otherwise a structurally valid native DLL can be attempted through ordinary Windows `LoadLibrary` behavior.

## RML-native exports

An RML-native plugin exposes:

```cpp
int RmlPluginQuery(uint32_t host_api_version, RmlPluginDescriptor* out_descriptor);
int RmlPluginLoad(const RmlHostApi* host);
```

Plugins remain loaded for the WRSR process lifetime; API 1 does not define a runtime unload callback.

`RmlPluginQuery` supplies the plugin descriptor and declares the API range the plugin accepts. RML loads a native plugin only when that range includes API 1. If an explicit valid sidecar manifest defines a strict identity, the descriptor identity must match it.

## Host services

`RmlHostApi` exposes:

- `log` — write a plugin-attributed RML log entry.
- `get_game_info` — retrieve WRSR version, executable SHA-256, image base and RML version/API information.
- `get_symbol` — retrieve a resolved symbol by semantic ID.
- `get_symbol_count` / `get_symbol_by_index` — enumerate the current symbol registry.
- `provide_service` — publish an opaque, versioned service pointer for other plugins.
- `consume_service` — retrieve a compatible service and provider identity.

All structures carry `struct_size` fields where applicable. Plugins should initialize output structures with their compiled structure size before passing them to the host.

## Symbols

A returned `RmlSymbolInfo` contains the semantic ID, current-process address, confidence and resolver description. Plugins should require the semantic symbols they actually depend on and fail safely when a required target is unavailable.

RML API 1 does not expose a native managed callback/hook endpoint API. A native plugin that needs behavior beyond the current API must implement that behavior explicitly and accept responsibility for its own compatibility.

## Shared services

Services are identified by name and version. Providers retain ownership and lifetime responsibility for the pointer they publish. Consumers should request the exact service/version contract they understand and treat a missing service as unavailable.

## Native binary expectations

Plugins should be conventional native AMD64 Windows DLLs with normal imports and ordinary executable protections. Avoid packers, obfuscation, writable-and-executable image sections and unnecessary executable-memory behavior.
