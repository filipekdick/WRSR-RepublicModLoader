#include "runtime_internal.h"

SymbolRecord* g_symbol_registry[RML_MAX_SYMBOLS] = {};
uint32_t g_symbol_count = 0;
ServiceRecord g_services[RML_MAX_SERVICES] = {};
uint32_t g_service_count = 0;
DeepTestStats g_deep_test_stats = {};

static SymbolRecord g_observed_symbols[RML_MAX_OBSERVED_SYMBOLS] = {};
static SymbolRecord g_cached_symbols[RML_MAX_CACHED_SYMBOLS] = {};
static uint32_t g_cached_symbol_count = 0;
static char g_observed_ids[RML_MAX_OBSERVED_SYMBOLS][64] = {};
static char g_observed_names[RML_MAX_OBSERVED_SYMBOLS][128] = {};
static char g_observed_evidence[RML_MAX_OBSERVED_SYMBOLS][128] = {};
static uint32_t g_observed_count = 0;

static uint16_t r_u16(const BYTE* p) { return (uint16_t)p[0] | ((uint16_t)p[1] << 8); }
static uint32_t r_u32(const BYTE* p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}

static void hex8(uint32_t value, char out[9]) {
    static const char* h = "0123456789ABCDEF";
    for (int i = 7; i >= 0; --i) { out[i] = h[value & 0xFu]; value >>= 4; }
    out[8] = 0;
}

static int main_image_info(BYTE** base_out, uint32_t* size_out, BYTE** runtime_out, uint32_t* runtime_count_out,
                           BYTE** text_out, uint32_t* text_size_out) {
    BYTE* base = (BYTE*)GetModuleHandleW(0);
    if (!base || r_u16(base) != 0x5A4D) return 0;
    uint32_t lf = r_u32(base + 0x3C);
    if (lf > 0x100000u || r_u32(base + lf) != 0x00004550u) return 0;
    BYTE* file = base + lf + 4;
    if (r_u16(file) != 0x8664) return 0;
    uint16_t sections = r_u16(file + 2), opt_size = r_u16(file + 16);
    BYTE* opt = file + 20;
    if (!sections || sections > 96 || r_u16(opt) != 0x20B || opt_size < 240) return 0;
    uint32_t image_size = r_u32(opt + 56);
    uint32_t runtime_rva = r_u32(opt + 112 + 3 * 8);
    uint32_t runtime_size = r_u32(opt + 112 + 3 * 8 + 4);
    BYTE* text = 0; uint32_t text_size = 0;
    BYTE* table = opt + opt_size;
    for (uint16_t i = 0; i < sections; ++i) {
        BYTE* s = table + i * 40;
        if (s[0] == '.' && s[1] == 't' && s[2] == 'e' && s[3] == 'x' && s[4] == 't' && s[5] == 0) {
            uint32_t va = r_u32(s + 12), vs = r_u32(s + 8), raw = r_u32(s + 16);
            uint32_t span = vs ? vs : raw;
            if (va < image_size && span && span <= image_size - va) { text = base + va; text_size = span; }
        }
    }
    if (!runtime_rva || runtime_size < 12 || runtime_rva >= image_size || runtime_size > image_size - runtime_rva) return 0;
    if (base_out) *base_out = base;
    if (size_out) *size_out = image_size;
    if (runtime_out) *runtime_out = base + runtime_rva;
    if (runtime_count_out) *runtime_count_out = runtime_size / 12;
    if (text_out) *text_out = text;
    if (text_size_out) *text_size_out = text_size;
    return 1;
}

static int runtime_extent_for_rva(BYTE* runtime, uint32_t count, uint32_t rva, uint32_t* begin_out, uint32_t* end_out) {
    uint32_t lo = 0, hi = count;
    while (lo < hi) {
        uint32_t mid = lo + (hi - lo) / 2u;
        uint32_t begin = r_u32(runtime + mid * 12u);
        if (begin <= rva) lo = mid + 1u; else hi = mid;
    }
    if (!lo) return 0;
    BYTE* rf = runtime + (lo - 1u) * 12u;
    uint32_t begin = r_u32(rf), end = r_u32(rf + 4);
    if (begin >= end || rva < begin || rva >= end) return 0;
    if (begin_out) *begin_out = begin;
    if (end_out) *end_out = end;
    return 1;
}

static void registry_add(SymbolRecord* symbol) {
    if (!symbol || !symbol->id || g_symbol_count >= RML_MAX_SYMBOLS) return;
    for (uint32_t i = 0; i < g_symbol_count; ++i)
        if (g_symbol_registry[i] == symbol || rml_strcmp(g_symbol_registry[i]->id, symbol->id) == 0) return;
    g_symbol_registry[g_symbol_count++] = symbol;
}

void symbol_registry_init(void) {
    rml_memset(g_symbol_registry, 0, sizeof(g_symbol_registry));
    rml_memset(g_observed_symbols, 0, sizeof(g_observed_symbols));
    rml_memset(g_cached_symbols, 0, sizeof(g_cached_symbols));
    g_cached_symbol_count = 0;
    rml_memset(g_observed_ids, 0, sizeof(g_observed_ids));
    rml_memset(g_observed_names, 0, sizeof(g_observed_names));
    rml_memset(g_observed_evidence, 0, sizeof(g_observed_evidence));
    g_symbol_count = 0; g_observed_count = 0;
    registry_add(&g_resource_get_symbol);
}

SymbolRecord* symbol_registry_find(const char* id) {
    if (!id) return 0;
    for (uint32_t i = 0; i < g_symbol_count; ++i)
        if (g_symbol_registry[i] && rml_strcmp(g_symbol_registry[i]->id, id) == 0) return g_symbol_registry[i];
    return 0;
}

SymbolRecord* symbol_registry_find_by_address(void* address) {
    uintptr_t a = (uintptr_t)address;
    if (!a) return 0;
    for (uint32_t i = 0; i < g_symbol_count; ++i)
        if (g_symbol_registry[i] && g_symbol_registry[i]->address == a) return g_symbol_registry[i];
    return 0;
}

SymbolRecord* symbol_registry_restore_cached(const char* id, void* address, uint32_t rva, uint32_t function_size,
                                             uint32_t confidence, const BYTE* fingerprint, uint32_t fingerprint_length,
                                             const char* display_name, const char* resolver, const char* evidence,
                                             const char* temperature, uint32_t observed_only) {
    if (!id || !*id || !address || !fingerprint || !fingerprint_length || fingerprint_length > 16u) return 0;
    SymbolRecord* by_id = symbol_registry_find(id);
    if (by_id) return by_id;
    SymbolRecord* by_address = symbol_registry_find_by_address(address);
    if (by_address) return by_address;
    if (g_cached_symbol_count >= RML_MAX_CACHED_SYMBOLS || g_symbol_count >= RML_MAX_SYMBOLS) return 0;
    SymbolRecord* s = &g_cached_symbols[g_cached_symbol_count++];
    rml_memset(s, 0, sizeof(*s));
    s->id = id;
    s->display_name = (display_name && *display_name) ? display_name : id;
    s->address = (uintptr_t)address;
    s->rva = rva;
    s->function_size = function_size;
    s->confidence = confidence >= 5u ? RML_SYMBOL_OBSERVED : (confidence >= 2u ? RML_SYMBOL_CONFIRMED : RML_SYMBOL_PROBABLE);
    s->resolver = resolver ? resolver : "Symbol Cache RVA + exact entry fingerprint";
    s->evidence = evidence ? evidence : "launcher-supplied exact-build Symbol Cache record";
    s->from_symbol_cache = 1;
    s->observed_only = observed_only;
    s->temperature = temperature ? temperature : "UNKNOWN";
    s->entry_fingerprint_length = fingerprint_length;
    rml_memcpy(s->entry_fingerprint, fingerprint, fingerprint_length);
    registry_add(s);
    return s;
}

SymbolRecord* symbol_registry_at(uint32_t index) { return index < g_symbol_count ? g_symbol_registry[index] : 0; }
uint32_t symbol_registry_count(void) { return g_symbol_count; }

SymbolRecord* symbol_registry_observe_target(void* address, const char* label, const char* evidence) {
    if (!address) return 0;
    for (uint32_t i = 0; i < g_symbol_count; ++i)
        if (g_symbol_registry[i] && g_symbol_registry[i]->address == (uintptr_t)address) return g_symbol_registry[i];
    if (g_observed_count >= RML_MAX_OBSERVED_SYMBOLS || g_symbol_count >= RML_MAX_SYMBOLS) return 0;

    BYTE* base = 0; uint32_t image_size = 0; BYTE* runtime = 0; uint32_t runtime_count = 0;
    if (!main_image_info(&base, &image_size, &runtime, &runtime_count, 0, 0)) return 0;
    uintptr_t a = (uintptr_t)address, b = (uintptr_t)base;
    if (a < b || a >= b + image_size) return 0;
    uint32_t rva = (uint32_t)(a - b);

    uint32_t index = g_observed_count++;
    char hex[9]; hex8(rva, hex);
    rml_copy_str(g_observed_ids[index], sizeof(g_observed_ids[index]), "wrsr.observed.rva_");
    size_t p = rml_strlen(g_observed_ids[index]);
    for (int i = 0; hex[i] && p + 1 < sizeof(g_observed_ids[index]); ++i) g_observed_ids[index][p++] = (char)(hex[i] >= 'A' && hex[i] <= 'F' ? hex[i] + ('a'-'A') : hex[i]);
    g_observed_ids[index][p] = 0;
    rml_copy_str(g_observed_names[index], sizeof(g_observed_names[index]), label && *label ? label : "Observed legacy target");
    rml_copy_str(g_observed_evidence[index], sizeof(g_observed_evidence[index]), evidence && *evidence ? evidence : "runtime-observation");

    SymbolRecord* s = &g_observed_symbols[index];
    s->id = g_observed_ids[index];
    s->display_name = g_observed_names[index];
    s->address = a;
    s->rva = rva;
    s->confidence = RML_SYMBOL_OBSERVED;
    s->resolver = "Runtime interaction observation; no semantic cross-version identity assigned";
    s->evidence = g_observed_evidence[index];
    s->observed_only = 1;
    s->temperature = "UNKNOWN";
    uint32_t begin = 0, end = 0;
    if (runtime_extent_for_rva(runtime, runtime_count, rva, &begin, &end)) s->function_size = end - begin;
    s->entry_fingerprint_length = 16;
    if (rva > image_size - 16u) s->entry_fingerprint_length = image_size - rva;
    if (s->entry_fingerprint_length) rml_memcpy(s->entry_fingerprint, address, s->entry_fingerprint_length);
    registry_add(s);
    log_write(RML_LOG_INFO, "SymbolRegistry", "Learned a new observed WRSR target from plugin runtime behaviour; retained as OBSERVED until given a semantic resolver.");
    log_write(RML_LOG_INFO, "SymbolRegistry", s->id);
    return s;
}

int rml_service_provide(PluginState* owner, const char* name, uint32_t version, void* service) {
    if (!owner || !name || !*name || !version || !service) return 0;
    for (uint32_t i = 0; i < g_service_count; ++i) {
        ServiceRecord* r = &g_services[i];
        if (rml_strcmp(r->name, name) == 0 && r->version == version) return r->owner == owner && r->service == service;
    }
    if (g_service_count >= RML_MAX_SERVICES) return 0;
    ServiceRecord* r = &g_services[g_service_count++];
    rml_copy_str(r->name, sizeof(r->name), name); r->version = version; r->owner = owner; r->service = service;
    return 1;
}

void* rml_service_consume(const char* name, uint32_t version, RmlServiceInfo* out_info) {
    if (!name || !*name || !version) return 0;
    for (uint32_t i = 0; i < g_service_count; ++i) {
        ServiceRecord* r = &g_services[i];
        if (rml_strcmp(r->name, name) != 0 || r->version != version) continue;
        if (out_info && out_info->struct_size >= sizeof(RmlServiceInfo)) {
            out_info->name = r->name; out_info->version = r->version;
            out_info->provider_id = r->owner ? r->owner->id : "rml";
        }
        return r->service;
    }
    return 0;
}

void rml_service_remove_owner(PluginState* owner) {
    if (!owner) return;
    uint32_t w = 0;
    for (uint32_t i = 0; i < g_service_count; ++i) {
        if (g_services[i].owner == owner) continue;
        if (w != i) g_services[w] = g_services[i];
        ++w;
    }
    while (g_service_count > w) rml_memset(&g_services[--g_service_count], 0, sizeof(ServiceRecord));
}

uint64_t runtime_text_fingerprint(void) {
    BYTE* text = 0; uint32_t text_size = 0;
    if (!main_image_info(0, 0, 0, 0, &text, &text_size) || !text || !text_size) return 0;
    uint64_t h = 1469598103934665603ull;
    for (uint32_t i = 0; i < text_size; ++i) { h ^= text[i]; h *= 1099511628211ull; }
    return h;
}


int runtime_text_page_snapshot(DeepTextSnapshot* out) {
    if (!out) return 0; rml_memset(out,0,sizeof(*out));
    BYTE* base=0; BYTE* text=0; uint32_t text_size=0;
    if (!main_image_info(&base,0,0,0,&text,&text_size) || !base || !text || !text_size) return 0;
    uint32_t text_rva=(uint32_t)((uintptr_t)text-(uintptr_t)base);
    const uint32_t page=4096; uint32_t count=(text_size+page-1u)/page;
    if (!count || count>RML_MAX_TEXT_PAGES) return 0;
    out->page_count=count; out->page_size=page; out->text_rva=text_rva;
    for (uint32_t i=0;i<count;++i) {
        uint32_t off=i*page, n=text_size-off; if (n>page) n=page;
        uint64_t h=1469598103934665603ull;
        for (uint32_t j=0;j<n;++j) { h^=text[off+j]; h*=1099511628211ull; }
        out->hashes[i]=h;
    }
    return 1;
}

uint32_t runtime_text_page_diff(const DeepTextSnapshot* before, const DeepTextSnapshot* after, uint32_t* first_rva) {
    if (first_rva) *first_rva=0;
    if (!before || !after || !before->page_count || before->page_count!=after->page_count || before->page_size!=after->page_size || before->text_rva!=after->text_rva) return 0;
    uint32_t changed=0;
    for (uint32_t i=0;i<before->page_count;++i) if (before->hashes[i]!=after->hashes[i]) {
        if (!changed && first_rva) *first_rva=before->text_rva+i*before->page_size;
        ++changed;
    }
    return changed;
}

uint32_t runtime_text_page_log_diff(const char* component, const char* phase,
                                    const DeepTextSnapshot* before, const DeepTextSnapshot* after) {
    if (!component) component = "DeepTrace";
    if (!before || !after || !before->page_count || before->page_count != after->page_count ||
        before->page_size != after->page_size || before->text_rva != after->text_rva) {
        log_write(RML_LOG_WARN, component, "DEEP TRACE page-diff detail unavailable because snapshot geometry changed or a snapshot is invalid.");
        return 0;
    }
    uint32_t changed = 0;
    BYTE* image = (BYTE*)GetModuleHandleW(0);
    if (phase && *phase) log_write(RML_LOG_INFO, component, phase);
    for (uint32_t i = 0; i < before->page_count; ++i) {
        if (before->hashes[i] == after->hashes[i]) continue;
        ++changed;
        uint32_t rva = before->text_rva + i * before->page_size;
        log_u32(RML_LOG_INFO, component, "Changed .text page index: ", i);
        log_hex(RML_LOG_INFO, component, "Changed page RVA: ", rva);
        if (image) log_hex(RML_LOG_INFO, component, "Changed page VA: ", (uintptr_t)(image + rva));
        log_u64(RML_LOG_INFO, component, "Hash before: ", before->hashes[i]);
        log_u64(RML_LOG_INFO, component, "Hash after: ", after->hashes[i]);
        if (image) {
            MEMORY_BASIC_INFORMATION mbi = {};
            if (VirtualQuery(image + rva, &mbi, sizeof(mbi))) {
                log_u32(RML_LOG_INFO, component, "Current page protection: ", (uint32_t)mbi.Protect);
                log_u64(RML_LOG_INFO, component, "Current region size: ", (uint64_t)mbi.RegionSize);
            }
        }
    }
    log_u32(RML_LOG_INFO, component, "Changed .text pages in this window: ", changed);
    return changed;
}
