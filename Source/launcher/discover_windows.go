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
	hkeyCurrentUser   = 0x80000001
	hkeyLocalMachine  = 0x80000002
	rrfRTRegSZ        = 0x00000002
	rmlWorkshopItemID = "3787969749"
)

var (
	advapi32                 = syscall.NewLazyDLL("advapi32.dll")
	procRegGetValueW         = advapi32.NewProc("RegGetValueW")
	steamPathEntryRE         = regexp.MustCompile(`(?im)^\s*"path"\s+"([^"]+)"`)
	steamLegacyLibraryLineRE = regexp.MustCompile(`(?im)^\s*"[0-9]+"\s+"([^"]+)"\s*$`)
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

func cleanSteamPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(strings.ReplaceAll(path, "/", `\`))
}

func appendUniquePath(paths []string, seen map[string]bool, path string) []string {
	path = cleanSteamPath(path)
	if path == "" {
		return paths
	}
	key := strings.ToLower(path)
	if seen[key] {
		return paths
	}
	seen[key] = true
	return append(paths, path)
}

// knownSteamRoots returns Steam client installations, not individual game
// libraries. Registry values are authoritative; the conventional locations are
// retained only as a recovery fallback for damaged/missing registry state.
func knownSteamRoots() []string {
	candidates := []string{
		registryString(hkeyCurrentUser, `Software\Valve\Steam`, "SteamPath"),
		registryString(hkeyLocalMachine, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath"),
		`C:\Program Files (x86)\Steam`,
		`C:\Program Files\Steam`,
	}
	seen := map[string]bool{}
	roots := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		roots = appendUniquePath(roots, seen, candidate)
	}
	return roots
}

// steamLibraries reads Steam's configured library folders. Current VDF files
// use nested "path" entries; the legacy numeric-entry form is accepted only as
// a fallback so numeric app IDs inside modern "apps" blocks are never mistaken
// for filesystem paths.
func steamLibraries(steamRoot string) []string {
	data, err := os.ReadFile(filepath.Join(steamRoot, "steamapps", "libraryfolders.vdf"))
	if err != nil {
		return nil
	}
	text := string(data)
	matches := steamPathEntryRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		matches = steamLegacyLibraryLineRE.FindAllStringSubmatch(text, -1)
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := strings.ReplaceAll(match[1], `\\`, `\`)
		result = appendUniquePath(result, seen, path)
	}
	return result
}

// allSteamLibraries returns every configured Steam library across every Steam
// client root we can identify. The client root itself is always included as a
// library because the default steamapps directory lives there.
func allSteamLibraries() []string {
	seen := map[string]bool{}
	var libraries []string
	for _, steamRoot := range knownSteamRoots() {
		libraries = appendUniquePath(libraries, seen, steamRoot)
		for _, library := range steamLibraries(steamRoot) {
			libraries = appendUniquePath(libraries, seen, library)
		}
	}
	return libraries
}

func existingFile(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

func discoverGamePath(base string) string {
	candidates := []string{
		filepath.Join(base, "SOVIET64.exe"),
		filepath.Join(filepath.Dir(base), "SOVIET64.exe"),
	}
	for _, library := range allSteamLibraries() {
		candidates = append(candidates, filepath.Join(library, "steamapps", "common", "SovietRepublic", "SOVIET64.exe"))
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		key := strings.ToLower(candidate)
		if seen[key] {
			continue
		}
		seen[key] = true
		if path := existingFile(candidate); path != "" {
			return path
		}
	}
	return ""
}

func usableRMLRuntimeRoot(root string) bool {
	return existingFile(filepath.Join(root, "rml_runtime.dll")) != ""
}

// resolveRMLRuntimeRoot allows RepublicModLoader.exe to be copied anywhere
// without copying the runtime payload. A complete local copy remains
// self-contained; otherwise the launcher locates Workshop item 3787969749
// through WRSR's configured Steam library and then, if necessary, through every
// Steam library listed by the client. No drive letter is assumed.
func resolveRMLRuntimeRoot(exeDir string) (root, mode string) {
	exeDir = filepath.Clean(exeDir)
	if usableRMLRuntimeRoot(exeDir) {
		return exeDir, "local-complete"
	}

	if gamePath := discoverGamePath(exeDir); gamePath != "" {
		if workshopRoot := workshopRootFromGamePath(gamePath); workshopRoot != "" {
			candidate := filepath.Join(workshopRoot, rmlWorkshopItemID)
			if usableRMLRuntimeRoot(candidate) {
				if abs, err := filepath.Abs(candidate); err == nil {
					candidate = abs
				}
				return candidate, "workshop-fallback"
			}
		}
	}

	for _, library := range allSteamLibraries() {
		candidate := filepath.Join(library, "steamapps", "workshop", "content", wrsrSteamAppID, rmlWorkshopItemID)
		if usableRMLRuntimeRoot(candidate) {
			if abs, err := filepath.Abs(candidate); err == nil {
				candidate = abs
			}
			return candidate, "workshop-fallback"
		}
	}

	// Keep the executable directory as a final, intentionally incomplete root so
	// the UI can open and report a useful missing-runtime error instead of dying
	// before the user can see what is wrong.
	return exeDir, "local-incomplete"
}
