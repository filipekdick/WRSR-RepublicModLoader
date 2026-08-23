//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DevelopmentMod represents an unpublished WRSR Workshop project living in
// media_soviet\workshop_wip. WRSR itself only enumerates numeric child
// directories here, so RML mirrors that rule instead of surfacing folders the
// game will never consider loadable.
type DevelopmentMod struct {
	ID                string
	Name              string
	Description       string
	Folder            string
	Thumbnail         string
	ConfigPath        string
	Enabled           bool
	Issues            []string
	PluginDirs        []string
	EmbeddedPluginIDs []string
}

func developmentRootFromGamePath(gamePath string) string {
	if strings.TrimSpace(gamePath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(gamePath), "media_soviet", "workshop_wip")
}

func discoverDevelopmentMods(gamePath string, settings map[string]bool) ([]DevelopmentMod, []string) {
	root := developmentRootFromGamePath(gamePath)
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("Development mods: %s (%v)", root, err)}
	}

	mods := make([]DevelopmentMod, 0, len(entries))
	warnings := []string{}
	for _, entry := range entries {
		if !entry.IsDir() || !validWorkshopID(entry.Name()) {
			continue
		}
		id := entry.Name()
		folder := filepath.Join(root, id)
		mod := DevelopmentMod{
			ID: id, Folder: folder, Name: "Development Mod " + id, Enabled: false,
		}
		if enabled, ok := settings[id]; ok {
			mod.Enabled = enabled
		}
		// WRSR's workshop_wip enumerator checks this file at the numeric project
		// root specifically; unlike subscribed Workshop metadata, a nested config
		// does not make a local WIP project loadable. Mirror that exact rule.
		rootConfig := filepath.Join(folder, "workshopconfig.ini")
		if info, err := os.Stat(rootConfig); err == nil && !info.IsDir() {
			mod.ConfigPath = rootConfig
		}
		if mod.ConfigPath != "" {
			name, desc, metaID := readWorkshopConfig(mod.ConfigPath)
			if name != "" {
				mod.Name = name
			}
			if desc != "" {
				mod.Description = desc
			}
			if metaID != "" && metaID != id {
				mod.Issues = append(mod.Issues, "Metadata ITEM_ID is "+metaID+" but folder ID is "+id)
				warnings = append(warnings, fmt.Sprintf("Development mod %s metadata declares ITEM_ID %s", id, metaID))
			}
		} else {
			mod.Issues = append(mod.Issues, "No workshopconfig.ini found")
		}
		mod.Thumbnail = findWorkshopThumbnail(folder)
		mod.PluginDirs = findWorkshopPluginDirs(folder)
		if mod.Description == "" {
			mod.Description = "Local unpublished WRSR development project from media_soviet\\workshop_wip."
		}
		if mod.Thumbnail == "" {
			mod.Issues = append(mod.Issues, "No preview image found")
		}
		if workshopFolderEmpty(folder) {
			mod.Issues = append(mod.Issues, "Development folder is empty")
		}
		mods = append(mods, mod)
	}

	sort.SliceStable(mods, func(i, j int) bool {
		cmp := naturalCompareFold(mods[i].Name, mods[j].Name)
		if cmp == 0 {
			return workshopIDNumber(mods[i].ID) < workshopIDNumber(mods[j].ID)
		}
		return cmp < 0
	})
	return mods, warnings
}

func (a *application) developmentItemIndexByID(id string) int {
	for i := range a.developmentItems {
		if a.developmentItems[i].ID == id {
			return i
		}
	}
	return -1
}

func (a *application) selectedDevelopmentItem() *DevelopmentMod {
	if a.selectedDevelopment < 0 || a.selectedDevelopment >= len(a.developmentItems) {
		return nil
	}
	return &a.developmentItems[a.selectedDevelopment]
}

func (a *application) releaseDevelopmentImage() {
	if a.developmentImage != 0 {
		disposeGDIPlusImage(a.developmentImage)
	}
	a.developmentImage = 0
	a.developmentImagePath = ""
}

func (a *application) ensureDevelopmentImage() uintptr {
	item := a.selectedDevelopmentItem()
	path := ""
	if item != nil {
		path = item.Thumbnail
	}
	if path == a.developmentImagePath {
		return a.developmentImage
	}
	a.releaseDevelopmentImage()
	if path == "" {
		return 0
	}
	a.developmentImagePath = path
	a.developmentImage = loadGDIPlusImage(path)
	return a.developmentImage
}

func (a *application) visibleDevelopmentIndices() []int {
	out := make([]int, 0, len(a.developmentItems))
	query := strings.TrimSpace(a.developmentSearch)
	for i := range a.developmentItems {
		item := &a.developmentItems[i]
		if a.cfg.HideDisabledDevelopment && !item.Enabled {
			continue
		}
		if query != "" && !containsFold(item.Name+"\n"+item.ID+"\n"+item.Folder, query) {
			continue
		}
		out = append(out, i)
	}
	return out
}

func (a *application) allDevelopmentEnabled() bool {
	if len(a.developmentItems) == 0 {
		return false
	}
	for i := range a.developmentItems {
		if !a.developmentItems[i].Enabled {
			return false
		}
	}
	return true
}

func (a *application) enabledDevelopmentCount() int {
	n := 0
	for i := range a.developmentItems {
		if a.developmentItems[i].Enabled {
			n++
		}
	}
	return n
}

func (a *application) rebuildDevelopmentPluginInventory() {
	for i := range a.developmentItems {
		a.developmentItems[i].EmbeddedPluginIDs = nil
	}
	for _, p := range a.developmentPlugins {
		if p.DevelopmentID == "" {
			continue
		}
		i := a.developmentItemIndexByID(p.DevelopmentID)
		if i < 0 {
			continue
		}
		a.developmentItems[i].EmbeddedPluginIDs = append(a.developmentItems[i].EmbeddedPluginIDs, p.Manifest.ID)
	}
	for i := range a.developmentItems {
		sort.Strings(a.developmentItems[i].EmbeddedPluginIDs)
	}
}

func (a *application) syncDevelopmentPluginEnableStates() {
	enabled := map[string]bool{}
	for i := range a.developmentItems {
		enabled[a.developmentItems[i].ID] = a.developmentItems[i].Enabled
	}
	for _, p := range a.developmentPlugins {
		p.Setting.Enabled = enabled[p.DevelopmentID]
		p.Setting.Force = false
	}
}

func (a *application) setDevelopmentEnabled(id string, enabled bool) {
	if a.cfg.DevelopmentSettings == nil {
		a.cfg.DevelopmentSettings = map[string]bool{}
	}
	if i := a.developmentItemIndexByID(id); i >= 0 {
		a.developmentItems[i].Enabled = enabled
		a.cfg.DevelopmentSettings[id] = enabled
		if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
			if a.cfg.DefaultDevelopmentSettings == nil {
				a.cfg.DefaultDevelopmentSettings = map[string]bool{}
			}
			a.cfg.DefaultDevelopmentSettings[id] = enabled
		}
	}
	a.syncDevelopmentPluginEnableStates()
	_ = saveConfig(a.configPath, a.cfg)
	a.developmentScroll = 0
	a.normalizeModSelections()
}

func (a *application) setAllDevelopment(enabled bool) {
	if a.cfg.DevelopmentSettings == nil {
		a.cfg.DevelopmentSettings = map[string]bool{}
	}
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) && a.cfg.DefaultDevelopmentSettings == nil {
		a.cfg.DefaultDevelopmentSettings = map[string]bool{}
	}
	for i := range a.developmentItems {
		item := &a.developmentItems[i]
		item.Enabled = enabled
		a.cfg.DevelopmentSettings[item.ID] = enabled
		if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
			a.cfg.DefaultDevelopmentSettings[item.ID] = enabled
		}
	}
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
	}
	a.syncDevelopmentPluginEnableStates()
	_ = saveConfig(a.configPath, a.cfg)
	a.developmentScroll = 0
	a.normalizeModSelections()
}
