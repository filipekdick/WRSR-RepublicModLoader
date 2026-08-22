//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const workshopUpdateCacheSchema = 1

const (
	steamItemStateInstalled       = 4
	steamItemStateNeedsUpdate     = 8
	steamItemStateDownloading     = 16
	steamItemStateDownloadPending = 32
)

type WorkshopUpdateRecord struct {
	State             string `json:"state"` // current, update, stale, downloading, missing, unavailable, unknown
	Reason            string `json:"reason,omitempty"`
	CheckedUnix       int64  `json:"checkedUnix"`
	InstalledUnix     int64  `json:"installedUnix,omitempty"`
	RemoteUpdatedUnix int64  `json:"remoteUpdatedUnix,omitempty"`
	SteamState        uint32 `json:"steamState,omitempty"`
	InstalledManifest string `json:"installedManifest,omitempty"`
	LatestManifest    string `json:"latestManifest,omitempty"`
}

type workshopUpdateCacheFile struct {
	SchemaVersion int                             `json:"schemaVersion"`
	Records       map[string]WorkshopUpdateRecord `json:"records"`
}

type workshopUpdateCompletion struct {
	Records map[string]WorkshopUpdateRecord
	Checked int
	Current int
	Update  int
	Stale   int
	Missing int
	Unknown int
}

type steamLocalWorkshopState struct {
	State         uint32
	Installed     bool
	InstalledUnix int64
	Folder        string
}

type workshopForceUpdateOutcome struct {
	Requested         bool
	Unsubscribed      bool
	FilesDeleted      bool
	Resubscribed      bool
	DownloadRequested bool
	Downloading       bool
	Message           string
	FilesDeleteError  string
}

type workshopForceUpdateCompletion struct {
	ID      string
	Name    string
	Outcome workshopForceUpdateOutcome
}

func workshopUpdateCachePath(knowledgeRoot string) string {
	return filepath.Join(knowledgeRoot, "workshop", "update-status.json")
}

func loadWorkshopUpdateCache(knowledgeRoot string) map[string]WorkshopUpdateRecord {
	out := map[string]WorkshopUpdateRecord{}
	data, err := os.ReadFile(workshopUpdateCachePath(knowledgeRoot))
	if err != nil || len(data) > 4*1024*1024 {
		return out
	}
	var file workshopUpdateCacheFile
	if json.Unmarshal(data, &file) != nil || file.SchemaVersion != workshopUpdateCacheSchema || file.Records == nil {
		return out
	}
	for id, record := range file.Records {
		if validWorkshopID(id) {
			out[id] = record
		}
	}
	return out
}

func saveWorkshopUpdateCache(knowledgeRoot string, records map[string]WorkshopUpdateRecord) {
	if records == nil {
		return
	}
	path := workshopUpdateCachePath(knowledgeRoot)
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	file := workshopUpdateCacheFile{SchemaVersion: workshopUpdateCacheSchema, Records: records}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) != nil {
		return
	}
	_ = os.Remove(path)
	_ = os.Rename(tmp, path)
}

func appWorkshopManifestPath(gamePath string) string {
	steamapps := steamappsFromGamePath(gamePath)
	if steamapps == "" {
		return ""
	}
	return filepath.Join(steamapps, "workshop", "appworkshop_"+wrsrSteamAppID+".acf")
}

// parseValveKeyValues implements the tiny quoted-string/braces subset used by
// Steam's appworkshop_<appid>.acf files. It deliberately does not attempt to be
// a general VDF parser; malformed input simply yields no corroborating data.
func parseValveKeyValues(data []byte) map[string]any {
	var tokens []string
	for i := 0; i < len(data); {
		switch data[i] {
		case ' ', '\t', '\r', '\n':
			i++
		case '{', '}':
			tokens = append(tokens, string(data[i]))
			i++
		case '"':
			i++
			var b strings.Builder
			for i < len(data) {
				if data[i] == '"' {
					i++
					break
				}
				if data[i] == '\\' && i+1 < len(data) {
					i++
				}
				b.WriteByte(data[i])
				i++
			}
			tokens = append(tokens, b.String())
		default:
			start := i
			for i < len(data) && data[i] != ' ' && data[i] != '\t' && data[i] != '\r' && data[i] != '\n' && data[i] != '{' && data[i] != '}' {
				i++
			}
			if i > start {
				tokens = append(tokens, string(data[start:i]))
			}
		}
	}
	pos := 0
	var parseObject func(stopAtBrace bool) map[string]any
	parseObject = func(stopAtBrace bool) map[string]any {
		obj := map[string]any{}
		for pos < len(tokens) {
			if tokens[pos] == "}" {
				if stopAtBrace {
					pos++
				}
				return obj
			}
			key := tokens[pos]
			pos++
			if pos >= len(tokens) {
				return obj
			}
			if tokens[pos] == "{" {
				pos++
				obj[key] = parseObject(true)
			} else {
				obj[key] = tokens[pos]
				pos++
			}
		}
		return obj
	}
	return parseObject(false)
}

func kvObject(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return nil
	}
	for k, v := range parent {
		if strings.EqualFold(k, key) {
			if m, ok := v.(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

func kvString(parent map[string]any, key string) string {
	if parent == nil {
		return ""
	}
	for k, v := range parent {
		if strings.EqualFold(k, key) {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

type acfWorkshopRecord struct {
	InstalledUnix     int64
	InstalledManifest string
	LatestUnix        int64
	LatestManifest    string
}

func readWorkshopACF(gamePath string) map[string]acfWorkshopRecord {
	out := map[string]acfWorkshopRecord{}
	path := appWorkshopManifestPath(gamePath)
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 32*1024*1024 {
		return out
	}
	root := parseValveKeyValues(data)
	app := kvObject(root, "AppWorkshop")
	installed := kvObject(app, "WorkshopItemsInstalled")
	details := kvObject(app, "WorkshopItemDetails")
	add := func(id string) {
		if !validWorkshopID(id) {
			return
		}
		rec := out[id]
		if m, ok := installed[id].(map[string]any); ok {
			rec.InstalledManifest = kvString(m, "manifest")
			rec.InstalledUnix, _ = strconv.ParseInt(kvString(m, "timeupdated"), 10, 64)
		}
		if m, ok := details[id].(map[string]any); ok {
			rec.LatestManifest = kvString(m, "latest_manifest")
			rec.LatestUnix, _ = strconv.ParseInt(kvString(m, "latest_timeupdated"), 10, 64)
			if rec.LatestManifest == "" {
				rec.LatestManifest = kvString(m, "manifest")
			}
			if rec.LatestUnix == 0 {
				rec.LatestUnix, _ = strconv.ParseInt(kvString(m, "timeupdated"), 10, 64)
			}
		}
		out[id] = rec
	}
	for id := range installed {
		add(id)
	}
	for id := range details {
		add(id)
	}
	return out
}

func steamUGCLocalStates(gamePath string, ids []string) map[string]steamLocalWorkshopState {
	out := map[string]steamLocalWorkshopState{}
	if len(ids) == 0 {
		return out
	}
	steamDLLPath := filepath.Join(filepath.Dir(gamePath), "steam_api64.dll")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	oldApp, hadApp := os.LookupEnv("SteamAppId")
	oldGame, hadGame := os.LookupEnv("SteamGameId")
	_ = os.Setenv("SteamAppId", wrsrSteamAppID)
	_ = os.Setenv("SteamGameId", wrsrSteamAppID)
	defer func() {
		if hadApp {
			_ = os.Setenv("SteamAppId", oldApp)
		} else {
			_ = os.Unsetenv("SteamAppId")
		}
		if hadGame {
			_ = os.Setenv("SteamGameId", oldGame)
		} else {
			_ = os.Unsetenv("SteamGameId")
		}
	}()

	dll, err := syscall.LoadDLL(steamDLLPath)
	if err != nil {
		return out
	}
	defer dll.Release()
	initProc, err := dll.FindProc("SteamAPI_Init")
	if err != nil {
		return out
	}
	shutdownProc, err := dll.FindProc("SteamAPI_Shutdown")
	if err != nil {
		return out
	}
	getUserProc, err := dll.FindProc("SteamAPI_GetHSteamUser")
	if err != nil {
		return out
	}
	findInterfaceProc, err := dll.FindProc("SteamInternal_FindOrCreateUserInterface")
	if err != nil {
		return out
	}
	getStateProc, err := dll.FindProc("SteamAPI_ISteamUGC_GetItemState")
	if err != nil {
		return out
	}
	getInstallProc, err := dll.FindProc("SteamAPI_ISteamUGC_GetItemInstallInfo")
	if err != nil {
		return out
	}
	ok, _, _ := initProc.Call()
	if ok == 0 {
		return out
	}
	defer shutdownProc.Call()
	hUser, _, _ := getUserProc.Call()
	ifaceName := append([]byte("STEAMUGC_INTERFACE_VERSION012"), 0)
	ugc, _, _ := findInterfaceProc.Call(hUser, uintptr(unsafe.Pointer(&ifaceName[0])))
	if ugc == 0 {
		return out
	}

	for _, id := range ids {
		fileID, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			continue
		}
		state, _, _ := getStateProc.Call(ugc, uintptr(fileID))
		local := steamLocalWorkshopState{State: uint32(state)}
		var sizeOnDisk uint64
		var stamp uint32
		folder := make([]byte, 4096)
		installedOK, _, _ := getInstallProc.Call(
			ugc, uintptr(fileID),
			uintptr(unsafe.Pointer(&sizeOnDisk)),
			uintptr(unsafe.Pointer(&folder[0])), uintptr(len(folder)),
			uintptr(unsafe.Pointer(&stamp)),
		)
		if installedOK != 0 {
			local.Installed = true
			local.InstalledUnix = int64(stamp)
			if n := bytesToNUL(folder); n > 0 {
				local.Folder = string(folder[:n])
			}
		}
		out[id] = local
	}
	return out
}

func bytesToNUL(b []byte) int {
	for i, v := range b {
		if v == 0 {
			return i
		}
	}
	return len(b)
}

type publishedFileDetailsResponse struct {
	Response struct {
		Details []struct {
			ID          string `json:"publishedfileid"`
			Result      int    `json:"result"`
			TimeUpdated int64  `json:"time_updated"`
		} `json:"publishedfiledetails"`
	} `json:"response"`
}

type remotePublishedState struct {
	Result      int
	TimeUpdated int64
}

func fetchWorkshopPublishedTimes(ids []string) map[string]remotePublishedState {
	out := map[string]remotePublishedState{}
	client := &http.Client{Timeout: 10 * time.Second}
	const batchSize = 50
	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		form := url.Values{}
		form.Set("itemcount", strconv.Itoa(end-start))
		for i, id := range ids[start:end] {
			form.Set(fmt.Sprintf("publishedfileids[%d]", i), id)
		}
		req, err := http.NewRequest(http.MethodPost, "https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/", strings.NewReader(form.Encode()))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", "RepublicModLoader/"+launcherVersion)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		var parsed publishedFileDetailsResponse
		decErr := json.NewDecoder(resp.Body).Decode(&parsed)
		resp.Body.Close()
		if decErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			for _, d := range parsed.Response.Details {
				if validWorkshopID(d.ID) {
					out[d.ID] = remotePublishedState{Result: d.Result, TimeUpdated: d.TimeUpdated}
				}
			}
		}
		if end < len(ids) {
			time.Sleep(90 * time.Millisecond)
		}
	}
	return out
}

func scanWorkshopUpdates(gamePath string, ids []string) workshopUpdateCompletion {
	completion := workshopUpdateCompletion{Records: map[string]WorkshopUpdateRecord{}}
	if len(ids) == 0 {
		return completion
	}
	localSteam := steamUGCLocalStates(gamePath, ids)
	acf := readWorkshopACF(gamePath)
	remote := fetchWorkshopPublishedTimes(ids)
	now := time.Now().Unix()

	for _, id := range ids {
		rec := WorkshopUpdateRecord{State: "unknown", Reason: "Could not verify Workshop revision", CheckedUnix: now}
		if local, ok := localSteam[id]; ok {
			rec.SteamState = local.State
			if local.InstalledUnix > 0 {
				rec.InstalledUnix = local.InstalledUnix
			}
		}
		if a, ok := acf[id]; ok {
			rec.InstalledManifest = a.InstalledManifest
			rec.LatestManifest = a.LatestManifest
			if rec.InstalledUnix == 0 {
				rec.InstalledUnix = a.InstalledUnix
			}
			if a.LatestUnix > 0 && a.LatestUnix > rec.RemoteUpdatedUnix {
				rec.RemoteUpdatedUnix = a.LatestUnix
			}
		}
		remoteState, remoteOK := remote[id]
		if remoteOK && remoteState.Result == 1 {
			if remoteState.TimeUpdated > rec.RemoteUpdatedUnix {
				rec.RemoteUpdatedUnix = remoteState.TimeUpdated
			}
		} else if remoteOK && remoteState.Result != 1 {
			rec.State = "unavailable"
			rec.Reason = "Workshop item is no longer retrievable from Steam"
			completion.Records[id] = rec
			completion.Checked++
			completion.Unknown++
			continue
		}

		local := localSteam[id]
		if local.State&(steamItemStateDownloading|steamItemStateDownloadPending) != 0 {
			rec.State = "downloading"
			rec.Reason = "Steam is currently downloading this Workshop item"
		} else if local.State&steamItemStateNeedsUpdate != 0 {
			rec.State = "update"
			rec.Reason = "Steam reports that this Workshop item needs an update"
		} else if !local.Installed && rec.InstalledUnix == 0 {
			rec.State = "missing"
			rec.Reason = "Workshop item is subscribed/tracked but no installed revision was found"
		} else if rec.LatestManifest != "" && rec.InstalledManifest != "" && rec.LatestManifest != "0" && rec.InstalledManifest != "0" && rec.LatestManifest != rec.InstalledManifest {
			rec.State = "stale"
			rec.Reason = "Steam's latest manifest differs from the installed manifest"
		} else if rec.RemoteUpdatedUnix > 0 && rec.InstalledUnix > 0 && rec.RemoteUpdatedUnix > rec.InstalledUnix+2 {
			rec.State = "stale"
			if local.State&steamItemStateNeedsUpdate == 0 {
				rec.Reason = "Workshop server revision is newer than the installed copy, but Steam is not flagging an update"
			} else {
				rec.Reason = "Workshop server revision is newer than the installed copy"
			}
		} else if rec.InstalledUnix > 0 && rec.RemoteUpdatedUnix > 0 {
			rec.State = "current"
			rec.Reason = "Installed Workshop revision matches the latest published update timestamp"
		} else if local.Installed || rec.InstalledUnix > 0 {
			rec.State = "unknown"
			rec.Reason = "Installed locally, but the latest published timestamp could not be verified"
		}

		completion.Records[id] = rec
		completion.Checked++
		switch rec.State {
		case "current":
			completion.Current++
		case "update":
			completion.Update++
		case "stale":
			completion.Stale++
		case "missing":
			completion.Missing++
		default:
			completion.Unknown++
		}
	}
	return completion
}

func forceUpdateWorkshopItem(gamePath, id string) workshopForceUpdateOutcome {
	out := workshopForceUpdateOutcome{}
	if !validWorkshopID(id) {
		out.Message = "Invalid Workshop item ID"
		return out
	}
	fileID, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		out.Message = "Invalid Workshop item ID"
		return out
	}
	steamDLLPath := filepath.Join(filepath.Dir(gamePath), "steam_api64.dll")
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	oldApp, hadApp := os.LookupEnv("SteamAppId")
	oldGame, hadGame := os.LookupEnv("SteamGameId")
	_ = os.Setenv("SteamAppId", wrsrSteamAppID)
	_ = os.Setenv("SteamGameId", wrsrSteamAppID)
	defer func() {
		if hadApp {
			_ = os.Setenv("SteamAppId", oldApp)
		} else {
			_ = os.Unsetenv("SteamAppId")
		}
		if hadGame {
			_ = os.Setenv("SteamGameId", oldGame)
		} else {
			_ = os.Unsetenv("SteamGameId")
		}
	}()

	dll, err := syscall.LoadDLL(steamDLLPath)
	if err != nil {
		out.Message = "Could not load WRSR steam_api64.dll: " + err.Error()
		return out
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
		out.Message = err.Error()
		return out
	}
	shutdownProc, err := find("SteamAPI_Shutdown")
	if err != nil {
		out.Message = err.Error()
		return out
	}
	getUserProc, err := find("SteamAPI_GetHSteamUser")
	if err != nil {
		out.Message = err.Error()
		return out
	}
	findInterfaceProc, err := find("SteamInternal_FindOrCreateUserInterface")
	if err != nil {
		out.Message = err.Error()
		return out
	}
	unsubscribeProc, err := find("SteamAPI_ISteamUGC_UnsubscribeItem")
	if err != nil {
		out.Message = err.Error()
		return out
	}
	subscribeProc, err := find("SteamAPI_ISteamUGC_SubscribeItem")
	if err != nil {
		out.Message = err.Error()
		return out
	}
	downloadProc, err := find("SteamAPI_ISteamUGC_DownloadItem")
	if err != nil {
		out.Message = err.Error()
		return out
	}
	getStateProc, err := find("SteamAPI_ISteamUGC_GetItemState")
	if err != nil {
		out.Message = err.Error()
		return out
	}
	getNumSubscribedProc, err := find("SteamAPI_ISteamUGC_GetNumSubscribedItems")
	if err != nil {
		out.Message = err.Error()
		return out
	}
	getSubscribedProc, err := find("SteamAPI_ISteamUGC_GetSubscribedItems")
	if err != nil {
		out.Message = err.Error()
		return out
	}
	runCallbacksProc, err := find("SteamAPI_RunCallbacks")
	if err != nil {
		out.Message = err.Error()
		return out
	}

	ok, _, _ := initProc.Call()
	if ok == 0 {
		out.Message = "SteamAPI_Init failed. Make sure Steam is running and signed in."
		return out
	}
	defer shutdownProc.Call()
	hUser, _, _ := getUserProc.Call()
	ifaceName := append([]byte("STEAMUGC_INTERFACE_VERSION012"), 0)
	ugc, _, _ := findInterfaceProc.Call(hUser, uintptr(unsafe.Pointer(&ifaceName[0])))
	if ugc == 0 {
		out.Message = "Steam UGC interface version 012 was unavailable"
		return out
	}

	// GetItemState is useful for download flags, but it can lag subscription
	// changes after an asynchronous SubscribeItem/UnsubscribeItem call. For the
	// destructive refresh transaction the authoritative check is the exact list
	// returned by GetSubscribedItems.
	isSubscribed := func() bool {
		countRaw, _, _ := getNumSubscribedProc.Call(ugc)
		count := uint32(countRaw)
		if count == 0 {
			return false
		}
		if count > 100000 {
			count = 100000
		} // defensive bound; real user lists are far smaller
		items := make([]uint64, count)
		gotRaw, _, _ := getSubscribedProc.Call(ugc, uintptr(unsafe.Pointer(&items[0])), uintptr(count))
		got := uint32(gotRaw)
		if got > count {
			got = count
		}
		for i := uint32(0); i < got; i++ {
			if items[i] == fileID {
				return true
			}
		}
		return false
	}
	waitSubscription := func(want bool, d time.Duration) bool {
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			runCallbacksProc.Call()
			if isSubscribed() == want {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		return isSubscribed() == want
	}

	out.Requested = true
	if isSubscribed() {
		handle, _, _ := unsubscribeProc.Call(ugc, uintptr(fileID))
		if handle == 0 {
			out.Message = "Steam rejected the unsubscribe step; nothing was deleted"
			return out
		}
		if !waitSubscription(false, 15*time.Second) {
			out.Message = "Steam did not confirm unsubscribe in its subscribed-item list; local files were left untouched"
			return out
		}
	}
	out.Unsubscribed = true

	if err := deleteWorkshopItemFiles(gamePath, id); err != nil {
		out.FilesDeleteError = err.Error()
		out.Message = "Unsubscribed, but local Workshop files could not be deleted: " + err.Error()
		return out
	}
	out.FilesDeleted = true

	// Subscription changes are asynchronous; do not trust the transient
	// GetItemState subscribed bit and a single five-second attempt; Steam can
	// legitimately lag that state. Retry the actual SubscribeItem transaction and
	// verify against GetSubscribedItems before considering the repair complete.
	const subscribeAttempts = 4
	for attempt := 1; attempt <= subscribeAttempts && !out.Resubscribed; attempt++ {
		handle, _, _ := subscribeProc.Call(ugc, uintptr(fileID))
		if handle != 0 && waitSubscription(true, 15*time.Second) {
			out.Resubscribed = true
			break
		}
		// Give the Steam client a short settle window before reissuing the
		// request. Reissuing SubscribeItem for an already-subscribed item is
		// harmless; the list check above prevents unnecessary retries.
		for i := 0; i < 10; i++ {
			runCallbacksProc.Call()
			time.Sleep(100 * time.Millisecond)
		}
		if isSubscribed() {
			out.Resubscribed = true
			break
		}
	}
	if !out.Resubscribed {
		out.Message = "Local files were removed, but Steam did not confirm the resubscribe after repeated attempts. The item remains unsubscribed; use Open Workshop to recover it manually."
		return out
	}

	// Once subscription is definitely back, keep the Steam API context alive
	// while asking for a high-priority reinstall. A small mod may jump straight
	// to Installed, so Downloading is not required for success.
	for attempt := 0; attempt < 3 && !out.DownloadRequested; attempt++ {
		started, _, _ := downloadProc.Call(ugc, uintptr(fileID), 1)
		out.DownloadRequested = started != 0
		if !out.DownloadRequested {
			for i := 0; i < 5; i++ {
				runCallbacksProc.Call()
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	if out.DownloadRequested {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			runCallbacksProc.Call()
			state, _, _ := getStateProc.Call(ugc, uintptr(fileID))
			flags := uint32(state)
			if flags&(steamItemStateDownloading|steamItemStateDownloadPending) != 0 {
				out.Downloading = true
				break
			}
			if flags&steamItemStateInstalled != 0 {
				// The download may have completed before the launcher observed an
				// intermediate downloading flag.
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		out.Message = "Force update succeeded: Steam confirmed the resubscribe and accepted a high-priority reinstall request."
		if out.Downloading {
			out.Message = "Force update succeeded: Steam confirmed the resubscribe and the fresh Workshop download is active."
		}
	} else {
		out.Message = "Steam confirmed the resubscribe, but did not accept an explicit high-priority download request. The subscription is restored; Steam's normal Workshop downloader should reinstall it."
	}
	// Pump callbacks briefly after the final request before SteamAPI_Shutdown so
	// the client has time to consume the queued UGC transaction.
	for i := 0; i < 20; i++ {
		runCallbacksProc.Call()
		time.Sleep(100 * time.Millisecond)
	}
	return out
}

func formatWorkshopUpdateTime(ts int64) string {
	if ts <= 0 {
		return "Unknown"
	}
	return time.Unix(ts, 0).Local().Format("02 Jan 2006 15:04")
}
