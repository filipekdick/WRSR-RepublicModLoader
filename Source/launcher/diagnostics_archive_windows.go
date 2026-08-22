//go:build windows

package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func highLevelLogPath(base string) string {
	return filepath.Join(base, "rml", "logs", "rml-session.log")
}
func crashArchiveDir(base string) string { return filepath.Join(base, "rml", "crash-archives") }

func writeHighLevelSessionLog(base string, exitCode uint32, profile string, elapsed time.Duration) string {
	path := highLevelLogPath(base)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	var b strings.Builder
	fmt.Fprintf(&b, "Republic Mod Loader %s\n", launcherVersion)
	fmt.Fprintf(&b, "Session completed: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Launch mode: %s\n", profile)
	fmt.Fprintf(&b, "Duration: %s\n", elapsed.Round(time.Second))
	if exitCode == 0 {
		fmt.Fprintf(&b, "Exit: normal (0x0)\n")
	} else {
		fmt.Fprintf(&b, "Exit: abnormal (0x%X)\n", exitCode)
	}
	report, err := loadRuntimeReport(filepath.Join(base, "rml", "state", "last_runtime_report.json"))
	if err == nil && report != nil {
		fmt.Fprintf(&b, "WRSR: %s\n", report.Game.Version)
		fmt.Fprintf(&b, "Bootstrap: 0x%X\n", report.BootstrapStatus)
		fmt.Fprintf(&b, "\nPLUGINS\n")
		plugins := append(report.Plugins[:0:0], report.Plugins...)
		sort.SliceStable(plugins, func(i, j int) bool { return plugins[i].LoadOrder < plugins[j].LoadOrder })
		for _, p := range plugins {
			mark := "OK"
			if p.Failed || !p.Loaded {
				mark = "FAIL"
			} else if p.LegacyHookFailures > 0 || p.UnmanagedInitPages+p.UnmanagedStartPages > 0 {
				mark = "WARN"
			}
			name := strings.TrimSpace(p.Name)
			if name == "" {
				name = p.ID
			}
			fmt.Fprintf(&b, "[%s] %s %s — %s\n", mark, name, p.Version, p.Status)
			if p.Failed || !p.Loaded {
				fmt.Fprintf(&b, "[PLUGIN FAILED TO LOAD] %s — %s\n", p.ID, p.Status)
			}
			if p.LegacyHookFailures > 0 {
				fmt.Fprintf(&b, "[PLUGIN DEGRADED] %s — hook requests not installed: %d\n", p.ID, p.LegacyHookFailures)
			}
			if p.UnmanagedInitPages+p.UnmanagedStartPages > 0 {
				fmt.Fprintf(&b, "[PLUGIN DIRECT CODE CHANGE] %s — unmanaged executable pages: %d\n", p.ID, p.UnmanagedInitPages+p.UnmanagedStartPages)
			}
		}
		fmt.Fprintf(&b, "\nDIAGNOSTIC SUMMARY\n")
		fmt.Fprintf(&b, "Hook requests: %d\n", report.DeepTest.HostHookRequests)
		fmt.Fprintf(&b, "Unmanaged executable-change windows: %d\n", report.DeepTest.UnmanagedTextChanges)
		fmt.Fprintf(&b, "Late executable-change windows: %d\n", report.DeepTest.LateChangeWindows)
		fmt.Fprintf(&b, "Final hook-chain audit issues: %d\n", report.DeepTest.LastAuditIssues)
	}
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
	return path
}

func latestVerboseDiagnostic(base string) string {
	dir := filepath.Join(base, "rml", "logs")
	entries, _ := os.ReadDir(dir)
	newest := ""
	var newestTime time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := strings.ToLower(e.Name())
		if !strings.HasPrefix(n, "detailed-diagnostic-") || !strings.HasSuffix(n, ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestTime) {
			newest = filepath.Join(dir, e.Name())
			newestTime = info.ModTime()
		}
	}
	return newest
}

func createSupportArchive(base, reason string) (string, error) {
	dir := crashArchiveDir(base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	path := filepath.Join(dir, "RML-Support-"+stamp+".zip")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	readme := fmt.Sprintf("Republic Mod Loader support archive\nCreated: %s\nReason: %s\n\nThis archive intentionally does not contain plugin DLL binaries.\n", time.Now().Format(time.RFC3339), reason)
	w, _ := zw.Create("README.txt")
	_, _ = w.Write([]byte(readme))
	files := []struct{ src, name string }{
		{highLevelLogPath(base), "session.log"},
		{filepath.Join(base, "rml", "logs", "rml-runtime.log"), "runtime.log"},
		{filepath.Join(base, "rml", "state", "last_runtime_report.json"), "runtime-report.json"},
		{filepath.Join(base, "rml", "config.json"), "configuration.json"},
	}
	if v := latestVerboseDiagnostic(base); v != "" {
		files = append(files, struct{ src, name string }{v, "verbose.log"})
	}
	for _, it := range files {
		in, err := os.Open(it.src)
		if err != nil {
			continue
		}
		out, err := zw.Create(it.name)
		if err == nil {
			_, _ = io.Copy(out, in)
		}
		in.Close()
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return path, nil
}
