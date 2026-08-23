//go:build windows

package main

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func containsFold(haystack, needle string) bool {
	needle = strings.TrimSpace(strings.ToLower(needle))
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(haystack), needle)
}

// naturalCompareFold compares names case-insensitively while treating runs of
// decimal digits as numbers. This keeps human series such as "Brick house 9"
// before "Brick house 10" instead of lexicographically placing 10 before 9.
func naturalCompareFold(a, b string) int {
	x, y := strings.ToLower(a), strings.ToLower(b)
	for i, j := 0, 0; i < len(x) || j < len(y); {
		if i >= len(x) {
			return -1
		}
		if j >= len(y) {
			return 1
		}
		if x[i] >= '0' && x[i] <= '9' && y[j] >= '0' && y[j] <= '9' {
			i0, j0 := i, j
			for i < len(x) && x[i] >= '0' && x[i] <= '9' {
				i++
			}
			for j < len(y) && y[j] >= '0' && y[j] <= '9' {
				j++
			}
			xr, yr := x[i0:i], y[j0:j]
			xs, ys := strings.TrimLeft(xr, "0"), strings.TrimLeft(yr, "0")
			if xs == "" {
				xs = "0"
			}
			if ys == "" {
				ys = "0"
			}
			if len(xs) < len(ys) {
				return -1
			}
			if len(xs) > len(ys) {
				return 1
			}
			if xs < ys {
				return -1
			}
			if xs > ys {
				return 1
			}
			// Numeric value is equal. Prefer fewer leading zeroes, then continue.
			if len(xr) < len(yr) {
				return -1
			}
			if len(xr) > len(yr) {
				return 1
			}
			continue
		}
		if x[i] < y[j] {
			return -1
		}
		if x[i] > y[j] {
			return 1
		}
		i++
		j++
	}
	return 0
}

func workshopUpdateNeedsAttention(item *WorkshopItem) bool {
	if item == nil {
		return false
	}
	if item.Unsupported || item.Unsubscribed {
		return true
	}
	switch item.UpdateState {
	case "update", "stale", "missing", "unavailable":
		return true
	}
	return false
}

func (a *application) pluginWorkshopItem(p *Plugin) *WorkshopItem {
	if p == nil || !validWorkshopID(p.WorkshopID) {
		return nil
	}
	return a.workshopItemByID(p.WorkshopID)
}

func (a *application) pluginNeedsAttention(p *Plugin) bool {
	if p == nil {
		return false
	}
	if !p.BasicValid || p.Status == "WARNING" {
		return true
	}
	return workshopUpdateNeedsAttention(a.pluginWorkshopItem(p))
}

func workshopUpdateShortLabel(item *WorkshopItem) string {
	if item == nil {
		return ""
	}
	switch item.UpdateState {
	case "current":
		return "up to date"
	case "update":
		return "update available"
	case "stale":
		return "potentially stale"
	case "downloading":
		return "update downloading"
	case "missing":
		return "installed copy missing"
	case "unavailable":
		return "Workshop unavailable"
	case "unknown":
		return "update status unknown"
	}
	if item.Unsupported {
		return "needs attention"
	}
	return "not yet scanned"
}

func (a *application) visiblePluginIndices() []int {
	out := make([]int, 0, len(a.plugins))
	query := strings.TrimSpace(a.pluginSearch)
	for i, p := range a.plugins {
		if a.cfg.HideDisabledPlugins && !p.Setting.Enabled && p.BasicValid {
			continue
		}
		if a.cfg.PluginNeedsAttentionOnly && !a.pluginNeedsAttention(p) {
			continue
		}
		if query != "" {
			hay := p.Manifest.Name + "\n" + p.Manifest.ID + "\n" + filepath.Base(p.DLLPath) + "\n" + p.WorkshopID
			if !containsFold(hay, query) {
				continue
			}
		}
		out = append(out, i)
	}
	return out
}

func workshopIDNumber(id string) uint64 {
	n, _ := strconv.ParseUint(id, 10, 64)
	return n
}

func (a *application) visibleWorkshopIndices() []int {
	out := make([]int, 0, len(a.workshopItems))
	query := strings.TrimSpace(a.workshopSearch)
	for i := range a.workshopItems {
		item := &a.workshopItems[i]
		if a.cfg.HideDisabledWorkshop && !item.Enabled {
			continue
		}
		if a.cfg.WorkshopNeedsAttentionOnly && !workshopUpdateNeedsAttention(item) {
			continue
		}
		if a.cfg.WorkshopIncludesPluginOnly && len(item.PluginDirs) == 0 {
			continue
		}
		if query != "" && !containsFold(item.Name+"\n"+item.ID, query) {
			continue
		}
		out = append(out, i)
	}
	if a.workshopSort == "" {
		return out
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := &a.workshopItems[out[i]], &a.workshopItems[out[j]]
		cmp := 0
		switch a.workshopSort {
		case "id":
			x, y := workshopIDNumber(ai.ID), workshopIDNumber(aj.ID)
			if x < y {
				cmp = -1
			} else if x > y {
				cmp = 1
			}
		case "on":
			if ai.Enabled != aj.Enabled {
				if ai.Enabled {
					cmp = 1
				} else {
					cmp = -1
				}
			}
		default: // name
			cmp = naturalCompareFold(ai.Name, aj.Name)
		}
		if cmp == 0 {
			x, y := workshopIDNumber(ai.ID), workshopIDNumber(aj.ID)
			if x < y {
				cmp = -1
			} else if x > y {
				cmp = 1
			}
		}
		if a.workshopSortDesc {
			return cmp > 0
		}
		return cmp < 0
	})
	return out
}

func clampListScroll(offset, total, visible int) int {
	if visible < 1 {
		visible = 1
	}
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset < 0 {
		return 0
	}
	if offset > maxOffset {
		return maxOffset
	}
	return offset
}

func indexVisible(list []int, value int) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func visiblePosition(list []int, value int) int {
	for i, v := range list {
		if v == value {
			return i
		}
	}
	return -1
}

func (a *application) moveActiveModSelection(delta int) {
	if delta == 0 {
		return
	}
	visibleRows := a.modScrollVisible
	if visibleRows < 1 {
		visibleRows = 1
	}
	if a.modView == 1 {
		list := a.visibleWorkshopIndices()
		if len(list) == 0 {
			return
		}
		pos := visiblePosition(list, a.selectedWorkshop)
		if pos < 0 {
			pos = 0
		} else {
			pos += delta
		}
		if pos < 0 {
			pos = 0
		}
		if pos >= len(list) {
			pos = len(list) - 1
		}
		if a.selectedWorkshop != list[pos] {
			a.releaseWorkshopImage()
			a.workshopDetailScroll = 0
		}
		a.selectedWorkshop = list[pos]
		if pos < a.workshopScroll {
			a.workshopScroll = pos
		}
		if pos >= a.workshopScroll+visibleRows {
			a.workshopScroll = pos - visibleRows + 1
		}
		a.workshopScroll = clampListScroll(a.workshopScroll, len(list), visibleRows)
		return
	}
	if a.modView == 2 {
		list := a.visibleDevelopmentIndices()
		if len(list) == 0 {
			return
		}
		pos := visiblePosition(list, a.selectedDevelopment)
		if pos < 0 {
			pos = 0
		} else {
			pos += delta
		}
		if pos < 0 {
			pos = 0
		}
		if pos >= len(list) {
			pos = len(list) - 1
		}
		if a.selectedDevelopment != list[pos] {
			a.releaseDevelopmentImage()
			a.developmentDetailScroll = 0
		}
		a.selectedDevelopment = list[pos]
		if pos < a.developmentScroll {
			a.developmentScroll = pos
		}
		if pos >= a.developmentScroll+visibleRows {
			a.developmentScroll = pos - visibleRows + 1
		}
		a.developmentScroll = clampListScroll(a.developmentScroll, len(list), visibleRows)
		return
	}
	list := a.visiblePluginIndices()
	if len(list) == 0 {
		return
	}
	pos := visiblePosition(list, a.selected)
	if pos < 0 {
		pos = 0
	} else {
		pos += delta
	}
	if pos < 0 {
		pos = 0
	}
	if pos >= len(list) {
		pos = len(list) - 1
	}
	if a.selected != list[pos] {
		a.pluginDetailScroll = 0
		a.releasePluginImage()
	}
	a.selected = list[pos]
	if pos < a.pluginScroll {
		a.pluginScroll = pos
	}
	if pos >= a.pluginScroll+visibleRows {
		a.pluginScroll = pos - visibleRows + 1
	}
	a.pluginScroll = clampListScroll(a.pluginScroll, len(list), visibleRows)
}

func (a *application) requestSelectedModUnsubscribe() {
	if a.modView == 1 {
		a.requestWorkshopUnsubscribe()
		return
	}
	if a.modView == 2 {
		a.status = "Development mods are local projects; RML will not delete them"
		return
	}
	if p := a.selectedPlugin(); p != nil && validWorkshopID(p.WorkshopID) {
		a.requestWorkshopUnsubscribeByID(p.WorkshopID, p.Manifest.Name)
	} else {
		a.status = "Selected plugin is not supplied by a Steam Workshop subscription"
	}
}

func (a *application) normalizeModSelections() {
	pv := a.visiblePluginIndices()
	if len(pv) == 0 {
		a.selected = -1
		a.pluginScroll = 0
		a.releasePluginImage()
	} else if !indexVisible(pv, a.selected) {
		a.selected = pv[0]
		a.pluginScroll = 0
		a.pluginDetailScroll = 0
		a.releasePluginImage()
	}
	wv := a.visibleWorkshopIndices()
	if len(wv) == 0 {
		a.selectedWorkshop = -1
		a.workshopScroll = 0
		a.releaseWorkshopImage()
	} else if !indexVisible(wv, a.selectedWorkshop) {
		a.selectedWorkshop = wv[0]
		a.workshopScroll = 0
		a.workshopDetailScroll = 0
		a.releaseWorkshopImage()
	}
	dv := a.visibleDevelopmentIndices()
	if len(dv) == 0 {
		a.selectedDevelopment = -1
		a.developmentScroll = 0
		a.releaseDevelopmentImage()
	} else if !indexVisible(dv, a.selectedDevelopment) {
		a.selectedDevelopment = dv[0]
		a.developmentScroll = 0
		a.developmentDetailScroll = 0
		a.releaseDevelopmentImage()
	}
}

func (a *application) activeSearchText() string {
	switch a.modView {
	case 1:
		return a.workshopSearch
	case 2:
		return a.developmentSearch
	default:
		return a.pluginSearch
	}
}

func (a *application) editModSearch(r rune) {
	if !a.modSearchFocus {
		return
	}
	var target *string
	switch a.modView {
	case 1:
		target = &a.workshopSearch
	case 2:
		target = &a.developmentSearch
	default:
		target = &a.pluginSearch
	}
	switch r {
	case '\b':
		rr := []rune(*target)
		if len(rr) > 0 {
			*target = string(rr[:len(rr)-1])
		}
	case '\r', '\n':
		a.modSearchFocus = false
	default:
		if r >= 0x20 && r != 0x7f {
			rr := []rune(*target)
			if len(rr) < 96 {
				*target += string(r)
			}
		}
	}
	switch a.modView {
	case 1:
		a.workshopScroll = 0
	case 2:
		a.developmentScroll = 0
	default:
		a.pluginScroll = 0
	}
	a.normalizeModSelections()
}

func (a *application) clearModSearch() {
	switch a.modView {
	case 1:
		a.workshopSearch = ""
		a.workshopScroll = 0
	case 2:
		a.developmentSearch = ""
		a.developmentScroll = 0
	default:
		a.pluginSearch = ""
		a.pluginScroll = 0
	}
	a.normalizeModSelections()
}

func clampPixelScroll(offset, total, visible int) int {
	maxOffset := total - visible
	if maxOffset < 0 {
		maxOffset = 0
	}
	if offset < 0 {
		return 0
	}
	if offset > maxOffset {
		return maxOffset
	}
	return offset
}

func (a *application) detailScrollPointer(view int) *int {
	switch view {
	case 1:
		return &a.workshopDetailScroll
	case 2:
		return &a.developmentDetailScroll
	default:
		return &a.pluginDetailScroll
	}
}

func (a *application) scrollActiveDetail(delta int) {
	if delta == 0 || a.detailScrollTotal <= a.detailScrollVisible {
		return
	}
	step := 42
	if delta > 0 {
		step = -step
	}
	p := a.detailScrollPointer(a.detailScrollView)
	*p = clampPixelScroll(*p+step, a.detailScrollTotal, a.detailScrollVisible)
}

func (a *application) beginDetailScrollDrag(view, mouseY int) {
	if a.detailScrollTotal <= a.detailScrollVisible || a.detailScrollThumbH >= a.detailScrollH {
		return
	}
	a.detailScrollDrag = true
	a.detailScrollDragView = view
	a.detailScrollDragY = mouseY
	a.detailScrollDragOff = *a.detailScrollPointer(view)
}

func (a *application) updateDetailScrollDrag(mouseY int) {
	if !a.detailScrollDrag {
		return
	}
	travel := a.detailScrollH - a.detailScrollThumbH
	maxOffset := a.detailScrollTotal - a.detailScrollVisible
	if travel <= 0 || maxOffset <= 0 {
		return
	}
	delta := mouseY - a.detailScrollDragY
	p := a.detailScrollPointer(a.detailScrollDragView)
	*p = clampPixelScroll(a.detailScrollDragOff+int(float64(delta)*float64(maxOffset)/float64(travel)), a.detailScrollTotal, a.detailScrollVisible)
}

func (a *application) endDetailScrollDrag() { a.detailScrollDrag = false }

func (a *application) scrollActiveMod(delta int, page bool) {
	if delta == 0 {
		return
	}
	step := 3
	if page {
		step = a.modScrollVisible - 1
		if step < 1 {
			step = 8
		}
	}
	if delta > 0 {
		step = -step
	}
	visibleRows := a.modScrollVisible
	if visibleRows < 1 {
		visibleRows = 1
	}
	switch a.modView {
	case 1:
		a.workshopScroll += step
		a.workshopScroll = clampListScroll(a.workshopScroll, len(a.visibleWorkshopIndices()), visibleRows)
	case 2:
		a.developmentScroll += step
		a.developmentScroll = clampListScroll(a.developmentScroll, len(a.visibleDevelopmentIndices()), visibleRows)
	default:
		a.pluginScroll += step
		a.pluginScroll = clampListScroll(a.pluginScroll, len(a.visiblePluginIndices()), visibleRows)
	}
}

func (a *application) toggleWorkshopSort(column string) {
	if a.workshopSort == column {
		a.workshopSortDesc = !a.workshopSortDesc
	} else {
		a.workshopSort = column
		// User-facing first click: high/ON/Z-to-A first, then reverse.
		a.workshopSortDesc = true
	}
	a.workshopScroll = 0
	a.normalizeModSelections()
}

func workshopSortLabel(label, column string, a *application) string {
	if a.workshopSort != column {
		return label
	}
	if a.workshopSortDesc {
		return label + "  ▼"
	}
	return label + "  ▲"
}

func (a *application) allPluginsEnabled() bool {
	if len(a.plugins) == 0 {
		return false
	}
	for _, p := range a.plugins {
		if !p.Setting.Enabled {
			return false
		}
	}
	return true
}

func (a *application) allWorkshopEnabled() bool {
	if len(a.workshopItems) == 0 {
		return false
	}
	for i := range a.workshopItems {
		if !a.workshopItems[i].Enabled {
			return false
		}
	}
	return true
}

func (a *application) setAllPlugins(enabled bool) {
	if a.cfg.PluginSettings == nil {
		a.cfg.PluginSettings = map[string]PluginSetting{}
	}
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) && a.cfg.DefaultPluginSettings == nil {
		a.cfg.DefaultPluginSettings = map[string]PluginSetting{}
	}
	linked := map[string]bool{}
	for _, p := range a.plugins {
		p.Setting.Enabled = enabled
		if !enabled {
			p.Setting.Force = false
		}
		a.cfg.PluginSettings[p.Manifest.ID] = p.Setting
		if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
			a.cfg.DefaultPluginSettings[p.Manifest.ID] = p.Setting
		}
		if p.WorkshopID != "" {
			linked[p.WorkshopID] = true
		}
	}
	for wid := range linked {
		if i := a.workshopItemIndexByID(wid); i >= 0 {
			a.workshopItems[i].Enabled = enabled
		}
		a.cfg.WorkshopSettings[wid] = enabled
		if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
			if a.cfg.DefaultWorkshopSettings == nil {
				a.cfg.DefaultWorkshopSettings = map[string]bool{}
			}
			a.cfg.DefaultWorkshopSettings[wid] = enabled
		}
	}
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
		a.cfg.DefaultAutoOrder = a.cfg.AutoOrder
	}
	_ = saveConfig(a.configPath, a.cfg)
	validatePlugins(a.plugins, a.game, a.cfg.AllowProbable)
	a.ordered, a.orderWarnings = calculateOrder(a.plugins, a.cfg.AutoOrder)
	a.compatibility, a.compatWarnings = analyseCompatibility(a.baseDir, a.knowledgeRoot, a.pluginDir, a.plugins, a.symbolCache, a.report)
	a.pluginScroll = 0
	a.normalizeModSelections()
}

func (a *application) setAllWorkshop(enabled bool) {
	if a.cfg.WorkshopSettings == nil {
		a.cfg.WorkshopSettings = map[string]bool{}
	}
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) && a.cfg.DefaultWorkshopSettings == nil {
		a.cfg.DefaultWorkshopSettings = map[string]bool{}
	}
	for i := range a.workshopItems {
		a.workshopItems[i].Enabled = enabled
		a.cfg.WorkshopSettings[a.workshopItems[i].ID] = enabled
		if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
			a.cfg.DefaultWorkshopSettings[a.workshopItems[i].ID] = enabled
		}
	}
	// A plugin supplied by a Workshop item is part of that item's package; bulk
	// Workshop state therefore applies to the embedded DLLs too.
	for _, p := range a.plugins {
		if p.WorkshopID == "" {
			continue
		}
		p.Setting.Enabled = enabled
		if !enabled {
			p.Setting.Force = false
		}
		a.cfg.PluginSettings[p.Manifest.ID] = p.Setting
		if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
			if a.cfg.DefaultPluginSettings == nil {
				a.cfg.DefaultPluginSettings = map[string]PluginSetting{}
			}
			a.cfg.DefaultPluginSettings[p.Manifest.ID] = p.Setting
		}
	}
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
	}
	_ = saveConfig(a.configPath, a.cfg)
	validatePlugins(a.plugins, a.game, a.cfg.AllowProbable)
	a.ordered, a.orderWarnings = calculateOrder(a.plugins, a.cfg.AutoOrder)
	a.compatibility, a.compatWarnings = analyseCompatibility(a.baseDir, a.knowledgeRoot, a.pluginDir, a.plugins, a.symbolCache, a.report)
	a.workshopScroll = 0
	a.normalizeModSelections()
}

func (a *application) workshopItemIndexByID(id string) int {
	for i := range a.workshopItems {
		if a.workshopItems[i].ID == id {
			return i
		}
	}
	return -1
}

// rebuildWorkshopPluginLinks binds DLLs in a Workshop item's plugin folder
// to the parent item so hybrid packages share one enable state.
func (a *application) rebuildWorkshopPluginLinks() {
	for i := range a.workshopItems {
		a.workshopItems[i].EmbeddedPluginIDs = nil
	}
	for _, p := range a.plugins {
		if p.WorkshopID == "" {
			continue
		}
		i := a.workshopItemIndexByID(p.WorkshopID)
		if i < 0 {
			continue
		}
		item := &a.workshopItems[i]
		item.EmbeddedPluginIDs = append(item.EmbeddedPluginIDs, p.Manifest.ID)
		// A hybrid Workshop/plugin package has one effective enabled state. If either
		// persisted side is enabled, keep the package enabled and synchronize both maps.
		enabled := item.Enabled || p.Setting.Enabled
		item.Enabled = enabled
		a.cfg.WorkshopSettings[item.ID] = enabled
		p.Setting.Enabled = enabled
		if !enabled {
			p.Setting.Force = false
		}
		a.cfg.PluginSettings[p.Manifest.ID] = p.Setting
		if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
			if a.cfg.DefaultWorkshopSettings == nil {
				a.cfg.DefaultWorkshopSettings = map[string]bool{}
			}
			if a.cfg.DefaultPluginSettings == nil {
				a.cfg.DefaultPluginSettings = map[string]PluginSetting{}
			}
			a.cfg.DefaultWorkshopSettings[item.ID] = enabled
			a.cfg.DefaultPluginSettings[p.Manifest.ID] = p.Setting
		}
	}
	for i := range a.workshopItems {
		sort.Strings(a.workshopItems[i].EmbeddedPluginIDs)
	}
}

func (a *application) setHybridWorkshopEnabled(workshopID string, enabled bool) {
	i := a.workshopItemIndexByID(workshopID)
	if i >= 0 {
		a.workshopItems[i].Enabled = enabled
		a.cfg.WorkshopSettings[workshopID] = enabled
		if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
			if a.cfg.DefaultWorkshopSettings == nil {
				a.cfg.DefaultWorkshopSettings = map[string]bool{}
			}
			a.cfg.DefaultWorkshopSettings[workshopID] = enabled
		}
	}
	for _, p := range a.plugins {
		if p.WorkshopID != workshopID {
			continue
		}
		p.Setting.Enabled = enabled
		if !enabled {
			p.Setting.Force = false
		}
		a.cfg.PluginSettings[p.Manifest.ID] = p.Setting
		if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
			if a.cfg.DefaultPluginSettings == nil {
				a.cfg.DefaultPluginSettings = map[string]PluginSetting{}
			}
			a.cfg.DefaultPluginSettings[p.Manifest.ID] = p.Setting
		}
	}
	if strings.EqualFold(a.cfg.SelectedPlaylist, defaultPlaylistName) {
	}
	_ = saveConfig(a.configPath, a.cfg)
	validatePlugins(a.plugins, a.game, a.cfg.AllowProbable)
	a.ordered, a.orderWarnings = calculateOrder(a.plugins, a.cfg.AutoOrder)
	a.compatibility, a.compatWarnings = analyseCompatibility(a.baseDir, a.knowledgeRoot, a.pluginDir, a.plugins, a.symbolCache, a.report)
	a.normalizeModSelections()
}

func (a *application) enabledWorkshopCount() int {
	n := 0
	for i := range a.workshopItems {
		if a.workshopItems[i].Enabled {
			n++
		}
	}
	return n
}
