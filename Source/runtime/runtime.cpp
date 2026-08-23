#include "runtime_internal.h"

HMODULE g_runtime_module = 0;
wchar_t g_root_path[RML_MAX_PATH] = {};
RuntimePerformanceStats g_performance_stats = {0, 0, 0, 0, 1};

static uint32_t g_prepare_status = 0;
static uint32_t g_prepare_ready = 0;
static uint32_t g_plan_ready = 0;
static uint32_t g_commit_done = 0;
static ULONGLONG g_bootstrap_start = 0;
static DeepTextSnapshot g_deep_matrix_snapshot = {};



static void deep_log_symbol_matrix(void) {
    if (!(g_plan.flags & PLAN_DEEP_TEST)) return;
    log_write(RML_LOG_INFO, "DeepTrace", "DEEP TRACE symbol registry matrix BEGIN");
    log_u32(RML_LOG_INFO, "DeepTrace", "Resolved/observed symbols: ", g_symbol_count);
    for (uint32_t i = 0; i < g_symbol_count; ++i) {
        SymbolRecord* s = g_symbol_registry[i];
        if (!s) continue;
        log_u32(RML_LOG_INFO, "DeepTrace", "Symbol index: ", i);
        log_write(RML_LOG_INFO, "DeepTrace", s->id ? s->id : "(null symbol id)");
        log_hex(RML_LOG_INFO, "DeepTrace", "Address: ", s->address);
        log_hex(RML_LOG_INFO, "DeepTrace", "RVA: ", s->rva);
        log_u32(RML_LOG_INFO, "DeepTrace", "Confidence enum: ", (uint32_t)s->confidence);
        log_write(RML_LOG_INFO, "DeepTrace", s->resolver ? s->resolver : "(no resolver)");
        log_write(RML_LOG_INFO, "DeepTrace", s->evidence ? s->evidence : "(no evidence)");
        log_u32(RML_LOG_INFO, "DeepTrace", "Function size: ", s->function_size);
        log_u32(RML_LOG_INFO, "DeepTrace", "From Symbol Cache: ", s->from_symbol_cache);
        log_u32(RML_LOG_INFO, "DeepTrace", "Observed-only: ", s->observed_only);
    }
    if (runtime_text_page_snapshot(&g_deep_matrix_snapshot)) {
        log_u32(RML_LOG_INFO, "DeepTrace", "SOVIET64 .text page count: ", g_deep_matrix_snapshot.page_count);
        log_u32(RML_LOG_INFO, "DeepTrace", "SOVIET64 .text page size: ", g_deep_matrix_snapshot.page_size);
        log_hex(RML_LOG_INFO, "DeepTrace", "SOVIET64 .text RVA: ", g_deep_matrix_snapshot.text_rva);
        log_u64(RML_LOG_INFO, "DeepTrace", "SOVIET64 .text whole-section fingerprint: ", runtime_text_fingerprint());
    }
    log_write(RML_LOG_INFO, "DeepTrace", "DEEP TRACE symbol registry matrix END");
}

static void deep_log_plugin_matrix(const char* phase) {
    if (!(g_plan.flags & PLAN_DEEP_TEST)) return;
    log_write(RML_LOG_INFO, "DeepTrace", phase ? phase : "Plugin state matrix");
    log_u32(RML_LOG_INFO, "DeepTrace", "Plugin records: ", g_plugin_count);
    for (uint32_t i = 0; i < g_plugin_count; ++i) {
        PluginState* p = &g_plugins[i];
        log_u32(RML_LOG_INFO, "DeepTrace", "Plugin index: ", i);
        log_write(RML_LOG_INFO, "DeepTrace", p->id[0] ? p->id : "(unnamed plugin)");
        log_write(RML_LOG_INFO, "DeepTrace", p->status[0] ? p->status : "(no status)");
        log_u32(RML_LOG_INFO, "DeepTrace", "Priority: ", (uint32_t)p->priority);
        log_u32(RML_LOG_INFO, "DeepTrace", "Legacy API: ", p->legacy_api);
        log_u32(RML_LOG_INFO, "DeepTrace", "Legacy hooks active: ", p->legacy_hooks_installed);
        log_u32(p->legacy_hook_failures ? RML_LOG_WARN : RML_LOG_INFO, "DeepTrace", "Legacy hook request failures: ", p->legacy_hook_failures);
        log_u32(RML_LOG_INFO, "DeepTrace", "Legacy services provided: ", p->legacy_services_provided);
        log_u32(RML_LOG_INFO, "DeepTrace", "Unmanaged init change windows: ", p->unmanaged_init_changes);
        log_u32(RML_LOG_INFO, "DeepTrace", "Unmanaged init pages: ", p->unmanaged_init_pages);
        log_u32(RML_LOG_INFO, "DeepTrace", "Unmanaged start change windows: ", p->unmanaged_start_changes);
        log_u32(RML_LOG_INFO, "DeepTrace", "Unmanaged start pages: ", p->unmanaged_start_pages);
        if (p->first_unmanaged_rva)
            log_hex(RML_LOG_INFO, "DeepTrace", "First unmanaged RVA: ", p->first_unmanaged_rva);
        if (p->module)
            log_hex(RML_LOG_INFO, "DeepTrace", "Module base: ", (uintptr_t)p->module);
    }
}

extern "C" BOOL WINAPI DllMain(HMODULE module, DWORD reason, LPVOID reserved) {
    (void)reserved;
    // Loader-lock rule: no allocation, file I/O, module loading, logging or
    // synchronization here. DisableThreadLibraryCalls removes needless
    // DLL_THREAD_ATTACH/DETACH traffic from WRSR's many game threads.
    if (reason == DLL_PROCESS_ATTACH) {
        g_runtime_module = module;
        DisableThreadLibraryCalls(module);
    }
    return TRUE;
}

// PREPARE PHASE
// -------------
// Runs while the WRSR primary thread remains live. This is the only phase that
// loads plugin DLLs. RML-native initialization and every Tesmio TsmPluginInit
// complete first. Legacy TsmPluginStart then runs with the compatibility host.
// For API fidelity, an unshared legacy
// inline/IAT hook is published at the host call exactly as TesmioLoader plugins
// expect; RML records provenance and COMMIT later revalidates the resulting path.
extern "C" __declspec(dllexport) DWORD WINAPI RmlBootstrap(LPVOID parameter) {
    if (g_prepare_ready) return g_prepare_status;
    g_bootstrap_start = GetTickCount64();
    const wchar_t* root = (const wchar_t*)parameter;
    if (!root) return 10;
    size_t n = 0;
    while (n < RML_MAX_PATH && root[n]) ++n;
    if (!n || n >= RML_MAX_PATH) return 11;
    rml_copy_wstr(g_root_path, RML_MAX_PATH, root);

    log_open();
    log_write(RML_LOG_INFO, "Core", "Republic Mod Loader runtime " RML_RUNTIME_VERSION " PREPARE phase bootstrapping.");
    log_write(RML_LOG_INFO, "Core", "Clean-room native x64 runtime; RML native API 1; Tesmio API 3/4 compatibility shim is automatic for detected legacy plugins.");
    log_write(RML_LOG_INFO, "Performance", "DisableThreadLibraryCalls enabled; Normal mode creates no resident worker thread, timer or polling loop. Detailed Diagnostic Logging may start one diagnostic-only read-only monitor after COMMIT.");
    log_write(RML_LOG_INFO, "Bootstrap", "PREPARE phase: DLL loading, symbol resolution and plugin Init/Start. Legacy Tesmio hooks use direct compatibility paths; COMMIT performs final integrity validation.");

    uint32_t status = 0;
    if (!plan_load()) {
        status = 20;
        g_prepare_status = status;
        g_prepare_ready = 1;
        g_performance_stats.bootstrap_ms = (uint32_t)(GetTickCount64() - g_bootstrap_start);
        log_write(RML_LOG_ERROR, "Core", "Runtime plan failed during PREPARE; COMMIT will finalize a degraded no-plugin runtime state.");
        return status;
    }
    g_plan_ready = 1;
    log_open_diagnostic();
    log_write(RML_LOG_INFO, "Diagnostics", "Timestamped exhaustive diagnostic mirror enabled for this validation profile.");
    symbol_registry_init();
    rml_memset(&g_deep_test_stats, 0, sizeof(g_deep_test_stats));
    if (g_plan.flags & PLAN_DEEP_TEST) {
        g_deep_test_stats.active = 1;
        log_write(RML_LOG_INFO, "DeepTest", "Manual forensic compatibility session active: real host-mediated hooks are installed exactly as in Normal mode, plugin lifecycle windows are fingerprinted page-by-page, and a Deep-only read-only monitor continues auditing SOVIET64 .text and hook chains while the user loads/plays a game manually.");
        log_u32(RML_LOG_INFO, "DeepTrace", "Process ID: ", GetCurrentProcessId());
        log_u32(RML_LOG_INFO, "DeepTrace", "Bootstrap thread ID: ", GetCurrentThreadId());
        log_u32(RML_LOG_INFO, "DeepTrace", "Plan flags: ", g_plan.flags);
        log_u32(RML_LOG_INFO, "DeepTrace", "Planned plugins: ", g_plan.plugin_count);
        log_u32(RML_LOG_INFO, "DeepTrace", "Cached symbols supplied: ", g_plan.cached_symbol_count);
        log_hex(RML_LOG_INFO, "DeepTrace", "SOVIET64 module base: ", (uintptr_t)GetModuleHandleW(0));
        log_hex(RML_LOG_INFO, "DeepTrace", "RML runtime module base: ", (uintptr_t)g_runtime_module);
    }

    if ((g_plan.flags & PLAN_FILTER_WORKSHOP) && !workshop_filter_install()) {
        status |= 0x800u;
        log_write(RML_LOG_WARN, "WorkshopFilter", "Workshop filtering could not be installed; WRSR will continue, but launcher Workshop Off states may still appear in-game.");
    }

    journal_session_start();
    log_write(RML_LOG_INFO, "Game", g_plan.game_version[0] ? g_plan.game_version : "Unknown game version");
    log_write(RML_LOG_INFO, "Game", g_plan.game_sha256[0] ? g_plan.game_sha256 : "No executable fingerprint supplied");

    if (!resolve_symbols()) status |= 0x100;
    if (g_plan.flags & PLAN_DEEP_TEST) deep_log_symbol_matrix();

    if (!load_plugins()) status |= 0x400;
    // Every Init completes before the first Start. Legacy hooks requested from
    // Start use the compatibility host's direct trampoline path.
    if (!tesmio_compat_start_plugins()) status |= 0x400;

    g_prepare_status = status;
    g_prepare_ready = 1;
    log_write(RML_LOG_INFO, "Bootstrap", "PREPARE phase complete. All plugin DLL loading, Init and legacy Start calls have returned.");
    log_u32(RML_LOG_INFO, "Bootstrap", "Host-mediated legacy hook requests active: ", tesmio_compat_pending_hook_count());
    log_write(RML_LOG_INFO, "Bootstrap", (g_plan.flags & PLAN_DEEP_TEST)
        ? "Detailed diagnostics use the same live hook path as Normal mode; COMMIT will verify every known chain before the session continues."
        : "COMMIT will revalidate all RML-known target chains before the launch is accepted.");
    return status;
}

// COMMIT PHASE
// ------------
// The launcher calls this only after PREPARE has returned. No DLL is
// loaded and no plugin lifecycle callback is invoked here. Legacy API hooks are
// already active through direct plugin -> continuation chains. COMMIT only
// revalidates those chains before accepting the launch.
// The WRSR primary thread remains live during PREPARE; this avoids suspending a
// thread while it may own an unrelated process-wide Windows/driver lock.
extern "C" __declspec(dllexport) DWORD WINAPI RmlBootstrapCommit(LPVOID parameter) {
    (void)parameter;
    if (!g_prepare_ready) return 30;
    if (g_commit_done) return g_prepare_status;
    g_commit_done = 1;

    uint32_t status = g_prepare_status;
    log_write(RML_LOG_INFO, "Bootstrap", "COMMIT phase BEGIN.");

    if (!g_plan_ready) {
        g_performance_stats.bootstrap_ms = (uint32_t)(GetTickCount64() - g_bootstrap_start);
        if (g_plan.flags & PLAN_DEEP_TEST) {
        log_u32(RML_LOG_INFO, "DeepTest", "Plugins observed: ", g_deep_test_stats.plugins_observed);
        log_u32(RML_LOG_INFO, "DeepTest", "Host hook requests observed in live rehearsal: ", g_deep_test_stats.host_hook_requests);
        log_u32(RML_LOG_INFO, "DeepTest", "Unmanaged SOVIET64 .text change windows observed: ", g_deep_test_stats.unmanaged_text_changes);
        log_u32(RML_LOG_INFO, "DeepTest", "Unmanaged 4 KiB .text pages changed across lifecycle windows: ", g_deep_test_stats.unmanaged_text_pages);
        log_write(RML_LOG_INFO, "DeepTest", "Deep Compatibility PREPARE failed before a manual forensic session could begin; startup evidence has been retained in the diagnostic log.");
    }

    g_deep_test_stats.bootstrap_status = status;
    write_runtime_report(status);
        log_write(RML_LOG_ERROR, "Core", "COMMIT finalized a failed PREPARE state; no plugins or executable hooks were activated.");
        log_close();
        return status;
    }

    // A failed legacy chain audit aborts the trial process. Plugins have already
    // completed Start with continuations supplied by the host, so continuing
    // after a broken chain would be unsafe.
    if (!tesmio_compat_commit_pending_hooks()) {
        status |= 0x80000400u;
        log_write(RML_LOG_ERROR, "TesmioCompat", "Legacy hook integrity verification failed. The launcher must discard this trial process rather than continue with an altered or incomplete execution chain.");
    }

    if (!(status & 0x80000000u)) {
        log_write(RML_LOG_INFO, "HookHost", tesmio_compat_pending_hook_count()
            ? "Legacy Tesmio targets use direct compatibility chains with no RML dispatcher on the hot path."
            : "No legacy executable hook was requested.");
    }

    // Detailed diagnostics omit the normal in-game status line so diagnostic
    // presentation cannot be confused with the standard launch profile.
    if (!(status & 0x80000000u) && !(g_plan.flags & PLAN_DEEP_TEST))
        install_menu_status(status);

    if (!(status & 0x80000000u) && (g_plan.flags & PLAN_DEEP_TEST)) {
        deep_log_plugin_matrix("DEEP TRACE: post-COMMIT plugin state matrix BEGIN");
        tesmio_compat_deep_audit_state("post-COMMIT baseline", 1);
        if (!deep_monitor_start())
            log_write(RML_LOG_WARN, "DeepMonitor", "Deep forensic monitor could not be started; startup/lifecycle evidence is still valid, but late runtime code changes will not be sampled.");
        else
            log_write(RML_LOG_INFO, "DeepTest", "Startup rehearsal complete. WRSR is now left running for manual gameplay; close the game when the reproduction session is finished.");
    }

    g_performance_stats.bootstrap_ms = (uint32_t)(GetTickCount64() - g_bootstrap_start);
    g_performance_stats.worker_threads = (g_plan.flags & PLAN_DEEP_TEST) ? 1u : 0u;
    g_performance_stats.timers = 0;
    g_performance_stats.polling_loops = (g_plan.flags & PLAN_DEEP_TEST) ? 1u : 0u;
    g_performance_stats.steady_state_none = !(g_plan.flags & PLAN_DEEP_TEST);
    log_u32(RML_LOG_INFO, "Performance", "Prepare+commit duration (ms): ", g_performance_stats.bootstrap_ms);
    if (g_plan.flags & PLAN_DEEP_TEST)
        log_write(RML_LOG_INFO, "Performance", "Deep-only diagnostic infrastructure: 1 read-only worker, 0 timers, 1 low-frequency polling loop; Normal mode remains 0/0/0.");
    else
        log_write(RML_LOG_INFO, "Performance", "RML steady-state infrastructure: 0 worker threads, 0 timers, 0 polling loops.");
    if (g_plan.flags & PLAN_DEEP_TEST) {
        log_write(RML_LOG_INFO, "Performance", "Detailed diagnostic steady-state work: read/fingerprint audit only; the monitor installs no additional gameplay detours.");
    } else {
        log_write(RML_LOG_INFO, "Performance", "Steady-state RML gameplay work: NONE (plugin-created work, if any, is outside this count).");
    }

    g_deep_test_stats.bootstrap_status = status;
    write_runtime_report(status);
    journal_bootstrap_complete(status);

    if (status)
        log_write(RML_LOG_WARN, "Core", "Bootstrap COMMIT completed with degraded functionality; inspect the report and log.");
    else
        log_write(RML_LOG_INFO, "Core", "Bootstrap COMMIT complete; game may continue with RML active.");

    return status;
}
