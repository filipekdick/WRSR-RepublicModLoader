//go:build windows

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unsafe"
)

const (
	hkeyCurrentUser  = 0x80000001
	hkeyLocalMachine = 0x80000002
	rrfRTRegSZ       = 0x00000002
)

var (
	advapi32         = syscall.NewLazyDLL("advapi32.dll")
	procRegGetValueW = advapi32.NewProc("RegGetValueW")
)

func registryString(root uintptr, subkey, value string) string {
	sk, _ := syscall.UTF16PtrFromString(subkey)
	vn, _ := syscall.UTF16PtrFromString(value)
	var size uint32
	r, _, _ := procRegGetValueW.Call(root, uintptr(unsafe.Pointer(sk)), uintptr(unsafe.Pointer(vn)), rrfRTRegSZ, 0, 0, uintptr(unsafe.Pointer(&size)))
	if r != 0 || size < 2 || size > 64*1024 {
		return ""
	}
	buf := make([]uint16, (size+1)/2)
	r, _, _ = procRegGetValueW.Call(root, uintptr(unsafe.Pointer(sk)), uintptr(unsafe.Pointer(vn)), rrfRTRegSZ, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func discoverGamePath(base string) string {
	var candidates []string
	candidates = append(candidates,
		filepath.Join(base, "SOVIET64.exe"),
		filepath.Join(filepath.Dir(base), "SOVIET64.exe"),
		`C:\Program Files (x86)\Steam\steamapps\common\SovietRepublic\SOVIET64.exe`,
		`C:\Program Files\Steam\steamapps\common\SovietRepublic\SOVIET64.exe`,
	)
	steamRoots := []string{
		registryString(hkeyCurrentUser, `Software\Valve\Steam`, "SteamPath"),
		registryString(hkeyLocalMachine, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath"),
	}
	seen := map[string]bool{}
	for _, steam := range steamRoots {
		if steam == "" {
			continue
		}
		steam = filepath.Clean(strings.ReplaceAll(steam, "/", `\`))
		if seen[strings.ToLower(steam)] {
			continue
		}
		seen[strings.ToLower(steam)] = true
		candidates = append(candidates, filepath.Join(steam, "steamapps", "common", "SovietRepublic", "SOVIET64.exe"))
		for _, library := range steamLibraries(steam) {
			candidates = append(candidates, filepath.Join(library, "steamapps", "common", "SovietRepublic", "SOVIET64.exe"))
		}
	}
	for _, path := range candidates {
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			abs, _ := filepath.Abs(path)
			return abs
		}
	}
	return ""
}

func steamLibraries(steamRoot string) []string {
	data, err := os.ReadFile(filepath.Join(steamRoot, "steamapps", "libraryfolders.vdf"))
	if err != nil {
		return nil
	}
	// Handles both legacy numeric entries and current nested "path" entries.
	re := regexp.MustCompile(`(?i)"(?:path|[0-9]+)"\s+"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(data), -1)
	var result []string
	seen := map[string]bool{}
	for _, m := range matches {
		path := strings.ReplaceAll(m[1], `\\`, `\`)
		key := strings.ToLower(filepath.Clean(path))
		if path != "" && !seen[key] {
			seen[key] = true
			result = append(result, filepath.Clean(path))
		}
	}
	return result
}
