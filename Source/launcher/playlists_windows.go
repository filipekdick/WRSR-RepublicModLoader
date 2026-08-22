//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const defaultPlaylistName = "Default"

type playlistExchange struct {
	SchemaVersion int              `json:"schemaVersion"`
	Format        string           `json:"format"`
	Name          string           `json:"name"`
	WorkshopItems []string         `json:"workshopItems"`
	Plugins       []PlaylistPlugin `json:"plugins,omitempty"`
}

const playlistExchangeFormat = "Republic Mod Loader Playlist"

func (a *application) selectedPlaylistSnapshot() PluginPlaylist {
	if p := a.currentPlaylist(); p != nil {
		return *p
	}
	plugins := make([]PlaylistPlugin, 0, len(a.cfg.DefaultPluginSettings))
	for id, setting := range a.cfg.DefaultPluginSettings {
		if setting.Enabled {
			plugins = append(plugins, PlaylistPlugin{ID: id, Force: setting.Force, Priority: setting.Priority, Order: len(plugins)})
		}
	}
	workshop := make([]string, 0, len(a.cfg.DefaultWorkshopSettings))
	for id, enabled := range a.cfg.DefaultWorkshopSettings {
		if enabled && validWorkshopID(id) {
			workshop = append(workshop, id)
		}
	}
	sort.Strings(workshop)
	return PluginPlaylist{Name: defaultPlaylistName, Plugins: plugins, Workshop: workshop}
}

func safePlaylistFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "playlist"
	}
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`<>:"/\\|?*`, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	name = strings.Trim(strings.TrimSpace(b.String()), ".")
	if name == "" {
		return "playlist"
	}
	return name
}

func (a *application) exportSelectedPlaylist() {
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) || a.selectedPlaylistIndex() < 0 {
		a.status = "Default cannot be exported"
		return
	}
	p := a.selectedPlaylistSnapshot()
	data := playlistExchange{SchemaVersion: 1, Format: playlistExchangeFormat, Name: p.Name, WorkshopItems: append([]string{}, p.Workshop...), Plugins: append([]PlaylistPlugin{}, p.Plugins...)}
	sort.Strings(data.WorkshopItems)
	exports := filepath.Join(a.baseDir, "Exports")
	if err := os.MkdirAll(exports, 0o755); err != nil {
		a.status = "Could not create Exports folder: " + err.Error()
		return
	}
	path := savePlaylistDialog(a.hwnd, exports, safePlaylistFilename(p.Name)+".json")
	if path == "" {
		a.status = "Playlist export cancelled"
		return
	}
	if !strings.EqualFold(filepath.Ext(path), ".json") {
		path += ".json"
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err == nil {
		encoded = append(encoded, '\n')
		err = os.WriteFile(path, encoded, 0o644)
	}
	if err != nil {
		a.status = "Could not export playlist: " + err.Error()
		return
	}
	a.status = "Playlist exported: " + path
}

func (a *application) uniqueImportedPlaylistName(name string) string {
	name = strings.TrimSpace(name)
	if !validPlaylistName(name) {
		name = "Imported Playlist"
	}
	used := func(candidate string) bool {
		if strings.EqualFold(candidate, defaultPlaylistName) {
			return true
		}
		for _, p := range a.cfg.Playlists {
			if strings.EqualFold(p.Name, candidate) {
				return true
			}
		}
		return false
	}
	if !used(name) {
		return name
	}
	base := name
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)", base, i)
		if len(candidate) <= 96 && !used(candidate) {
			return candidate
		}
	}
	return ""
}

func (a *application) importPlaylistFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		a.status = "Could not read playlist: " + err.Error()
		return
	}
	if len(data) > 2*1024*1024 {
		a.status = "Playlist file is too large"
		return
	}
	var exchange playlistExchange
	if err := json.Unmarshal(data, &exchange); err != nil {
		a.status = "Invalid playlist JSON: " + err.Error()
		return
	}
	if exchange.SchemaVersion != 1 || exchange.Format != playlistExchangeFormat {
		a.status = "This is not a supported Republic Mod Loader playlist"
		return
	}
	if len(a.cfg.Playlists) >= 64 {
		a.status = "Playlist limit reached (64)"
		return
	}
	seen := map[string]bool{}
	workshop := make([]string, 0, len(exchange.WorkshopItems))
	for _, id := range exchange.WorkshopItems {
		id = strings.TrimSpace(id)
		if validWorkshopID(id) && !seen[id] {
			seen[id] = true
			workshop = append(workshop, id)
		}
	}
	if len(workshop) > 4096 {
		a.status = "Playlist contains too many Workshop items"
		return
	}
	sort.Strings(workshop)
	name := a.uniqueImportedPlaylistName(exchange.Name)
	if name == "" {
		a.status = "Could not create a unique playlist name"
		return
	}
	plugins := make([]PlaylistPlugin, 0, minIntModel(len(exchange.Plugins), 128))
	seenPlugins := map[string]bool{}
	for _, item := range exchange.Plugins {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" || seenPlugins[item.ID] || len(plugins) >= 128 {
			continue
		}
		seenPlugins[item.ID] = true
		item.Priority = clampPriority(item.Priority)
		item.Order = len(plugins)
		plugins = append(plugins, item)
	}
	a.cfg.Playlists = append(a.cfg.Playlists, PluginPlaylist{Name: name, Plugins: plugins, Workshop: workshop})
	sort.SliceStable(a.cfg.Playlists, func(i, j int) bool {
		return strings.ToLower(a.cfg.Playlists[i].Name) < strings.ToLower(a.cfg.Playlists[j].Name)
	})
	a.cfg.SelectedPlaylist = name
	a.restoreSelectedPlaylistConfig()
	_ = saveConfig(a.configPath, a.cfg)
	a.playlistImportModal = false
	outcome := subscribeWorkshopItems(a.cfg.GamePath, workshop)
	a.refresh(true)
	if outcome.Failed > 0 {
		a.status = fmt.Sprintf("Imported %q; Steam subscription requested for %d item(s), %d failed", name, outcome.Requested, outcome.Failed)
	} else {
		a.status = fmt.Sprintf("Imported %q; Steam subscription requested for %d Workshop item(s)", name, outcome.Requested)
	}
}

func (a *application) openPlaylistRename() {
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) || a.selectedPlaylistIndex() < 0 {
		a.status = "Default cannot be renamed"
		return
	}
	a.playlistModalMode = "rename"
	a.playlistDraft = a.cfg.SelectedPlaylist
	a.playlistModal = true
	a.playlistDropdown = false
}

func (a *application) renamePlaylistDraft() {
	old := a.cfg.SelectedPlaylist
	name := strings.TrimSpace(a.playlistDraft)
	if !validPlaylistName(name) {
		a.status = "Playlist name must be 1–96 printable characters and cannot be Default"
		return
	}
	for _, p := range a.cfg.Playlists {
		if !strings.EqualFold(p.Name, old) && strings.EqualFold(p.Name, name) {
			a.status = "A playlist with that name already exists"
			return
		}
	}
	i := a.selectedPlaylistIndex()
	if i < 0 {
		a.status = "Default cannot be renamed"
		return
	}
	a.cfg.Playlists[i].Name = name
	a.cfg.SelectedPlaylist = name
	a.playlistModal = false
	a.playlistModalMode = ""
	a.playlistDraft = ""
	sort.SliceStable(a.cfg.Playlists, func(i, j int) bool {
		return strings.ToLower(a.cfg.Playlists[i].Name) < strings.ToLower(a.cfg.Playlists[j].Name)
	})
	_ = saveConfig(a.configPath, a.cfg)
	a.status = fmt.Sprintf("Playlist renamed: %q → %q", old, name)
}

func (a *application) selectedPlaylistIndex() int {
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) || a.cfg.SelectedPlaylist == "" {
		return -1
	}
	for i := range a.cfg.Playlists {
		if a.cfg.Playlists[i].Name == a.cfg.SelectedPlaylist {
			return i
		}
	}
	return -1
}

func (a *application) currentPlaylist() *PluginPlaylist {
	i := a.selectedPlaylistIndex()
	if i < 0 || i >= len(a.cfg.Playlists) {
		return nil
	}
	return &a.cfg.Playlists[i]
}

func (a *application) playlistNames() []string {
	names := make([]string, 0, len(a.cfg.Playlists)+1)
	names = append(names, defaultPlaylistName)
	for _, p := range a.cfg.Playlists {
		if p.Name != "" && !strings.EqualFold(p.Name, defaultPlaylistName) {
			names = append(names, p.Name)
		}
	}
	saved := names[1:]
	sort.SliceStable(saved, func(i, j int) bool {
		return strings.ToLower(saved[i]) < strings.ToLower(saved[j])
	})
	return names
}

func (a *application) captureDefaultConfiguration() {
	a.cfg.DefaultPluginSettings = clonePluginSettings(a.cfg.PluginSettings)
	a.cfg.DefaultWorkshopSettings = cloneWorkshopSettings(a.cfg.WorkshopSettings)
	a.cfg.DefaultDevelopmentSettings = cloneDevelopmentSettings(a.cfg.DevelopmentSettings)
	a.cfg.DefaultAutoOrder = a.cfg.AutoOrder
}

func (a *application) syncDefaultNewPlugins() {
	if a.cfg.DefaultPluginSettings == nil {
		a.cfg.DefaultPluginSettings = map[string]PluginSetting{}
	}
	for _, p := range a.plugins {
		if _, exists := a.cfg.DefaultPluginSettings[p.Manifest.ID]; !exists {
			a.cfg.DefaultPluginSettings[p.Manifest.ID] = p.Setting
		}
	}
}

func (a *application) syncDefaultNewWorkshop() {
	if a.cfg.WorkshopSettings == nil {
		a.cfg.WorkshopSettings = map[string]bool{}
	}
	if a.cfg.DefaultWorkshopSettings == nil {
		a.cfg.DefaultWorkshopSettings = map[string]bool{}
	}
	defaultActive := strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName)
	for i := range a.workshopItems {
		id := a.workshopItems[i].ID
		if _, exists := a.cfg.DefaultWorkshopSettings[id]; !exists {
			a.cfg.DefaultWorkshopSettings[id] = true
		}
		if _, exists := a.cfg.WorkshopSettings[id]; !exists {
			if defaultActive {
				a.cfg.WorkshopSettings[id] = a.cfg.DefaultWorkshopSettings[id]
			} else {
				a.cfg.WorkshopSettings[id] = false
			}
		}
		a.workshopItems[i].Enabled = a.cfg.WorkshopSettings[id]
	}
}

func (a *application) syncDefaultNewDevelopment() {
	if a.cfg.DevelopmentSettings == nil {
		a.cfg.DevelopmentSettings = map[string]bool{}
	}
	if a.cfg.DefaultDevelopmentSettings == nil {
		a.cfg.DefaultDevelopmentSettings = map[string]bool{}
	}
	for i := range a.developmentItems {
		id := a.developmentItems[i].ID
		if _, exists := a.cfg.DefaultDevelopmentSettings[id]; !exists {
			// Local authoring projects are opt-in. Merely creating a workshop_wip
			// folder must never make that project enter an RML launch automatically.
			a.cfg.DefaultDevelopmentSettings[id] = false
		}
		if _, exists := a.cfg.DevelopmentSettings[id]; !exists {
			a.cfg.DevelopmentSettings[id] = false
		}
		a.developmentItems[i].Enabled = a.cfg.DevelopmentSettings[id]
	}
}

// restoreSelectedPlaylistConfig is intentionally configuration-only: it can run
// before plugin discovery. This makes the selected playlist authoritative again
// on the next launcher start instead of leaving stale settings from another set.
func (a *application) restoreSelectedPlaylistConfig() {
	if a.cfg.PluginSettings == nil {
		a.cfg.PluginSettings = map[string]PluginSetting{}
	}
	if a.cfg.SelectedPlaylist == "" || strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
		a.cfg.SelectedPlaylist = defaultPlaylistName
		a.cfg.PluginSettings = clonePluginSettings(a.cfg.DefaultPluginSettings)
		a.cfg.WorkshopSettings = cloneWorkshopSettings(a.cfg.DefaultWorkshopSettings)
		a.cfg.DevelopmentSettings = cloneDevelopmentSettings(a.cfg.DefaultDevelopmentSettings)
		a.cfg.AutoOrder = a.cfg.DefaultAutoOrder
		return
	}
	var playlist *PluginPlaylist
	for i := range a.cfg.Playlists {
		if a.cfg.Playlists[i].Name == a.cfg.SelectedPlaylist {
			playlist = &a.cfg.Playlists[i]
			break
		}
	}
	if playlist == nil {
		a.cfg.SelectedPlaylist = defaultPlaylistName
		a.cfg.PluginSettings = clonePluginSettings(a.cfg.DefaultPluginSettings)
		a.cfg.WorkshopSettings = cloneWorkshopSettings(a.cfg.DefaultWorkshopSettings)
		a.cfg.DevelopmentSettings = cloneDevelopmentSettings(a.cfg.DefaultDevelopmentSettings)
		a.cfg.AutoOrder = a.cfg.DefaultAutoOrder
		return
	}
	// Preserve known IDs in the config but disable them before applying the
	// playlist. Missing/uninstalled IDs can remain in the map for later return.
	for id, setting := range a.cfg.PluginSettings {
		setting.Enabled = false
		setting.Force = false
		a.cfg.PluginSettings[id] = setting
	}
	for i, entry := range playlist.Plugins {
		setting := a.cfg.PluginSettings[entry.ID]
		setting.Enabled = true
		setting.Force = entry.Force
		// Playlist order is explicit. Space priorities to preserve that order even
		// where the original plugin priorities were tied.
		setting.Priority = clampPriority(100 + i*100)
		a.cfg.PluginSettings[entry.ID] = setting
	}
	for id := range a.cfg.WorkshopSettings {
		a.cfg.WorkshopSettings[id] = false
	}
	for _, id := range playlist.Workshop {
		if validWorkshopID(id) {
			a.cfg.WorkshopSettings[id] = true
		}
	}
	for id := range a.cfg.DevelopmentSettings {
		a.cfg.DevelopmentSettings[id] = false
	}
	for _, id := range playlist.Development {
		if validWorkshopID(id) {
			a.cfg.DevelopmentSettings[id] = true
		}
	}
	a.cfg.AutoOrder = false
}

func (a *application) selectPlaylist(name string) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, defaultPlaylistName) {
		a.cfg.SelectedPlaylist = defaultPlaylistName
	} else {
		found := false
		for _, p := range a.cfg.Playlists {
			if p.Name == name {
				found = true
				break
			}
		}
		if !found {
			a.status = "Playlist no longer exists: " + name
			return
		}
		a.cfg.SelectedPlaylist = name
	}
	a.restoreSelectedPlaylistConfig()
	_ = saveConfig(a.configPath, a.cfg)
	a.refresh(true)
	if a.cfg.SelectedPlaylist == defaultPlaylistName {
		a.status = "Default mod configuration active"
	} else {
		missing := 0
		if p := a.currentPlaylist(); p != nil {
			installed := map[string]bool{}
			for _, plug := range a.plugins {
				installed[plug.Manifest.ID] = true
			}
			for _, entry := range p.Plugins {
				if !installed[entry.ID] {
					missing++
				}
			}
		}
		if missing > 0 {
			a.status = fmt.Sprintf("Playlist %q active; %d saved plugin(s) are not currently installed", a.cfg.SelectedPlaylist, missing)
		} else {
			a.status = "Playlist active: " + a.cfg.SelectedPlaylist
		}
	}
}

func (a *application) selectPlaylistOption(index int) {
	names := a.playlistNames()
	if index < 0 || index >= len(names) {
		return
	}
	a.playlistDropdown = false
	a.selectPlaylist(names[index])
}

func (a *application) openPlaylistSave() {
	a.playlistModalMode = "save"
	draft := ""
	if p := a.currentPlaylist(); p != nil {
		draft = p.Name
	}
	if draft == "" {
		draft = fmt.Sprintf("Playlist %d", len(a.cfg.Playlists)+1)
	}
	a.playlistDraft = draft
	a.playlistModal = true
	a.playlistDropdown = false
	a.status = "Enter a playlist name and press Enter or Save"
}

func validPlaylistName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, defaultPlaylistName) || len(name) > 96 || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (a *application) playlistEntriesFromCurrentConfiguration() []PlaylistPlugin {
	entries := make([]PlaylistPlugin, 0, len(a.plugins))
	seen := map[string]bool{}
	// Start with the actual effective load sequence so Auto Order playlists
	// snapshot the recommendation the user was looking at when they saved it.
	for _, p := range a.ordered {
		if !p.Setting.Enabled || seen[p.Manifest.ID] {
			continue
		}
		seen[p.Manifest.ID] = true
		entries = append(entries, PlaylistPlugin{ID: p.Manifest.ID, Force: p.Setting.Force, Priority: p.Setting.Priority, Order: len(entries)})
	}
	// Preserve enabled-but-currently-unloadable mods as well; their playlist
	// should not silently forget them merely because today's validation failed.
	for _, p := range a.plugins {
		if !p.Setting.Enabled || seen[p.Manifest.ID] {
			continue
		}
		seen[p.Manifest.ID] = true
		entries = append(entries, PlaylistPlugin{ID: p.Manifest.ID, Force: p.Setting.Force, Priority: p.Setting.Priority, Order: len(entries)})
	}
	return entries
}

func (a *application) savePlaylistDraft() {
	if a.playlistModalMode == "rename" {
		a.renamePlaylistDraft()
		return
	}
	name := strings.TrimSpace(a.playlistDraft)
	if !validPlaylistName(name) {
		a.status = "Playlist name must be 1–96 printable characters and cannot be Default"
		return
	}
	entries := a.playlistEntriesFromCurrentConfiguration()
	workshop := make([]string, 0, len(a.workshopItems))
	for _, item := range a.workshopItems {
		if a.cfg.WorkshopSettings[item.ID] {
			workshop = append(workshop, item.ID)
		}
	}
	development := make([]string, 0, len(a.developmentItems))
	for _, item := range a.developmentItems {
		if a.cfg.DevelopmentSettings[item.ID] {
			development = append(development, item.ID)
		}
	}
	replacement := PluginPlaylist{Name: name, Plugins: entries, Workshop: workshop, Development: development}
	found := -1
	for i := range a.cfg.Playlists {
		if strings.EqualFold(a.cfg.Playlists[i].Name, name) {
			found = i
			break
		}
	}
	if found >= 0 {
		a.cfg.Playlists[found] = replacement
	} else {
		if len(a.cfg.Playlists) >= 64 {
			a.status = "Playlist limit reached (64)"
			return
		}
		a.cfg.Playlists = append(a.cfg.Playlists, replacement)
	}
	sort.SliceStable(a.cfg.Playlists, func(i, j int) bool {
		return strings.ToLower(a.cfg.Playlists[i].Name) < strings.ToLower(a.cfg.Playlists[j].Name)
	})
	a.cfg.SelectedPlaylist = name
	a.playlistModal = false
	a.playlistModalMode = ""
	a.playlistDraft = ""
	_ = saveConfig(a.configPath, a.cfg)
	a.status = fmt.Sprintf("Playlist %q saved: %d plugin(s), %d Workshop item(s), %d development mod(s)", name, len(entries), len(workshop), len(development))
}

func (a *application) requestDeleteSelectedPlaylist() {
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) || a.selectedPlaylistIndex() < 0 {
		a.status = "Default is the baseline configuration and cannot be deleted"
		return
	}
	a.playlistDeleteName = a.cfg.SelectedPlaylist
	a.playlistDeleteModal = true
	a.playlistDropdown = false
}

func (a *application) confirmDeleteSelectedPlaylist() {
	name := a.playlistDeleteName
	if name == "" {
		a.playlistDeleteModal = false
		return
	}
	found := -1
	for i := range a.cfg.Playlists {
		if a.cfg.Playlists[i].Name == name {
			found = i
			break
		}
	}
	if found >= 0 {
		a.cfg.Playlists = append(a.cfg.Playlists[:found], a.cfg.Playlists[found+1:]...)
	}
	a.playlistDeleteModal = false
	a.playlistDeleteName = ""
	a.cfg.SelectedPlaylist = defaultPlaylistName
	a.restoreSelectedPlaylistConfig()
	_ = saveConfig(a.configPath, a.cfg)
	a.refresh(true)
	a.status = "Deleted playlist: " + name + " — Default configuration restored"
}

func (a *application) playlistKey(r rune) {
	if !a.playlistModal {
		return
	}
	switch r {
	case 27: // Escape
		a.playlistModal = false
		a.playlistDraft = ""
		a.status = "Playlist save cancelled"
	case 13: // Enter
		a.savePlaylistDraft()
	case 8: // Backspace
		rr := []rune(a.playlistDraft)
		if len(rr) > 0 {
			a.playlistDraft = string(rr[:len(rr)-1])
		}
	default:
		if r >= 0x20 && r != 0x7f && len([]rune(a.playlistDraft)) < 48 {
			a.playlistDraft += string(r)
		}
	}
	invalidateWindow(a.hwnd)
}
