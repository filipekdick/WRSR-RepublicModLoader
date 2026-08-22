# Tesmio API 3/4 compatibility

RML contains a clean-room compatibility host for WRSR plugins built against the Tesmio API 3/4 host ABI. TesmioLoader itself is not embedded or required at runtime.

## Admission and lifecycle

Tesmio interface detection classifies a DLL; it is not a substitute for PE validation. A selected plugin with a complete Tesmio lifecycle is initialized through the compatibility host. API 3 and API 4 plugins are accepted by that host.

Plugin initialization occurs during PREPARE. Tesmio start-phase work is staged so initialization can complete across the selected set before COMMIT finalizes the launch transaction and integrity state.

## Inline hooks

For a first host-mediated owner of a target, RML:

1. validates the plugin's expected bytes against the current target state;
2. records the original bytes and target span;
3. creates the original continuation trampoline;
4. publishes the plugin detour;
5. records the effective top and provenance.

The first owner's stolen-byte span becomes the canonical protected publication/resume boundary for that target.

For a later owner on the same RML-tracked target:

1. the target must still correspond to the known effective chain or a supported recognizable external adoption case;
2. the incoming expected bytes must match the current executable target over the incoming requested span;
3. the incoming span must be at least 14 bytes and no wider than the first owner's tracked span;
4. RML creates a continuation whose first hop is the previous effective top;
5. the entry is republished using the first owner's canonical patch width;
6. the new plugin becomes the effective top.

The resulting order is:

```text
game target -> newest owner -> previous owner(s) -> original game continuation
```

A request that would widen beyond the established first-owner boundary is refused because the preserved original continuation resumes at the original boundary. Overwriting beyond it could make that continuation return into bytes that have since been replaced.

Unknown target mutations are not assumed to be chainable.

## Recognized external chains

A plugin may alter a target without using the compatibility host. RML can adopt a narrow recognized external detour only when the live entry encoding, executable destination and attribution are sufficiently validated. Otherwise the target is reported as changed/conflicting rather than followed blindly.

## IAT hooks

Host-mediated IAT replacements use deterministic owner ordering. RML validates the current slot destination and refuses an unexpected target rather than silently overwriting it.

## Direct plugin changes

Legacy plugins may patch executable pages through private code paths. Detailed diagnostics compare executable-page fingerprints around plugin lifecycle windows and distinguish host-managed changes from unmanaged plugin changes where possible. An unmanaged change is an observation, not automatically a failure.

## Degraded state

A Tesmio plugin can complete its lifecycle while one or more individual hook requests fail. In that case RML reports the plugin as `TESMIO ACTIVE — DEGRADED` and records the failed request rather than misclassifying the DLL as statically invalid.

## Detailed Diagnostic Logging

Detailed Diagnostic Logging uses the same real hook installation semantics as Normal mode and adds attribution/integrity instrumentation. It records hook requests, chain steps, lifecycle timing, executable-page changes and ongoing read-only chain audits until WRSR exits.
