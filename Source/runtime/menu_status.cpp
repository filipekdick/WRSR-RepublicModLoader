#include "runtime_internal.h"

// Main-menu status indication for RML.
//
// WRSR 1.1.1.9 renders the lower-left version/GPU line from FUN_14028af60:
//   C3D_FONTMANAGER::PrintLeftUnicode(...,
//       L"v%d.%d.%d.%d (64 bit DX11.1 - GPU: %ls)", 1,1,1,9,gpuName);
//
// Rather than hook the renderer, RML locates the unique RIP-relative LEA that
// supplies this exact format string and repoints only its disp32 to an RML-owned
// read-only copy with a short status suffix. The vanilla string itself is not
// modified and the PrintLeftUnicode call is left untouched.
//
// This is intentionally optional. Any ambiguity, unexpected instruction state,
// allocation failure, or third-party modification we cannot understand causes a
// logged refusal while the loader/game continue normally.

struct MenuPeView {
    BYTE* base;
    uint32_t image_size;
    BYTE* text;
    uint32_t text_rva;
    uint32_t text_size;
    BYTE* sections;
    uint16_t section_count;
};

struct MenuLeaRef {
    BYTE* instruction;
    BYTE* displacement;
    BYTE* instruction_end;
    uintptr_t target;
    uint32_t instruction_rva;
};

static uint16_t ms_u16(const BYTE* p) {
    return (uint16_t)p[0] | ((uint16_t)p[1] << 8);
}

static uint32_t ms_u32(const BYTE* p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) |
           ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}

static int32_t ms_i32(const BYTE* p) {
    return (int32_t)ms_u32(p);
}

static void ms_put_i32(BYTE* p, int32_t v) {
    uint32_t x = (uint32_t)v;
    p[0] = (BYTE)(x & 0xFFu);
    p[1] = (BYTE)((x >> 8) & 0xFFu);
    p[2] = (BYTE)((x >> 16) & 0xFFu);
    p[3] = (BYTE)((x >> 24) & 0xFFu);
}

static int ms_section_name_eq(const BYTE* section, const char* name) {
    size_t n = rml_strlen(name);
    if (!n || n > 8) return 0;
    for (size_t i = 0; i < n; ++i)
        if ((char)section[i] != name[i]) return 0;
    return n == 8 || section[n] == 0;
}

static int ms_pe_open(MenuPeView* out) {
    rml_memset(out, 0, sizeof(*out));
    BYTE* base = (BYTE*)GetModuleHandleW(0);
    if (!base || ms_u16(base) != 0x5A4D) return 0;
    uint32_t lfanew = ms_u32(base + 0x3C);
    if (lfanew > 0x100000u || ms_u32(base + lfanew) != 0x00004550u) return 0;
    BYTE* file = base + lfanew + 4;
    if (ms_u16(file) != 0x8664) return 0;
    uint16_t section_count = ms_u16(file + 2);
    uint16_t optional_size = ms_u16(file + 16);
    if (!section_count || section_count > 96) return 0;
    BYTE* optional = file + 20;
    if (ms_u16(optional) != 0x20B || optional_size < 112 + 16 * 8) return 0;
    uint32_t image_size = ms_u32(optional + 56);
    uint32_t headers_size = ms_u32(optional + 60);
    if (image_size < 0x10000u || headers_size > image_size) return 0;
    uint64_t table_end = (uint64_t)lfanew + 4u + 20u + optional_size +
                         (uint64_t)section_count * 40u;
    if (table_end > headers_size) return 0;

    BYTE* table = optional + optional_size;
    out->base = base;
    out->image_size = image_size;
    out->sections = table;
    out->section_count = section_count;

    for (uint16_t i = 0; i < section_count; ++i) {
        BYTE* s = table + i * 40;
        uint32_t va = ms_u32(s + 12);
        uint32_t vs = ms_u32(s + 8);
        uint32_t raw = ms_u32(s + 16);
        uint32_t span = vs ? vs : raw;
        if (!span || va >= image_size || span > image_size - va) continue;
        if (ms_section_name_eq(s, ".text")) {
            uint32_t characteristics = ms_u32(s + 36);
            if ((characteristics & 0x60000000u) != 0x60000000u) continue;
            out->text = base + va;
            out->text_rva = va;
            out->text_size = span;
        }
    }
    return out->text != 0;
}

static int ms_page_readable(uintptr_t address, size_t bytes) {
    if (!address || !bytes) return 0;
    MEMORY_BASIC_INFORMATION mbi = {};
    if (VirtualQuery((LPCVOID)address, &mbi, sizeof(mbi)) != sizeof(mbi)) return 0;
    if (mbi.State != MEM_COMMIT) return 0;
    if ((mbi.Protect & PAGE_GUARD) || ((mbi.Protect & 0xFFu) == PAGE_NOACCESS)) return 0;
    uintptr_t region = (uintptr_t)mbi.BaseAddress;
    uintptr_t region_end = region + mbi.RegionSize;
    if (region_end < region || address < region) return 0;
    if (bytes > region_end - address) return 0;
    return 1;
}

static int ms_wide_prefix(const wchar_t* value, const wchar_t* prefix) {
    if (!value || !prefix) return 0;
    size_t n = rml_wcslen(prefix);
    if (!n || n > 160) return 0;
    if (!ms_page_readable((uintptr_t)value, (n + 1) * sizeof(wchar_t))) return 0;
    for (size_t i = 0; i < n; ++i)
        if (value[i] != prefix[i]) return 0;
    return 1;
}

static size_t ms_safe_wcslen(const wchar_t* value, size_t cap) {
    if (!value || !cap) return 0;
    for (size_t i = 0; i < cap; ++i) {
        if (!ms_page_readable((uintptr_t)(value + i), sizeof(wchar_t))) return 0;
        if (!value[i]) return i;
    }
    return 0;
}

static int ms_contains_ascii_wide(const wchar_t* haystack, size_t length, const wchar_t* needle) {
    size_t n = rml_wcslen(needle);
    if (!n || n > length) return 0;
    for (size_t i = 0; i + n <= length; ++i) {
        size_t j = 0;
        while (j < n && haystack[i + j] == needle[j]) ++j;
        if (j == n) return 1;
    }
    return 0;
}

static int ms_known_menu_function(uint32_t* begin_rva, uint32_t* end_rva) {
    static const char* hash_1119 = "296644a9f207d609031fc2ae73fed2dcb34619a1d55a35d1c7b51965ce6841b8";
    // Function containing Tesmio P_MENU_VERSION_SITE 0x28B54C.
    if (rml_strcmp(g_plan.game_sha256, "4df5fbecc75300e3b843ae4e6426761c84ae2e989321c011c6f560be79122125") == 0) {
        *begin_rva = 0x0028AEE0u;
        *end_rva = 0x0028B5B0u;
        return 1;
    }
    if (rml_strcmp(g_plan.game_sha256, hash_1119) == 0) {
        // Supplied 1.1.1.9 decompilation:
        // FUN_14028af60 -> next function FUN_14028b630.
        *begin_rva = 0x0028AF60u;
        *end_rva   = 0x0028B630u;
        return 1;
    }
    return 0;
}

static uintptr_t ms_find_exact_wide_anchor(const MenuPeView* pe,
                                            const wchar_t* anchor,
                                            uint32_t* matches) {
    size_t chars = rml_wcslen(anchor) + 1;
    size_t bytes = chars * sizeof(wchar_t);
    uintptr_t found = 0;
    *matches = 0;

    for (uint16_t i = 0; i < pe->section_count; ++i) {
        BYTE* s = pe->sections + i * 40;
        uint32_t va = ms_u32(s + 12);
        uint32_t vs = ms_u32(s + 8);
        uint32_t raw = ms_u32(s + 16);
        uint32_t span = vs ? vs : raw;
        uint32_t characteristics = ms_u32(s + 36);
        if (!(characteristics & 0x40000000u)) continue; // IMAGE_SCN_MEM_READ
        if (va >= pe->image_size || span > pe->image_size - va || span < bytes) continue;
        BYTE* begin = pe->base + va;
        BYTE* end = begin + span - bytes;
        for (BYTE* p = begin; p <= end; p += sizeof(wchar_t)) {
            if (rml_memeq(p, anchor, bytes)) {
                found = (uintptr_t)p;
                ++*matches;
            }
        }
    }
    return found;
}

static int ms_decode_rip_lea(BYTE* p, BYTE* end, MenuLeaRef* out) {
    BYTE* start = p;
    if (p >= end) return 0;
    if (*p >= 0x40 && *p <= 0x4F) ++p; // optional REX
    if (p + 6 > end || p[0] != 0x8D || (p[1] & 0xC7u) != 0x05u) return 0;
    BYTE* instruction_end = p + 6;
    out->instruction = start;
    out->displacement = p + 2;
    out->instruction_end = instruction_end;
    out->target = (uintptr_t)instruction_end + (intptr_t)ms_i32(p + 2);
    out->instruction_rva = 0;
    return 1;
}

static uint32_t ms_find_menu_refs(const MenuPeView* pe,
                                  uintptr_t exact_anchor,
                                  MenuLeaRef* selected,
                                  const wchar_t** selected_format) {
    const wchar_t* prefix = L"v%d.%d.%d.%d (64 bit DX11.1 - GPU: %ls)";
    uint32_t known_begin = 0, known_end = 0;
    int known = ms_known_menu_function(&known_begin, &known_end);
    uint32_t matches = 0;

    BYTE* scan_begin = pe->text;
    BYTE* scan_end = pe->text + pe->text_size;
    if (known && known_begin >= pe->text_rva && known_end > known_begin &&
        known_end <= pe->text_rva + pe->text_size) {
        scan_begin = pe->base + known_begin;
        scan_end = pe->base + known_end;
    }

    for (BYTE* p = scan_begin; p + 7 <= scan_end; ++p) {
        MenuLeaRef ref = {};
        if (!ms_decode_rip_lea(p, scan_end, &ref)) continue;
        // We scan bytewise so a REX-prefixed LEA would otherwise be decoded
        // twice: once from the REX byte and again from the 0x8D opcode. Skip to
        // the end of every successfully decoded LEA before continuing.
        p = ref.instruction_end - 1;
        ref.instruction_rva = (uint32_t)((uintptr_t)ref.instruction - (uintptr_t)pe->base);

        const wchar_t* candidate = (const wchar_t*)ref.target;
        int accepted = 0;
        if (exact_anchor && ref.target == exact_anchor) {
            accepted = 1;
        } else if (ms_wide_prefix(candidate, prefix)) {
            // Compatibility case: another loader may already have repointed the
            // LEA at a validated copy with an appended tag. Preserve that text
            // rather than blindly restoring vanilla.
            accepted = 1;
        }
        if (!accepted) continue;

        if (known && (ref.instruction_rva < known_begin || ref.instruction_rva >= known_end))
            continue;

        *selected = ref;
        *selected_format = candidate;
        ++matches;
    }
    return matches;
}

static uintptr_t ms_align_up(uintptr_t value, uintptr_t alignment) {
    return (value + alignment - 1u) & ~(alignment - 1u);
}

static void* ms_alloc_near(uintptr_t rip, size_t size) {
    // RIP-relative disp32 gives a +/-2 GiB window. Stay comfortably inside it.
    const uintptr_t reach = 0x70000000ull;
    const uintptr_t granularity = 0x10000ull; // Windows allocation granularity
    uintptr_t low = rip > reach ? rip - reach : granularity;
    uintptr_t high = rip + reach;
    if (high < rip) high = ~(uintptr_t)0;
    low = ms_align_up(low, granularity);

    uintptr_t cursor = low;
    uint32_t guard = 0;
    while (cursor < high && guard++ < 131072u) {
        MEMORY_BASIC_INFORMATION mbi = {};
        if (VirtualQuery((LPCVOID)cursor, &mbi, sizeof(mbi)) != sizeof(mbi)) break;
        uintptr_t region = (uintptr_t)mbi.BaseAddress;
        uintptr_t region_end = region + mbi.RegionSize;
        if (region_end <= region) break;

        if (mbi.State == MEM_FREE) {
            uintptr_t candidate = ms_align_up(region < low ? low : region, granularity);
            if (candidate >= region && candidate < region_end &&
                size <= region_end - candidate && candidate < high) {
                void* memory = VirtualAlloc((LPVOID)candidate, size,
                                            MEM_RESERVE | MEM_COMMIT,
                                            PAGE_READWRITE);
                if (memory) {
                    intptr_t delta = (intptr_t)(uintptr_t)memory - (intptr_t)rip;
                    if (delta >= (intptr_t)INT32_MIN && delta <= (intptr_t)INT32_MAX)
                        return memory;
                    VirtualFree(memory, 0, MEM_RELEASE);
                }
            }
        }
        cursor = region_end;
    }
    return 0;
}

static void ms_append_ascii(wchar_t* dst, size_t cap, const char* src) {
    if (!dst || !cap || !src) return;
    size_t d = rml_wcslen(dst);
    while (*src && d + 1 < cap) {
        char c = *src++;
        dst[d++] = (wchar_t)((unsigned char)c);
    }
    dst[d] = 0;
}

static void ms_append_wide(wchar_t* dst, size_t cap, const wchar_t* src) {
    if (!dst || !cap || !src) return;
    size_t d = rml_wcslen(dst);
    while (*src && d + 1 < cap) dst[d++] = *src++;
    dst[d] = 0;
}

static int ms_repoint_lea(const MenuLeaRef* ref, uintptr_t new_target) {
    intptr_t delta = (intptr_t)new_target - (intptr_t)(uintptr_t)ref->instruction_end;
    if (delta < (intptr_t)INT32_MIN || delta > (intptr_t)INT32_MAX) return 0;

    // Re-read immediately before the write. If somebody changed this instruction
    // after our discovery pass, refuse rather than racing another patcher.
    MenuLeaRef current = {};
    if (!ms_decode_rip_lea(ref->instruction, ref->instruction + 16, &current)) return 0;
    if (current.displacement != ref->displacement || current.target != ref->target) return 0;

    DWORD old_protect = 0;
    if (!VirtualProtect(ref->displacement, 4, PAGE_EXECUTE_READWRITE, &old_protect)) return 0;
    ms_put_i32(ref->displacement, (int32_t)delta);
    FlushInstructionCache(GetCurrentProcess(), ref->displacement, 4);
    DWORD ignored = 0;
    // Once the displacement is changed the new string must remain allocated.
    // A protection-restore failure is therefore not treated as a patch failure
    // (which would tempt the caller to free live storage). We still attempt the
    // restore immediately and leave the patched instruction valid either way.
    VirtualProtect(ref->displacement, 4, old_protect, &ignored);
    return 1;
}

int install_menu_status(uint32_t bootstrap_status) {
    MenuPeView pe = {};
    if (!ms_pe_open(&pe)) {
        log_write(RML_LOG_WARN, "MenuStatus", "Status line unavailable: main game image could not be validated.");
        return 0;
    }

    const wchar_t* vanilla = L"v%d.%d.%d.%d (64 bit DX11.1 - GPU: %ls)";
    uint32_t anchor_matches = 0;
    uintptr_t exact_anchor = ms_find_exact_wide_anchor(&pe, vanilla, &anchor_matches);
    log_u32(RML_LOG_INFO, "MenuStatus", "Vanilla version-format matches: ", anchor_matches);
    if (anchor_matches > 1) {
        log_write(RML_LOG_WARN, "MenuStatus", "Status line refused: vanilla version format is ambiguous.");
        return 0;
    }

    MenuLeaRef ref = {};
    const wchar_t* current_format = 0;
    uint32_t refs = ms_find_menu_refs(&pe, exact_anchor, &ref, &current_format);
    log_u32(RML_LOG_INFO, "MenuStatus", "Validated version-format LEA references: ", refs);
    if (refs != 1 || !current_format) {
        log_write(RML_LOG_WARN, "MenuStatus", refs
            ? "Status line refused: more than one validated menu format reference was found."
            : "Status line unavailable: the WRSR main-menu version format reference was not resolved.");
        return 0;
    }

    size_t current_len = ms_safe_wcslen(current_format, 384);
    if (!current_len) {
        log_write(RML_LOG_WARN, "MenuStatus", "Status line refused: current menu format string is unreadable or unbounded.");
        return 0;
    }
    if (ms_contains_ascii_wide(current_format, current_len, L"RML ") ||
        ms_contains_ascii_wide(current_format, current_len, L"Republic Mod Loader")) {
        log_write(RML_LOG_INFO, "MenuStatus", "RML status tag is already present; no second patch was applied.");
        return 1;
    }

    wchar_t combined[512] = {};
    if (current_len + 64 >= 512) {
        log_write(RML_LOG_WARN, "MenuStatus", "Status line refused: existing version format leaves insufficient bounded space for an RML tag.");
        return 0;
    }
    for (size_t i = 0; i <= current_len; ++i) combined[i] = current_format[i];
    if (g_plan.flags & PLAN_DEEP_TEST) {
        // Diagnostic mode uses a compact, user-facing signpost rather than the
        // normal loader version/status suffix. It is intentionally easy to spot
        // in screenshots of the WRSR splash/main-menu version line.
        ms_append_wide(combined, 512, bootstrap_status
            ? L" | [Republic Mod Loader - Diagnostic mode - degraded]"
            : L" | [Republic Mod Loader - Diagnostic mode]");
    } else {
        ms_append_wide(combined, 512, L" | Republic Mod Loader ");
        ms_append_ascii(combined, 512, RML_RUNTIME_VERSION);
        ms_append_wide(combined, 512, bootstrap_status ? L" DEGRADED" : L" ACTIVE");
    }

    size_t bytes = (rml_wcslen(combined) + 1) * sizeof(wchar_t);
    void* storage = ms_alloc_near((uintptr_t)ref.instruction_end, bytes);
    if (!storage) {
        log_write(RML_LOG_WARN, "MenuStatus", "Status line unavailable: no read-only string allocation could be reserved within RIP-relative range.");
        return 0;
    }
    rml_memcpy(storage, combined, bytes);
    DWORD old = 0;
    if (!VirtualProtect(storage, bytes, PAGE_READONLY, &old)) {
        VirtualFree(storage, 0, MEM_RELEASE);
        log_write(RML_LOG_WARN, "MenuStatus", "Status line unavailable: could not seal the RML format string read-only.");
        return 0;
    }

    if (!ms_repoint_lea(&ref, (uintptr_t)storage)) {
        VirtualFree(storage, 0, MEM_RELEASE);
        log_write(RML_LOG_WARN, "MenuStatus", "Status line refused: the validated LEA changed or could not be safely repointed.");
        return 0;
    }

    log_hex(RML_LOG_INFO, "MenuStatus", "Version-format LEA patched at: ", (uintptr_t)ref.instruction);
    log_hex(RML_LOG_INFO, "MenuStatus", "RML format string stored at: ", (uintptr_t)storage);
    if (g_plan.flags & PLAN_DEEP_TEST) {
        log_write(RML_LOG_INFO, "MenuStatus", bootstrap_status
            ? "WRSR lower-left version line will show [Republic Mod Loader - Diagnostic mode - degraded]."
            : "WRSR lower-left version line will show [Republic Mod Loader - Diagnostic mode].");
    } else {
        log_write(RML_LOG_INFO, "MenuStatus", bootstrap_status
            ? "WRSR lower-left version line will show Republic Mod Loader DEGRADED."
            : "WRSR lower-left version line will show Republic Mod Loader ACTIVE.");
    }
    return 1;
}
