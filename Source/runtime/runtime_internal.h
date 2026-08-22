#ifndef RML_RUNTIME_INTERNAL_H
#define RML_RUNTIME_INTERNAL_H

#include "win32_min.h"
#include "../include/rml_api.h"

#ifndef RML_BUILD_VERSION
#define RML_BUILD_VERSION "1.0.0"
#endif
#define RML_RUNTIME_VERSION RML_BUILD_VERSION
#define RML_MAX_PLUGINS 128
#define RML_MAX_REQUIRED_SYMBOLS 32
#define RML_MAX_PATH 1024
#define RML_MAX_CACHED_SYMBOLS 256
#define RML_MAX_FINGERPRINT 64
#define RML_SESSION_ID_CAP 64
#define RML_MAX_ENABLED_WORKSHOP 4096
#define RML_MAX_SYMBOLS 512
#define RML_MAX_OBSERVED_SYMBOLS 384
#define RML_MAX_SERVICES 128
#define RML_MAX_TEXT_PAGES 4096
#define RML_MAX_CHAIN_STEPS 32

struct PlanCachedSymbol {
    char* id;
    uint32_t rva;
    uint32_t function_size;
    uint32_t confidence;
    uint32_t fingerprint_length;
    BYTE fingerprint[RML_MAX_FINGERPRINT];
};

struct PlanPlugin {
    wchar_t* path;
    char* id;
    char* version;
    char dll_sha256[65];
    int32_t priority;
    uint32_t flags;
    uint32_t required_count;
    char** required_symbols;
};

struct RuntimePlan {
    uint32_t flags;
    uint32_t profile;
    char game_version[32];
    char game_sha256[65];
    char session_id[RML_SESSION_ID_CAP];
    uint32_t cached_symbol_count;
    PlanCachedSymbol cached_symbols[RML_MAX_CACHED_SYMBOLS];
    uint32_t plugin_count;
    PlanPlugin plugins[RML_MAX_PLUGINS];
    uint32_t enabled_workshop_count;
    uint64_t enabled_workshop_ids[RML_MAX_ENABLED_WORKSHOP];
};

enum : uint32_t {
    PLAN_ALLOW_PROBABLE  = 1u << 0,
    PLAN_USE_SYMBOL_CACHE = 1u << 1,
    PLAN_FILTER_WORKSHOP = 1u << 2,
    PLAN_DEEP_TEST       = 1u << 3
};

enum : uint32_t {
    PROFILE_NORMAL = 8,
    PROFILE_DEEP_TEST = 9
};

enum : uint32_t {
    PLUGIN_FORCED        = 1u << 0,
    PLUGIN_TESMIO_LEGACY = 1u << 1,
    PLUGIN_RAW_NATIVE    = 1u << 2,
    PLUGIN_MANIFEST_IDENTITY = 1u << 3
};

struct PluginState;

struct SymbolRecord {
    const char* id;
    const char* display_name;
    uintptr_t address;
    uint32_t rva;
    RmlSymbolConfidence confidence;
    const char* resolver;
    const char* evidence;
    uint32_t xref_count;
    uint32_t function_size;
    BYTE entry_fingerprint[16];
    uint32_t entry_fingerprint_length;
    uint32_t from_symbol_cache;
    uint32_t observed_only;
    const char* temperature;
};


struct ServiceRecord {
    char name[96];
    uint32_t version;
    PluginState* owner;
    void* service;
};


struct PluginState {
    wchar_t* path;
    char id[128];
    char name[128];
    char version[32];
    char author[128];
    char dll_sha256[65];
    int32_t priority;
    uint32_t flags;
    uint32_t loaded;
    uint32_t failed;
    uint32_t unmanaged_init_changes;
    uint32_t unmanaged_start_changes;
    uint32_t unmanaged_init_pages;
    uint32_t unmanaged_start_pages;
    uint32_t first_unmanaged_rva;
    uint32_t legacy_api;
    uint32_t legacy_initialized;
    uint32_t legacy_hooks_installed;
    uint32_t legacy_hook_failures;
    uint32_t legacy_services_provided;
    void* legacy_start;
    void* legacy_host;
    HMODULE module;
    RmlHostApi host;
    char status[256];
};


struct SymbolCacheRuntimeStats {
    uint32_t cache_supplied;
    uint32_t cache_validated;
    uint32_t cache_rejected;
    uint32_t full_resolutions;
    uint32_t validation_ms;
    uint32_t full_resolution_ms;
    const char* state;
};

struct RuntimePerformanceStats {
    uint32_t worker_threads;
    uint32_t timers;
    uint32_t polling_loops;
    uint32_t bootstrap_ms;
    uint32_t steady_state_none;
};


struct DeepTextSnapshot {
    uint32_t page_count;
    uint32_t page_size;
    uint32_t text_rva;
    uint64_t hashes[RML_MAX_TEXT_PAGES];
};

struct DeepTestStats {
    uint32_t active;
    uint32_t plugins_observed;
    uint32_t host_hook_requests;
    uint32_t unmanaged_text_changes;
    uint32_t unmanaged_text_pages;
    uint64_t monitor_scans;
    uint64_t late_change_windows;
    uint32_t last_audit_issues;
    uint32_t bootstrap_status;
};

struct LegacyHookChainStepSnapshot {
    char plugin_id[128];
    char plugin_sha256[65];
    char label[128];
    uintptr_t detour;
    uintptr_t continuation;
    uintptr_t previous_effective_top;
    uintptr_t original_trampoline;
    uint32_t stolen_bytes;
    uint32_t install_order;
    uint32_t execution_order;
};

struct LegacyHookChainSnapshot {
    char type[16];
    char target_desc[160];
    char symbol_id[128];
    char integrity[64];
    char proof[64];
    char observed_top_owner[128];
    uintptr_t target;
    uint32_t rva;
    uint32_t stolen;
    uintptr_t effective_top;
    uintptr_t current_destination;
    uintptr_t original;
    uint32_t step_count;
    LegacyHookChainStepSnapshot steps[RML_MAX_CHAIN_STEPS];
};

extern HMODULE g_runtime_module;
extern wchar_t g_root_path[RML_MAX_PATH];
extern RuntimePlan g_plan;
extern SymbolRecord g_resource_get_symbol;
extern SymbolRecord* g_symbol_registry[RML_MAX_SYMBOLS];
extern uint32_t g_symbol_count;
extern ServiceRecord g_services[RML_MAX_SERVICES];
extern uint32_t g_service_count;
extern PluginState g_plugins[RML_MAX_PLUGINS];
extern uint32_t g_plugin_count;
extern volatile LONG g_log_lock;
extern HANDLE g_log_file;
extern SymbolCacheRuntimeStats g_symbol_cache_stats;
extern RuntimePerformanceStats g_performance_stats;
extern DeepTestStats g_deep_test_stats;

void* rml_alloc(size_t size, int zero = 1);
void rml_free(void* ptr);
size_t rml_strlen(const char* s);
size_t rml_wcslen(const wchar_t* s);
int rml_strcmp(const char* a, const char* b);
int rml_wstrcmp_i(const wchar_t* a, const wchar_t* b);
void rml_memcpy(void* dst, const void* src, size_t n);
void rml_memset(void* dst, int value, size_t n);
int rml_memeq(const void* a, const void* b, size_t n);
void rml_copy_str(char* dst, size_t cap, const char* src);
void rml_append_hex(char* dst, size_t cap, uint32_t value);
void rml_copy_wstr(wchar_t* dst, size_t cap, const wchar_t* src);
int rml_join_path(wchar_t* out, size_t cap, const wchar_t* a, const wchar_t* b);
int rml_utf16_to_utf8(const wchar_t* in, char* out, int cap);

void log_open(void);
void log_open_diagnostic(void);
void log_close(void);
void log_write(RmlLogLevel level, const char* component, const char* message);
void log_fault(RmlLogLevel level, const char* component, const char* marker, const char* message);
void log_fault_u32(RmlLogLevel level, const char* component, const char* marker, const char* prefix, uint32_t value);
void log_hex(RmlLogLevel level, const char* component, const char* prefix, uintptr_t value);
void log_u32(RmlLogLevel level, const char* component, const char* prefix, uint32_t value);
void log_u64(RmlLogLevel level, const char* component, const char* prefix, uint64_t value);

int plan_load(void);
void plan_free(void);
const char* plan_profile_name(void);
const char* plan_profile_short(void);
PlanCachedSymbol* plan_find_cached_symbol(const char* id);
int plan_workshop_enabled(uint64_t item_id);
int workshop_filter_install(void);
int resolve_symbols(void);
void symbol_registry_init(void);
SymbolRecord* symbol_registry_find(const char* id);
SymbolRecord* symbol_registry_find_by_address(void* address);
SymbolRecord* symbol_registry_restore_cached(const char* id, void* address, uint32_t rva, uint32_t function_size,
                                             uint32_t confidence, const BYTE* fingerprint, uint32_t fingerprint_length,
                                             const char* display_name, const char* resolver, const char* evidence,
                                             const char* temperature, uint32_t observed_only);
SymbolRecord* symbol_registry_at(uint32_t index);
uint32_t symbol_registry_count(void);
SymbolRecord* symbol_registry_observe_target(void* address, const char* label, const char* evidence);
uint64_t runtime_text_fingerprint(void);
int runtime_text_page_snapshot(DeepTextSnapshot* out);
uint32_t runtime_text_page_diff(const DeepTextSnapshot* before, const DeepTextSnapshot* after, uint32_t* first_rva);
uint32_t runtime_text_page_log_diff(const char* component, const char* phase, const DeepTextSnapshot* before, const DeepTextSnapshot* after);
int deep_monitor_start(void);
uint32_t tesmio_compat_deep_audit_state(const char* reason, int verbose);
int load_plugins(void);
int tesmio_compat_prepare(void);
int tesmio_compat_init_plugin(PluginState* state, PlanPlugin* plan);
int tesmio_compat_start_plugins(void);
int tesmio_compat_commit_pending_hooks(void);
uint32_t tesmio_compat_pending_hook_count(void);
uint32_t tesmio_compat_chain_count(void);
int tesmio_compat_chain_snapshot(uint32_t index, LegacyHookChainSnapshot* out);
int rml_service_provide(PluginState* owner, const char* name, uint32_t version, void* service);
void* rml_service_consume(const char* name, uint32_t version, RmlServiceInfo* out_info);
void rml_service_remove_owner(PluginState* owner);
void tesmio_compat_remove_services(PluginState* owner);
PlanPlugin* plan_find_plugin(const char* id);
void write_runtime_report(uint32_t bootstrap_status);
int install_menu_status(uint32_t bootstrap_status);

void journal_session_start(void);
void journal_plugin_event(const PlanPlugin* plugin, const char* event, const char* target,
                          const char* interaction, const char* evidence, const char* detail);
void journal_hook_event(const char* event, const char* target, const char* interaction,
                        const char* evidence, const char* detail);
void journal_bootstrap_complete(uint32_t bootstrap_status);

#endif
