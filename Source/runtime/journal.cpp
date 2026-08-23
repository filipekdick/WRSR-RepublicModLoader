#include "runtime_internal.h"

static HANDLE g_journal_lock = INVALID_HANDLE_VALUE;

static void j_put_char(char* b, size_t cap, size_t* p, char c) {
    if (*p + 1 < cap) b[(*p)++] = c;
}
static void j_put_str(char* b, size_t cap, size_t* p, const char* s) {
    if (!s) return;
    while (*s && *p + 1 < cap) b[(*p)++] = *s++;
}
static void j_put_u32(char* b, size_t cap, size_t* p, uint32_t v) {
    char tmp[16]; int n = 0;
    do { tmp[n++] = (char)('0' + (v % 10u)); v /= 10u; } while (v && n < 16);
    while (n) j_put_char(b, cap, p, tmp[--n]);
}
static void j_put_json(char* b, size_t cap, size_t* p, const char* s) {
    j_put_char(b, cap, p, '"');
    if (s) {
        while (*s && *p + 8 < cap) {
            unsigned char c = (unsigned char)*s++;
            if (c == '"' || c == '\\') { j_put_char(b, cap, p, '\\'); j_put_char(b, cap, p, (char)c); }
            else if (c == '\n') j_put_str(b, cap, p, "\\n");
            else if (c == '\r') j_put_str(b, cap, p, "\\r");
            else if (c == '\t') j_put_str(b, cap, p, "\\t");
            else if (c >= 0x20) j_put_char(b, cap, p, (char)c);
        }
    }
    j_put_char(b, cap, p, '"');
}

static void j_timestamp(char* out, size_t cap) {
    if (!out || cap < 25) return;
    SYSTEMTIME st = {};
    GetSystemTime(&st);
    // Fixed-width UTC timestamp. Session identity and append order remain the
    // authoritative sequence if two events share the same millisecond.
    size_t p = 0;
    uint32_t values[7] = {st.wYear, st.wMonth, st.wDay, st.wHour, st.wMinute, st.wSecond, st.wMilliseconds};
    const uint32_t widths[7] = {4,2,2,2,2,2,3};
    const char separators[6] = {'-','-','T',':',':','.'};
    for (uint32_t i = 0; i < 7; ++i) {
        uint32_t div = 1;
        for (uint32_t x = 1; x < widths[i]; ++x) div *= 10;
        for (uint32_t x = 0; x < widths[i] && p + 1 < cap; ++x) {
            out[p++] = (char)('0' + ((values[i] / div) % 10u));
            if (div > 1) div /= 10;
        }
        if (i < 6 && p + 1 < cap) out[p++] = separators[i];
    }
    out[p] = 0;
}

static __declspec(noinline) int build_session_path(wchar_t* out_path, size_t cap,
                                                       const wchar_t* suffix) {
    wchar_t state[RML_MAX_PATH];
    if (!rml_join_path(state, RML_MAX_PATH, g_root_path, L"rml\\state")) return 0;
    CreateDirectoryW(state, 0);
    if (!rml_join_path(out_path, cap, state, L"sessions")) return 0;
    CreateDirectoryW(out_path, 0);
    wchar_t file[96] = {};
    size_t p = 0;
    for (size_t i = 0; g_plan.session_id[i] && p + 1 < 80; ++i) {
        char c = g_plan.session_id[i];
        if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
            (c >= '0' && c <= '9') || c == '-' || c == '_') {
            file[p++] = (wchar_t)c;
        }
    }
    if (!p || !suffix) return 0;
    for (size_t i = 0; suffix[i] && p + 1 < 96; ++i) file[p++] = suffix[i];
    file[p] = 0;
    rml_copy_wstr(state, RML_MAX_PATH, out_path);
    return rml_join_path(out_path, cap, state, file);
}

static void open_session_lock(void) {
    if (g_journal_lock != INVALID_HANDLE_VALUE || !g_plan.session_id[0]) return;
    wchar_t path[RML_MAX_PATH];
    if (!build_session_path(path, RML_MAX_PATH, L".lock")) return;
    // Share mode zero is intentional. A second launcher can see the lock file but
    // cannot open it while this WRSR process is alive. After a crash Windows
    // closes the handle automatically, allowing the next launcher to recover the
    // journal without mistaking an active session for a crashed one.
    g_journal_lock = CreateFileW(path, GENERIC_READ | GENERIC_WRITE, 0, 0,
                                 CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, 0);
    if (g_journal_lock == INVALID_HANDLE_VALUE)
        log_write(RML_LOG_WARN, "Journal", "Could not acquire the exclusive session journal lock; observations remain best-effort.");
}

static void append_event(const PlanPlugin* plugin, const char* event,
                         const char* target, const char* interaction,
                         const char* evidence, const char* detail,
                         uint32_t bootstrap_status, int include_status) {
    if (!g_plan.session_id[0] || !event) return;
    const size_t cap = 4096;
    char* b = (char*)rml_alloc(cap);
    if (!b) return;
    size_t p = 0;
    char timestamp[40] = {};
    j_timestamp(timestamp, sizeof(timestamp));

    j_put_char(b, cap, &p, '{');
    j_put_str(b, cap, &p, "\"schemaVersion\":1,\"timestamp\":"); j_put_json(b, cap, &p, timestamp);
    j_put_str(b, cap, &p, ",\"sessionId\":"); j_put_json(b, cap, &p, g_plan.session_id);
    j_put_str(b, cap, &p, ",\"gameVersion\":"); j_put_json(b, cap, &p, g_plan.game_version);
    j_put_str(b, cap, &p, ",\"gameSha256\":"); j_put_json(b, cap, &p, g_plan.game_sha256);
    if (plugin) {
        j_put_str(b, cap, &p, ",\"dllSha256\":"); j_put_json(b, cap, &p, plugin->dll_sha256);
        j_put_str(b, cap, &p, ",\"pluginId\":"); j_put_json(b, cap, &p, plugin->id);
        j_put_str(b, cap, &p, ",\"pluginVersion\":"); j_put_json(b, cap, &p, plugin->version);
    }
    j_put_str(b, cap, &p, ",\"event\":"); j_put_json(b, cap, &p, event);
    if (target && target[0]) { j_put_str(b, cap, &p, ",\"target\":"); j_put_json(b, cap, &p, target); }
    if (interaction && interaction[0]) { j_put_str(b, cap, &p, ",\"interaction\":"); j_put_json(b, cap, &p, interaction); }
    if (evidence && evidence[0]) { j_put_str(b, cap, &p, ",\"evidence\":"); j_put_json(b, cap, &p, evidence); }
    if (detail && detail[0]) { j_put_str(b, cap, &p, ",\"detail\":"); j_put_json(b, cap, &p, detail); }
    if (include_status) { j_put_str(b, cap, &p, ",\"exitCode\":"); j_put_u32(b, cap, &p, bootstrap_status); }
    j_put_str(b, cap, &p, "}\r\n");
    b[p] = 0;

    wchar_t path[RML_MAX_PATH];
    if (!build_session_path(path, RML_MAX_PATH, L".journal")) { rml_free(b); return; }
    HANDLE f = CreateFileW(path, GENERIC_WRITE, FILE_SHARE_READ, 0, OPEN_ALWAYS,
                           FILE_ATTRIBUTE_NORMAL, 0);
    if (f == INVALID_HANDLE_VALUE) { rml_free(b); return; }
    LARGE_INTEGER zero = {};
    SetFilePointerEx(f, zero, 0, FILE_END);
    DWORD written = 0;
    WriteFile(f, b, (DWORD)p, &written, 0);
    CloseHandle(f);
    rml_free(b);
}

void journal_session_start(void) {
    open_session_lock();
    append_event(0, "session_start", 0, plan_profile_short(),
                 "rml-runtime", "RML runtime bootstrap began", 0, 0);
}

void journal_plugin_event(const PlanPlugin* plugin, const char* event,
                          const char* target, const char* interaction,
                          const char* evidence, const char* detail) {
    append_event(plugin, event, target, interaction, evidence, detail, 0, 0);
}

void journal_hook_event(const char* event, const char* target,
                        const char* interaction, const char* evidence,
                        const char* detail) {
    append_event(0, event, target, interaction, evidence, detail, 0, 0);
}

void journal_bootstrap_complete(uint32_t bootstrap_status) {
    append_event(0, "bootstrap_complete", 0, plan_profile_short(),
                 "rml-runtime", bootstrap_status ? "Bootstrap completed in degraded state" : "Bootstrap completed successfully",
                 bootstrap_status, 1);
}
