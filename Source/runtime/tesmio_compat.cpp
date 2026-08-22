#include "runtime_internal.h"
#include "legacy_chain_policy.h"

static DeepTextSnapshot g_deep_legacy_before = {};
static DeepTextSnapshot g_deep_legacy_after = {};
#include <stdarg.h>

extern "C" int _fltused = 0;

// Clean-room compatibility surface for the public TesmioLoader plugin ABI.
// This is intentionally an adapter, not a fork of TesmioLoader. The ABI shape
// is reproduced so existing API 3/4 plugins can run while RML owns diagnostics,
// provenance and (where requested through this host) the physical patch operation.

struct TsmPluginInfoCompat {
    const char* name;
    const char* version;
};

typedef unsigned (*TsmApiVersionFn)(void);
typedef int (*TsmInitFn)(const void*, TsmPluginInfoCompat*);
typedef int (*TsmStartFn)(void);

struct TsmHostCompat {
    unsigned apiVersion;
    unsigned structSize;
    void* exeModule;
    unsigned char* exeBase;
    size_t exeSize;
    void* engineModule;
    const char* baseDir;
    const char* pluginDir;
    void (*log)(const char* fmt, ...);
    void** (*findIatSlot)(void* module, const char* dll, const char* fn);
    int (*patchIat)(void* module, const char* dll, const char* fn, void* detour, void** original, const char* label);
    int (*installInlineHook)(void* target, void* detour, void** trampoline, const unsigned char* expect, size_t stolen, const char* label);
    unsigned char* (*allocNear)(unsigned char* anchor, size_t size);
    int (*readablePtr)(const void* p, size_t n);
    long (*faultFilter)(const char* what, void* exceptionPointers);
    int (*configInt)(const char* iniName, const char* section, const char* key, int fallback);
    int (*configString)(const char* iniName, const char* section, const char* key, char* out, int outSize, const char* fallback);
    int (*provide)(const char* service, unsigned version, const void* iface);
    const void* (*consume)(const char* service, unsigned version);
    const char* vfsRoot;
};

static_assert(sizeof(TsmHostCompat) == 0x98, "Tesmio API 4 host ABI drift");

struct LegacyService {
    char name[128];
    unsigned version;
    const void* iface;
    PluginState* owner;
};

static TsmHostCompat g_tsm_host = {};
static char g_tsm_base_dir[RML_MAX_PATH * 3] = {};
static char g_tsm_plugin_dir[RML_MAX_PATH * 3] = {};
static char g_tsm_vfs_root[RML_MAX_PATH * 3] = {};
static wchar_t g_tsm_base_dir_w[RML_MAX_PATH] = {};
static wchar_t g_tsm_plugin_dir_w[RML_MAX_PATH] = {};
static PluginState* g_tsm_active_plugin = 0;
static LegacyService g_tsm_services[256] = {};
static uint32_t g_tsm_service_count = 0;
static uint32_t g_tsm_ready = 0;

static void tsm_log(const char* format, ...);

static int tsm_readable_ptr(const void* p, size_t n);
static int executable_ptr(const void* p);

// -----------------------------------------------------------------------------
// Legacy hook compatibility bridge
// -----------------------------------------------------------------------------
// Tesmio inline/IAT hooks receive a private continuation trampoline and point
// directly at the plugin detour, with no RML dispatcher on the hot path. RML
// records ownership and can chain later host-mediated requests by returning the
// previous detour as the next plugin's virtual original. Legacy trampolines live
// for the process lifetime because plugins retain their returned pointers.

#define RML_MAX_LEGACY_HOOK_TARGETS 128
#define RML_MAX_LEGACY_HOOK_REQUESTS 256
#define RML_MAX_LEGACY_IAT_TARGETS 128
#define RML_MAX_LEGACY_IAT_REQUESTS 256

struct LegacyInlineTarget {
    BYTE* target;
    size_t stolen;
    BYTE original[128];
    void* top_detour;
    BYTE* original_trampoline;
    char target_desc[160];
    uint32_t committed;
    // A compatible third-party detour may become part of the effective chain
    // after an RML-mediated owner has already installed. Preserve that fact
    // separately from top_detour: top_detour may later become another RML
    // owner, while the external hop still remains in its continuation path.
    uint32_t external_chain_adopted;
    void* external_destination;
    char external_owner[96];
    char external_shape[48];
};

struct LegacyInlineRequest {
    PluginState* owner;
    uint32_t target_index;
    void* detour;
    BYTE* continuation;
    void* previous_effective_top;
    BYTE* original_trampoline;
    size_t stolen;
    char label[128];
};

struct LegacyIatTarget {
    void** slot;
    void* original;
    void* top_detour;
    char target_desc[160];
    uint32_t committed;
    uint32_t external_chain_adopted;
    void* external_destination;
    char external_owner[96];
    char external_shape[48];
};

struct LegacyIatRequest {
    PluginState* owner;
    uint32_t target_index;
    void* detour;
    void* continuation;
    char label[128];
};

static LegacyInlineTarget g_legacy_inline_targets[RML_MAX_LEGACY_HOOK_TARGETS] = {};
static LegacyInlineRequest g_legacy_inline_requests[RML_MAX_LEGACY_HOOK_REQUESTS] = {};
static uint32_t g_legacy_inline_target_count = 0;
static uint32_t g_legacy_inline_request_count = 0;
static LegacyIatTarget g_legacy_iat_targets[RML_MAX_LEGACY_IAT_TARGETS] = {};
static LegacyIatRequest g_legacy_iat_requests[RML_MAX_LEGACY_IAT_REQUESTS] = {};
static uint32_t g_legacy_iat_target_count = 0;
static uint32_t g_legacy_iat_request_count = 0;

static BYTE* legacy_alloc_code(size_t required, const char* purpose) {
    (void)purpose;
    size_t size = required < 64u ? 64u : required;
    if (size > 4096u) return 0;
    // Strict Tesmio compatibility path. The API promises a callable
    // continuation immediately from installInlineHook, and established plugins
    // retain that address for the life of the process.
    return (BYTE*)VirtualAlloc(0, size, MEM_COMMIT | MEM_RESERVE, PAGE_EXECUTE_READWRITE);
}

static int legacy_seal_code(BYTE* mem, size_t size, const char* purpose) {
    (void)purpose;
    if (!mem || !size) return 0;
    // TesmioLoader's legacy contract leaves the tiny private trampoline RWX.
    // Keep that exact behaviour only for this compatibility surface; RML-native
    // executable memory remains W^X.
    return FlushInstructionCache(GetCurrentProcess(), mem, size) ? 1 : 0;
}

static void legacy_release_code(BYTE* mem) {
    if (mem) VirtualFree(mem, 0, MEM_RELEASE);
}

static BYTE* build_absolute_jump_continuation(void* destination, const char* purpose) {
    BYTE* mem = legacy_alloc_code(64u, purpose);
    if (!mem) return 0;
    rml_memset(mem, 0xCC, 64u);
    if (!rml_legacy_make_ff25_patch(mem, 14u, (uintptr_t)destination)) { legacy_release_code(mem); return 0; }
    if (!legacy_seal_code(mem, 64u, purpose)) { legacy_release_code(mem); return 0; }
    return mem;
}

static int legacy_inline_patch_points_to(const LegacyInlineTarget* target, const void* destination) {
    if (!target || !target->target || target->stolen < 14 || !tsm_readable_ptr(target->target, target->stolen)) return 0;
    const BYTE* p = target->target;
    if (p[0] != 0xFF || p[1] != 0x25 || p[2] || p[3] || p[4] || p[5]) return 0;
    uintptr_t actual = 0;
    rml_memcpy(&actual, p + 6, sizeof(actual));
    if (actual != (uintptr_t)destination) return 0;
    for (size_t i = 14; i < target->stolen; ++i) if (p[i] != 0x90) return 0;
    return 1;
}

// Decode common third-party detour forms without following arbitrary pointers.
// This is used to accommodate legacy plugins that directly chain/replace a
// host-mediated target after RML first hooks it. Only recognizable executable
// destinations are accepted; unknown modified instruction streams remain fatal.
static int legacy_decode_entry_detour(const BYTE* p, size_t available, void** destination, const char** shape) {
    if (destination) *destination = 0;
    if (shape) *shape = "unknown";
    if (!p || available < 5 || !tsm_readable_ptr(p, available < 16 ? available : 16)) return 0;

    uintptr_t dest = 0;
    if (available >= 14 && rml_legacy_decode_ff25_patch(p, available, &dest)) {
        if (shape) *shape = "FF25 absolute";
    } else if (available >= 6 && p[0] == 0xFF && p[1] == 0x25) {
        int32_t disp = 0; rml_memcpy(&disp, p + 2, sizeof(disp));
        const BYTE* slot = p + 6 + disp;
        if (!tsm_readable_ptr(slot, sizeof(uintptr_t))) return 0;
        rml_memcpy(&dest, slot, sizeof(dest));
        if (shape) *shape = "FF25 RIP-indirect";
    } else if (p[0] == 0xE9) {
        int32_t rel = 0; rml_memcpy(&rel, p + 1, sizeof(rel));
        dest = (uintptr_t)(p + 5 + rel);
        if (shape) *shape = "E9 rel32";
    } else if (available >= 12 && p[0] == 0x48 && p[1] == 0xB8 && p[10] == 0xFF && p[11] == 0xE0) {
        rml_memcpy(&dest, p + 2, sizeof(dest));
        if (shape) *shape = "MOV RAX/JMP RAX";
    } else {
        return 0;
    }
    if (!dest || !executable_ptr((const void*)dest)) return 0;
    if (destination) *destination = (void*)dest;
    return 1;
}

static int legacy_current_points_to(const LegacyInlineTarget* target, const void* destination) {
    if (legacy_inline_patch_points_to(target, destination)) return 1;
    void* observed = 0;
    if (!target || !legacy_decode_entry_detour(target->target, target->stolen, &observed, 0)) return 0;
    return observed == destination;
}

static int legacy_publish_inline(LegacyInlineTarget* record, void* new_detour, int expect_original) {
    if (!record || !record->target || !new_detour || record->stolen < 14 || record->stolen > sizeof(record->original)) return 0;
    if (!executable_ptr(record->target) || !executable_ptr(new_detour) || !tsm_readable_ptr(record->target, record->stolen)) return 0;
    if (expect_original) {
        if (!rml_memeq(record->target, record->original, record->stolen)) return 0;
    } else if (!legacy_current_points_to(record, record->top_detour)) {
        return 0;
    }

    BYTE patch[128];
    if (!rml_legacy_make_ff25_patch(patch, record->stolen, (uintptr_t)new_detour)) return 0;

    DWORD old = 0;
    if (!VirtualProtect(record->target, record->stolen, PAGE_EXECUTE_READWRITE, &old)) return 0;
    int still_expected = expect_original
        ? rml_memeq(record->target, record->original, record->stolen)
        : legacy_current_points_to(record, record->top_detour);
    if (!still_expected) {
        DWORD ignored = 0; VirtualProtect(record->target, record->stolen, old, &ignored);
        return 0;
    }
    rml_memcpy(record->target, patch, record->stolen);
    FlushInstructionCache(GetCurrentProcess(), record->target, record->stolen);
    DWORD ignored = 0;
    if (!VirtualProtect(record->target, record->stolen, old, &ignored)) return 0;
    return 1;
}

static int ascii_lower(int c) { return c >= 'A' && c <= 'Z' ? c + ('a' - 'A') : c; }
static int ascii_equal_i(const char* a, const char* b) {
    if (!a || !b) return 0;
    while (*a && *b) {
        if (ascii_lower((unsigned char)*a) != ascii_lower((unsigned char)*b)) return 0;
        ++a; ++b;
    }
    return *a == 0 && *b == 0;
}
static int is_exec_protect(DWORD p) {
    p &= 0xFFu;
    return p == PAGE_EXECUTE || p == PAGE_EXECUTE_READ || p == PAGE_EXECUTE_READWRITE || p == PAGE_EXECUTE_WRITECOPY;
}
// readablePtr is a very hot Tesmio host service for some legacy plugins.
// ForceNodes, for example, validates world/path/node pointers repeatedly while
// building its overlay scene. Calling VirtualQuery for every tiny pointer read
// can turn a useful safety check into tens of thousands of Win32 queries per
// frame. Keep Tesmio's observable semantics but cache positive region metadata
// for a very short window. The cache is per calling thread, positive-only, and
// expires after 8 ms; decommitted/guarded pages are never cached as readable.
// VirtualQuery itself only proves page mapping/protection (not C++ object
// lifetime), so this short reuse window does not weaken the API's object-lifetime
// guarantees: legacy plugins still own that validation just as they did before.
#define RML_READ_CACHE_THREADS 16
#define RML_READ_CACHE_REGIONS 32
#define RML_READ_CACHE_TTL_MS 8ull

struct TsmReadableRegionCacheEntry {
    uintptr_t base;
    uintptr_t end;
    ULONGLONG expires_ms;
};
struct TsmReadableThreadCache {
    DWORD thread_id;
    uint32_t cursor;
    TsmReadableRegionCacheEntry regions[RML_READ_CACHE_REGIONS];
};
static TsmReadableThreadCache g_tsm_read_cache[RML_READ_CACHE_THREADS] = {};
static volatile LONG g_tsm_read_cache_claim_lock = 0;

static TsmReadableThreadCache* readable_thread_cache() {
    DWORD tid = GetCurrentThreadId();
    for (uint32_t i = 0; i < RML_READ_CACHE_THREADS; ++i) {
        if (g_tsm_read_cache[i].thread_id == tid) return &g_tsm_read_cache[i];
    }
    // Thread slots are claimed only once in normal plugin use. Misses may race
    // during exotic multi-threaded plugin startup, so serialize only this cold
    // path; cache hits never take a lock.
    while (RmlInterlockedCompareExchange(&g_tsm_read_cache_claim_lock, 1, 0) != 0) Sleep(0);
    TsmReadableThreadCache* chosen = 0;
    for (uint32_t i = 0; i < RML_READ_CACHE_THREADS; ++i) {
        if (g_tsm_read_cache[i].thread_id == tid) { chosen = &g_tsm_read_cache[i]; break; }
        if (!chosen && g_tsm_read_cache[i].thread_id == 0) chosen = &g_tsm_read_cache[i];
    }
    if (chosen && chosen->thread_id == 0) {
        rml_memset(chosen, 0, sizeof(*chosen));
        chosen->thread_id = tid;
    }
    RmlInterlockedExchange(&g_tsm_read_cache_claim_lock, 0);
    return chosen;
}

static int readable_cached_region(TsmReadableThreadCache* cache, uintptr_t begin, uintptr_t end, ULONGLONG now) {
    if (!cache) return 0;
    for (uint32_t i = 0; i < RML_READ_CACHE_REGIONS; ++i) {
        const TsmReadableRegionCacheEntry& e = cache->regions[i];
        if (e.expires_ms >= now && begin >= e.base && end <= e.end && e.end > e.base) return 1;
    }
    return 0;
}

static void remember_readable_region(TsmReadableThreadCache* cache, const MEMORY_BASIC_INFORMATION& mbi, ULONGLONG now) {
    if (!cache || mbi.State != MEM_COMMIT || (mbi.Protect & (PAGE_NOACCESS | PAGE_GUARD))) return;
    uintptr_t begin = (uintptr_t)mbi.BaseAddress;
    uintptr_t end = begin + (uintptr_t)mbi.RegionSize;
    if (end <= begin) return;
    uint32_t slot = cache->cursor++ % RML_READ_CACHE_REGIONS;
    cache->regions[slot].base = begin;
    cache->regions[slot].end = end;
    cache->regions[slot].expires_ms = now + RML_READ_CACHE_TTL_MS;
}

static int tsm_readable_ptr(const void* p, size_t n) {
    // Preserve TesmioLoader's established API semantics: a non-null zero-length
    // range succeeds; non-empty ranges must be wholly committed and must not
    // cross a NOACCESS/GUARD page.
    if (!p) return 0;
    const BYTE* at = (const BYTE*)p;
    const BYTE* want = at + n;
    if (want < at) return 0;
    if (!n) return 1;

    TsmReadableThreadCache* cache = readable_thread_cache();
    ULONGLONG now = GetTickCount64();
    uintptr_t begin_value = (uintptr_t)at;
    uintptr_t end_value = (uintptr_t)want;
    if (readable_cached_region(cache, begin_value, end_value, now)) return 1;

    while (at < want) {
        MEMORY_BASIC_INFORMATION mbi = {};
        if (!VirtualQuery(at, &mbi, sizeof(mbi))) return 0;
        if (mbi.State != MEM_COMMIT) return 0;
        if (mbi.Protect & (PAGE_NOACCESS | PAGE_GUARD)) return 0;
        const BYTE* end = (const BYTE*)mbi.BaseAddress + mbi.RegionSize;
        if (end <= at) return 0;
        remember_readable_region(cache, mbi, now);
        at = end;
    }
    return 1;
}
static int executable_ptr(const void* p) {
    MEMORY_BASIC_INFORMATION mbi = {};
    return p && VirtualQuery(p, &mbi, sizeof(mbi)) && mbi.State == MEM_COMMIT &&
           !(mbi.Protect & (PAGE_GUARD | PAGE_NOACCESS)) && is_exec_protect(mbi.Protect);
}

static PluginState* plugin_from_callsite(void* return_address) {
    // Prefer the actual calling module. g_tsm_active_plugin is only a serialized
    // Init/Start fallback for helper-thunk cases; preferring the callsite avoids
    // misattributing a legacy plugin's worker-thread log while another plugin is
    // in its initialization phase.
    MEMORY_BASIC_INFORMATION mbi = {};
    if (return_address && VirtualQuery(return_address, &mbi, sizeof(mbi))) {
        HMODULE module = (HMODULE)mbi.AllocationBase;
        for (uint32_t i = 0; i < g_plugin_count; ++i) {
            if ((g_plugins[i].flags & PLUGIN_TESMIO_LEGACY) && g_plugins[i].module == module) return &g_plugins[i];
        }
    }
    return g_tsm_active_plugin;
}

static void legacy_component(PluginState* owner, char* out, size_t cap) {
    rml_copy_str(out, cap, "Tesmio:");
    size_t p = rml_strlen(out);
    const char* id = owner && owner->id[0] ? owner->id : "legacy";
    for (size_t i = 0; id[i] && p + 1 < cap; ++i) out[p++] = id[i];
    out[p] = 0;
}

static void legacy_fault(PluginState* owner, RmlLogLevel level, const char* marker, const char* message) {
    char comp[180];
    legacy_component(owner, comp, sizeof(comp));
    log_fault(level, comp, marker, message);
}

static void legacy_hook_terminal(PluginState* owner, const char* kind, const char* outcome) {
    if (!(g_plan.flags & PLAN_DEEP_TEST)) return;
    char comp[180]; legacy_component(owner, comp, sizeof(comp));
    char msg[256];
    rml_copy_str(msg, sizeof(msg), "DEEP TRACE ");
    size_t p = rml_strlen(msg);
    const char* parts[3] = { kind ? kind : "hook", " request END — ", outcome ? outcome : "UNKNOWN" };
    for (int j = 0; j < 3; ++j) {
        const char* q = parts[j];
        while (*q && p + 1 < sizeof(msg)) msg[p++] = *q++;
    }
    msg[p] = 0;
    log_write(RML_LOG_INFO, comp, msg);
}

static int legacy_hook_fail_early(PluginState* owner, const char* marker, const char* message) {
    if (owner) ++owner->legacy_hook_failures;
    legacy_fault(owner, RML_LOG_ERROR, marker ? marker : "HOOK FAILED", message);
    return 0;
}

static int legacy_hook_fail(PluginState* owner, const char* kind, const char* marker, const char* message) {
    if (owner) ++owner->legacy_hook_failures;
    legacy_fault(owner, RML_LOG_ERROR, marker ? marker : "HOOK FAILED", message);
    legacy_hook_terminal(owner, kind, marker ? marker : "HOOK FAILED");
    return 0;
}

static void legacy_hook_success(PluginState* owner, const char* kind, const char* marker, const char* message) {
    char comp[180]; legacy_component(owner, comp, sizeof(comp));
    log_fault(RML_LOG_INFO, comp, marker, message);
    legacy_hook_terminal(owner, kind, marker);
}

static void fmt_put_char(char* out, size_t cap, size_t* p, char c) { if (*p + 1 < cap) out[(*p)++] = c; }
static void fmt_put_str(char* out, size_t cap, size_t* p, const char* s) { if (s) while (*s && *p + 1 < cap) out[(*p)++] = *s++; }
static void fmt_put_uint(char* out, size_t cap, size_t* p, uint64_t v, int base, int upper, int min_width, char pad) {
    char tmp[40]; int n = 0;
    const char* digits = upper ? "0123456789ABCDEF" : "0123456789abcdef";
    // Legacy plugin log formats are untrusted input. Keep formatting bounded and
    // accept only the bases this shim actually implements.
    if (base != 16) base = 10;
    if (min_width < 0) min_width = 0;
    if (min_width > 64) min_width = 64;
    do { tmp[n++] = digits[v % (uint32_t)base]; v /= (uint32_t)base; } while (v && n < (int)sizeof(tmp));
    // Pad with an independent counter so width formatting cannot stall.
    for (int i = n; i < min_width; ++i) fmt_put_char(out, cap, p, pad);
    while (n) fmt_put_char(out, cap, p, tmp[--n]);
}
static void fmt_put_int(char* out, size_t cap, size_t* p, int64_t v, int width, char pad) {
    if (v < 0) { fmt_put_char(out, cap, p, '-'); uint64_t m = (uint64_t)(-(v + 1)) + 1u; fmt_put_uint(out, cap, p, m, 10, 0, width > 0 ? width - 1 : 0, pad); }
    else fmt_put_uint(out, cap, p, (uint64_t)v, 10, 0, width, pad);
}
static void fmt_put_double(char* out, size_t cap, size_t* p, double value, int precision) {
    if (precision < 0) precision = 6;
    if (precision > 9) precision = 9;
    if (value < 0.0) { fmt_put_char(out, cap, p, '-'); value = -value; }
    uint64_t whole = (uint64_t)value;
    fmt_put_uint(out, cap, p, whole, 10, 0, 0, '0');
    if (!precision) return;
    fmt_put_char(out, cap, p, '.');
    double fraction = value - (double)whole;
    uint64_t scale = 1;
    for (int i = 0; i < precision; ++i) scale *= 10u;
    uint64_t frac = (uint64_t)(fraction * (double)scale + 0.5);
    if (frac >= scale) frac = scale - 1; // log formatting only; avoid carry complexity
    fmt_put_uint(out, cap, p, frac, 10, 0, precision, '0');
}

static void tsm_log(const char* format, ...) {
    PluginState* owner = plugin_from_callsite(__builtin_return_address(0));
    char component[180]; legacy_component(owner, component, sizeof(component));
    if (!format) { log_write(RML_LOG_INFO, component, "(null legacy log format)"); return; }
    char out[2048]; size_t p = 0;
    va_list ap; va_start(ap, format);
    for (size_t i = 0; format[i] && p + 1 < sizeof(out); ++i) {
        if (format[i] != '%') { fmt_put_char(out, sizeof(out), &p, format[i]); continue; }
        ++i;
        if (!format[i]) break;
        if (format[i] == '%') { fmt_put_char(out, sizeof(out), &p, '%'); continue; }
        char pad = ' '; int width = 0; int precision = -1; int long_count = 0; int size_mod = 0;
        if (format[i] == '0') { pad = '0'; ++i; }
        while (format[i] >= '0' && format[i] <= '9') { width = width * 10 + (format[i] - '0'); ++i; }
        if (format[i] == '.') { precision = 0; ++i; while (format[i] >= '0' && format[i] <= '9') { precision = precision * 10 + (format[i] - '0'); ++i; } }
        while (format[i] == 'l') { ++long_count; ++i; }
        if (format[i] == 'z') { size_mod = 1; ++i; }
        char spec = format[i];
        switch (spec) {
            case 's': {
                if (long_count) {
                    const wchar_t* ws = va_arg(ap, const wchar_t*);
                    char tmp[768] = {};
                    if (ws && tsm_readable_ptr(ws, sizeof(wchar_t))) rml_utf16_to_utf8(ws, tmp, (int)sizeof(tmp));
                    fmt_put_str(out, sizeof(out), &p, ws ? tmp : "(null)");
                } else {
                    const char* v = va_arg(ap, const char*);
                    fmt_put_str(out, sizeof(out), &p, v ? v : "(null)");
                }
                break;
            }
            case 'c': fmt_put_char(out, sizeof(out), &p, (char)va_arg(ap, int)); break;
            case 'd': case 'i': {
                int64_t v = long_count >= 2 ? va_arg(ap, long long) : (long_count == 1 ? va_arg(ap, long) : (size_mod ? (int64_t)va_arg(ap, ptrdiff_t) : va_arg(ap, int)));
                fmt_put_int(out, sizeof(out), &p, v, width, pad); break;
            }
            case 'u': {
                uint64_t v = long_count >= 2 ? va_arg(ap, unsigned long long) : (long_count == 1 ? va_arg(ap, unsigned long) : (size_mod ? (uint64_t)va_arg(ap, size_t) : va_arg(ap, unsigned)));
                fmt_put_uint(out, sizeof(out), &p, v, 10, 0, width, pad); break;
            }
            case 'x': case 'X': {
                uint64_t v = long_count >= 2 ? va_arg(ap, unsigned long long) : (long_count == 1 ? va_arg(ap, unsigned long) : (size_mod ? (uint64_t)va_arg(ap, size_t) : va_arg(ap, unsigned)));
                fmt_put_uint(out, sizeof(out), &p, v, 16, spec == 'X', width, pad); break;
            }
            case 'p': {
                uintptr_t v = (uintptr_t)va_arg(ap, void*); fmt_put_str(out, sizeof(out), &p, "0x"); fmt_put_uint(out, sizeof(out), &p, v, 16, 1, 16, '0'); break;
            }
            case 'f': case 'F': fmt_put_double(out, sizeof(out), &p, va_arg(ap, double), precision); break;
            default: fmt_put_char(out, sizeof(out), &p, '%'); fmt_put_char(out, sizeof(out), &p, spec); break;
        }
    }
    va_end(ap); out[p] = 0;
    log_write(RML_LOG_INFO, component, out);
}

static uint16_t pe_u16(const BYTE* p) { return (uint16_t)p[0] | ((uint16_t)p[1] << 8); }
static uint32_t pe_u32(const BYTE* p) { return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24); }
static uint64_t pe_u64(const BYTE* p) { return (uint64_t)pe_u32(p) | ((uint64_t)pe_u32(p + 4) << 32); }

static int module_image_size(void* module, uint32_t* out_size) {
    BYTE* base = (BYTE*)module;
    if (!base || !tsm_readable_ptr(base, 0x100) || pe_u16(base) != 0x5A4D) return 0;
    uint32_t lf = pe_u32(base + 0x3C);
    if (lf > 0x100000 || !tsm_readable_ptr(base + lf, 0x200) || pe_u32(base + lf) != 0x4550) return 0;
    BYTE* opt = base + lf + 24;
    if (pe_u16(opt) != 0x20B) return 0;
    uint32_t size = pe_u32(opt + 56);
    if (size < 0x10000 || size > 0x80000000u) return 0;
    if (out_size) *out_size = size;
    return 1;
}

static void** tsm_find_iat_slot(void* module, const char* dll, const char* fn) {
    if (!module || !dll || !fn) return 0;
    BYTE* base = (BYTE*)module; uint32_t image_size = 0;
    if (!module_image_size(module, &image_size)) return 0;
    uint32_t lf = pe_u32(base + 0x3C); BYTE* opt = base + lf + 24;
    uint32_t import_rva = pe_u32(opt + 112 + 8); uint32_t import_size = pe_u32(opt + 112 + 12);
    if (!import_rva || import_rva >= image_size || import_size < 20) return 0;
    for (uint32_t di = 0; di < 4096; ++di) {
        uint32_t off = import_rva + di * 20u;
        if (off + 20u > image_size) return 0;
        BYTE* d = base + off;
        uint32_t original_thunk = pe_u32(d + 0); uint32_t name_rva = pe_u32(d + 12); uint32_t first_thunk = pe_u32(d + 16);
        if (!original_thunk && !name_rva && !first_thunk) break;
        if (!name_rva || name_rva >= image_size || !first_thunk || first_thunk >= image_size) continue;
        const char* imported_dll = (const char*)(base + name_rva);
        if (!tsm_readable_ptr(imported_dll, 1) || !ascii_equal_i(imported_dll, dll)) continue;
        uint32_t names = original_thunk ? original_thunk : first_thunk;
        for (uint32_t i = 0; i < 65536; ++i) {
            uint64_t nrva_off = (uint64_t)names + (uint64_t)i * 8u;
            uint64_t iat_off = (uint64_t)first_thunk + (uint64_t)i * 8u;
            if (nrva_off + 8 > image_size || iat_off + 8 > image_size) break;
            uint64_t thunk = pe_u64(base + (uint32_t)nrva_off);
            if (!thunk) break;
            if (thunk & 0x8000000000000000ull) continue;
            uint32_t by_name = (uint32_t)thunk;
            if (by_name + 3 >= image_size) continue;
            const char* name = (const char*)(base + by_name + 2);
            if (tsm_readable_ptr(name, 1) && rml_strcmp(name, fn) == 0)
                return (void**)(base + (uint32_t)iat_off);
        }
    }
    return 0;
}

static void append_target_description(char* out, size_t cap, const void* target) {
    rml_copy_str(out, cap, "0x");
    size_t p = rml_strlen(out); uintptr_t value = (uintptr_t)target; char tmp[32]; int n = 0;
    const char* hex = "0123456789ABCDEF";
    do { tmp[n++] = hex[value & 0xFu]; value >>= 4; } while (value && n < 32);
    while (n && p + 1 < cap) out[p++] = tmp[--n]; out[p] = 0;
    BYTE* game = (BYTE*)GetModuleHandleW(0); uint32_t size = 0;
    if (game && module_image_size(game, &size) && (uintptr_t)target >= (uintptr_t)game && (uintptr_t)target < (uintptr_t)game + size) {
        uintptr_t rva = (uintptr_t)target - (uintptr_t)game;
        rml_copy_str(out, cap, "SOVIET64.exe+0x"); p = rml_strlen(out); n = 0; value = rva;
        do { tmp[n++] = hex[value & 0xFu]; value >>= 4; } while (value && n < 32);
        while (n && p + 1 < cap) out[p++] = tmp[--n]; out[p] = 0;
    }
}


static void deep_log_bytes(const char* component, const char* prefix, const BYTE* data, size_t count) {
    if (!(g_plan.flags & PLAN_DEEP_TEST) || !data || !count) return;
    if (count > 32u) count = 32u;
    char msg[512]; size_t p = 0;
    fmt_put_str(msg, sizeof(msg), &p, prefix ? prefix : "bytes: ");
    for (size_t i = 0; i < count && p + 4 < sizeof(msg); ++i) {
        if (i) fmt_put_char(msg, sizeof(msg), &p, ' ');
        fmt_put_uint(msg, sizeof(msg), &p, data[i], 16, 1, 2, '0');
    }
    msg[p] = 0;
    log_write(RML_LOG_INFO, component ? component : "DeepTrace", msg);
}


static uint32_t deep_classify_text_changes(PluginState* owner, const DeepTextSnapshot* before,
                                           const DeepTextSnapshot* after, uint32_t inline_request_begin,
                                           const char* phase, uint32_t* managed_pages, uint32_t* first_unmanaged_rva) {
    if (managed_pages) *managed_pages = 0;
    if (first_unmanaged_rva) *first_unmanaged_rva = 0;
    if (!(g_plan.flags & PLAN_DEEP_TEST) || !before || !after || before->page_count != after->page_count ||
        before->page_size != after->page_size || before->text_rva != after->text_rva) return 0;
    BYTE* image = (BYTE*)GetModuleHandleW(0);
    uint32_t unmanaged = 0;
    char comp[180]; legacy_component(owner, comp, sizeof(comp));
    for (uint32_t page = 0; page < before->page_count; ++page) {
        if (before->hashes[page] == after->hashes[page]) continue;
        uint32_t page_rva = before->text_rva + page * before->page_size;
        uintptr_t page_va = image ? (uintptr_t)(image + page_rva) : 0;
        int managed = 0;
        for (uint32_t r = inline_request_begin; r < g_legacy_inline_request_count; ++r) {
            LegacyInlineRequest* req = &g_legacy_inline_requests[r];
            if (owner && req->owner != owner) continue;
            if (req->target_index >= g_legacy_inline_target_count) continue;
            uintptr_t target_va = (uintptr_t)g_legacy_inline_targets[req->target_index].target;
            if (page_va && target_va >= page_va && target_va < page_va + before->page_size) {
                managed = 1;
                break;
            }
        }
        log_write(managed ? RML_LOG_INFO : RML_LOG_WARN, comp,
                  managed ? "DEEP TRACE changed page classification: MANAGED by a new host-mediated inline target in this lifecycle phase."
                          : "DEEP TRACE changed page classification: UNMANAGED — no new host-mediated inline target in this lifecycle phase maps to this page.");
        if (phase) log_write(RML_LOG_INFO, comp, phase);
        log_hex(managed ? RML_LOG_INFO : RML_LOG_WARN, comp, "Classified page RVA: ", page_rva);
        if (managed) {
            if (managed_pages) ++*managed_pages;
        } else {
            if (!unmanaged && first_unmanaged_rva) *first_unmanaged_rva = page_rva;
            ++unmanaged;
        }
    }
    return unmanaged;
}

static int tsm_patch_iat(void* module, const char* dll, const char* fn, void* detour, void** original, const char* label) {
    PluginState* owner = g_tsm_active_plugin ? g_tsm_active_plugin : plugin_from_callsite(__builtin_return_address(0));
    PlanPlugin* plan = owner ? plan_find_plugin(owner->id) : 0;
    if (!module || !detour || !executable_ptr(detour)) {
        return legacy_hook_fail_early(owner, "HOOK REJECTED", "IAT hook request rejected because the module/detour is not safely usable.");
    }
    void** slot = tsm_find_iat_slot(module, dll, fn);
    if (!slot || !tsm_readable_ptr(slot, sizeof(void*))) {
        return legacy_hook_fail_early(owner, "HOOK REJECTED", "Requested IAT import slot could not be resolved/read.");
    }
    if (g_plan.flags & PLAN_DEEP_TEST) {
        ++g_deep_test_stats.host_hook_requests;
        char comp[180]; legacy_component(owner, comp, sizeof(comp));
        log_write(RML_LOG_INFO, comp, "DEEP TRACE IAT hook request BEGIN");
        log_write(RML_LOG_INFO, comp, label ? label : (fn ? fn : "unnamed IAT hook"));
        log_write(RML_LOG_INFO, comp, dll ? dll : "(unknown import DLL)");
        log_write(RML_LOG_INFO, comp, fn ? fn : "(unknown import function)");
        log_hex(RML_LOG_INFO, comp, "IAT slot: ", (uintptr_t)slot);
        log_hex(RML_LOG_INFO, comp, "Current slot destination: ", (uintptr_t)(*slot));
        log_hex(RML_LOG_INFO, comp, "Requested detour: ", (uintptr_t)detour);
        log_write(RML_LOG_INFO, comp, "DEEP TRACE IAT hook request will be physically applied in this manual forensic session.");
    }
    if (g_legacy_iat_request_count >= RML_MAX_LEGACY_IAT_REQUESTS) {
        return legacy_hook_fail(owner, "IAT hook", "HOOK REJECTED", "IAT hook request table is full.");
    }

    uint32_t target_index = 0xFFFFFFFFu;
    for (uint32_t i = 0; i < g_legacy_iat_target_count; ++i) {
        if (g_legacy_iat_targets[i].slot == slot) { target_index = i; break; }
    }

    void* continuation = 0;
    int first_owner = target_index == 0xFFFFFFFFu;
    if (first_owner) {
        if (g_legacy_iat_target_count >= RML_MAX_LEGACY_IAT_TARGETS) {
            return legacy_hook_fail(owner, "IAT hook", "HOOK REJECTED", "IAT target table is full.");
        }
        target_index = g_legacy_iat_target_count++;
        LegacyIatTarget* target = &g_legacy_iat_targets[target_index];
        target->slot = slot;
        target->original = *slot;
        target->top_detour = 0;
        target->external_chain_adopted = 0;
        target->external_destination = 0;
        target->external_owner[0] = 0;
        target->external_shape[0] = 0;
        append_target_description(target->target_desc, sizeof(target->target_desc), slot);
        continuation = target->original;

        DWORD old = 0;
        if (!VirtualProtect(slot, sizeof(void*), PAGE_READWRITE, &old)) { --g_legacy_iat_target_count; return legacy_hook_fail(owner, "IAT hook", "HOOK FAILED", "IAT slot could not be made writable."); }
        if (*slot != target->original) { DWORD ignored = 0; VirtualProtect(slot, sizeof(void*), old, &ignored); --g_legacy_iat_target_count; return legacy_hook_fail(owner, "IAT hook", "HOOK TARGET CHANGED", "IAT slot changed during the hook transaction."); }
        *slot = detour;
        DWORD ignored = 0;
        if (!VirtualProtect(slot, sizeof(void*), old, &ignored)) { *slot = target->original; VirtualProtect(slot, sizeof(void*), old, &ignored); --g_legacy_iat_target_count; return legacy_hook_fail(owner, "IAT hook", "HOOK FAILED", "IAT hook protection restore failed and the patch was rolled back."); }
        target->top_detour = detour;
        target->committed = 1;
    } else {
        LegacyIatTarget* target = &g_legacy_iat_targets[target_index];
        if (!target->committed || *slot != target->top_detour) return legacy_hook_fail(owner, "IAT hook", "HOOK CONFLICT", "Shared IAT hook found an unexpected current slot destination.");
        continuation = target->top_detour;
        DWORD old = 0;
        if (!VirtualProtect(slot, sizeof(void*), PAGE_READWRITE, &old)) return legacy_hook_fail(owner, "IAT hook", "HOOK FAILED", "Shared IAT slot could not be made writable.");
        if (*slot != target->top_detour) { DWORD ignored = 0; VirtualProtect(slot, sizeof(void*), old, &ignored); return legacy_hook_fail(owner, "IAT hook", "HOOK TARGET CHANGED", "Shared IAT slot changed during the hook transaction."); }
        *slot = detour;
        DWORD ignored = 0;
        if (!VirtualProtect(slot, sizeof(void*), old, &ignored)) { *slot = target->top_detour; VirtualProtect(slot, sizeof(void*), old, &ignored); return legacy_hook_fail(owner, "IAT hook", "HOOK FAILED", "Shared IAT hook protection restore failed and the patch was rolled back."); }
        target->top_detour = detour;
    }

    LegacyIatRequest* req = &g_legacy_iat_requests[g_legacy_iat_request_count++];
    req->owner = owner;
    req->target_index = target_index;
    req->detour = detour;
    req->continuation = continuation;
    rml_copy_str(req->label, sizeof(req->label), label ? label : (fn ? fn : "unnamed"));
    if (original) *original = continuation;
    if (owner) { owner->legacy_hooks_installed++; rml_copy_str(owner->status, sizeof(owner->status), owner->legacy_hook_failures ? "TESMIO ACTIVE — DEGRADED" : "TESMIO ACTIVE"); }
    journal_plugin_event(plan, "legacy_iat_hook_committed", g_legacy_iat_targets[target_index].target_desc,
                         "iat-patch", first_owner ? "tesmio-fidelity-direct" : "rml-virtual-bridge",
                         label ? label : (fn ? fn : "unnamed"));
    if (g_plan.flags & PLAN_DEEP_TEST) {
        char comp[180]; legacy_component(owner, comp, sizeof(comp));
        log_u32(RML_LOG_INFO, comp, "DEEP TRACE IAT target index: ", target_index);
        log_write(RML_LOG_INFO, comp, first_owner ? "DEEP TRACE IAT chain role: first owner" : "DEEP TRACE IAT chain role: chained after existing owner");
        log_hex(RML_LOG_INFO, comp, "Continuation/original returned to plugin: ", (uintptr_t)continuation);
        log_hex(RML_LOG_INFO, comp, "Effective top detour after install: ", (uintptr_t)g_legacy_iat_targets[target_index].top_detour);
    }
    legacy_hook_success(owner, "IAT hook", first_owner ? "HOOK INSTALLED" : "HOOK CHAINED",
                        first_owner ? "Legacy IAT hook installed." : "Legacy IAT hook chained after the previous effective destination.");
    return 1;
}

static unsigned char* tsm_alloc_near(unsigned char* anchor, size_t size) {
    PluginState* owner = plugin_from_callsite(__builtin_return_address(0));
    if (!anchor || !size || size > 16u * 1024u * 1024u) return 0;
    uintptr_t target = (uintptr_t)anchor, range = 0x70000000ull;
    uintptr_t low = target > range ? target - range : 0x10000ull;
    uintptr_t high = target + range; if (high < target) high = ~(uintptr_t)0;
    low = (low + 0xFFFFull) & ~0xFFFFull;
    MEMORY_BASIC_INFORMATION mbi = {};
    for (uintptr_t p = low; p < high;) {
        if (!VirtualQuery((LPCVOID)p, &mbi, sizeof(mbi))) break;
        uintptr_t region = (uintptr_t)mbi.BaseAddress, next = region + mbi.RegionSize; if (next <= p) break;
        if (mbi.State == MEM_FREE) {
            uintptr_t candidate = (region + 0xFFFFull) & ~0xFFFFull;
            if (candidate >= low && candidate + size > candidate && candidate + size <= next && candidate + size <= high) {
                // Compatibility mode follows the hook helper contract:
                // the caller may immediately emit and execute a stub. RML-native
                // APIs continue to use W^X allocations instead.
                unsigned char* mem = (unsigned char*)VirtualAlloc((LPVOID)candidate, size, MEM_RESERVE | MEM_COMMIT, PAGE_EXECUTE_READWRITE);
                if (mem) {
                    tsm_log("RML legacy allocNear granted %u bytes of temporary RWX-compatible memory", (unsigned)size);
                    if (g_plan.flags & PLAN_DEEP_TEST) {
                        char comp[180]; legacy_component(owner, comp, sizeof(comp));
                        log_hex(RML_LOG_INFO, comp, "DEEP TRACE allocNear anchor: ", (uintptr_t)anchor);
                        log_u64(RML_LOG_INFO, comp, "DEEP TRACE allocNear requested bytes: ", (uint64_t)size);
                        log_hex(RML_LOG_INFO, comp, "DEEP TRACE allocNear result: ", (uintptr_t)mem);
                        log_u64(RML_LOG_INFO, comp, "DEEP TRACE allocNear distance from anchor: ", (uint64_t)((uintptr_t)mem > target ? (uintptr_t)mem - target : target - (uintptr_t)mem));
                    }
                    journal_plugin_event(owner ? plan_find_plugin(owner->id) : 0, "legacy_alloc_near", 0, "executable-allocation", "tesmio-shim", "Legacy API requested near executable memory");
                    return mem;
                }
            }
        }
        p = next;
    }
    if (g_plan.flags & PLAN_DEEP_TEST) {
        char comp[180]; legacy_component(owner, comp, sizeof(comp));
        log_hex(RML_LOG_WARN, comp, "DEEP TRACE allocNear failed for anchor: ", (uintptr_t)anchor);
        log_u64(RML_LOG_WARN, comp, "DEEP TRACE allocNear requested bytes: ", (uint64_t)size);
    }
    return 0;
}

static int tsm_install_inline_hook_impl(void* target, void* detour, void** trampoline,
                                        const unsigned char* expect, size_t stolen, const char* label) {
    PluginState* owner = g_tsm_active_plugin ? g_tsm_active_plugin : plugin_from_callsite(__builtin_return_address(0));
    PlanPlugin* plan = owner ? plan_find_plugin(owner->id) : 0;
    char target_desc[160]; append_target_description(target_desc, sizeof(target_desc), target);
    if (!target || !detour || !trampoline || stolen < 14 || stolen > 128 ||
        !executable_ptr(target) || !executable_ptr(detour) || !tsm_readable_ptr(target, stolen)) {
        return legacy_hook_fail_early(owner, "HOOK REJECTED", "Inline hook request rejected because the target/detour/stolen-byte range is not safely usable.");
    }
    if (g_legacy_inline_request_count >= RML_MAX_LEGACY_HOOK_REQUESTS) {
        return legacy_hook_fail_early(owner, "HOOK REJECTED", "Inline hook request rejected because the compatibility-host request table is full.");
    }
    if (expect && !tsm_readable_ptr(expect, stolen)) {
        return legacy_hook_fail_early(owner, "HOOK REJECTED", "Inline hook request supplied unreadable expected-byte data.");
    }

    symbol_registry_observe_target(target, label ? label : "legacy inline target", "tesmio-host-inline-hook");

    if (g_plan.flags & PLAN_DEEP_TEST) {
        ++g_deep_test_stats.host_hook_requests;
        char comp[180]; legacy_component(owner, comp, sizeof(comp));
        log_write(RML_LOG_INFO, comp, "DEEP TRACE inline hook request BEGIN");
        log_write(RML_LOG_INFO, comp, label ? label : "unnamed inline hook");
        log_write(RML_LOG_INFO, comp, target_desc);
        log_hex(RML_LOG_INFO, comp, "Requested detour: ", (uintptr_t)detour);
        log_u64(RML_LOG_INFO, comp, "Requested stolen bytes: ", (uint64_t)stolen);
        deep_log_bytes(comp, "Current target bytes: ", (const BYTE*)target, stolen < 32u ? stolen : 32u);
        if (expect) deep_log_bytes(comp, "Expected target bytes: ", (const BYTE*)expect, stolen < 32u ? stolen : 32u);
        log_write(RML_LOG_INFO, comp, "DEEP TRACE inline hook request will be physically applied in this manual forensic session.");
    }

    uint32_t target_index = 0xFFFFFFFFu;
    for (uint32_t i = 0; i < g_legacy_inline_target_count; ++i) {
        if (g_legacy_inline_targets[i].target == (BYTE*)target) { target_index = i; break; }
    }

    BYTE* continuation = 0;
    void* previous_effective_top = 0;
    int first_owner = target_index == 0xFFFFFFFFu;
    if (first_owner) {
        if (g_legacy_inline_target_count >= RML_MAX_LEGACY_HOOK_TARGETS) {
            return legacy_hook_fail(owner, "inline hook", "HOOK REJECTED", "Inline hook target table is full.");
        }
        // Tesmio's contract compares the caller-supplied expected bytes against
        // the target as it exists at the instant of installation. For a first
        // owner that is normally the vanilla prologue.
        if (!rml_legacy_expect_matches_current((const BYTE*)target, expect, stolen)) {
            journal_plugin_event(plan, "legacy_hook_refused", target_desc, "inline-detour", "expected-bytes-mismatch", label ? label : "unnamed");
            return legacy_hook_fail(owner, "inline hook", "HOOK TARGET CHANGED", "Inline hook expected bytes do not match the current executable target.");
        }

        size_t tramp_size = stolen + 14u;
        size_t alloc_size = tramp_size < 64u ? 64u : tramp_size;
        BYTE* tramp = legacy_alloc_code(alloc_size, label ? label : "legacy inline original");
        if (!tramp) {
            return legacy_hook_fail(owner, "inline hook", "HOOK FAILED", "Could not allocate the continuation trampoline for an inline hook.");
        }
        rml_memset(tramp, 0xCC, alloc_size);
        rml_memcpy(tramp, target, stolen);
        BYTE* back = tramp + stolen;
        if (!rml_legacy_make_ff25_patch(back, 14u, (uintptr_t)target + stolen)) {
            legacy_release_code(tramp);
            return legacy_hook_fail(owner, "inline hook", "HOOK FAILED", "Could not build the original continuation trampoline.");
        }
        if (!legacy_seal_code(tramp, alloc_size, label ? label : "legacy inline original")) {
            legacy_release_code(tramp);
            return legacy_hook_fail(owner, "inline hook", "HOOK FAILED", "Continuation trampoline could not be finalized for execution.");
        }

        target_index = g_legacy_inline_target_count++;
        LegacyInlineTarget* record = &g_legacy_inline_targets[target_index];
        record->target = (BYTE*)target;
        record->stolen = stolen;
        rml_memcpy(record->original, target, stolen);
        record->top_detour = 0;
        record->original_trampoline = tramp;
        rml_copy_str(record->target_desc, sizeof(record->target_desc), target_desc);
        record->committed = 0;
        record->external_chain_adopted = 0;
        record->external_destination = 0;
        record->external_owner[0] = 0;
        record->external_shape[0] = 0;
        continuation = tramp;
        previous_effective_top = tramp;

        if (!legacy_publish_inline(record, detour, 1)) {
            --g_legacy_inline_target_count;
            legacy_release_code(tramp);
            return legacy_hook_fail(owner, "inline hook", "HOOK FAILED", "Inline detour could not be published to the game target.");
        }
        record->top_detour = detour;
        record->committed = 1;
    } else {
        LegacyInlineTarget* record = &g_legacy_inline_targets[target_index];
        if (!rml_legacy_shared_stolen_compatible(record->stolen, stolen)) {
            journal_plugin_event(plan, "legacy_hook_refused", target_desc, "inline-detour", "shared-stolen-span-widens-first-owner", label ? label : "unnamed");
            return legacy_hook_fail(owner, "inline hook", "HOOK CONFLICT", "Shared inline-hook request would widen beyond the first owner's preserved stolen-byte boundary.");
        }
        if (!record->committed) {
            return legacy_hook_fail(owner, "inline hook", "HOOK CONFLICT", "Shared inline-hook request reached an incomplete target record.");
        }
        if ((g_plan.flags & PLAN_DEEP_TEST) && stolen < record->stolen) {
            char comp[180]; legacy_component(owner, comp, sizeof(comp));
            log_u64(RML_LOG_INFO, comp, "DEEP TRACE shared target canonical stolen bytes: ", (uint64_t)record->stolen);
            log_u64(RML_LOG_INFO, comp, "DEEP TRACE incoming chain-request stolen bytes: ", (uint64_t)stolen);
            log_write(RML_LOG_INFO, comp, "DEEP TRACE shorter shared span accepted; RML will preserve the first owner's canonical patch width and return the previous effective top as the new continuation.");
        }

        // The chain registry is authoritative. First prove that the live target
        // still represents the effective top RML has recorded. Existing FF25
        // detours installed by RML are therefore a valid state, not evidence of
        // corruption merely because the original vanilla bytes are gone.
        if (!legacy_inline_patch_points_to(record, record->top_detour)) {
            void* observed = 0; const char* shape = 0;
            if (rml_memeq(record->target, record->original, record->stolen)) {
                // If the target temporarily returns to vanilla bytes, restore the recorded
                // chain before evaluating the incoming plugin's expectation.
                if (!legacy_publish_inline(record, record->top_detour, 1)) {
                    journal_plugin_event(plan, "legacy_hook_refused", target_desc, "inline-detour", "restoration-repair-failed", label ? label : "unnamed");
                    return legacy_hook_fail(owner, "inline hook", "HOOK FAILED", "A recoverable vanilla restoration was detected but the known detour could not be restored.");
                }
                log_write(RML_LOG_WARN, "TesmioCompat", "A previously mediated target had been restored to vanilla bytes; RML restored the known legacy top detour before evaluating the next chain request.");
            } else if (legacy_decode_entry_detour(record->target, record->stolen, &observed, &shape)) {
                record->top_detour = observed;
                record->external_chain_adopted = 1;
                record->external_destination = observed;
                PluginState* external_owner = plugin_from_callsite(observed);
                rml_copy_str(record->external_owner, sizeof(record->external_owner), external_owner ? external_owner->id : "");
                rml_copy_str(record->external_shape, sizeof(record->external_shape), shape ? shape : "recognized detour");
                log_write(RML_LOG_WARN, "TesmioCompat", "Recognized a later third-party detour on an RML-known target; adopting the current executable destination as the effective top of the legacy chain.");
                if (shape) log_write(RML_LOG_INFO, "TesmioCompat", shape);
                journal_plugin_event(plan, "legacy_external_chain_adopted", target_desc, "inline-detour", shape ? shape : "recognized-detour", label ? label : "unnamed");
            } else {
                journal_plugin_event(plan, "legacy_hook_refused", target_desc, "inline-detour", "unexpected-current-detour", label ? label : "unnamed");
                return legacy_hook_fail(owner, "inline hook", "HOOK CONFLICT", "The current executable target contains an unrecognized modification and cannot be safely chained.");
            }
        }

        // Shared requests compare against the current effective target bytes so a
        // plugin can explicitly accept a preceding detour and receive that
        // effective chain as its continuation.
        if (!rml_legacy_expect_matches_current(record->target, expect, stolen)) {
            journal_plugin_event(plan, "legacy_hook_refused", target_desc, "inline-detour", "current-expected-bytes-mismatch", label ? label : "unnamed");
            return legacy_hook_fail(owner, "inline hook", "HOOK TARGET CHANGED", "Shared inline-hook request does not accept the current effective target bytes; refusing to chain.");
        }

        previous_effective_top = record->top_detour;
        continuation = build_absolute_jump_continuation(previous_effective_top, label ? label : "legacy bridge continuation");
        if (!continuation) {
            return legacy_hook_fail(owner, "inline hook", "HOOK FAILED", "Could not create a bridge continuation for a shared inline target.");
        }
        if (!legacy_publish_inline(record, detour, 0)) {
            legacy_release_code(continuation);
            return legacy_hook_fail(owner, "inline hook", "HOOK TARGET CHANGED", "Shared inline target changed during publication; the new detour was not installed.");
        }
        record->top_detour = detour;
    }

    LegacyInlineRequest* req = &g_legacy_inline_requests[g_legacy_inline_request_count++];
    req->owner = owner;
    req->target_index = target_index;
    req->detour = detour;
    req->continuation = continuation;
    req->previous_effective_top = previous_effective_top;
    req->original_trampoline = g_legacy_inline_targets[target_index].original_trampoline;
    req->stolen = stolen;
    rml_copy_str(req->label, sizeof(req->label), label ? label : "unnamed");
    *trampoline = continuation;
    if (owner) {
        owner->legacy_hooks_installed++;
        rml_copy_str(owner->status, sizeof(owner->status), owner->legacy_hook_failures ? "TESMIO ACTIVE — DEGRADED" : "TESMIO ACTIVE");
    }

    journal_plugin_event(plan, "legacy_hook_committed", target_desc, "inline-detour",
                         first_owner ? "tesmio-fidelity-direct" : "rml-virtual-bridge",
                         label ? label : "unnamed");
    if (g_plan.flags & PLAN_DEEP_TEST) {
        char comp[180]; legacy_component(owner, comp, sizeof(comp));
        log_u32(RML_LOG_INFO, comp, "DEEP TRACE inline target index: ", target_index);
        log_write(RML_LOG_INFO, comp, first_owner ? "DEEP TRACE chain role: first owner" : "DEEP TRACE chain role: chained onto an existing effective top detour");
        log_hex(RML_LOG_INFO, comp, "Previous effective top: ", (uintptr_t)previous_effective_top);
        log_hex(RML_LOG_INFO, comp, "Continuation returned to plugin: ", (uintptr_t)continuation);
        log_hex(RML_LOG_INFO, comp, "Original game trampoline: ", (uintptr_t)g_legacy_inline_targets[target_index].original_trampoline);
        log_hex(RML_LOG_INFO, comp, "Effective top detour after install: ", (uintptr_t)g_legacy_inline_targets[target_index].top_detour);
        deep_log_bytes(comp, "Published target bytes: ", (const BYTE*)target, stolen < 32u ? stolen : 32u);
    }
    legacy_hook_success(owner, "inline hook", first_owner ? "HOOK INSTALLED" : "HOOK CHAINED",
                        first_owner ? "Legacy inline hook installed on the current target." : "Legacy inline hook chained onto the previous effective target owner.");
    return 1;
}

static volatile LONG g_legacy_inline_host_busy = 0;
static int tsm_install_inline_hook(void* target, void* detour, void** trampoline,
                                   const unsigned char* expect, size_t stolen, const char* label) {
    // Re-entering the same legacy host hook helper is never a valid install
    // transaction. Refuse instead of spinning or recursively corrupting staging.
    if (RmlInterlockedCompareExchange(&g_legacy_inline_host_busy, 1, 0) != 0) {
        PluginState* owner = g_tsm_active_plugin ? g_tsm_active_plugin : plugin_from_callsite(__builtin_return_address(0));
        return legacy_hook_fail_early(owner, "HOOK REJECTED", "Re-entrant legacy inline-hook installation was refused.");
    }
    int result = tsm_install_inline_hook_impl(target, detour, trampoline, expect, stolen, label);
    RmlInterlockedExchange(&g_legacy_inline_host_busy, 0);
    return result;
}

uint32_t tesmio_compat_pending_hook_count(void) {
    return g_legacy_inline_request_count + g_legacy_iat_request_count;
}

int tesmio_compat_commit_pending_hooks(void) {
    if (g_plan.flags & PLAN_DEEP_TEST) {
        log_u32(RML_LOG_INFO, "DeepTest", "Live rehearsal host-hook requests physically active: ", g_deep_test_stats.host_hook_requests);
        tesmio_compat_deep_audit_state("pre-COMMIT integrity baseline", 1);
    }
    // Legacy fidelity path commits host-mediated Tesmio hooks at the
    // exact API call, like TesmioLoader. COMMIT therefore performs only a final
    // integrity check: there must be no failed plugin whose Start installed a
    // hook, and every RML-known legacy target must still point at the expected
    // top detour. This keeps the compatibility path out of steady-state
    // execution while retaining conflict detection/provenance.
    for (uint32_t i = 0; i < g_plugin_count; ++i) {
        if ((g_plugins[i].flags & PLUGIN_TESMIO_LEGACY) && g_plugins[i].failed && g_plugins[i].legacy_hooks_installed) {
            log_fault(RML_LOG_ERROR, "TesmioCompat", "HOOK INTEGRITY FAILED", "A plugin failed after installing a host-mediated hook.");
            return 0;
        }
    }
    for (uint32_t i = 0; i < g_legacy_inline_target_count; ++i) {
        LegacyInlineTarget* target = &g_legacy_inline_targets[i];
        if (!target->committed || !target->top_detour || !executable_ptr(target->top_detour)) {
            log_fault(RML_LOG_ERROR, "TesmioCompat", "HOOK INTEGRITY FAILED", "An inline target record is incomplete or its known continuation is no longer executable.");
            journal_hook_event("legacy_verify_failed", target->target_desc, "inline-detour", "runtime-revalidation", "Known target/continuation became invalid");
            return 0;
        }
        if (legacy_inline_patch_points_to(target, target->top_detour)) continue;

        if (rml_memeq(target->target, target->original, target->stolen)) {
            // Preserve RML-mediated plugins if another startup transaction merely
            // restored the vanilla prologue after our first install.
            if (!legacy_publish_inline(target, target->top_detour, 1)) {
                log_fault(RML_LOG_ERROR, "TesmioCompat", "HOOK INTEGRITY FAILED", "A target reverted to vanilla and RML could not restore the known detour.");
                journal_hook_event("legacy_verify_failed", target->target_desc, "inline-detour", "runtime-revalidation", "Vanilla restoration could not be repaired");
                return 0;
            }
            log_write(RML_LOG_WARN, "TesmioCompat", "Legacy COMMIT repaired a mediated target that had been restored to its original WRSR bytes by another startup path.");
            journal_hook_event("legacy_verify_repaired", target->target_desc, "inline-detour", "runtime-revalidation", "Restored known top detour after vanilla-byte restoration");
            continue;
        }

        void* observed = 0; const char* shape = 0;
        if (legacy_decode_entry_detour(target->target, target->stolen, &observed, &shape)) {
            PluginState* observed_owner = plugin_from_callsite(observed);
            log_write(RML_LOG_WARN, "TesmioCompat", "Legacy COMMIT found a recognized later detour on an RML-known target. The current execution chain is being preserved instead of forcing RML's earlier detour back on top.");
            log_write(RML_LOG_INFO, "TesmioCompat", target->target_desc);
            log_hex(RML_LOG_INFO, "TesmioCompat", "Observed effective top detour: ", (uintptr_t)observed);
            if (shape) log_write(RML_LOG_INFO, "TesmioCompat", shape);
            if (observed_owner) log_write(RML_LOG_INFO, "TesmioCompat", observed_owner->id);
            target->external_chain_adopted = 1;
            target->external_destination = observed;
            rml_copy_str(target->external_owner, sizeof(target->external_owner), observed_owner ? observed_owner->id : "");
            rml_copy_str(target->external_shape, sizeof(target->external_shape), shape ? shape : "recognized detour");
            target->top_detour = observed;
            journal_hook_event("legacy_external_chain_preserved", target->target_desc, "inline-detour", shape ? shape : "recognized-detour", observed_owner ? observed_owner->id : "unknown loaded code");
            continue;
        }

        log_fault(RML_LOG_ERROR, "TesmioCompat", "HOOK INTEGRITY FAILED", "An inline target was modified with an instruction sequence RML cannot safely interpret.");
        log_write(RML_LOG_ERROR, "TesmioCompat", target->target_desc);
        journal_hook_event("legacy_verify_failed", target->target_desc, "inline-detour", "runtime-revalidation", "Unrecognized target modification after legacy hook installation");
        return 0;
    }
    for (uint32_t i = 0; i < g_legacy_iat_target_count; ++i) {
        LegacyIatTarget* target = &g_legacy_iat_targets[i];
        if (!target->committed || !target->slot || !tsm_readable_ptr(target->slot, sizeof(void*))) {
            log_fault(RML_LOG_ERROR, "TesmioCompat", "HOOK INTEGRITY FAILED", "An IAT target record became unreadable.");
            journal_hook_event("legacy_verify_failed", target->target_desc, "iat-patch", "runtime-revalidation", "IAT slot became unreadable");
            return 0;
        }
        if (*target->slot == target->top_detour) continue;
        if (*target->slot == target->original && executable_ptr(target->top_detour)) {
            DWORD old = 0;
            if (VirtualProtect(target->slot, sizeof(void*), PAGE_READWRITE, &old)) {
                *target->slot = target->top_detour;
                DWORD ignored = 0; VirtualProtect(target->slot, sizeof(void*), old, &ignored);
                log_write(RML_LOG_WARN, "TesmioCompat", "Legacy COMMIT restored a mediated IAT slot that another startup path had reset to its original function.");
                continue;
            }
        }
        if (executable_ptr(*target->slot)) {
            target->top_detour = *target->slot;
            target->external_chain_adopted = 1;
            target->external_destination = *target->slot;
            PluginState* observed_owner = plugin_from_callsite(*target->slot);
            rml_copy_str(target->external_owner, sizeof(target->external_owner), observed_owner ? observed_owner->id : "");
            rml_copy_str(target->external_shape, sizeof(target->external_shape), "executable IAT destination");
            log_write(RML_LOG_WARN, "TesmioCompat", "Legacy COMMIT found a later executable IAT detour and preserved it as the effective top of the chain.");
            journal_hook_event("legacy_external_chain_preserved", target->target_desc, "iat-patch", "recognized-executable-target", "Later IAT detour preserved");
            continue;
        }
        log_fault(RML_LOG_ERROR, "TesmioCompat", "HOOK INTEGRITY FAILED", "An IAT target changed to a non-executable destination.");
        journal_hook_event("legacy_verify_failed", target->target_desc, "iat-patch", "runtime-revalidation", "IAT slot changed to invalid destination");
        return 0;
    }
    if (g_legacy_inline_request_count || g_legacy_iat_request_count) {
        log_u32(RML_LOG_INFO, "TesmioCompat", "Legacy fidelity hooks active: ", g_legacy_inline_request_count + g_legacy_iat_request_count);
        log_write(RML_LOG_INFO, "TesmioCompat", "Legacy steady-state path is direct: WRSR -> plugin detour -> private continuation -> WRSR. No RML dispatcher is inserted for an unshared Tesmio target.");
    } else {
        log_write(RML_LOG_INFO, "TesmioCompat", "No host-mediated legacy hooks were requested.");
    }
    if (g_plan.flags & PLAN_DEEP_TEST)
        tesmio_compat_deep_audit_state("post-COMMIT integrity baseline", 1);
    return 1;
}


uint32_t tesmio_compat_chain_count(void) {
    return g_legacy_inline_target_count + g_legacy_iat_target_count;
}

static void snapshot_step(LegacyHookChainStepSnapshot* out, PluginState* owner, const char* label,
                          void* detour, void* continuation, void* previous_effective_top,
                          void* original_trampoline, uint32_t stolen_bytes,
                          uint32_t install_order, uint32_t execution_order) {
    if (!out) return;
    rml_memset(out, 0, sizeof(*out));
    if (owner) {
        rml_copy_str(out->plugin_id, sizeof(out->plugin_id), owner->id);
        rml_copy_str(out->plugin_sha256, sizeof(out->plugin_sha256), owner->dll_sha256);
    }
    rml_copy_str(out->label, sizeof(out->label), label ? label : "unnamed hook");
    out->detour = (uintptr_t)detour;
    out->continuation = (uintptr_t)continuation;
    out->previous_effective_top = (uintptr_t)previous_effective_top;
    out->original_trampoline = (uintptr_t)original_trampoline;
    out->stolen_bytes = stolen_bytes;
    out->install_order = install_order;
    out->execution_order = execution_order;
}

int tesmio_compat_chain_snapshot(uint32_t index, LegacyHookChainSnapshot* out) {
    if (!out || index >= tesmio_compat_chain_count()) return 0;
    rml_memset(out, 0, sizeof(*out));
    uintptr_t base = (uintptr_t)GetModuleHandleW(0);
    if (index < g_legacy_inline_target_count) {
        LegacyInlineTarget* target = &g_legacy_inline_targets[index];
        rml_copy_str(out->type, sizeof(out->type), "inline");
        rml_copy_str(out->target_desc, sizeof(out->target_desc), target->target_desc);
        out->target = (uintptr_t)target->target;
        out->stolen = (uint32_t)target->stolen;
        out->effective_top = (uintptr_t)target->top_detour;
        out->original = (uintptr_t)target->original_trampoline;
        if (base && out->target >= base) out->rva = (uint32_t)(out->target - base);
        SymbolRecord* symbol = symbol_registry_find_by_address(target->target);
        if (symbol && symbol->id) rml_copy_str(out->symbol_id, sizeof(out->symbol_id), symbol->id);

        int healthy = target->committed && target->top_detour && executable_ptr(target->top_detour) &&
                      legacy_inline_patch_points_to(target, target->top_detour);
        int vanilla = target->target && target->stolen && rml_memeq(target->target, target->original, target->stolen);
        void* observed = 0; const char* shape = 0;
        int recognized = (!healthy && !vanilla && target->target) ? legacy_decode_entry_detour(target->target, target->stolen, &observed, &shape) : 0;
        if (healthy) {
            if (target->external_chain_adopted) {
                rml_copy_str(out->integrity, sizeof(out->integrity), "RECOGNIZED EXTERNAL CHAIN");
                rml_copy_str(out->proof, sizeof(out->proof), target->external_shape[0] ? target->external_shape : "recognized executable detour");
                rml_copy_str(out->observed_top_owner, sizeof(out->observed_top_owner), target->external_owner);
            } else {
                rml_copy_str(out->integrity, sizeof(out->integrity), "PASS");
                rml_copy_str(out->proof, sizeof(out->proof), "CURRENT VERIFIED CHAIN");
            }
            out->current_destination = (uintptr_t)target->top_detour;
        } else if (vanilla) {
            rml_copy_str(out->integrity, sizeof(out->integrity), "REVERTED TO VANILLA");
            rml_copy_str(out->proof, sizeof(out->proof), "BROKEN");
            out->current_destination = out->target;
        } else if (recognized) {
            rml_copy_str(out->integrity, sizeof(out->integrity), "RECOGNIZED EXTERNAL CHAIN");
            rml_copy_str(out->proof, sizeof(out->proof), shape ? shape : "recognized executable detour");
            out->current_destination = (uintptr_t)observed;
            PluginState* observed_owner = plugin_from_callsite(observed);
            if (observed_owner) rml_copy_str(out->observed_top_owner, sizeof(out->observed_top_owner), observed_owner->id);
        } else {
            rml_copy_str(out->integrity, sizeof(out->integrity), "UNKNOWN MODIFICATION");
            rml_copy_str(out->proof, sizeof(out->proof), "BROKEN");
        }

        uint32_t matches[RML_MAX_CHAIN_STEPS] = {}; uint32_t match_count = 0;
        for (uint32_t r = 0; r < g_legacy_inline_request_count && match_count < RML_MAX_CHAIN_STEPS; ++r)
            if (g_legacy_inline_requests[r].target_index == index) matches[match_count++] = r;
        out->step_count = match_count;
        for (uint32_t e = 0; e < match_count; ++e) {
            uint32_t r = matches[match_count - 1u - e];
            LegacyInlineRequest* req = &g_legacy_inline_requests[r];
            snapshot_step(&out->steps[e], req->owner, req->label, req->detour, req->continuation,
                          req->previous_effective_top, req->original_trampoline, (uint32_t)req->stolen, r, e);
        }
        return 1;
    }

    uint32_t iat_index = index - g_legacy_inline_target_count;
    LegacyIatTarget* target = &g_legacy_iat_targets[iat_index];
    rml_copy_str(out->type, sizeof(out->type), "iat");
    rml_copy_str(out->target_desc, sizeof(out->target_desc), target->target_desc);
    out->target = (uintptr_t)target->slot;
    out->effective_top = (uintptr_t)target->top_detour;
    out->original = (uintptr_t)target->original;
    void* current = (target->slot && tsm_readable_ptr(target->slot, sizeof(void*))) ? *target->slot : 0;
    out->current_destination = (uintptr_t)current;
    int healthy = target->committed && current == target->top_detour && executable_ptr(target->top_detour);
    if (healthy) {
        if (target->external_chain_adopted) {
            rml_copy_str(out->integrity, sizeof(out->integrity), "RECOGNIZED EXTERNAL CHAIN");
            rml_copy_str(out->proof, sizeof(out->proof), target->external_shape[0] ? target->external_shape : "executable IAT destination");
            rml_copy_str(out->observed_top_owner, sizeof(out->observed_top_owner), target->external_owner);
        } else {
            rml_copy_str(out->integrity, sizeof(out->integrity), "PASS");
            rml_copy_str(out->proof, sizeof(out->proof), "CURRENT VERIFIED CHAIN");
        }
    } else if (current == target->original) {
        rml_copy_str(out->integrity, sizeof(out->integrity), "REVERTED TO ORIGINAL");
        rml_copy_str(out->proof, sizeof(out->proof), "BROKEN");
    } else if (current && executable_ptr(current)) {
        rml_copy_str(out->integrity, sizeof(out->integrity), "RECOGNIZED EXTERNAL CHAIN");
        rml_copy_str(out->proof, sizeof(out->proof), "executable IAT destination");
        PluginState* observed_owner = plugin_from_callsite(current);
        if (observed_owner) rml_copy_str(out->observed_top_owner, sizeof(out->observed_top_owner), observed_owner->id);
    } else {
        rml_copy_str(out->integrity, sizeof(out->integrity), "INVALID DESTINATION");
        rml_copy_str(out->proof, sizeof(out->proof), "BROKEN");
    }
    uint32_t matches[RML_MAX_CHAIN_STEPS] = {}; uint32_t match_count = 0;
    for (uint32_t r = 0; r < g_legacy_iat_request_count && match_count < RML_MAX_CHAIN_STEPS; ++r)
        if (g_legacy_iat_requests[r].target_index == iat_index) matches[match_count++] = r;
    out->step_count = match_count;
    for (uint32_t e = 0; e < match_count; ++e) {
        uint32_t r = matches[match_count - 1u - e];
        LegacyIatRequest* req = &g_legacy_iat_requests[r];
        snapshot_step(&out->steps[e], req->owner, req->label, req->detour, req->continuation,
                      req->continuation, 0, 0, r, e);
    }
    return 1;
}

uint32_t tesmio_compat_deep_audit_state(const char* reason, int verbose) {
    if (!(g_plan.flags & PLAN_DEEP_TEST)) return 0;
    uint32_t issues = 0;
    log_write(RML_LOG_INFO, "DeepHookAudit", "============================================================");
    log_write(RML_LOG_INFO, "DeepHookAudit", reason ? reason : "hook-chain audit");
    log_u32(RML_LOG_INFO, "DeepHookAudit", "Inline target records: ", g_legacy_inline_target_count);
    log_u32(RML_LOG_INFO, "DeepHookAudit", "Inline hook requests: ", g_legacy_inline_request_count);
    log_u32(RML_LOG_INFO, "DeepHookAudit", "IAT target records: ", g_legacy_iat_target_count);
    log_u32(RML_LOG_INFO, "DeepHookAudit", "IAT hook requests: ", g_legacy_iat_request_count);

    for (uint32_t i = 0; i < g_legacy_inline_target_count; ++i) {
        LegacyInlineTarget* target = &g_legacy_inline_targets[i];
        int healthy = target->committed && target->top_detour && executable_ptr(target->top_detour) &&
                      legacy_inline_patch_points_to(target, target->top_detour);
        int vanilla = target->target && target->stolen && rml_memeq(target->target, target->original, target->stolen);
        void* observed = 0; const char* shape = 0;
        int recognized = (!healthy && !vanilla && target->target) ? legacy_decode_entry_detour(target->target, target->stolen, &observed, &shape) : 0;
        if (!healthy) ++issues;
        if (!verbose && healthy) continue;

        log_u32(healthy ? RML_LOG_INFO : RML_LOG_WARN, "DeepHookAudit", "Inline target index: ", i);
        log_write(healthy ? RML_LOG_INFO : RML_LOG_WARN, "DeepHookAudit", target->target_desc[0] ? target->target_desc : "(unnamed inline target)");
        log_u64(RML_LOG_INFO, "DeepHookAudit", "Stolen bytes: ", (uint64_t)target->stolen);
        log_hex(RML_LOG_INFO, "DeepHookAudit", "Expected effective top detour: ", (uintptr_t)target->top_detour);
        log_hex(RML_LOG_INFO, "DeepHookAudit", "Original continuation trampoline: ", (uintptr_t)target->original_trampoline);
        if (target->target) deep_log_bytes("DeepHookAudit", "Current target bytes: ", target->target, target->stolen < 32u ? target->stolen : 32u);
        deep_log_bytes("DeepHookAudit", "Original target bytes: ", target->original, target->stolen < 32u ? target->stolen : 32u);

        if (healthy) {
            if (target->external_chain_adopted) {
                log_write(RML_LOG_INFO, "DeepHookAudit", "Integrity: PASS — recorded effective chain includes a recognized external detour preserved by RML.");
                log_hex(RML_LOG_INFO, "DeepHookAudit", "Recognized external destination: ", (uintptr_t)target->external_destination);
                if (target->external_shape[0]) log_write(RML_LOG_INFO, "DeepHookAudit", target->external_shape);
                if (target->external_owner[0]) log_write(RML_LOG_INFO, "DeepHookAudit", target->external_owner);
            } else {
                log_write(RML_LOG_INFO, "DeepHookAudit", "Integrity: PASS — target still points to RML's recorded effective top detour.");
            }
        } else if (vanilla) {
            log_write(RML_LOG_WARN, "DeepHookAudit", "Integrity: CHANGED — target currently contains its original vanilla bytes.");
        } else if (recognized) {
            log_write(RML_LOG_WARN, "DeepHookAudit", "Integrity: CHANGED — target now contains a recognizable later detour.");
            log_hex(RML_LOG_WARN, "DeepHookAudit", "Observed later detour destination: ", (uintptr_t)observed);
            if (shape) log_write(RML_LOG_WARN, "DeepHookAudit", shape);
            PluginState* observed_owner = plugin_from_callsite(observed);
            if (observed_owner) log_write(RML_LOG_WARN, "DeepHookAudit", observed_owner->id);
        } else {
            log_write(RML_LOG_ERROR, "DeepHookAudit", "Integrity: UNKNOWN MODIFICATION — current target bytes do not match the recorded top detour, vanilla bytes, or a recognized detour shape.");
        }

        uint32_t chain_steps = 0, original_continuation_refs = 0;
        for (uint32_t r = 0; r < g_legacy_inline_request_count; ++r) {
            LegacyInlineRequest* req = &g_legacy_inline_requests[r];
            if (req->target_index != i) continue;
            ++chain_steps;
            log_u32(RML_LOG_INFO, "DeepHookAudit", "  Chain request index: ", r);
            log_write(RML_LOG_INFO, "DeepHookAudit", req->owner && req->owner->id[0] ? req->owner->id : "(unknown owner)");
            log_write(RML_LOG_INFO, "DeepHookAudit", req->label[0] ? req->label : "(unnamed hook)");
            log_hex(RML_LOG_INFO, "DeepHookAudit", "  Plugin detour: ", (uintptr_t)req->detour);
            log_hex(RML_LOG_INFO, "DeepHookAudit", "  Previous effective top: ", (uintptr_t)req->previous_effective_top);
            log_hex(RML_LOG_INFO, "DeepHookAudit", "  Continuation returned to plugin: ", (uintptr_t)req->continuation);
            log_hex(RML_LOG_INFO, "DeepHookAudit", "  Original game trampoline: ", (uintptr_t)req->original_trampoline);
            log_u64(RML_LOG_INFO, "DeepHookAudit", "  Request stolen bytes: ", (uint64_t)req->stolen);

            int link_ok = req->detour && executable_ptr(req->detour) && req->continuation && executable_ptr(req->continuation) &&
                          req->original_trampoline == target->original_trampoline &&
                          rml_legacy_shared_stolen_compatible(target->stolen, req->stolen);
            if (req->continuation == target->original_trampoline) {
                ++original_continuation_refs;
                if (req->previous_effective_top != target->original_trampoline) link_ok = 0;
            } else {
                uintptr_t linked = 0;
                if (!rml_legacy_decode_ff25_patch(req->continuation, 14u, &linked) ||
                    linked != (uintptr_t)req->previous_effective_top || !executable_ptr(req->previous_effective_top)) link_ok = 0;
            }
            if (!link_ok) {
                ++issues;
                log_fault(RML_LOG_ERROR, "DeepHookAudit", "HOOK INTEGRITY FAILED", "A recorded legacy inline-chain continuation no longer proves the expected predecessor link.");
            } else if (verbose) {
                log_write(RML_LOG_INFO, "DeepHookAudit", "  Chain link: PASS");
            }
        }
        if (chain_steps && original_continuation_refs != 1u) {
            ++issues;
            log_fault(RML_LOG_ERROR, "DeepHookAudit", "HOOK INTEGRITY FAILED", "Legacy inline chain does not reach the original game trampoline exactly once.");
        }
    }

    for (uint32_t i = 0; i < g_legacy_iat_target_count; ++i) {
        LegacyIatTarget* target = &g_legacy_iat_targets[i];
        void* current = (target->slot && tsm_readable_ptr(target->slot, sizeof(void*))) ? *target->slot : 0;
        int healthy = target->committed && current == target->top_detour && executable_ptr(target->top_detour);
        if (!healthy) ++issues;
        if (!verbose && healthy) continue;
        log_u32(healthy ? RML_LOG_INFO : RML_LOG_WARN, "DeepHookAudit", "IAT target index: ", i);
        log_write(healthy ? RML_LOG_INFO : RML_LOG_WARN, "DeepHookAudit", target->target_desc[0] ? target->target_desc : "(unnamed IAT target)");
        log_hex(RML_LOG_INFO, "DeepHookAudit", "IAT slot: ", (uintptr_t)target->slot);
        log_hex(RML_LOG_INFO, "DeepHookAudit", "Original destination: ", (uintptr_t)target->original);
        log_hex(RML_LOG_INFO, "DeepHookAudit", "Expected effective top detour: ", (uintptr_t)target->top_detour);
        log_hex(healthy ? RML_LOG_INFO : RML_LOG_WARN, "DeepHookAudit", "Current destination: ", (uintptr_t)current);
        if (healthy) {
            if (target->external_chain_adopted) {
                log_write(RML_LOG_INFO, "DeepHookAudit", "Integrity: PASS — IAT chain includes a recognized external executable destination preserved by RML.");
                log_hex(RML_LOG_INFO, "DeepHookAudit", "Recognized external destination: ", (uintptr_t)target->external_destination);
                if (target->external_owner[0]) log_write(RML_LOG_INFO, "DeepHookAudit", target->external_owner);
            } else {
                log_write(RML_LOG_INFO, "DeepHookAudit", "Integrity: PASS — IAT slot matches the recorded effective top detour.");
            }
        } else if (current == target->original) log_write(RML_LOG_WARN, "DeepHookAudit", "Integrity: CHANGED — IAT slot reverted to the original imported function.");
        else if (current && executable_ptr(current)) log_write(RML_LOG_WARN, "DeepHookAudit", "Integrity: CHANGED — IAT slot points to another executable destination.");
        else log_write(RML_LOG_ERROR, "DeepHookAudit", "Integrity: INVALID — IAT slot is unreadable or points to non-executable memory.");
        for (uint32_t r = 0; r < g_legacy_iat_request_count; ++r) {
            LegacyIatRequest* req = &g_legacy_iat_requests[r];
            if (req->target_index != i) continue;
            log_u32(RML_LOG_INFO, "DeepHookAudit", "  IAT chain request index: ", r);
            log_write(RML_LOG_INFO, "DeepHookAudit", req->owner && req->owner->id[0] ? req->owner->id : "(unknown owner)");
            log_write(RML_LOG_INFO, "DeepHookAudit", req->label[0] ? req->label : "(unnamed IAT hook)");
            log_hex(RML_LOG_INFO, "DeepHookAudit", "  Plugin detour: ", (uintptr_t)req->detour);
        }
    }

    log_u32(issues ? RML_LOG_WARN : RML_LOG_INFO, "DeepHookAudit", "Audit anomalies: ", issues);
    log_write(RML_LOG_INFO, "DeepHookAudit", "============================================================");
    g_deep_test_stats.last_audit_issues = issues;
    return issues;
}

static long tsm_fault_filter(const char* what, void* exceptionPointers) {
    (void)exceptionPointers; tsm_log("Legacy plugin exception reported through faultFilter: %s", what ? what : "unknown");
    return 1; // EXCEPTION_EXECUTE_HANDLER
}

static int safe_relative_ini_name(const char* name) {
    if (!name || !*name || name[0] == '\\' || name[0] == '/' || name[1] == ':') return 0;
    const char* p = name;
    while (*p) {
        if (p[0] == '.' && p[1] == '.' && (p[2] == 0 || p[2] == '\\' || p[2] == '/')) return 0;
        ++p;
    }
    return 1;
}
static __declspec(noinline) int make_ini_path(const char* name, wchar_t* out, size_t cap) {
    if (!safe_relative_ini_name(name)) return 0;
    wchar_t rel[RML_MAX_PATH] = {};
    if (!MultiByteToWideChar(CP_UTF8, 0, name, -1, rel, RML_MAX_PATH)) return 0;
    return rml_join_path(out, cap, g_tsm_base_dir_w, rel);
}
static __declspec(noinline) int make_plugin_ini_path(PluginState* owner, const char* name, wchar_t* out, size_t cap) {
    if (!safe_relative_ini_name(name) || !out || cap < 4) return 0;
    const char* relative = name;
    // Tesmio plugins may request `plugins\Name.ini`. A plugin distributed
    // inside a Workshop item's own plugin folder interprets that prefix as
    // its own plugin directory rather than RML's fallback directory.
    if ((relative[0] == 'p' || relative[0] == 'P') &&
        (relative[1] == 'l' || relative[1] == 'L') &&
        (relative[2] == 'u' || relative[2] == 'U') &&
        (relative[3] == 'g' || relative[3] == 'G') &&
        (relative[4] == 'i' || relative[4] == 'I') &&
        (relative[5] == 'n' || relative[5] == 'N') &&
        (relative[6] == 's' || relative[6] == 'S') &&
        (relative[7] == '\\' || relative[7] == '/')) relative += 8;
    if (owner && owner->path && owner->path[0]) {
        size_t n = rml_wcslen(owner->path);
        if (n + 1u >= cap) return 0;
        for (size_t i = 0; i <= n; ++i) out[i] = owner->path[i];
        size_t cut = n;
        while (cut && out[cut-1] != L'\\' && out[cut-1] != L'/') --cut;
        if (cut) {
            // `cut` already points immediately after the path separator.
            int room = (int)(cap - cut);
            if (room <= 1 || !MultiByteToWideChar(CP_UTF8, 0, relative, -1, out + cut, room)) return 0;
            return 1;
        }
    }
    wchar_t rel[RML_MAX_PATH] = {};
    if (!MultiByteToWideChar(CP_UTF8, 0, relative, -1, rel, RML_MAX_PATH)) return 0;
    return rml_join_path(out, cap, g_tsm_plugin_dir_w, rel);
}

static char* trim_left(char* s) { while (*s == ' ' || *s == '\t') ++s; return s; }
static void trim_right(char* s) { size_t n = rml_strlen(s); while (n && (s[n-1] == ' ' || s[n-1] == '\t' || s[n-1] == '\r')) s[--n] = 0; }
static int ini_find_value(PluginState* owner, const char* ini, const char* section, const char* key, char* out, int outSize) {
    if (!ini || !section || !key || !out || outSize <= 0) return 0;
    wchar_t path[RML_MAX_PATH] = {};
    // Prefer the directory containing the actual DLL. This is equivalent to the
    // base-relative rml\plugins layout for local installs and is essential when a
    // Workshop item supplies its plugin and INI together.
    HANDLE f = INVALID_HANDLE_VALUE;
    if (make_plugin_ini_path(owner, ini, path, RML_MAX_PATH))
        f = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ, 0, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, 0);
    // Fall back to the traditional RML/Tesmio base-relative interpretation for
    // legacy packages that intentionally reference another shared configuration.
    if (f == INVALID_HANDLE_VALUE && make_ini_path(ini, path, RML_MAX_PATH))
        f = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ, 0, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, 0);
    if (f == INVALID_HANDLE_VALUE) return 0;
    LARGE_INTEGER li = {}; if (!GetFileSizeEx(f, &li) || li.QuadPart < 0 || li.QuadPart > 512 * 1024) { CloseHandle(f); return 0; }
    size_t n = (size_t)li.QuadPart; char* data = (char*)rml_alloc(n + 2u); if (!data) { CloseHandle(f); return 0; }
    DWORD read = 0; BOOL ok = ReadFile(f, data, (DWORD)n, &read, 0); CloseHandle(f); if (!ok || read != (DWORD)n) { rml_free(data); return 0; }
    data[n] = '\n'; data[n+1] = 0; int in_section = 0; char* line = data;
    for (size_t i = 0; i <= n; ++i) {
        if (data[i] != '\n' && data[i] != 0) continue;
        data[i] = 0; char* s = trim_left(line); trim_right(s); line = data + i + 1;
        if (!*s || *s == ';' || *s == '#') continue;
        if (*s == '[') { char* end = s + 1; while (*end && *end != ']') ++end; if (*end == ']') { *end = 0; trim_right(s+1); in_section = ascii_equal_i(trim_left(s+1), section); } continue; }
        if (!in_section) continue;
        char* eq = s; while (*eq && *eq != '=') ++eq; if (*eq != '=') continue; *eq = 0;
        trim_right(s); char* k = trim_left(s); char* v = trim_left(eq + 1); trim_right(v);
        if (!ascii_equal_i(k, key)) continue;
        int count = 0; while (v[count] && count + 1 < outSize) { out[count] = v[count]; ++count; } out[count] = 0; rml_free(data); return count;
    }
    rml_free(data); return 0;
}
static int tsm_config_string(const char* ini, const char* section, const char* key, char* out, int outSize, const char* fallback) {
    if (!out || outSize <= 0) return 0;
    PluginState* owner = g_tsm_active_plugin ? g_tsm_active_plugin : plugin_from_callsite(__builtin_return_address(0));
    int n = ini_find_value(owner, ini, section, key, out, outSize);
    if (n > 0) return n;
    int p = 0; if (fallback) while (fallback[p] && p + 1 < outSize) { out[p] = fallback[p]; ++p; } out[p] = 0; return p;
}
static int parse_int(const char* s, int fallback) {
    if (!s) return fallback; while (*s == ' ' || *s == '\t') ++s;
    if (ascii_equal_i(s, "true") || ascii_equal_i(s, "yes") || ascii_equal_i(s, "on")) return 1;
    if (ascii_equal_i(s, "false") || ascii_equal_i(s, "no") || ascii_equal_i(s, "off")) return 0;
    int sign = 1; if (*s == '-') { sign = -1; ++s; } else if (*s == '+') ++s;
    int base = 10; if (s[0] == '0' && (s[1] == 'x' || s[1] == 'X')) { base = 16; s += 2; }
    int any = 0; int64_t v = 0; while (*s) { int d = -1; if (*s >= '0' && *s <= '9') d = *s - '0'; else if (base == 16 && *s >= 'a' && *s <= 'f') d = *s - 'a' + 10; else if (base == 16 && *s >= 'A' && *s <= 'F') d = *s - 'A' + 10; else break; if (d >= base) break; any = 1; v = v * base + d; if (v > 0x7FFFFFFFll + (sign < 0)) break; ++s; }
    return any ? (int)(v * sign) : fallback;
}
static int tsm_config_int(const char* ini, const char* section, const char* key, int fallback) {
    PluginState* owner = g_tsm_active_plugin ? g_tsm_active_plugin : plugin_from_callsite(__builtin_return_address(0));
    char value[128] = {}; return ini_find_value(owner, ini, section, key, value, sizeof(value)) > 0 ? parse_int(value, fallback) : fallback;
}

static int tsm_provide(const char* service, unsigned version, const void* iface) {
    PluginState* owner = plugin_from_callsite(__builtin_return_address(0));
    if (!owner || !service || !*service || !iface) return 0;
    for (uint32_t i = 0; i < g_tsm_service_count; ++i) if (g_tsm_services[i].version == version && ascii_equal_i(g_tsm_services[i].name, service)) return 0;
    if (g_tsm_service_count >= 256) return 0;
    LegacyService* s = &g_tsm_services[g_tsm_service_count++]; rml_copy_str(s->name, sizeof(s->name), service); s->version = version; s->iface = iface; s->owner = owner; owner->legacy_services_provided++;
    tsm_log("RML legacy service provided: %s v%u", service, version);
    journal_plugin_event(plan_find_plugin(owner->id), "legacy_service_provided", service, "service-provide", "tesmio-shim", "Tesmio service registered in the RML compatibility registry");
    return 1;
}
static const void* tsm_consume(const char* service, unsigned version) {
    PluginState* owner = plugin_from_callsite(__builtin_return_address(0));
    for (uint32_t i = 0; i < g_tsm_service_count; ++i) {
        if (g_tsm_services[i].version == version && ascii_equal_i(g_tsm_services[i].name, service)) {
            if (owner) journal_plugin_event(plan_find_plugin(owner->id), "legacy_service_consumed", service, "service-consume", "tesmio-shim", "Tesmio service resolved through RML");
            return g_tsm_services[i].iface;
        }
    }
    return 0;
}
void tesmio_compat_remove_services(PluginState* owner) {
    if (!owner) return;
    uint32_t w = 0; for (uint32_t i = 0; i < g_tsm_service_count; ++i) { if (g_tsm_services[i].owner == owner) continue; if (w != i) g_tsm_services[w] = g_tsm_services[i]; ++w; }
    for (uint32_t i = w; i < g_tsm_service_count; ++i) rml_memset(&g_tsm_services[i], 0, sizeof(g_tsm_services[i])); g_tsm_service_count = w; owner->legacy_services_provided = 0;
}

int tesmio_compat_prepare(void) {
    if (g_tsm_ready) return 1;
    if (!rml_join_path(g_tsm_base_dir_w, RML_MAX_PATH, g_root_path, L"rml")) return 0;
    if (!rml_join_path(g_tsm_plugin_dir_w, RML_MAX_PATH, g_tsm_base_dir_w, L"plugins")) return 0;
    wchar_t vfs_w[RML_MAX_PATH]; if (!rml_join_path(vfs_w, RML_MAX_PATH, g_tsm_base_dir_w, L"vfs")) return 0;
    CreateDirectoryW(g_tsm_base_dir_w, 0); CreateDirectoryW(g_tsm_plugin_dir_w, 0); CreateDirectoryW(vfs_w, 0);
    if (!rml_utf16_to_utf8(g_tsm_base_dir_w, g_tsm_base_dir, (int)sizeof(g_tsm_base_dir)) ||
        !rml_utf16_to_utf8(g_tsm_plugin_dir_w, g_tsm_plugin_dir, (int)sizeof(g_tsm_plugin_dir)) ||
        !rml_utf16_to_utf8(vfs_w, g_tsm_vfs_root, (int)sizeof(g_tsm_vfs_root))) return 0;

    HMODULE exe = GetModuleHandleW(0); uint32_t exe_size = 0; if (!module_image_size(exe, &exe_size)) return 0;
    HMODULE engine = 0;
    ULONGLONG deadline = GetTickCount64() + 5000u;
    while (!(engine = GetModuleHandleW(L"C3DDLL64.dll")) && GetTickCount64() < deadline) Sleep(10);
    if (!engine) log_write(RML_LOG_WARN, "TesmioCompat", "C3DDLL64.dll was not loaded within 5 seconds; legacy plugins that require engineModule may decline initialization.");
    else log_hex(RML_LOG_INFO, "TesmioCompat", "C3DDLL64.dll ready at ", (uintptr_t)engine);

    g_tsm_host.apiVersion = 4; g_tsm_host.structSize = sizeof(g_tsm_host); g_tsm_host.exeModule = exe; g_tsm_host.exeBase = (unsigned char*)exe; g_tsm_host.exeSize = exe_size;
    g_tsm_host.engineModule = engine; g_tsm_host.baseDir = g_tsm_base_dir; g_tsm_host.pluginDir = g_tsm_plugin_dir; g_tsm_host.log = tsm_log;
    g_tsm_host.findIatSlot = tsm_find_iat_slot; g_tsm_host.patchIat = tsm_patch_iat; g_tsm_host.installInlineHook = tsm_install_inline_hook; g_tsm_host.allocNear = tsm_alloc_near;
    g_tsm_host.readablePtr = tsm_readable_ptr; g_tsm_host.faultFilter = tsm_fault_filter; g_tsm_host.configInt = tsm_config_int; g_tsm_host.configString = tsm_config_string;
    g_tsm_host.provide = tsm_provide; g_tsm_host.consume = tsm_consume; g_tsm_host.vfsRoot = g_tsm_vfs_root;
    g_tsm_ready = 1;
    log_write(RML_LOG_INFO, "TesmioCompat", "Clean-room Tesmio API 4 compatibility host ready (accepts plugin API 3/4). Legacy hook requests are provenance-tracked by RML.");
    log_write(RML_LOG_INFO, "TesmioCompat", "Legacy readablePtr acceleration enabled: positive VirtualQuery regions cached per calling thread for up to 8 ms; Tesmio page-protection semantics preserved.");
    return 1;
}

static int safe_copy_plugin_string(char* out, size_t cap, const char* value) {
    if (!out || cap < 2 || !value || !tsm_readable_ptr(value, 1)) return 0;
    size_t i = 0; for (; i + 1 < cap; ++i) { if (!tsm_readable_ptr(value + i, 1)) return 0; char c = value[i]; out[i] = c; if (!c) return i > 0; }
    out[cap-1] = 0; return 0;
}

static int guarded_api_version(TsmApiVersionFn fn, unsigned* out) {
    if (!fn || !out) return 0;
#if defined(_MSC_VER)
    __try { *out = fn(); return 1; }
    __except(1) { return 0; }
#else
    *out = fn(); return 1;
#endif
}

static int guarded_init(TsmInitFn fn, const void* host, TsmPluginInfoCompat* info, int* result) {
    if (!fn || !result) return 0;
#if defined(_MSC_VER)
    __try { *result = fn(host, info); return 1; }
    __except(1) { return 0; }
#else
    *result = fn(host, info); return 1;
#endif
}

static int guarded_start(TsmStartFn fn, int* result) {
    if (!fn || !result) return 0;
#if defined(_MSC_VER)
    __try { *result = fn(); return 1; }
    __except(1) { return 0; }
#else
    *result = fn(); return 1;
#endif
}

int tesmio_compat_init_plugin(PluginState* state, PlanPlugin* plan) {
    if (!state || !plan || !tesmio_compat_prepare()) return 0;
    ULONGLONG deep_phase_begin = GetTickCount64();
    if (g_plan.flags & PLAN_DEEP_TEST) {
        log_write(RML_LOG_INFO, "DeepTrace", "Legacy plugin initialization transaction BEGIN");
        log_write(RML_LOG_INFO, "DeepTrace", state->id);
        log_write(RML_LOG_INFO, "DeepTrace", state->dll_sha256);
        log_u32(RML_LOG_INFO, "DeepTrace", "Priority: ", (uint32_t)state->priority);
    }
    ULONGLONG deep_load_begin = GetTickCount64();
    HMODULE module = LoadLibraryW(plan->path);
    if (!module) { log_fault_u32(RML_LOG_ERROR, "TesmioCompat", "PLUGIN FAILED TO LOAD", "Legacy LoadLibraryW error: ", GetLastError()); return 0; }
    state->module = module;
    if (g_plan.flags & PLAN_DEEP_TEST) {
        log_u64(RML_LOG_INFO, "DeepTrace", "Legacy LoadLibraryW duration (ms): ", GetTickCount64() - deep_load_begin);
        log_hex(RML_LOG_INFO, "DeepTrace", "Legacy module base: ", (uintptr_t)module);
    }
    TsmApiVersionFn api_fn = (TsmApiVersionFn)GetProcAddress(module, "TsmPluginApiVersion");
    TsmInitFn init_fn = (TsmInitFn)GetProcAddress(module, "TsmPluginInit");
    TsmStartFn start_fn = (TsmStartFn)GetProcAddress(module, "TsmPluginStart");
    if (!api_fn || !init_fn) {
        rml_copy_str(state->status, sizeof(state->status), "TESMIO EXPORT MISSING");
        log_fault(RML_LOG_ERROR, "TesmioCompat", "PLUGIN API FAILED", "Tesmio lifecycle was selected but required TsmPluginApiVersion/TsmPluginInit exports are missing at runtime.");
        FreeLibrary(module); state->module = 0; return 0;
    }
    unsigned api = 0;
    if (!guarded_api_version(api_fn, &api)) {
        rml_copy_str(state->status, sizeof(state->status), "TESMIO API QUERY EXCEPTION");
        log_fault(RML_LOG_ERROR, "TesmioCompat", "PLUGIN EXCEPTION", "Legacy plugin raised a native exception from TsmPluginApiVersion; module rejected before initialization.");
        journal_plugin_event(plan, "legacy_api_exception", 0, "TsmPluginApiVersion", "tesmio-shim", "Native exception while querying plugin API version");
        FreeLibrary(module); state->module = 0; return 0;
    }
    state->legacy_api = api;
    if (api < 3 || api > 4) {
        tsm_log("RML rejected Tesmio plugin API %u; shim supports API 3/4", api);
        rml_copy_str(state->status, sizeof(state->status), "TESMIO API INCOMPATIBLE");
        log_fault(RML_LOG_ERROR, "TesmioCompat", "PLUGIN API FAILED", "Detected Tesmio API is outside the compatibility shim's supported API 3/4 range.");
        FreeLibrary(module); state->module = 0; return 0;
    }
    // Give every legacy DLL its own immutable host table. API 3 sees the 0x90
    // prefix it was built for; API 4 receives the append-only 0x98 form. This
    // avoids one plugin observing another plugin's reported API level later.
    TsmHostCompat* host = (TsmHostCompat*)rml_alloc(sizeof(TsmHostCompat));
    if (!host) {
        log_fault(RML_LOG_ERROR, "TesmioCompat", "PLUGIN INITIALIZATION FAILED", "Could not allocate a per-plugin Tesmio host table.");
        FreeLibrary(module); state->module = 0; return 0;
    }
    *host = g_tsm_host;
    host->apiVersion = api;
    host->structSize = api >= 4 ? (unsigned)sizeof(TsmHostCompat) : 0x90u;
    state->legacy_host = host;
    TsmPluginInfoCompat info = {};
    uint32_t deep_hooks_before = state->legacy_hooks_installed;
    uint32_t deep_inline_requests_before = g_legacy_inline_request_count;
    int deep_before_ok = (g_plan.flags & PLAN_DEEP_TEST) ? runtime_text_page_snapshot(&g_deep_legacy_before) : 0;
    ULONGLONG deep_init_begin = GetTickCount64();
    g_tsm_active_plugin = state; int result = 1; int init_completed = guarded_init(init_fn, host, &info, &result); g_tsm_active_plugin = 0;
    if (g_plan.flags & PLAN_DEEP_TEST) {
        char comp[180]; legacy_component(state, comp, sizeof(comp));
        log_u64(RML_LOG_INFO, comp, "DEEP TRACE TsmPluginInit call duration (ms): ", GetTickCount64() - deep_init_begin);
        log_u32(RML_LOG_INFO, comp, "DEEP TRACE TsmPluginInit completed without SEH escape: ", init_completed ? 1u : 0u);
        log_u32(RML_LOG_INFO, comp, "DEEP TRACE TsmPluginInit return value: ", (uint32_t)result);
        log_u32(RML_LOG_INFO, comp, "DEEP TRACE hooks installed during Init: ", state->legacy_hooks_installed - deep_hooks_before);
        log_u32(state->legacy_hook_failures ? RML_LOG_WARN : RML_LOG_INFO, comp, "DEEP TRACE cumulative hook request failures: ", state->legacy_hook_failures);
    }
    int deep_after_ok = (g_plan.flags & PLAN_DEEP_TEST) ? runtime_text_page_snapshot(&g_deep_legacy_after) : 0;
    if ((g_plan.flags & PLAN_DEEP_TEST) && deep_before_ok && deep_after_ok) {
        uint32_t first_rva=0; uint32_t pages=runtime_text_page_diff(&g_deep_legacy_before,&g_deep_legacy_after,&first_rva);
        if (pages) {
            char detail_comp[180]; legacy_component(state, detail_comp, sizeof(detail_comp));
            runtime_text_page_log_diff(detail_comp, "DEEP TRACE TsmPluginInit .text page diff BEGIN", &g_deep_legacy_before, &g_deep_legacy_after);
            uint32_t managed_pages = 0, first_unmanaged = 0;
            uint32_t unmanaged_pages = deep_classify_text_changes(state, &g_deep_legacy_before, &g_deep_legacy_after,
                                                                   deep_inline_requests_before, "TsmPluginInit", &managed_pages, &first_unmanaged);
            log_u32(RML_LOG_INFO, detail_comp, "DEEP TRACE managed changed pages during Init: ", managed_pages);
            log_u32(unmanaged_pages ? RML_LOG_WARN : RML_LOG_INFO, detail_comp, "DEEP TRACE unmanaged changed pages during Init: ", unmanaged_pages);
            if (managed_pages) {
                char target[64]; rml_copy_str(target,sizeof(target),"SOVIET64.exe+0x"); rml_append_hex(target,sizeof(target),first_rva);
                journal_plugin_event(plan, "managed_text_change", target, "host-mediated-code-modification", "deep-test-live-rehearsal", "One or more changed .text pages contained a new RML-mediated inline target during TsmPluginInit");
            }
            if (unmanaged_pages) {
                ++state->unmanaged_init_changes; state->unmanaged_init_pages += unmanaged_pages; if (!state->first_unmanaged_rva) state->first_unmanaged_rva=first_unmanaged;
                ++g_deep_test_stats.unmanaged_text_changes; g_deep_test_stats.unmanaged_text_pages += unmanaged_pages;
                log_fault(RML_LOG_WARN, detail_comp, "PLUGIN DIRECT CODE CHANGE", "Detailed diagnostics found Init-phase SOVIET64 .text changes that cannot be attributed to a host-mediated inline request from this plugin.");
                char target[64]; rml_copy_str(target,sizeof(target),"SOVIET64.exe+0x"); rml_append_hex(target,sizeof(target),first_unmanaged);
                journal_plugin_event(plan, "unmanaged_text_change", target, "direct-code-modification", "deep-test-page-diff", "TsmPluginInit changed .text page(s) not covered by this phase's host-mediated inline targets");
            }
        }
    }
    if (g_plan.flags & PLAN_DEEP_TEST) ++g_deep_test_stats.plugins_observed;
    if (!init_completed) {
        state->failed = 1;
        rml_copy_str(state->status, sizeof(state->status), "TESMIO INIT EXCEPTION");
        log_fault(RML_LOG_ERROR, "TesmioCompat", "PLUGIN EXCEPTION", "Legacy plugin raised a native exception during TsmPluginInit; services are being removed and the module will be unloaded.");
        journal_plugin_event(plan, "legacy_init_exception", 0, "TsmPluginInit", "tesmio-shim", "Native exception during legacy initialization");
        tesmio_compat_remove_services(state); rml_free(state->legacy_host); state->legacy_host = 0; FreeLibrary(module); state->module = 0; return 0;
    }
    if (result != 0) {
        rml_copy_str(state->status, sizeof(state->status), "TESMIO INIT DECLINED");
        log_fault(RML_LOG_ERROR, "TesmioCompat", "PLUGIN INITIALIZATION FAILED", "TsmPluginInit returned nonzero; the plugin was unloaded.");
        journal_plugin_event(plan, "legacy_init_failure", 0, "TsmPluginInit", "tesmio-shim", "TsmPluginInit returned nonzero");
        tesmio_compat_remove_services(state); rml_free(state->legacy_host); state->legacy_host = 0; FreeLibrary(module); state->module = 0; return 0;
    }
    if (info.name) safe_copy_plugin_string(state->name, sizeof(state->name), info.name);
    if (info.version) safe_copy_plugin_string(state->version, sizeof(state->version), info.version);
    state->legacy_initialized = 1; state->legacy_start = (void*)start_fn;
    rml_copy_str(state->status, sizeof(state->status), start_fn ? "TESMIO INIT READY" : "TESMIO LOADED");
    char comp[180]; legacy_component(state, comp, sizeof(comp));
    log_u32(RML_LOG_INFO, comp, "Tesmio plugin API accepted: ", api);
    log_write(RML_LOG_INFO, comp, start_fn ? "TsmPluginInit succeeded; TsmPluginStart staged for phase two." : "TsmPluginInit succeeded; plugin has no start phase.");
    journal_plugin_event(plan, "legacy_init_success", 0, "TsmPluginInit", "tesmio-shim", "Legacy plugin initialized through RML compatibility host");
    if (!start_fn) {
        state->loaded = 1;
        if (state->legacy_hook_failures) {
            rml_copy_str(state->status, sizeof(state->status), "TESMIO ACTIVE — DEGRADED");
            log_fault_u32(RML_LOG_WARN, comp, "PLUGIN DEGRADED", "Legacy plugin loaded successfully but hook requests failed: ", state->legacy_hook_failures);
        } else {
            rml_copy_str(state->status, sizeof(state->status), "TESMIO LOADED");
        }
    }
    if (g_plan.flags & PLAN_DEEP_TEST) {
        char deep_comp[180]; legacy_component(state, deep_comp, sizeof(deep_comp));
        log_u64(RML_LOG_INFO, deep_comp, "DEEP TRACE total legacy Init transaction duration (ms): ", GetTickCount64() - deep_phase_begin);
        log_write(RML_LOG_INFO, deep_comp, "DEEP TRACE legacy plugin initialization transaction END — SUCCESS");
    }
    return 1;
}

static void report_active_requests_for_plugin(PluginState* owner) {
    if (!owner) return;
    char comp[180]; legacy_component(owner, comp, sizeof(comp));
    for (uint32_t i = 0; i < g_legacy_inline_request_count; ++i) {
        LegacyInlineRequest* req = &g_legacy_inline_requests[i];
        if (req->owner != owner || req->target_index >= g_legacy_inline_target_count) continue;
        LegacyInlineTarget* target = &g_legacy_inline_targets[req->target_index];
        char msg[512]; size_t p = 0;
        fmt_put_str(msg, sizeof(msg), &p, "RML legacy inline hook ACTIVE (direct fidelity): ");
        fmt_put_str(msg, sizeof(msg), &p, req->label);
        fmt_put_str(msg, sizeof(msg), &p, " target=");
        fmt_put_str(msg, sizeof(msg), &p, target->target_desc);
        fmt_put_str(msg, sizeof(msg), &p, " continuation=");
        fmt_put_str(msg, sizeof(msg), &p, "0x");
        fmt_put_uint(msg, sizeof(msg), &p, (uintptr_t)req->continuation, 16, 1, 16, '0');
        msg[p] = 0;
        log_write(RML_LOG_INFO, comp, msg);
        // The exact modification was journaled by installInlineHook at commit time.
        // This post-Start message exists only to make the effective chain obvious.
    }
}

int tesmio_compat_start_plugins(void) {
    int all_ok = 1;
    for (uint32_t i = 0; i < g_plugin_count; ++i) {
        PluginState* state = &g_plugins[i]; if (!(state->flags & PLUGIN_TESMIO_LEGACY) || !state->legacy_initialized || !state->legacy_start || state->failed) continue;
        TsmStartFn start = (TsmStartFn)state->legacy_start;
        char comp[180]; legacy_component(state, comp, sizeof(comp));
        log_write(RML_LOG_INFO, comp, "TsmPluginStart BEGIN; compatibility shim is active.");
        journal_plugin_event(plan_find_plugin(state->id), "legacy_start_begin", 0, "TsmPluginStart", "tesmio-shim", "Entering legacy start phase");
        uint32_t deep_hooks_before = state->legacy_hooks_installed;
        uint32_t deep_inline_requests_before = g_legacy_inline_request_count;
        int deep_before_ok = (g_plan.flags & PLAN_DEEP_TEST) ? runtime_text_page_snapshot(&g_deep_legacy_before) : 0;
        ULONGLONG deep_start_begin = GetTickCount64();
        if (g_plan.flags & PLAN_DEEP_TEST) {
            log_write(RML_LOG_INFO, comp, "DEEP TRACE TsmPluginStart transaction BEGIN");
            log_u32(RML_LOG_INFO, comp, "DEEP TRACE hooks active before Start: ", deep_hooks_before);
        }
        g_tsm_active_plugin = state; int result = 1; int start_completed = guarded_start(start, &result); g_tsm_active_plugin = 0;
        if (g_plan.flags & PLAN_DEEP_TEST) {
            log_u64(RML_LOG_INFO, comp, "DEEP TRACE TsmPluginStart call duration (ms): ", GetTickCount64() - deep_start_begin);
            log_u32(RML_LOG_INFO, comp, "DEEP TRACE TsmPluginStart completed without SEH escape: ", start_completed ? 1u : 0u);
            log_u32(RML_LOG_INFO, comp, "DEEP TRACE TsmPluginStart return value: ", (uint32_t)result);
            log_u32(RML_LOG_INFO, comp, "DEEP TRACE hooks installed during Start: ", state->legacy_hooks_installed - deep_hooks_before);
            log_u32(state->legacy_hook_failures ? RML_LOG_WARN : RML_LOG_INFO, comp, "DEEP TRACE cumulative hook request failures: ", state->legacy_hook_failures);
        }
        int deep_after_ok = (g_plan.flags & PLAN_DEEP_TEST) ? runtime_text_page_snapshot(&g_deep_legacy_after) : 0;
        if ((g_plan.flags & PLAN_DEEP_TEST) && deep_before_ok && deep_after_ok) {
            uint32_t first_rva=0; uint32_t pages=runtime_text_page_diff(&g_deep_legacy_before,&g_deep_legacy_after,&first_rva);
            if (pages) {
                runtime_text_page_log_diff(comp, "DEEP TRACE TsmPluginStart .text page diff BEGIN", &g_deep_legacy_before, &g_deep_legacy_after);
                uint32_t managed_pages = 0, first_unmanaged = 0;
                uint32_t unmanaged_pages = deep_classify_text_changes(state, &g_deep_legacy_before, &g_deep_legacy_after,
                                                                       deep_inline_requests_before, "TsmPluginStart", &managed_pages, &first_unmanaged);
                log_u32(RML_LOG_INFO, comp, "DEEP TRACE managed changed pages during Start: ", managed_pages);
                log_u32(unmanaged_pages ? RML_LOG_WARN : RML_LOG_INFO, comp, "DEEP TRACE unmanaged changed pages during Start: ", unmanaged_pages);
                if (managed_pages) {
                    char target[64]; rml_copy_str(target,sizeof(target),"SOVIET64.exe+0x"); rml_append_hex(target,sizeof(target),first_rva);
                    journal_plugin_event(plan_find_plugin(state->id), "managed_text_change", target, "host-mediated-code-modification", "deep-test-live-rehearsal", "One or more changed .text pages contained a new RML-mediated inline target during TsmPluginStart");
                }
                if (unmanaged_pages) {
                    ++state->unmanaged_start_changes; state->unmanaged_start_pages += unmanaged_pages; if (!state->first_unmanaged_rva) state->first_unmanaged_rva=first_unmanaged;
                    ++g_deep_test_stats.unmanaged_text_changes; g_deep_test_stats.unmanaged_text_pages += unmanaged_pages;
                    log_fault(RML_LOG_WARN, comp, "PLUGIN DIRECT CODE CHANGE", "Detailed diagnostics found Start-phase SOVIET64 .text changes that cannot be attributed to a host-mediated inline request from this plugin.");
                    char target[64]; rml_copy_str(target,sizeof(target),"SOVIET64.exe+0x"); rml_append_hex(target,sizeof(target),first_unmanaged);
                    journal_plugin_event(plan_find_plugin(state->id), "unmanaged_text_change", target, "direct-code-modification", "deep-test-page-diff", "TsmPluginStart changed .text page(s) not covered by this phase's host-mediated inline targets");
                }
            }
        }
        log_write(RML_LOG_INFO, comp, "TsmPluginStart returned to RML compatibility host.");
        if (start_completed) report_active_requests_for_plugin(state);
        if (!start_completed) {
            state->failed = 1; all_ok = 0;
            if (state->legacy_hooks_installed) {
                rml_copy_str(state->status, sizeof(state->status), "TESMIO START EXCEPTION • LAUNCH ABORTED");
                log_fault(RML_LOG_ERROR, comp, "PLUGIN EXCEPTION", "Native exception during TsmPluginStart after a legacy hook became active; the launcher must abort this trial process.");
            } else {
                rml_copy_str(state->status, sizeof(state->status), "TESMIO START EXCEPTION");
                log_fault(RML_LOG_ERROR, comp, "PLUGIN EXCEPTION", "Native exception during TsmPluginStart before any host-mediated hook commit; module rolled back.");
                tesmio_compat_remove_services(state); rml_free(state->legacy_host); state->legacy_host = 0; FreeLibrary(state->module); state->module = 0; state->legacy_initialized = 0;
            }
            journal_plugin_event(plan_find_plugin(state->id), "legacy_start_exception", 0, "TsmPluginStart", "tesmio-shim", (state->legacy_hooks_installed) ? "Native exception after direct legacy hook activation; launch abort required" : "Native exception before hook staging; module rolled back");
            continue;
        }
        if (result != 0) {
            state->failed = 1; all_ok = 0;
            if (state->legacy_hooks_installed) {
                rml_copy_str(state->status, sizeof(state->status), "TESMIO START FAILED • LAUNCH ABORTED");
                log_fault(RML_LOG_ERROR, comp, "PLUGIN INITIALIZATION FAILED", "TsmPluginStart returned nonzero after a legacy hook became active; the launcher must abort this trial process.");
            } else {
                rml_copy_str(state->status, sizeof(state->status), "TESMIO START FAILED");
                log_fault(RML_LOG_ERROR, comp, "PLUGIN INITIALIZATION FAILED", "TsmPluginStart returned nonzero; no hooks were committed, so services and module are being rolled back.");
                tesmio_compat_remove_services(state); rml_free(state->legacy_host); state->legacy_host = 0; FreeLibrary(state->module); state->module = 0; state->legacy_initialized = 0;
            }
            journal_plugin_event(plan_find_plugin(state->id), "legacy_start_failure", 0, "TsmPluginStart", "tesmio-shim", (state->legacy_hooks_installed) ? "Start failed after direct legacy hook activation; launch abort required" : "Start failed before hook staging; module rolled back");
            continue;
        }
        state->loaded = 1;
        if (state->legacy_hook_failures) {
            rml_copy_str(state->status, sizeof(state->status), "TESMIO ACTIVE — DEGRADED");
            log_fault_u32(RML_LOG_WARN, comp, "PLUGIN DEGRADED", "TsmPluginStart returned success, but requested hooks were not installed: ", state->legacy_hook_failures);
        } else {
            rml_copy_str(state->status, sizeof(state->status), "TESMIO ACTIVE");
        }
        if (g_plan.flags & PLAN_DEEP_TEST) {
            log_u32(RML_LOG_INFO, comp, "TsmPluginStart succeeded under Detailed Diagnostic Logging; host-mediated hooks active: ", state->legacy_hooks_installed);
            log_write(RML_LOG_INFO, comp, "DEEP TRACE TsmPluginStart transaction END — SUCCESS");
            journal_plugin_event(plan_find_plugin(state->id), "legacy_start_success", 0, "TsmPluginStart", "deep-test-live-rehearsal", "Legacy Start completed with real host-mediated hooks inside manual forensic session");
        } else if (state->legacy_hooks_installed) {
            log_u32(RML_LOG_INFO, comp, "TsmPluginStart succeeded; direct legacy hook(s) active with no RML steady-state dispatcher: ", state->legacy_hooks_installed);
            journal_plugin_event(plan_find_plugin(state->id), "legacy_start_success", 0, "TsmPluginStart", "tesmio-shim", "Legacy Start complete; direct compatibility hook path active");
        } else {
            log_write(RML_LOG_INFO, comp, "TsmPluginStart succeeded; no host-mediated executable hook was requested by this plugin.");
            journal_plugin_event(plan_find_plugin(state->id), "legacy_start_success", 0, "TsmPluginStart", "tesmio-shim", "Legacy Start completed successfully");
        }
    }
    return all_ok;
}
