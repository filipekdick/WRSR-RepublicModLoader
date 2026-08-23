#include "runtime_internal.h"

volatile LONG g_log_lock = 0;
HANDLE g_log_file = INVALID_HANDLE_VALUE;
static HANDLE g_diag_file = INVALID_HANDLE_VALUE;
static wchar_t g_log_rml_dir[RML_MAX_PATH] = {};
static wchar_t g_log_logs_dir[RML_MAX_PATH] = {};
static wchar_t g_log_state_dir[RML_MAX_PATH] = {};
static wchar_t g_log_path[RML_MAX_PATH] = {};
static wchar_t g_diag_path[RML_MAX_PATH] = {};

static void lock_log(void) {
    // Logging is intentionally low-frequency. A tiny intrinsic spin lock avoids
    // importing SRW helpers during the earliest loader bootstrap.
    while (RmlInterlockedCompareExchange(&g_log_lock, 1, 0) != 0) { }
}
static void unlock_log(void) { RmlInterlockedExchange(&g_log_lock, 0); }

static void append_char(char* out, size_t cap, size_t* pos, char c) {
    if (*pos + 1 < cap) out[(*pos)++] = c;
}
static void append_str(char* out, size_t cap, size_t* pos, const char* s) {
    if (!s) return;
    while (*s && *pos + 1 < cap) out[(*pos)++] = *s++;
}
static void append_uint(char* out, size_t cap, size_t* pos, uint64_t value, int width, int hex) {
    char tmp[32]; int n = 0; uint32_t base = hex ? 16u : 10u;
    do { uint32_t d = (uint32_t)(value % base); tmp[n++] = (char)(d < 10 ? '0' + d : 'A' + d - 10); value /= base; }
    while (value && n < (int)sizeof(tmp));
    while (n < width && n < (int)sizeof(tmp)) tmp[n++] = '0';
    while (n) append_char(out, cap, pos, tmp[--n]);
}
static const char* level_name(RmlLogLevel level) {
    switch (level) {
        case RML_LOG_TRACE: return "TRACE";
        case RML_LOG_WARN: return "WARN ";
        case RML_LOG_ERROR: return "ERROR";
        default: return "INFO ";
    }
}

void log_open(void) {
    rml_join_path(g_log_rml_dir, RML_MAX_PATH, g_root_path, L"rml");
    rml_join_path(g_log_logs_dir, RML_MAX_PATH, g_log_rml_dir, L"logs");
    rml_join_path(g_log_state_dir, RML_MAX_PATH, g_log_rml_dir, L"state");
    CreateDirectoryW(g_log_rml_dir, 0);
    CreateDirectoryW(g_log_logs_dir, 0);
    CreateDirectoryW(g_log_state_dir, 0);
    rml_join_path(g_log_path, RML_MAX_PATH, g_log_logs_dir, L"rml-runtime.log");
    g_log_file = CreateFileW(g_log_path, GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE,
                             0, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, 0);
    if (g_log_file != INVALID_HANDLE_VALUE) {
        LARGE_INTEGER zero = {};
        SetFilePointerEx(g_log_file, zero, 0, FILE_END);
    }
}

void log_open_diagnostic(void) {
    if (!g_plan.session_id[0]) return;
    const wchar_t* prefix = 0;
    if (g_plan.profile == PROFILE_DEEP_TEST) prefix = L"detailed-diagnostic-";
    if (!prefix) return;
    wchar_t name[160] = {}; size_t p = 0;
    while (prefix[p] && p + 1 < 160) { name[p] = prefix[p]; ++p; }
    for (size_t i = 0; g_plan.session_id[i] && p + 5 < 160; ++i) {
        char c = g_plan.session_id[i];
        if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') name[p++] = (wchar_t)c;
        else name[p++] = L'_';
    }
    const wchar_t* ext = L".log"; for (size_t i=0; ext[i] && p+1<160; ++i) name[p++]=ext[i]; name[p]=0;
    rml_join_path(g_diag_path, RML_MAX_PATH, g_log_logs_dir, name);
    g_diag_file = CreateFileW(g_diag_path, GENERIC_WRITE, FILE_SHARE_READ | FILE_SHARE_WRITE, 0, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, 0);
    if (g_diag_file != INVALID_HANDLE_VALUE) { LARGE_INTEGER zero={}; SetFilePointerEx(g_diag_file, zero, 0, FILE_END); }
}

void log_close(void) {
    lock_log();
    HANDLE file = g_log_file; HANDLE diag = g_diag_file;
    g_log_file = INVALID_HANDLE_VALUE; g_diag_file = INVALID_HANDLE_VALUE;
    unlock_log();
    if (file != INVALID_HANDLE_VALUE) CloseHandle(file);
    if (diag != INVALID_HANDLE_VALUE) CloseHandle(diag);
}

void log_write(RmlLogLevel level, const char* component, const char* message) {
    char line[2048]; size_t p = 0; SYSTEMTIME t = {};
    GetLocalTime(&t);
    append_char(line, sizeof(line), &p, '[');
    append_uint(line, sizeof(line), &p, t.wHour, 2, 0); append_char(line, sizeof(line), &p, ':');
    append_uint(line, sizeof(line), &p, t.wMinute, 2, 0); append_char(line, sizeof(line), &p, ':');
    append_uint(line, sizeof(line), &p, t.wSecond, 2, 0); append_char(line, sizeof(line), &p, '.');
    append_uint(line, sizeof(line), &p, t.wMilliseconds, 3, 0);
    append_str(line, sizeof(line), &p, "] ["); append_str(line, sizeof(line), &p, level_name(level));
    append_str(line, sizeof(line), &p, "] ["); append_str(line, sizeof(line), &p, component ? component : "Core");
    append_str(line, sizeof(line), &p, "] "); append_str(line, sizeof(line), &p, message ? message : "");
    append_str(line, sizeof(line), &p, "\r\n"); line[p] = 0;
    lock_log();
    if (g_log_file != INVALID_HANDLE_VALUE) { DWORD written = 0; WriteFile(g_log_file, line, (DWORD)p, &written, 0); }
    if (g_diag_file != INVALID_HANDLE_VALUE) { DWORD written = 0; WriteFile(g_diag_file, line, (DWORD)p, &written, 0); }
    unlock_log();
}

void log_fault(RmlLogLevel level, const char* component, const char* marker, const char* message) {
    char msg[1536]; size_t p = 0;
    append_char(msg, sizeof(msg), &p, '[');
    append_str(msg, sizeof(msg), &p, marker ? marker : "FAULT");
    append_str(msg, sizeof(msg), &p, "] ");
    append_str(msg, sizeof(msg), &p, message ? message : "");
    msg[p] = 0;
    log_write(level, component, msg);
}

void log_fault_u32(RmlLogLevel level, const char* component, const char* marker, const char* prefix, uint32_t value) {
    char msg[512]; size_t p = 0;
    append_str(msg, sizeof(msg), &p, prefix ? prefix : "");
    append_uint(msg, sizeof(msg), &p, value, 0, 0);
    msg[p] = 0;
    log_fault(level, component, marker, msg);
}

void log_hex(RmlLogLevel level, const char* component, const char* prefix, uintptr_t value) {
    char msg[256]; size_t p = 0; append_str(msg, sizeof(msg), &p, prefix); append_str(msg, sizeof(msg), &p, "0x");
    append_uint(msg, sizeof(msg), &p, (uint64_t)value, 16, 1); msg[p] = 0; log_write(level, component, msg);
}
void log_u32(RmlLogLevel level, const char* component, const char* prefix, uint32_t value) {
    char msg[256]; size_t p = 0; append_str(msg, sizeof(msg), &p, prefix); append_uint(msg, sizeof(msg), &p, value, 0, 0);
    msg[p] = 0; log_write(level, component, msg);
}
void log_u64(RmlLogLevel level, const char* component, const char* prefix, uint64_t value) {
    char msg[256]; size_t p = 0; append_str(msg, sizeof(msg), &p, prefix); append_uint(msg, sizeof(msg), &p, value, 0, 0);
    msg[p] = 0; log_write(level, component, msg);
}
