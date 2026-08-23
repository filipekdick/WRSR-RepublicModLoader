//go:build windows

package main

import (
	"math"
	"strings"
	"syscall"
	"unsafe"
)

var palette = struct {
	Background, Sidebar, Surface, SurfaceRaised, SurfaceHover uint32
	Border, BorderBright, Text, TextMuted, TextFaint          uint32
	Accent, AccentSoft, AccentBright                          uint32
	Good, GoodSoft, Warn, WarnSoft, Bad, BadSoft              uint32
	White, Black                                              uint32
}{
	Background:    rgb(10, 14, 20),
	Sidebar:       rgb(13, 19, 27),
	Surface:       rgb(17, 25, 35),
	SurfaceRaised: rgb(22, 33, 46),
	SurfaceHover:  rgb(27, 40, 55),
	Border:        rgb(37, 51, 68),
	BorderBright:  rgb(54, 72, 94),
	Text:          rgb(236, 242, 248),
	TextMuted:     rgb(148, 164, 182),
	TextFaint:     rgb(99, 116, 135),
	Accent:        rgb(205, 67, 79),
	AccentSoft:    rgb(72, 31, 39),
	AccentBright:  rgb(242, 91, 102),
	Good:          rgb(75, 195, 139),
	GoodSoft:      rgb(26, 63, 50),
	Warn:          rgb(232, 184, 92),
	WarnSoft:      rgb(70, 55, 29),
	Bad:           rgb(239, 105, 113),
	BadSoft:       rgb(73, 35, 41),
	White:         rgb(255, 255, 255),
	Black:         rgb(0, 0, 0),
}

type uiFonts struct {
	Display, Heading, Title, Body, BodyStrong, Small, SmallStrong, Mono, Tiny uintptr
	dpi                                                                       uint32
}

var fonts uiFonts

func disposeFonts() {
	handles := []uintptr{fonts.Display, fonts.Heading, fonts.Title, fonts.Body, fonts.BodyStrong, fonts.Small, fonts.SmallStrong, fonts.Mono, fonts.Tiny}
	seen := map[uintptr]bool{}
	for _, h := range handles {
		if h != 0 && !seen[h] {
			procDeleteObject.Call(h)
			seen[h] = true
		}
	}
	fonts = uiFonts{}
}

func createUIFont(face string, points float64, weight int32, dpi uint32) uintptr {
	height := -int32(math.Round(points * float64(dpi) / 72.0))
	f := utf16Ptr(face)
	h, _, _ := procCreateFontW.Call(
		uintptr(height), 0, 0, 0, uintptr(weight),
		0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(f)),
	)
	return h
}

func ensureFonts(dpi uint32) {
	if dpi == 0 {
		dpi = 96
	}
	if fonts.dpi == dpi && fonts.Body != 0 {
		return
	}
	disposeFonts()
	fonts.dpi = dpi
	fonts.Display = createUIFont("Segoe UI Variable Display", 25, 650, dpi)
	fonts.Heading = createUIFont("Segoe UI Variable Display", 18, 650, dpi)
	fonts.Title = createUIFont("Segoe UI Variable Text", 13, 650, dpi)
	fonts.Body = createUIFont("Segoe UI Variable Text", 10.5, 400, dpi)
	fonts.BodyStrong = createUIFont("Segoe UI Variable Text", 10.5, 600, dpi)
	fonts.Small = createUIFont("Segoe UI Variable Text", 9, 400, dpi)
	fonts.SmallStrong = createUIFont("Segoe UI Variable Text", 9, 600, dpi)
	fonts.Mono = createUIFont("Cascadia Mono", 8.7, 400, dpi)
	fonts.Tiny = createUIFont("Segoe UI Variable Text", 8, 600, dpi)
	// Variable fonts are unavailable on some Windows 10 builds. GDI falls back,
	// but explicit Segoe UI fallbacks keep the metrics sane if creation failed.
	if fonts.Body == 0 {
		fonts.Body = createUIFont("Segoe UI", 10.5, 400, dpi)
	}
	if fonts.Display == 0 {
		fonts.Display = createUIFont("Segoe UI", 25, 650, dpi)
	}
}

type canvas struct {
	dc            uintptr
	app           *application
	width, height int
	scale         float64
}

func (c *canvas) s(v int) int { return int(math.Round(float64(v) * c.scale)) }

func (c *canvas) addHit(id string, x, y, w, h int) {
	c.app.hits = append(c.app.hits, hitRegion{ID: id, X: x, Y: y, W: w, H: h})
}

func createBrush(color uint32) uintptr {
	h, _, _ := procCreateSolidBrush.Call(uintptr(color))
	return h
}

func createPen(color uint32, width int) uintptr {
	h, _, _ := procCreatePen.Call(psSolid, uintptr(width), uintptr(color))
	return h
}

func (c *canvas) fillRect(x, y, w, h int, color uint32) {
	if w <= 0 || h <= 0 {
		return
	}
	r := rect{int32(x), int32(y), int32(x + w), int32(y + h)}
	brush := createBrush(color)
	procFillRect.Call(c.dc, uintptr(unsafe.Pointer(&r)), brush)
	procDeleteObject.Call(brush)
}

func (c *canvas) roundedRect(x, y, w, h, radius int, fill, border uint32) {
	if w <= 0 || h <= 0 {
		return
	}
	brush := createBrush(fill)
	pen := createPen(border, maxInt(1, c.s(1)))
	oldBrush, _, _ := procSelectObject.Call(c.dc, brush)
	oldPen, _, _ := procSelectObject.Call(c.dc, pen)
	procRoundRect.Call(c.dc, uintptr(x), uintptr(y), uintptr(x+w), uintptr(y+h), uintptr(radius), uintptr(radius))
	procSelectObject.Call(c.dc, oldBrush)
	procSelectObject.Call(c.dc, oldPen)
	procDeleteObject.Call(brush)
	procDeleteObject.Call(pen)
}

func (c *canvas) line(x1, y1, x2, y2, width int, color uint32) {
	pen := createPen(color, maxInt(1, width))
	old, _, _ := procSelectObject.Call(c.dc, pen)
	procMoveToEx.Call(c.dc, uintptr(x1), uintptr(y1), 0)
	procLineTo.Call(c.dc, uintptr(x2), uintptr(y2))
	procSelectObject.Call(c.dc, old)
	procDeleteObject.Call(pen)
}

func (c *canvas) circle(x, y, diameter int, fill, border uint32) {
	brush := createBrush(fill)
	pen := createPen(border, maxInt(1, c.s(1)))
	oldBrush, _, _ := procSelectObject.Call(c.dc, brush)
	oldPen, _, _ := procSelectObject.Call(c.dc, pen)
	procEllipse.Call(c.dc, uintptr(x), uintptr(y), uintptr(x+diameter), uintptr(y+diameter))
	procSelectObject.Call(c.dc, oldBrush)
	procSelectObject.Call(c.dc, oldPen)
	procDeleteObject.Call(brush)
	procDeleteObject.Call(pen)
}

func (c *canvas) text(value string, x, y, w, h int, font uintptr, color uint32, flags uint32) {
	if value == "" || w <= 0 || h <= 0 {
		return
	}
	u := syscall.StringToUTF16(value)
	if len(u) == 0 {
		return
	}
	old, _, _ := procSelectObject.Call(c.dc, font)
	procSetBkMode.Call(c.dc, transparent)
	procSetTextColor.Call(c.dc, uintptr(color))
	r := rect{int32(x), int32(y), int32(x + w), int32(y + h)}
	procDrawTextW.Call(c.dc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&r)), uintptr(flags|dtNoPrefix))
	procSelectObject.Call(c.dc, old)
}

func (c *canvas) textHeight(value string, width int, font uintptr) int {
	if value == "" || width <= 0 || font == 0 {
		return 0
	}
	u := syscall.StringToUTF16(value)
	if len(u) <= 1 {
		return 0
	}
	old, _, _ := procSelectObject.Call(c.dc, font)
	r := rect{0, 0, int32(width), 32767}
	procDrawTextW.Call(c.dc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&r)), uintptr(dtLeft|dtWordBreak|dtNoPrefix|dtCalcRect))
	procSelectObject.Call(c.dc, old)
	if r.Bottom <= r.Top {
		return 0
	}
	return int(r.Bottom - r.Top)
}

func (c *canvas) clippedWrappedText(value string, x, y, w, h int, font uintptr, color uint32, offset, contentH int) {
	if value == "" || w <= 0 || h <= 0 {
		return
	}
	saved, _, _ := procSaveDC.Call(c.dc)
	procIntersectClipRect.Call(c.dc, uintptr(x), uintptr(y), uintptr(x+w), uintptr(y+h))
	c.text(value, x, y-offset, w, maxInt(contentH, h)+offset, font, color, dtLeft|dtWordBreak)
	if saved != 0 {
		procRestoreDC.Call(c.dc, saved)
	}
}

func (c *canvas) textWidth(value string, font uintptr) int {
	if value == "" || font == 0 {
		return 0
	}
	u := syscall.StringToUTF16(value)
	if len(u) <= 1 {
		return 0
	}
	old, _, _ := procSelectObject.Call(c.dc, font)
	r := rect{0, 0, 32767, 4096}
	procDrawTextW.Call(c.dc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&r)), uintptr(dtSingleLine|dtNoPrefix|dtCalcRect))
	procSelectObject.Call(c.dc, old)
	if r.Right <= r.Left {
		return 0
	}
	return int(r.Right - r.Left)
}

func (c *canvas) label(value string, x, y, w int, color uint32) {
	c.text(strings.ToUpper(value), x, y, w, c.s(18), fonts.Tiny, color, dtSingleLine|dtVCenter|dtEndEllipsis)
}

func (c *canvas) button(id, label string, x, y, w, h int, primary bool) {
	c.buttonEnabled(id, label, x, y, w, h, primary, true)
}

func (c *canvas) buttonEnabled(id, label string, x, y, w, h int, primary, enabled bool) {
	hovered := enabled && c.app.hover == id
	if hovered && c.textWidth(label, fonts.BodyStrong) > maxInt(1, w-c.s(24)) {
		c.app.tooltipText = label
	}
	fill, border, textColor := palette.SurfaceRaised, palette.BorderBright, palette.Text
	if !enabled {
		fill, border, textColor = palette.Surface, palette.Border, palette.TextFaint
	} else if primary {
		fill, border = palette.Accent, palette.AccentBright
		if hovered {
			fill = palette.AccentBright
		}
	} else if hovered {
		fill, border = palette.SurfaceHover, palette.TextFaint
	}
	c.roundedRect(x, y, w, h, c.s(10), fill, border)
	c.text(label, x+c.s(12), y, w-c.s(24), h, fonts.BodyStrong, textColor, dtCenter|dtVCenter|dtSingleLine|dtEndEllipsis)
	if enabled {
		c.addHit(id, x, y, w, h)
	}
}

func (c *canvas) dangerButton(id, label string, x, y, w, h int, enabled bool) {
	hovered := enabled && c.app.hover == id
	if hovered && c.textWidth(label, fonts.BodyStrong) > maxInt(1, w-c.s(20)) {
		c.app.tooltipText = label
	}
	fill, border, textColor := palette.BadSoft, palette.Bad, palette.Bad
	if hovered {
		fill, textColor = palette.Bad, palette.White
	}
	if !enabled {
		fill, border, textColor = palette.Surface, palette.Border, palette.TextFaint
	}
	c.roundedRect(x, y, w, h, c.s(10), fill, border)
	c.text(label, x+c.s(10), y, w-c.s(20), h, fonts.BodyStrong, textColor, dtCenter|dtVCenter|dtSingleLine|dtEndEllipsis)
	if enabled {
		c.addHit(id, x, y, w, h)
	}
}

func (c *canvas) compactButton(id, label string, x, y, w, h int) {
	hovered := c.app.hover == id
	if hovered && c.textWidth(label, fonts.SmallStrong) > maxInt(1, w-c.s(16)) {
		c.app.tooltipText = label
	}
	fill := palette.SurfaceRaised
	border := palette.Border
	if hovered {
		fill, border = palette.SurfaceHover, palette.BorderBright
	}
	c.roundedRect(x, y, w, h, c.s(8), fill, border)
	c.text(label, x+c.s(8), y, w-c.s(16), h, fonts.SmallStrong, palette.Text, dtCenter|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.addHit(id, x, y, w, h)
}

func (c *canvas) toggle(id string, checked bool, x, y int) {
	w, h := c.s(42), c.s(23)
	fill, border := palette.SurfaceRaised, palette.BorderBright
	if checked {
		fill, border = palette.Accent, palette.AccentBright
	}
	if c.app.hover == id && !checked {
		fill = palette.SurfaceHover
	}
	c.roundedRect(x, y, w, h, h, fill, border)
	knob := h - c.s(6)
	kx := x + c.s(3)
	if checked {
		kx = x + w - knob - c.s(3)
	}
	c.circle(kx, y+c.s(3), knob, palette.Text, palette.Text)
	c.addHit(id, x, y, w, h)
}

func (c *canvas) statusToggle(id string, enabled bool, x, y, w, h int) {
	fill, fg := palette.SurfaceRaised, palette.TextFaint
	label := "DISABLED"
	if enabled {
		fill, fg = palette.GoodSoft, palette.Good
		label = "ENABLED"
	}
	if c.app.hover == id {
		if enabled {
			fill = rgb(31, 76, 60)
		} else {
			fill = palette.SurfaceHover
		}
	}
	c.roundedRect(x, y, w, h, h/2, fill, fill)
	toggleW, toggleH := c.s(38), c.s(21)
	tx := x + w - toggleW - c.s(6)
	ty := y + (h-toggleH)/2
	trackFill, trackBorder := palette.Background, palette.BorderBright
	if enabled {
		trackFill, trackBorder = palette.Good, palette.Good
	}
	c.roundedRect(tx, ty, toggleW, toggleH, toggleH, trackFill, trackBorder)
	knob := toggleH - c.s(6)
	kx := tx + c.s(3)
	if enabled {
		kx = tx + toggleW - knob - c.s(3)
	}
	c.circle(kx, ty+c.s(3), knob, palette.Text, palette.Text)
	c.text(label, x+c.s(9), y, maxInt(c.s(45), w-toggleW-c.s(18)), h, fonts.Tiny, fg, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.addHit(id, x, y, w, h)
}

func (c *canvas) invalidStatusControl(id string, x, y, w, h int) {
	fill, fg, border := palette.BadSoft, palette.Bad, palette.Bad
	if c.app.hover == id {
		fill, fg = palette.Bad, palette.White
	}
	c.roundedRect(x, y, w, h, h/2, fill, border)
	c.text("INVALID", x+c.s(10), y, w-c.s(20), h, fonts.Tiny, fg, dtCenter|dtVCenter|dtSingleLine)
	c.addHit(id, x, y, w, h)
}

func statusColors(class int, status string) (uint32, uint32) {
	if status == "DISABLED" {
		return palette.SurfaceRaised, palette.TextFaint
	}
	switch class {
	case 0:
		return palette.GoodSoft, palette.Good
	case 1:
		return palette.WarnSoft, palette.Warn
	case 2:
		return palette.BadSoft, palette.Bad
	default:
		return palette.SurfaceRaised, palette.TextMuted
	}
}

func (c *canvas) pill(label string, class int, x, y, w, h int) {
	fill, fg := statusColors(class, label)
	c.roundedRect(x, y, w, h, h, fill, fill)
	c.text(label, x+c.s(8), y, w-c.s(16), h, fonts.Tiny, fg, dtCenter|dtVCenter|dtSingleLine|dtEndEllipsis)
}

func (c *canvas) card(x, y, w, h int) {
	c.roundedRect(x, y, w, h, c.s(14), palette.Surface, palette.Border)
}

func (c *canvas) sectionTitle(kicker, title, subtitle string, x, y, w int) int {
	c.label(kicker, x, y, w, palette.AccentBright)
	c.text(title, x, y+c.s(18), w, c.s(44), fonts.Display, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	if subtitle != "" {
		c.text(subtitle, x, y+c.s(63), w, c.s(36), fonts.Body, palette.TextMuted, dtLeft|dtWordBreak)
		return y + c.s(105)
	}
	return y + c.s(70)
}

func (c *canvas) metricCard(label, value, detail string, x, y, w, h int, class int) {
	c.card(x, y, w, h)
	fill, fg := statusColors(class, "")
	// Three explicit vertical bands keep compact Compatibility cards aligned.
	labelY := y + c.s(12)
	valueY := y + c.s(31)
	detailY := y + h - c.s(24)
	c.circle(x+c.s(18), labelY+c.s(3), c.s(12), fg, fg)
	c.label(label, x+c.s(40), labelY, w-c.s(58), palette.TextMuted)
	valueH := maxInt(c.s(24), detailY-valueY-c.s(2))
	c.text(value, x+c.s(18), valueY, w-c.s(36), valueH, fonts.Heading, palette.Text, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	c.text(detail, x+c.s(18), detailY, w-c.s(36), c.s(17), fonts.Tiny, palette.TextMuted, dtLeft|dtVCenter|dtSingleLine|dtEndEllipsis)
	_ = fill
}

func (c *canvas) divider(x, y, w int) { c.line(x, y, x+w, y, c.s(1), palette.Border) }

func (c *canvas) drawBrand(x, y int) {
	d := c.s(44)
	if c.app != nil && c.app.brandImage != 0 && drawGDIPlusImage(c.dc, c.app.brandImage, x, y, d, d) {
		return
	}
	// Fallback only if the packaged transparent brand asset could not be loaded.
	c.circle(x-c.s(4), y-c.s(4), d+c.s(8), palette.AccentSoft, palette.AccentSoft)
	c.circle(x, y, d, palette.Accent, palette.AccentBright)
	c.text("R", x, y, d, d, fonts.Heading, palette.White, dtCenter|dtVCenter|dtSingleLine)
}

func (c *canvas) busyIndicator(x, y int) {
	d := c.s(10)
	phase := math.Mod(c.app.animation, 1)
	for i := 0; i < 3; i++ {
		strength := math.Mod(phase+float64(i)*0.33, 1)
		col := palette.TextFaint
		if strength > 0.55 {
			col = palette.AccentBright
		} else if strength > 0.25 {
			col = palette.Accent
		}
		c.circle(x+i*c.s(16), y, d, col, col)
	}
}

func safeLine(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " ")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// squareIconButton keeps the playlist controls compact and language-neutral.
// The action can be disabled while retaining its hover label (for example,
// Rename/Delete on the Default playlist).
func (c *canvas) squareIconButton(id, kind string, x, y, size int, danger, enabled bool, tooltip string) {
	hitID := id
	if !enabled {
		hitID = "tooltip-only:" + id
	}
	pointerOver := c.app.hover == hitID
	if pointerOver && tooltip != "" {
		c.app.tooltipText = tooltip
	}
	hovered := enabled && pointerOver
	fill, border, ink := palette.SurfaceRaised, palette.Border, palette.TextMuted
	if hovered {
		fill, border = palette.SurfaceHover, palette.BorderBright
	}
	if danger {
		ink = palette.Bad
		if hovered {
			fill, border = palette.BadSoft, palette.Bad
		}
	}
	if !enabled {
		ink = palette.TextFaint
		fill, border = palette.Surface, palette.Border
	}
	c.roundedRect(x, y, size, size, c.s(7), fill, border)
	cx, cy := x+size/2, y+size/2
	switch kind {
	case "save":
		w, h := c.s(16), c.s(17)
		left, top := cx-w/2, cy-h/2
		c.line(left, top, left+w, top, c.s(2), ink)
		c.line(left, top, left, top+h, c.s(2), ink)
		c.line(left+w, top, left+w, top+h, c.s(2), ink)
		c.line(left, top+h, left+w, top+h, c.s(2), ink)
		c.line(left+c.s(4), top, left+c.s(4), top+c.s(6), c.s(2), ink)
		c.line(left+c.s(4), top+c.s(6), left+w-c.s(3), top+c.s(6), c.s(2), ink)
		c.line(left+c.s(4), top+c.s(11), left+w-c.s(4), top+c.s(11), c.s(2), ink)
	case "pencil":
		c.line(cx-c.s(6), cy+c.s(6), cx+c.s(6), cy-c.s(6), c.s(3), ink)
		c.line(cx-c.s(8), cy+c.s(8), cx-c.s(4), cy+c.s(7), c.s(2), ink)
		c.line(cx+c.s(5), cy-c.s(7), cx+c.s(8), cy-c.s(4), c.s(2), ink)
	case "export":
		c.line(cx-c.s(7), cy+c.s(6), cx+c.s(7), cy+c.s(6), c.s(2), ink)
		c.line(cx-c.s(7), cy+c.s(6), cx-c.s(7), cy-c.s(1), c.s(2), ink)
		c.line(cx+c.s(7), cy+c.s(6), cx+c.s(7), cy-c.s(1), c.s(2), ink)
		c.line(cx, cy+c.s(2), cx, cy-c.s(8), c.s(2), ink)
		c.line(cx, cy-c.s(8), cx-c.s(4), cy-c.s(4), c.s(2), ink)
		c.line(cx, cy-c.s(8), cx+c.s(4), cy-c.s(4), c.s(2), ink)
	case "import":
		c.line(cx-c.s(7), cy+c.s(6), cx+c.s(7), cy+c.s(6), c.s(2), ink)
		c.line(cx-c.s(7), cy+c.s(6), cx-c.s(7), cy-c.s(1), c.s(2), ink)
		c.line(cx+c.s(7), cy+c.s(6), cx+c.s(7), cy-c.s(1), c.s(2), ink)
		c.line(cx, cy-c.s(7), cx, cy+c.s(3), c.s(2), ink)
		c.line(cx, cy+c.s(3), cx-c.s(4), cy-c.s(1), c.s(2), ink)
		c.line(cx, cy+c.s(3), cx+c.s(4), cy-c.s(1), c.s(2), ink)
	default:
		w := c.s(15)
		left := cx - w/2
		top := cy - c.s(8)
		c.line(left, top+c.s(3), left+w, top+c.s(3), c.s(2), ink)
		c.line(left+c.s(4), top, left+w-c.s(4), top, c.s(2), ink)
		c.line(left+c.s(2), top+c.s(6), left+c.s(4), top+c.s(16), c.s(2), ink)
		c.line(left+w-c.s(2), top+c.s(6), left+w-c.s(4), top+c.s(16), c.s(2), ink)
		c.line(left+c.s(4), top+c.s(16), left+w-c.s(4), top+c.s(16), c.s(2), ink)
	}
	c.addHit(hitID, x, y, size, size)
}
