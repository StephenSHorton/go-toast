//go:build windows

package toast

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The few Win32 calls the cards need, bound directly so the module depends on
// nothing but golang.org/x/sys. Values and layouts are the Windows SDK's.

type (
	hwnd    = windows.HWND
	hdc     uintptr
	hgdiobj uintptr
	hfont   = hgdiobj
	hmon    uintptr
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")
	winmm    = windows.NewLazySystemDLL("winmm.dll")

	procRegisterClassEx    = user32.NewProc("RegisterClassExW")
	procCreateWindowEx     = user32.NewProc("CreateWindowExW")
	procDestroyWindow      = user32.NewProc("DestroyWindow")
	procDefWindowProc      = user32.NewProc("DefWindowProcW")
	procGetMessage         = user32.NewProc("GetMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessage    = user32.NewProc("DispatchMessageW")
	procPostMessage        = user32.NewProc("PostMessageW")
	procSetWindowPos       = user32.NewProc("SetWindowPos")
	procShowWindow         = user32.NewProc("ShowWindow")
	procSetLayeredAttrs    = user32.NewProc("SetLayeredWindowAttributes")
	procSetTimer           = user32.NewProc("SetTimer")
	procKillTimer          = user32.NewProc("KillTimer")
	procGetWindowLongPtr   = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtr   = user32.NewProc("SetWindowLongPtrW")
	procInvalidateRect     = user32.NewProc("InvalidateRect")
	procSetCursor          = user32.NewProc("SetCursor")
	procLoadCursor         = user32.NewProc("LoadCursorW")
	procTrackMouseEvent    = user32.NewProc("TrackMouseEvent")
	procMonitorFromWindow  = user32.NewProc("MonitorFromWindow")
	procMonitorFromRect    = user32.NewProc("MonitorFromRect")
	procGetMonitorInfo     = user32.NewProc("GetMonitorInfoW")
	procGetDC              = user32.NewProc("GetDC")
	procReleaseDC          = user32.NewProc("ReleaseDC")
	procDrawText           = user32.NewProc("DrawTextW")
	procFillRect           = user32.NewProc("FillRect")
	procFrameRect          = user32.NewProc("FrameRect")
	procBeginPaint         = user32.NewProc("BeginPaint")
	procEndPaint           = user32.NewProc("EndPaint")
	procGetClientRect      = user32.NewProc("GetClientRect")
	procSetWindowRgn       = user32.NewProc("SetWindowRgn")
	procAffinity           = user32.NewProc("SetWindowDisplayAffinity")
	procExtraInfo          = user32.NewProc("GetMessageExtraInfo")
	procGetModuleHandle    = kernel32.NewProc("GetModuleHandleW")
	procCreateFontIndirect = gdi32.NewProc("CreateFontIndirectW")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procCreateSolidBrush   = gdi32.NewProc("CreateSolidBrush")
	procCreatePen          = gdi32.NewProc("CreatePen")
	procGetStockObject     = gdi32.NewProc("GetStockObject")
	procRoundRect          = gdi32.NewProc("RoundRect")
	procCreateRoundRectRgn = gdi32.NewProc("CreateRoundRectRgn")
	procSetBkMode          = gdi32.NewProc("SetBkMode")
	procSetTextColor       = gdi32.NewProc("SetTextColor")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procCompatBmp          = gdi32.NewProc("CreateCompatibleBitmap")
	procBitBlt             = gdi32.NewProc("BitBlt")
	procDpiForMon          = shcore.NewProc("GetDpiForMonitor")
	procPlaySound          = winmm.NewProc("PlaySoundW")
)

const (
	wmPaint          = 0x000F
	wmEraseBkgnd     = 0x0014
	wmSettingChange  = 0x001A
	wmSetCursor      = 0x0020
	wmMouseActivate  = 0x0021
	wmDisplayChange  = 0x007E
	wmTimer          = 0x0113
	wmMouseMove      = 0x0200
	wmLButtonDown    = 0x0201
	wmLButtonUp      = 0x0202
	wmRButtonUp      = 0x0205
	wmMouseLeave     = 0x02A3
	wmDpiChanged     = 0x02E0
	wmApp            = 0x8000
	spiSetWorkArea   = 0x002F
	wsPopup          = 0x80000000
	wsExTopmost      = 0x00000008
	wsExTransparent  = 0x00000020
	wsExToolWindow   = 0x00000080
	wsExLayered      = 0x00080000
	wsExNoActivate   = 0x08000000
	gwlExStyle       = -20
	swpNoSize        = 0x0001
	swpNoMove        = 0x0002
	swpNoZOrder      = 0x0004
	swpNoActivate    = 0x0010
	swpFrameChanged  = 0x0020
	swShowNoActivate = 4
	lwaAlpha         = 0x00000002
	tmeLeave         = 0x00000002
	idcArrow         = 32512
	idcHand          = 32649
	monDefaultToNull = 0
	monDefaultToNear = 2
	dtCenter         = 0x00000001
	dtVCenter        = 0x00000004
	dtWordBreak      = 0x00000010
	dtSingleLine     = 0x00000020
	dtNoClip         = 0x00000100
	dtCalcRect       = 0x00000400
	dtNoPrefix       = 0x00000800
	dtEditControl    = 0x00002000
	dtEndEllipsis    = 0x00008000
	srcCopy          = 0x00CC0020
	bkTransparent    = 1
	psSolid          = 0
	nullBrush        = 5
)

// hwndTopmost and hwndMessage are HWND_TOPMOST (-1) and HWND_MESSAGE (-3).
var (
	hwndTopmost = ^hwnd(0)
	hwndMessage = ^hwnd(2)
)

type winRect struct{ Left, Top, Right, Bottom int32 }

type winPoint struct{ X, Y int32 }

type wndClassEx struct {
	Size       uint32
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

type winMsg struct {
	Hwnd    hwnd
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      winPoint
}

type monitorInfo struct {
	CbSize    uint32
	RcMonitor winRect
	RcWork    winRect
	DwFlags   uint32
}

type logFont struct {
	Height         int32
	Width          int32
	Escapement     int32
	Orientation    int32
	Weight         int32
	Italic         byte
	Underline      byte
	StrikeOut      byte
	CharSet        byte
	OutPrecision   byte
	ClipPrecision  byte
	Quality        byte
	PitchAndFamily byte
	FaceName       [32]uint16
}

type trackMouseEvent struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   hwnd
	DwHoverTime uint32
}

type paintStruct struct {
	Hdc         hdc
	FErase      int32
	RcPaint     winRect
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

func utf16(s string) *uint16 { return windows.StringToUTF16Ptr(s) }

func moduleHandle() uintptr {
	r, _, _ := procGetModuleHandle.Call(0)
	return r
}

func registerClass(wc *wndClassEx) {
	_, _, _ = procRegisterClassEx.Call(uintptr(unsafe.Pointer(wc)))
}

func createWindowEx(exStyle uint32, class, title string, style uint32, x, y, w, h int, parent hwnd, inst uintptr) hwnd {
	r, _, _ := procCreateWindowEx.Call(uintptr(exStyle), uintptr(unsafe.Pointer(utf16(class))),
		uintptr(unsafe.Pointer(utf16(title))), uintptr(style), uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(parent), 0, inst, 0)
	return hwnd(r)
}

func destroyWindow(h hwnd) { _, _, _ = procDestroyWindow.Call(uintptr(h)) }

func defWindowProc(h hwnd, msg uint32, wParam, lParam uintptr) uintptr {
	r, _, _ := procDefWindowProc.Call(uintptr(h), uintptr(msg), wParam, lParam)
	return r
}

func getMessage(m *winMsg) int32 {
	r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(m)), 0, 0, 0)
	return int32(r)
}

func translateMessage(m *winMsg) { _, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(m))) }

func dispatchMessage(m *winMsg) { _, _, _ = procDispatchMessage.Call(uintptr(unsafe.Pointer(m))) }

func postMessage(h hwnd, msg uint32, wParam, lParam uintptr) {
	_, _, _ = procPostMessage.Call(uintptr(h), uintptr(msg), wParam, lParam)
}

func setWindowPos(h, after hwnd, x, y, w, ht int, flags uint32) {
	_, _, _ = procSetWindowPos.Call(uintptr(h), uintptr(after), uintptr(x), uintptr(y), uintptr(w), uintptr(ht), uintptr(flags))
}

func showWindow(h hwnd, cmd int) { _, _, _ = procShowWindow.Call(uintptr(h), uintptr(cmd)) }

func setLayeredAlpha(h hwnd, alpha byte) {
	_, _, _ = procSetLayeredAttrs.Call(uintptr(h), 0, uintptr(alpha), lwaAlpha)
}

func setTimer(h hwnd, id, ms uintptr) bool {
	r, _, _ := procSetTimer.Call(uintptr(h), id, ms, 0)
	return r != 0
}

func killTimer(h hwnd, id uintptr) { _, _, _ = procKillTimer.Call(uintptr(h), id) }

func getExStyle(h hwnd) uintptr {
	idx := int32(gwlExStyle)
	r, _, _ := procGetWindowLongPtr.Call(uintptr(h), uintptr(idx))
	return r
}

func setExStyle(h hwnd, v uintptr) {
	idx := int32(gwlExStyle)
	_, _, _ = procSetWindowLongPtr.Call(uintptr(h), uintptr(idx), v)
}

func invalidate(h hwnd) { _, _, _ = procInvalidateRect.Call(uintptr(h), 0, 0) }

func setCursor(id uintptr) {
	c, _, _ := procLoadCursor.Call(0, id)
	_, _, _ = procSetCursor.Call(c)
}

func trackLeave(h hwnd) bool {
	tme := trackMouseEvent{DwFlags: tmeLeave, HwndTrack: h}
	tme.CbSize = uint32(unsafe.Sizeof(tme))
	r, _, _ := procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
	return r != 0
}

func monitorFromWindow(h hwnd, flags uint32) hmon {
	r, _, _ := procMonitorFromWindow.Call(uintptr(h), uintptr(flags))
	return hmon(r)
}

func monitorFromRect(r *winRect, flags uint32) hmon {
	m, _, _ := procMonitorFromRect.Call(uintptr(unsafe.Pointer(r)), uintptr(flags))
	return hmon(m)
}

func getMonitorInfo(m hmon, mi *monitorInfo) {
	_, _, _ = procGetMonitorInfo.Call(uintptr(m), uintptr(unsafe.Pointer(mi)))
}

func getDC(h hwnd) hdc {
	r, _, _ := procGetDC.Call(uintptr(h))
	return hdc(r)
}

func releaseDC(h hwnd, dc hdc) { _, _, _ = procReleaseDC.Call(uintptr(h), uintptr(dc)) }

func drawText(dc hdc, s string, r *winRect, format uint32) {
	u := windows.StringToUTF16(s)
	_, _, _ = procDrawText.Call(uintptr(dc), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1),
		uintptr(unsafe.Pointer(r)), uintptr(format))
}

func fillRect(dc hdc, r *winRect, brush hgdiobj) {
	_, _, _ = procFillRect.Call(uintptr(dc), uintptr(unsafe.Pointer(r)), uintptr(brush))
}

func frameRect(dc hdc, r *winRect, brush hgdiobj) {
	_, _, _ = procFrameRect.Call(uintptr(dc), uintptr(unsafe.Pointer(r)), uintptr(brush))
}

func beginPaint(h hwnd, ps *paintStruct) hdc {
	r, _, _ := procBeginPaint.Call(uintptr(h), uintptr(unsafe.Pointer(ps)))
	return hdc(r)
}

func endPaint(h hwnd, ps *paintStruct) {
	_, _, _ = procEndPaint.Call(uintptr(h), uintptr(unsafe.Pointer(ps)))
}

func getClientRect(h hwnd) winRect {
	var r winRect
	_, _, _ = procGetClientRect.Call(uintptr(h), uintptr(unsafe.Pointer(&r)))
	return r
}

// setRoundRegion clips h to a rounded rectangle w x ht px with corner radius
// r px, or removes the clip when r is 0. The window owns the region after.
func setRoundRegion(h hwnd, w, ht, r int) {
	var rgn uintptr
	if r > 0 {
		rgn, _, _ = procCreateRoundRectRgn.Call(0, 0, uintptr(w+1), uintptr(ht+1), uintptr(2*r), uintptr(2*r))
	}
	_, _, _ = procSetWindowRgn.Call(uintptr(h), rgn, 1)
}

func createFont(lf *logFont) hfont {
	r, _, _ := procCreateFontIndirect.Call(uintptr(unsafe.Pointer(lf)))
	return hfont(r)
}

func selectObject(dc hdc, obj hgdiobj) hgdiobj {
	r, _, _ := procSelectObject.Call(uintptr(dc), uintptr(obj))
	return hgdiobj(r)
}

func deleteObject(obj hgdiobj) { _, _, _ = procDeleteObject.Call(uintptr(obj)) }

func solidBrush(c uint32) hgdiobj {
	r, _, _ := procCreateSolidBrush.Call(uintptr(c))
	return hgdiobj(r)
}

func solidPen(c uint32) hgdiobj {
	r, _, _ := procCreatePen.Call(psSolid, 1, uintptr(c))
	return hgdiobj(r)
}

func stockObject(i int) hgdiobj {
	r, _, _ := procGetStockObject.Call(uintptr(i))
	return hgdiobj(r)
}

func roundRect(dc hdc, r winRect, radius int) {
	_, _, _ = procRoundRect.Call(uintptr(dc), uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom),
		uintptr(2*radius), uintptr(2*radius))
}

func setBkMode(dc hdc, mode int) { _, _, _ = procSetBkMode.Call(uintptr(dc), uintptr(mode)) }

func setTextColor(dc hdc, c uint32) { _, _, _ = procSetTextColor.Call(uintptr(dc), uintptr(c)) }

func createCompatibleDC(dc hdc) hdc {
	r, _, _ := procCreateCompatibleDC.Call(uintptr(dc))
	return hdc(r)
}

func deleteDC(dc hdc) { _, _, _ = procDeleteDC.Call(uintptr(dc)) }
