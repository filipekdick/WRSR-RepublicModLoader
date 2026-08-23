//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

const steamItemStateSubscribed = 1

type workshopUnsubscribeOutcome struct {
	Requested        bool
	Confirmed        bool
	Message          string
	FilesDeleted     bool
	FilesDeleteError string
}

func deleteWorkshopItemFiles(gamePath, id string) error {
	if !validWorkshopID(id) {
		return fmt.Errorf("invalid Workshop item ID")
	}
	root := workshopRootFromGamePath(gamePath)
	if root == "" {
		return fmt.Errorf("could not determine the WRSR Workshop content directory")
	}
	root = filepath.Clean(root)
	folder := filepath.Clean(filepath.Join(root, id))
	// Never let an untrusted/edited ID escape the exact Steam Workshop app
	// directory. The numeric ID validation above is repeated by checking the
	// final parent and basename before RemoveAll.
	if filepath.Clean(filepath.Dir(folder)) != root || filepath.Base(folder) != id {
		return fmt.Errorf("refusing to delete an unexpected Workshop path")
	}
	info, err := os.Lstat(folder)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("Workshop item path is not a directory")
	}
	if err := os.RemoveAll(folder); err != nil {
		return err
	}
	if _, err := os.Stat(folder); err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("Workshop item directory still exists after deletion")
	}
	return nil
}

func withTemporaryEnv(key, value string, fn func() workshopUnsubscribeOutcome) workshopUnsubscribeOutcome {
	old, had := os.LookupEnv(key)
	_ = os.Setenv(key, value)
	defer func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	}()
	return fn()
}

// unsubscribeWorkshopItem uses the Steamworks API shipped with WRSR. Local-file
// cleanup is deliberately a separate post-request step. The WRSR 1.1.1.9 executable itself requests
// STEAMUGC_INTERFACE_VERSION012, so the launcher asks the same steam_api64.dll
// for that interface and invokes the exported flat UnsubscribeItem function.
// The request is asynchronous; we run Steam callbacks briefly and use
// GetItemState to confirm that Steam has cleared the subscribed bit when it can.

type workshopSubscribeOutcome struct {
	Requested int
	Confirmed int
	Failed    int
}

func subscribeWorkshopItems(gamePath string, ids []string) workshopSubscribeOutcome {
	valid := make([]uint64, 0, len(ids))
	seen := map[uint64]bool{}
	for _, id := range ids {
		if !validWorkshopID(id) {
			continue
		}
		n, err := strconv.ParseUint(id, 10, 64)
		if err == nil && n != 0 && !seen[n] {
			seen[n] = true
			valid = append(valid, n)
		}
	}
	out := workshopSubscribeOutcome{}
	if len(valid) == 0 {
		return out
	}
	steamDLLPath := filepath.Join(filepath.Dir(gamePath), "steam_api64.dll")
	if info, err := os.Stat(steamDLLPath); err != nil || info.IsDir() {
		out.Failed = len(valid)
		return out
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	run := func() workshopSubscribeOutcome {
		result := workshopSubscribeOutcome{}
		dll, err := syscall.LoadDLL(steamDLLPath)
		if err != nil {
			result.Failed = len(valid)
			return result
		}
		defer dll.Release()
		find := func(name string) *syscall.Proc {
			p, e := dll.FindProc(name)
			if e != nil {
				return nil
			}
			return p
		}
		initProc, shutdownProc := find("SteamAPI_Init"), find("SteamAPI_Shutdown")
		getUserProc, findInterfaceProc := find("SteamAPI_GetHSteamUser"), find("SteamInternal_FindOrCreateUserInterface")
		subscribeProc, getStateProc, runCallbacksProc := find("SteamAPI_ISteamUGC_SubscribeItem"), find("SteamAPI_ISteamUGC_GetItemState"), find("SteamAPI_RunCallbacks")
		if initProc == nil || shutdownProc == nil || getUserProc == nil || findInterfaceProc == nil || subscribeProc == nil || getStateProc == nil || runCallbacksProc == nil {
			result.Failed = len(valid)
			return result
		}
		ok, _, _ := initProc.Call()
		if ok == 0 {
			result.Failed = len(valid)
			return result
		}
		defer shutdownProc.Call()
		hUser, _, _ := getUserProc.Call()
		ifaceName := append([]byte("STEAMUGC_INTERFACE_VERSION012"), 0)
		ugc, _, _ := findInterfaceProc.Call(hUser, uintptr(unsafe.Pointer(&ifaceName[0])))
		if ugc == 0 {
			result.Failed = len(valid)
			return result
		}
		pending := map[uint64]bool{}
		for _, id := range valid {
			state, _, _ := getStateProc.Call(ugc, uintptr(id))
			if uint32(state)&steamItemStateSubscribed != 0 {
				result.Confirmed++
				continue
			}
			h, _, _ := subscribeProc.Call(ugc, uintptr(id))
			if h == 0 {
				result.Failed++
				continue
			}
			result.Requested++
			pending[id] = true
		}
		deadline := time.Now().Add(4 * time.Second)
		for len(pending) > 0 && time.Now().Before(deadline) {
			runCallbacksProc.Call()
			for id := range pending {
				state, _, _ := getStateProc.Call(ugc, uintptr(id))
				if uint32(state)&steamItemStateSubscribed != 0 {
					result.Confirmed++
					delete(pending, id)
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return result
	}
	return withTemporaryEnvSubscribe("SteamAppId", wrsrSteamAppID, func() workshopSubscribeOutcome {
		return withTemporaryEnvSubscribe("SteamGameId", wrsrSteamAppID, run)
	})
}

func withTemporaryEnvSubscribe(key, value string, fn func() workshopSubscribeOutcome) workshopSubscribeOutcome {
	old, had := os.LookupEnv(key)
	_ = os.Setenv(key, value)
	defer func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	}()
	return fn()
}
func unsubscribeWorkshopItem(gamePath, id string) workshopUnsubscribeOutcome {
	if !validWorkshopID(id) {
		return workshopUnsubscribeOutcome{Message: "Invalid Workshop item ID"}
	}
	fileID, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return workshopUnsubscribeOutcome{Message: "Invalid Workshop item ID"}
	}
	steamDLLPath := filepath.Join(filepath.Dir(gamePath), "steam_api64.dll")
	if info, err := os.Stat(steamDLLPath); err != nil || info.IsDir() {
		return workshopUnsubscribeOutcome{Message: "steam_api64.dll was not found beside SOVIET64.exe"}
	}

	// Steam's per-thread glue is safest when initialization, interface lookup,
	// the async request and callback pumping all happen on one OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	run := func() workshopUnsubscribeOutcome {
		dll, err := syscall.LoadDLL(steamDLLPath)
		if err != nil {
			return workshopUnsubscribeOutcome{Message: "Could not load WRSR steam_api64.dll: " + err.Error()}
		}
		defer dll.Release()

		find := func(name string) (*syscall.Proc, error) {
			p, e := dll.FindProc(name)
			if e != nil {
				return nil, fmt.Errorf("%s is not exported by steam_api64.dll", name)
			}
			return p, nil
		}
		initProc, err := find("SteamAPI_Init")
		if err != nil {
			return workshopUnsubscribeOutcome{Message: err.Error()}
		}
		shutdownProc, err := find("SteamAPI_Shutdown")
		if err != nil {
			return workshopUnsubscribeOutcome{Message: err.Error()}
		}
		getUserProc, err := find("SteamAPI_GetHSteamUser")
		if err != nil {
			return workshopUnsubscribeOutcome{Message: err.Error()}
		}
		findInterfaceProc, err := find("SteamInternal_FindOrCreateUserInterface")
		if err != nil {
			return workshopUnsubscribeOutcome{Message: err.Error()}
		}
		unsubscribeProc, err := find("SteamAPI_ISteamUGC_UnsubscribeItem")
		if err != nil {
			return workshopUnsubscribeOutcome{Message: err.Error()}
		}
		getStateProc, err := find("SteamAPI_ISteamUGC_GetItemState")
		if err != nil {
			return workshopUnsubscribeOutcome{Message: err.Error()}
		}
		runCallbacksProc, err := find("SteamAPI_RunCallbacks")
		if err != nil {
			return workshopUnsubscribeOutcome{Message: err.Error()}
		}

		ok, _, callErr := initProc.Call()
		if ok == 0 {
			return workshopUnsubscribeOutcome{Message: "SteamAPI_Init failed. Make sure the Steam client is running and signed in."}
		}
		defer shutdownProc.Call()

		hUser, _, _ := getUserProc.Call()
		ifaceName := append([]byte("STEAMUGC_INTERFACE_VERSION012"), 0)
		ugc, _, _ := findInterfaceProc.Call(hUser, uintptr(unsafe.Pointer(&ifaceName[0])))
		if ugc == 0 {
			return workshopUnsubscribeOutcome{Message: "Steam UGC interface version 012 was unavailable"}
		}

		callHandle, _, callErr := unsubscribeProc.Call(ugc, uintptr(fileID))
		if callHandle == 0 {
			if callErr != syscall.Errno(0) {
				return workshopUnsubscribeOutcome{Message: "Steam rejected the unsubscribe request: " + callErr.Error()}
			}
			return workshopUnsubscribeOutcome{Message: "Steam rejected the unsubscribe request"}
		}
		out := workshopUnsubscribeOutcome{Requested: true, Message: "Steam accepted the unsubscribe request"}

		deadline := time.Now().Add(3500 * time.Millisecond)
		for time.Now().Before(deadline) {
			runCallbacksProc.Call()
			state, _, _ := getStateProc.Call(ugc, uintptr(fileID))
			if uint32(state)&steamItemStateSubscribed == 0 {
				out.Confirmed = true
				out.Message = "Unsubscribed in Steam."
				return out
			}
			time.Sleep(50 * time.Millisecond)
		}
		out.Message = "Steam accepted the unsubscribe request; confirmation is still pending."
		return out
	}

	// SteamAPI_Init discovers the consumer AppID through the process environment
	// when this launcher is not itself started as the game executable.
	return withTemporaryEnv("SteamAppId", wrsrSteamAppID, func() workshopUnsubscribeOutcome {
		return withTemporaryEnv("SteamGameId", wrsrSteamAppID, run)
	})
}
