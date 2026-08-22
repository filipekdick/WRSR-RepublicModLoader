//go:build windows

package main

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	csHRedraw = 0x0002
	csVRedraw = 0x0001

	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000

	cwUseDefault = ^uintptr(0x7fffffff)
	swHide       = 0
	swShow       = 5
	swRestore    = 9

	wmCreate                     = 0x0001
	wmKeyDown                    = 0x0100
	wmChar                       = 0x0102
	wmDestroy                    = 0x0002
	wmSize                       = 0x0005
	wmPaint                      = 0x000F
	wmClose                      = 0x0010
	wmEraseBkgnd                 = 0x0014
	wmGetMinMaxInfo              = 0x0024
	wmMouseMove                  = 0x0200
	wmLButtonDown                = 0x0201
	wmLButtonUp                  = 0x0202
	wmMouseWheel                 = 0x020A
	wmMouseLeave                 = 0x02A3
	wmDropFiles                  = 0x0233
	wmTimer                      = 0x0113
	wmDpiChanged                 = 0x02E0
	wmApp                        = 0x8000
	wmRMLLaunchDone              = wmApp + 41
	wmRMLGameExited              = wmApp + 42
	wmRMLWorkshopUnsubDone       = wmApp + 43
	wmRMLWorkshopSupportDone     = wmApp + 44
	wmRMLWorkshopUpdateDone      = wmApp + 45
	wmRMLWorkshopForceUpdateDone = wmApp + 46

	tmeLeave = 0x00000002

	dtLeft        = 0x00000000
	dtCenter      = 0x00000001
	dtRight       = 0x00000002
	dtVCenter     = 0x00000004
	dtWordBreak   = 0x00000010
	dtSingleLine  = 0x00000020
	dtNoPrefix    = 0x00000800
	dtCalcRect    = 0x00000400
	dtEndEllipsis = 0x00008000

	transparent = 1
	psSolid     = 0
	srccopy     = 0x00CC0020

	ofnFileMustExist   = 0x00001000
	ofnPathMustExist   = 0x00000800
	ofnExplorer        = 0x00080000
	ofnNoChangeDir     = 0x00000008
	ofnOverwritePrompt = 0x00000002

	idcArrow       = 32512
	idiApplication = 32512

	colorWindow = 5

	vkReturn = 0x0D
	vkEscape = 0x1B
	vkPrior  = 0x21 // Page Up
	vkNext   = 0x22 // Page Down
	vkUp     = 0x26
	vkDown   = 0x28
	vkDelete = 0x2E
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }

type wndClassEx struct {
	CbSize     uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type msg struct {
	Hwnd     uintptr
	Message  uint32
	_        uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type paintStruct struct {
	Hdc       uintptr
	Erase     int32
	RcPaint   rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type minMaxInfo struct {
	Reserved     point
	MaxSize      point
	MaxPosition  point
	MinTrackSize point
	MaxTrackSize point
}

type trackMouseEvent struct {
	Size      uint32
	Flags     uint32
	HwndTrack uintptr
	HoverTime uint32
}

type openFileName struct {
	StructSize      uint32
	Owner           uintptr
	Instance        uintptr
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustomFilter uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	FileExtension   uint16
	DefExt          *uint16
	CustData        uintptr
	Hook            uintptr
	TemplateName    *uint16
	Reserved        unsafe.Pointer
	Reserved2       uint32
	FlagsEx         uint32
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdiplus  = syscall.NewLazyDLL("gdiplus.dll")

	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	procUpdateWindow                  = user32.NewProc("UpdateWindow")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procBeginPaint                    = user32.NewProc("BeginPaint")
	procEndPaint                      = user32.NewProc("EndPaint")
	procGetClientRect                 = user32.NewProc("GetClientRect")
	procInvalidateRect                = user32.NewProc("InvalidateRect")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procTrackMouseEvent               = user32.NewProc("TrackMouseEvent")
	procSetCapture                    = user32.NewProc("SetCapture")
	procReleaseCapture                = user32.NewProc("ReleaseCapture")
	procSetTimer                      = user32.NewProc("SetTimer")
	procKillTimer                     = user32.NewProc("KillTimer")
	procSetWindowPos                  = user32.NewProc("SetWindowPos")
	procGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procLoadIconW                     = user32.NewProc("LoadIconW")
	procDrawTextW                     = user32.NewProc("DrawTextW")
	procFillRect                      = user32.NewProc("FillRect")
	procOpenClipboard                 = user32.NewProc("OpenClipboard")
	procEmptyClipboard                = user32.NewProc("EmptyClipboard")
	procSetClipboardData              = user32.NewProc("SetClipboardData")
	procCloseClipboard                = user32.NewProc("CloseClipboard")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalFree   = kernel32.NewProc("GlobalFree")

	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	procCreatePen              = gdi32.NewProc("CreatePen")
	procRoundRect              = gdi32.NewProc("RoundRect")
	procEllipse                = gdi32.NewProc("Ellipse")
	procMoveToEx               = gdi32.NewProc("MoveToEx")
	procLineTo                 = gdi32.NewProc("LineTo")
	procSetBkMode              = gdi32.NewProc("SetBkMode")
	procSetTextColor           = gdi32.NewProc("SetTextColor")
	procCreateFontW            = gdi32.NewProc("CreateFontW")
	procSaveDC                 = gdi32.NewProc("SaveDC")
	procRestoreDC              = gdi32.NewProc("RestoreDC")
	procIntersectClipRect      = gdi32.NewProc("IntersectClipRect")

	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
	procGetOpenFileNameW      = comdlg32.NewProc("GetOpenFileNameW")
	procGetSaveFileNameW      = comdlg32.NewProc("GetSaveFileNameW")
	procShellExecuteW         = shell32.NewProc("ShellExecuteW")
	procDragAcceptFiles       = shell32.NewProc("DragAcceptFiles")
	procDragQueryFileW        = shell32.NewProc("DragQueryFileW")
	procDragFinish            = shell32.NewProc("DragFinish")

	procGdiplusStartup        = gdiplus.NewProc("GdiplusStartup")
	procGdiplusShutdown       = gdiplus.NewProc("GdiplusShutdown")
	procGdipLoadImageFromFile = gdiplus.NewProc("GdipLoadImageFromFile")
	procGdipDisposeImage      = gdiplus.NewProc("GdipDisposeImage")
	procGdipCreateFromHDC     = gdiplus.NewProc("GdipCreateFromHDC")
	procGdipDeleteGraphics    = gdiplus.NewProc("GdipDeleteGraphics")
	procGdipDrawImageRectI    = gdiplus.NewProc("GdipDrawImageRectI")
	procGdipGetImageWidth     = gdiplus.NewProc("GdipGetImageWidth")
	procGdipGetImageHeight    = gdiplus.NewProc("GdipGetImageHeight")
)

type gdiplusStartupInput struct {
	Version                  uint32
	DebugEventCallback       uintptr
	SuppressBackgroundThread int32
	SuppressExternalCodecs   int32
}

var gdiplusToken uintptr

func ensureGDIPlus() bool {
	if gdiplusToken != 0 {
		return true
	}
	input := gdiplusStartupInput{Version: 1}
	status, _, _ := procGdiplusStartup.Call(uintptr(unsafe.Pointer(&gdiplusToken)), uintptr(unsafe.Pointer(&input)), 0)
	return status == 0 && gdiplusToken != 0
}

func loadGDIPlusImage(path string) uintptr {
	if path == "" || !ensureGDIPlus() {
		return 0
	}
	p := utf16Ptr(path)
	var image uintptr
	status, _, _ := procGdipLoadImageFromFile.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&image)))
	if status != 0 {
		return 0
	}
	return image
}

func disposeGDIPlusImage(image uintptr) {
	if image != 0 {
		procGdipDisposeImage.Call(image)
	}
}

func drawGDIPlusImage(dc, image uintptr, x, y, w, h int) bool {
	if dc == 0 || image == 0 || w <= 0 || h <= 0 {
		return false
	}
	var graphics uintptr
	status, _, _ := procGdipCreateFromHDC.Call(dc, uintptr(unsafe.Pointer(&graphics)))
	if status != 0 || graphics == 0 {
		return false
	}
	defer procGdipDeleteGraphics.Call(graphics)
	status, _, _ = procGdipDrawImageRectI.Call(graphics, image, uintptr(x), uintptr(y), uintptr(w), uintptr(h))
	return status == 0
}

func gdiplusImageSize(image uintptr) (int, int, bool) {
	if image == 0 {
		return 0, 0, false
	}
	var w, h uint32
	sw, _, _ := procGdipGetImageWidth.Call(image, uintptr(unsafe.Pointer(&w)))
	sh, _, _ := procGdipGetImageHeight.Call(image, uintptr(unsafe.Pointer(&h)))
	if sw != 0 || sh != 0 || w == 0 || h == 0 {
		return 0, 0, false
	}
	return int(w), int(h), true
}

func drawGDIPlusImageContainedNative(dc, image uintptr, x, y, w, h int) bool {
	if dc == 0 || image == 0 || w <= 0 || h <= 0 {
		return false
	}
	iw, ih, ok := gdiplusImageSize(image)
	if !ok {
		return drawGDIPlusImage(dc, image, x, y, w, h)
	}
	dw, dh := iw, ih
	if dw > w || dh > h {
		sx := float64(w) / float64(dw)
		sy := float64(h) / float64(dh)
		scale := sx
		if sy < scale {
			scale = sy
		}
		dw = maxInt(1, int(float64(dw)*scale))
		dh = maxInt(1, int(float64(dh)*scale))
	}
	dx := x + (w-dw)/2
	dy := y + (h-dh)/2
	return drawGDIPlusImage(dc, image, dx, dy, dw, dh)
}
func shutdownGDIPlus() {
	if gdiplusToken != 0 {
		procGdiplusShutdown.Call(gdiplusToken)
		gdiplusToken = 0
	}
}

func utf16Ptr(value string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(value)
	return p
}

func rgb(r, g, b byte) uint32 { return uint32(r) | uint32(g)<<8 | uint32(b)<<16 }

func lowWord(v uintptr) int  { return int(int16(v & 0xFFFF)) }
func highWord(v uintptr) int { return int(int16((v >> 16) & 0xFFFF)) }

func invalidateWindow(hwnd uintptr) {
	if hwnd != 0 {
		procInvalidateRect.Call(hwnd, 0, 0)
	}
}

func postAppMessage(hwnd uintptr, message uint32, wparam, lparam uintptr) {
	if hwnd != 0 {
		procPostMessageW.Call(hwnd, uintptr(message), wparam, lparam)
	}
}

func openPath(path string) {
	if path == "" {
		return
	}
	verb := utf16Ptr("open")
	p := utf16Ptr(path)
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(p)), 0, 0, swShow)
}

func multiString(parts ...string) []uint16 {
	var out []uint16
	for _, part := range parts {
		u := syscall.StringToUTF16(part)
		out = append(out, u...)
	}
	out = append(out, 0)
	return out
}

func openGameDialog(hwnd uintptr, current string) string {
	file := make([]uint16, 32768)
	if current != "" {
		copy(file, syscall.StringToUTF16(current))
	}
	filter := multiString(
		"Workers & Resources executable", "SOVIET64.exe",
		"Windows executables", "*.exe",
		"All files", "*.*",
	)
	title := syscall.StringToUTF16("Select Workers & Resources: Soviet Republic executable")
	defExt := syscall.StringToUTF16("exe")
	var initial []uint16
	if current != "" {
		initial = syscall.StringToUTF16(filepath.Dir(current))
	}

	ofn := openFileName{
		StructSize:  uint32(unsafe.Sizeof(openFileName{})),
		Owner:       hwnd,
		Filter:      &filter[0],
		FilterIndex: 1,
		File:        &file[0],
		MaxFile:     uint32(len(file)),
		Title:       &title[0],
		Flags:       ofnFileMustExist | ofnPathMustExist | ofnExplorer | ofnNoChangeDir,
		DefExt:      &defExt[0],
	}
	if len(initial) > 0 {
		ofn.InitialDir = &initial[0]
	}
	r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(file)
}

func openPlaylistDialog(hwnd uintptr, initialDir string) string {
	file := make([]uint16, 32768)
	filter := multiString("Republic Mod Loader playlist", "*.json", "JSON files", "*.json", "All files", "*.*")
	title := syscall.StringToUTF16("Import Republic Mod Loader playlist")
	defExt := syscall.StringToUTF16("json")
	var initial []uint16
	if initialDir != "" {
		initial = syscall.StringToUTF16(initialDir)
	}
	ofn := openFileName{StructSize: uint32(unsafe.Sizeof(openFileName{})), Owner: hwnd, Filter: &filter[0], FilterIndex: 1, File: &file[0], MaxFile: uint32(len(file)), Title: &title[0], Flags: ofnFileMustExist | ofnPathMustExist | ofnExplorer | ofnNoChangeDir, DefExt: &defExt[0]}
	if len(initial) > 0 {
		ofn.InitialDir = &initial[0]
	}
	r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(file)
}

func savePlaylistDialog(hwnd uintptr, initialDir, filename string) string {
	file := make([]uint16, 32768)
	if filename != "" {
		copy(file, syscall.StringToUTF16(filename))
	}
	filter := multiString("Republic Mod Loader playlist", "*.json", "JSON files", "*.json", "All files", "*.*")
	title := syscall.StringToUTF16("Export Republic Mod Loader playlist")
	defExt := syscall.StringToUTF16("json")
	var initial []uint16
	if initialDir != "" {
		initial = syscall.StringToUTF16(initialDir)
	}
	ofn := openFileName{StructSize: uint32(unsafe.Sizeof(openFileName{})), Owner: hwnd, Filter: &filter[0], FilterIndex: 1, File: &file[0], MaxFile: uint32(len(file)), Title: &title[0], Flags: ofnPathMustExist | ofnExplorer | ofnNoChangeDir | ofnOverwritePrompt, DefExt: &defExt[0]}
	if len(initial) > 0 {
		ofn.InitialDir = &initial[0]
	}
	r, _, _ := procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(file)
}

func firstDroppedFile(hdrop uintptr) string {
	if hdrop == 0 {
		return ""
	}
	defer procDragFinish.Call(hdrop)
	n, _, _ := procDragQueryFileW.Call(hdrop, ^uintptr(0), 0, 0)
	if n == 0 {
		return ""
	}
	length, _, _ := procDragQueryFileW.Call(hdrop, 0, 0, 0)
	if length == 0 || length > 32767 {
		return ""
	}
	buf := make([]uint16, int(length)+1)
	procDragQueryFileW.Call(hdrop, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func copyTextToClipboard(hwnd uintptr, text string) error {
	const (
		cfUnicodeText = 13
		gmemMoveable  = 0x0002
		gmemZeroInit  = 0x0040
	)
	u := syscall.StringToUTF16(text)
	if len(u) == 0 {
		return nil
	}
	r, _, e := procOpenClipboard.Call(hwnd)
	if r == 0 {
		return winErr("OpenClipboard", e)
	}
	defer procCloseClipboard.Call()
	if r, _, e = procEmptyClipboard.Call(); r == 0 {
		return winErr("EmptyClipboard", e)
	}
	bytes := uintptr(len(u) * 2)
	h, _, e := procGlobalAlloc.Call(gmemMoveable|gmemZeroInit, bytes)
	if h == 0 {
		return winErr("GlobalAlloc", e)
	}
	owned := true
	defer func() {
		if owned {
			procGlobalFree.Call(h)
		}
	}()
	p, _, e := procGlobalLock.Call(h)
	if p == 0 {
		return winErr("GlobalLock", e)
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(u))
	copy(dst, u)
	procGlobalUnlock.Call(h)
	if r, _, e = procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		return winErr("SetClipboardData", e)
	}
	owned = false // clipboard owns the global allocation after success
	return nil
}

func applyModernWindowEffects(hwnd uintptr) {
	// Attributes are opportunistic: Windows 10 ignores unsupported values.
	dark := int32(1)
	procDwmSetWindowAttribute.Call(hwnd, 20, uintptr(unsafe.Pointer(&dark)), unsafe.Sizeof(dark))
	corner := int32(2) // DWMWCP_ROUND
	procDwmSetWindowAttribute.Call(hwnd, 33, uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
	backdrop := int32(2) // DWMSBT_MAINWINDOW / Mica on Windows 11
	procDwmSetWindowAttribute.Call(hwnd, 38, uintptr(unsafe.Pointer(&backdrop)), unsafe.Sizeof(backdrop))
}

func setPerMonitorDPIAware() {
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = (HANDLE)-4.
	procSetProcessDpiAwarenessContext.Call(^uintptr(3))
}

var procMessageBoxW = user32.NewProc("MessageBoxW")

func showFatalMessage(title, message string) {
	t := utf16Ptr(title)
	m := utf16Ptr(message)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10)
}

func showWindow(hwnd uintptr, command int) {
	if hwnd != 0 {
		procShowWindow.Call(hwnd, uintptr(command))
	}
}
