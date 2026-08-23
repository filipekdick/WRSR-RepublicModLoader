#include "runtime_internal.h"

SymbolRecord g_resource_get_symbol = {
    "wrsr.resource_get",
    "ResourceGet",
    0, 0, RML_SYMBOL_UNRESOLVED,
    "ASCII anchor + RIP-relative XREF + x64 .pdata boundary",
    "unique error string + containing runtime function + known-build extent",
    0, 0, {}, 0, 0, 0,
    "EXTREME"
};

SymbolCacheRuntimeStats g_symbol_cache_stats = {0, 0, 0, 0, 0, 0, "NOT REQUESTED"};

struct PeView {
    BYTE* base;
    uint32_t image_size;
    BYTE* text;
    uint32_t text_rva;
    uint32_t text_size;
    BYTE* sections;
    uint16_t section_count;
    BYTE* runtime_functions;
    uint32_t runtime_count;
};

static uint16_t u16(const BYTE* p) {
    return (uint16_t)p[0] | ((uint16_t)p[1] << 8);
}
static uint32_t u32(const BYTE* p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) |
           ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}
static int32_t i32(const BYTE* p) { return (int32_t)u32(p); }

static int section_name_eq(const BYTE* section, const char* name) {
    size_t n = rml_strlen(name);
    if (!n || n > 8) return 0;
    for (size_t i = 0; i < n; ++i)
        if ((char)section[i] != name[i]) return 0;
    return n == 8 || section[n] == 0;
}

static int pe_open(PeView* out) {
    rml_memset(out, 0, sizeof(*out));
    BYTE* base = (BYTE*)GetModuleHandleW(0);
    if (!base || u16(base) != 0x5A4D) return 0;
    uint32_t lfanew = u32(base + 0x3C);
    if (lfanew > 0x100000 || u32(base + lfanew) != 0x00004550) return 0;
    BYTE* file = base + lfanew + 4;
    if (u16(file) != 0x8664) return 0;
    uint16_t sections = u16(file + 2);
    uint16_t opt_size = u16(file + 16);
    if (!sections || sections > 96) return 0;
    BYTE* opt = file + 20;
    if (u16(opt) != 0x20B || opt_size < 112 + 16 * 8) return 0;
    uint32_t image_size = u32(opt + 56);
    uint32_t headers_size = u32(opt + 60);
    if (image_size < 0x10000u || headers_size > image_size) return 0;
    uint64_t section_table_end = (uint64_t)lfanew + 4u + 20u + opt_size +
                                 (uint64_t)sections * 40u;
    if (section_table_end > headers_size) return 0;
    BYTE* section_table = opt + opt_size;

    out->base = base;
    out->image_size = image_size;
    out->sections = section_table;
    out->section_count = sections;

    for (uint16_t i = 0; i < sections; ++i) {
        BYTE* s = section_table + i * 40;
        uint32_t va = u32(s + 12);
        uint32_t vs = u32(s + 8);
        uint32_t raw = u32(s + 16);
        uint32_t span = vs ? vs : raw;
        if (!span || va >= image_size || span > image_size - va) continue;
        if (section_name_eq(s, ".text")) {
            uint32_t characteristics = u32(s + 36);
            if ((characteristics & 0x60000000u) != 0x60000000u) continue;
            out->text = base + va;
            out->text_rva = va;
            out->text_size = span;
        }
    }

    uint32_t exception_rva = u32(opt + 112 + 3 * 8);
    uint32_t exception_size = u32(opt + 112 + 3 * 8 + 4);
    if (exception_rva && exception_size >= 12 && exception_rva < image_size &&
        exception_size <= image_size - exception_rva) {
        out->runtime_functions = base + exception_rva;
        out->runtime_count = exception_size / 12;
    }
    return out->text && out->runtime_functions && out->runtime_count;
}

static int runtime_function_for_rva(const PeView* pe, uint32_t rva,
                                    uint32_t* begin, uint32_t* end) {
    uint32_t lo = 0, hi = pe->runtime_count;
    while (lo < hi) {
        uint32_t mid = lo + (hi - lo) / 2;
        const BYTE* rf = pe->runtime_functions + mid * 12;
        uint32_t b = u32(rf);
        if (b <= rva) lo = mid + 1;
        else hi = mid;
    }
    if (!lo) return 0;
    const BYTE* rf = pe->runtime_functions + (lo - 1) * 12;
    uint32_t b = u32(rf);
    uint32_t e = u32(rf + 4);
    if (rva < b || rva >= e || b >= pe->image_size || e > pe->image_size) return 0;
    *begin = b;
    *end = e;
    return 1;
}

static int known_build_expected_extent(const char* sha, uint32_t* out_rva,
                                       uint32_t* out_next_rva) {
    static const char* hash_1117 = "ec05bb6257da31cfcaec639c8462683ee7bcf26158e0751a29a2d48025169522";
    static const char* hash_1119 = "296644a9f207d609031fc2ae73fed2dcb34619a1d55a35d1c7b51965ce6841b8";
    if (rml_strcmp(sha, hash_1117) == 0) {
        *out_rva = 0x002AA7C0u; *out_next_rva = 0x002AAAA0u; return 1;
    }
    if (rml_strcmp(sha, hash_1119) == 0) {
        *out_rva = 0x002AA830u; *out_next_rva = 0x002AAB10u; return 1;
    }
    return 0;
}

static uintptr_t find_anchor(const PeView* pe, const char* anchor, uint32_t* count) {
    const size_t n = rml_strlen(anchor) + 1;
    uintptr_t found = 0;
    *count = 0;
    for (uint16_t i = 0; i < pe->section_count; ++i) {
        BYTE* s = pe->sections + i * 40;
        uint32_t va = u32(s + 12);
        uint32_t vs = u32(s + 8);
        uint32_t raw = u32(s + 16);
        uint32_t span = vs ? vs : raw;
        uint32_t characteristics = u32(s + 36);
        if (!(characteristics & 0x40000000u)) continue;
        if (va >= pe->image_size || span > pe->image_size - va || span < n) continue;
        BYTE* begin = pe->base + va;
        BYTE* end = begin + span - n;
        for (BYTE* p = begin; p <= end; ++p) {
            if (rml_memeq(p, anchor, n)) {
                found = (uintptr_t)p;
                ++*count;
            }
        }
    }
    return found;
}

static int is_simple_prefix(BYTE b) {
    return b == 0x66 || b == 0x67 || b == 0x2E || b == 0x36 || b == 0x3E ||
           b == 0x26 || b == 0x64 || b == 0x65 || b == 0xF2 || b == 0xF3;
}

static uint32_t find_anchor_xrefs(const PeView* pe, uintptr_t anchor,
                                  uint32_t* unique_begin, uint32_t* unique_end,
                                  uint32_t* distinct_functions) {
    uint32_t xrefs = 0;
    uint32_t first_begin = 0, first_end = 0;
    uint32_t function_count = 0;
    BYTE* end = pe->text + pe->text_size;
    for (BYTE* p = pe->text; p + 8 < end; ++p) {
        BYTE* q = p;
        int prefix_count = 0;
        while (q < end && is_simple_prefix(*q) && prefix_count++ < 4) ++q;
        if (q < end && *q >= 0x40 && *q <= 0x4F) ++q;
        if (q + 6 > end) continue;
        BYTE opcode = q[0];
        BYTE modrm = q[1];
        if ((opcode != 0x8D && opcode != 0x8B) || (modrm & 0xC7) != 0x05) continue;
        BYTE* instruction_end = q + 6;
        uintptr_t target = (uintptr_t)instruction_end + (intptr_t)i32(q + 2);
        if (target != anchor) continue;
        uint32_t rva = (uint32_t)((uintptr_t)p - (uintptr_t)pe->base);
        uint32_t b = 0, e = 0;
        if (!runtime_function_for_rva(pe, rva, &b, &e)) continue;
        ++xrefs;
        if (!first_begin) {
            first_begin = b;
            first_end = e;
            function_count = 1;
        } else if (b != first_begin) {
            function_count = 2;
        }
    }
    *unique_begin = first_begin;
    *unique_end = first_end;
    *distinct_functions = function_count;
    return xrefs;
}

static void capture_fingerprint(SymbolRecord* symbol) {
    symbol->entry_fingerprint_length = 0;
    if (!symbol->address) return;
    symbol->entry_fingerprint_length = 16;
    rml_memcpy(symbol->entry_fingerprint, (const void*)symbol->address, 16);
}

static int validate_cached_resource_get(const PeView* pe, PlanCachedSymbol* cached) {
    if (!cached || cached->rva < pe->text_rva || cached->rva >= pe->text_rva + pe->text_size)
        return 0;
    if (cached->fingerprint_length < 8 || cached->fingerprint_length > RML_MAX_FINGERPRINT ||
        cached->rva > pe->image_size - cached->fingerprint_length)
        return 0;
    BYTE* address = pe->base + cached->rva;
    if (!rml_memeq(address, cached->fingerprint, cached->fingerprint_length))
        return 0;
    uint32_t begin = 0, end = 0;
    if (!runtime_function_for_rva(pe, cached->rva, &begin, &end) || begin != cached->rva || end <= begin)
        return 0;
    uint32_t function_size = end - begin;
    if (cached->function_size && function_size != cached->function_size)
        return 0;

    g_resource_get_symbol.address = (uintptr_t)address;
    g_resource_get_symbol.rva = cached->rva;
    g_resource_get_symbol.function_size = function_size;
    g_resource_get_symbol.confidence = cached->confidence >= 2u
        ? RML_SYMBOL_CONFIRMED : RML_SYMBOL_PROBABLE;
    g_resource_get_symbol.resolver = "Symbol Cache RVA + entry fingerprint + x64 .pdata boundary";
    g_resource_get_symbol.from_symbol_cache = 1;
    capture_fingerprint(&g_resource_get_symbol);
    return 1;
}


struct RuntimeSemanticMeta {
    const char* id; const char* display; const char* temperature; const char* capability; const char* evidence;
};

static const RuntimeSemanticMeta* semantic_meta(const char* id) {
    static const RuntimeSemanticMeta metas[] = {
        {"wrsr.construction.tool_frame", "Construction / Tool Interaction Frame", "HOT", "symbol-only", "decompile-backed semantic identity across known WRSR builds"},
        {"wrsr.building.assignment_target_select", "Building Assignment Target Selection", "WARM", "symbol-only", "decompile-backed semantic identity across known WRSR builds"},
        {"wrsr.vehicle.compatibility_check", "Vehicle Compatibility Check", "WARM", "symbol-only", "decompile-backed semantic identity across known WRSR builds"},
        {"wrsr.vehicle.task_advance", "Vehicle Task Advance", "WARM", "symbol-only", "decompile-backed semantic identity across known WRSR builds"},
        {"wrsr.infrastructure.route_validation", "Infrastructure Route Validation", "WARM", "symbol-only", "decompile-backed semantic identity across known WRSR builds"},
        {"wrsr.gui.tool_resources_init", "GUI Tool Resource Initialisation", "COLD", "symbol-only", "decompile-backed semantic identity across known WRSR builds"},
    };
    for (uint32_t i = 0; i < sizeof(metas)/sizeof(metas[0]); ++i)
        if (rml_strcmp(id, metas[i].id) == 0) return &metas[i];
    return 0;
}

static void restore_additional_cached_symbols(const PeView* pe) {
    if (!pe || !(g_plan.flags & PLAN_USE_SYMBOL_CACHE)) return;
    uint32_t semantic_restored = 0, observed_restored = 0, rejected = 0;
    for (uint32_t i = 0; i < g_plan.cached_symbol_count; ++i) {
        PlanCachedSymbol* c = &g_plan.cached_symbols[i];
        if (!c->id || rml_strcmp(c->id, g_resource_get_symbol.id) == 0) continue;
        if (c->rva >= pe->image_size || !c->fingerprint_length || c->fingerprint_length > RML_MAX_FINGERPRINT ||
            c->rva > pe->image_size - c->fingerprint_length) { ++rejected; continue; }
        BYTE* address = pe->base + c->rva;
        if (!rml_memeq(address, c->fingerprint, c->fingerprint_length)) { ++rejected; continue; }

        const RuntimeSemanticMeta* meta = semantic_meta(c->id);
        uint32_t function_size = 0;
        uint32_t begin = 0, end = 0;
        if (runtime_function_for_rva(pe, c->rva, &begin, &end) && begin == c->rva && end > begin)
            function_size = end - begin;
        if (c->function_size && function_size && c->function_size != function_size) { ++rejected; continue; }

        SymbolRecord* restored = symbol_registry_restore_cached(
            c->id, address, c->rva, function_size ? function_size : c->function_size, c->confidence,
            c->fingerprint, c->fingerprint_length > 16u ? 16u : c->fingerprint_length,
            meta ? meta->display : c->id,
            meta ? "Semantic Symbol Cache RVA + exact entry fingerprint + optional .pdata entry validation"
                 : "Symbol Cache RVA + exact entry fingerprint (observed target)",
            meta ? meta->evidence : "runtime-observed plugin interaction on this exact executable fingerprint",
            meta ? meta->temperature : "UNKNOWN", meta ? 0u : 1u);
        if (!restored) continue;
        if (meta) ++semantic_restored; else ++observed_restored;
    }
    g_symbol_cache_stats.cache_validated += semantic_restored + observed_restored;
    g_symbol_cache_stats.cache_rejected += rejected;
    if (semantic_restored) log_u32(RML_LOG_INFO, "SymbolCache", "Additional semantic Symbol Cache records restored: ", semantic_restored);
    if (observed_restored) log_u32(RML_LOG_INFO, "SymbolCache", "Additional observed target aliases restored: ", observed_restored);
    if (rejected) log_u32(RML_LOG_WARN, "SymbolCache", "Additional cached symbol records rejected by runtime validation: ", rejected);
}


int resolve_symbols(void) {
    ULONGLONG total_start = GetTickCount64();
    PeView pe = {};
    if (!pe_open(&pe)) {
        g_resource_get_symbol.confidence = RML_SYMBOL_INCOMPATIBLE;
        log_write(RML_LOG_ERROR, "Resolver", "SOVIET64.exe is not a valid x64 PE image with exception metadata.");
        return 0;
    }

    log_hex(RML_LOG_INFO, "Resolver", "Game image base: ", (uintptr_t)pe.base);
    log_u32(RML_LOG_INFO, "Resolver", ".text bytes: ", pe.text_size);
    log_u32(RML_LOG_INFO, "Resolver", "x64 runtime-function records: ", pe.runtime_count);

    g_symbol_cache_stats.cache_supplied = g_plan.cached_symbol_count;
    PlanCachedSymbol* cached = (g_plan.flags & PLAN_USE_SYMBOL_CACHE)
        ? plan_find_cached_symbol(g_resource_get_symbol.id) : 0;
    if (cached) {
        ULONGLONG cache_start = GetTickCount64();
        if (validate_cached_resource_get(&pe, cached)) {
            g_symbol_cache_stats.cache_validated = 1;
            g_symbol_cache_stats.validation_ms = (uint32_t)(GetTickCount64() - cache_start);
            g_symbol_cache_stats.state = "HIT";
            log_write(RML_LOG_INFO, "SymbolCache", "HIT: wrsr.resource_get restored from the exact-executable index.");
            log_write(RML_LOG_INFO, "SymbolCache", "Cached RVA, entry fingerprint and .pdata boundary validated; full scan skipped.");
            log_hex(RML_LOG_INFO, "Resolver", "ResourceGet cached address: ", g_resource_get_symbol.address);
            log_u32(RML_LOG_INFO, "Resolver", "ResourceGet cached RVA: ", g_resource_get_symbol.rva);
            restore_additional_cached_symbols(&pe);
            return 1;
        }
        g_symbol_cache_stats.cache_rejected = 1;
        g_symbol_cache_stats.validation_ms = (uint32_t)(GetTickCount64() - cache_start);
        g_symbol_cache_stats.state = "REJECTED - FULL RESOLUTION";
        log_write(RML_LOG_WARN, "SymbolCache", "Cached ResourceGet record failed validation; falling back to full evidence-based resolution.");
    } else {
        g_symbol_cache_stats.state = (g_plan.flags & PLAN_USE_SYMBOL_CACHE) ? "MISS" : "NOT SUPPLIED";
        log_write(RML_LOG_INFO, "SymbolCache", (g_plan.flags & PLAN_USE_SYMBOL_CACHE)
            ? "MISS: no usable ResourceGet record was supplied for this executable."
            : "No Symbol Cache record supplied; full resolution required.");
    }

    ULONGLONG full_start = GetTickCount64();
    const char* anchor_text = "ResourceGet - not found %s";
    uint32_t anchor_count = 0;
    uintptr_t anchor = find_anchor(&pe, anchor_text, &anchor_count);
    log_u32(RML_LOG_INFO, "Resolver", "ResourceGet anchor matches: ", anchor_count);
    if (anchor_count != 1) {
        g_resource_get_symbol.confidence = anchor_count ? RML_SYMBOL_AMBIGUOUS : RML_SYMBOL_UNRESOLVED;
        log_write(RML_LOG_ERROR, "Resolver", anchor_count
            ? "ResourceGet anchor is not unique; automatic resolution refused."
            : "ResourceGet anchor was not found.");
        return 0;
    }
    log_hex(RML_LOG_INFO, "Resolver", "ResourceGet anchor address: ", anchor);

    uint32_t begin = 0, end = 0, distinct = 0;
    uint32_t xrefs = find_anchor_xrefs(&pe, anchor, &begin, &end, &distinct);
    g_resource_get_symbol.xref_count = xrefs;
    log_u32(RML_LOG_INFO, "Resolver", "Validated RIP-relative anchor XREFs: ", xrefs);
    if (!xrefs || distinct != 1 || !begin || end <= begin) {
        g_resource_get_symbol.confidence = xrefs ? RML_SYMBOL_AMBIGUOUS : RML_SYMBOL_UNRESOLVED;
        log_write(RML_LOG_ERROR, "Resolver", xrefs
            ? "Anchor XREFs resolve to multiple functions; automatic hooking refused."
            : "No supported RIP-relative anchor XREF was found.");
        return 0;
    }

    uint32_t function_size = end - begin;
    g_resource_get_symbol.address = (uintptr_t)pe.base + begin;
    g_resource_get_symbol.rva = begin;
    g_resource_get_symbol.function_size = function_size;
    g_resource_get_symbol.from_symbol_cache = 0;
    log_hex(RML_LOG_INFO, "Resolver", "ResourceGet candidate: ", g_resource_get_symbol.address);
    log_u32(RML_LOG_INFO, "Resolver", "ResourceGet candidate RVA: ", begin);
    log_u32(RML_LOG_INFO, "Resolver", "ResourceGet candidate size: ", function_size);

    if (function_size < 0x180 || function_size > 0x600) {
        g_resource_get_symbol.confidence = RML_SYMBOL_INCOMPATIBLE;
        log_write(RML_LOG_ERROR, "Resolver", "Candidate function size is outside the validated structural envelope.");
        return 0;
    }

    uint32_t expected_rva = 0, expected_next_rva = 0;
    if (known_build_expected_extent(g_plan.game_sha256, &expected_rva, &expected_next_rva)) {
        uint32_t expected_extent = expected_next_rva - expected_rva;
        uint32_t tail_gap = (end <= expected_next_rva) ? (expected_next_rva - end) : 0xFFFFFFFFu;
        if (begin != expected_rva || function_size > expected_extent ||
            tail_gap > 16u || function_size < expected_extent - 16u) {
            g_resource_get_symbol.confidence = RML_SYMBOL_INCOMPATIBLE;
            log_write(RML_LOG_ERROR, "Resolver", "Known-build cross-check failed; candidate entry/extent differs materially from the supplied decompilation.");
            return 0;
        }
        g_resource_get_symbol.confidence = RML_SYMBOL_CONFIRMED;
        if (function_size != expected_extent)
            log_write(RML_LOG_INFO, "Resolver", "Known-build unwind span ends in validated alignment padding before the next decompiled function boundary.");
        log_write(RML_LOG_INFO, "Resolver", "ResourceGet CONFIRMED by structural evidence and known-build cross-check.");
    } else {
        g_resource_get_symbol.confidence = RML_SYMBOL_PROBABLE;
        log_write(RML_LOG_WARN, "Resolver", "ResourceGet is structurally PROBABLE on this untested executable fingerprint.");
    }

    capture_fingerprint(&g_resource_get_symbol);
    g_symbol_cache_stats.full_resolutions = 1;
    g_symbol_cache_stats.full_resolution_ms = (uint32_t)(GetTickCount64() - full_start);
    if (g_symbol_cache_stats.cache_rejected)
        g_symbol_cache_stats.state = "REBUILT AFTER REJECTION";
    else
        g_symbol_cache_stats.state = "BUILT";
    log_u32(RML_LOG_INFO, "SymbolCache", "Full resolution time (ms): ", g_symbol_cache_stats.full_resolution_ms);
    restore_additional_cached_symbols(&pe);
    (void)total_start;
    return 1;
}
