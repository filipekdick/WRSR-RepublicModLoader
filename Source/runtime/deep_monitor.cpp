#include "runtime_internal.h"

static DeepTextSnapshot g_deep_monitor_baseline = {};
static DeepTextSnapshot g_deep_monitor_current = {};
static volatile LONG g_deep_monitor_started = 0;

static DWORD WINAPI deep_monitor_thread(LPVOID) {
    if (!runtime_text_page_snapshot(&g_deep_monitor_baseline)) {
        log_write(RML_LOG_ERROR, "DeepMonitor", "Unable to establish post-COMMIT SOVIET64 .text baseline; late-write monitoring is disabled.");
        return 1;
    }

    log_u32(RML_LOG_INFO, "DeepMonitor", "Baseline .text pages: ", g_deep_monitor_baseline.page_count);
    log_u32(RML_LOG_INFO, "DeepMonitor", "Page size: ", g_deep_monitor_baseline.page_size);
    log_hex(RML_LOG_INFO, "DeepMonitor", "Baseline .text RVA: ", g_deep_monitor_baseline.text_rva);
    log_write(RML_LOG_INFO, "DeepMonitor", "Read-only late-change monitor online: full SOVIET64 .text fingerprint every 2 seconds; unchanged scans are silent except for a 30-second heartbeat.");

    uint64_t scans = 0;
    uint64_t change_windows = 0;
    for (;;) {
        Sleep(2000);
        ++scans;
        g_deep_test_stats.monitor_scans = scans;
        if (!runtime_text_page_snapshot(&g_deep_monitor_current)) {
            log_write(RML_LOG_WARN, "DeepMonitor", "A scheduled .text snapshot failed; monitor will retry on the next interval.");
            continue;
        }

        uint32_t first_rva = 0;
        uint32_t changed = runtime_text_page_diff(&g_deep_monitor_baseline, &g_deep_monitor_current, &first_rva);
        if (changed) {
            ++change_windows;
            g_deep_test_stats.late_change_windows = change_windows;
            log_write(RML_LOG_WARN, "DeepMonitor", "LATE EXECUTABLE CHANGE DETECTED after COMMIT.");
            log_u64(RML_LOG_INFO, "DeepMonitor", "Monitor scan number: ", scans);
            log_u64(RML_LOG_INFO, "DeepMonitor", "Late change window number: ", change_windows);
            log_u32(RML_LOG_INFO, "DeepMonitor", "Pages changed: ", changed);
            log_hex(RML_LOG_INFO, "DeepMonitor", "First changed RVA: ", first_rva);
            runtime_text_page_log_diff("DeepMonitor", "DEEP TRACE late-page diff BEGIN", &g_deep_monitor_baseline, &g_deep_monitor_current);
            tesmio_compat_deep_audit_state("late executable change", 1);
            g_deep_monitor_baseline = g_deep_monitor_current;
            log_write(RML_LOG_INFO, "DeepMonitor", "Late-change baseline advanced to the newly observed executable state.");
            write_runtime_report(g_deep_test_stats.bootstrap_status);
        } else if ((scans % 15u) == 0u) {
            log_u64(RML_LOG_INFO, "DeepMonitor", "Heartbeat — unchanged full-.text scans completed: ", scans);
            uint32_t issues = tesmio_compat_deep_audit_state("30-second integrity heartbeat", 0);
            if (!issues)
                log_write(RML_LOG_INFO, "DeepMonitor", "Heartbeat hook-chain audit: no new integrity anomaly detected.");
            else
                log_u32(RML_LOG_WARN, "DeepMonitor", "Heartbeat hook-chain anomalies: ", issues);
            write_runtime_report(g_deep_test_stats.bootstrap_status);
        }
    }
}

int deep_monitor_start(void) {
    if (!(g_plan.flags & PLAN_DEEP_TEST)) return 1;
    if (RmlInterlockedCompareExchange(&g_deep_monitor_started, 1, 0) != 0) return 1;
    HANDLE thread = CreateThread(0, 0, deep_monitor_thread, 0, 0, 0);
    if (!thread) {
        RmlInterlockedExchange(&g_deep_monitor_started, 0);
        log_u32(RML_LOG_ERROR, "DeepMonitor", "CreateThread failed: ", GetLastError());
        return 0;
    }
    CloseHandle(thread);
    return 1;
}
