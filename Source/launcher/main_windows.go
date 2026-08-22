//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var activeApp *application
var windowProcCallback = syscall.NewCallback(windowProc)

func windowProc(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
	app := activeApp
	switch message {
	case wmCreate:
		if app != nil {
			app.hwnd = hwnd
			dpi, _, _ := procGetDpiForWindow.Call(hwnd)
			if dpi == 0 {
				dpi = 96
			}
			app.dpi = uint32(dpi)
			app.scale = float64(app.dpi) / 96.0
			applyModernWindowEffects(hwnd)
			procDragAcceptFiles.Call(hwnd, 1)
			procSetTimer.Call(hwnd, 1, 40, 0)
			// Update status is part of the normal Mod inventory now. Refresh it on
			// launcher startup in the background rather than requiring a manual scan.
			app.startWorkshopUpdateScan()
			if app.cfg.WorkshopNeedsAttentionOnly || app.cfg.PluginNeedsAttentionOnly {
				app.startWorkshopSupportScan(false)
			}
		}
		return 0
	case wmGetMinMaxInfo:
		if app != nil && lparam != 0 {
			info := (*minMaxInfo)(unsafe.Pointer(lparam))
			info.MinTrackSize.X = int32(1080.0 * app.scale)
			info.MinTrackSize.Y = int32(700.0 * app.scale)
			return 0
		}
	case wmSize:
		invalidateWindow(hwnd)
		return 0
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		var ps paintStruct
		hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		var rc rect
		procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		width := int(rc.Right - rc.Left)
		height := int(rc.Bottom - rc.Top)
		if width > 0 && height > 0 {
			memDC, _, _ := procCreateCompatibleDC.Call(hdc)
			bitmap, _, _ := procCreateCompatibleBitmap.Call(hdc, uintptr(width), uintptr(height))
			if memDC != 0 && bitmap != 0 {
				old, _, _ := procSelectObject.Call(memDC, bitmap)
				if app != nil {
					renderApplication(app, memDC, width, height)
				}
				procBitBlt.Call(hdc, 0, 0, uintptr(width), uintptr(height), memDC, 0, 0, srccopy)
				procSelectObject.Call(memDC, old)
				procDeleteObject.Call(bitmap)
			}
			if memDC != 0 {
				procDeleteDC.Call(memDC)
			}
		}
		procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case wmMouseMove:
		if app != nil {
			x, y := lowWord(lparam), highWord(lparam)
			app.mouseX, app.mouseY = x, y
			if app.modScrollDrag {
				app.updateModScrollDrag(y)
				invalidateWindow(hwnd)
			}
			if app.detailScrollDrag {
				app.updateDetailScrollDrag(y)
				invalidateWindow(hwnd)
			}
			hover := hitAt(app, x, y)
			// Cursor-following tooltips repaint while their hit region remains active.
			if hover != app.hover || hover == "copy-build-sha" || app.tooltipText != "" {
				app.hover = hover
				invalidateWindow(hwnd)
			}
			tme := trackMouseEvent{Size: uint32(unsafe.Sizeof(trackMouseEvent{})), Flags: tmeLeave, HwndTrack: hwnd}
			procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
		}
		return 0
	case wmMouseLeave:
		if app != nil && app.hover != "" {
			app.hover = ""
			app.mouseX, app.mouseY = -1, -1
			invalidateWindow(hwnd)
		}
		return 0
	case wmDropFiles:
		if app != nil && app.playlistImportModal {
			path := firstDroppedFile(wparam)
			if path != "" && strings.EqualFold(filepath.Ext(path), ".json") {
				app.importPlaylistFile(path)
			} else if path != "" {
				app.status = "Playlist import requires a JSON file"
			}
			invalidateWindow(hwnd)
		} else {
			procDragFinish.Call(wparam)
		}
		return 0
	case wmKeyDown:
		if app != nil && app.playlistImportModal {
			if wparam == vkEscape {
				app.handleAction("playlist-import-cancel")
				return 0
			}
		}
		if app != nil && app.workshopForceUpdateModal {
			switch wparam {
			case vkReturn:
				app.beginWorkshopForceUpdate()
				return 0
			case vkEscape:
				app.cancelWorkshopForceUpdate()
				return 0
			}
		}
		if app != nil && app.workshopUnsubscribeModal {
			switch wparam {
			case vkReturn:
				app.beginWorkshopUnsubscribe()
				return 0
			case vkEscape:
				app.cancelWorkshopUnsubscribe()
				return 0
			}
		}
		if app != nil && app.page == 1 && !app.workshopUnsubscribeModal && !app.workshopForceUpdateModal && !app.playlistModal && !app.playlistDeleteModal && !app.playlistDropdown {
			switch wparam {
			case vkUp:
				app.moveActiveModSelection(-1)
				invalidateWindow(hwnd)
				return 0
			case vkDown:
				app.moveActiveModSelection(1)
				invalidateWindow(hwnd)
				return 0
			case vkDelete:
				if !app.modSearchFocus {
					app.requestSelectedModUnsubscribe()
					invalidateWindow(hwnd)
					return 0
				}
			case vkPrior:
				app.scrollActiveMod(1, true)
				invalidateWindow(hwnd)
				return 0
			case vkNext:
				app.scrollActiveMod(-1, true)
				invalidateWindow(hwnd)
				return 0
			}
		}
	case wmChar:
		if app != nil && app.playlistModal {
			app.playlistKey(rune(wparam))
			return 0
		}
		if app != nil && app.modSearchFocus && app.page == 1 && !app.workshopUnsubscribeModal && !app.workshopForceUpdateModal {
			app.editModSearch(rune(wparam))
			invalidateWindow(hwnd)
			return 0
		}
	case wmMouseWheel:
		if app != nil && app.playlistDropdown {
			delta := highWord(wparam)
			if delta > 0 {
				app.playlistScroll--
			} else if delta < 0 {
				app.playlistScroll++
			}
			if app.playlistScroll < 0 {
				app.playlistScroll = 0
			}
			invalidateWindow(hwnd)
			return 0
		}
		if app != nil && app.page == 1 && !app.workshopUnsubscribeModal && !app.workshopForceUpdateModal && app.mouseX >= app.detailScrollX && app.mouseX < app.detailScrollX+app.detailScrollW && app.mouseY >= app.detailScrollY && app.mouseY < app.detailScrollY+app.detailScrollH {
			app.scrollActiveDetail(highWord(wparam))
			invalidateWindow(hwnd)
			return 0
		}
		if app != nil && app.page == 1 && !app.workshopUnsubscribeModal && !app.workshopForceUpdateModal && app.mouseX >= app.modListX && app.mouseX < app.modListX+app.modListW && app.mouseY >= app.modListY && app.mouseY < app.modListY+app.modListH {
			app.scrollActiveMod(highWord(wparam), false)
			invalidateWindow(hwnd)
			return 0
		}
	case wmLButtonDown:
		if app != nil && app.page == 1 && !app.workshopUnsubscribeModal && !app.workshopForceUpdateModal {
			x, y := lowWord(lparam), highWord(lparam)
			id := hitAt(app, x, y)
			if id == "plugin-scroll-thumb" || id == "workshop-scroll-thumb" || id == "development-scroll-thumb" {
				view := 0
				if id == "workshop-scroll-thumb" {
					view = 1
				} else if id == "development-scroll-thumb" {
					view = 2
				}
				app.beginModScrollDrag(view, y)
				if app.modScrollDrag {
					procSetCapture.Call(hwnd)
				}
				return 0
			}
			if id == "plugin-detail-scroll-thumb" || id == "workshop-detail-scroll-thumb" || id == "development-detail-scroll-thumb" {
				view := 0
				if id == "workshop-detail-scroll-thumb" {
					view = 1
				} else if id == "development-detail-scroll-thumb" {
					view = 2
				}
				app.beginDetailScrollDrag(view, y)
				if app.detailScrollDrag {
					procSetCapture.Call(hwnd)
				}
				return 0
			}
		}
	case wmLButtonUp:
		if app != nil {
			if app.modScrollDrag {
				app.updateModScrollDrag(highWord(lparam))
				app.endModScrollDrag()
				procReleaseCapture.Call()
				invalidateWindow(hwnd)
				return 0
			}
			if app.detailScrollDrag {
				app.updateDetailScrollDrag(highWord(lparam))
				app.endDetailScrollDrag()
				procReleaseCapture.Call()
				invalidateWindow(hwnd)
				return 0
			}
			id := hitAt(app, lowWord(lparam), highWord(lparam))
			if app.playlistModal && !strings.HasPrefix(id, "playlist-modal-") {
				return 0
			}
			if app.playlistDeleteModal && !strings.HasPrefix(id, "playlist-delete-") {
				return 0
			}
			if app.playlistImportModal && !strings.HasPrefix(id, "playlist-import-") {
				return 0
			}
			if app.workshopUnsubscribeModal && !strings.HasPrefix(id, "workshop-unsubscribe-") {
				return 0
			}
			if app.workshopForceUpdateModal && !strings.HasPrefix(id, "workshop-force-update-") {
				return 0
			}
			if app.modSearchFocus && id != "mod-search" && id != "mod-search-clear" {
				app.modSearchFocus = false
			}
			if app.playlistDropdown && id != "playlist-dropdown" && !strings.HasPrefix(id, "playlist-option:") {
				app.playlistDropdown = false
				invalidateWindow(hwnd)
				// Continue processing a real underlying control so the click does
				// not feel swallowed merely because it also closed the dropdown.
			}
			if id != "" && !app.busy {
				app.handleAction(id)
			}
		}
		return 0
	case wmTimer:
		if app != nil && app.busy {
			app.animation += 0.055
			if app.animation >= 1000 {
				app.animation = 0
			}
			invalidateWindow(hwnd)
		}
		return 0
	case wmDpiChanged:
		if app != nil {
			dpi := uint32(wparam & 0xFFFF)
			if dpi == 0 {
				dpi = 96
			}
			app.dpi = dpi
			app.scale = float64(dpi) / 96.0
			disposeFonts()
			if lparam != 0 {
				suggested := (*rect)(unsafe.Pointer(lparam))
				procSetWindowPos.Call(hwnd, 0, uintptr(suggested.Left), uintptr(suggested.Top), uintptr(suggested.Right-suggested.Left), uintptr(suggested.Bottom-suggested.Top), 0x0014)
			}
			invalidateWindow(hwnd)
		}
		return 0
	case wmRMLLaunchDone:
		if app != nil {
			app.finishLaunch()
		}
		return 0
	case wmRMLGameExited:
		if app != nil {
			code := uint32(wparam)
			wasDeep := normalizeLaunchProfile(app.cfg.LaunchProfile) == profileDeepTest
			app.status = fmt.Sprintf("WRSR exited with code 0x%X; behaviour journal merged", code)
			app.refresh(false)
			if wasDeep && app.lastSessionID != "" {
				diag := diagnosticLogPath(app.baseDir, profileDeepTest, app.lastSessionID)
				appendDiagnosticFileSection(diag, "COMPATIBILITY REPORT JSON", filepath.Join(app.baseDir, "rml", "state", "last_compatibility_report.json"))
				setLauncherDiagnosticMirror("")
				app.status = fmt.Sprintf("Detailed diagnostic session finished (exit 0x%X) — detailed log finalized", code)
			}
			if wasDeep && app.deepPreviousProfile != "" {
				app.cfg.LaunchProfile = string(app.deepPreviousProfile)
				app.deepPreviousProfile = ""
				_ = saveConfig(app.configPath, app.cfg)
			}
			if app.closeWhenGameExits {
				procDestroyWindow.Call(hwnd)
				return 0
			}
			showWindow(hwnd, swRestore)
			procSetForegroundWindow.Call(hwnd)
			invalidateWindow(hwnd)
		}
		return 0
	case wmRMLWorkshopUnsubDone:
		if app != nil {
			app.finishWorkshopUnsubscribe()
		}
		return 0
	case wmRMLWorkshopSupportDone:
		if app != nil {
			app.finishWorkshopSupportScan()
		}
		return 0
	case wmRMLWorkshopUpdateDone:
		if app != nil {
			app.finishWorkshopUpdateScan()
		}
		return 0
	case wmRMLWorkshopForceUpdateDone:
		if app != nil {
			app.finishWorkshopForceUpdate()
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procKillTimer.Call(hwnd, 1)
		if app != nil {
			app.releaseWorkshopImage()
			app.releaseDevelopmentImage()
			app.releasePluginImage()
			if app.brandImage != 0 {
				disposeGDIPlusImage(app.brandImage)
				app.brandImage = 0
			}
		}
		disposeFonts()
		shutdownGDIPlus()
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wparam, lparam)
	return r
}

func runGUI() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	setPerMonitorDPIAware()

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	base := filepath.Dir(exe)
	activeApp = newApplication(base)

	instance, _, callErr := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return fmt.Errorf("GetModuleHandleW: %v", callErr)
	}
	className := utf16Ptr("RepublicModLoaderWindowClass")
	title := utf16Ptr("Republic Mod Loader")
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	// Resource ID 1 is the packaged multi-resolution RML icon. Fall back to
	// the stock application icon only if the resource is unavailable.
	icon, _, _ := procLoadIconW.Call(instance, 1)
	if icon == 0 {
		icon, _, _ = procLoadIconW.Call(0, idiApplication)
	}
	wc := wndClassEx{
		CbSize:     uint32(unsafe.Sizeof(wndClassEx{})),
		Style:      csHRedraw | csVRedraw,
		WndProc:    windowProcCallback,
		Instance:   instance,
		Icon:       icon,
		Cursor:     cursor,
		Background: uintptr(colorWindow + 1),
		ClassName:  className,
		IconSm:     icon,
	}
	atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return fmt.Errorf("RegisterClassExW: %v", callErr)
	}

	hwnd, _, callErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow|wsVisible,
		cwUseDefault, cwUseDefault,
		1360, 840,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %v", callErr)
	}
	activeApp.hwnd = hwnd
	applyModernWindowEffects(hwnd)
	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)

	var m msg
	for {
		r, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) == -1 {
			return fmt.Errorf("GetMessageW: %v", callErr)
		}
		if r == 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func main() {
	if err := runGUI(); err != nil {
		showFatalMessage("Republic Mod Loader", err.Error())
	}
}
