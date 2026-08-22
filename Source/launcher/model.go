package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const loaderAPI = 1

var launcherVersion = "1.0.0"

type APIRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type HookIntent struct {
	Symbol      string `json:"symbol"`
	Interaction string `json:"interaction"`
	Phase       string `json:"phase,omitempty"`
	Temperature string `json:"temperature,omitempty"`
}

type Manifest struct {
	SchemaVersion         int          `json:"schemaVersion"`
	ID                    string       `json:"id"`
	Name                  string       `json:"name"`
	Version               string       `json:"version"`
	Author                string       `json:"author"`
	Description           string       `json:"description"`
	DLL                   string       `json:"dll"`
	LoaderAPI             APIRange     `json:"loaderApi"`
	SupportedGameVersions []string     `json:"supportedGameVersions"`
	RequiredSymbols       []string     `json:"requiredSymbols"`
	OptionalSymbols       []string     `json:"optionalSymbols"`
	HookIntents           []HookIntent `json:"hookIntents,omitempty"`
	Dependencies          []string     `json:"dependencies"`
	Conflicts             []string     `json:"conflicts"`
	LoadBefore            []string     `json:"loadBefore"`
	LoadAfter             []string     `json:"loadAfter"`
	DefaultPriority       int          `json:"defaultPriority"`
	EnabledByDefault      bool         `json:"enabledByDefault"`
}

type PluginSetting struct {
	Enabled  bool `json:"enabled"`
	Force    bool `json:"force"`
	Priority int  `json:"priority"`
}

type PlaylistPlugin struct {
	ID       string `json:"id"`
	Force    bool   `json:"force"`
	Priority int    `json:"priority"`
	Order    int    `json:"order"`
}

type PluginPlaylist struct {
	Name        string           `json:"name"`
	Plugins     []PlaylistPlugin `json:"plugins"`
	Workshop    []string         `json:"workshop,omitempty"`
	Development []string         `json:"development,omitempty"`
}

type AppConfig struct {
	GamePath                   string                   `json:"gamePath"`
	AutoOrder                  bool                     `json:"autoOrder"`
	AllowProbable              bool                     `json:"allowProbableSymbols"`
	CloseAfterLaunch           bool                     `json:"closeAfterLaunch"`
	AutoHideOnLaunch           bool                     `json:"autoHideOnLaunch"`
	LaunchProfile              string                   `json:"launchProfile"`
	PluginSettings             map[string]PluginSetting `json:"plugins"`
	Playlists                  []PluginPlaylist         `json:"playlists,omitempty"`
	SelectedPlaylist           string                   `json:"selectedPlaylist,omitempty"`
	DefaultPluginSettings      map[string]PluginSetting `json:"defaultPlugins,omitempty"`
	WorkshopSettings           map[string]bool          `json:"workshopItems,omitempty"`
	DefaultWorkshopSettings    map[string]bool          `json:"defaultWorkshopItems,omitempty"`
	DevelopmentSettings        map[string]bool          `json:"developmentItems,omitempty"`
	DefaultDevelopmentSettings map[string]bool          `json:"defaultDevelopmentItems,omitempty"`
	DefaultAutoOrder           bool                     `json:"defaultAutoOrder"`
	HideDisabledPlugins        bool                     `json:"hideDisabledPlugins,omitempty"`
	HideDisabledWorkshop       bool                     `json:"hideDisabledWorkshop,omitempty"`
	WorkshopIncludesPluginOnly bool                     `json:"workshopIncludesPluginOnly,omitempty"`
	HideDisabledDevelopment    bool                     `json:"hideDisabledDevelopment,omitempty"`
	PluginNeedsAttentionOnly   bool                     `json:"pluginNeedsAttentionOnly,omitempty"`
	WorkshopNeedsAttentionOnly bool                     `json:"workshopNeedsAttentionOnly,omitempty"`
	VerboseDiagnostics         bool                     `json:"verboseDiagnostics,omitempty"`
	AutoCrashArchives          bool                     `json:"autoCrashArchives"`
}

type Plugin struct {
	LoaderKind          string
	LegacyAPI           int
	BasicValid          bool
	PEValidation        pluginPEValidation
	HasRMLInterface     bool
	HasRMLApiVersion    bool
	HasRMLQuery         bool
	HasRMLLoad          bool
	HasTesmioInterface  bool
	HasTesmioApiVersion bool
	HasTesmioInit       bool
	HasTesmioStart      bool
	ManifestPath        string
	DLLPath             string
	DLLSHA256           string
	WorkshopID          string // non-empty when the DLL is supplied by a Steam Workshop item
	DevelopmentID       string // internal-only: DLL payload discovered inside an independent workshop_wip project
	SourceRoot          string // folder from which this plugin was discovered
	SettingsPath        string // preferred editable .ini settings file, when one can be identified deterministically
	SettingsDir         string // folder containing candidate settings files
	SettingsCount       int    // number of non-reserved .ini candidates found near the plugin
	Manifest            Manifest
	Setting             PluginSetting
	Status              string
	StatusClass         int
	Reasons             []string
	Order               int
}

type GameIdentity struct {
	Path    string
	Version string
	SHA256  string
	Known   bool
	PE64    bool
	Size    int64
}

type RuntimeReport struct {
	BootstrapStatus uint32 `json:"bootstrapStatus"`
	Game            struct {
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	} `json:"game"`
	Symbols []struct {
		ID               string `json:"id"`
		RVA              uint32 `json:"rva"`
		Confidence       string `json:"confidence"`
		Resolver         string `json:"resolver"`
		FunctionSize     int    `json:"functionSize"`
		EntryFingerprint string `json:"entryFingerprint"`
		Temperature      string `json:"temperature"`
	} `json:"symbols"`
	DeepTest struct {
		PluginsObserved      int    `json:"pluginsObserved"`
		HostHookRequests     int    `json:"hostHookRequests"`
		UnmanagedTextChanges int    `json:"unmanagedTextChanges"`
		UnmanagedTextPages   int    `json:"unmanagedTextPages"`
		MonitorScans         uint64 `json:"monitorScans"`
		LateChangeWindows    uint64 `json:"lateChangeWindows"`
		LastAuditIssues      int    `json:"lastAuditIssues"`
	} `json:"deepTest"`
	HookChains []struct {
		Target           string `json:"target"`
		SymbolID         string `json:"symbolId,omitempty"`
		Integrity        string `json:"integrity"`
		Proof            string `json:"proof"`
		ObservedTopOwner string `json:"observedTopOwner,omitempty"`
		Steps            []struct {
			PluginID       string `json:"pluginId"`
			PluginSHA256   string `json:"pluginSha256"`
			Label          string `json:"label"`
			ExecutionOrder int    `json:"executionOrder"`
		} `json:"steps"`
	} `json:"hookChains"`
	Plugins []struct {
		ID                    string `json:"id"`
		Name                  string `json:"name"`
		Version               string `json:"version"`
		SHA256                string `json:"sha256"`
		Priority              int    `json:"priority"`
		LoadOrder             int    `json:"loadOrder"`
		LegacyHooks           int    `json:"legacyHooks"`
		LegacyHookFailures    int    `json:"legacyHookFailures"`
		UnmanagedInitChanges  int    `json:"unmanagedInitChanges"`
		UnmanagedStartChanges int    `json:"unmanagedStartChanges"`
		UnmanagedInitPages    int    `json:"unmanagedInitPages"`
		UnmanagedStartPages   int    `json:"unmanagedStartPages"`
		FirstUnmanagedRVA     uint32 `json:"firstUnmanagedRva"`
		Loaded                bool   `json:"loaded"`
		Failed                bool   `json:"failed"`
		Status                string `json:"status"`
	} `json:"plugins"`
}

var knownGames = map[string]string{
	"ec05bb6257da31cfcaec639c8462683ee7bcf26158e0751a29a2d48025169522": "1.1.1.7",
	"296644a9f207d609031fc2ae73fed2dcb34619a1d55a35d1c7b51965ce6841b8": "1.1.1.9",
}

var loaderSymbolCatalog = map[string]struct{}{
	"wrsr.resource_get":                      {},
	"wrsr.construction.tool_frame":           {},
	"wrsr.building.assignment_target_select": {},
	"wrsr.vehicle.compatibility_check":       {},
	"wrsr.vehicle.task_advance":              {},
	"wrsr.infrastructure.route_validation":   {},
	"wrsr.gui.tool_resources_init":           {},
}

func defaultConfig() AppConfig {
	return AppConfig{
		AutoOrder:                  true,
		AllowProbable:              false,
		CloseAfterLaunch:           false,
		AutoHideOnLaunch:           false,
		LaunchProfile:              string(profileNormal),
		PluginSettings:             map[string]PluginSetting{},
		WorkshopSettings:           map[string]bool{},
		DevelopmentSettings:        map[string]bool{},
		SelectedPlaylist:           "Default",
		DefaultPluginSettings:      map[string]PluginSetting{},
		DefaultWorkshopSettings:    map[string]bool{},
		DefaultDevelopmentSettings: map[string]bool{},
		DefaultAutoOrder:           true,
		AutoCrashArchives:          true,
	}
}

func clampPriority(value int) int {
	const limit = 100000
	if value < -limit {
		return -limit
	}
	if value > limit {
		return limit
	}
	return value
}

func validPluginID(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validManifestText(value string, maxBytes int, required bool) bool {
	if value == "" {
		return !required
	}
	if len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validateIDList(label string, items []string, maxItems int, self string) error {
	if len(items) > maxItems {
		return fmt.Errorf("%s contains more than %d entries", label, maxItems)
	}
	seen := map[string]bool{}
	for _, item := range items {
		if !validPluginID(item) {
			return fmt.Errorf("%s contains invalid identifier %q", label, item)
		}
		if self != "" && item == self {
			return fmt.Errorf("%s cannot reference the plugin itself", label)
		}
		if seen[item] {
			return fmt.Errorf("%s contains duplicate identifier %q", label, item)
		}
		seen[item] = true
	}
	return nil
}

func validateManifestShape(m *Manifest) error {
	if !validManifestText(m.Name, 127, true) {
		return errors.New("name must be 1–127 printable UTF-8 bytes")
	}
	if !validManifestText(m.Version, 31, true) {
		return errors.New("version must be 1–31 printable UTF-8 bytes")
	}
	if !validManifestText(m.Author, 127, false) {
		return errors.New("author must be at most 127 printable UTF-8 bytes")
	}
	if !validManifestText(m.Description, 2048, false) {
		return errors.New("description must be at most 2048 printable UTF-8 bytes")
	}
	if len(m.DLL) > 255 {
		return errors.New("dll filename exceeds 255 bytes")
	}
	if m.LoaderAPI.Min < 1 || m.LoaderAPI.Max < m.LoaderAPI.Min || m.LoaderAPI.Max > 65535 {
		return errors.New("loaderApi range is invalid")
	}
	if len(m.RequiredSymbols) > 32 || len(m.OptionalSymbols) > 32 {
		return errors.New("requiredSymbols and optionalSymbols are limited to 32 entries each")
	}
	for _, list := range []struct {
		name  string
		items []string
	}{
		{"requiredSymbols", m.RequiredSymbols},
		{"optionalSymbols", m.OptionalSymbols},
	} {
		seen := map[string]bool{}
		for _, symbol := range list.items {
			if !validPluginID(symbol) {
				return fmt.Errorf("%s contains invalid symbol identifier %q", list.name, symbol)
			}
			if seen[symbol] {
				return fmt.Errorf("%s contains duplicate symbol %q", list.name, symbol)
			}
			seen[symbol] = true
		}
	}
	if len(m.HookIntents) > 64 {
		return errors.New("hookIntents contains more than 64 entries")
	}
	for _, intent := range m.HookIntents {
		if !validPluginID(intent.Symbol) {
			return fmt.Errorf("hookIntents contains invalid symbol %q", intent.Symbol)
		}
		if !validManifestText(intent.Interaction, 63, true) || !validManifestText(intent.Phase, 15, false) || !validManifestText(intent.Temperature, 15, false) {
			return errors.New("hookIntents contains invalid text")
		}
	}

	for _, list := range []struct {
		name  string
		items []string
	}{
		{"dependencies", m.Dependencies},
		{"conflicts", m.Conflicts},
		{"loadBefore", m.LoadBefore},
		{"loadAfter", m.LoadAfter},
	} {
		if err := validateIDList(list.name, list.items, 128, m.ID); err != nil {
			return err
		}
	}
	if len(m.SupportedGameVersions) > 64 {
		return errors.New("supportedGameVersions contains more than 64 entries")
	}
	for _, version := range m.SupportedGameVersions {
		if !validManifestText(version, 31, true) {
			return errors.New("supportedGameVersions contains an invalid version string")
		}
	}
	return nil
}

func clonePluginSettings(src map[string]PluginSetting) map[string]PluginSetting {
	out := make(map[string]PluginSetting, len(src))
	for id, setting := range src {
		setting.Priority = clampPriority(setting.Priority)
		out[id] = setting
	}
	return out
}

func cloneWorkshopSettings(src map[string]bool) map[string]bool {
	out := make(map[string]bool, len(src))
	for id, enabled := range src {
		if validWorkshopID(id) {
			out[id] = enabled
		}
	}
	return out
}

func cloneDevelopmentSettings(src map[string]bool) map[string]bool {
	return cloneWorkshopSettings(src)
}

func loadConfig(path string) AppConfig {
	cfg := defaultConfig()
	if info, err := os.Stat(path); err == nil && info.Size() >= 0 && info.Size() <= 1024*1024 {
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &cfg)
		}
	}
	cfg.LaunchProfile = string(normalizeLaunchProfile(cfg.LaunchProfile))
	if cfg.LaunchProfile == string(profileDeepTest) {
		cfg.VerboseDiagnostics = true
	}
	if cfg.VerboseDiagnostics {
		cfg.LaunchProfile = string(profileDeepTest)
	} else {
		cfg.LaunchProfile = string(profileNormal)
	}
	if cfg.PluginSettings == nil {
		cfg.PluginSettings = map[string]PluginSetting{}
	}
	if cfg.WorkshopSettings == nil {
		cfg.WorkshopSettings = map[string]bool{}
	}
	if cfg.DefaultWorkshopSettings == nil {
		cfg.DefaultWorkshopSettings = map[string]bool{}
	}
	if cfg.DevelopmentSettings == nil {
		cfg.DevelopmentSettings = map[string]bool{}
	}
	if cfg.DefaultDevelopmentSettings == nil {
		cfg.DefaultDevelopmentSettings = map[string]bool{}
	}
	for id, setting := range cfg.PluginSettings {
		setting.Priority = clampPriority(setting.Priority)
		cfg.PluginSettings[id] = setting
	}

	if cfg.DefaultPluginSettings == nil {
		cfg.DefaultPluginSettings = map[string]PluginSetting{}
	}
	for id, setting := range cfg.DefaultPluginSettings {
		setting.Priority = clampPriority(setting.Priority)
		cfg.DefaultPluginSettings[id] = setting
	}

	// Playlist data is user-editable JSON. Keep it bounded and discard malformed
	// entries instead of letting a broken config make the launcher unusable.
	// "Default" is reserved for the launcher's baseline configuration and is
	// never stored as a user playlist.
	clean := make([]PluginPlaylist, 0, minIntModel(len(cfg.Playlists), 64))
	seenNames := map[string]bool{}
	for _, playlist := range cfg.Playlists {
		name := strings.TrimSpace(playlist.Name)
		if strings.EqualFold(name, "Default") || !validManifestText(name, 96, true) || seenNames[strings.ToLower(name)] || len(clean) >= 64 {
			continue
		}
		seenNames[strings.ToLower(name)] = true
		plugins := make([]PlaylistPlugin, 0, minIntModel(len(playlist.Plugins), 256))
		seenIDs := map[string]bool{}
		for _, entry := range playlist.Plugins {
			if len(plugins) >= 256 || !validPluginID(entry.ID) || seenIDs[entry.ID] {
				continue
			}
			seenIDs[entry.ID] = true
			entry.Priority = clampPriority(entry.Priority)
			if entry.Order < 0 {
				entry.Order = 0
			}
			plugins = append(plugins, entry)
		}
		sort.SliceStable(plugins, func(i, j int) bool { return plugins[i].Order < plugins[j].Order })
		workshop := make([]string, 0, minIntModel(len(playlist.Workshop), 4096))
		seenWorkshop := map[string]bool{}
		for _, id := range playlist.Workshop {
			id = strings.TrimSpace(id)
			if len(workshop) >= 4096 || !validWorkshopID(id) || seenWorkshop[id] {
				continue
			}
			seenWorkshop[id] = true
			workshop = append(workshop, id)
		}
		development := make([]string, 0, minIntModel(len(playlist.Development), 4096))
		seenDevelopment := map[string]bool{}
		for _, id := range playlist.Development {
			id = strings.TrimSpace(id)
			if len(development) >= 4096 || !validWorkshopID(id) || seenDevelopment[id] {
				continue
			}
			seenDevelopment[id] = true
			development = append(development, id)
		}
		playlist.Name, playlist.Plugins, playlist.Workshop, playlist.Development = name, plugins, workshop, development
		clean = append(clean, playlist)
	}
	sort.SliceStable(clean, func(i, j int) bool {
		return strings.ToLower(clean[i].Name) < strings.ToLower(clean[j].Name)
	})
	cfg.Playlists = clean

	selected := strings.TrimSpace(cfg.SelectedPlaylist)
	if selected == "" || strings.EqualFold(selected, "Default") {
		cfg.SelectedPlaylist = "Default"
	} else {
		found := false
		for _, playlist := range cfg.Playlists {
			if playlist.Name == selected {
				found = true
				break
			}
		}
		if found {
			cfg.SelectedPlaylist = selected
		} else {
			cfg.SelectedPlaylist = "Default"
		}
	}
	return cfg
}

func minIntModel(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func saveConfig(path string, cfg AppConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".rml-config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

const (
	pluginKindRML    = "rml-native"
	pluginKindTesmio = "tesmio-legacy"
	pluginKindRaw    = "raw-native"
)

func syntheticPluginID(prefix, filename string) string {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(filename)), strings.ToLower(filepath.Ext(filename)))
	var b strings.Builder
	lastSep := false
	for _, r := range base {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastSep = false
		} else if !lastSep && b.Len() > 0 {
			b.WriteByte('.')
			lastSep = true
		}
	}
	clean := strings.Trim(b.String(), ".")
	if clean == "" {
		clean = "plugin"
	}
	id := prefix + "." + clean
	if len(id) > 128 {
		id = id[:128]
	}
	return id
}

func inferTesmioAPIVersion(path string) int {
	rva, err := exportRVA(path, "TsmPluginApiVersion")
	if err != nil {
		return 0
	}
	b, err := readPEBytesAtRVA(path, rva, 32)
	if err != nil {
		return 0
	}
	for i := 0; i+5 <= len(b); i++ {
		if b[i] != 0xB8 {
			continue
		}
		v := int(uint32(b[i+1]) | uint32(b[i+2])<<8 | uint32(b[i+3])<<16 | uint32(b[i+4])<<24)
		if v < 1 || v > 64 {
			continue
		}
		limit := i + 12
		if limit > len(b) {
			limit = len(b)
		}
		for j := i + 5; j < limit; j++ {
			if b[j] == 0xC3 {
				return v
			}
		}
	}
	return 0
}

type pluginInterfaceInfo struct {
	HasRMLApiVersion    bool
	HasRMLQuery         bool
	HasRMLLoad          bool
	HasTesmioApiVersion bool
	HasTesmioInit       bool
	HasTesmioStart      bool
}

func detectPluginInterfaces(path string, basicValid bool) pluginInterfaceInfo {
	var out pluginInterfaceInfo
	if !basicValid {
		return out
	}
	exports, err := peExportNames(path)
	if err != nil {
		return out
	}
	out.HasRMLApiVersion = containsString(exports, "RmlPluginApiVersion")
	out.HasRMLQuery = containsString(exports, "RmlPluginQuery")
	out.HasRMLLoad = containsString(exports, "RmlPluginLoad")
	out.HasTesmioApiVersion = containsString(exports, "TsmPluginApiVersion")
	out.HasTesmioInit = containsString(exports, "TsmPluginInit")
	out.HasTesmioStart = containsString(exports, "TsmPluginStart")
	return out
}

type pluginSource struct {
	Dir           string
	WorkshopID    string
	DevelopmentID string
	Label         string
}

func addPluginSource(out []pluginSource, seen map[string]bool, dir, workshopID, developmentID, label string) []pluginSource {
	if strings.TrimSpace(dir) == "" {
		return out
	}
	clean, err := filepath.Abs(dir)
	if err != nil {
		clean = filepath.Clean(dir)
	}
	key := strings.ToLower(filepath.Clean(clean))
	if seen[key] {
		return out
	}
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return out
	}
	seen[key] = true
	return append(out, pluginSource{Dir: clean, WorkshopID: workshopID, DevelopmentID: developmentID, Label: label})
}

// Published Workshop plugin folders are authoritative over the loader-local
// fallback. Development/workshop_wip projects are deliberately NOT part of
// this source list: they are scanned into a separate, independent inventory.
func buildPluginSources(localPluginDir string, workshopItems []WorkshopItem) []pluginSource {
	seen := map[string]bool{}
	var out []pluginSource
	for i := range workshopItems {
		item := &workshopItems[i]
		for _, dir := range item.PluginDirs {
			out = addPluginSource(out, seen, dir, item.ID, "", "Workshop "+item.ID)
		}
	}
	out = addPluginSource(out, seen, localPluginDir, "", "", "RML local plugins")
	if strings.EqualFold(filepath.Base(localPluginDir), "plugins") {
		out = addPluginSource(out, seen, filepath.Join(filepath.Dir(localPluginDir), "plugin"), "", "", "RML local plugin fallback")
	}
	return out
}

// Development DLLs are intentionally isolated from Plugins/Workshop discovery.
// They are loaded only when their parent workshop_wip project is enabled.
func buildDevelopmentPluginSources(developmentItems []DevelopmentMod) []pluginSource {
	seen := map[string]bool{}
	var out []pluginSource
	for i := range developmentItems {
		item := &developmentItems[i]
		for _, dir := range item.PluginDirs {
			out = addPluginSource(out, seen, dir, "", item.ID, "Development "+item.ID)
		}
	}
	return out
}

func uniqueSyntheticID(prefix, filename, workshopID, developmentID string, seen map[string]bool) string {
	id := syntheticPluginID(prefix, filename)
	if !seen[id] {
		return id
	}
	if developmentID != "" {
		candidate := id + ".dev" + developmentID
		if len(candidate) <= 128 && !seen[candidate] {
			return candidate
		}
	}
	if workshopID != "" {
		candidate := id + ".ws" + workshopID
		if len(candidate) <= 128 && !seen[candidate] {
			return candidate
		}
	}
	return id
}

func scanPlugins(pluginDir string, workshopItems []WorkshopItem, cfg *AppConfig) ([]*Plugin, []string) {
	return scanPluginSources(buildPluginSources(pluginDir, workshopItems), cfg)
}

// scanDevelopmentPlugins builds an independent runtime-only DLL inventory for
// local workshop_wip projects. It never reads or writes normal PluginSettings,
// never suppresses a subscribed Workshop plugin, and never toggles one.
func scanDevelopmentPlugins(developmentItems []DevelopmentMod) ([]*Plugin, []string) {
	tmp := defaultConfig()
	tmp.PluginSettings = map[string]PluginSetting{}
	plugins, warnings := scanPluginSources(buildDevelopmentPluginSources(developmentItems), &tmp)
	enabled := map[string]bool{}
	for i := range developmentItems {
		enabled[developmentItems[i].ID] = developmentItems[i].Enabled
	}
	for _, p := range plugins {
		p.Setting.Enabled = enabled[p.DevelopmentID]
		p.Setting.Force = false
	}
	return plugins, warnings
}

func scanPluginSources(sources []pluginSource, cfg *AppConfig) ([]*Plugin, []string) {
	var plugins []*Plugin
	var warnings []string
	seen := map[string]bool{}
	seenHash := map[string]bool{}

	if cfg.PluginSettings == nil {
		cfg.PluginSettings = map[string]PluginSetting{}
	}

	type manifestCandidate struct {
		manifest Manifest
		path     string
	}

	for _, source := range sources {
		entries, err := os.ReadDir(source.Dir)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", source.Label, err))
			continue
		}

		// Sidecar manifests are optional metadata. A bad or missing manifest must
		// never hide an otherwise valid DLL from discovery.
		manifestByDLL := map[string]manifestCandidate{}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".rml.json") {
				continue
			}
			manifestPath := filepath.Join(source.Dir, entry.Name())
			if info, err := entry.Info(); err != nil || info.Size() < 0 || info.Size() > 1024*1024 {
				warnings = append(warnings, fmt.Sprintf("%s: manifest exceeds the 1 MiB safety limit; DLL discovery will continue without it", manifestPath))
				continue
			}
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v; DLL discovery will continue without it", manifestPath, err))
				continue
			}
			var m Manifest
			if err := json.Unmarshal(data, &m); err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: invalid JSON: %v; DLL discovery will continue without it", manifestPath, err))
				continue
			}
			if m.ID == "" || m.Name == "" || m.DLL == "" || !validPluginID(m.ID) ||
				filepath.Base(m.DLL) != m.DLL || strings.ContainsAny(m.DLL, "/\\") || !strings.EqualFold(filepath.Ext(m.DLL), ".dll") {
				warnings = append(warnings, fmt.Sprintf("%s: invalid optional RML manifest metadata; DLL discovery will continue without it", manifestPath))
				continue
			}
			if err := validateManifestShape(&m); err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v; DLL discovery will continue without it", manifestPath, err))
				continue
			}
			m.DefaultPriority = clampPriority(m.DefaultPriority)
			key := strings.ToLower(filepath.Clean(filepath.Join(source.Dir, m.DLL)))
			manifestByDLL[key] = manifestCandidate{manifest: m, path: manifestPath}
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".dll") {
				continue
			}
			dllPath := filepath.Join(source.Dir, entry.Name())
			key := strings.ToLower(filepath.Clean(dllPath))
			hash, _ := hashFileSHA256(dllPath)
			// Keep the existing source precedence for byte-identical published/local
			// copies. Development is scanned separately and therefore never loses to
			// this de-duplication rule.
			if hash != "" && seenHash[hash] {
				continue
			}

			peValidation := validatePluginPE(dllPath)
			interfaces := detectPluginInterfaces(dllPath, peValidation.Valid)
			rmlUsable := interfaces.HasRMLQuery && interfaces.HasRMLLoad
			tesmioUsable := interfaces.HasTesmioApiVersion && interfaces.HasTesmioInit
			loaderKind := pluginKindRaw
			if rmlUsable {
				loaderKind = pluginKindRML
			} else if tesmioUsable {
				loaderKind = pluginKindTesmio
			}

			candidate, hasManifest := manifestByDLL[key]
			var m Manifest
			manifestPath := ""
			if hasManifest {
				m = candidate.manifest
				manifestPath = candidate.path
				if seen[m.ID] {
					warnings = append(warnings, fmt.Sprintf("duplicate plugin id %s in %s; earlier source retained", m.ID, source.Label))
					continue
				}
			} else {
				prefix := "native"
				switch loaderKind {
				case pluginKindRML:
					prefix = "rml"
				case pluginKindTesmio:
					prefix = "tesmio"
				}
				id := uniqueSyntheticID(prefix, entry.Name(), source.WorkshopID, source.DevelopmentID, seen)
				name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
				version := "native"
				description := "Native AMD64 Windows DLL discovered without an RML manifest. RML will attempt to load it using the detected interface, or as a raw DLL when no supported plugin lifecycle is present."
				if loaderKind == pluginKindTesmio {
					api := inferTesmioAPIVersion(dllPath)
					if api > 0 {
						version = fmt.Sprintf("api%d", api)
					}
					description = "TesmioLoader plugin discovered without an RML manifest. RML will use the Tesmio API 3/4 compatibility shim when its runtime API is supported."
				} else if loaderKind == pluginKindRML {
					version = "rml-api"
					description = "RML-aware native plugin discovered without a sidecar manifest. Its DLL exports provide the runtime plugin interface."
				}
				m = Manifest{
					SchemaVersion: 1, ID: id, Name: name, Version: version, DLL: entry.Name(),
					Description: description, LoaderAPI: APIRange{Min: 1, Max: loaderAPI},
					DefaultPriority: 1000 + len(plugins), EnabledByDefault: false,
				}
			}

			api := 0
			if interfaces.HasTesmioApiVersion && interfaces.HasTesmioInit {
				api = inferTesmioAPIVersion(dllPath)
			}
			setting, exists := cfg.PluginSettings[m.ID]
			if !exists {
				setting = PluginSetting{Enabled: m.EnabledByDefault, Priority: m.DefaultPriority}
			}
			setting.Priority = clampPriority(setting.Priority)
			// Force policy is retained only for config compatibility.
			setting.Force = false
			cfg.PluginSettings[m.ID] = setting
			p := &Plugin{
				LoaderKind: loaderKind, LegacyAPI: api, BasicValid: peValidation.Valid, PEValidation: peValidation,
				HasRMLInterface:  interfaces.HasRMLApiVersion || interfaces.HasRMLQuery || interfaces.HasRMLLoad,
				HasRMLApiVersion: interfaces.HasRMLApiVersion, HasRMLQuery: interfaces.HasRMLQuery, HasRMLLoad: interfaces.HasRMLLoad,
				HasTesmioInterface:  interfaces.HasTesmioApiVersion || interfaces.HasTesmioInit || interfaces.HasTesmioStart,
				HasTesmioApiVersion: interfaces.HasTesmioApiVersion, HasTesmioInit: interfaces.HasTesmioInit, HasTesmioStart: interfaces.HasTesmioStart,
				ManifestPath: manifestPath, DLLPath: dllPath, DLLSHA256: hash,
				WorkshopID: source.WorkshopID, DevelopmentID: source.DevelopmentID, SourceRoot: source.Dir,
				Manifest: m, Setting: setting,
			}
			populatePluginSettingsMetadata(p)
			plugins = append(plugins, p)
			seen[m.ID] = true
			if hash != "" {
				seenHash[hash] = true
			}
		}
	}
	sort.Slice(plugins, func(i, j int) bool {
		if plugins[i].Setting.Priority != plugins[j].Setting.Priority {
			return plugins[i].Setting.Priority < plugins[j].Setting.Priority
		}
		return plugins[i].Manifest.ID < plugins[j].Manifest.ID
	})
	return plugins, warnings
}

func hashGame(path string) (GameIdentity, error) {
	var id GameIdentity
	id.Path = path
	f, err := os.Open(path)
	if err != nil {
		return id, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return id, err
	}
	id.Size = stat.Size()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return id, err
	}
	id.SHA256 = hex.EncodeToString(h.Sum(nil))
	id.Version, id.Known = knownGames[id.SHA256]
	if !id.Known {
		id.Version = "Untested build"
	}
	machine, is64, err := readPEMachine(path)
	if err != nil {
		return id, err
	}
	id.PE64 = is64 && machine == 0x8664
	return id, nil
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func validatePlugins(plugins []*Plugin, game *GameIdentity, allowProbable bool) {
	byID := map[string]*Plugin{}
	activeByID := map[string][]*Plugin{}
	activeByHash := map[string][]*Plugin{}
	for _, p := range plugins {
		p.Reasons = nil
		byID[p.Manifest.ID] = p
		if p.Setting.Enabled {
			activeByID[p.Manifest.ID] = append(activeByID[p.Manifest.ID], p)
			if p.DLLSHA256 != "" {
				activeByHash[p.DLLSHA256] = append(activeByHash[p.DLLSHA256], p)
			}
		}
	}

	for _, p := range plugins {
		p.StatusClass = 0
		// Basic PE validation is the only static admission gate. Do not mutate the
		// saved Enabled bit: an invalid entry may remain present in a playlist, but
		// it is never eligible for the runtime queue.
		if !p.BasicValid {
			p.Status = "INVALID"
			p.StatusClass = 2
			p.Reasons = append(p.Reasons, p.PEValidation.Failures...)
			continue
		}
		if !p.Setting.Enabled {
			p.Status = "DISABLED"
			p.StatusClass = 3
			continue
		}

		// Everything below is diagnostic/classification information only. It may
		// warn the user, but it must not prevent a structurally valid AMD64 DLL from
		// being queued and attempted.
		warn := false
		if _, err := os.Stat(p.DLLPath); err != nil {
			p.Reasons = append(p.Reasons, "DLL file could not be re-opened after discovery: "+err.Error())
			warn = true
		}
		if p.LoaderKind == pluginKindTesmio {
			if p.LegacyAPI != 0 && (p.LegacyAPI < 3 || p.LegacyAPI > 4) {
				p.Reasons = append(p.Reasons, fmt.Sprintf("Detected Tesmio API %d; the current compatibility shim supports API 3–4 and runtime initialization may fail", p.LegacyAPI))
				warn = true
			} else if p.LegacyAPI == 0 {
				p.Reasons = append(p.Reasons, "Tesmio lifecycle detected; API version will be verified inside WRSR")
			} else {
				p.Reasons = append(p.Reasons, fmt.Sprintf("Tesmio API %d compatibility shim available", p.LegacyAPI))
			}
		} else if p.LoaderKind == pluginKindRML {
			if p.ManifestPath != "" && (p.Manifest.LoaderAPI.Min > loaderAPI || p.Manifest.LoaderAPI.Max < 1 || p.Manifest.LoaderAPI.Max < p.Manifest.LoaderAPI.Min) {
				p.Reasons = append(p.Reasons, "Optional manifest API metadata does not match the current loader; the DLL's runtime descriptor will decide whether initialization succeeds")
				warn = true
			}
		} else {
			p.Reasons = append(p.Reasons, "No supported plugin lifecycle was detected; RML will attempt raw LoadLibrary behaviour only")
		}

		for _, dep := range p.Manifest.Dependencies {
			q, ok := byID[dep]
			if !ok || !q.Setting.Enabled {
				p.Reasons = append(p.Reasons, "Declared dependency is missing or disabled: "+dep)
				warn = true
			}
		}
		for _, symbol := range p.Manifest.RequiredSymbols {
			if _, known := loaderSymbolCatalog[symbol]; !known {
				p.Reasons = append(p.Reasons, "Declared RML symbol is not present in this loader build: "+symbol)
				warn = true
			}
		}
		for _, conflict := range p.Manifest.Conflicts {
			if q, ok := byID[conflict]; ok && q.Setting.Enabled {
				p.Reasons = append(p.Reasons, "Declared conflict is enabled: "+conflict)
				warn = true
			}
		}
		if game == nil || game.Path == "" {
			p.Reasons = append(p.Reasons, "SOVIET64.exe has not been selected; launch will fail until a game path is configured")
			warn = true
		} else if !game.PE64 {
			p.Reasons = append(p.Reasons, "Selected game executable is not an x64 PE")
			warn = true
		} else if game.Known && len(p.Manifest.SupportedGameVersions) > 0 && !containsString(p.Manifest.SupportedGameVersions, game.Version) {
			p.Reasons = append(p.Reasons, "Optional manifest does not list WRSR "+game.Version+"; RML will still attempt the plugin")
			warn = true
		}
		if !gameKnownOrNil(game) && len(p.Manifest.SupportedGameVersions) > 0 {
			p.Reasons = append(p.Reasons, "WRSR executable fingerprint is untested; plugin admission is unaffected")
			warn = true
		}
		if warn {
			p.Status = "WARNING"
			p.StatusClass = 1
		} else {
			p.Status = "VALID"
			p.StatusClass = 0
		}
	}

	// Duplicate identities/hashes warn without overriding the user's enable state.
	for id, owners := range activeByID {
		if len(owners) < 2 {
			continue
		}
		for _, p := range owners {
			if !p.BasicValid {
				continue
			}
			p.Reasons = append(p.Reasons, fmt.Sprintf("Duplicate enabled plugin id %s is present from multiple sources", id))
			if p.Status == "VALID" {
				p.Status = "WARNING"
				p.StatusClass = 1
			}
		}
	}
	for hash, owners := range activeByHash {
		if len(owners) < 2 {
			continue
		}
		for _, p := range owners {
			if !p.BasicValid || len(hash) < 12 {
				continue
			}
			p.Reasons = append(p.Reasons, fmt.Sprintf("Exact DLL SHA %s is enabled from multiple sources", hash[:12]))
			if p.Status == "VALID" {
				p.Status = "WARNING"
				p.StatusClass = 1
			}
		}
	}
}

func gameKnownOrNil(game *GameIdentity) bool {
	return game == nil || game.Known
}

func canLoad(p *Plugin) bool {
	return p != nil && p.BasicValid && p.Setting.Enabled
}

func calculateOrder(plugins []*Plugin, auto bool) ([]*Plugin, []string) {
	var active []*Plugin
	byID := map[string]*Plugin{}
	idCounts := map[string]int{}
	for _, p := range plugins {
		if canLoad(p) {
			active = append(active, p)
			byID[p.Manifest.ID] = p
			idCounts[p.Manifest.ID]++
		}
	}
	for id, count := range idCounts {
		if count > 1 {
			sort.SliceStable(active, func(i, j int) bool {
				if active[i].Setting.Priority != active[j].Setting.Priority {
					return active[i].Setting.Priority < active[j].Setting.Priority
				}
				if active[i].Manifest.ID != active[j].Manifest.ID {
					return active[i].Manifest.ID < active[j].Manifest.ID
				}
				return strings.ToLower(active[i].DLLPath) < strings.ToLower(active[j].DLLPath)
			})
			for i, p := range active {
				p.Order = i + 1
			}
			return active, []string{fmt.Sprintf("Duplicate enabled plugin id %s: dependency graph ordering is ambiguous, so all valid DLLs will be attempted in priority/path order", id)}
		}
	}
	if !auto {
		sort.SliceStable(active, func(i, j int) bool {
			if active[i].Setting.Priority != active[j].Setting.Priority {
				return active[i].Setting.Priority < active[j].Setting.Priority
			}
			return active[i].Manifest.ID < active[j].Manifest.ID
		})
		for i, p := range active {
			p.Order = i + 1
		}
		return active, manualOrderWarnings(active)
	}

	edges := map[string]map[string]bool{}
	indegree := map[string]int{}
	for id := range byID {
		edges[id] = map[string]bool{}
		indegree[id] = 0
	}
	addEdge := func(from, to string) {
		if from == to || byID[from] == nil || byID[to] == nil || edges[from][to] {
			return
		}
		edges[from][to] = true
		indegree[to]++
	}
	for _, p := range active {
		for _, dep := range p.Manifest.Dependencies {
			addEdge(dep, p.Manifest.ID)
		}
		for _, after := range p.Manifest.LoadAfter {
			addEdge(after, p.Manifest.ID)
		}
		for _, before := range p.Manifest.LoadBefore {
			addEdge(p.Manifest.ID, before)
		}
	}

	var ready []*Plugin
	for id, n := range indegree {
		if n == 0 {
			ready = append(ready, byID[id])
		}
	}
	sortReady := func() {
		sort.Slice(ready, func(i, j int) bool {
			if ready[i].Setting.Priority != ready[j].Setting.Priority {
				return ready[i].Setting.Priority < ready[j].Setting.Priority
			}
			return ready[i].Manifest.ID < ready[j].Manifest.ID
		})
	}
	sortReady()
	var result []*Plugin
	for len(ready) > 0 {
		p := ready[0]
		ready = ready[1:]
		result = append(result, p)
		for to := range edges[p.Manifest.ID] {
			indegree[to]--
			if indegree[to] == 0 {
				ready = append(ready, byID[to])
				sortReady()
			}
		}
	}
	if len(result) != len(active) {
		var cycle []string
		for id, n := range indegree {
			if n > 0 {
				cycle = append(cycle, id)
			}
		}
		sort.Strings(cycle)
		return result, []string{"Dependency/order cycle: " + strings.Join(cycle, " → ")}
	}
	for i, p := range result {
		p.Order = i + 1
	}
	return result, nil
}

func manualOrderWarnings(active []*Plugin) []string {
	index := map[string]int{}
	for i, p := range active {
		index[p.Manifest.ID] = i
	}
	var warnings []string
	for i, p := range active {
		for _, dep := range p.Manifest.Dependencies {
			if j, ok := index[dep]; ok && j > i {
				warnings = append(warnings, fmt.Sprintf("%s is manually ordered before dependency %s", p.Manifest.ID, dep))
			}
		}
		for _, after := range p.Manifest.LoadAfter {
			if j, ok := index[after]; ok && j > i {
				warnings = append(warnings, fmt.Sprintf("%s violates loadAfter %s", p.Manifest.ID, after))
			}
		}
		for _, before := range p.Manifest.LoadBefore {
			if j, ok := index[before]; ok && j < i {
				warnings = append(warnings, fmt.Sprintf("%s violates loadBefore %s", p.Manifest.ID, before))
			}
		}
	}
	return warnings
}

func loadRuntimeReport(path string) (*RuntimeReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var report RuntimeReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}
	return &report, nil
}

func selectedGameError(game *GameIdentity) error {
	if game == nil || game.Path == "" {
		return errors.New("select SOVIET64.exe")
	}
	if !game.PE64 {
		return errors.New("selected game executable is not x64")
	}
	return nil
}
