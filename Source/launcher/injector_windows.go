//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	createSuspended     = 0x00000004
	createUnicodeEnv    = 0x00000400
	normalPriorityClass = 0x00000020

	memCommit  = 0x00001000
	memReserve = 0x00002000
	memRelease = 0x00008000

	pageNoAccess    = 0x01
	pageReadWrite   = 0x04
	pageExecute     = 0x10
	pageExecuteRead = 0x20
	pageExecuteRW   = 0x40
	pageExecuteWC   = 0x80
	pageGuard       = 0x100

	waitObject0 = 0x00000000
	waitTimeout = 0x00000102

	memCommitState = 0x00001000
	memImage       = 0x01000000
)

type startupInfo struct {
	Cb            uint32
	_             uint32
	Reserved      *uint16
	Desktop       *uint16
	Title         *uint16
	X             uint32
	Y             uint32
	XSize         uint32
	YSize         uint32
	XCountChars   uint32
	YCountChars   uint32
	FillAttribute uint32
	Flags         uint32
	ShowWindow    uint16
	CbReserved2   uint16
	Reserved2     *byte
	StdInput      uintptr
	StdOutput     uintptr
	StdErr        uintptr
}

type processInformation struct {
	Process   uintptr
	Thread    uintptr
	ProcessID uint32
	ThreadID  uint32
}

var (
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procCreateProcessW            = kernel32.NewProc("CreateProcessW")
	procVirtualAllocEx            = kernel32.NewProc("VirtualAllocEx")
	procVirtualFreeEx             = kernel32.NewProc("VirtualFreeEx")
	procVirtualProtectEx          = kernel32.NewProc("VirtualProtectEx")
	procWriteProcessMemory        = kernel32.NewProc("WriteProcessMemory")
	procReadProcessMemory         = kernel32.NewProc("ReadProcessMemory")
	procVirtualQueryEx            = kernel32.NewProc("VirtualQueryEx")
	procCreateRemoteThread        = kernel32.NewProc("CreateRemoteThread")
	procWaitForSingleObject       = kernel32.NewProc("WaitForSingleObject")
	procGetExitCodeThread         = kernel32.NewProc("GetExitCodeThread")
	procResumeThread              = kernel32.NewProc("ResumeThread")
	procTerminateProcess          = kernel32.NewProc("TerminateProcess")
	procGetExitCodeProcess        = kernel32.NewProc("GetExitCodeProcess")
	procGetProcessTimes           = kernel32.NewProc("GetProcessTimes")
	procGetPriorityClass          = kernel32.NewProc("GetPriorityClass")
	procGetProcessAffinityMask    = kernel32.NewProc("GetProcessAffinityMask")
	procGetThreadPriority         = kernel32.NewProc("GetThreadPriority")
	procGetProcessPriorityBoost   = kernel32.NewProc("GetProcessPriorityBoost")
	procGetProcessInformation     = kernel32.NewProc("GetProcessInformation")
	procCloseHandle               = kernel32.NewProc("CloseHandle")
	procLoadLibraryW              = kernel32.NewProc("LoadLibraryW")
	procGetLastError              = kernel32.NewProc("GetLastError")
	procGetModuleHandleW          = kernel32.NewProc("GetModuleHandleW")
	procGetModuleHandleExW        = kernel32.NewProc("GetModuleHandleExW")
	procGetModuleFileNameW        = kernel32.NewProc("GetModuleFileNameW")
	procFreeLibrary               = kernel32.NewProc("FreeLibrary")
	procFlushInstructionCache     = kernel32.NewProc("FlushInstructionCache")
	ntdll                         = syscall.NewLazyDLL("ntdll.dll")
	procNtQueryInformationProcess = ntdll.NewProc("NtQueryInformationProcess")
	psapi                         = syscall.NewLazyDLL("psapi.dll")
	procGetMappedFileNameW        = psapi.NewProc("GetMappedFileNameW")
)

func winErr(name string, err error) error {
	if errno, ok := err.(syscall.Errno); ok && errno == 0 {
		return errors.New(name + " failed")
	}
	return fmt.Errorf("%s: %w", name, err)
}

func closeHandle(h uintptr) {
	if h != 0 && h != ^uintptr(0) {
		procCloseHandle.Call(h)
	}
}

func readRemote(process, address uintptr, size int) ([]byte, error) {
	if address == 0 || size <= 0 {
		return nil, errors.New("invalid remote read")
	}
	buf := make([]byte, size)
	var read uintptr
	r, _, e := procReadProcessMemory.Call(
		process,
		address,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(size),
		uintptr(unsafe.Pointer(&read)),
	)
	if r == 0 || read != uintptr(size) {
		return nil, winErr("ReadProcessMemory", e)
	}
	return buf, nil
}

func isExecutableProtect(protect uint32) bool {
	if protect&pageGuard != 0 || protect&pageNoAccess != 0 {
		return false
	}
	base := protect & 0xff
	return base == pageExecute || base == pageExecuteRead || base == pageExecuteRW || base == pageExecuteWC
}

// readPtr64 reads one native x64 pointer from the target process.
func readPtr64(process, address uintptr) (uintptr, error) {
	b, err := readRemote(process, address, 8)
	if err != nil {
		return 0, err
	}
	return uintptr(binary.LittleEndian.Uint64(b)), nil
}

// remoteModuleBasePEB uses the target's own loader list once the Windows loader
// has initialized it. PEB offsets are used only as a fallback discovery path;
// the memory-map path below remains independent of loader-linked lists.
func remoteModuleBasePEB(process uintptr, moduleName string) (uintptr, error) {
	pbi := make([]byte, 48) // PROCESS_BASIC_INFORMATION on x64
	var retLen uintptr
	status, _, _ := procNtQueryInformationProcess.Call(
		process, 0, uintptr(unsafe.Pointer(&pbi[0])), uintptr(len(pbi)), uintptr(unsafe.Pointer(&retLen)),
	)
	if int32(status) < 0 {
		return 0, fmt.Errorf("NtQueryInformationProcess failed: NTSTATUS 0x%08X", uint32(status))
	}
	peb := uintptr(binary.LittleEndian.Uint64(pbi[8:16]))
	if peb == 0 {
		return 0, errors.New("remote PEB is null")
	}
	ldr, err := readPtr64(process, peb+0x18) // x64 PEB->Ldr
	if err != nil {
		return 0, fmt.Errorf("read PEB.Ldr: %w", err)
	}
	if ldr == 0 {
		return 0, errors.New("remote PEB loader data is not initialized")
	}
	head := ldr + 0x10 // PEB_LDR_DATA.InLoadOrderModuleList
	cur, err := readPtr64(process, head)
	if err != nil {
		return 0, fmt.Errorf("read loader list head: %w", err)
	}
	for i := 0; i < 512 && cur != 0 && cur != head; i++ {
		entry, err := readRemote(process, cur, 0x68)
		if err != nil {
			return 0, fmt.Errorf("read loader entry: %w", err)
		}
		next := uintptr(binary.LittleEndian.Uint64(entry[0:8]))
		dllBase := uintptr(binary.LittleEndian.Uint64(entry[0x30:0x38]))
		nameLen := int(binary.LittleEndian.Uint16(entry[0x58:0x5A]))
		nameBuf := uintptr(binary.LittleEndian.Uint64(entry[0x60:0x68]))
		if nameLen > 0 && nameLen <= 2048 && nameBuf != 0 {
			raw, readErr := readRemote(process, nameBuf, nameLen)
			if readErr == nil && len(raw)%2 == 0 {
				u16 := make([]uint16, len(raw)/2)
				for j := range u16 {
					u16[j] = binary.LittleEndian.Uint16(raw[j*2 : j*2+2])
				}
				if strings.EqualFold(syscall.UTF16ToString(u16), moduleName) && dllBase != 0 {
					return dllBase, nil
				}
			}
		}
		if next == cur {
			break
		}
		cur = next
	}
	return 0, fmt.Errorf("module %s not found via remote PEB", moduleName)
}

// remoteModuleBaseMemoryMap identifies image mappings by their mapped filename.
// This path does not rely on Toolhelp or on the PEB loader-linked list.
func remoteModuleBaseMemoryMap(process uintptr, moduleName string) (uintptr, error) {
	var address uintptr
	seen := map[uintptr]bool{}
	for address < ^uintptr(0)-0x10000 {
		mbi := make([]byte, 48) // MEMORY_BASIC_INFORMATION64
		n, _, _ := procVirtualQueryEx.Call(process, address, uintptr(unsafe.Pointer(&mbi[0])), uintptr(len(mbi)))
		if n == 0 {
			break
		}
		base := uintptr(binary.LittleEndian.Uint64(mbi[0:8]))
		allocBase := uintptr(binary.LittleEndian.Uint64(mbi[8:16]))
		regionSize := uintptr(binary.LittleEndian.Uint64(mbi[24:32]))
		typeValue := binary.LittleEndian.Uint32(mbi[40:44])
		if typeValue == memImage && allocBase != 0 && !seen[allocBase] {
			seen[allocBase] = true
			buf := make([]uint16, 32768)
			chars, _, _ := procGetMappedFileNameW.Call(
				process, allocBase, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
			)
			if chars > 0 && chars < uintptr(len(buf)) {
				mapped := syscall.UTF16ToString(buf[:chars])
				if strings.EqualFold(filepath.Base(mapped), moduleName) {
					return allocBase, nil
				}
			}
		}
		if regionSize == 0 {
			break
		}
		next := base + regionSize
		if next <= address {
			break
		}
		address = next
	}
	return 0, fmt.Errorf("module %s not found in target memory map", moduleName)
}

func remoteModuleBase(process uintptr, moduleName string) (uintptr, error) {
	base, pebErr := remoteModuleBasePEB(process, moduleName)
	if pebErr == nil && base != 0 {
		return base, nil
	}
	base, mapErr := remoteModuleBaseMemoryMap(process, moduleName)
	if mapErr == nil && base != 0 {
		return base, nil
	}
	return 0, fmt.Errorf("module discovery failed for %s; PEB: %v; memory map: %v", moduleName, pebErr, mapErr)
}

// localProcedureLocation records the module + RVA that actually contains a
// resolved Win32 procedure. This avoids assuming that a Kernel32 export's code
// necessarily lives in Kernel32; API forwarding to KernelBase is common.
func localProcedureLocation(proc *syscall.LazyProc) (address, moduleBase uintptr, moduleName string, err error) {
	address = proc.Addr()
	if address == 0 {
		return 0, 0, "", errors.New("local procedure address is null")
	}
	const (
		getModuleHandleExFlagUnchangedRefcount = 0x00000002
		getModuleHandleExFlagFromAddress       = 0x00000004
	)
	var module uintptr
	r, _, callErr := procGetModuleHandleExW.Call(
		getModuleHandleExFlagUnchangedRefcount|getModuleHandleExFlagFromAddress,
		address,
		uintptr(unsafe.Pointer(&module)),
	)
	if r == 0 || module == 0 {
		return 0, 0, "", winErr("GetModuleHandleExW(FROM_ADDRESS)", callErr)
	}
	buffer := make([]uint16, 32768)
	n, _, callErr := procGetModuleFileNameW.Call(module, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if n == 0 || n >= uintptr(len(buffer)) {
		return 0, 0, "", winErr("GetModuleFileNameW", callErr)
	}
	moduleName = filepath.Base(syscall.UTF16ToString(buffer[:n]))
	if moduleName == "" {
		return 0, 0, "", errors.New("could not identify module containing local procedure")
	}
	return address, module, moduleName, nil
}

// Validate the remote module+RVA as committed executable memory. Entry bytes
// may legitimately differ between processes because Windows can instrument or
// hot-patch system procedures.
func validateRemoteSystemProcedure(process, localAddress, remoteAddress uintptr, label string) (bool, error) {
	if localAddress == 0 || remoteAddress == 0 {
		return false, fmt.Errorf("%s address is null", label)
	}
	mbi := make([]byte, 48)
	n, _, e := procVirtualQueryEx.Call(process, remoteAddress, uintptr(unsafe.Pointer(&mbi[0])), uintptr(len(mbi)))
	if n == 0 {
		return false, winErr("VirtualQueryEx("+label+")", e)
	}
	state := binary.LittleEndian.Uint32(mbi[32:36])
	protect := binary.LittleEndian.Uint32(mbi[36:40])
	if state != memCommitState || !isExecutableProtect(protect) {
		return false, fmt.Errorf("%s target address 0x%X is not committed executable memory yet (state=0x%X protect=0x%X)", label, remoteAddress, state, protect)
	}
	const compareBytes = 16
	remote, err := readRemote(process, remoteAddress, compareBytes)
	if err != nil {
		return false, fmt.Errorf("read target %s bytes: %w", label, err)
	}
	local := unsafe.Slice((*byte)(unsafe.Pointer(localAddress)), compareBytes)
	return bytes.Equal(remote, local), nil
}

type targetSystemAPIs struct {
	LoadLibraryW uintptr
	GetLastError uintptr
}

// Resume the primary thread once so Windows can complete process-loader
// initialization, then resolve the remote loader entry points by module RVA.
func waitForTargetSystemAPIs(process, thread uintptr, logf func(string, ...any)) (targetSystemAPIs, error) {
	var result targetSystemAPIs
	loadLocal, loadLocalBase, loadModule, err := localProcedureLocation(procLoadLibraryW)
	if err != nil {
		return result, fmt.Errorf("identify local LoadLibraryW: %w", err)
	}
	errorLocal, errorLocalBase, errorModule, err := localProcedureLocation(procGetLastError)
	if err != nil {
		return result, fmt.Errorf("identify local GetLastError: %w", err)
	}
	loadRVA := loadLocal - loadLocalBase
	errorRVA := errorLocal - errorLocalBase
	logf("local system API bindings: %s!LoadLibraryW RVA=0x%X; %s!GetLastError RVA=0x%X", loadModule, loadRVA, errorModule, errorRVA)

	previous, _, e := procResumeThread.Call(thread)
	if previous == 0xFFFFFFFF {
		return result, winErr("ResumeThread(Windows loader warm-up)", e)
	}
	logf("primary WRSR thread resumed for Windows loader initialization and will remain running during RML attach")

	deadline := time.Now().Add(15 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		alive, exitCode, stateErr := processIsActive(process)
		if stateErr != nil {
			return result, fmt.Errorf("query WRSR during Windows loader initialization: %w", stateErr)
		}
		if !alive {
			return result, fmt.Errorf("WRSR exited during Windows loader initialization (exit code=0x%X)", exitCode)
		}

		remoteLoadBase, loadErr := remoteModuleBase(process, loadModule)
		remoteErrorBase, errorErr := remoteModuleBase(process, errorModule)
		if loadErr == nil && errorErr == nil {
			remoteLoad := remoteLoadBase + loadRVA
			remoteError := remoteErrorBase + errorRVA
			loadSame, loadValidateErr := validateRemoteSystemProcedure(process, loadLocal, remoteLoad, loadModule+"!LoadLibraryW")
			if loadValidateErr != nil {
				lastErr = loadValidateErr
			} else {
				errorSame, errorValidateErr := validateRemoteSystemProcedure(process, errorLocal, remoteError, errorModule+"!GetLastError")
				if errorValidateErr != nil {
					lastErr = errorValidateErr
				} else {
					if !loadSame {
						logf("target %s!LoadLibraryW entry bytes differ from the launcher process; module+RVA/executable mapping accepted", loadModule)
					}
					if !errorSame {
						logf("target %s!GetLastError entry bytes differ from the launcher process; module+RVA/executable mapping accepted", errorModule)
					}
					result.LoadLibraryW = remoteLoad
					result.GetLastError = remoteError
					logf("target Windows loader ready: %s!LoadLibraryW=0x%X; %s!GetLastError=0x%X", loadModule, remoteLoad, errorModule, remoteError)
					return result, nil
				}
			}
		} else if loadErr != nil {
			lastErr = loadErr
		} else {
			lastErr = errorErr
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		return result, fmt.Errorf("Windows loader did not become injection-ready within 15s: %w", lastErr)
	}
	return result, errors.New("Windows loader did not become injection-ready within 15s")
}

func allocWrite(process uintptr, data []byte) (uintptr, error) {
	if len(data) == 0 {
		return 0, errors.New("cannot allocate empty remote block")
	}
	remote, _, e := procVirtualAllocEx.Call(process, 0, uintptr(len(data)), memCommit|memReserve, pageReadWrite)
	if remote == 0 {
		return 0, winErr("VirtualAllocEx", e)
	}
	var written uintptr
	r, _, e := procWriteProcessMemory.Call(
		process,
		remote,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&written)),
	)
	if r == 0 || written != uintptr(len(data)) {
		procVirtualFreeEx.Call(process, remote, 0, memRelease)
		return 0, winErr("WriteProcessMemory", e)
	}
	return remote, nil
}

type remoteThreadResult struct {
	Code      uint32
	Completed bool
}

func runRemoteThread(process, start, parameter uintptr, timeout time.Duration, stage string) (remoteThreadResult, error) {
	var result remoteThreadResult
	var tid uint32
	thread, _, e := procCreateRemoteThread.Call(process, 0, 0, start, parameter, 0, uintptr(unsafe.Pointer(&tid)))
	if thread == 0 {
		return result, winErr("CreateRemoteThread("+stage+")", e)
	}
	defer closeHandle(thread)

	wait, _, e := procWaitForSingleObject.Call(thread, uintptr(timeout/time.Millisecond))
	if wait == waitTimeout {
		return result, fmt.Errorf("%s timed out after %s; remote thread left running", stage, timeout)
	}
	if wait != waitObject0 {
		return result, winErr("WaitForSingleObject("+stage+")", e)
	}
	result.Completed = true
	r, _, e := procGetExitCodeThread.Call(thread, uintptr(unsafe.Pointer(&result.Code)))
	if r == 0 {
		return result, winErr("GetExitCodeThread("+stage+")", e)
	}
	return result, nil
}

// loadRemoteLibrary uses a tiny transient x64 thunk solely so the full 64-bit
// HMODULE returned in RAX is written to remote memory. GetExitCodeThread exposes
// only a DWORD and would truncate a normal x64 module base.
func loadRemoteLibrary(process uintptr, remoteLoadLibrary, remoteGetLastError, remotePath uintptr, timeout time.Duration) (uintptr, uint32, error) {
	// Context: path, LoadLibraryW, GetLastError, HMODULE result, DWORD error.
	ctx := make([]byte, 40)
	binary.LittleEndian.PutUint64(ctx[0:8], uint64(remotePath))
	binary.LittleEndian.PutUint64(ctx[8:16], uint64(remoteLoadLibrary))
	binary.LittleEndian.PutUint64(ctx[16:24], uint64(remoteGetLastError))
	remoteCtx, err := allocWrite(process, ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("allocate remote loader context: %w", err)
	}

	// Windows x64 ABI: CreateRemoteThread enters with RCX=context and RSP at the
	// usual function-entry alignment. push rbx + 20h shadow space aligns calls.
	stub := []byte{
		0x53,                   // push rbx
		0x48, 0x83, 0xEC, 0x20, // sub rsp,20h
		0x48, 0x89, 0xCB, // mov rbx,rcx
		0x48, 0x8B, 0x43, 0x08, // mov rax,[rbx+08]
		0x48, 0x8B, 0x0B, // mov rcx,[rbx]
		0xFF, 0xD0, // call rax
		0x48, 0x89, 0x43, 0x18, // mov [rbx+18],rax
		0x48, 0x85, 0xC0, // test rax,rax
		0x75, 0x09, // jne success
		0x48, 0x8B, 0x43, 0x10, // mov rax,[rbx+10]
		0xFF, 0xD0, // call rax
		0x89, 0x43, 0x20, // mov [rbx+20],eax
		0x33, 0xC0, // success: xor eax,eax
		0x48, 0x83, 0xC4, 0x20, // add rsp,20h
		0x5B, // pop rbx
		0xC3, // ret
	}
	remoteStub, err := allocWrite(process, stub)
	if err != nil {
		procVirtualFreeEx.Call(process, remoteCtx, 0, memRelease)
		return 0, 0, fmt.Errorf("allocate remote loader thunk: %w", err)
	}
	var oldProtect uint32
	r, _, e := procVirtualProtectEx.Call(process, remoteStub, uintptr(len(stub)), pageExecuteRead, uintptr(unsafe.Pointer(&oldProtect)))
	if r == 0 {
		procVirtualFreeEx.Call(process, remoteStub, 0, memRelease)
		procVirtualFreeEx.Call(process, remoteCtx, 0, memRelease)
		return 0, 0, winErr("VirtualProtectEx(loader thunk)", e)
	}
	procFlushInstructionCache.Call(process, remoteStub, uintptr(len(stub)))

	tr, err := runRemoteThread(process, remoteStub, remoteCtx, timeout, "remote LoadLibraryW")
	if err != nil {
		// A timed-out remote thread may still be executing this code/context.
		// Deliberately leak these two tiny allocations for the lifetime of the
		// child instead of creating a use-after-free in WRSR.
		return 0, 0, err
	}
	_ = tr

	result, err := readRemote(process, remoteCtx, len(ctx))
	procVirtualFreeEx.Call(process, remoteStub, 0, memRelease)
	procVirtualFreeEx.Call(process, remoteCtx, 0, memRelease)
	if err != nil {
		return 0, 0, fmt.Errorf("read remote loader result: %w", err)
	}
	module := uintptr(binary.LittleEndian.Uint64(result[24:32]))
	lastErr := binary.LittleEndian.Uint32(result[32:36])
	if module == 0 {
		return 0, lastErr, fmt.Errorf("remote LoadLibraryW returned NULL (target GetLastError=%d / 0x%X)", lastErr, lastErr)
	}
	return module, lastErr, nil
}

func utf16Bytes(value string) ([]byte, error) {
	u, err := syscall.UTF16FromString(value)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(out[i*2:i*2+2], v)
	}
	return out, nil
}

type LaunchOptions struct {
	Profile   LaunchProfile
	SessionID string
}

type LaunchResult struct {
	ProcessID            uint32
	ProcessHandle        uintptr
	BootstrapStatus      uint32
	LoaderActive         bool
	SessionID            string
	Profile              string
	StartedAtUnixNano    int64
	RemoteThreadsCreated int
}

func createGameProcess(gamePath string) (processInformation, error) {
	var pi processInformation
	cmd, err := syscall.UTF16PtrFromString("\"" + gamePath + "\"")
	if err != nil {
		return pi, err
	}
	exe, err := syscall.UTF16PtrFromString(gamePath)
	if err != nil {
		return pi, err
	}
	cwd, err := syscall.UTF16PtrFromString(filepath.Dir(gamePath))
	if err != nil {
		return pi, err
	}
	flags := uintptr(createUnicodeEnv | normalPriorityClass | createSuspended)
	si := startupInfo{Cb: uint32(unsafe.Sizeof(startupInfo{}))}
	r, _, e := procCreateProcessW.Call(
		uintptr(unsafe.Pointer(exe)), uintptr(unsafe.Pointer(cmd)),
		0, 0, 0, flags, 0, uintptr(unsafe.Pointer(cwd)),
		uintptr(unsafe.Pointer(&si)), uintptr(unsafe.Pointer(&pi)),
	)
	if r == 0 {
		return pi, winErr("CreateProcessW", e)
	}
	return pi, nil
}

func launchAndInject(gamePath, runtimePath, rootPath string, options LaunchOptions) (result LaunchResult, retErr error) {
	result.StartedAtUnixNano = time.Now().UnixNano()
	result.SessionID = options.SessionID
	result.Profile = string(options.Profile)
	gamePath, _ = filepath.Abs(gamePath)
	runtimePath, _ = filepath.Abs(runtimePath)
	rootPath, _ = filepath.Abs(rootPath)
	logPath := filepath.Join(rootPath, "rml", "logs", "rml-runtime.log")
	logf := func(format string, args ...any) { appendLauncherLog(logPath, format, args...) }

	logf("launch requested: game=%s runtime=%s", gamePath, runtimePath)
	logf("launch assurance: fail-closed is enabled; a failed or partial RML attach is discarded and control returns to the launcher")

	if err := preflightRuntime(runtimePath, logf); err != nil {
		logf("RML runtime preflight failed: %v", err)
		return result, fmt.Errorf("RML runtime preflight failed: %w", err)
	}
	if root16, err := syscall.UTF16FromString(rootPath); err != nil || len(root16) >= 1024 {
		reason := "RML root path exceeds the runtime UTF-16 path limit"
		if err != nil {
			reason = "RML root path cannot be represented as UTF-16: " + err.Error()
		}
		logf("%s; RML launch aborted before WRSR was created", reason)
		return result, errors.New(reason)
	}

	pi, err := createGameProcess(gamePath)
	if err != nil {
		return result, err
	}
	result.ProcessID = pi.ProcessID
	logf("created WRSR process suspended before game code execution: PID %d", pi.ProcessID)
	logProcessScheduling(pi.Process, pi.Thread, logf, "created-suspended")
	processTransferred := false
	defer func() {
		closeHandle(pi.Thread)
		if !processTransferred {
			closeHandle(pi.Process)
		}
	}()

	// From this point onward every loader failure is fail-closed. A process that
	// may contain a partial runtime/plugin state is terminated rather than resumed.
	failClosed := func(cause error) (LaunchResult, error) {
		reason := cause.Error()
		logf("RML attach failed; terminating the trial process and returning to the launcher: %s", reason)
		if err := terminateTrialProcess(pi.Process, pi.ProcessID, logf); err != nil {
			logf("CRITICAL: could not dispose of partial WRSR trial process: %v", err)
			return result, fmt.Errorf("RML attach failed (%s) and the partial WRSR process could not be safely discarded: %w", reason, err)
		}
		return result, fmt.Errorf("RML launch aborted: %s", reason)
	}

	apis, err := waitForTargetSystemAPIs(pi.Process, pi.Thread, logf)
	if err != nil {
		return failClosed(fmt.Errorf("target Windows loader readiness failed: %w", err))
	}

	runtimeBytes, err := utf16Bytes(runtimePath)
	if err != nil {
		return failClosed(err)
	}
	remoteRuntime, err := allocWrite(pi.Process, runtimeBytes)
	if err != nil {
		return failClosed(err)
	}
	remoteRuntimeCanFree := true
	defer func() {
		if remoteRuntimeCanFree {
			procVirtualFreeEx.Call(pi.Process, remoteRuntime, 0, memRelease)
		}
	}()

	logf("injecting rml_runtime.dll while WRSR primary thread remains live")
	runtimeBase, remoteLastError, err := loadRemoteLibrary(pi.Process, apis.LoadLibraryW, apis.GetLastError, remoteRuntime, 20*time.Second)
	if err != nil {
		if bytes.Contains([]byte(err.Error()), []byte("timed out")) {
			remoteRuntimeCanFree = false
		}
		return failClosed(err)
	}
	result.RemoteThreadsCreated++
	logf("rml_runtime.dll loaded successfully at 0x%X (remote last-error=%d)", runtimeBase, remoteLastError)

	prepareRVA, err := exportRVA(runtimePath, "RmlBootstrap")
	if err != nil {
		return failClosed(fmt.Errorf("resolve RmlBootstrap prepare export: %w", err))
	}
	commitRVA, err := exportRVA(runtimePath, "RmlBootstrapCommit")
	if err != nil {
		return failClosed(fmt.Errorf("resolve RmlBootstrapCommit export: %w", err))
	}
	prepare := runtimeBase + uintptr(prepareRVA)
	commit := runtimeBase + uintptr(commitRVA)
	if err := validateRemoteExecutableAddress(pi.Process, prepare, "RmlBootstrap"); err != nil {
		return failClosed(err)
	}
	if err := validateRemoteExecutableAddress(pi.Process, commit, "RmlBootstrapCommit"); err != nil {
		return failClosed(err)
	}

	rootBytes, err := utf16Bytes(rootPath)
	if err != nil {
		return failClosed(err)
	}
	remoteRoot, err := allocWrite(pi.Process, rootBytes)
	if err != nil {
		return failClosed(err)
	}
	remoteRootCanFree := true
	defer func() {
		if remoteRootCanFree {
			procVirtualFreeEx.Call(pi.Process, remoteRoot, 0, memRelease)
		}
	}()

	// PREPARE keeps the WRSR primary thread live while plugin DLL loading and
	// lifecycle callbacks run, avoiding suspension while it may own a Windows or
	// driver lock.
	logf("calling RmlBootstrap PREPARE at 0x%X while WRSR primary thread remains live", prepare)
	prepareResult, err := runRemoteThread(pi.Process, prepare, remoteRoot, 60*time.Second, "RmlBootstrap PREPARE")
	if err != nil {
		if bytes.Contains([]byte(err.Error()), []byte("timed out")) {
			remoteRootCanFree = false
			logf("[PLUGIN CALL HUNG] RmlBootstrap PREPARE did not return within 60s. This phase contains plugin LoadLibrary/Init/Start work; inspect the last plugin lifecycle line for the closest attributable call.")
		}
		return failClosed(err)
	}
	result.RemoteThreadsCreated++
	prepareStatus := prepareResult.Code
	logf("RmlBootstrap PREPARE completed with status 0x%X", prepareStatus)

	// The root buffer is no longer used after PREPARE. Releasing it here also
	// makes any accidental use by COMMIT obvious rather than silently relying on
	// stale remote state.
	procVirtualFreeEx.Call(pi.Process, remoteRoot, 0, memRelease)
	remoteRootCanFree = false

	// COMMIT also keeps the WRSR primary thread live. Every legacy Start callback
	// has returned; COMMIT performs final chain verification/publication only.
	logf("RML COMMIT will run while WRSR primary thread remains live; legacy Start has already returned and host-mediated hooks are staged")
	logf("calling RmlBootstrapCommit at 0x%X", commit)
	commitResult, err := runRemoteThread(pi.Process, commit, 0, 30*time.Second, "RmlBootstrap COMMIT")
	if err != nil {
		if bytes.Contains([]byte(err.Error()), []byte("timed out")) {
			logf("[HOOK COMMIT HUNG] RmlBootstrap COMMIT did not return within 30s while final hook integrity/publication work was in progress.")
		}
		return failClosed(err)
	}
	result.RemoteThreadsCreated++
	commitStatus := commitResult.Code
	logf("RmlBootstrap COMMIT completed with status 0x%X", commitStatus)
	if commitStatus&0x80000000 != 0 {
		return failClosed(fmt.Errorf("RML hook transaction failed with fatal bootstrap status 0x%X", commitStatus))
	}

	result.BootstrapStatus = prepareStatus | commitStatus
	result.LoaderActive = true
	logf("RML two-phase bootstrap completed with combined status 0x%X", result.BootstrapStatus)

	if alive, exitCode, err := processIsActive(pi.Process); err != nil {
		return failClosed(fmt.Errorf("could not verify WRSR process state after bootstrap: %w", err))
	} else if !alive {
		return failClosed(fmt.Errorf("WRSR terminated during RML bootstrap (exit code=0x%X)", exitCode))
	}

	logProcessScheduling(pi.Process, pi.Thread, logf, "post-bootstrap")
	logf("RML bootstrap finished; WRSR primary thread is running and game launch is committed; remote threads created=%d", result.RemoteThreadsCreated)
	result.ProcessHandle = pi.Process
	processTransferred = true
	return result, nil
}

const stillActive = 259

func processIsActive(process uintptr) (bool, uint32, error) {
	var code uint32
	r, _, e := procGetExitCodeProcess.Call(process, uintptr(unsafe.Pointer(&code)))
	if r == 0 {
		return false, 0, winErr("GetExitCodeProcess", e)
	}
	return code == stillActive, code, nil
}

func terminateTrialProcess(process uintptr, pid uint32, logf func(string, ...any)) error {
	alive, code, err := processIsActive(process)
	if err != nil {
		return err
	}
	if !alive {
		logf("partial WRSR trial process PID %d had already exited (code=0x%X)", pid, code)
		return nil
	}
	r, _, e := procTerminateProcess.Call(process, 0xE04D4C01)
	if r == 0 {
		return winErr("TerminateProcess(partial WRSR trial)", e)
	}
	wait, _, e := procWaitForSingleObject.Call(process, 5000)
	if wait == waitTimeout {
		return errors.New("partial WRSR trial process did not terminate within 5s")
	}
	if wait != waitObject0 {
		return winErr("WaitForSingleObject(partial WRSR trial)", e)
	}
	logf("discarded partial WRSR trial process PID %d; no fallback game will be started", pid)
	return nil
}

func validateRemoteExecutableAddress(process, address uintptr, label string) error {
	mbi := make([]byte, 48)
	n, _, e := procVirtualQueryEx.Call(process, address, uintptr(unsafe.Pointer(&mbi[0])), uintptr(len(mbi)))
	if n == 0 {
		return winErr("VirtualQueryEx("+label+")", e)
	}
	state := binary.LittleEndian.Uint32(mbi[32:36])
	protect := binary.LittleEndian.Uint32(mbi[36:40])
	if state != memCommitState || !isExecutableProtect(protect) {
		return fmt.Errorf("%s at 0x%X is not executable in target (state=0x%X protect=0x%X)", label, address, state, protect)
	}
	return nil
}

const (
	processPowerThrottlingInformation = 4
	processPowerThrottlingVersion     = 1
	processPowerThrottlingExecution   = 0x1
)

type processPowerThrottlingState struct {
	Version     uint32
	ControlMask uint32
	StateMask   uint32
}

// logProcessScheduling records the process-level state most likely to explain
// a launch-path performance regression. This is diagnostic-only and avoids
// changing affinity or thread priority behind the user's
// back. CreateProcess already requests NORMAL_PRIORITY_CLASS explicitly.
func logProcessScheduling(process, thread uintptr, logf func(string, ...any), stage string) {
	if process == 0 || logf == nil {
		return
	}
	priority, _, _ := procGetPriorityClass.Call(process)
	var processMask, systemMask uintptr
	affinityOK, _, _ := procGetProcessAffinityMask.Call(
		process,
		uintptr(unsafe.Pointer(&processMask)),
		uintptr(unsafe.Pointer(&systemMask)),
	)
	threadPriority := int32(0x7fffffff)
	if thread != 0 {
		value, _, _ := procGetThreadPriority.Call(thread)
		threadPriority = int32(value)
	}
	var boostDisabled uint32
	boostOK, _, _ := procGetProcessPriorityBoost.Call(process, uintptr(unsafe.Pointer(&boostDisabled)))

	throttling := processPowerThrottlingState{Version: processPowerThrottlingVersion}
	powerOK, _, _ := procGetProcessInformation.Call(
		process,
		processPowerThrottlingInformation,
		uintptr(unsafe.Pointer(&throttling)),
		unsafe.Sizeof(throttling),
	)

	logf("process scheduling [%s]: priorityClass=0x%X threadPriority=%d", stage, priority, threadPriority)
	if affinityOK != 0 {
		logf("process scheduling [%s]: processAffinity=0x%X systemAffinity=0x%X", stage, processMask, systemMask)
	} else {
		logf("process scheduling [%s]: affinity unavailable", stage)
	}
	if boostOK != 0 {
		logf("process scheduling [%s]: priorityBoostDisabled=%t", stage, boostDisabled != 0)
	} else {
		logf("process scheduling [%s]: priority boost state unavailable", stage)
	}
	if powerOK != 0 {
		executionThrottled := throttling.ControlMask&processPowerThrottlingExecution != 0 && throttling.StateMask&processPowerThrottlingExecution != 0
		logf("process scheduling [%s]: powerThrottlingControl=0x%X state=0x%X executionSpeedThrottled=%t", stage, throttling.ControlMask, throttling.StateMask, executionThrottled)
	} else {
		logf("process scheduling [%s]: power throttling state unavailable on this Windows build", stage)
	}
}
