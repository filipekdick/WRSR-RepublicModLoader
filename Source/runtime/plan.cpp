#include "runtime_internal.h"

RuntimePlan g_plan = {};

static uint32_t read_u32(const BYTE* data, size_t size, size_t* pos, int* ok) {
    if (!*ok || *pos + 4 > size) { *ok = 0; return 0; }
    uint32_t v = (uint32_t)data[*pos] |
                 ((uint32_t)data[*pos + 1] << 8) |
                 ((uint32_t)data[*pos + 2] << 16) |
                 ((uint32_t)data[*pos + 3] << 24);
    *pos += 4;
    return v;
}

static int32_t read_i32(const BYTE* data, size_t size, size_t* pos, int* ok) {
    return (int32_t)read_u32(data, size, pos, ok);
}

static uint64_t read_u64(const BYTE* data, size_t size, size_t* pos, int* ok) {
    if (!*ok || *pos + 8 > size) { *ok = 0; return 0; }
    uint64_t v = 0;
    for (int i = 0; i < 8; ++i) v |= ((uint64_t)data[*pos + i]) << (i * 8);
    *pos += 8;
    return v;
}

static char* read_utf8(const BYTE* data, size_t size, size_t* pos, uint32_t bytes,
                       uint32_t maximum, int* ok) {
    if (!*ok || !bytes || bytes > maximum || *pos + bytes > size) { *ok = 0; return 0; }
    char* out = (char*)rml_alloc((size_t)bytes + 1u);
    if (!out) { *ok = 0; return 0; }
    rml_memcpy(out, data + *pos, bytes);
    out[bytes] = 0;
    *pos += bytes;
    return out;
}

static wchar_t* read_utf16(const BYTE* data, size_t size, size_t* pos,
                           uint32_t chars, int* ok) {
    size_t bytes = (size_t)chars * 2u;
    if (!*ok || !chars || chars > RML_MAX_PATH * 4u || *pos + bytes > size) {
        *ok = 0;
        return 0;
    }
    wchar_t* out = (wchar_t*)rml_alloc(((size_t)chars + 1u) * sizeof(wchar_t));
    if (!out) { *ok = 0; return 0; }
    for (uint32_t i = 0; i < chars; ++i) {
        out[i] = (wchar_t)((uint16_t)data[*pos + i * 2u] |
                           ((uint16_t)data[*pos + i * 2u + 1u] << 8));
    }
    out[chars] = 0;
    *pos += bytes;
    return out;
}

static int read_file_all(const wchar_t* path, BYTE** out_data, size_t* out_size) {
    *out_data = 0;
    *out_size = 0;
    HANDLE f = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ, 0, OPEN_EXISTING,
                           FILE_ATTRIBUTE_NORMAL, 0);
    if (f == INVALID_HANDLE_VALUE) return 0;
    LARGE_INTEGER li = {};
    if (!GetFileSizeEx(f, &li) || li.QuadPart <= 0 || li.QuadPart > 16 * 1024 * 1024) {
        CloseHandle(f);
        return 0;
    }
    BYTE* data = (BYTE*)rml_alloc((size_t)li.QuadPart, 0);
    if (!data) { CloseHandle(f); return 0; }
    DWORD read = 0;
    BOOL ok = ReadFile(f, data, (DWORD)li.QuadPart, &read, 0);
    CloseHandle(f);
    if (!ok || read != (DWORD)li.QuadPart) {
        rml_free(data);
        return 0;
    }
    *out_data = data;
    *out_size = (size_t)li.QuadPart;
    return 1;
}

const char* plan_profile_name(void) {
    return g_plan.profile == PROFILE_DEEP_TEST ? "Detailed diagnostic launch" : "Normal / recommended";
}

const char* plan_profile_short(void) {
    return g_plan.profile == PROFILE_DEEP_TEST ? "DIAGNOSTIC" : "NORMAL";
}


PlanPlugin* plan_find_plugin(const char* id) {
    if (!id) return 0;
    for (uint32_t i = 0; i < g_plan.plugin_count; ++i) {
        if (g_plan.plugins[i].id && rml_strcmp(g_plan.plugins[i].id, id) == 0) return &g_plan.plugins[i];
    }
    return 0;
}

PlanCachedSymbol* plan_find_cached_symbol(const char* id) {
    if (!id) return 0;
    for (uint32_t i = 0; i < g_plan.cached_symbol_count; ++i) {
        if (rml_strcmp(g_plan.cached_symbols[i].id, id) == 0)
            return &g_plan.cached_symbols[i];
    }
    return 0;
}

int plan_workshop_enabled(uint64_t item_id) {
    uint32_t lo = 0, hi = g_plan.enabled_workshop_count;
    while (lo < hi) {
        uint32_t mid = lo + (hi - lo) / 2u;
        uint64_t value = g_plan.enabled_workshop_ids[mid];
        if (value == item_id) return 1;
        if (value < item_id) lo = mid + 1u; else hi = mid;
    }
    return 0;
}

int plan_load(void) {
    wchar_t path[RML_MAX_PATH];
    if (!rml_join_path(path, RML_MAX_PATH, g_root_path, L"rml\\state\\runtime_plan.bin")) {
        log_write(RML_LOG_ERROR, "Plan", "Runtime-plan path is too long.");
        return 0;
    }

    BYTE* data = 0;
    size_t size = 0;
    if (!read_file_all(path, &data, &size)) {
        log_write(RML_LOG_ERROR, "Plan", "Could not read rml\\state\\runtime_plan.bin.");
        return 0;
    }

    int ok = 1;
    size_t p = 0;
    static const char magic[8] = {'R','M','L','P','L','A','N','7'};
    if (size < 197 || !rml_memeq(data, magic, 8)) ok = 0;
    p = 8;
    uint32_t version = read_u32(data, size, &p, &ok);
    g_plan.flags = read_u32(data, size, &p, &ok);
    g_plan.profile = read_u32(data, size, &p, &ok);
    uint32_t plugin_count = read_u32(data, size, &p, &ok);
    uint32_t cached_count = read_u32(data, size, &p, &ok);
    uint32_t enabled_workshop_count = read_u32(data, size, &p, &ok);
        if (!ok || version != 7 || (g_plan.profile != PROFILE_NORMAL && g_plan.profile != PROFILE_DEEP_TEST) || plugin_count > RML_MAX_PLUGINS ||
        cached_count > RML_MAX_CACHED_SYMBOLS || enabled_workshop_count > RML_MAX_ENABLED_WORKSHOP ||
        p + 32 + 65 + RML_SESSION_ID_CAP > size) {
        ok = 0;
    }
    if (ok) {
        rml_memcpy(g_plan.game_version, data + p, 32);
        g_plan.game_version[31] = 0;
        p += 32;
        rml_memcpy(g_plan.game_sha256, data + p, 65);
        g_plan.game_sha256[64] = 0;
        p += 65;
        rml_memcpy(g_plan.session_id, data + p, RML_SESSION_ID_CAP);
        g_plan.session_id[RML_SESSION_ID_CAP - 1] = 0;
        p += RML_SESSION_ID_CAP;
    }

    for (uint32_t i = 0; ok && i < cached_count; ++i) {
        PlanCachedSymbol* item = &g_plan.cached_symbols[i];
        uint32_t id_len = read_u32(data, size, &p, &ok);
        item->rva = read_u32(data, size, &p, &ok);
        item->function_size = read_u32(data, size, &p, &ok);
        item->confidence = read_u32(data, size, &p, &ok);
        item->fingerprint_length = read_u32(data, size, &p, &ok);
        if (item->fingerprint_length == 0 || item->fingerprint_length > RML_MAX_FINGERPRINT)
            ok = 0;
        item->id = read_utf8(data, size, &p, id_len, 255, &ok);
        if (ok && p + item->fingerprint_length <= size) {
            rml_memcpy(item->fingerprint, data + p, item->fingerprint_length);
            p += item->fingerprint_length;
        } else {
            ok = 0;
        }
    }

    for (uint32_t i = 0; ok && i < plugin_count; ++i) {
        PlanPlugin* item = &g_plan.plugins[i];
        item->flags = read_u32(data, size, &p, &ok);
        item->priority = read_i32(data, size, &p, &ok);
        uint32_t id_len = read_u32(data, size, &p, &ok);
        uint32_t ver_len = read_u32(data, size, &p, &ok);
        uint32_t hash_len = read_u32(data, size, &p, &ok);
        uint32_t path_chars = read_u32(data, size, &p, &ok);
        uint32_t required = read_u32(data, size, &p, &ok);
        if (required > RML_MAX_REQUIRED_SYMBOLS || hash_len != 64u) ok = 0;
        item->id = read_utf8(data, size, &p, id_len, 127, &ok);
        item->version = read_utf8(data, size, &p, ver_len, 31, &ok);
        if (ok && p + hash_len <= size) {
            rml_memcpy(item->dll_sha256, data + p, hash_len);
            item->dll_sha256[64] = 0;
            p += hash_len;
        } else {
            ok = 0;
        }
        item->path = read_utf16(data, size, &p, path_chars, &ok);
        item->required_count = required;
        if (required) {
            item->required_symbols = (char**)rml_alloc(sizeof(char*) * required);
            if (!item->required_symbols) ok = 0;
        }
        for (uint32_t r = 0; ok && r < required; ++r) {
            uint32_t n = read_u32(data, size, &p, &ok);
            item->required_symbols[r] = read_utf8(data, size, &p, n, 255, &ok);
        }
    }

    for (uint32_t i = 0; ok && i < enabled_workshop_count; ++i) {
        uint64_t id = read_u64(data, size, &p, &ok);
        if (!id || (i > 0 && id <= g_plan.enabled_workshop_ids[i - 1])) ok = 0;
        g_plan.enabled_workshop_ids[i] = id;
    }


    if (ok && p != size) {
        // Trailing data usually means launcher/runtime schema drift. Refuse it
        // rather than silently interpreting a partially compatible plan.
        ok = 0;
    }
    if (ok) {
        g_plan.plugin_count = plugin_count;
        g_plan.cached_symbol_count = cached_count;
        g_plan.enabled_workshop_count = enabled_workshop_count;
    }
    rml_free(data);

    if (!ok) {
        plan_free();
        log_write(RML_LOG_ERROR, "Plan", "Runtime plan is malformed or uses an unsupported schema.");
        return 0;
    }

    log_write(RML_LOG_INFO, "LaunchProfile", plan_profile_name());
    log_write(RML_LOG_INFO, "LaunchProfile", plan_profile_short());
    log_u32(RML_LOG_INFO, "Plan", "Plugins scheduled: ", g_plan.plugin_count);
    log_u32(RML_LOG_INFO, "SymbolCache", "Cached symbol records supplied: ", g_plan.cached_symbol_count);
    log_u32(RML_LOG_INFO, "WorkshopFilter", "Workshop items explicitly enabled by launcher plan: ", g_plan.enabled_workshop_count);
    log_write(RML_LOG_INFO, "Plan", (g_plan.flags & PLAN_ALLOW_PROBABLE)
        ? "Probable symbol use is enabled."
        : "Only confirmed symbols may be hooked.");
    return 1;
}

void plan_free(void) {
    for (uint32_t i = 0; i < RML_MAX_CACHED_SYMBOLS; ++i)
        rml_free(g_plan.cached_symbols[i].id);
    for (uint32_t i = 0; i < RML_MAX_PLUGINS; ++i) {
        PlanPlugin* item = &g_plan.plugins[i];
        if (item->required_symbols) {
            for (uint32_t r = 0; r < item->required_count; ++r)
                rml_free(item->required_symbols[r]);
            rml_free(item->required_symbols);
        }
        rml_free(item->path);
        rml_free(item->id);
        rml_free(item->version);
    }
    rml_memset(&g_plan, 0, sizeof(g_plan));
}
