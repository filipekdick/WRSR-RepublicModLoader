#include "runtime_internal.h"

// WRSR 1.1.1.9 workshop enumeration forwards subscribed items through the
// C3D WorkshopForward layer. RML filters that existing forwarding boundary only;
// Steam subscription state and WRSR's Workshop implementation remain intact.
// Targets are resolved by friendly/decorated export name with an IAT-pointer
// fallback for ordinal thunks.
//
// C3DDLL64.dll 1.1.1.9 export evidence:
//   ordinal 552 ?C3DPath_WorkshopForwardAddItem@@YAX_KPEAD@Z
//   ordinal 554 ?C3DPath_WorkshopForwardLoadItems@@YAXPEAD@Z
//   ordinal 555 ?C3DPath_WorkshopForwardSaveItems@@YAXPEAD@Z

typedef void (*WorkshopForwardLoadItemsFn)(char*);
typedef void (*WorkshopForwardSaveItemsFn)(char*);
typedef void (*WorkshopForwardAddItemFn)(uint64_t, char*);

static WorkshopForwardLoadItemsFn g_workshop_load_original = 0;
static WorkshopForwardSaveItemsFn g_workshop_save_original = 0;
static WorkshopForwardAddItemFn g_workshop_add_original = 0;
static void** g_workshop_load_slot = 0;
static void** g_workshop_save_slot = 0;
static void** g_workshop_add_slot = 0;
static uint32_t g_workshop_suppressed = 0;
static uint32_t g_workshop_forwarded = 0;
static uint32_t g_workshop_filter_ready = 0;

static int ascii_lower_ws(int c) { return c >= 'A' && c <= 'Z' ? c + ('a' - 'A') : c; }
static int ascii_equal_i_ws(const char* a, const char* b) {
    if (!a || !b) return 0;
    while (*a && *b) {
        if (ascii_lower_ws((unsigned char)*a) != ascii_lower_ws((unsigned char)*b)) return 0;
        ++a; ++b;
    }
    return *a == 0 && *b == 0;
}
static uint16_t pe16_ws(const BYTE* p) { return (uint16_t)p[0] | ((uint16_t)p[1] << 8); }
static uint32_t pe32_ws(const BYTE* p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}
static uint64_t pe64_ws(const BYTE* p) { return (uint64_t)pe32_ws(p) | ((uint64_t)pe32_ws(p + 4) << 32); }

static int readable_ws(const void* p, size_t n) {
    if (!p || !n) return 0;
    uintptr_t start = (uintptr_t)p;
    uintptr_t end = start + n;
    if (end < start) return 0;
    MEMORY_BASIC_INFORMATION mbi = {};
    uintptr_t cur = start;
    while (cur < end) {
        if (!VirtualQuery((LPCVOID)cur, &mbi, sizeof(mbi))) return 0;
        if (mbi.State != MEM_COMMIT || (mbi.Protect & (PAGE_NOACCESS | PAGE_GUARD))) return 0;
        uintptr_t next = (uintptr_t)mbi.BaseAddress + mbi.RegionSize;
        if (next <= cur) return 0;
        cur = next;
    }
    return 1;
}

static int module_image_size_ws(void* module, uint32_t* out_size) {
    BYTE* base = (BYTE*)module;
    if (!base || !readable_ws(base, 0x100) || pe16_ws(base) != 0x5A4D) return 0;
    uint32_t lf = pe32_ws(base + 0x3C);
    if (lf > 0x100000 || !readable_ws(base + lf, 0x200) || pe32_ws(base + lf) != 0x4550) return 0;
    BYTE* opt = base + lf + 24;
    if (pe16_ws(opt) != 0x20B) return 0;
    uint32_t size = pe32_ws(opt + 56);
    if (size < 0x10000 || size > 0x80000000u) return 0;
    if (out_size) *out_size = size;
    return 1;
}

// Locate an import slot by imported name OR by its already-resolved current
// function pointer. Pointer matching matters for ordinal imports: the name thunk
// deliberately has no IMAGE_IMPORT_BY_NAME record in that case.
static void** find_iat_slot_ws(void* module, const char* dll, const char* friendly,
                               const char* decorated, void* resolved_export,
                               const char** method_out) {
    if (method_out) *method_out = "not found";
    if (!module || !dll) return 0;
    BYTE* base = (BYTE*)module;
    uint32_t image_size = 0;
    if (!module_image_size_ws(module, &image_size)) return 0;
    uint32_t lf = pe32_ws(base + 0x3C);
    BYTE* opt = base + lf + 24;
    uint32_t import_rva = pe32_ws(opt + 112 + 8);
    uint32_t import_size = pe32_ws(opt + 112 + 12);
    if (!import_rva || import_rva >= image_size || import_size < 20) return 0;

    for (uint32_t di = 0; di < 4096; ++di) {
        uint32_t off = import_rva + di * 20u;
        if (off + 20u > image_size) return 0;
        BYTE* d = base + off;
        uint32_t original_thunk = pe32_ws(d + 0);
        uint32_t name_rva = pe32_ws(d + 12);
        uint32_t first_thunk = pe32_ws(d + 16);
        if (!original_thunk && !name_rva && !first_thunk) break;
        if (!name_rva || name_rva >= image_size || !first_thunk || first_thunk >= image_size) continue;
        const char* imported_dll = (const char*)(base + name_rva);
        if (!readable_ws(imported_dll, 1) || !ascii_equal_i_ws(imported_dll, dll)) continue;

        uint32_t names = original_thunk ? original_thunk : first_thunk;
        for (uint32_t i = 0; i < 65536; ++i) {
            uint64_t nrva_off = (uint64_t)names + (uint64_t)i * 8u;
            uint64_t iat_off = (uint64_t)first_thunk + (uint64_t)i * 8u;
            if (nrva_off + 8 > image_size || iat_off + 8 > image_size) break;
            uint64_t thunk = pe64_ws(base + (uint32_t)nrva_off);
            if (!thunk) break;
            void** slot = (void**)(base + (uint32_t)iat_off);

            // First match the live IAT pointer. This succeeds for named and
            // ordinal imports after the Windows loader has fixed the table up.
            if (resolved_export && readable_ws(slot, sizeof(void*)) && *slot == resolved_export) {
                if (method_out) *method_out = "resolved export pointer";
                return slot;
            }

            if (thunk & 0x8000000000000000ull) continue; // ordinal: no name record
            uint32_t by_name = (uint32_t)thunk;
            if (by_name + 3 >= image_size) continue;
            const char* name = (const char*)(base + by_name + 2);
            if (!readable_ws(name, 1)) continue;
            if (friendly && rml_strcmp(name, friendly) == 0) {
                if (method_out) *method_out = "friendly import name";
                return slot;
            }
            if (decorated && rml_strcmp(name, decorated) == 0) {
                if (method_out) *method_out = "MSVC decorated import name";
                return slot;
            }
        }
    }
    return 0;
}

static int patch_slot_ws(void** slot, void* replacement, void** original) {
    if (!slot || !replacement || !readable_ws(slot, sizeof(void*))) return 0;
    void* before = *slot;
    DWORD old = 0;
    if (!VirtualProtect(slot, sizeof(void*), PAGE_READWRITE, &old)) return 0;
    if (*slot != before) {
        DWORD ignored = 0; VirtualProtect(slot, sizeof(void*), old, &ignored);
        return 0;
    }
    *slot = replacement;
    DWORD ignored = 0;
    if (!VirtualProtect(slot, sizeof(void*), old, &ignored)) {
        *slot = before;
        VirtualProtect(slot, sizeof(void*), old, &ignored);
        return 0;
    }
    if (original) *original = before;
    return 1;
}

static void restore_slot_ws(void** slot, void* original) {
    if (!slot || !original) return;
    DWORD old = 0;
    if (!VirtualProtect(slot, sizeof(void*), PAGE_READWRITE, &old)) return;
    *slot = original;
    DWORD ignored = 0;
    VirtualProtect(slot, sizeof(void*), old, &ignored);
}

static void workshop_load_filter(char* cache_name) {
    (void)cache_name;
    // WRSR already called WorkshopForwardClearItems immediately beforehand.
    // Suppressing LoadItems keeps the normal all-subscribed offline cache from
    // repopulating IDs disabled by the selected RML playlist.
}

static void workshop_save_filter(char* cache_name) {
    (void)cache_name;
    // Do not overwrite WRSR's ordinary offline cache with an RML playlist subset.
    log_u32(RML_LOG_INFO, "WorkshopFilter", "WRSR Workshop enumeration forwarded launcher-enabled subscribed items: ", g_workshop_forwarded);
    log_u32(RML_LOG_INFO, "WorkshopFilter", "WRSR Workshop enumeration suppressed subscribed items not in launcher allow-list: ", g_workshop_suppressed);
}

static void workshop_add_filter(uint64_t item_id, char* install_path) {
    // Default-deny: the launcher sends the exact set
    // of Workshop IDs enabled in the selected playlist. Any Steam-subscribed
    // item that is not in that allow-list is withheld from WRSR, including an
    // item that exists/subscribes but was absent from the current launcher inventory.
    if (!plan_workshop_enabled(item_id)) {
        ++g_workshop_suppressed;
        return;
    }
    ++g_workshop_forwarded;
    if (g_workshop_add_original) g_workshop_add_original(item_id, install_path);
}

static void log_slot_result(const char* label, void** slot, const char* method) {
    char msg[384]; size_t p = 0;
    const char* prefix = slot ? "resolved " : "FAILED ";
    while (*prefix && p + 1 < sizeof(msg)) msg[p++] = *prefix++;
    while (*label && p + 1 < sizeof(msg)) msg[p++] = *label++;
    const char* mid = slot ? " via " : ": ";
    while (*mid && p + 1 < sizeof(msg)) msg[p++] = *mid++;
    const char* m = method ? method : "unknown";
    while (*m && p + 1 < sizeof(msg)) msg[p++] = *m++;
    msg[p] = 0;
    log_write(slot ? RML_LOG_INFO : RML_LOG_ERROR, "WorkshopFilter", msg);
}

int workshop_filter_install(void) {
    if (g_workshop_filter_ready) return 1;
    const int filter_workshop = (g_plan.flags & PLAN_FILTER_WORKSHOP) != 0;
    if (!filter_workshop) return 1;

    HMODULE exe = GetModuleHandleW(0);
    HMODULE c3d = GetModuleHandleW(L"C3DDLL64.dll");
    if (!exe || !c3d) {
        log_write(RML_LOG_ERROR, "WorkshopFilter", "SOVIET64.exe or C3DDLL64.dll is not available while installing mod visibility filters.");
        return 0;
    }

    const char* dll = "C3DDLL64.DLL";
    const char* load_decorated = "?C3DPath_WorkshopForwardLoadItems@@YAXPEAD@Z";
    const char* add_decorated  = "?C3DPath_WorkshopForwardAddItem@@YAX_KPEAD@Z";
    const char* save_decorated = "?C3DPath_WorkshopForwardSaveItems@@YAXPEAD@Z";

    if (filter_workshop) {
        void* load_export = (void*)GetProcAddress(c3d, load_decorated);
        void* add_export  = (void*)GetProcAddress(c3d, add_decorated);
        void* save_export = (void*)GetProcAddress(c3d, save_decorated);
        const char* load_method = 0; const char* add_method = 0; const char* save_method = 0;
        g_workshop_load_slot = find_iat_slot_ws(exe, dll, "C3DPath_WorkshopForwardLoadItems", load_decorated, load_export, &load_method);
        g_workshop_add_slot = find_iat_slot_ws(exe, dll, "C3DPath_WorkshopForwardAddItem", add_decorated, add_export, &add_method);
        g_workshop_save_slot = find_iat_slot_ws(exe, dll, "C3DPath_WorkshopForwardSaveItems", save_decorated, save_export, &save_method);
        log_slot_result("WorkshopForwardLoadItems", g_workshop_load_slot, load_method);
        log_slot_result("WorkshopForwardAddItem", g_workshop_add_slot, add_method);
        log_slot_result("WorkshopForwardSaveItems", g_workshop_save_slot, save_method);
        if (!g_workshop_load_slot || !g_workshop_add_slot || !g_workshop_save_slot) {
            log_write(RML_LOG_ERROR, "WorkshopFilter", "Workshop-forward import resolution remains incomplete; launcher Workshop Off states will not be enforced rather than installing a partial filter.");
            return 0;
        }
    }



    void* load_orig = 0; void* add_orig = 0; void* save_orig = 0;
    if (filter_workshop) {
        if (!patch_slot_ws(g_workshop_load_slot, (void*)&workshop_load_filter, &load_orig)) {
            log_write(RML_LOG_ERROR, "WorkshopFilter", "Failed to patch WorkshopForwardLoadItems IAT slot."); return 0;
        }
        if (!patch_slot_ws(g_workshop_add_slot, (void*)&workshop_add_filter, &add_orig)) {
            restore_slot_ws(g_workshop_load_slot, load_orig);
            log_write(RML_LOG_ERROR, "WorkshopFilter", "Failed to patch WorkshopForwardAddItem IAT slot; earlier filter patch rolled back."); return 0;
        }
        if (!patch_slot_ws(g_workshop_save_slot, (void*)&workshop_save_filter, &save_orig)) {
            restore_slot_ws(g_workshop_add_slot, add_orig);
            restore_slot_ws(g_workshop_load_slot, load_orig);
            log_write(RML_LOG_ERROR, "WorkshopFilter", "Failed to patch WorkshopForwardSaveItems IAT slot; earlier filter patches rolled back."); return 0;
        }
    }


    if (filter_workshop) {
        g_workshop_load_original = (WorkshopForwardLoadItemsFn)load_orig;
        g_workshop_add_original = (WorkshopForwardAddItemFn)add_orig;
        g_workshop_save_original = (WorkshopForwardSaveItemsFn)save_orig;
        log_u32(RML_LOG_INFO, "WorkshopFilter", "Launcher Workshop visibility filter installed; explicitly enabled item IDs: ", g_plan.enabled_workshop_count);
        log_write(RML_LOG_INFO, "WorkshopFilter", "Default-deny Workshop policy active: only launcher-enabled subscribed Workshop IDs will be forwarded into WRSR's workshop_subscribed namespace for this RML session.");
    }

    g_workshop_filter_ready = 1;
    return 1;
}

