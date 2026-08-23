//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type hitRegion struct {
	ID   string
	X, Y int
	W, H int
}

type launchCompletion struct {
	Result LaunchResult
	Err    error
}

type workshopActionCompletion struct {
	ID      string
	Name    string
	Outcome workshopUnsubscribeOutcome
}

type application struct {
	hwnd          uintptr
	baseDir       string
	configPath    string
	pluginDir     string
	planPath      string
	reportPath    string
	logPath       string
	runtimeDLL    string
	knowledgeRoot string

	cfg                       AppConfig
	plugins                   []*Plugin
	ordered                   []*Plugin
	orderWarnings             []string
	scanWarnings              []string
	game                      *GameIdentity
	gameError                 string
	report                    *RuntimeReport
	logTail                   string
	compatibility             CompatibilitySummary
	compatWarnings            []string
	workshopItems             []WorkshopItem
	workshopWarnings          []string
	selectedWorkshop          int
	developmentItems          []DevelopmentMod
	developmentPlugins        []*Plugin
	developmentWarnings       []string
	developmentPluginWarnings []string
	selectedDevelopment       int
	modView                   int // 0=Plugins, 1=Workshop, 2=Development
	workshopImage             uintptr
	workshopImagePath         string
	pluginImage               uintptr
	pluginImagePath           string
	developmentImage          uintptr
	developmentImagePath      string
	brandImage                uintptr
	pluginScroll              int
	workshopScroll            int
	developmentScroll         int
	pluginSearch              string
	workshopSearch            string
	developmentSearch         string
	modSearchFocus            bool
	workshopSort              string // ""=natural/name ascending, name, id, on
	workshopSortDesc          bool
	modListX, modListY        int
	modListW, modListH        int
	workshopUnsubscribeModal  bool
	workshopUnsubscribeID     string
	workshopUnsubscribeName   string
	workshopActionBusy        bool
	workshopCompletion        workshopActionCompletion
	workshopSupportCache      map[string]WorkshopSupportRecord
	workshopSupportBusy       bool
	workshopSupportCompletion workshopSupportCompletion
	workshopUpdateCache       map[string]WorkshopUpdateRecord
	workshopUpdateBusy        bool
	workshopUpdateCompletion  workshopUpdateCompletion
	workshopForceUpdateModal  bool
	workshopForceUpdateID     string
	workshopForceUpdateName   string
	workshopForceCompletion   workshopForceUpdateCompletion
	workshopMu                sync.Mutex

	modScrollTrackH     int
	modScrollThumbY     int
	modScrollThumbH     int
	modScrollTotal      int
	modScrollVisible    int
	modScrollView       int
	modScrollDrag       bool
	modScrollDragView   int
	modScrollDragStartY int
	modScrollDragOffset int

	pluginDetailScroll      int
	workshopDetailScroll    int
	developmentDetailScroll int
	detailScrollX           int
	detailScrollY           int
	detailScrollW           int
	detailScrollH           int
	detailScrollTotal       int
	detailScrollVisible     int
	detailScrollThumbY      int
	detailScrollThumbH      int
	detailScrollView        int
	detailScrollDrag        bool
	detailScrollDragView    int
	detailScrollDragY       int
	detailScrollDragOff     int

	symbolCache        *SymbolCache
	lastSessionID      string
	closeWhenGameExits bool

	page                int
	selected            int
	hover               string
	hits                []hitRegion
	status              string
	lastLaunchError     string
	busy                bool
	animation           float64
	dpi                 uint32
	scale               float64
	playlistModal       bool
	playlistModalMode   string
	playlistDraft       string
	playlistImportModal bool
	playlistDropdown    bool
	playlistDeleteModal bool
	playlistDeleteName  string
	playlistDropX       int
	playlistDropY       int
	playlistDropW       int
	playlistScroll      int
	mouseX              int
	mouseY              int
	tooltipText         string
	toastText           string
	toastUntil          time.Time
	invalidClickPlugin  int
	invalidClickAt      time.Time

	launchMu            sync.Mutex
	completion          launchCompletion
	deepPreviousProfile LaunchProfile
}

func newApplication(base string) *application {
	app := &application{
		baseDir:             base,
		configPath:          filepath.Join(base, "rml", "config.json"),
		pluginDir:           filepath.Join(base, "rml", "plugins"),
		planPath:            filepath.Join(base, "rml", "state", "runtime_plan.bin"),
		reportPath:          filepath.Join(base, "rml", "state", "last_runtime_report.json"),
		logPath:             filepath.Join(base, "rml", "logs", "rml-runtime.log"),
		runtimeDLL:          filepath.Join(base, "rml_runtime.dll"),
		knowledgeRoot:       persistentKnowledgeRoot(base),
		selected:            0,
		selectedWorkshop:    -1,
		selectedDevelopment: -1,
		invalidClickPlugin:  -1,
		page:                0,
		status:              "Ready",
		dpi:                 96,
		scale:               1,
	}
	// Brand artwork is a runtime asset so the same transparent cog/star is used
	// consistently at every DPI. Failure falls back to the built-in geometric mark.
	app.brandImage = loadGDIPlusImage(filepath.Join(base, "rml", "assets", "brand_logo.png"))
	app.cfg = loadConfig(app.configPath)
	app.workshopSupportCache = loadWorkshopSupportCache(app.knowledgeRoot)
	app.workshopUpdateCache = loadWorkshopUpdateCache(app.knowledgeRoot)
	// The currently selected playlist is authoritative across launcher restarts.
	// Default is a persistent baseline, not a saved playlist.
	app.restoreSelectedPlaylistConfig()
	if app.cfg.GamePath == "" {
		app.cfg.GamePath = discoverGamePath(base)
	}
	app.refresh(false)
	return app
}

func (a *application) refresh(save bool) {
	merged, journalWarnings := mergeSessionJournals(a.baseDir, a.knowledgeRoot)
	if merged > 0 {
		a.status = fmt.Sprintf("Merged %d completed/crash-recovery behaviour journal(s)", merged)
	}
	a.scanWarnings = append(a.scanWarnings[:0], journalWarnings...)
	// Workshop discovery now precedes plugin discovery because a Workshop item may
	// itself be the authoritative source of one or more DLL plugins.
	a.identifyGame()
	a.workshopItems, a.workshopWarnings = discoverWorkshopItems(a.cfg.GamePath, a.cfg.WorkshopSettings)
	a.developmentItems, a.developmentWarnings = discoverDevelopmentMods(a.cfg.GamePath, a.cfg.DevelopmentSettings)
	for i := range a.workshopItems {
		if record, ok := a.workshopSupportCache[a.workshopItems[i].ID]; ok {
			a.workshopItems[i].applySupportRecord(record)
		}
		if record, ok := a.workshopUpdateCache[a.workshopItems[i].ID]; ok {
			a.applyWorkshopUpdateRecord(&a.workshopItems[i], record)
		}
	}
	a.syncDefaultNewWorkshop()
	a.syncDefaultNewDevelopment()
	for i := range a.workshopItems {
		a.workshopItems[i].Enabled = a.cfg.WorkshopSettings[a.workshopItems[i].ID]
	}
	for i := range a.developmentItems {
		a.developmentItems[i].Enabled = a.cfg.DevelopmentSettings[a.developmentItems[i].ID]
	}
	a.plugins, a.scanWarnings = scanPlugins(a.pluginDir, a.workshopItems, &a.cfg)
	a.developmentPlugins, a.developmentPluginWarnings = scanDevelopmentPlugins(a.developmentItems)
	a.scanWarnings = append(a.scanWarnings, journalWarnings...)
	a.scanWarnings = append(a.scanWarnings, a.workshopWarnings...)
	a.scanWarnings = append(a.scanWarnings, a.developmentWarnings...)
	a.scanWarnings = append(a.scanWarnings, a.developmentPluginWarnings...)
	// Published Workshop hybrids remain linked to their Workshop item. Local
	// workshop_wip projects are deliberately independent: their embedded DLLs are
	// kept in a separate runtime-only inventory and never replace/couple a plugin
	// shown in Plugins or Workshop.
	a.rebuildWorkshopPluginLinks()
	a.rebuildDevelopmentPluginInventory()
	a.syncDevelopmentPluginEnableStates()
	// Newly discovered plugins become part of Default with their current/default
	// settings, without disturbing an existing named playlist.
	a.syncDefaultNewPlugins()
	if a.selectedWorkshop >= len(a.workshopItems) {
		a.selectedWorkshop = len(a.workshopItems) - 1
	}
	if a.selectedWorkshop < 0 && len(a.workshopItems) > 0 {
		a.selectedWorkshop = 0
	}
	if a.selectedDevelopment >= len(a.developmentItems) {
		a.selectedDevelopment = len(a.developmentItems) - 1
	}
	if a.selectedDevelopment < 0 && len(a.developmentItems) > 0 {
		a.selectedDevelopment = 0
	}
	a.refreshDiagnostics()
	a.refreshSymbolCache()
	validatePlugins(a.plugins, a.game, a.cfg.AllowProbable)
	a.ordered, a.orderWarnings = calculateOrder(a.plugins, a.cfg.AutoOrder)
	a.compatibility, a.compatWarnings = analyseCompatibility(a.baseDir, a.knowledgeRoot, a.pluginDir, a.plugins, a.symbolCache, a.report)
	a.scanWarnings = append(a.scanWarnings, a.compatWarnings...)
	if a.selected >= len(a.plugins) {
		a.selected = len(a.plugins) - 1
	}
	if a.selected < 0 && len(a.plugins) > 0 {
		a.selected = 0
	}
	a.normalizeModSelections()
	if save {
		_ = saveConfig(a.configPath, a.cfg)
	}
}

func (a *application) refreshSymbolCache() {
	a.symbolCache = nil
	if a.game == nil {
		return
	}
	cache, _, err := loadSymbolCache(a.knowledgeRoot, a.game)
	if err == nil {
		a.symbolCache = cache
	}
}

func (a *application) identifyGame() {
	a.game = nil
	a.gameError = ""
	if a.cfg.GamePath == "" {
		return
	}
	id, err := hashGame(a.cfg.GamePath)
	if err != nil {
		a.gameError = err.Error()
		return
	}
	a.game = &id
}

func (a *application) refreshDiagnostics() {
	a.report = nil
	if report, err := loadRuntimeReport(a.reportPath); err == nil {
		a.report = report
		if a.game != nil {
			_ = mergeRuntimeReportIntoSymbolCache(a.knowledgeRoot, a.game, report)
		}
	}
	a.logTail = "No runtime log has been produced yet."
	if data, err := os.ReadFile(a.logPath); err == nil {
		if len(data) > 24000 {
			data = data[len(data)-24000:]
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		if len(lines) > 18 {
			lines = lines[len(lines)-18:]
		}
		a.logTail = strings.Join(lines, "\n")
	}
}

func (a *application) savePlugin(p *Plugin) {
	a.cfg.PluginSettings[p.Manifest.ID] = p.Setting
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
		if a.cfg.DefaultPluginSettings == nil {
			a.cfg.DefaultPluginSettings = map[string]PluginSetting{}
		}
		a.cfg.DefaultPluginSettings[p.Manifest.ID] = p.Setting
		a.cfg.DefaultAutoOrder = a.cfg.AutoOrder
	}
	_ = saveConfig(a.configPath, a.cfg)
	validatePlugins(a.plugins, a.game, a.cfg.AllowProbable)
	a.ordered, a.orderWarnings = calculateOrder(a.plugins, a.cfg.AutoOrder)
}

func (a *application) selectedPlugin() *Plugin {
	if a.selected < 0 || a.selected >= len(a.plugins) {
		return nil
	}
	return a.plugins[a.selected]
}

func (a *application) showToast(text string, duration time.Duration) {
	if strings.TrimSpace(text) == "" {
		return
	}
	a.toastText = text
	a.toastUntil = time.Now().Add(duration)
}

func (a *application) selectedWorkshopItem() *WorkshopItem {
	if a.selectedWorkshop < 0 || a.selectedWorkshop >= len(a.workshopItems) {
		return nil
	}
	return &a.workshopItems[a.selectedWorkshop]
}

func (a *application) workshopItemByID(id string) *WorkshopItem {
	if !validWorkshopID(id) {
		return nil
	}
	for i := range a.workshopItems {
		if a.workshopItems[i].ID == id {
			return &a.workshopItems[i]
		}
	}
	return nil
}

func (a *application) saveWorkshop(item *WorkshopItem) {
	if item == nil {
		return
	}
	if a.cfg.WorkshopSettings == nil {
		a.cfg.WorkshopSettings = map[string]bool{}
	}
	a.cfg.WorkshopSettings[item.ID] = item.Enabled
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
		if a.cfg.DefaultWorkshopSettings == nil {
			a.cfg.DefaultWorkshopSettings = map[string]bool{}
		}
		a.cfg.DefaultWorkshopSettings[item.ID] = item.Enabled
	}
	_ = saveConfig(a.configPath, a.cfg)
}

func (a *application) combinedLaunchPlugins() []*Plugin {
	out := make([]*Plugin, 0, len(a.plugins)+len(a.developmentPlugins))
	out = append(out, a.plugins...)
	out = append(out, a.developmentPlugins...)
	return out
}

func (a *application) enabledCount() int {
	n := 0
	for _, p := range a.plugins {
		if p.Setting.Enabled {
			n++
		}
	}
	return n
}

func (a *application) canLaunch() (bool, string) {
	if a.busy {
		return false, "Launch already in progress"
	}
	if err := selectedGameError(a.game); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func (a *application) startLaunch() {
	ok, reason := a.canLaunch()
	if !ok {
		a.status = "Cannot launch: " + reason
		invalidateWindow(a.hwnd)
		return
	}

	a.lastLaunchError = ""
	logPath := filepath.Join(a.baseDir, "rml", "logs", "rml-runtime.log")
	resetLauncherLog(logPath)
	_ = os.Remove(a.reportPath)

	profile := launchProfileByID(normalizeLaunchProfile(a.cfg.LaunchProfile))
	a.cfg.LaunchProfile = string(profile.ID)
	a.lastSessionID = newSessionID()
	setLauncherDiagnosticMirror("")
	if profile.ID == profileDeepTest {
		diag := diagnosticLogPath(a.baseDir, profile.ID, a.lastSessionID)
		_ = os.MkdirAll(filepath.Dir(diag), 0o755)
		_ = os.WriteFile(diag, nil, 0o644)
		setLauncherDiagnosticMirror(diag)
	}
	appendLauncherLog(logPath, "launch profile: %s (%s); session=%s", profile.Name, profile.ID, a.lastSessionID)

	launchPlugins := a.combinedLaunchPlugins()
	validatePlugins(launchPlugins, a.game, a.cfg.AllowProbable)
	for _, p := range launchPlugins {
		switch {
		case !p.BasicValid:
			reason := "basic PE validation failed"
			if len(p.PEValidation.Failures) > 0 {
				reason = strings.Join(p.PEValidation.Failures, "; ")
			}
			appendLauncherLog(logPath, "[PLUGIN INVALID] id=%s path=%s reason=%s", p.Manifest.ID, p.DLLPath, reason)
		case !p.Setting.Enabled:
			appendLauncherLog(logPath, "[PLUGIN DISABLED] id=%s path=%s", p.Manifest.ID, p.DLLPath)
		default:
			appendLauncherLog(logPath, "[PLUGIN QUEUED] id=%s loader=%s sha256=%s path=%s", p.Manifest.ID, p.LoaderKind, p.DLLSHA256, p.DLLPath)
		}
	}
	launchOrdered, launchWarnings := calculateOrder(launchPlugins, a.cfg.AutoOrder)
	for _, warning := range launchWarnings {
		appendLauncherLog(logPath, "plugin ordering warning (launch will continue with safe subset): %s", warning)
	}
	appendLauncherLog(logPath, "Workshop inventory: installed=%d enabled=%d; Normal profile uses an explicit enabled-ID allow-list", len(a.workshopItems), a.enabledWorkshopCount())
	appendLauncherLog(logPath, "Development inventory: local=%d enabled=%d root=%s", len(a.developmentItems), a.enabledDevelopmentCount(), developmentRootFromGamePath(a.cfg.GamePath))
	hybridCount := 0
	for _, p := range a.plugins {
		if p.WorkshopID == "" {
			continue
		}
		hybridCount++
		appendLauncherLog(logPath, "Workshop hybrid plugin: %s <- Workshop %s (%s)", p.Manifest.ID, p.WorkshopID, p.DLLPath)
	}
	if hybridCount > 0 {
		appendLauncherLog(logPath, "Workshop hybrid plugin links discovered: %d", hybridCount)
	}
	developmentDLLCount := 0
	for _, p := range a.developmentPlugins {
		developmentDLLCount++
		if p.Setting.Enabled {
			appendLauncherLog(logPath, "Development DLL payload: %s <- WIP %s (%s)", p.Manifest.ID, p.DevelopmentID, p.DLLPath)
		}
	}
	if developmentDLLCount > 0 {
		appendLauncherLog(logPath, "Development DLL payloads discovered: %d (independent of Plugins/Workshop)", developmentDLLCount)
	}
	if profile.ID == profileDeepTest {
		appendLauncherLog(logPath, "DIAGNOSTIC TRACE: enabled plugin/load-order snapshot BEGIN")
		for i, p := range launchOrdered {
			appendLauncherLog(logPath, "DIAGNOSTIC plugin[%02d] id=%s name=%q version=%s sha256=%s loader=%s legacyAPI=%d priority=%d workshop=%s development=%s path=%s", i, p.Manifest.ID, p.Manifest.Name, p.Manifest.Version, p.DLLSHA256, p.LoaderKind, p.LegacyAPI, p.Setting.Priority, p.WorkshopID, p.DevelopmentID, p.DLLPath)
			if profile.ID == profileDeepTest {
				appendLauncherLog(logPath, "DEEP TRACE plugin[%02d] force=%t status=%q statusClass=%d required=%v optional=%v dependencies=%v conflicts=%v loadBefore=%v loadAfter=%v", i, p.Setting.Force, p.Status, p.StatusClass, p.Manifest.RequiredSymbols, p.Manifest.OptionalSymbols, p.Manifest.Dependencies, p.Manifest.Conflicts, p.Manifest.LoadBefore, p.Manifest.LoadAfter)
				for _, reason := range p.Reasons {
					appendLauncherLog(logPath, "DEEP TRACE plugin[%02d] validation=%s", i, reason)
				}
			}
		}
		if a.symbolCache != nil {
			appendLauncherLog(logPath, "DIAGNOSTIC Symbol Cache schema=%d resolver=%d symbols=%d", a.symbolCache.SchemaVersion, a.symbolCache.ResolverEngine, len(a.symbolCache.Symbols))
			for _, sym := range a.symbolCache.Symbols {
				appendLauncherLog(logPath, "DIAGNOSTIC symbol id=%s rva=0x%X confidence=%s source=%s size=%d", sym.ID, sym.RVA, sym.Confidence, sym.Resolver, sym.FunctionSize)
			}
		}
		appendLauncherLog(logPath, "DIAGNOSTIC Workshop enabled=%d installed=%d", a.enabledWorkshopCount(), len(a.workshopItems))
		appendLauncherLog(logPath, "DIAGNOSTIC Development enabled=%d local=%d", a.enabledDevelopmentCount(), len(a.developmentItems))
		if profile.ID == profileDeepTest {
			appendLauncherLog(logPath, "DEEP TRACE pre-launch compatibility: detectedDLLs=%d enabledDLLs=%d sharedTargets=%d potentialConflicts=%d bridgeCandidates=%d overall=%s", a.compatibility.DLLCount, a.compatibility.SelectedDLLs, a.compatibility.SharedTargets, a.compatibility.PotentialConflicts, a.compatibility.BridgeCandidates, a.compatibility.Overall)
			for _, target := range a.compatibility.Targets {
				if len(target.Interactions) == 0 {
					continue
				}
				appendLauncherLog(logPath, "DEEP TRACE target=%s result=%s temperature=%s reason=%s interactions=%d", target.Symbol, target.Result, target.Temperature, target.Reason, len(target.Interactions))
				for _, interaction := range target.Interactions {
					appendLauncherLog(logPath, "DEEP TRACE target=%s plugin=%s name=%q loader=%s action=%s phase=%s evidence=%s managed=%t", target.Symbol, interaction.PluginID, interaction.PluginName, interaction.LoaderType, interaction.Action, interaction.Phase, interaction.Evidence, interaction.Managed)
				}
			}
		}
		appendLauncherLog(logPath, "DIAGNOSTIC TRACE: enabled plugin/load-order snapshot END")
	}

	planErr := writeRuntimePlan(a.planPath, a.cfg, *a.game, launchOrdered, profile, a.lastSessionID, a.symbolCache, a.workshopItems)
	_ = saveConfig(a.configPath, a.cfg)
	a.busy = true
	a.status = "Launching WRSR — " + profile.Name + "…"
	invalidateWindow(a.hwnd)
	gamePath := a.game.Path
	runtime := a.runtimeDLL
	root := a.baseDir
	hwnd := a.hwnd
	sessionID := a.lastSessionID

	go func() {
		var result LaunchResult
		var err error
		if planErr != nil {
			reason := "runtime plan could not be written: " + planErr.Error()
			appendLauncherLog(logPath, "%s; launch aborted before WRSR was started", reason)
			err = errors.New(reason)
		} else {
			result, err = launchAndInject(gamePath, runtime, root, LaunchOptions{Profile: profile.ID, SessionID: sessionID})
		}
		a.launchMu.Lock()
		a.completion = launchCompletion{Result: result, Err: err}
		a.launchMu.Unlock()
		postAppMessage(hwnd, wmRMLLaunchDone, 0, 0)
	}()
}

func (a *application) finishLaunch() {
	a.launchMu.Lock()
	completion := a.completion
	a.launchMu.Unlock()
	a.busy = false
	profile := launchProfileByID(normalizeLaunchProfile(a.cfg.LaunchProfile))

	// Refresh first on failures because the terminated trial runtime normally wrote
	// a final report containing the plugin that forced the abort. Prefer that
	// concrete plugin status over an opaque bootstrap status code on Dashboard.
	a.refreshDiagnostics()
	if completion.Err != nil {
		detail := completion.Err.Error()
		if a.report != nil {
			for _, p := range a.report.Plugins {
				upper := strings.ToUpper(p.Status)
				if !p.Loaded && p.LegacyHooks > 0 && (strings.Contains(upper, "FAILED") || strings.Contains(upper, "EXCEPTION")) {
					name := p.Name
					if strings.TrimSpace(name) == "" {
						name = p.ID
					}
					detail = name + " — " + p.Status
					break
				}
			}
		}
		a.lastLaunchError = detail
		a.status = "Error loading — " + detail
		a.page = 0
		_ = writeHighLevelSessionLog(a.baseDir, 0xFFFFFFFF, string(profile.ID), 0)
		if a.cfg.AutoCrashArchives {
			if p, err := createSupportArchive(a.baseDir, "RML bootstrap/launch failure: "+detail); err == nil {
				a.status += " — crash archive: " + filepath.Base(p)
			}
		}
		if profile.ID == profileDeepTest {
			if a.lastSessionID != "" {
				diag := diagnosticLogPath(a.baseDir, profileDeepTest, a.lastSessionID)
				appendDiagnosticFileSection(diag, "RUNTIME REPORT JSON", filepath.Join(a.baseDir, "rml", "state", "last_runtime_report.json"))
				appendDiagnosticFileSection(diag, "LAUNCHER CONFIG JSON", filepath.Join(a.baseDir, "rml", "config.json"))
			}
			setLauncherDiagnosticMirror("")
			if a.deepPreviousProfile != "" {
				a.cfg.LaunchProfile = string(a.deepPreviousProfile)
				a.deepPreviousProfile = ""
				_ = saveConfig(a.configPath, a.cfg)
			}
			a.status = "Detailed diagnostic bootstrap failed — detailed trace finalized: " + detail
		}
	} else if profile.ID == profileDeepTest {
		a.lastLaunchError = ""
		a.status = fmt.Sprintf("Detailed diagnostic session active (PID %d)", completion.Result.ProcessID)
	} else if completion.Result.BootstrapStatus != 0 {
		a.lastLaunchError = ""
		a.status = fmt.Sprintf("WRSR launched with degraded RML status 0x%X — %s", completion.Result.BootstrapStatus, profile.Name)
	} else {
		a.lastLaunchError = ""
		a.status = fmt.Sprintf("WRSR + RML launched successfully (PID %d) — %s", completion.Result.ProcessID, profile.Name)
	}
	a.refreshSymbolCache()
	invalidateWindow(a.hwnd)
	if completion.Err == nil && completion.Result.ProcessHandle != 0 {
		go monitorLaunchedGame(completion.Result, a.baseDir, a.hwnd)
	}
	a.closeWhenGameExits = completion.Err == nil && a.cfg.CloseAfterLaunch
	if completion.Err == nil && (a.cfg.AutoHideOnLaunch || a.closeWhenGameExits) {
		showWindow(a.hwnd, swHide)
	}
}

func (a *application) chooseGame() {
	if path := openGameDialog(a.hwnd, a.cfg.GamePath); path != "" {
		a.cfg.GamePath = path
		a.status = "Checking SOVIET64.exe…"
		a.refresh(true)
		if a.game != nil {
			if a.game.Known {
				a.status = "Recognised WRSR " + a.game.Version
			} else {
				a.status = "Untested WRSR executable selected"
			}
		} else if a.gameError != "" {
			a.status = "Game validation failed: " + a.gameError
		}
		invalidateWindow(a.hwnd)
	}
}

func (a *application) resetRecommended() {
	for _, p := range a.plugins {
		p.Setting.Enabled = p.Manifest.EnabledByDefault
		p.Setting.Force = false
		p.Setting.Priority = p.Manifest.DefaultPriority
		a.cfg.PluginSettings[p.Manifest.ID] = p.Setting
	}
	a.cfg.AutoOrder = true
	a.cfg.SelectedPlaylist = defaultPlaylistName
	a.captureDefaultConfiguration()
	a.refresh(true)
	a.status = "Recommended Default plugin configuration restored"
}

func (a *application) requestWorkshopUnsubscribeByID(id, fallbackName string) {
	if !validWorkshopID(id) || a.workshopActionBusy {
		return
	}
	name := strings.TrimSpace(fallbackName)
	if item := a.workshopItemByID(id); item != nil && strings.TrimSpace(item.Name) != "" {
		name = item.Name
	}
	if name == "" {
		name = "Workshop Item " + id
	}
	a.workshopUnsubscribeID = id
	a.workshopUnsubscribeName = name
	a.workshopUnsubscribeModal = true
	a.modSearchFocus = false
	a.status = "Confirm Workshop unsubscribe"
	invalidateWindow(a.hwnd)
}

func (a *application) requestWorkshopUnsubscribe() {
	item := a.selectedWorkshopItem()
	if item == nil {
		return
	}
	a.requestWorkshopUnsubscribeByID(item.ID, item.Name)
}

func (a *application) cancelWorkshopUnsubscribe() {
	a.workshopUnsubscribeModal = false
	a.workshopUnsubscribeID = ""
	a.workshopUnsubscribeName = ""
	a.status = "Workshop unsubscribe cancelled"
	invalidateWindow(a.hwnd)
}

func (a *application) beginWorkshopUnsubscribe() {
	if a.workshopActionBusy || !validWorkshopID(a.workshopUnsubscribeID) {
		return
	}
	id, name, gamePath, hwnd := a.workshopUnsubscribeID, a.workshopUnsubscribeName, a.cfg.GamePath, a.hwnd
	a.workshopUnsubscribeModal = false
	a.workshopActionBusy = true
	a.status = "Requesting Steam Workshop unsubscribe for " + name + "…"
	invalidateWindow(a.hwnd)
	go func() {
		outcome := unsubscribeWorkshopItem(gamePath, id)
		if outcome.Requested {
			if err := deleteWorkshopItemFiles(gamePath, id); err != nil {
				outcome.FilesDeleteError = err.Error()
			} else {
				outcome.FilesDeleted = true
			}
		}
		a.workshopMu.Lock()
		a.workshopCompletion = workshopActionCompletion{ID: id, Name: name, Outcome: outcome}
		a.workshopMu.Unlock()
		postAppMessage(hwnd, wmRMLWorkshopUnsubDone, 0, 0)
	}()
}

func (a *application) removeWorkshopFromConfigurations(id string) {
	delete(a.cfg.WorkshopSettings, id)
	delete(a.cfg.DefaultWorkshopSettings, id)
	for i := range a.cfg.Playlists {
		out := a.cfg.Playlists[i].Workshop[:0]
		for _, savedID := range a.cfg.Playlists[i].Workshop {
			if savedID != id {
				out = append(out, savedID)
			}
		}
		a.cfg.Playlists[i].Workshop = out
	}
}

func (a *application) finishWorkshopUnsubscribe() {
	a.workshopMu.Lock()
	completion := a.workshopCompletion
	a.workshopMu.Unlock()
	a.workshopActionBusy = false
	a.workshopUnsubscribeID = ""
	a.workshopUnsubscribeName = ""

	removedFromList := false
	if completion.Outcome.Requested {
		for i := range a.workshopItems {
			if a.workshopItems[i].ID != completion.ID {
				continue
			}
			a.workshopItems[i].Enabled = false
			a.workshopItems[i].Unsubscribed = completion.Outcome.Confirmed
			a.saveWorkshop(&a.workshopItems[i])
			if completion.Outcome.FilesDeleted {
				if a.selectedWorkshop == i {
					a.releaseWorkshopImage()
				}
				a.workshopItems = append(a.workshopItems[:i], a.workshopItems[i+1:]...)
				removedFromList = true
			}
			break
		}
		if completion.Outcome.FilesDeleted {
			a.removeWorkshopFromConfigurations(completion.ID)
			delete(a.workshopSupportCache, completion.ID)
			delete(a.workshopUpdateCache, completion.ID)
			saveWorkshopSupportCache(a.knowledgeRoot, a.workshopSupportCache)
			saveWorkshopUpdateCache(a.knowledgeRoot, a.workshopUpdateCache)
		}
	}
	if removedFromList {
		if a.selectedWorkshop >= len(a.workshopItems) {
			a.selectedWorkshop = len(a.workshopItems) - 1
		}
	}
	_ = saveConfig(a.configPath, a.cfg)
	// If the Workshop package supplied one or more plugin DLLs, deleting the
	// package should remove those plugin rows immediately rather than leaving a
	// stale Workshop-sourced DLL visible until the user manually presses Refresh.
	if completion.Outcome.FilesDeleted {
		a.refresh(true)
	}
	if completion.Outcome.Message != "" {
		a.status = completion.Name + ": " + completion.Outcome.Message
	} else {
		a.status = completion.Name + ": Workshop unsubscribe finished"
	}
	if completion.Outcome.FilesDeleteError != "" {
		a.status += "; local files could not be deleted: " + completion.Outcome.FilesDeleteError
	} else if completion.Outcome.FilesDeleted {
		a.status += "; local Workshop files deleted"
	}
	a.normalizeModSelections()
	invalidateWindow(a.hwnd)
}

func (a *application) startWorkshopSupportScan(force bool) {
	if a.workshopSupportBusy || a.hwnd == 0 || len(a.workshopItems) == 0 {
		return
	}
	now := time.Now()
	ids := make([]string, 0, len(a.workshopItems))
	for _, item := range a.workshopItems {
		record, ok := a.workshopSupportCache[item.ID]
		if force || !ok || !workshopSupportRecordFresh(record, now) {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	a.workshopSupportBusy = true
	a.status = fmt.Sprintf("Checking Workshop support status for %d item(s)…", len(ids))
	hwnd := a.hwnd
	go func() {
		completion := scanWorkshopSupport(ids)
		a.workshopMu.Lock()
		a.workshopSupportCompletion = completion
		a.workshopMu.Unlock()
		postAppMessage(hwnd, wmRMLWorkshopSupportDone, 0, 0)
	}()
}

func (a *application) finishWorkshopSupportScan() {
	a.workshopMu.Lock()
	completion := a.workshopSupportCompletion
	a.workshopMu.Unlock()
	a.workshopSupportBusy = false
	if a.workshopSupportCache == nil {
		a.workshopSupportCache = map[string]WorkshopSupportRecord{}
	}
	for id, record := range completion.Records {
		a.workshopSupportCache[id] = record
	}
	saveWorkshopSupportCache(a.knowledgeRoot, a.workshopSupportCache)
	needsAttention := 0
	for i := range a.workshopItems {
		if record, ok := a.workshopSupportCache[a.workshopItems[i].ID]; ok {
			a.workshopItems[i].applySupportRecord(record)
		}
		if a.workshopItems[i].Unsupported {
			needsAttention++
		}
	}
	a.normalizeModSelections()
	a.status = fmt.Sprintf("Workshop support check complete: %d item(s) checked, %d need attention", completion.Checked, needsAttention)
	invalidateWindow(a.hwnd)
}

func (a *application) applyWorkshopUpdateRecord(item *WorkshopItem, record WorkshopUpdateRecord) {
	if item == nil {
		return
	}
	item.UpdateState = record.State
	item.UpdateReason = record.Reason
	item.UpdateCheckedUnix = record.CheckedUnix
	item.InstalledUpdatedUnix = record.InstalledUnix
	item.RemoteUpdatedUnix = record.RemoteUpdatedUnix
	item.InstalledManifest = record.InstalledManifest
	item.LatestManifest = record.LatestManifest
	item.SteamItemState = record.SteamState
}

func (a *application) startWorkshopUpdateScan() {
	if a.workshopUpdateBusy || a.workshopActionBusy || a.hwnd == 0 || len(a.workshopItems) == 0 {
		return
	}
	ids := make([]string, 0, len(a.workshopItems))
	for _, item := range a.workshopItems {
		if validWorkshopID(item.ID) {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	a.workshopUpdateBusy = true
	a.status = fmt.Sprintf("Scanning %d Workshop item(s) for newer published revisions…", len(ids))
	hwnd, gamePath := a.hwnd, a.cfg.GamePath
	go func() {
		completion := scanWorkshopUpdates(gamePath, ids)
		a.workshopMu.Lock()
		a.workshopUpdateCompletion = completion
		a.workshopMu.Unlock()
		postAppMessage(hwnd, wmRMLWorkshopUpdateDone, 0, 0)
	}()
}

func (a *application) finishWorkshopUpdateScan() {
	a.workshopMu.Lock()
	completion := a.workshopUpdateCompletion
	a.workshopMu.Unlock()
	a.workshopUpdateBusy = false
	if a.workshopUpdateCache == nil {
		a.workshopUpdateCache = map[string]WorkshopUpdateRecord{}
	}
	for id, record := range completion.Records {
		a.workshopUpdateCache[id] = record
	}
	saveWorkshopUpdateCache(a.knowledgeRoot, a.workshopUpdateCache)
	for i := range a.workshopItems {
		if record, ok := a.workshopUpdateCache[a.workshopItems[i].ID]; ok {
			a.applyWorkshopUpdateRecord(&a.workshopItems[i], record)
		}
	}
	a.normalizeModSelections()
	a.status = fmt.Sprintf("Workshop update scan complete: %d checked • %d current • %d update available • %d potentially stale • %d missing/unknown", completion.Checked, completion.Current, completion.Update, completion.Stale, completion.Missing+completion.Unknown)
	invalidateWindow(a.hwnd)
}

func (a *application) requestWorkshopForceUpdateByID(id, fallbackName string) {
	if !validWorkshopID(id) || a.workshopActionBusy || a.workshopUpdateBusy {
		return
	}
	name := strings.TrimSpace(fallbackName)
	if item := a.workshopItemByID(id); item != nil && strings.TrimSpace(item.Name) != "" {
		name = item.Name
	}
	if name == "" {
		name = "Workshop Item " + id
	}
	a.workshopForceUpdateID = id
	a.workshopForceUpdateName = name
	a.workshopForceUpdateModal = true
	a.modSearchFocus = false
	a.status = "Confirm Workshop force update"
	invalidateWindow(a.hwnd)
}

func (a *application) requestWorkshopForceUpdate() {
	item := a.selectedWorkshopItem()
	if item == nil {
		return
	}
	a.requestWorkshopForceUpdateByID(item.ID, item.Name)
}

func (a *application) cancelWorkshopForceUpdate() {
	a.workshopForceUpdateModal = false
	a.workshopForceUpdateID = ""
	a.workshopForceUpdateName = ""
	a.status = "Workshop force update cancelled"
	invalidateWindow(a.hwnd)
}

func (a *application) beginWorkshopForceUpdate() {
	if a.workshopActionBusy || a.workshopUpdateBusy || !validWorkshopID(a.workshopForceUpdateID) {
		return
	}
	id, name, gamePath, hwnd := a.workshopForceUpdateID, a.workshopForceUpdateName, a.cfg.GamePath, a.hwnd
	a.workshopForceUpdateModal = false
	a.workshopActionBusy = true
	a.status = "Force-updating Steam Workshop item " + name + "…"
	appendLauncherLog(a.logPath, "Workshop force update BEGIN: %s (%s)", name, id)
	invalidateWindow(a.hwnd)
	go func() {
		outcome := forceUpdateWorkshopItem(gamePath, id)
		a.workshopMu.Lock()
		a.workshopForceCompletion = workshopForceUpdateCompletion{ID: id, Name: name, Outcome: outcome}
		a.workshopMu.Unlock()
		postAppMessage(hwnd, wmRMLWorkshopForceUpdateDone, 0, 0)
	}()
}

func (a *application) finishWorkshopForceUpdate() {
	a.workshopMu.Lock()
	completion := a.workshopForceCompletion
	a.workshopMu.Unlock()
	a.workshopActionBusy = false
	a.workshopForceUpdateID = ""
	a.workshopForceUpdateName = ""

	if completion.Outcome.Resubscribed {
		// Force-update is maintenance, not a configuration change. Preserve the
		// user's RML Workshop/playlist enable state even though the local package
		// was deleted and resubscribed underneath it.
		if a.workshopUpdateCache == nil {
			a.workshopUpdateCache = map[string]WorkshopUpdateRecord{}
		}
		state := "unknown"
		reason := "Steam reinstall requested; rescan after the download completes"
		if completion.Outcome.Downloading || completion.Outcome.DownloadRequested {
			state = "downloading"
			reason = "Steam reinstall is queued/downloading after a forced resubscribe"
		}
		a.workshopUpdateCache[completion.ID] = WorkshopUpdateRecord{State: state, Reason: reason, CheckedUnix: time.Now().Unix()}
		saveWorkshopUpdateCache(a.knowledgeRoot, a.workshopUpdateCache)
		for i := range a.workshopItems {
			if a.workshopItems[i].ID == completion.ID {
				a.applyWorkshopUpdateRecord(&a.workshopItems[i], a.workshopUpdateCache[completion.ID])
			}
		}
	}
	appendLauncherLog(a.logPath, "Workshop force update END: %s (%s) unsubscribed=%t filesDeleted=%t resubscribed=%t downloadRequested=%t downloading=%t message=%s", completion.Name, completion.ID, completion.Outcome.Unsubscribed, completion.Outcome.FilesDeleted, completion.Outcome.Resubscribed, completion.Outcome.DownloadRequested, completion.Outcome.Downloading, completion.Outcome.Message)
	if completion.Outcome.Message != "" {
		a.status = completion.Name + ": " + completion.Outcome.Message
	} else {
		a.status = completion.Name + ": Workshop force update finished"
	}
	invalidateWindow(a.hwnd)
}

func (a *application) beginModScrollDrag(view, mouseY int) {
	if a.modScrollTotal <= a.modScrollVisible || a.modScrollTrackH <= a.modScrollThumbH {
		return
	}
	a.modScrollDrag = true
	a.modScrollDragView = view
	a.modScrollDragStartY = mouseY
	if view == 1 {
		a.modScrollDragOffset = a.workshopScroll
	} else if view == 2 {
		a.modScrollDragOffset = a.developmentScroll
	} else {
		a.modScrollDragOffset = a.pluginScroll
	}
}

func (a *application) updateModScrollDrag(mouseY int) {
	if !a.modScrollDrag || a.modScrollView != a.modScrollDragView {
		return
	}
	travel := a.modScrollTrackH - a.modScrollThumbH
	maxOffset := a.modScrollTotal - a.modScrollVisible
	if travel <= 0 || maxOffset <= 0 {
		return
	}
	delta := mouseY - a.modScrollDragStartY
	offset := a.modScrollDragOffset + int(float64(delta)*float64(maxOffset)/float64(travel)+0.5)
	offset = clampListScroll(offset, a.modScrollTotal, a.modScrollVisible)
	if a.modScrollDragView == 1 {
		a.workshopScroll = offset
	} else if a.modScrollDragView == 2 {
		a.developmentScroll = offset
	} else {
		a.pluginScroll = offset
	}
}

func (a *application) endModScrollDrag() {
	a.modScrollDrag = false
}

func (a *application) handleAction(id string) {
	if strings.HasPrefix(id, "nav:") {
		var page int
		_, _ = fmt.Sscanf(id, "nav:%d", &page)
		a.page = page
		a.playlistDropdown = false
		a.status = "Ready"
		invalidateWindow(a.hwnd)
		return
	}
	if strings.HasPrefix(id, "plugin-row:") {
		var index int
		_, _ = fmt.Sscanf(id, "plugin-row:%d", &index)
		if index >= 0 && index < len(a.plugins) {
			if a.selected != index {
				a.pluginDetailScroll = 0
				a.releasePluginImage()
			}
			a.selected = index
		}
		invalidateWindow(a.hwnd)
		return
	}
	if strings.HasPrefix(id, "playlist-option:") {
		var index int
		_, _ = fmt.Sscanf(id, "playlist-option:%d", &index)
		a.selectPlaylistOption(index)
		invalidateWindow(a.hwnd)
		return
	}
	if strings.HasPrefix(id, "profile:") {
		profileID := strings.TrimPrefix(id, "profile:")
		profile := launchProfileByID(normalizeLaunchProfile(profileID))
		a.cfg.LaunchProfile = string(profile.ID)
		_ = saveConfig(a.configPath, a.cfg)
		a.status = "Launch profile selected: " + profile.Name
		invalidateWindow(a.hwnd)
		return
	}
	if strings.HasPrefix(id, "mod-view:") {
		view := strings.TrimPrefix(id, "mod-view:")
		switch view {
		case "workshop":
			a.modView = 1
		case "development":
			a.modView = 2
		default:
			a.modView = 0
		}
		a.playlistDropdown = false
		a.modSearchFocus = false
		a.normalizeModSelections()
		switch a.modView {
		case 1:
			a.status = "Workshop mods"
		case 2:
			a.status = "Development mods from media_soviet\\workshop_wip"
		default:
			a.status = "Plugins"
		}
		if (a.modView == 1 && a.cfg.WorkshopNeedsAttentionOnly) || (a.modView == 0 && a.cfg.PluginNeedsAttentionOnly) {
			a.startWorkshopSupportScan(false)
		}
		invalidateWindow(a.hwnd)
		return
	}
	if strings.HasPrefix(id, "workshop-row:") {
		var index int
		_, _ = fmt.Sscanf(id, "workshop-row:%d", &index)
		if index >= 0 && index < len(a.workshopItems) {
			if a.selectedWorkshop != index {
				a.workshopDetailScroll = 0
			}
			a.selectedWorkshop = index
			a.releaseWorkshopImage()
		}
		invalidateWindow(a.hwnd)
		return
	}
	if strings.HasPrefix(id, "workshop-toggle:") {
		var index int
		_, _ = fmt.Sscanf(id, "workshop-toggle:%d", &index)
		if index >= 0 && index < len(a.workshopItems) {
			item := &a.workshopItems[index]
			next := !item.Enabled
			a.setHybridWorkshopEnabled(item.ID, next)
			a.status = item.Name + " " + map[bool]string{true: "enabled with linked Workshop plugin(s)", false: "disabled with linked Workshop plugin(s)"}[next]
		}
		invalidateWindow(a.hwnd)
		return
	}
	if strings.HasPrefix(id, "development-row:") {
		var index int
		_, _ = fmt.Sscanf(id, "development-row:%d", &index)
		if index >= 0 && index < len(a.developmentItems) {
			if a.selectedDevelopment != index {
				a.developmentDetailScroll = 0
			}
			a.selectedDevelopment = index
			a.releaseDevelopmentImage()
		}
		invalidateWindow(a.hwnd)
		return
	}
	if strings.HasPrefix(id, "development-toggle:") {
		var index int
		_, _ = fmt.Sscanf(id, "development-toggle:%d", &index)
		if index >= 0 && index < len(a.developmentItems) {
			item := &a.developmentItems[index]
			next := !item.Enabled
			a.setDevelopmentEnabled(item.ID, next)
			a.status = item.Name + " " + map[bool]string{true: "enabled for local development testing", false: "disabled for local development testing"}[next]
		}
		invalidateWindow(a.hwnd)
		return
	}
	if strings.HasPrefix(id, "plugin-toggle:") {
		var index int
		_, _ = fmt.Sscanf(id, "plugin-toggle:%d", &index)
		if index >= 0 && index < len(a.plugins) {
			p := a.plugins[index]
			if !p.BasicValid {
				if a.selected != index {
					a.pluginDetailScroll = 0
				}
				a.selected = index
				a.releasePluginImage()
				now := time.Now()
				if a.invalidClickPlugin == index && now.Sub(a.invalidClickAt) <= 2*time.Second {
					a.showToast("Detected plugin is invalid — see Plugin Details", 2400*time.Millisecond)
				}
				a.invalidClickPlugin = index
				a.invalidClickAt = now
				a.status = "Detected plugin is invalid — see Plugin Details"
				invalidateWindow(a.hwnd)
				return
			}
			next := !p.Setting.Enabled
			if p.WorkshopID != "" {
				a.setHybridWorkshopEnabled(p.WorkshopID, next)
				a.status = p.Manifest.Name + " " + map[bool]string{true: "enabled with Workshop item " + p.WorkshopID, false: "disabled with Workshop item " + p.WorkshopID}[next]
			} else {
				p.Setting.Enabled = next
				if !p.Setting.Enabled {
					p.Setting.Force = false
				}
				a.savePlugin(p)
				a.compatibility, a.compatWarnings = analyseCompatibility(a.baseDir, a.knowledgeRoot, a.pluginDir, a.plugins, a.symbolCache, a.report)
				a.status = p.Manifest.Name + " " + map[bool]string{true: "enabled", false: "disabled"}[p.Setting.Enabled]
				a.normalizeModSelections()
			}
		}
		invalidateWindow(a.hwnd)
		return
	}

	switch id {
	case "copy-build-sha":
		if a.game != nil && a.game.SHA256 != "" {
			if err := copyTextToClipboard(a.hwnd, a.game.SHA256); err != nil {
				a.status = "Could not copy Build SHA: " + err.Error()
			} else {
				a.status = "Build SHA copied to clipboard"
			}
		}
	case "playlist-dropdown":
		a.playlistDropdown = !a.playlistDropdown
	case "playlist-save":
		a.openPlaylistSave()
	case "playlist-rename":
		a.openPlaylistRename()
	case "playlist-export":
		a.exportSelectedPlaylist()
	case "playlist-import":
		a.playlistImportModal = true
		a.playlistDropdown = false
		a.status = "Choose a playlist JSON or drag it into the import window"
	case "playlist-import-cancel":
		a.playlistImportModal = false
		a.status = "Playlist import cancelled"
	case "playlist-import-browse":
		initial := filepath.Join(a.baseDir, "Exports")
		if path := openPlaylistDialog(a.hwnd, initial); path != "" {
			a.importPlaylistFile(path)
		}
	case "playlist-delete":
		a.requestDeleteSelectedPlaylist()
	case "playlist-delete-cancel":
		a.playlistDeleteModal = false
		a.playlistDeleteName = ""
		a.status = "Playlist deletion cancelled"
	case "playlist-delete-confirm":
		a.confirmDeleteSelectedPlaylist()
	case "playlist-modal-cancel":
		a.playlistModal = false
		a.playlistModalMode = ""
		a.playlistDraft = ""
		a.status = "Playlist save cancelled"
	case "playlist-modal-save":
		a.savePlaylistDraft()
	case "mod-enable-all":
		if a.modView == 1 {
			enable := !a.allWorkshopEnabled()
			a.setAllWorkshop(enable)
			a.status = map[bool]string{true: "All Workshop mods enabled for the current RML configuration", false: "All Workshop mods disabled for the current RML configuration"}[enable]
		} else if a.modView == 2 {
			enable := !a.allDevelopmentEnabled()
			a.setAllDevelopment(enable)
			a.status = map[bool]string{true: "All Development mods enabled for local testing", false: "All Development mods disabled for local testing"}[enable]
		} else {
			enable := !a.allPluginsEnabled()
			a.setAllPlugins(enable)
			a.status = map[bool]string{true: "All plugins enabled", false: "All plugins disabled"}[enable]
		}
	case "mod-hide-disabled":
		if a.modView == 1 {
			a.cfg.HideDisabledWorkshop = !a.cfg.HideDisabledWorkshop
			a.workshopScroll = 0
		} else if a.modView == 2 {
			a.cfg.HideDisabledDevelopment = !a.cfg.HideDisabledDevelopment
			a.developmentScroll = 0
		} else {
			a.cfg.HideDisabledPlugins = !a.cfg.HideDisabledPlugins
			a.pluginScroll = 0
		}
		a.normalizeModSelections()
		_ = saveConfig(a.configPath, a.cfg)
	case "mod-scan-updates":
		a.startWorkshopUpdateScan()
		// The unified Needs Attention filter includes both revision problems and
		// delisted/unsupported Workshop items, so a manual scan refreshes both.
		a.startWorkshopSupportScan(true)
	case "mod-needs-attention":
		if a.modView == 1 {
			a.cfg.WorkshopNeedsAttentionOnly = !a.cfg.WorkshopNeedsAttentionOnly
			a.workshopScroll = 0
		} else {
			a.cfg.PluginNeedsAttentionOnly = !a.cfg.PluginNeedsAttentionOnly
			a.pluginScroll = 0
		}
		a.normalizeModSelections()
		_ = saveConfig(a.configPath, a.cfg)
		a.startWorkshopSupportScan(false)
	case "mod-includes-plugin":
		if a.modView == 1 {
			a.cfg.WorkshopIncludesPluginOnly = !a.cfg.WorkshopIncludesPluginOnly
			a.workshopScroll = 0
			a.normalizeModSelections()
			_ = saveConfig(a.configPath, a.cfg)
		}
	case "mod-search":
		a.modSearchFocus = true
		a.status = "Search filters this Mod list as you type"
	case "mod-search-clear":
		a.clearModSearch()
		a.modSearchFocus = true
	case "plugin-detail-scroll-pageup", "workshop-detail-scroll-pageup", "development-detail-scroll-pageup":
		a.scrollActiveDetail(1)
	case "plugin-detail-scroll-pagedown", "workshop-detail-scroll-pagedown", "development-detail-scroll-pagedown":
		a.scrollActiveDetail(-1)
	case "workshop-sort:name":
		a.toggleWorkshopSort("name")
	case "workshop-sort:id":
		a.toggleWorkshopSort("id")
	case "workshop-sort:on":
		a.toggleWorkshopSort("on")
	case "workshop-unsubscribe":
		a.requestWorkshopUnsubscribe()
	case "workshop-unsubscribe-cancel":
		a.cancelWorkshopUnsubscribe()
	case "workshop-unsubscribe-confirm":
		a.beginWorkshopUnsubscribe()
	case "workshop-force-update":
		a.requestWorkshopForceUpdate()
	case "workshop-force-update-cancel":
		a.cancelWorkshopForceUpdate()
	case "workshop-force-update-confirm":
		a.beginWorkshopForceUpdate()
	case "browse":
		a.chooseGame()
	case "refresh":
		a.refresh(true)
		a.status = "Plugins, Workshop, Development mods and diagnostics refreshed"
		if (a.modView == 1 && a.cfg.WorkshopNeedsAttentionOnly) || (a.modView == 0 && a.cfg.PluginNeedsAttentionOnly) {
			a.startWorkshopSupportScan(true)
		}
	case "launch":
		a.startLaunch()
		return
	case "auto-order":
		a.cfg.AutoOrder = !a.cfg.AutoOrder
		if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
			a.cfg.DefaultAutoOrder = a.cfg.AutoOrder
		}
		a.refresh(true)
		if a.cfg.AutoOrder {
			a.status = "Automatic dependency ordering enabled"
		} else {
			a.status = "Manual priority ordering enabled"
		}
	case "reset":
		a.resetRecommended()
	case "force":
		if p := a.selectedPlugin(); p != nil && p.Setting.Enabled {
			p.Setting.Force = !p.Setting.Force
			a.savePlugin(p)
			a.status = "Force-load policy updated for " + p.Manifest.Name
		}
	case "priority-minus":
		if p := a.selectedPlugin(); p != nil {
			p.Setting.Priority -= 10
			a.savePlugin(p)
			a.status = "Priority updated"
		}
	case "priority-plus":
		if p := a.selectedPlugin(); p != nil {
			p.Setting.Priority += 10
			a.savePlugin(p)
			a.status = "Priority updated"
		}
	case "verbose-diagnostics":
		a.cfg.VerboseDiagnostics = !a.cfg.VerboseDiagnostics
		if a.cfg.VerboseDiagnostics {
			a.cfg.LaunchProfile = string(profileDeepTest)
			a.status = "Detailed Diagnostic Logging enabled"
		} else {
			a.cfg.LaunchProfile = string(profileNormal)
			a.status = "Detailed Diagnostic Logging disabled"
		}
		_ = saveConfig(a.configPath, a.cfg)
	case "auto-crash-archives":
		a.cfg.AutoCrashArchives = !a.cfg.AutoCrashArchives
		_ = saveConfig(a.configPath, a.cfg)
		a.status = map[bool]string{true: "Automatic crash archives enabled", false: "Automatic crash archives disabled"}[a.cfg.AutoCrashArchives]
	case "close-after":
		a.cfg.CloseAfterLaunch = !a.cfg.CloseAfterLaunch
		_ = saveConfig(a.configPath, a.cfg)
	case "auto-hide":
		a.cfg.AutoHideOnLaunch = !a.cfg.AutoHideOnLaunch
		_ = saveConfig(a.configPath, a.cfg)
	case "open-log":
		openPath(highLevelLogPath(a.baseDir))
	case "open-verbose-log":
		if p := latestVerboseDiagnostic(a.baseDir); p != "" {
			openPath(p)
		} else {
			a.status = "No detailed diagnostic log has been produced yet"
		}
	case "create-support-archive":
		if p, err := createSupportArchive(a.baseDir, "manual support archive"); err != nil {
			a.status = "Could not create support archive: " + err.Error()
		} else {
			a.status = "Support archive created: " + p
		}
	case "open-crash-folder":
		_ = os.MkdirAll(crashArchiveDir(a.baseDir), 0o755)
		openPath(crashArchiveDir(a.baseDir))
	case "development-open-root":
		openPath(developmentRootFromGamePath(a.cfg.GamePath))
	case "development-open-folder":
		if item := a.selectedDevelopmentItem(); item != nil {
			openPath(item.Folder)
		}
	case "development-open-config":
		if item := a.selectedDevelopmentItem(); item != nil && item.ConfigPath != "" {
			openPath(item.ConfigPath)
		}
	case "plugin-open-settings":
		if p := a.selectedPlugin(); p != nil {
			if p.SettingsPath != "" {
				openPath(p.SettingsPath)
				a.status = "Opened plugin settings: " + filepath.Base(p.SettingsPath)
			} else if p.SettingsCount > 1 && p.SettingsDir != "" {
				openPath(p.SettingsDir)
				a.status = fmt.Sprintf("Multiple plugin INI files found; opened settings folder (%d candidates)", p.SettingsCount)
			} else {
				a.status = "No editable plugin INI/settings file was found beside this DLL"
			}
		}
	case "plugin-workshop-open-page":
		if p := a.selectedPlugin(); p != nil && validWorkshopID(p.WorkshopID) {
			openPath(workshopURL(p.WorkshopID))
		}
	case "plugin-workshop-open-folder":
		if p := a.selectedPlugin(); p != nil && validWorkshopID(p.WorkshopID) {
			if item := a.workshopItemByID(p.WorkshopID); item != nil {
				openPath(item.Folder)
			} else if p.SourceRoot != "" {
				// SourceRoot is normally the Workshop item's plugin/plugins folder.
				// Walk back only when we can prove the parent directory is the
				// expected numeric Workshop ID.
				dir := filepath.Clean(p.SourceRoot)
				for i := 0; i < 2; i++ {
					if filepath.Base(dir) == p.WorkshopID {
						openPath(dir)
						break
					}
					dir = filepath.Dir(dir)
				}
			}
		}
	case "plugin-workshop-force-update":
		if p := a.selectedPlugin(); p != nil && validWorkshopID(p.WorkshopID) {
			a.requestWorkshopForceUpdateByID(p.WorkshopID, p.Manifest.Name)
		}
	case "plugin-workshop-unsubscribe":
		if p := a.selectedPlugin(); p != nil && validWorkshopID(p.WorkshopID) {
			a.requestWorkshopUnsubscribeByID(p.WorkshopID, p.Manifest.Name)
		}
	case "workshop-open-page":
		if item := a.selectedWorkshopItem(); item != nil {
			openPath(workshopURL(item.ID))
		}
	case "workshop-open-folder":
		if item := a.selectedWorkshopItem(); item != nil {
			openPath(item.Folder)
		}
	}
	invalidateWindow(a.hwnd)
}
