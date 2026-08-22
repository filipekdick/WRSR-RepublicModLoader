//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

type fileTime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

func fileTime100ns(value fileTime) uint64 {
	return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
}

func processCPUTime(process uintptr) (kernelMs, userMs uint64, ok bool) {
	var create, exit, kernel, user fileTime
	r, _, _ := procGetProcessTimes.Call(
		process,
		uintptr(unsafe.Pointer(&create)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if r == 0 {
		return 0, 0, false
	}
	return fileTime100ns(kernel) / 10000, fileTime100ns(user) / 10000, true
}

func monitorLaunchedGame(result LaunchResult, base string, hwnd uintptr) {
	process := result.ProcessHandle
	if process == 0 {
		return
	}
	defer closeHandle(process)
	wait, _, _ := procWaitForSingleObject.Call(process, ^uintptr(0))
	var exitCode uint32
	if wait == waitObject0 {
		procGetExitCodeProcess.Call(process, uintptr(unsafe.Pointer(&exitCode)))
	}
	logPath := filepath.Join(base, "rml", "logs", "rml-runtime.log")
	elapsed := time.Duration(0)
	if result.StartedAtUnixNano > 0 {
		elapsed = time.Since(time.Unix(0, result.StartedAtUnixNano))
	}
	if kernelMs, userMs, ok := processCPUTime(process); ok {
		appendLauncherLog(logPath, "WRSR process exit: PID %d code=0x%X wall=%s kernelCPU=%dms userCPU=%dms profile=%s", result.ProcessID, exitCode, elapsed.Round(time.Millisecond), kernelMs, userMs, result.Profile)
	} else {
		appendLauncherLog(logPath, "WRSR process exit: PID %d code=0x%X wall=%s profile=%s (CPU times unavailable)", result.ProcessID, exitCode, elapsed.Round(time.Millisecond), result.Profile)
	}
	if result.Profile == string(profileDeepTest) && result.SessionID != "" {
		diag := diagnosticLogPath(base, profileDeepTest, result.SessionID)
		appendDiagnosticTextSection(diag, "DEEP FINDINGS SUMMARY", deepFindingsSummary(base))
		appendDiagnosticFileSection(diag, "RUNTIME REPORT JSON", filepath.Join(base, "rml", "state", "last_runtime_report.json"))
		appendDiagnosticFileSection(diag, "LAUNCHER CONFIG JSON", filepath.Join(base, "rml", "config.json"))
	}
	writeHighLevelSessionLog(base, exitCode, result.Profile, elapsed)
	if exitCode != 0 {
		cfg := loadConfig(filepath.Join(base, "rml", "config.json"))
		if cfg.AutoCrashArchives {
			_, _ = createSupportArchive(base, fmt.Sprintf("WRSR exited abnormally with code 0x%X", exitCode))
		}
	}
	if result.SessionID != "" && result.LoaderActive {
		event := JournalEvent{
			SchemaVersion: 1,
			Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
			SessionID:     result.SessionID,
			Event:         "session_end",
			Evidence:      "launcher-process-monitor",
			Detail:        fmt.Sprintf("WRSR process exited; profile=%s", result.Profile),
			ExitCode:      exitCode,
		}
		_ = appendJournalEventFile(base, event)
		_, _ = mergeSessionJournals(base, persistentKnowledgeRoot(base))
	}
	postAppMessage(hwnd, wmRMLGameExited, uintptr(exitCode), 0)
}

func appendJournalEventFile(base string, event JournalEvent) error {
	if event.SessionID == "" {
		return nil
	}
	dir := filepath.Join(base, "rml", "state", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, event.SessionID+".journal")
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func deepFindingsSummary(base string) string {
	report, err := loadRuntimeReport(filepath.Join(base, "rml", "state", "last_runtime_report.json"))
	if err != nil || report == nil {
		return "Runtime report unavailable; use the granular Deep trace above for failure attribution."
	}
	failed := 0
	unmanagedPlugins := 0
	var failedLines, unmanagedLines []string
	for _, p := range report.Plugins {
		if p.Failed {
			failed++
			failedLines = append(failedLines, fmt.Sprintf("FAILED plugin: %s • %s", p.ID, p.Status))
		}
		pages := p.UnmanagedInitPages + p.UnmanagedStartPages
		if pages > 0 {
			unmanagedPlugins++
			line := fmt.Sprintf("UNMANAGED code change: %s • %d page(s)", p.ID, pages)
			if p.FirstUnmanagedRVA != 0 {
				line += fmt.Sprintf(" • first page RVA 0x%X", p.FirstUnmanagedRVA)
			}
			unmanagedLines = append(unmanagedLines, line)
		}
	}
	passChains, externalChains, brokenChains, sharedChains := 0, 0, 0, 0
	var chainLines []string
	for _, c := range report.HookChains {
		if len(c.Steps) > 1 || c.ObservedTopOwner != "" {
			sharedChains++
		}
		switch strings.ToUpper(c.Integrity) {
		case "PASS":
			passChains++
		case "RECOGNIZED EXTERNAL CHAIN":
			externalChains++
			owner := c.ObservedTopOwner
			if owner == "" {
				owner = "unattributed external owner"
			}
			chainLines = append(chainLines, fmt.Sprintf("RECOGNIZED external chain: %s • top owner %s", c.Target, owner))
		default:
			brokenChains++
			chainLines = append(chainLines, fmt.Sprintf("BROKEN chain: %s • %s • %s", c.Target, c.Integrity, c.Proof))
		}
	}
	result := "CLEAN — no current integrity failure observed"
	if failed > 0 {
		result = "PLUGIN LIFECYCLE FAILURE"
	} else if brokenChains > 0 || report.DeepTest.LastAuditIssues > 0 {
		result = "HOOK-CHAIN INTEGRITY FAILURE"
	} else if report.DeepTest.LateChangeWindows > 0 {
		result = "LATE EXECUTABLE CHANGE OBSERVED"
	} else if externalChains > 0 || report.DeepTest.UnmanagedTextChanges > 0 {
		result = "COMPATIBILITY OBSERVATIONS — inspect attributed changes below"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Result: %s\n", result)
	fmt.Fprintf(&b, "Game: %s • SHA256 %s\n", report.Game.Version, report.Game.SHA256)
	fmt.Fprintf(&b, "Bootstrap: 0x%X • plugins observed %d • lifecycle failures %d\n", report.BootstrapStatus, report.DeepTest.PluginsObserved, failed)
	fmt.Fprintf(&b, "Host hook requests: %d • runtime chains: %d PASS / %d recognized external / %d broken • shared chains: %d\n", report.DeepTest.HostHookRequests, passChains, externalChains, brokenChains, sharedChains)
	fmt.Fprintf(&b, "Unmanaged executable changes: %d window(s) / %d page(s) across %d plugin(s)\n", report.DeepTest.UnmanagedTextChanges, report.DeepTest.UnmanagedTextPages, unmanagedPlugins)
	fmt.Fprintf(&b, "Deep monitor: %d scans • %d late change window(s) • last chain audit issues %d\n", report.DeepTest.MonitorScans, report.DeepTest.LateChangeWindows, report.DeepTest.LastAuditIssues)
	for _, line := range failedLines {
		b.WriteString(line + "\n")
	}
	for _, line := range unmanagedLines {
		b.WriteString(line + "\n")
	}
	for _, line := range chainLines {
		b.WriteString(line + "\n")
	}
	return strings.TrimSpace(b.String())
}
