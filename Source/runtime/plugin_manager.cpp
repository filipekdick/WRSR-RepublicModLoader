#include "runtime_internal.h"

static DeepTextSnapshot g_deep_native_before = {};
static DeepTextSnapshot g_deep_native_after = {};

PluginState g_plugins[RML_MAX_PLUGINS] = {};
uint32_t g_plugin_count = 0;

static int confidence_usable(RmlSymbolConfidence confidence) {
    if (confidence == RML_SYMBOL_CONFIRMED) return 1;
    return confidence == RML_SYMBOL_PROBABLE && (g_plan.flags & PLAN_ALLOW_PROBABLE);
}

static void plugin_component(PluginState* plugin, char* out, size_t cap) {
    rml_copy_str(out, cap, "Plugin:");
    size_t p = rml_strlen(out);
    size_t i = 0;
    while (plugin && plugin->id[i] && p + 1 < cap) out[p++] = plugin->id[i++];
    out[p] = 0;
}

static void RML_CALL host_log(void* context, RmlLogLevel level, const char* message) {
    PluginState* plugin = (PluginState*)context;
    char component[180];
    plugin_component(plugin, component, sizeof(component));
    log_write(level, component, message ? message : "(null plugin log message)");
}

static int RML_CALL host_get_game_info(void* context, RmlGameInfo* out_info) {
    (void)context;
    if (!out_info || out_info->struct_size < sizeof(RmlGameInfo)) return 0;
    out_info->version = g_plan.game_version;
    out_info->sha256 = g_plan.game_sha256;
    out_info->image_base = (uintptr_t)GetModuleHandleW(0);
    out_info->loader_api = RML_API_VERSION;
    out_info->loader_version = RML_RUNTIME_VERSION;
    return 1;
}

static int fill_symbol_info(SymbolRecord* symbol, RmlSymbolInfo* out_info) {
    if (!symbol || !out_info || out_info->struct_size < sizeof(RmlSymbolInfo)) return 0;
    out_info->id = symbol->id;
    out_info->address = symbol->address;
    out_info->confidence = symbol->confidence;
    out_info->resolver = symbol->resolver;
    return 1;
}

static int RML_CALL host_get_symbol(void* context, const char* symbol_id, RmlSymbolInfo* out_info) {
    (void)context;
    return fill_symbol_info(symbol_registry_find(symbol_id), out_info);
}

static uint32_t RML_CALL host_get_symbol_count(void* context) { (void)context; return symbol_registry_count(); }
static int RML_CALL host_get_symbol_by_index(void* context, uint32_t index, RmlSymbolInfo* out_info) {
    (void)context; return fill_symbol_info(symbol_registry_at(index), out_info);
}

static PlanPlugin* plan_for_plugin(const PluginState* plugin) {
    if (!plugin) return 0;
    return plan_find_plugin(plugin->id);
}

static int RML_CALL host_provide_service(void* context, const char* name, uint32_t version, void* service) {
    PluginState* plugin = (PluginState*)context;
    if (!plugin) return 0;
    int ok = rml_service_provide(plugin, name, version, service);
    if (ok) journal_plugin_event(plan_for_plugin(plugin), "service_provided", name, "rml-service", "rml-api1", "Opaque versioned service published");
    return ok;
}
static void* RML_CALL host_consume_service(void* context, const char* name, uint32_t version, RmlServiceInfo* out_info) {
    PluginState* plugin = (PluginState*)context;
    if (!plugin) return 0;
    return rml_service_consume(name, version, out_info);
}

static int required_symbol_available(const char* id) {
    SymbolRecord* symbol = symbol_registry_find(id);
    return symbol ? confidence_usable(symbol->confidence) : 0;
}

static int bounded_copy_required(char* out, size_t cap, const char* source) {
    if (!out || cap < 2 || !source) return 0;
    for (size_t i = 0; i + 1 < cap; ++i) {
        char c = source[i];
        out[i] = c;
        if (!c) return i != 0;
    }
    out[cap - 1] = 0;
    return 0;
}

static int bounded_copy_optional(char* out, size_t cap, const char* source) {
    if (!out || !cap) return 0;
    if (!source) { out[0] = 0; return 1; }
    for (size_t i = 0; i + 1 < cap; ++i) {
        char c = source[i];
        out[i] = c;
        if (!c) return 1;
    }
    out[cap - 1] = 0;
    return 0;
}

static int bounded_equal(const char* a, const char* b, size_t cap) {
    if (!a || !b || !cap) return 0;
    for (size_t i = 0; i < cap; ++i) {
        if (a[i] != b[i]) return 0;
        if (!a[i]) return 1;
    }
    return 0;
}

enum {
    DESCRIPTOR_INVALID = 0,
    DESCRIPTOR_ID_MISMATCH = 1,
    DESCRIPTOR_OK = 2
};

static int safe_copy_descriptor(const RmlPluginDescriptor* descriptor,
                                const char* expected_id, PluginState* state, int strict_id) {
    if (!descriptor || !descriptor->id) return DESCRIPTOR_INVALID;
    if (strict_id && !bounded_equal(descriptor->id, expected_id, sizeof(state->id))) return DESCRIPTOR_ID_MISMATCH;
    if (!bounded_copy_required(state->name, sizeof(state->name), descriptor->name) ||
        !bounded_copy_required(state->version, sizeof(state->version), descriptor->version) ||
        !bounded_copy_optional(state->author, sizeof(state->author), descriptor->author)) return DESCRIPTOR_INVALID;
    return DESCRIPTOR_OK;
}

static int safe_query_version(RmlPluginQueryFn fn, uint32_t version, RmlPluginDescriptor* descriptor) {
    if (!fn || !descriptor) return 0;
    __try { return fn(version, descriptor); }
    __except(1) { return 0; }
}

static int safe_query(RmlPluginQueryFn fn, RmlPluginDescriptor* descriptor) {
    return safe_query_version(fn, RML_API_VERSION, descriptor);
}

static int safe_load(RmlPluginLoadFn fn, const RmlHostApi* api) {
    if (!fn) return 0;
    __try { return fn(api); }
    __except(1) { return -1; }
}


int load_plugins(void) {
    int all_loaded = 1;
    for (uint32_t i = 0; i < g_plan.plugin_count; ++i) {
        PlanPlugin* plan = &g_plan.plugins[i];
        PluginState* state = &g_plugins[g_plugin_count++];
        state->path = plan->path;
        state->priority = plan->priority;
        state->flags = plan->flags;
        rml_copy_str(state->id, sizeof(state->id), plan->id);
        rml_copy_str(state->version, sizeof(state->version), plan->version);
        rml_copy_str(state->dll_sha256, sizeof(state->dll_sha256), plan->dll_sha256);
        rml_copy_str(state->status, sizeof(state->status), "PENDING");
        journal_plugin_event(plan, "plugin_load_attempt", 0, "load", "rml-runtime",
                             "Plugin initialization transaction started");
        ULONGLONG deep_plugin_begin = GetTickCount64();
        if (g_plan.flags & PLAN_DEEP_TEST) {
            log_write(RML_LOG_INFO, "DeepTrace", "RML plugin initialization transaction BEGIN");
            log_u32(RML_LOG_INFO, "DeepTrace", "Plan index: ", i);
            log_write(RML_LOG_INFO, "DeepTrace", plan->id ? plan->id : "(null plugin id)");
            log_write(RML_LOG_INFO, "DeepTrace", plan->version ? plan->version : "(null plugin version)");
            log_write(RML_LOG_INFO, "DeepTrace", state->dll_sha256);
            log_u32(RML_LOG_INFO, "DeepTrace", "Priority: ", (uint32_t)state->priority);
            log_u32(RML_LOG_INFO, "DeepTrace", "Plan flags: ", state->flags);
            log_u32(RML_LOG_INFO, "DeepTrace", "Required symbol count: ", plan->required_count);
            for (uint32_t dr = 0; dr < plan->required_count; ++dr)
                log_write(RML_LOG_INFO, "DeepTrace", plan->required_symbols[dr] ? plan->required_symbols[dr] : "(null required symbol)");
        }

        int missing = 0;
        for (uint32_t r = 0; r < plan->required_count; ++r) {
            if (!required_symbol_available(plan->required_symbols[r])) {
                missing = 1;
                char msg[320];
                rml_copy_str(msg, sizeof(msg), "Required symbol unavailable: ");
                size_t p = rml_strlen(msg), n = 0;
                while (plan->required_symbols[r][n] && p + 1 < sizeof(msg))
                    msg[p++] = plan->required_symbols[r][n++];
                msg[p] = 0;
                char component[180];
                plugin_component(state, component, sizeof(component));
                log_fault(RML_LOG_ERROR, component, "PLUGIN FAILED TO LOAD", msg);
            }
        }
        if (missing) {
            state->failed = 1;
            all_loaded = 0;
            rml_copy_str(state->status, sizeof(state->status), "UNRESOLVED REQUIRED SYMBOL");
            journal_plugin_event(plan, "plugin_load_failure", 0, "load", "rml-runtime",
                                 "Required symbol unavailable; Force cannot invent a target");
            continue; // Force never invents a target.
        }

        if (state->flags & PLUGIN_TESMIO_LEGACY) {
            char component[180]; plugin_component(state, component, sizeof(component));
            log_write(RML_LOG_INFO, component, "Legacy Tesmio lifecycle detected; handing initialization to the Tesmio API 3/4 compatibility host.");
            if (!tesmio_compat_init_plugin(state, plan)) {
                state->failed = 1;
                all_loaded = 0;
                if (!state->status[0] || rml_strcmp(state->status, "PENDING") == 0)
                    rml_copy_str(state->status, sizeof(state->status), "TESMIO INIT FAILED");
                log_fault(RML_LOG_ERROR, component, "PLUGIN INITIALIZATION FAILED", "Tesmio compatibility initialization did not complete successfully.");
                journal_plugin_event(plan, "plugin_load_failure", 0, "legacy-init", "tesmio-shim",
                                     "Legacy plugin could not initialize through the compatibility host");
            }
            if (g_plan.flags & PLAN_DEEP_TEST) {
                char deep_comp[180]; plugin_component(state, deep_comp, sizeof(deep_comp));
                log_u64(RML_LOG_INFO, deep_comp, "DEEP TRACE total plan->legacy Init duration (ms): ", GetTickCount64() - deep_plugin_begin);
                log_write(state->failed ? RML_LOG_WARN : RML_LOG_INFO, deep_comp, state->failed ? "DEEP TRACE plugin initialization transaction END — FAILED" : "DEEP TRACE plugin initialization transaction END — INIT COMPLETE");
            }
            continue;
        }

        int deep_load_before_ok = (g_plan.flags & PLAN_DEEP_TEST) ? runtime_text_page_snapshot(&g_deep_native_before) : 0;
        ULONGLONG deep_load_begin = GetTickCount64();
        HMODULE module = LoadLibraryW(plan->path);
        int deep_load_after_ok = (g_plan.flags & PLAN_DEEP_TEST) ? runtime_text_page_snapshot(&g_deep_native_after) : 0;
        if (g_plan.flags & PLAN_DEEP_TEST) {
            char deep_comp[180]; plugin_component(state, deep_comp, sizeof(deep_comp));
            log_u64(RML_LOG_INFO, deep_comp, "DEEP TRACE LoadLibraryW duration (ms): ", GetTickCount64() - deep_load_begin);
            if (module) log_hex(RML_LOG_INFO, deep_comp, "DEEP TRACE module base: ", (uintptr_t)module);
            if (deep_load_before_ok && deep_load_after_ok) {
                uint32_t first_rva = 0;
                uint32_t pages = runtime_text_page_diff(&g_deep_native_before, &g_deep_native_after, &first_rva);
                if (pages) {
                    runtime_text_page_log_diff(deep_comp, "DEEP TRACE LoadLibraryW/process-attach .text page diff BEGIN", &g_deep_native_before, &g_deep_native_after);
                    state->unmanaged_init_changes++;
                    state->unmanaged_init_pages += pages;
                    if (!state->first_unmanaged_rva) state->first_unmanaged_rva = first_rva;
                    g_deep_test_stats.unmanaged_text_changes++;
                    g_deep_test_stats.unmanaged_text_pages += pages;
                    log_fault(RML_LOG_WARN, deep_comp, "PLUGIN DIRECT CODE CHANGE",
                              "Detailed diagnostics observed SOVIET64 executable-page changes during the plugin LoadLibrary/process-attach window; the plugin or one of its dependencies performed the write.");
                    char target[64];
                    rml_copy_str(target, sizeof(target), "SOVIET64.exe+0x");
                    rml_append_hex(target, sizeof(target), first_rva);
                    journal_plugin_event(plan, "unmanaged_text_change", target, "direct-code-modification", "diagnostic-loadlibrary-page-diff",
                                         "One or more 4 KiB .text pages changed during LoadLibrary/process attach");
                }
            }
        }
        if (!module) {
            state->failed = 1;
            all_loaded = 0;
            rml_copy_str(state->status, sizeof(state->status), "LOADLIBRARY FAILED");
            char component[180]; plugin_component(state, component, sizeof(component));
            log_fault_u32(RML_LOG_ERROR, component, "PLUGIN FAILED TO LOAD", "LoadLibraryW error: ", GetLastError());
            journal_plugin_event(plan, "plugin_load_failure", 0, "loadlibrary", "windows-loader",
                                 "LoadLibraryW failed");
            continue;
        }
        state->module = module;

        if (state->flags & PLUGIN_RAW_NATIVE) {
            state->loaded = 1;
            rml_copy_str(state->status, sizeof(state->status), "LOADED RAW DLL");
            char component[180]; plugin_component(state, component, sizeof(component));
            log_write(RML_LOG_INFO, component, "Raw native DLL loaded successfully through LoadLibraryW; no supported RML/Tesmio lifecycle was invoked.");
            if (g_plan.flags & PLAN_DEEP_TEST) ++g_deep_test_stats.plugins_observed;
            journal_plugin_event(plan, "plugin_load_success", 0, "loadlibrary", "windows-loader",
                                 "Raw native DLL loaded; DllMain/process-attach behaviour only");
            continue;
        }

        RmlPluginQueryFn query = (RmlPluginQueryFn)GetProcAddress(module, "RmlPluginQuery");
        RmlPluginLoadFn load = (RmlPluginLoadFn)GetProcAddress(module, "RmlPluginLoad");
        if (!query || !load) {
            state->failed = 1;
            all_loaded = 0;
            rml_copy_str(state->status, sizeof(state->status), "REQUIRED EXPORT MISSING");
            char component[180]; plugin_component(state, component, sizeof(component));
            log_fault(RML_LOG_ERROR, component, "PLUGIN API FAILED", "Selected RML lifecycle is incomplete at runtime: RmlPluginQuery and RmlPluginLoad were not both exported.");
            FreeLibrary(module);
            state->module = 0;
            journal_plugin_event(plan, "plugin_load_failure", 0, "exports", "rml-runtime",
                                 "Required RML exports are missing");
            continue;
        }

        RmlPluginDescriptor desc = {};
        desc.struct_size = sizeof(desc);
        ULONGLONG deep_query_begin = GetTickCount64();
        int deep_query_ok = safe_query(query, &desc);
        if (g_plan.flags & PLAN_DEEP_TEST) {
            char deep_comp[180]; plugin_component(state, deep_comp, sizeof(deep_comp));
            log_u64(RML_LOG_INFO, deep_comp, "DEEP TRACE RmlPluginQuery duration (ms): ", GetTickCount64() - deep_query_begin);
            log_u32(RML_LOG_INFO, deep_comp, "DEEP TRACE query call succeeded: ", deep_query_ok ? 1u : 0u);
            log_u32(RML_LOG_INFO, deep_comp, "DEEP TRACE descriptor version: ", desc.descriptor_version);
            log_u32(RML_LOG_INFO, deep_comp, "DEEP TRACE API min: ", desc.api_min);
            log_u32(RML_LOG_INFO, deep_comp, "DEEP TRACE API max: ", desc.api_max);
        }
        if (!deep_query_ok || desc.struct_size < sizeof(RmlPluginDescriptor) ||
            desc.descriptor_version != RML_PLUGIN_DESCRIPTOR_VERSION ||
            !desc.id || !desc.version || !desc.name) {
            state->failed = 1;
            all_loaded = 0;
            rml_copy_str(state->status, sizeof(state->status), "QUERY FAILED");
            char component[180]; plugin_component(state, component, sizeof(component));
            log_fault(RML_LOG_ERROR, component, "PLUGIN API FAILED", "RmlPluginQuery failed or returned an invalid descriptor.");
            FreeLibrary(module);
            state->module = 0;
            journal_plugin_event(plan, "plugin_load_failure", 0, "query", "rml-runtime",
                                 "RmlPluginQuery failed or returned invalid metadata");
            continue;
        }
        if (desc.api_min > RML_API_VERSION || desc.api_max < RML_API_VERSION || desc.api_max < desc.api_min) {
            state->failed = 1;
            all_loaded = 0;
            rml_copy_str(state->status, sizeof(state->status), "API INCOMPATIBLE");
            char component[180]; plugin_component(state, component, sizeof(component));
            log_fault(RML_LOG_ERROR, component, "PLUGIN API FAILED", "Plugin API range cannot be negotiated with this loader.");
            FreeLibrary(module);
            state->module = 0;
            journal_plugin_event(plan, "plugin_load_failure", 0, "api-compatibility", "rml-runtime",
                                 "No supported RML API version intersects the plugin range");
            continue;
        }
        int descriptor_copy = safe_copy_descriptor(&desc, plan->id, state,
                                                   (state->flags & PLUGIN_MANIFEST_IDENTITY) != 0);
        if (descriptor_copy != DESCRIPTOR_OK) {
            state->failed = 1;
            all_loaded = 0;
            char component[180]; plugin_component(state, component, sizeof(component));
            if (descriptor_copy == DESCRIPTOR_ID_MISMATCH) {
                rml_copy_str(state->status, sizeof(state->status), "MANIFEST ID MISMATCH");
                log_fault(RML_LOG_ERROR, component, "PLUGIN API FAILED", "DLL descriptor ID does not match its explicit sidecar manifest.");
            } else {
                rml_copy_str(state->status, sizeof(state->status), "INVALID DESCRIPTOR STRINGS");
                log_fault(RML_LOG_ERROR, component, "PLUGIN API FAILED", "DLL descriptor contains an unreadable, empty or overlong string.");
            }
            FreeLibrary(module);
            state->module = 0;
            journal_plugin_event(plan, "plugin_load_failure", 0, "descriptor-validation", "rml-runtime",
                                 "Plugin descriptor validation failed");
            continue;
        }
        rml_memset(&state->host, 0, sizeof(state->host));
        state->host.struct_size = RML_HOST_API_SIZE;
        state->host.api_version = RML_API_VERSION;
        state->host.plugin_context = state;
        state->host.log = host_log;
        state->host.get_game_info = host_get_game_info;
        state->host.get_symbol = host_get_symbol;
        state->host.get_symbol_count = host_get_symbol_count;
        state->host.get_symbol_by_index = host_get_symbol_by_index;
        state->host.provide_service = host_provide_service;
        state->host.consume_service = host_consume_service;

        char component[180]; plugin_component(state, component, sizeof(component));
        if (state->flags & PLUGIN_FORCED)
            log_write(RML_LOG_WARN, component, "FORCED load requested; policy warnings were overridden by the launcher.");
        int deep_before_ok = (g_plan.flags & PLAN_DEEP_TEST) ? runtime_text_page_snapshot(&g_deep_native_before) : 0;
        ULONGLONG deep_init_begin = GetTickCount64();
        int initialized = safe_load(load, &state->host);
        if (g_plan.flags & PLAN_DEEP_TEST) {
            log_u64(RML_LOG_INFO, component, "DEEP TRACE RmlPluginLoad duration (ms): ", GetTickCount64() - deep_init_begin);
            log_u32(RML_LOG_INFO, component, "DEEP TRACE RmlPluginLoad result: ", (uint32_t)initialized);
        }
        int deep_after_ok = (g_plan.flags & PLAN_DEEP_TEST) ? runtime_text_page_snapshot(&g_deep_native_after) : 0;
        if ((g_plan.flags & PLAN_DEEP_TEST) && deep_before_ok && deep_after_ok) {
            uint32_t first_rva=0; uint32_t pages=runtime_text_page_diff(&g_deep_native_before,&g_deep_native_after,&first_rva);
            if (pages) {
                runtime_text_page_log_diff(component, "DEEP TRACE RmlPluginLoad .text page diff BEGIN", &g_deep_native_before, &g_deep_native_after);
                state->unmanaged_init_changes++; state->unmanaged_init_pages += pages; if (!state->first_unmanaged_rva) state->first_unmanaged_rva=first_rva;
                g_deep_test_stats.unmanaged_text_changes++; g_deep_test_stats.unmanaged_text_pages += pages;
                log_fault(RML_LOG_WARN, component, "PLUGIN DIRECT CODE CHANGE", "Detailed diagnostics observed unmanaged SOVIET64 executable-page changes during RmlPluginLoad.");
                char target[64]; rml_copy_str(target,sizeof(target),"SOVIET64.exe+0x"); rml_append_hex(target,sizeof(target),first_rva);
                journal_plugin_event(plan, "unmanaged_text_change", target, "direct-code-modification", "deep-test-page-diff", "One or more 4 KiB .text pages changed during RmlPluginLoad");
            }
        }
        if (initialized <= 0) {
            state->failed = 1;
            all_loaded = 0;
            rml_service_remove_owner(state);
            rml_copy_str(state->status, sizeof(state->status), initialized < 0 ? "INITIALIZATION EXCEPTION" : "INITIALIZATION FAILED");
            log_fault(RML_LOG_ERROR, component, initialized < 0 ? "PLUGIN EXCEPTION" : "PLUGIN INITIALIZATION FAILED",
                      initialized < 0 ? "Native exception escaped RmlPluginLoad; staged services were rolled back." : "RmlPluginLoad returned failure; partial services were rolled back.");
            journal_plugin_event(plan, "plugin_load_failure", 0, "initialization", "rml-runtime",
                                 initialized < 0 ? "Native exception during plugin initializer; staged services rolled back" : "Plugin initializer failed; staged services rolled back");
            FreeLibrary(module);
            state->module = 0;
            continue;
        }
        state->loaded = 1;
        rml_copy_str(state->status, sizeof(state->status), state->flags & PLUGIN_FORCED ? "FORCED" : "LOADED");
        log_write(RML_LOG_INFO, component, "Plugin initialized through RML API 1.");
        if (g_plan.flags & PLAN_DEEP_TEST) ++g_deep_test_stats.plugins_observed;
        journal_plugin_event(plan, "plugin_load_success", 0, "load", "rml-runtime",
                             "Plugin initialized successfully");
        if (g_plan.flags & PLAN_DEEP_TEST) {
            log_u64(RML_LOG_INFO, component, "DEEP TRACE total native plugin initialization transaction (ms): ", GetTickCount64() - deep_plugin_begin);
            log_write(RML_LOG_INFO, component, "DEEP TRACE plugin initialization transaction END — SUCCESS");
        }
    }

    // Tesmio Start runs after this function returns, so all plugin Init calls
    // finish before any legacy Start callback is invoked.
    return all_loaded;
}
