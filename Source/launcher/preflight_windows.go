//go:build windows

package main

import (
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

const imageScnMemExecute = 0x20000000

// preflightRuntime validates everything we can prove in the launcher process
// before a WRSR process is created. In particular, this converts a remote
// ERROR_PROC_NOT_FOUND into a precise missing-import diagnostic and ensures the
// runtime itself can be loaded by this exact Windows installation.
func preflightRuntime(runtimePath string, logf func(string, ...any)) error {
	info, err := os.Stat(runtimePath)
	if err != nil {
		return fmt.Errorf("runtime DLL is unavailable: %w", err)
	}
	if info.IsDir() || info.Size() <= 0 {
		return errors.New("runtime DLL path is not a non-empty file")
	}

	machine, is64, err := readPEMachine(runtimePath)
	if err != nil {
		return fmt.Errorf("runtime PE validation failed: %w", err)
	}
	if machine != 0x8664 || !is64 {
		return fmt.Errorf("runtime DLL is not native AMD64 PE32+ (machine=0x%X pe64=%v)", machine, is64)
	}

	bootstrapRVA, err := exportRVA(runtimePath, "RmlBootstrap")
	if err != nil {
		return fmt.Errorf("runtime prepare export validation failed: %w", err)
	}
	commitRVA, err := exportRVA(runtimePath, "RmlBootstrapCommit")
	if err != nil {
		return fmt.Errorf("runtime commit export validation failed: %w", err)
	}

	pf, err := pe.Open(runtimePath)
	if err != nil {
		return fmt.Errorf("runtime PE parser rejected DLL: %w", err)
	}
	defer pf.Close()

	exportExecutable := func(rva uint32) bool {
		for _, sec := range pf.Sections {
			start := sec.VirtualAddress
			span := sec.VirtualSize
			if sec.Size > span {
				span = sec.Size
			}
			if rva >= start && rva < start+span {
				return sec.Characteristics&imageScnMemExecute != 0
			}
		}
		return false
	}
	if !exportExecutable(bootstrapRVA) {
		return fmt.Errorf("RmlBootstrap RVA 0x%X is not inside an executable PE section", bootstrapRVA)
	}
	if !exportExecutable(commitRVA) {
		return fmt.Errorf("RmlBootstrapCommit RVA 0x%X is not inside an executable PE section", commitRVA)
	}

	imports, err := pf.ImportedSymbols()
	if err != nil {
		return fmt.Errorf("runtime import table could not be read: %w", err)
	}
	missing := validateImportedProcedures(imports)
	if len(missing) != 0 {
		return fmt.Errorf("runtime has unresolved Windows imports: %s", strings.Join(missing, "; "))
	}
	logf("runtime static preflight passed: AMD64 PE32+, prepare RVA=0x%X commit RVA=0x%X, %d imported procedures resolved locally", bootstrapRVA, commitRVA, len(imports))

	// The strongest loader-time check: ask Windows to load the exact runtime in
	// the launcher process. DllMain is deliberately minimal, so this is safe and
	// catches import/relocation/loader rejection before WRSR exists.
	path16, err := syscall.UTF16PtrFromString(runtimePath)
	if err != nil {
		return fmt.Errorf("runtime path cannot be represented as UTF-16: %w", err)
	}
	h, _, callErr := procLoadLibraryW.Call(uintptr(unsafe.Pointer(path16)))
	if h == 0 {
		errno := uint32(0)
		if e, ok := callErr.(syscall.Errno); ok {
			errno = uint32(e)
		}
		if errno == 0 {
			r, _, _ := procGetLastError.Call()
			errno = uint32(r)
		}
		return fmt.Errorf("Windows rejected rml_runtime.dll during local LoadLibraryW preflight (GetLastError=%d / 0x%X)", errno, errno)
	}
	fr, _, freeErr := procFreeLibrary.Call(h)
	if fr == 0 {
		return winErr("FreeLibrary(runtime preflight)", freeErr)
	}
	logf("runtime dynamic preflight passed: local LoadLibraryW + FreeLibrary succeeded")
	return nil
}

func validateImportedProcedures(imports []string) []string {
	byDLL := map[string]*syscall.DLL{}
	var loaded []*syscall.DLL
	defer func() {
		for _, d := range loaded {
			_ = d.Release()
		}
	}()

	var missing []string
	seen := map[string]bool{}
	for _, item := range imports {
		// debug/pe returns "Procedure:DLL.dll". Split on the final colon so a
		// future unusual symbol name cannot confuse the DLL portion.
		split := strings.LastIndexByte(item, ':')
		if split <= 0 || split+1 >= len(item) {
			if !seen[item] {
				missing = append(missing, "malformed import "+item)
				seen[item] = true
			}
			continue
		}
		name := item[:split]
		dllName := item[split+1:]
		key := strings.ToLower(dllName)
		d := byDLL[key]
		if d == nil {
			var err error
			d, err = syscall.LoadDLL(dllName)
			if err != nil {
				msg := dllName + " (DLL load failed: " + err.Error() + ")"
				if !seen[msg] {
					missing = append(missing, msg)
					seen[msg] = true
				}
				byDLL[key] = &syscall.DLL{}
				continue
			}
			byDLL[key] = d
			loaded = append(loaded, d)
		}
		if d.Handle == 0 {
			continue
		}
		if _, err := d.FindProc(name); err != nil {
			msg := dllName + "!" + name
			if !seen[msg] {
				missing = append(missing, msg)
				seen[msg] = true
			}
		}
	}
	sort.Strings(missing)
	return missing
}
