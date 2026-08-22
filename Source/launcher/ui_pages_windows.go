//go:build windows

package main

import (
	"fmt"
	"strings"
	"time"
)

var navItems = []struct{ Label, Hint string }{
	{"Dashboard", "Launch overview"},
	{"Mod Manager", "Mods and playlists"},
	{"Diagnostics", "Logs"},
}

func renderApplication(app *application, dc uintptr, width, height int) {
	ensureFonts(app.dpi)
	app.hits = app.hits[:0]
	c := &canvas{dc: dc, app: app, width: width, height: height, scale: app.scale}
	c.fillRect(0, 0, width, height, palette.Background)

	sidebar := c.s(222)
	statusH := c.s(42)
	drawSidebar(c, sidebar, height-statusH)

	contentX := sidebar + c.s(30)
	contentY := c.s(22)
	contentW := width - contentX - c.s(30)
	contentH := height - contentY - statusH - c.s(10)
	if contentW < c.s(600) {
		contentW = c.s(600)
	}

	app.tooltipText = ""
	switch app.page {
	case 1:
		drawPluginsPage(c, contentX, contentY, contentW, contentH)
	case 2:
		drawDiagnosticsPage(c, contentX, contentY, contentW, contentH)
	default:
		drawDashboardPage(c, contentX, contentY, contentW, contentH)
	}
	// Page-owned overlays render after page content so dropdowns/tooltips are not
	// painted over by the table/detail panels beneath them.
	if (app.page == 0 || app.page == 1) && app.playlistDropdown && !app.playlistModal && !app.playlistDeleteModal && !app.playlistImportModal && !app.workshopUnsubscribeModal && !app.workshopForceUpdateModal {
		drawPlaylistDropdownOverlay(c)
	}
	drawStatusBar(c, sidebar, height-statusH, width-sidebar, statusH)
	if app.hover == "copy-build-sha" && app.page == 0 && !app.playlistModal && !app.playlistDeleteModal && !app.playlistImportModal && !app.workshopUnsubscribeModal && !app.workshopForceUpdateModal {
		drawBuildSHATooltip(c)
	}
	if app.playlistModal {
		drawPlaylistModal(c)
	}
	if app.playlistDeleteModal {
		drawPlaylistDeleteModal(c)
	}
	if app.playlistImportModal {
		drawPlaylistImportModal(c)
	}
	if app.workshopUnsubscribeModal {
		drawWorkshopUnsubscribeModal(c)
	}
	if app.workshopForceUpdateModal {
		drawWorkshopForceUpdateModal(c)
	}
	if app.tooltipText != "" {
		drawGenericTooltip(c, app.tooltipText)
	}
	if app.toastText != "" && time.Now().Before(app.toastUntil) {
		drawToast(c, app.toastText)
	}
}

func drawPlaylistModal(c *canvas) {
	// Dim the application behind a compact launcher-owned text-entry dialog.
	c.fillRect(0, 0, c.width, c.height, rgb(8, 11, 16))
	w, h := c.s(520), c.s(244)
	x, y := (c.width-w)/2, (c.height-h)/2
	c.roundedRect(x, y, w, h, c.s(16), palette.Surface, palette.BorderBright)
	c.label("PLUGIN PLAYLIST", x+c.s(24), y+c.s(20), w-c.s(48), palette.AccentBright)
	title := "Save current configuration"
	detail := "Stores enabled mods and their current load order. Saving an existing name updates it."
	button := "Save playlist"
	if c.app.playlistModalMode == "rename" {
		title = "Rename playlist"
		detail = "Choose a new name for this saved playlist."
		button = "Rename"
	}
	c.text(title, x+c.s(24), y+c.s(43), w-c.s(48), c.s(34), fonts.Heading, palette.Text, dtLeft|dtVCenter|dtSingleLine)
	c.text(detail, x+c.s(24), y+c.s(81), w-c.s(48), c.s(42), fonts.Small, palette.TextMuted, dtLeft|dtWordBreak)
	fieldY := y + c.s(128)
	c.roundedRect(x+c.s(24), fieldY, w-c.s(48), c.s(42), c.s(9), palette.SurfaceRaised, palette.BorderBright)
	draft := c.app.playlistDraft
	if draft == "" {
		draft = "Type a playlist name…"
	}
	c.text(draft+map[bool]string{true: "  │", false: ""}[c.app.playlistDraft != ""], x+c.s(36), fieldY, w-c.s(72), c.s(42), fonts.Body, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.compactButton("playlist-modal-cancel", "Cancel", x+w-c.s(212), y+h-c.s(54), c.s(88), c.s(34))
	c.button("playlist-modal-save", button, x+w-c.s(116), y+h-c.s(54), c.s(92), c.s(34), true)
}

func drawPlaylistImportModal(c *canvas) {
	c.fillRect(0, 0, c.width, c.height, rgb(8, 11, 16))
	w, h := c.s(540), c.s(250)
	x, y := (c.width-w)/2, (c.height-h)/2
	c.roundedRect(x, y, w, h, c.s(16), palette.Surface, palette.BorderBright)
	c.label("IMPORT PLAYLIST", x+c.s(24), y+c.s(20), w-c.s(48), palette.AccentBright)
	c.text("Import a Republic Mod Loader playlist", x+c.s(24), y+c.s(46), w-c.s(48), c.s(34), fonts.Heading, palette.Text, dtLeft|dtVCenter|dtSingleLine)
	dropY := y + c.s(92)
	c.roundedRect(x+c.s(24), dropY, w-c.s(48), c.s(82), c.s(10), palette.SurfaceRaised, palette.BorderBright)
	c.text("Drag and drop a playlist JSON here", x+c.s(36), dropY+c.s(10), w-c.s(72), c.s(28), fonts.BodyStrong, palette.Text, dtCenter|dtVCenter|dtSingleLine)
	c.text("or choose a file from disk", x+c.s(36), dropY+c.s(40), w-c.s(72), c.s(24), fonts.Small, palette.TextMuted, dtCenter|dtVCenter|dtSingleLine)
	c.compactButton("playlist-import-cancel", "Cancel", x+w-c.s(206), y+h-c.s(52), c.s(82), c.s(34))
	c.button("playlist-import-browse", "Browse…", x+w-c.s(116), y+h-c.s(52), c.s(92), c.s(34), true)
}

func drawPlaylistDeleteModal(c *canvas) {
	c.fillRect(0, 0, c.width, c.height, rgb(8, 11, 16))
	w, h := c.s(470), c.s(210)
	x, y := (c.width-w)/2, (c.height-h)/2
	c.roundedRect(x, y, w, h, c.s(16), palette.Surface, palette.BorderBright)
	c.label("DELETE PLAYLIST", x+c.s(24), y+c.s(20), w-c.s(48), palette.Bad)
	name := c.app.playlistDeleteName
	c.text("Delete \""+name+"\"?", x+c.s(24), y+c.s(49), w-c.s(48), c.s(34), fonts.Heading, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text("This removes only the saved launcher playlist. It does not delete or modify any mod DLL.", x+c.s(24), y+c.s(88), w-c.s(48), c.s(43), fonts.Small, palette.TextMuted, dtLeft|dtWordBreak)
	c.compactButton("playlist-delete-cancel", "Cancel", x+w-c.s(210), y+h-c.s(52), c.s(84), c.s(34))
	// Draw a red confirmation button rather than reusing the primary accent.
	bx, by, bw, bh := x+w-c.s(116), y+h-c.s(52), c.s(92), c.s(34)
	fill := palette.BadSoft
	if c.app.hover == "playlist-delete-confirm" {
		fill = palette.Bad
	}
	c.roundedRect(bx, by, bw, bh, c.s(8), fill, palette.Bad)
	c.text("Delete", bx+c.s(8), by, bw-c.s(16), bh, fonts.SmallStrong, palette.Bad, dtCenter|dtVCenter|dtSingleLine)
	if c.app.hover == "playlist-delete-confirm" {
		c.text("Delete", bx+c.s(8), by, bw-c.s(16), bh, fonts.SmallStrong, palette.White, dtCenter|dtVCenter|dtSingleLine)
	}
	c.addHit("playlist-delete-confirm", bx, by, bw, bh)
}

func drawWorkshopUnsubscribeModal(c *canvas) {
	c.fillRect(0, 0, c.width, c.height, rgb(8, 11, 16))
	w, h := c.s(510), c.s(220)
	x, y := (c.width-w)/2, (c.height-h)/2
	c.roundedRect(x, y, w, h, c.s(16), palette.Surface, palette.BorderBright)
	c.label("STEAM WORKSHOP", x+c.s(24), y+c.s(20), w-c.s(48), palette.Bad)
	c.text("Unsubscribe from this mod?", x+c.s(24), y+c.s(47), w-c.s(48), c.s(34), fonts.Heading, palette.Text, dtLeft|dtVCenter|dtSingleLine)
	name := c.app.workshopUnsubscribeName
	id := c.app.workshopUnsubscribeID
	c.text(name, x+c.s(24), y+c.s(83), w-c.s(48), c.s(25), fonts.BodyStrong, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text("Workshop ID  "+id, x+c.s(24), y+c.s(108), w-c.s(48), c.s(22), fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine)
	c.text("Steam will unsubscribe the item and RML will immediately delete its local Workshop folder.", x+c.s(24), y+c.s(133), w-c.s(48), c.s(32), fonts.Small, palette.TextMuted, dtLeft|dtWordBreak)
	c.compactButton("workshop-unsubscribe-cancel", "Cancel", x+w-c.s(214), y+h-c.s(52), c.s(86), c.s(34))
	c.dangerButton("workshop-unsubscribe-confirm", "Unsubscribe", x+w-c.s(120), y+h-c.s(52), c.s(96), c.s(34), true)
}

func drawBuildSHATooltip(c *canvas) {
	if c.app.mouseX < 0 || c.app.mouseY < 0 {
		return
	}
	// Keep the tooltip anchored just below the pointer.
	x, y := c.app.mouseX+c.s(1), c.app.mouseY+c.s(1)
	w, h := c.s(90), c.s(23)
	if x+w > c.width-c.s(4) {
		x = c.width - w - c.s(4)
	}
	if y+h > c.height-c.s(4) {
		y = c.height - h - c.s(4)
	}
	c.roundedRect(x, y, w, h, c.s(5), rgb(24, 31, 41), palette.BorderBright)
	c.text("Click to Copy", x+c.s(6), y, w-c.s(12), h, fonts.Tiny, palette.Text, dtLeft|dtVCenter|dtSingleLine)
}

func drawGenericTooltip(c *canvas, text string) {
	if text == "" || c.app.mouseX < 0 || c.app.mouseY < 0 {
		return
	}
	pad := c.s(8)
	w := c.textWidth(text, fonts.Small) + pad*2
	maxW := maxInt(c.s(140), c.width-c.s(10))
	if w > maxW {
		w = maxW
	}
	h := c.s(25)
	x, y := c.app.mouseX+c.s(2), c.app.mouseY+c.s(2)
	if x+w > c.width-c.s(4) {
		x = c.width - w - c.s(4)
	}
	if y+h > c.height-c.s(4) {
		y = c.height - h - c.s(4)
	}
	c.roundedRect(x, y, w, h, c.s(5), rgb(24, 31, 41), palette.BorderBright)
	c.text(text, x+pad, y, w-pad*2, h, fonts.Small, palette.Text, dtLeft|dtVCenter|dtSingleLine)
}

func drawToast(c *canvas, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	pad := c.s(12)
	w := c.textWidth(text, fonts.SmallStrong) + pad*2
	if w > c.s(430) {
		w = c.s(430)
	}
	if w < c.s(220) {
		w = c.s(220)
	}
	h := c.s(42)
	x := c.width - w - c.s(18)
	y := c.height - h - c.s(58)
	c.roundedRect(x, y, w, h, c.s(9), palette.SurfaceRaised, palette.BorderBright)
	c.text(text, x+pad, y, w-pad*2, h, fonts.SmallStrong, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
}

func drawPlaylistDropdownOverlay(c *canvas) {
	names := c.app.playlistNames()
	if len(names) == 0 || c.app.playlistDropW <= 0 {
		return
	}
	x, y, w := c.app.playlistDropX, c.app.playlistDropY, c.app.playlistDropW
	rowH := c.s(30)
	// Default is pinned to the first row. Saved playlists below it are scrollable
	// so the dropdown remains usable even with the configured 64-playlist limit.
	available := c.height - y - c.s(58)
	maxRows := maxInt(2, available/rowH)
	visibleSaved := minInt(maxRows-1, maxInt(0, len(names)-1))
	maxScroll := maxInt(0, len(names)-1-visibleSaved)
	if c.app.playlistScroll > maxScroll {
		c.app.playlistScroll = maxScroll
	}
	if c.app.playlistScroll < 0 {
		c.app.playlistScroll = 0
	}
	rows := 1 + visibleSaved
	h := rows*rowH + c.s(8)
	c.roundedRect(x, y, w, h, c.s(9), palette.SurfaceRaised, palette.BorderBright)

	drawRow := func(optionIndex, visualRow int, name string) {
		ry := y + c.s(4) + visualRow*rowH
		id := fmt.Sprintf("playlist-option:%d", optionIndex)
		selected := strings.EqualFold(name, c.app.cfg.SelectedPlaylist)
		hovered := c.app.hover == id
		fill := palette.SurfaceRaised
		if selected {
			fill = palette.AccentSoft
		} else if hovered {
			fill = palette.SurfaceHover
		}
		c.roundedRect(x+c.s(4), ry, w-c.s(8), rowH-c.s(2), c.s(6), fill, fill)
		if selected {
			c.circle(x+c.s(12), ry+c.s(10), c.s(8), palette.AccentBright, palette.AccentBright)
		}
		c.text(name, x+c.s(28), ry, w-c.s(43), rowH-c.s(2), fonts.Small, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		c.addHit(id, x+c.s(4), ry, w-c.s(8), rowH-c.s(2))
	}
	drawRow(0, 0, names[0])
	for i := 0; i < visibleSaved; i++ {
		global := 1 + c.app.playlistScroll + i
		if global >= len(names) {
			break
		}
		drawRow(global, i+1, names[global])
	}
	if len(names)-1 > visibleSaved {
		trackX := x + w - c.s(8)
		trackY := y + c.s(4) + rowH
		trackH := visibleSaved*rowH - c.s(4)
		c.fillRect(trackX, trackY, c.s(2), trackH, palette.Border)
		totalSaved := len(names) - 1
		thumbH := maxInt(c.s(14), trackH*visibleSaved/totalSaved)
		thumbY := trackY
		if maxScroll > 0 {
			thumbY += (trackH - thumbH) * c.app.playlistScroll / maxScroll
		}
		c.fillRect(trackX, thumbY, c.s(2), thumbH, palette.TextFaint)
	}
}

func drawSidebar(c *canvas, w, h int) {
	c.fillRect(0, 0, w, h, palette.Sidebar)
	c.line(w-c.s(1), 0, w-c.s(1), h, c.s(1), palette.Border)
	c.drawBrand(c.s(25), c.s(25))
	c.text("REPUBLIC", c.s(80), c.s(22), w-c.s(96), c.s(24), fonts.Title, palette.Text, dtLeft|dtVCenter|dtSingleLine)
	c.text("MOD LOADER", c.s(80), c.s(45), w-c.s(96), c.s(18), fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine)

	y := c.s(105)
	for i, item := range navItems {
		id := fmt.Sprintf("nav:%d", i)
		selected := c.app.page == i
		hovered := c.app.hover == id
		fill := palette.Sidebar
		border := palette.Sidebar
		if selected {
			fill, border = palette.AccentSoft, palette.AccentSoft
		}
		if hovered && !selected {
			fill, border = palette.Surface, palette.Border
		}
		x := c.s(14)
		rowW := w - c.s(28)
		rowH := c.s(55)
		c.roundedRect(x, y, rowW, rowH, c.s(11), fill, border)
		if selected {
			c.roundedRect(x, y+c.s(9), c.s(4), rowH-c.s(18), c.s(4), palette.AccentBright, palette.AccentBright)
		}
		iconFill := palette.SurfaceRaised
		iconText := palette.TextMuted
		if selected {
			iconFill, iconText = palette.Accent, palette.White
		}
		c.circle(x+c.s(15), y+c.s(13), c.s(29), iconFill, iconFill)
		c.text(string(item.Label[0]), x+c.s(15), y+c.s(13), c.s(29), c.s(29), fonts.SmallStrong, iconText, dtCenter|dtVCenter|dtSingleLine)
		c.text(item.Label, x+c.s(55), y+c.s(8), rowW-c.s(66), c.s(24), fonts.BodyStrong, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		c.text(item.Hint, x+c.s(55), y+c.s(29), rowW-c.s(66), c.s(18), fonts.Tiny, palette.TextFaint, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		c.addHit(id, x, y, rowW, rowH)
		y += c.s(62)
	}

}

func drawStatusBar(c *canvas, x, y, w, h int) {
	c.fillRect(x, y, w, h, palette.Sidebar)
	c.line(x, y, x+w, y, c.s(1), palette.Border)
	tx := x + c.s(24)
	if c.app.busy {
		c.busyIndicator(tx, y+c.s(16))
		tx += c.s(62)
	} else {
		color := palette.Good
		if strings.Contains(strings.ToLower(c.app.status), "fail") || strings.Contains(strings.ToLower(c.app.status), "cannot") || strings.Contains(strings.ToLower(c.app.status), "error") {
			color = palette.Bad
		}
		c.circle(tx, y+c.s(16), c.s(9), color, color)
		tx += c.s(18)
	}
	c.text(c.app.status, tx, y, w-c.s(300), h, fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	right := fmt.Sprintf("RML %s  •  API %d", launcherVersion, loaderAPI)
	c.text(right, x+w-c.s(255), y, c.s(230), h, fonts.Tiny, palette.TextFaint, dtRight|dtVCenter|dtSingleLine)
}

func drawWorkshopForceUpdateModal(c *canvas) {
	c.fillRect(0, 0, c.width, c.height, rgb(8, 11, 16))
	w, h := c.s(560), c.s(268)
	x, y := (c.width-w)/2, (c.height-h)/2
	c.roundedRect(x, y, w, h, c.s(16), palette.Surface, palette.BorderBright)
	c.label("WORKSHOP MAINTENANCE", x+c.s(24), y+c.s(20), w-c.s(48), palette.Warn)
	c.text("Force-update this mod?", x+c.s(24), y+c.s(47), w-c.s(48), c.s(34), fonts.Heading, palette.Text, dtLeft|dtVCenter|dtSingleLine)
	name := c.app.workshopForceUpdateName
	id := c.app.workshopForceUpdateID
	c.text(name, x+c.s(24), y+c.s(83), w-c.s(48), c.s(25), fonts.BodyStrong, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text("Workshop ID  "+id, x+c.s(24), y+c.s(108), w-c.s(48), c.s(22), fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine)
	c.text("RML will unsubscribe the item, delete its local Workshop folder, resubscribe it, then ask Steam to begin a high-priority download. Playlist enable state is preserved.", x+c.s(24), y+c.s(136), w-c.s(48), c.s(64), fonts.Small, palette.TextMuted, dtLeft|dtWordBreak)
	c.compactButton("workshop-force-update-cancel", "Cancel", x+w-c.s(220), y+h-c.s(52), c.s(90), c.s(34))
	bx, by, bw, bh := x+w-c.s(122), y+h-c.s(52), c.s(98), c.s(34)
	fill := palette.WarnSoft
	if c.app.hover == "workshop-force-update-confirm" {
		fill = palette.Warn
	}
	c.roundedRect(bx, by, bw, bh, c.s(8), fill, palette.Warn)
	fg := palette.Warn
	if c.app.hover == "workshop-force-update-confirm" {
		fg = palette.White
	}
	c.text("Force Update", bx+c.s(6), by, bw-c.s(12), bh, fonts.SmallStrong, fg, dtCenter|dtVCenter|dtSingleLine)
	c.addHit("workshop-force-update-confirm", bx, by, bw, bh)
}

func drawDashboardPage(c *canvas, x, y, w, h int) {
	cursor := c.sectionTitle("CENTRAL COMMITTEE", "Republic Mod Loader", "", x, y, w)

	gameH := c.s(168)
	c.card(x, cursor, w, gameH)
	gameName := "Workers & Resources: Soviet Republic"
	buildVersion := "Build Version: —"
	buildSHA := "Build SHA: —"
	gameClass := 2
	if c.app.game != nil {
		if c.app.game.Version != "" {
			buildVersion = "Build Version: " + c.app.game.Version
		} else {
			buildVersion = "Build Version: untested"
		}
		if c.app.game.SHA256 != "" {
			buildSHA = "Build SHA: " + c.app.game.SHA256
		}
		if c.app.game.Known {
			gameClass = 0
		} else {
			gameClass = 1
		}
	} else if c.app.gameError != "" {
		buildSHA = "Build SHA: " + safeLine(c.app.gameError)
	}
	c.text(gameName, x+c.s(22), cursor+c.s(18), w-c.s(220), c.s(36), fonts.Heading, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text(buildVersion, x+c.s(34), cursor+c.s(60), w-c.s(230), c.s(24), fonts.Body, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	shaColor := palette.TextMuted
	if c.app.hover == "copy-build-sha" && c.app.game != nil && c.app.game.SHA256 != "" {
		shaColor = palette.AccentBright
	}
	shaX := x + c.s(34)
	shaAvailable := w - c.s(68)
	c.text(buildSHA, shaX, cursor+c.s(91), shaAvailable, c.s(24), fonts.Body, shaColor, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	if c.app.game != nil && c.app.game.SHA256 != "" {
		shaTextW := minInt(shaAvailable, c.textWidth(buildSHA, fonts.Body))
		if shaTextW > 0 {
			c.addHit("copy-build-sha", shaX, cursor+c.s(91), shaTextW, c.s(24))
		}
	}
	fill, fg := statusColors(gameClass, "")
	c.roundedRect(x+w-c.s(176), cursor+c.s(22), c.s(145), c.s(32), c.s(16), fill, fill)
	gameState := "NEEDS PATH"
	if c.app.game != nil && c.app.game.Known {
		gameState = "RECOGNISED"
	}
	if c.app.game != nil && !c.app.game.Known {
		gameState = "UNTESTED"
	}
	c.text(gameState, x+w-c.s(176), cursor+c.s(22), c.s(145), c.s(32), fonts.Tiny, fg, dtCenter|dtVCenter|dtSingleLine)
	c.compactButton("browse", "Choose executable", x+w-c.s(176), cursor+c.s(70), c.s(145), c.s(36))
	cursor += gameH + c.s(16)

	gap := c.s(14)
	metricW := (w - gap*2) / 3
	metricH := c.s(116)
	lastState := "No previous launch"
	lastDetail := ""
	lastClass := 1
	if c.app.lastLaunchError != "" {
		lastState, lastDetail, lastClass = "Launch error", safeLine(c.app.lastLaunchError), 2
	} else if c.app.report != nil {
		if c.app.report.BootstrapStatus == 0 {
			lastState, lastDetail, lastClass = "Loader OK", "Last bootstrap completed successfully", 0
		} else {
			lastState, lastDetail, lastClass = fmt.Sprintf("0x%X", c.app.report.BootstrapStatus), "Last bootstrap reported a problem", 2
		}
	}
	c.metricCard("Discovered", fmt.Sprintf("%d Plugins / %d Workshop", len(c.app.plugins), len(c.app.workshopItems)), "", x, cursor, metricW, metricH, 0)
	enabledWorkshop := 0
	enabledDevelopment := 0
	enabledWorkshopUpdates := 0
	for i := range c.app.workshopItems {
		item := &c.app.workshopItems[i]
		if !item.Enabled {
			continue
		}
		enabledWorkshop++
		if item.UpdateState == "update" || item.UpdateState == "stale" {
			enabledWorkshopUpdates++
		}
	}
	for i := range c.app.developmentItems {
		if c.app.developmentItems[i].Enabled {
			enabledDevelopment++
		}
	}
	enabledDetail := ""
	enabledClass := 0
	if enabledWorkshopUpdates > 0 {
		enabledDetail = fmt.Sprintf("%d enabled Workshop item(s) may need updating", enabledWorkshopUpdates)
		enabledClass = 1
	} else if enabledDevelopment > 0 {
		enabledDetail = fmt.Sprintf("%d Development project(s) active", enabledDevelopment)
	}
	c.metricCard("Enabled", fmt.Sprintf("%d Plugins / %d Workshop", c.app.enabledCount(), enabledWorkshop), enabledDetail, x+metricW+gap, cursor, metricW, metricH, enabledClass)
	c.metricCard("Last launch", lastState, lastDetail, x+(metricW+gap)*2, cursor, metricW, metricH, lastClass)
	cursor += metricH + c.s(16)

	lowerH := maxInt(c.s(168), h-(cursor-y)-c.s(94))
	leftW := int(float64(w-gap) * 0.58)
	rightW := w - gap - leftW
	drawDashboardPlaylistCard(c, x, cursor, leftW, lowerH)
	drawDashboardSettingsCard(c, x+leftW+gap, cursor, rightW, lowerH)

	launchY := y + h - c.s(72)
	c.roundedRect(x, launchY, w, c.s(58), c.s(14), palette.SurfaceRaised, palette.BorderBright)
	launchText := "Launch Workers & Resources"
	if c.app.busy {
		launchText = "Launching and validating runtime…"
	}
	buttonW := c.s(278)
	c.button("launch", launchText, x+w-buttonW-c.s(10), launchY+c.s(8), buttonW, c.s(42), true)
	state := "Ready to launch with RML"
	if ok, reason := c.app.canLaunch(); !ok {
		state = reason
	}
	c.text(state, x+c.s(20), launchY, c.s(maxInt(260, w-buttonW-c.s(50))), c.s(58), fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
}

func drawDashboardPlaylistCard(c *canvas, x, y, w, h int) {
	c.card(x, y, w, h)
	c.label("Playlist", x+c.s(18), y+c.s(15), w-c.s(36), palette.TextMuted)
	name := c.app.cfg.SelectedPlaylist
	if name == "" {
		name = defaultPlaylistName
	}
	c.text("Active mod configuration", x+c.s(18), y+c.s(39), w-c.s(36), c.s(25), fonts.Title, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)

	dropX := x + c.s(18)
	dropY := y + c.s(76)
	dropW := w - c.s(36)
	dropH := c.s(38)
	fill, border := palette.SurfaceRaised, palette.Border
	if c.app.hover == "playlist-dropdown" || c.app.playlistDropdown {
		fill, border = palette.SurfaceHover, palette.BorderBright
	}
	c.roundedRect(dropX, dropY, dropW, dropH, c.s(8), fill, border)
	c.text(name, dropX+c.s(12), dropY, dropW-c.s(42), dropH, fonts.BodyStrong, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	cx, cy := dropX+dropW-c.s(19), dropY+dropH/2
	c.line(cx-c.s(4), cy-c.s(2), cx, cy+c.s(2), c.s(1), palette.TextMuted)
	c.line(cx, cy+c.s(2), cx+c.s(4), cy-c.s(2), c.s(1), palette.TextMuted)
	c.addHit("playlist-dropdown", dropX, dropY, dropW, dropH)
	c.app.playlistDropX, c.app.playlistDropY, c.app.playlistDropW = dropX, dropY+dropH+c.s(3), dropW

	mode := "Automatic dependency ordering"
	if !c.app.cfg.AutoOrder {
		mode = "Saved / manual playlist order"
	}
	detail := fmt.Sprintf("%d enabled • %s", c.app.enabledCount(), mode)
	c.text(detail, x+c.s(18), dropY+c.s(50), w-c.s(36), c.s(30), fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
}

func drawDashboardSettingsCard(c *canvas, x, y, w, h int) {
	c.card(x, y, w, h)
	c.label("Settings", x+c.s(18), y+c.s(15), w-c.s(36), palette.TextMuted)
	c.text("Launch & support", x+c.s(18), y+c.s(36), w-c.s(36), c.s(25), fonts.Title, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)

	rows := []struct {
		Title string
		ID    string
		Value bool
	}{
		{"Detailed Diagnostic Logging", "verbose-diagnostics", c.app.cfg.VerboseDiagnostics},
		{"Automatic crash archives", "auto-crash-archives", c.app.cfg.AutoCrashArchives},
		{"Automatic ordering", "auto-order", c.app.cfg.AutoOrder},
		{"Close after launch", "close-after", c.app.cfg.CloseAfterLaunch},
		{"Hide while WRSR runs", "auto-hide", c.app.cfg.AutoHideOnLaunch},
	}
	contentY := y + c.s(65)
	available := maxInt(c.s(90), h-c.s(76))
	rowH := maxInt(c.s(23), available/len(rows))
	for i, row := range rows {
		ry := contentY + i*rowH
		if i > 0 {
			c.line(x+c.s(18), ry, x+w-c.s(18), ry, 1, palette.Border)
		}
		c.text(row.Title, x+c.s(18), ry, w-c.s(86), rowH, fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		c.toggle(row.ID, row.Value, x+w-c.s(60), ry+(rowH-c.s(23))/2)
	}
}

func drawModSearchBox(c *canvas, x, y, w, h int) {
	text := c.app.activeSearchText()
	active := c.app.modSearchFocus
	fill, border := palette.Background, palette.Border
	if c.app.hover == "mod-search" || active {
		fill, border = palette.SurfaceRaised, palette.BorderBright
	}
	c.roundedRect(x, y, w, h, c.s(8), fill, border)
	display := text
	color := palette.Text
	if display == "" {
		display, color = "Search name / ID…", palette.TextFaint
	}
	if active && text != "" {
		display += "  │"
	}
	clearW := c.s(28)
	c.text(display, x+c.s(11), y, w-clearW-c.s(8), h, fonts.Small, color, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.addHit("mod-search", x, y, w, h)
	if text != "" {
		cx := x + w - clearW
		c.text("×", cx, y, clearW, h, fonts.BodyStrong, palette.TextMuted, dtCenter|dtVCenter|dtSingleLine)
		c.addHit("mod-search-clear", cx, y, clearW, h)
	}
}

func drawListScrollbar(c *canvas, prefix string, x, y, w, h, total, visible, offset int) {
	view := 0
	if prefix == "workshop" {
		view = 1
	} else if prefix == "development" {
		view = 2
	}
	c.app.modScrollView = view
	c.app.modScrollTrackH = h
	c.app.modScrollTotal = total
	c.app.modScrollVisible = visible
	c.app.modScrollThumbY = y
	c.app.modScrollThumbH = h
	if total <= visible || total <= 0 || h <= c.s(20) {
		return
	}
	c.roundedRect(x, y, w, h, w, palette.Background, palette.Background)
	thumbH := maxInt(c.s(28), int(float64(h)*float64(visible)/float64(total)))
	if thumbH > h {
		thumbH = h
	}
	maxOffset := total - visible
	travel := h - thumbH
	thumbY := y
	if maxOffset > 0 {
		thumbY += int(float64(travel) * float64(offset) / float64(maxOffset))
	}
	c.app.modScrollThumbY = thumbY
	c.app.modScrollThumbH = thumbH
	c.roundedRect(x, thumbY, w, thumbH, w, palette.BorderBright, palette.BorderBright)
	c.addHit(prefix+"-scroll-thumb", x-c.s(7), thumbY, w+c.s(14), thumbH)
	if thumbY > y {
		c.addHit(prefix+"-scroll-pageup", x-c.s(6), y, w+c.s(12), thumbY-y)
	}
	if thumbY+thumbH < y+h {
		c.addHit(prefix+"-scroll-pagedown", x-c.s(6), thumbY+thumbH, w+c.s(12), y+h-(thumbY+thumbH))
	}
}

func detailThumbnailWidth(c *canvas, panelW int) int {
	inner := maxInt(c.s(120), panelW-c.s(40))
	// Reserve enough room for the status pill and two short metadata lines. The
	// preview reaches the Workshop-style 190px width on normal panels but
	// shrinks rather than pushing metadata off-screen on a narrow window.
	return minInt(c.s(190), maxInt(c.s(88), inner-c.s(118)))
}

func drawScrollableDescription(c *canvas, view int, text string, x, y, w, h int, font uintptr, color uint32) {
	if w <= 0 || h <= 0 {
		return
	}
	barW := c.s(7)
	textW := w
	contentH := c.textHeight(text, maxInt(1, w-barW-c.s(6)), font)
	if contentH < h {
		contentH = h
	}
	offset := c.app.pluginDetailScroll
	if view == 1 {
		offset = c.app.workshopDetailScroll
	} else if view == 2 {
		offset = c.app.developmentDetailScroll
	}
	offset = clampPixelScroll(offset, contentH, h)
	if view == 1 {
		c.app.workshopDetailScroll = offset
	} else if view == 2 {
		c.app.developmentDetailScroll = offset
	} else {
		c.app.pluginDetailScroll = offset
	}
	c.app.detailScrollX, c.app.detailScrollY, c.app.detailScrollW, c.app.detailScrollH = x, y, w, h
	c.app.detailScrollTotal, c.app.detailScrollVisible, c.app.detailScrollView = contentH, h, view
	c.app.detailScrollThumbY, c.app.detailScrollThumbH = y, h
	c.clippedWrappedText(text, x, y, textW-barW-c.s(6), h, font, color, offset, contentH)
	if contentH <= h {
		return
	}
	trackX := x + w - barW
	c.roundedRect(trackX, y, barW, h, barW, palette.Background, palette.Background)
	thumbH := maxInt(c.s(24), int(float64(h)*float64(h)/float64(contentH)))
	if thumbH > h {
		thumbH = h
	}
	maxOffset := contentH - h
	travel := h - thumbH
	thumbY := y
	if maxOffset > 0 {
		thumbY += int(float64(travel) * float64(offset) / float64(maxOffset))
	}
	c.app.detailScrollThumbY, c.app.detailScrollThumbH = thumbY, thumbH
	c.roundedRect(trackX, thumbY, barW, thumbH, barW, palette.BorderBright, palette.BorderBright)
	prefix := "plugin"
	if view == 1 {
		prefix = "workshop"
	} else if view == 2 {
		prefix = "development"
	}
	c.addHit(prefix+"-detail-scroll-thumb", trackX-c.s(6), thumbY, barW+c.s(12), thumbH)
	if thumbY > y {
		c.addHit(prefix+"-detail-scroll-pageup", trackX-c.s(6), y, barW+c.s(12), thumbY-y)
	}
	if thumbY+thumbH < y+h {
		c.addHit(prefix+"-detail-scroll-pagedown", trackX-c.s(6), thumbY+thumbH, barW+c.s(12), y+h-(thumbY+thumbH))
	}
}

func drawPluginsPage(c *canvas, x, y, w, h int) {
	cursor := c.sectionTitle("BUREAU OF MODIFICATION", "Mod Manager", "", x, y, w)

	switchW, switchH := c.s(420), c.s(38)
	switchX := x
	c.roundedRect(switchX, cursor, switchW, switchH, c.s(10), palette.Surface, palette.Border)
	third := switchW / 3
	labels := []string{"Plugins", "Workshop", "Development"}
	hits := []string{"mod-view:plugins", "mod-view:workshop", "mod-view:development"}
	for i := 0; i < 3; i++ {
		bx := switchX + i*third
		bw := third
		if i == 2 {
			bw = switchW - 2*third
		}
		fill := palette.Surface
		textColor := palette.TextMuted
		if c.app.modView == i {
			fill = palette.AccentSoft
			textColor = palette.AccentBright
		}
		insetX := c.s(2)
		insetW := bw - c.s(4)
		c.roundedRect(bx+insetX, cursor+c.s(2), insetW, switchH-c.s(4), c.s(8), fill, fill)
		c.text(labels[i], bx, cursor, bw, switchH, fonts.SmallStrong, textColor, dtCenter|dtVCenter|dtSingleLine)
		c.addHit(hits[i], bx, cursor, bw, switchH)
	}
	cursor += switchH + c.s(9)

	toolbarH := c.s(86)
	c.roundedRect(x, cursor, w, toolbarH, c.s(11), palette.Surface, palette.Border)
	row1Y := cursor + c.s(7)
	row2Y := cursor + c.s(46)
	c.compactButton("refresh", "Refresh", x+c.s(8), row1Y, c.s(76), c.s(32))
	if c.app.modView == 2 {
		c.compactButton("development-open-root", "Open projects folder", x+c.s(91), row1Y, c.s(142), c.s(32))
	} else {
		c.compactButton("mod-scan-updates", "Scan for updates", x+c.s(91), row1Y, c.s(126), c.s(32))
	}

	allLabel := "All Plugins"
	allValue := c.app.allPluginsEnabled()
	hideValue := c.app.cfg.HideDisabledPlugins
	needsValue := c.app.cfg.PluginNeedsAttentionOnly
	if c.app.modView == 1 {
		allLabel = "All Workshop"
		allValue = c.app.allWorkshopEnabled()
		hideValue = c.app.cfg.HideDisabledWorkshop
		needsValue = c.app.cfg.WorkshopNeedsAttentionOnly
	} else if c.app.modView == 2 {
		allLabel = "All Development"
		allValue = c.app.allDevelopmentEnabled()
		hideValue = c.app.cfg.HideDisabledDevelopment
		needsValue = false
	}
	allX := x + c.s(247)
	c.text(allLabel, allX, row1Y, c.s(96), c.s(32), fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.toggle("mod-enable-all", allValue, allX+c.s(98), row1Y+c.s(6))
	hideX := allX + c.s(152)
	c.text("Hide Disabled", hideX, row1Y, c.s(96), c.s(32), fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.toggle("mod-hide-disabled", hideValue, hideX+c.s(98), row1Y+c.s(6))

	attentionX := x + c.s(10)
	searchX := attentionX
	attentionW := 0
	if c.app.modView != 2 {
		c.text("Needs Attention", attentionX, row2Y, c.s(106), c.s(32), fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		c.toggle("mod-needs-attention", needsValue, attentionX+c.s(108), row2Y+c.s(6))
		attentionW = c.s(162)
		searchX = attentionX + attentionW
	}
	if c.app.modView == 1 {
		pluginOnlyX := searchX
		c.text("Includes DLL", pluginOnlyX, row2Y, c.s(86), c.s(32), fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		c.toggle("mod-includes-plugin", c.app.cfg.WorkshopIncludesPluginOnly, pluginOnlyX+c.s(88), row2Y+c.s(6))
		searchX = pluginOnlyX + c.s(140)
	}
	gap2 := c.s(7)
	autoW, resetW := 0, 0
	if c.app.modView == 0 {
		autoLabel := "Auto order: " + map[bool]string{true: "On", false: "Off"}[c.app.cfg.AutoOrder]
		autoW = maxInt(c.s(100), c.textWidth(autoLabel, fonts.SmallStrong)+c.s(18))
		resetW = maxInt(c.s(118), c.textWidth("Reset recommended", fonts.SmallStrong)+c.s(18))
	}
	reservedRight := c.s(10)
	if autoW > 0 {
		reservedRight += autoW + gap2 + resetW + gap2
	}
	availableSearch := x + w - reservedRight - searchX
	searchW := minInt(c.s(270), maxInt(c.s(120), availableSearch))
	drawModSearchBox(c, searchX, row2Y, searchW, c.s(32))
	nextX := searchX + searchW + c.s(9)
	if c.app.modView == 0 {
		autoLabel := "Auto order: " + map[bool]string{true: "On", false: "Off"}[c.app.cfg.AutoOrder]
		c.compactButton("auto-order", autoLabel, nextX, row2Y, autoW, c.s(32))
		nextX += autoW + gap2
		c.compactButton("reset", "Reset recommended", nextX, row2Y, resetW, c.s(32))
		nextX += resetW + gap2
	}
	if w-(nextX-x) > c.s(110) {
		summary := fmt.Sprintf("%d shown / %d total", len(c.app.visiblePluginIndices()), len(c.app.plugins))
		if c.app.modView == 1 {
			summary = fmt.Sprintf("%d shown / %d installed", len(c.app.visibleWorkshopIndices()), len(c.app.workshopItems))
		} else if c.app.modView == 2 {
			summary = fmt.Sprintf("%d shown / %d local", len(c.app.visibleDevelopmentIndices()), len(c.app.developmentItems))
		}
		c.text(summary, nextX, row2Y, x+w-nextX-c.s(10), c.s(32), fonts.Tiny, palette.TextMuted, dtRight|dtVCenter|dtSingleLine|dtEndEllipsis)
	}
	cursor += toolbarH + c.s(10)

	playlistH := c.s(50)
	c.roundedRect(x, cursor, w, playlistH, c.s(11), palette.Surface, palette.Border)
	c.label("Playlist", x+c.s(14), cursor+c.s(5), c.s(75), palette.TextFaint)
	name := c.app.cfg.SelectedPlaylist
	if name == "" {
		name = defaultPlaylistName
	}
	dropX := x + c.s(92)
	dropY := cursor + c.s(8)
	dropW := maxInt(c.s(260), minInt(c.s(440), w-c.s(250)))
	dropH := c.s(34)
	fill, border := palette.SurfaceRaised, palette.Border
	if c.app.hover == "playlist-dropdown" || c.app.playlistDropdown {
		fill, border = palette.SurfaceHover, palette.BorderBright
	}
	c.roundedRect(dropX, dropY, dropW, dropH, c.s(8), fill, border)
	c.text(name, dropX+c.s(12), dropY, dropW-c.s(42), dropH, fonts.BodyStrong, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	cx, cy := dropX+dropW-c.s(19), dropY+dropH/2
	c.line(cx-c.s(4), cy-c.s(2), cx, cy+c.s(2), c.s(1), palette.TextMuted)
	c.line(cx, cy+c.s(2), cx+c.s(4), cy-c.s(2), c.s(1), palette.TextMuted)
	c.addHit("playlist-dropdown", dropX, dropY, dropW, dropH)
	c.app.playlistDropX, c.app.playlistDropY, c.app.playlistDropW = dropX, dropY+dropH+c.s(3), dropW

	buttonSize := c.s(34)
	gapB := c.s(8)
	startX := x + w - (buttonSize*5 + gapB*4) - c.s(8)
	canEdit := !strings.EqualFold(name, defaultPlaylistName) && c.app.selectedPlaylistIndex() >= 0
	c.squareIconButton("playlist-save", "save", startX, dropY, buttonSize, false, true, "Save Playlist")
	c.squareIconButton("playlist-rename", "pencil", startX+(buttonSize+gapB), dropY, buttonSize, false, canEdit, "Rename Playlist")
	c.squareIconButton("playlist-export", "export", startX+2*(buttonSize+gapB), dropY, buttonSize, false, canEdit, "Export Playlist")
	c.squareIconButton("playlist-import", "import", startX+3*(buttonSize+gapB), dropY, buttonSize, false, true, "Import Playlist")
	c.squareIconButton("playlist-delete", "trash", startX+4*(buttonSize+gapB), dropY, buttonSize, true, canEdit, "Delete Playlist")
	cursor += playlistH + c.s(12)

	gap := c.s(14)
	leftW := int(float64(w-gap) * 0.58)
	rightW := w - gap - leftW
	panelH := h - (cursor - y) - c.s(10)
	switch c.app.modView {
	case 1:
		drawWorkshopTable(c, x, cursor, leftW, panelH)
		drawWorkshopDetail(c, x+leftW+gap, cursor, rightW, panelH)
	case 2:
		drawDevelopmentTable(c, x, cursor, leftW, panelH)
		drawDevelopmentDetail(c, x+leftW+gap, cursor, rightW, panelH)
	default:
		drawPluginTable(c, x, cursor, leftW, panelH)
		drawPluginDetail(c, x+leftW+gap, cursor, rightW, panelH)
	}
}

func drawPluginTable(c *canvas, x, y, w, h int) {
	c.card(x, y, w, h)
	headerH := c.s(42)
	c.fillRect(x+c.s(1), y+c.s(1), w-c.s(2), headerH, palette.SurfaceRaised)
	statusW := c.s(146)
	statusX := x + w - statusW - c.s(8)
	idW := c.s(92)
	idX := statusX - idW - c.s(10)
	c.text("PLUGIN", x+c.s(18), y, maxInt(c.s(60), idX-x-c.s(28)), headerH, fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine)
	c.text("ITEM ID", idX, y, idW, headerH, fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text("STATUS", statusX, y, statusW, headerH, fonts.Tiny, palette.TextMuted, dtCenter|dtVCenter|dtSingleLine)

	rowH := c.s(61)
	maxRows := maxInt(1, (h-headerH-c.s(6))/rowH)
	visible := c.app.visiblePluginIndices()
	c.app.pluginScroll = clampListScroll(c.app.pluginScroll, len(visible), maxRows)
	offset := c.app.pluginScroll
	rowY := y + headerH
	end := minInt(len(visible), offset+maxRows)
	for pos := offset; pos < end; pos++ {
		i := visible[pos]
		p := c.app.plugins[i]
		selected := c.app.selected == i
		id := fmt.Sprintf("plugin-row:%d", i)
		fill := palette.Surface
		nameColor := palette.Text
		if !p.BasicValid {
			fill = palette.Background
			nameColor = palette.TextMuted
		}
		if selected {
			fill = palette.AccentSoft
		} else if c.app.hover == id {
			fill = palette.SurfaceHover
		}
		c.fillRect(x+c.s(1), rowY, w-c.s(2), rowH, fill)
		if selected {
			c.fillRect(x+c.s(1), rowY, c.s(4), rowH, palette.AccentBright)
		}
		nameW := maxInt(c.s(60), idX-x-c.s(28))
		c.text(p.Manifest.Name, x+c.s(18), rowY+c.s(6), nameW, c.s(25), fonts.BodyStrong, nameColor, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		pluginSubline := p.Manifest.ID
		pluginSubColor := palette.TextFaint
		if !p.BasicValid {
			pluginSubColor = palette.Bad
			if len(p.PEValidation.Failures) > 0 {
				pluginSubline += "  •  " + p.PEValidation.Failures[0]
			}
		}
		if p.WorkshopID != "" {
			if item := c.app.pluginWorkshopItem(p); item != nil {
				label := workshopUpdateShortLabel(item)
				if label != "" {
					pluginSubline += "  •  " + label
				}
				switch item.UpdateState {
				case "current":
					pluginSubColor = palette.Good
				case "update", "stale":
					pluginSubColor = palette.Warn
				case "missing", "unavailable":
					pluginSubColor = palette.Bad
				case "downloading":
					pluginSubColor = palette.AccentBright
				}
			}
		}
		if p.BasicValid && p.Setting.Enabled && !canLoad(p) {
			pluginSubline += "  •  Needs attention"
			pluginSubColor = palette.Bad
		} else if p.BasicValid && p.Setting.Enabled && p.Status == "WARNING" {
			pluginSubline += "  •  Warning"
			pluginSubColor = palette.Warn
		}
		c.text(pluginSubline, x+c.s(18), rowY+c.s(31), nameW, c.s(20), fonts.Tiny, pluginSubColor, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		itemID := "N/A"
		if p.WorkshopID != "" {
			itemID = p.WorkshopID
		}
		c.text(itemID, idX, rowY, idW, rowH, fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		if p.BasicValid {
			c.statusToggle(fmt.Sprintf("plugin-toggle:%d", i), p.Setting.Enabled, statusX, rowY+c.s(15), statusW, c.s(31))
		} else {
			c.invalidStatusControl(fmt.Sprintf("plugin-toggle:%d", i), statusX, rowY+c.s(15), statusW, c.s(31))
		}
		c.addHit(id, x, rowY, maxInt(c.s(20), statusX-x-c.s(4)), rowH)
		c.line(x+c.s(15), rowY+rowH-1, x+w-c.s(15), rowY+rowH-1, 1, palette.Border)
		rowY += rowH
	}
	c.app.modListX, c.app.modListY, c.app.modListW, c.app.modListH = x, y+headerH, w, h-headerH
	drawListScrollbar(c, "plugin", x+w-c.s(10), y+headerH+c.s(4), c.s(6), h-headerH-c.s(8), len(visible), maxRows, offset)
	if len(visible) == 0 {
		msg := "No plugins match the current search/filter."
		if len(c.app.plugins) == 0 {
			msg = "No plugin DLLs were found in enabled Workshop plugin folders or rml\\plugins."
		}
		c.text(msg, x+c.s(22), y+c.s(70), w-c.s(44), c.s(70), fonts.Body, palette.TextMuted, dtCenter|dtVCenter|dtWordBreak)
	}
}

func pluginInterfaceSummary(p *Plugin) string {
	if p == nil {
		return "Interfaces: unknown"
	}
	parts := []string{}
	if p.HasRMLInterface {
		parts = append(parts, "RML")
	}
	if p.HasTesmioInterface {
		if p.LegacyAPI > 0 {
			parts = append(parts, fmt.Sprintf("Tesmio API %d", p.LegacyAPI))
		} else {
			parts = append(parts, "Tesmio")
		}
	}
	if len(parts) == 0 {
		return "Interface: Raw/native DLL"
	}
	return "Interfaces: " + strings.Join(parts, " + ")
}

func pluginLoadPathSummary(p *Plugin) string {
	if p == nil {
		return "Load path: unknown"
	}
	switch p.LoaderKind {
	case pluginKindRML:
		return "Load path: Native RML interface"
	case pluginKindTesmio:
		return "Load path: Tesmio compatibility shim"
	default:
		if p.HasRMLInterface || p.HasTesmioInterface {
			return "Load path: Raw LoadLibrary (no complete supported lifecycle)"
		}
		return "Load path: Raw LoadLibrary"
	}
}

func pluginValidationDetails(p *Plugin) string {
	if p == nil {
		return ""
	}
	v := p.PEValidation
	peState := "INVALID"
	if v.PEReadable {
		peState = "VALID"
	}
	dllState := "Unable to determine"
	if v.PEReadable {
		if v.DeclaredDLL {
			dllState = "Yes"
		} else {
			dllState = "No"
		}
	}
	if p.BasicValid {
		return "Binary Validation: VALID\nPE Image: VALID\nArchitecture: " + v.Architecture + "\nPE Format: " + v.PEFormat + "\nDeclared as DLL: " + dllState
	}
	var b strings.Builder
	b.WriteString("Plugin Validation Failed\n")
	b.WriteString("PE Image: " + peState + "\n")
	b.WriteString("Architecture: " + v.Architecture + "\n")
	b.WriteString("PE Format: " + v.PEFormat + "\n")
	b.WriteString("Declared as DLL: " + dllState)
	// Individual admission failures are rendered as prominent banners below the
	// validation facts. Keeping them out of this paragraph prevents the same
	// failure from being repeated as ordinary metadata and as a banner.
	if !v.PEReadable && strings.TrimSpace(v.ParserError) != "" {
		b.WriteString("\nParser detail: " + v.ParserError)
	}
	return b.String()
}

func pluginValidationBannerHeights(c *canvas, failures []string, width int) []int {
	if c == nil || len(failures) == 0 || width <= 0 {
		return nil
	}
	textW := maxInt(c.s(40), width-c.s(28))
	heights := make([]int, 0, len(failures))
	for _, failure := range failures {
		if strings.TrimSpace(failure) == "" {
			continue
		}
		textH := c.textHeight(failure, textW, fonts.SmallStrong)
		heights = append(heights, maxInt(c.s(36), textH+c.s(14)))
	}
	return heights
}

func drawPluginValidationBanners(c *canvas, failures []string, heights []int, x, y, w int) int {
	if c == nil || len(failures) == 0 || len(heights) == 0 || w <= 0 {
		return 0
	}
	used := 0
	hi := 0
	for _, failure := range failures {
		failure = strings.TrimSpace(failure)
		if failure == "" || hi >= len(heights) {
			continue
		}
		h := heights[hi]
		hi++
		by := y + used
		c.roundedRect(x, by, w, h, c.s(8), palette.BadSoft, palette.Bad)
		c.text(failure, x+c.s(14), by+c.s(5), w-c.s(28), h-c.s(10), fonts.SmallStrong, palette.Bad, dtCenter|dtVCenter|dtWordBreak)
		used += h
		if hi < len(heights) {
			used += c.s(8)
		}
	}
	return used
}

func drawPluginDetail(c *canvas, x, y, w, h int) {
	c.card(x, y, w, h)
	p := c.app.selectedPlugin()
	if p == nil {
		c.text("Select a plugin to inspect it.", x+c.s(24), y+c.s(40), w-c.s(48), c.s(90), fonts.Body, palette.TextMuted, dtCenter|dtVCenter|dtWordBreak)
		return
	}
	c.label("Plugin details", x+c.s(20), y+c.s(16), w-c.s(40), palette.TextMuted)
	c.text(p.Manifest.Name, x+c.s(20), y+c.s(37), w-c.s(40), c.s(36), fonts.Heading, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	sourceLine := "Local plugin"
	if p.WorkshopID != "" {
		sourceLine = "Workshop ID  " + p.WorkshopID
	}
	c.text(sourceLine, x+c.s(20), y+c.s(72), w-c.s(40), c.s(24), fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)

	thumbX, thumbY := x+c.s(20), y+c.s(105)
	thumbW := detailThumbnailWidth(c, w)
	thumbH := c.s(108)
	c.roundedRect(thumbX, thumbY, thumbW, thumbH, c.s(9), palette.Background, palette.Border)
	if image := c.app.ensurePluginImage(); image != 0 {
		drawGDIPlusImageContainedNative(c.dc, image, thumbX+c.s(3), thumbY+c.s(3), thumbW-c.s(6), thumbH-c.s(6))
	} else {
		c.text("No package preview", thumbX+c.s(8), thumbY, thumbW-c.s(16), thumbH, fonts.Small, palette.TextFaint, dtCenter|dtVCenter|dtSingleLine|dtEndEllipsis)
	}

	pluginState, pluginStateClass := "DISABLED", 3
	if !p.BasicValid {
		pluginState, pluginStateClass = "INVALID", 2
	} else if p.Setting.Enabled {
		pluginState, pluginStateClass = "ENABLED", 0
	}
	metaX := thumbX + thumbW + c.s(14)
	metaW := maxInt(c.s(82), x+w-metaX-c.s(20))
	c.pill(pluginState, pluginStateClass, metaX, thumbY+c.s(8), minInt(c.s(104), metaW), c.s(28))
	c.text(pluginInterfaceSummary(p), metaX, thumbY+c.s(42), metaW, c.s(22), fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text(pluginLoadPathSummary(p), metaX, thumbY+c.s(65), metaW, c.s(22), fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	thirdLine := "Binary: " + p.PEValidation.Architecture + " • " + p.PEValidation.PEFormat
	thirdColor := palette.TextMuted
	if item := c.app.pluginWorkshopItem(p); item != nil {
		label := workshopUpdateShortLabel(item)
		if label != "" {
			thirdLine = "Update: " + strings.ToUpper(label)
			switch item.UpdateState {
			case "current":
				thirdColor = palette.Good
			case "update", "stale":
				thirdColor = palette.Warn
			case "missing", "unavailable":
				thirdColor = palette.Bad
			case "downloading":
				thirdColor = palette.AccentBright
			}
		}
	}
	c.text(thirdLine, metaX, thumbY+c.s(87), metaW, c.s(18), fonts.Tiny, thirdColor, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)

	buttonY3 := y + h - c.s(60)
	buttonY2 := buttonY3 - c.s(48)
	buttonY1 := buttonY2 - c.s(48)
	buttonTop := buttonY3
	if p.WorkshopID != "" {
		buttonTop = buttonY1
	}
	controlY := buttonTop - c.s(64)
	heroBottom := thumbY + thumbH
	descLabelY := heroBottom + c.s(14)
	descTextY := descLabelY + c.s(20)
	if controlY < descTextY+c.s(42) {
		controlY = descTextY + c.s(42)
	}
	descH := maxInt(c.s(36), controlY-descTextY-c.s(10))
	c.label("Description", x+c.s(20), descLabelY, w-c.s(40), palette.TextFaint)
	body := p.Manifest.Description
	if len(p.Manifest.Dependencies) > 0 {
		body += "\n\nDependencies: " + strings.Join(p.Manifest.Dependencies, "  •  ")
	}
	if len(p.Manifest.RequiredSymbols) > 0 {
		body += "\n\nRequired symbols: " + strings.Join(p.Manifest.RequiredSymbols, "  •  ")
	}
	// Invalid PE-admission failures have their own high-visibility banners below,
	// so do not repeat those same strings in the ordinary metadata paragraph.
	if len(p.Reasons) > 0 && p.BasicValid {
		body += "\n\nRuntime / metadata notes: " + strings.Join(p.Reasons, "  •  ")
	}
	body += "\n\n" + pluginValidationDetails(p)
	bannerW := w - c.s(40)
	bannerHeights := []int(nil)
	bannerBlockH := 0
	if !p.BasicValid {
		bannerHeights = pluginValidationBannerHeights(c, p.PEValidation.Failures, bannerW)
		for i, bh := range bannerHeights {
			bannerBlockH += bh
			if i+1 < len(bannerHeights) {
				bannerBlockH += c.s(8)
			}
		}
	}
	if bannerBlockH > 0 {
		// Reserve banner space inside the details body. The textual description
		// remains independently scrollable if it is long, while every validation
		// failure remains immediately visible without being buried in prose.
		descH = maxInt(c.s(36), descH-bannerBlockH-c.s(10))
	}
	drawScrollableDescription(c, 0, body, x+c.s(20), descTextY, w-c.s(40), descH, fonts.Small, palette.TextMuted)
	if bannerBlockH > 0 {
		drawPluginValidationBanners(c, p.PEValidation.Failures, bannerHeights, x+c.s(20), descTextY+descH+c.s(10), bannerW)
	}

	c.divider(x+c.s(20), controlY, w-c.s(40))
	controlY += c.s(10)
	c.text("Callback priority", x+c.s(20), controlY, w-c.s(40), c.s(22), fonts.BodyStrong, palette.Text, dtLeft|dtVCenter|dtSingleLine)
	c.text("Lower priorities run earlier within their phase.", x+c.s(20), controlY+c.s(23), w-c.s(170), c.s(19), fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.compactButton("priority-minus", "− 10", x+w-c.s(170), controlY+c.s(4), c.s(48), c.s(34))
	c.roundedRect(x+w-c.s(116), controlY+c.s(4), c.s(42), c.s(34), c.s(8), palette.Background, palette.Border)
	c.text(fmt.Sprintf("%d", p.Setting.Priority), x+w-c.s(116), controlY+c.s(4), c.s(42), c.s(34), fonts.SmallStrong, palette.Text, dtCenter|dtVCenter|dtSingleLine)
	c.compactButton("priority-plus", "+ 10", x+w-c.s(68), controlY+c.s(4), c.s(48), c.s(34))

	settingsLabel := "Open plugin INI / Settings"
	settingsAvailable := p.SettingsPath != "" || (p.SettingsCount > 1 && p.SettingsDir != "")
	if p.SettingsPath == "" {
		if p.SettingsCount > 1 && p.SettingsDir != "" {
			settingsLabel = "Open settings folder"
		} else {
			settingsLabel = "No plugin INI found"
		}
	}
	if p.WorkshopID != "" {
		gap := c.s(8)
		buttonW := (w - c.s(40) - gap) / 2
		c.buttonEnabled("plugin-open-settings", settingsLabel, x+c.s(20), buttonY1, w-c.s(40), c.s(40), false, settingsAvailable)
		c.button("plugin-workshop-open-page", "Open Workshop", x+c.s(20), buttonY2, buttonW, c.s(40), false)
		c.button("plugin-workshop-open-folder", "Open file location", x+c.s(20)+buttonW+gap, buttonY2, buttonW, c.s(40), false)
		canAction := !c.app.workshopActionBusy && !c.app.workshopUpdateBusy
		if item := c.app.workshopItemByID(p.WorkshopID); item != nil && item.Unsubscribed {
			canAction = false
		}
		c.button("plugin-workshop-force-update", "Force Update", x+c.s(20), buttonY3, buttonW, c.s(40), true)
		c.dangerButton("plugin-workshop-unsubscribe", "Unsubscribe", x+c.s(20)+buttonW+gap, buttonY3, buttonW, c.s(40), canAction)
	} else {
		c.buttonEnabled("plugin-open-settings", settingsLabel, x+c.s(20), buttonY3, w-c.s(40), c.s(40), false, settingsAvailable)
	}
}

func drawDevelopmentTable(c *canvas, x, y, w, h int) {
	c.card(x, y, w, h)
	headerH := c.s(42)
	c.fillRect(x+c.s(1), y+c.s(1), w-c.s(2), headerH, palette.SurfaceRaised)
	statusW := c.s(146)
	statusX := x + w - statusW - c.s(8)
	idW := c.s(92)
	idX := statusX - idW - c.s(10)
	c.text("DEVELOPMENT MOD", x+c.s(18), y, maxInt(c.s(60), idX-x-c.s(28)), headerH, fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text("PROJECT ID", idX, y, idW, headerH, fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text("STATUS", statusX, y, statusW, headerH, fonts.Tiny, palette.TextMuted, dtCenter|dtVCenter|dtSingleLine)

	rowH := c.s(61)
	maxRows := maxInt(1, (h-headerH-c.s(6))/rowH)
	visible := c.app.visibleDevelopmentIndices()
	c.app.developmentScroll = clampListScroll(c.app.developmentScroll, len(visible), maxRows)
	offset := c.app.developmentScroll
	rowY := y + headerH
	end := minInt(len(visible), offset+maxRows)
	for pos := offset; pos < end; pos++ {
		i := visible[pos]
		item := &c.app.developmentItems[i]
		selected := c.app.selectedDevelopment == i
		id := fmt.Sprintf("development-row:%d", i)
		fill := palette.Surface
		if selected {
			fill = palette.AccentSoft
		} else if c.app.hover == id {
			fill = palette.SurfaceHover
		}
		c.fillRect(x+c.s(1), rowY, w-c.s(2), rowH, fill)
		if selected {
			c.fillRect(x+c.s(1), rowY, c.s(4), rowH, palette.AccentBright)
		}
		nameW := maxInt(c.s(60), idX-x-c.s(28))
		c.text(item.Name, x+c.s(18), rowY+c.s(6), nameW, c.s(25), fonts.BodyStrong, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		sub := "Local unpublished project"
		subColor := palette.TextFaint
		if len(item.EmbeddedPluginIDs) > 0 {
			sub = fmt.Sprintf("Local unpublished project  •  %d embedded DLL(s)", len(item.EmbeddedPluginIDs))
			subColor = palette.AccentBright
		}
		if len(item.Issues) > 0 {
			sub += fmt.Sprintf("  •  %d issue(s)", len(item.Issues))
			subColor = palette.Warn
		}
		c.text(sub, x+c.s(18), rowY+c.s(31), nameW, c.s(20), fonts.Tiny, subColor, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		c.text(item.ID, idX, rowY, idW, rowH, fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		c.statusToggle(fmt.Sprintf("development-toggle:%d", i), item.Enabled, statusX, rowY+c.s(15), statusW, c.s(31))
		c.addHit(id, x, rowY, maxInt(c.s(20), statusX-x-c.s(4)), rowH)
		c.line(x+c.s(15), rowY+rowH-1, x+w-c.s(15), rowY+rowH-1, 1, palette.Border)
		rowY += rowH
	}
	c.app.modListX, c.app.modListY, c.app.modListW, c.app.modListH = x, y+headerH, w, h-headerH
	drawListScrollbar(c, "development", x+w-c.s(10), y+headerH+c.s(4), c.s(6), h-headerH-c.s(8), len(visible), maxRows, offset)
	if len(visible) == 0 {
		msg := "No development mods match the current search/filter."
		if len(c.app.developmentItems) == 0 {
			msg = "No numeric development project folders were found under media_soviet\\workshop_wip."
		}
		c.text(msg, x+c.s(22), y+c.s(70), w-c.s(44), c.s(80), fonts.Body, palette.TextMuted, dtCenter|dtVCenter|dtWordBreak)
	}
}

func drawDevelopmentDetail(c *canvas, x, y, w, h int) {
	c.card(x, y, w, h)
	item := c.app.selectedDevelopmentItem()
	if item == nil {
		c.text("Select a local development project to inspect it.", x+c.s(24), y+c.s(40), w-c.s(48), c.s(90), fonts.Body, palette.TextMuted, dtCenter|dtVCenter|dtWordBreak)
		return
	}
	c.label("Development mod", x+c.s(20), y+c.s(16), w-c.s(40), palette.TextMuted)
	c.text(item.Name, x+c.s(20), y+c.s(37), w-c.s(40), c.s(36), fonts.Heading, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text("Workshop ID  "+item.ID, x+c.s(20), y+c.s(72), w-c.s(40), c.s(24), fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)

	thumbX, thumbY := x+c.s(20), y+c.s(105)
	thumbW := detailThumbnailWidth(c, w)
	thumbH := c.s(108)
	c.roundedRect(thumbX, thumbY, thumbW, thumbH, c.s(9), palette.Background, palette.Border)
	if image := c.app.ensureDevelopmentImage(); image != 0 {
		drawGDIPlusImageContainedNative(c.dc, image, thumbX+c.s(3), thumbY+c.s(3), thumbW-c.s(6), thumbH-c.s(6))
	} else {
		c.text("No preview image", thumbX+c.s(8), thumbY, thumbW-c.s(16), thumbH, fonts.Small, palette.TextFaint, dtCenter|dtVCenter|dtSingleLine|dtEndEllipsis)
	}
	state, stateClass := "DISABLED", 3
	if item.Enabled {
		state, stateClass = "ENABLED", 0
	}
	metaX := thumbX + thumbW + c.s(14)
	metaW := maxInt(c.s(82), x+w-metaX-c.s(20))
	c.pill(state, stateClass, metaX, thumbY+c.s(8), minInt(c.s(104), metaW), c.s(28))
	packageType := "Development Workshop project"
	if len(item.EmbeddedPluginIDs) > 0 {
		packageType = fmt.Sprintf("Hybrid package • %d embedded DLL(s)", len(item.EmbeddedPluginIDs))
	}
	c.text(packageType, metaX, thumbY+c.s(42), metaW, c.s(40), fonts.Tiny, palette.TextMuted, dtLeft|dtWordBreak)
	if len(item.Issues) > 0 {
		c.text(fmt.Sprintf("Needs attention • %d project check(s)", len(item.Issues)), metaX, thumbY+c.s(86), metaW, c.s(20), fonts.Tiny, palette.Warn, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	}

	rowY := thumbY + thumbH + c.s(15)
	c.label("Embedded DLL payloads", x+c.s(20), rowY, w-c.s(40), palette.TextFaint)
	rowY += c.s(20)
	payloads := "None"
	if len(item.EmbeddedPluginIDs) > 0 {
		payloads = strings.Join(item.EmbeddedPluginIDs, "  •  ")
	}
	payloadH := minInt(c.s(42), maxInt(c.s(20), c.textHeight(payloads, w-c.s(40), fonts.Small)))
	c.text(payloads, x+c.s(20), rowY, w-c.s(40), payloadH, fonts.Small, palette.TextMuted, dtLeft|dtWordBreak)
	rowY += payloadH + c.s(10)
	c.label("Description", x+c.s(20), rowY, w-c.s(40), palette.TextFaint)
	rowY += c.s(20)

	buttonY := y + h - c.s(60)
	descH := maxInt(c.s(45), buttonY-rowY-c.s(12))
	description := item.Description
	if len(item.Issues) > 0 {
		description += "\n\nProject checks:\n• " + strings.Join(item.Issues, "\n• ")
	}
	drawScrollableDescription(c, 2, description, x+c.s(20), rowY, w-c.s(40), descH, fonts.Small, palette.TextMuted)

	gap := c.s(8)
	buttonW := (w - c.s(40) - gap) / 2
	c.button("development-open-folder", "Open project folder", x+c.s(20), buttonY, buttonW, c.s(40), false)
	configLabel := "Open workshopconfig.ini"
	configAvailable := item.ConfigPath != ""
	if !configAvailable {
		configLabel = "Config not found"
	}
	c.buttonEnabled("development-open-config", configLabel, x+c.s(20)+buttonW+gap, buttonY, buttonW, c.s(40), false, configAvailable)
}

func drawWorkshopTable(c *canvas, x, y, w, h int) {
	c.card(x, y, w, h)
	headerH := c.s(42)
	c.fillRect(x+c.s(1), y+c.s(1), w-c.s(2), headerH, palette.SurfaceRaised)
	statusW := c.s(146)
	statusX := x + w - statusW - c.s(8)
	idW := c.s(92)
	idX := statusX - idW - c.s(10)
	nameLabel := workshopSortLabel("WORKSHOP MOD", "name", c.app)
	idLabel := workshopSortLabel("ITEM ID", "id", c.app)
	statusLabel := workshopSortLabel("STATUS", "on", c.app)
	nameColor, idColor, statusColor := palette.TextMuted, palette.TextMuted, palette.TextMuted
	if c.app.workshopSort == "name" {
		nameColor = palette.AccentBright
	}
	if c.app.workshopSort == "id" {
		idColor = palette.AccentBright
	}
	if c.app.workshopSort == "on" {
		statusColor = palette.AccentBright
	}
	c.text(nameLabel, x+c.s(18), y, maxInt(c.s(60), idX-x-c.s(28)), headerH, fonts.Tiny, nameColor, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text(idLabel, idX, y, idW, headerH, fonts.Tiny, idColor, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text(statusLabel, statusX, y, statusW, headerH, fonts.Tiny, statusColor, dtCenter|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.addHit("workshop-sort:name", x, y, maxInt(c.s(20), idX-x), headerH)
	c.addHit("workshop-sort:id", idX, y, idW, headerH)
	c.addHit("workshop-sort:on", statusX, y, statusW, headerH)

	rowH := c.s(61)
	maxRows := maxInt(1, (h-headerH-c.s(6))/rowH)
	visible := c.app.visibleWorkshopIndices()
	c.app.workshopScroll = clampListScroll(c.app.workshopScroll, len(visible), maxRows)
	offset := c.app.workshopScroll
	rowY := y + headerH
	end := minInt(len(visible), offset+maxRows)
	for pos := offset; pos < end; pos++ {
		i := visible[pos]
		item := &c.app.workshopItems[i]
		selected := c.app.selectedWorkshop == i
		id := fmt.Sprintf("workshop-row:%d", i)
		fill := palette.Surface
		if selected {
			fill = palette.AccentSoft
		} else if c.app.hover == id {
			fill = palette.SurfaceHover
		}
		c.fillRect(x+c.s(1), rowY, w-c.s(2), rowH, fill)
		if selected {
			c.fillRect(x+c.s(1), rowY, c.s(4), rowH, palette.AccentBright)
		}
		nameW := maxInt(c.s(60), idX-x-c.s(28))
		c.text(item.Name, x+c.s(18), rowY+c.s(6), nameW, c.s(25), fonts.BodyStrong, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		sub := "Steam Workshop"
		subColor := palette.TextFaint
		if item.Unsubscribed {
			sub = "Unsubscribed"
			subColor = palette.Bad
		} else if item.UpdateState == "stale" {
			sub = "Potentially stale — " + item.UpdateReason
			subColor = palette.Warn
		} else if item.UpdateState == "update" {
			sub = "Update available — " + item.UpdateReason
			subColor = palette.Warn
		} else if item.UpdateState == "downloading" {
			sub = "Steam update downloading / queued"
			subColor = palette.AccentBright
		} else if item.UpdateState == "missing" {
			sub = "Installed copy missing"
			subColor = palette.Bad
		} else if item.UpdateState == "current" {
			sub = "Steam Workshop — up to date"
			subColor = palette.Good
		} else if item.Unsupported {
			sub = "Needs attention"
			if len(item.UnsupportedReasons) > 0 {
				sub += " — " + item.UnsupportedReasons[0]
			}
			subColor = palette.Bad
		} else if item.RemoteSupportState == "ok" {
			sub = "Steam Workshop — page verified"
		}
		c.text(sub, x+c.s(18), rowY+c.s(31), nameW, c.s(20), fonts.Tiny, subColor, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		c.text(item.ID, idX, rowY, idW, rowH, fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		c.statusToggle(fmt.Sprintf("workshop-toggle:%d", i), item.Enabled, statusX, rowY+c.s(15), statusW, c.s(31))
		c.addHit(id, x, rowY, maxInt(c.s(20), statusX-x-c.s(4)), rowH)
		c.line(x+c.s(15), rowY+rowH-1, x+w-c.s(15), rowY+rowH-1, 1, palette.Border)
		rowY += rowH
	}
	c.app.modListX, c.app.modListY, c.app.modListW, c.app.modListH = x, y+headerH, w, h-headerH
	drawListScrollbar(c, "workshop", x+w-c.s(10), y+headerH+c.s(4), c.s(6), h-headerH-c.s(8), len(visible), maxRows, offset)
	if len(visible) == 0 {
		msg := "No Workshop mods match the current search/filter."
		if len(c.app.workshopItems) == 0 {
			msg = "No installed WRSR Workshop content was found in this Steam library."
		}
		c.text(msg, x+c.s(22), y+c.s(70), w-c.s(44), c.s(70), fonts.Body, palette.TextMuted, dtCenter|dtVCenter|dtWordBreak)
	}
}

func drawWorkshopDetail(c *canvas, x, y, w, h int) {
	c.card(x, y, w, h)
	item := c.app.selectedWorkshopItem()
	if item == nil {
		c.text("Select an installed Workshop mod to inspect its local metadata.", x+c.s(24), y+c.s(40), w-c.s(48), c.s(90), fonts.Body, palette.TextMuted, dtCenter|dtVCenter|dtWordBreak)
		return
	}
	c.label("Workshop details", x+c.s(20), y+c.s(16), w-c.s(40), palette.TextMuted)
	c.text(item.Name, x+c.s(20), y+c.s(37), w-c.s(40), c.s(36), fonts.Heading, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text("Workshop ID  "+item.ID, x+c.s(20), y+c.s(72), w-c.s(40), c.s(24), fonts.Small, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)

	thumbX, thumbY := x+c.s(20), y+c.s(105)
	if item.Unsupported {
		reason := "Needs attention"
		if len(item.UnsupportedReasons) > 0 {
			reason += ": " + strings.Join(item.UnsupportedReasons, "; ")
		}
		c.text(reason, x+c.s(20), y+c.s(96), w-c.s(40), c.s(29), fonts.Tiny, palette.Bad, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		thumbY = y + c.s(126)
	}
	thumbW := detailThumbnailWidth(c, w)
	thumbH := c.s(108)
	c.roundedRect(thumbX, thumbY, thumbW, thumbH, c.s(9), palette.Background, palette.Border)
	image := c.app.ensureWorkshopImage()
	if image != 0 {
		drawGDIPlusImageContainedNative(c.dc, image, thumbX+c.s(3), thumbY+c.s(3), thumbW-c.s(6), thumbH-c.s(6))
	} else {
		c.text("No local thumbnail", thumbX+c.s(8), thumbY, thumbW-c.s(16), thumbH, fonts.Small, palette.TextFaint, dtCenter|dtVCenter|dtSingleLine|dtEndEllipsis)
	}
	state := "DISABLED"
	class := 3
	if item.Enabled {
		state, class = "ENABLED", 0
	}
	if item.Unsubscribed {
		state, class = "UNSUBSCRIBED", 2
	}
	c.pill(state, class, thumbX+thumbW+c.s(14), thumbY+c.s(8), c.s(104), c.s(28))
	workshopStateDetail := "Playlist inclusion"
	if len(item.EmbeddedPluginIDs) > 0 {
		workshopStateDetail = fmt.Sprintf("Hybrid package • %d linked plugin(s)\nEnable state is coupled", len(item.EmbeddedPluginIDs))
	}
	c.text(workshopStateDetail, thumbX+thumbW+c.s(14), thumbY+c.s(42), maxInt(c.s(80), w-thumbW-c.s(54)), c.s(48), fonts.Tiny, palette.TextMuted, dtLeft|dtWordBreak)

	descY := thumbY + thumbH + c.s(16)
	if item.UpdateState != "" {
		updateLabel := strings.ToUpper(item.UpdateState)
		updateColor := palette.TextMuted
		if item.UpdateState == "current" {
			updateColor = palette.Good
		}
		if item.UpdateState == "update" || item.UpdateState == "stale" {
			updateColor = palette.Warn
		}
		if item.UpdateState == "missing" || item.UpdateState == "unavailable" {
			updateColor = palette.Bad
		}
		c.label("Update status", x+c.s(20), descY, w-c.s(40), palette.TextFaint)
		descY += c.s(19)
		c.text(updateLabel+"  •  "+item.UpdateReason, x+c.s(20), descY, w-c.s(40), c.s(30), fonts.Tiny, updateColor, dtLeft|dtWordBreak)
		descY += c.s(31)
		times := "Installed: " + formatWorkshopUpdateTime(item.InstalledUpdatedUnix) + "   •   Workshop: " + formatWorkshopUpdateTime(item.RemoteUpdatedUnix)
		c.text(times, x+c.s(20), descY, w-c.s(40), c.s(20), fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
		descY += c.s(25)
	}
	c.label("Description", x+c.s(20), descY, w-c.s(40), palette.TextFaint)
	descY += c.s(20)
	buttonY2 := y + h - c.s(62)
	buttonY1 := buttonY2 - c.s(48)
	descH := maxInt(c.s(55), buttonY1-descY-c.s(12))
	drawScrollableDescription(c, 1, item.Description, x+c.s(20), descY, w-c.s(40), descH, fonts.Small, palette.TextMuted)
	gap := c.s(8)
	buttonW := (w - c.s(40) - gap) / 2
	c.button("workshop-open-page", "Open Workshop", x+c.s(20), buttonY1, buttonW, c.s(40), false)
	c.button("workshop-open-folder", "Open file location", x+c.s(20)+buttonW+gap, buttonY1, buttonW, c.s(40), false)
	c.button("workshop-force-update", "Force Update", x+c.s(20), buttonY2, buttonW, c.s(40), true)
	canUnsubscribe := !c.app.workshopActionBusy && !c.app.workshopUpdateBusy && !item.Unsubscribed
	c.dangerButton("workshop-unsubscribe", "Unsubscribe", x+c.s(20)+buttonW+gap, buttonY2, buttonW, c.s(40), canUnsubscribe)
}

func drawDiagnosticsPage(c *canvas, x, y, w, h int) {
	cursor := c.sectionTitle("SUPPORT & DIAGNOSTICS", "Diagnostics", "", x, y, w)
	gap := c.s(14)
	topH := c.s(104)
	cardW := (w - gap*2) / 3
	state, detail, cls := "No report", "No WRSR session recorded yet", 1
	if c.app.report != nil {
		if c.app.report.BootstrapStatus == 0 {
			state, detail, cls = "OK", "Last RML bootstrap completed", 0
		} else {
			state, detail, cls = fmt.Sprintf("0x%X", c.app.report.BootstrapStatus), "Last bootstrap reported an error", 2
		}
	}
	c.metricCard("Last session", state, detail, x, cursor, cardW, topH, cls)
	detailed := "Off"
	if c.app.cfg.VerboseDiagnostics {
		detailed = "On"
	}
	c.metricCard("Detailed logging", detailed, "Full troubleshooting trace is optional", x+cardW+gap, cursor, cardW, topH, map[bool]int{true: 1, false: 0}[c.app.cfg.VerboseDiagnostics])
	archives := "Manual only"
	if c.app.cfg.AutoCrashArchives {
		archives = "Automatic"
	}
	c.metricCard("Crash archives", archives, "Timestamped ZIP on abnormal launch/exit", x+(cardW+gap)*2, cursor, cardW, topH, 0)
	cursor += topH + c.s(14)

	buttons := []struct {
		ID    string
		Label string
	}{
		{"refresh", "Refresh"},
		{"open-log", "Open session log"},
		{"open-verbose-log", "Open detailed log"},
		{"create-support-archive", "Create support ZIP"},
		{"open-crash-folder", "Crash archives"},
	}
	buttonGap := c.s(7)
	innerW := w - c.s(14)
	lineW, rows := 0, 1
	widths := make([]int, len(buttons))
	for i, b := range buttons {
		bw := maxInt(c.s(72), c.textWidth(b.Label, fonts.SmallStrong)+c.s(20))
		widths[i] = bw
		if lineW > 0 && lineW+buttonGap+bw > innerW {
			rows++
			lineW = bw
		} else {
			if lineW > 0 {
				lineW += buttonGap
			}
			lineW += bw
		}
	}
	buttonH := c.s(34)
	toolbarH := c.s(10) + rows*buttonH + (rows-1)*c.s(5)
	c.roundedRect(x, cursor, w, toolbarH, c.s(10), palette.Surface, palette.Border)
	bx, by := x+c.s(7), cursor+c.s(5)
	for i, b := range buttons {
		bw := widths[i]
		if bx > x+c.s(7) && bx+bw > x+w-c.s(7) {
			bx = x + c.s(7)
			by += buttonH + c.s(5)
		}
		c.compactButton(b.ID, b.Label, bx, by, bw, buttonH)
		bx += bw + buttonGap
	}
	cursor += toolbarH + c.s(12)

	logH := h - (cursor - y) - c.s(10)
	c.roundedRect(x, cursor, w, logH, c.s(12), rgb(7, 10, 14), palette.Border)
	c.fillRect(x+c.s(1), cursor+c.s(1), c.s(5), logH-c.s(2), palette.Accent)
	c.label("Latest session — high-level tail", x+c.s(22), cursor+c.s(14), w-c.s(44), palette.TextFaint)
	log := c.app.logTail
	if len(log) > 16000 {
		log = log[len(log)-16000:]
	}
	c.text(log, x+c.s(22), cursor+c.s(40), w-c.s(44), logH-c.s(54), fonts.Mono, palette.TextMuted, dtLeft|dtWordBreak)
}

func hitAt(app *application, x, y int) string {
	// Later-drawn regions win, which lets toggles sit over their parent row.
	for i := len(app.hits) - 1; i >= 0; i-- {
		h := app.hits[i]
		if x >= h.X && y >= h.Y && x < h.X+h.W && y < h.Y+h.H {
			return h.ID
		}
	}
	return ""
}
