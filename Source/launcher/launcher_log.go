package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var launcherLogMu sync.Mutex
var launcherDiagnosticMirror string

func appendLauncherLog(path string, format string, args ...any) {
	launcherLogMu.Lock()
	defer launcherLogMu.Unlock()
	line := fmt.Sprintf(format, args...)
	stamp := time.Now().Format("2006-01-02 15:04:05.000")
	write := func(target string) {
		if target == "" {
			return
		}
		_ = os.MkdirAll(filepath.Dir(target), 0755)
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = fmt.Fprintf(f, "%s [Launcher] %s\r\n", stamp, line)
	}
	write(path)
	if launcherDiagnosticMirror != "" && !strings.EqualFold(filepath.Clean(launcherDiagnosticMirror), filepath.Clean(path)) {
		write(launcherDiagnosticMirror)
	}
}

func setLauncherDiagnosticMirror(path string) {
	launcherLogMu.Lock()
	launcherDiagnosticMirror = path
	launcherLogMu.Unlock()
}

func diagnosticLogPath(base string, profile LaunchProfile, sessionID string) string {
	prefix := "diagnostic"
	switch profile {
	case profileDeepTest:
		prefix = "detailed-diagnostic"
	}
	safe := strings.NewReplacer(":", "-", "/", "-", "\\", "-", " ", "_").Replace(sessionID)
	return filepath.Join(base, "rml", "logs", prefix+"-"+safe+".log")
}

func appendDiagnosticFileSection(path, title, source string) {
	if path == "" || source == "" {
		return
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return
	}
	launcherLogMu.Lock()
	defer launcherLogMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "\r\n==================== %s ====================\r\n", title)
	_, _ = f.Write(data)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		_, _ = f.Write([]byte("\r\n"))
	}
}

func resetLauncherLog(path string) {
	launcherLogMu.Lock()
	defer launcherLogMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, nil, 0644)
}

func appendDiagnosticTextSection(path, title, text string) {
	if path == "" || strings.TrimSpace(text) == "" {
		return
	}
	launcherLogMu.Lock()
	defer launcherLogMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "\r\n==================== %s ====================\r\n", title)
	text = strings.ReplaceAll(text, "\n", "\r\n")
	_, _ = f.WriteString(text)
	if !strings.HasSuffix(text, "\r\n") {
		_, _ = f.WriteString("\r\n")
	}
}
