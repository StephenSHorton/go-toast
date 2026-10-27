//go:build windows

package toast

import (
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Each card is a tiny native window, not a webview: it must NEVER take focus
// from a game. Cards are WS_EX_NOACTIVATE, answer WM_MOUSEACTIVATE with
// MA_NOACTIVATE (so clicking one doesn't activate it either), are shown with
// SW_SHOWNOACTIVATE and put back on top of the topmost band (HWND_TOPMOST)
// whenever a note is posted. They appear over borderless/windowed games;
// exclusive-fullscreen games bypass the desktop compositor so nothing can draw
// over them, which is what the sound is for.
//
// All the windows live on one locked OS thread, started by the first Post. Other
// goroutines only change the model under mu and PostMessage the thread's
// message-only window. The thread never runs a note's callbacks itself: each
// gets its own goroutine, after the card is out of the model and mu is free,
// so a callback that posts, closes or blocks can't wedge the windows (Suzuri's
// notice stack deadlocked exactly that way).
//
// With Config.CaptureExclude the cards are excluded from screen capture
// (WDA_EXCLUDEFROMCAPTURE), so a screenshot taken while "Copied to clipboard"
// is still up, or a recording made under a card, doesn't picture the app's own
// notification. Input Config.IgnoreInput claims (an app's own injected clicks)
// never presses a card, and while HoldClickThrough is held the cards are
// click-through.

const (
	wmSync       = wmApp + 1
	wmRehome     = wmApp + 2 // a display or work-area change: re-place the stack
	timerTick    = 1
	tickMs       = 30 // expiry and fades; runs only while cards are up
	maNoActivate = 3
	// wdaExcludeFromCapture hides a window from BitBlt, DXGI duplication and
	// Windows.Graphics.Capture while it stays on screen (Windows 10 2004+;
	// older builds refuse it and the card is captured as before).
	wdaExcludeFromCapture = 0x11

	iconFace = "Segoe MDL2 Assets" // on every Windows 10/11
	glyphX   = ""                 // ChromeClose
)

func iconFor(k Kind) string {
	switch k {
	case Success:
		return "" // CheckMark
	case Warn:
		return "" // Warning
	case Error:
		return "" // Error
	default:
		return "" // Info
	}
}

// Shared state, guarded by mu.
var (
	mu      sync.Mutex
	model   stack
	host    hwnd // the thread's message-only window; 0 until it starts
	work    rect // work area (px) of the monitor the stack is on
	scale   = 1.0
	raise   bool   // re-assert topmost on the next sync
	through int    // HoldClickThrough holds; cards are click-through while > 0
	hwnds   []hwnd // card windows, for tests
	started sync.Once
	shown   atomic.Int32 // card windows on screen
	// runCfg is the Config the window thread started with (class names, title).
	runCfg Config
)

// Toast-thread state: only the window thread touches these.
var (
	wins     = map[uint64]*cardWin{}
	byHwnd   = map[hwnd]*cardWin{}
	hoverKey uint64 // card under the cursor, 0 = none
	ticking  bool
)

// cardWin is a card's window and its copy of the card's content.
type cardWin struct {
	hwnd         hwnd
	key, rev     uint64
	scale        float64
	cfg          Config // the setup when the card was created
	title, body  string
	kind         Kind
	labels       []string
	clickable    bool // has OnClick: the body gets a hand cursor
	lines        int  // body lines
	w, h         int
	radius       int // corner radius, px
	rgnW, rgnH   int // size the window region was made for
	close        rect
	buttons      []rect
	x, y, pw, ph int // last placement
	placed       bool
	alpha        byte
	hot, pressed part
	tracking     bool
	through      bool // WS_EX_TRANSPARENT is set
}

// Post shows n in the notification stack (or replaces the card with its ID)
// and plays its sound. A no-op when notifications are off. Never blocks.
func Post(n Note) {
	o := CurrentOptions()
	n, ok := clean(n)
	if !o.Enabled || !ok {
		return
	}
	mu.Lock()
	if len(model.cards) == 0 { // a new stack goes where the user is looking
		work, scale = activeWorkArea()
	} else { // a monitor unplugged or resized under a sticky card moves the stack
		work, scale = rehome(work)
	}
	model.post(n, time.Now())
	raise = true
	h := host
	mu.Unlock()
	if !n.Silent && o.Sound {
		play(soundFor(n.Kind, len(n.Actions) > 0), o.Volume)
	}
	wake(h)
}

// Close removes the card with id, if it is up, without running its callbacks.
func Close(id string) {
	mu.Lock()
	closed := model.closeID(id)
	h := host
	mu.Unlock()
	if closed {
		wake(h)
	}
}

// Show posts a plain card: ok picks Success or Error.
func Show(title, body string, ok bool) {
	k := Success
	if !ok {
		k = Error
	}
	Post(Note{Title: title, Body: body, Kind: k})
}

// HoldClickThrough makes the cards click-through until the returned release
// is called, so clicks land on the window under them. An auto clicker holds
// it for a run: its injected clicks then reach the app it is clicking instead
// of a card. Holds nest (a stopped run can outlive the next one's start).
func HoldClickThrough() (release func()) {
	mu.Lock()
	through++
	h := host
	mu.Unlock()
	if h != 0 {
		postMessage(h, wmSync, 0, 0)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			through--
			h := host
			mu.Unlock()
			if h != 0 {
				postMessage(h, wmSync, 0, 0)
			}
		})
	}
}

// Visible reports whether any card is on screen (tests).
func Visible() bool { return shown.Load() > 0 }

func optionsChanged(o Options) {
	mu.Lock()
	if !o.Enabled {
		model.cards = nil
	}
	h := host
	mu.Unlock()
	if h != 0 {
		postMessage(h, wmSync, 0, 0) // a corner change moves the stack
	}
}

// wake asks the window thread to sync, starting it the first time. A post that
// races the start is picked up by the thread's first sync, which runs after it
// publishes host under mu.
func wake(h hwnd) {
	if h != 0 {
		postMessage(h, wmSync, 0, 0)
		return
	}
	started.Do(func() { go run() })
}

func playWAV(b []byte) {
	const sndAsync, sndNoDefault, sndMemory = 0x0001, 0x0002, 0x0004
	_, _, _ = procPlaySound.Call(uintptr(unsafe.Pointer(&b[0])), 0, sndAsync|sndNoDefault|sndMemory)
}

// run is the window thread. It lives as long as the process.
func run() {
	runtime.LockOSThread()
	c := CurrentConfig()
	inst := moduleHandle()
	for class, proc := range map[string]uintptr{
		c.hostClassName(): syscall.NewCallback(hostProc),
		c.cardClassName(): syscall.NewCallback(cardProc),
	} {
		wc := wndClassEx{WndProc: proc, Instance: inst, ClassName: utf16(class)}
		wc.Size = uint32(unsafe.Sizeof(wc))
		registerClass(&wc)
	}
	h := createWindowEx(0, c.hostClassName(), c.AppName, 0, 0, 0, 0, 0, hwndMessage, inst)
	if h == 0 {
		log.Printf("%scan't create the notification window", c.logPrefix())
		return
	}
	mu.Lock()
	host, runCfg = h, c
	mu.Unlock()
	syncCards()

	var m winMsg
	for getMessage(&m) > 0 {
		translateMessage(&m)
		dispatchMessage(&m)
	}
}

func hostProc(h hwnd, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSync:
		syncCards()
		return 0
	case wmRehome:
		mu.Lock()
		if len(model.cards) > 0 {
			work, scale = rehome(work)
			raise = true
		}
		mu.Unlock()
		syncCards()
		return 0
	case wmTimer:
		mu.Lock()
		model.tick(time.Now(), hoverKey != 0)
		mu.Unlock()
		syncCards()
		return 0
	}
	return defWindowProc(h, msg, wParam, lParam)
}

// syncCards makes the windows match the model: creates, re-measures, places,
// fades and destroys them, and runs the tick timer while any card is fading
// or counting down (a stack of only sticky cards needs no timer).
func syncCards() {
	type view struct {
		key, rev  uint64
		title     string
		body      string
		kind      Kind
		labels    []string
		clickable bool
		alpha     byte
	}
	now := time.Now()
	corner := CurrentOptions().Corner
	timed := false
	mu.Lock()
	views := make([]view, len(model.cards))
	for i, c := range model.cards {
		v := view{key: c.key, rev: c.rev, title: c.Title, body: c.Body, kind: c.Kind,
			clickable: c.OnClick != nil, alpha: c.alpha(now)}
		for _, a := range c.Actions {
			v.labels = append(v.labels, a.Label)
		}
		views[i] = v
		if !c.until.IsZero() || !c.closing.IsZero() || v.alpha < fullAlpha {
			timed = true
		}
	}
	wa, sc, up, pass := work, scale, raise, through > 0
	raise = false
	mu.Unlock()

	live := make(map[uint64]bool, len(views))
	for _, v := range views {
		live[v.key] = true
	}
	for k, cw := range wins {
		if !live[k] {
			destroyCard(cw)
		}
	}

	var cards []*cardWin
	var heights []int
	var alphas []byte
	for _, v := range views {
		cw := wins[v.key]
		fresh := cw == nil
		if fresh {
			cw = &cardWin{key: v.key, hot: partNone, pressed: partNone, cfg: CurrentConfig()}
		}
		if fresh || cw.rev != v.rev || cw.scale != sc {
			cw.rev, cw.scale = v.rev, sc
			cw.title, cw.body, cw.kind, cw.labels, cw.clickable = v.title, v.body, v.kind, v.labels, v.clickable
			measure(cw)
			if !fresh {
				invalidate(cw.hwnd)
			}
		}
		if fresh && !createCard(cw) {
			continue
		}
		if cw.radius > 0 && (cw.rgnW != cw.w || cw.rgnH != cw.h) {
			setRoundRegion(cw.hwnd, cw.w, cw.h, cw.radius)
			cw.rgnW, cw.rgnH = cw.w, cw.h
		}
		if cw.through != pass {
			setClickThrough(cw, pass)
		}
		cards, heights, alphas = append(cards, cw), append(heights, cw.h), append(alphas, v.alpha)
	}

	w := px(cardWidthDip, sc)
	origins := stackOrigins(wa, corner, w, heights, px(cardGapDip, sc), px(cardMarginDip, sc))
	for i, cw := range cards {
		p := origins[i]
		moved := !cw.placed || p.X != cw.x || p.Y != cw.y || cw.w != cw.pw || cw.h != cw.ph
		if moved || up {
			after, flags := hwndTopmost, uint32(swpNoActivate)
			if cw.placed && !up {
				after, flags = 0, flags|swpNoZOrder
			}
			setWindowPos(cw.hwnd, after, p.X, p.Y, cw.w, cw.h, flags)
			cw.x, cw.y, cw.pw, cw.ph = p.X, p.Y, cw.w, cw.h
		}
		if alphas[i] != cw.alpha {
			cw.alpha = alphas[i]
			setLayeredAlpha(cw.hwnd, cw.alpha)
		}
		if !cw.placed {
			cw.placed = true
			showWindow(cw.hwnd, swShowNoActivate)
		}
	}

	list := make([]hwnd, len(cards))
	for i, cw := range cards {
		list[i] = cw.hwnd
	}
	mu.Lock()
	hwnds = list
	h := host
	mu.Unlock()
	switch {
	case timed && !ticking:
		ticking = setTimer(h, timerTick, tickMs)
	case !timed && ticking:
		killTimer(h, timerTick)
		ticking = false
	}
}

func createCard(cw *cardWin) bool {
	mu.Lock()
	rc := runCfg
	mu.Unlock()
	h := createWindowEx(
		wsExTopmost|wsExToolWindow|wsExNoActivate|wsExLayered,
		rc.cardClassName(), rc.AppName,
		wsPopup, 0, 0, cw.w, cw.h, 0, moduleHandle())
	if h == 0 {
		return false
	}
	setLayeredAlpha(h, 0) // fades in from nothing
	if cw.cfg.CaptureExclude {
		_, _, _ = procAffinity.Call(uintptr(h), wdaExcludeFromCapture)
	}
	cw.hwnd = h
	wins[cw.key], byHwnd[h] = cw, cw
	shown.Add(1)
	return true
}

// setClickThrough adds or removes WS_EX_TRANSPARENT on cw. A card going
// click-through gets no WM_MOUSELEAVE, so its hover is dropped here.
func setClickThrough(cw *cardWin, on bool) {
	ex := getExStyle(cw.hwnd)
	if on {
		ex |= wsExTransparent
		if hoverKey == cw.key {
			hoverKey = 0
		}
		cw.tracking, cw.pressed = false, partNone
		setHot(cw, partNone)
	} else {
		ex &^= wsExTransparent
	}
	setExStyle(cw.hwnd, ex)
	setWindowPos(cw.hwnd, 0, 0, 0, 0, 0, swpFrameChanged|swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate)
	cw.through = on
}

func destroyCard(cw *cardWin) {
	delete(wins, cw.key)
	delete(byHwnd, cw.hwnd)
	if hoverKey == cw.key { // no WM_MOUSELEAVE comes for a destroyed window
		hoverKey = 0
	}
	destroyWindow(cw.hwnd)
	shown.Add(-1)
}

// click handles a left click on p (or a right click, as partClose): the card
// leaves the model first, then its callback gets its own goroutine. The
// windows catch up on the next sync, posted rather than run here so the
// clicked window isn't destroyed inside its own message.
func click(key uint64, p part) {
	mu.Lock()
	c := model.take(key)
	h := host
	mu.Unlock()
	if c == nil {
		return
	}
	switch {
	case p >= 0 && int(p) < len(c.Actions):
		goRun(c.Actions[p].Run)
	case p == partBody && c.OnClick != nil:
		goRun(c.OnClick)
	default:
		goRun(c.OnDismiss)
	}
	postMessage(h, wmSync, 0, 0)
}

func goRun(fn func()) {
	if fn == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("%snotification callback panicked: %v", CurrentConfig().logPrefix(), r)
			}
		}()
		fn()
	}()
}

func cardProc(h hwnd, msg uint32, wParam, lParam uintptr) uintptr {
	// Top-level cards get the display broadcasts the message-only host
	// doesn't. Each card forwards them; re-placing is idempotent.
	switch {
	case msg == wmDisplayChange, msg == wmDpiChanged,
		msg == wmSettingChange && wParam == spiSetWorkArea:
		mu.Lock()
		hh := host
		mu.Unlock()
		if hh != 0 {
			postMessage(hh, wmRehome, 0, 0)
		}
	}
	cw := byHwnd[h]
	if cw == nil {
		if msg == wmMouseActivate {
			return maNoActivate
		}
		return defWindowProc(h, msg, wParam, lParam)
	}
	x, y := int(int16(lParam&0xffff)), int(int16(lParam>>16&0xffff))
	switch msg {
	case wmMouseMove, wmLButtonDown, wmLButtonUp, wmRButtonUp:
		// The app's own synthetic input (an auto clicker) neither hovers nor
		// presses a card: a run at a fixed point must not hit Install.
		if ignore := cw.cfg.IgnoreInput; ignore != nil {
			if extra, _, _ := procExtraInfo.Call(); ignore(extra) {
				return 0
			}
		}
	}
	switch msg {
	case wmMouseActivate:
		return maNoActivate
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		paintCard(cw)
		return 0
	case wmSetCursor:
		cursor := uintptr(idcArrow)
		if cw.hot >= 0 || cw.hot == partClose || (cw.hot == partBody && cw.clickable) {
			cursor = idcHand
		}
		setCursor(cursor)
		return 1
	case wmMouseMove:
		if !cw.tracking {
			cw.tracking = trackLeave(h)
			invalidate(h) // the x brightens on a hovered card
		}
		hoverKey = cw.key
		setHot(cw, hitPart(x, y, cw.close, cw.buttons))
		return 0
	case wmMouseLeave:
		cw.tracking = false
		if hoverKey == cw.key {
			hoverKey = 0
		}
		cw.pressed = partNone
		setHot(cw, partNone)
		invalidate(h)
		return 0
	case wmLButtonDown:
		cw.pressed = hitPart(x, y, cw.close, cw.buttons)
		return 0
	case wmLButtonUp:
		// Pressed and released on the same part, like a button.
		p := hitPart(x, y, cw.close, cw.buttons)
		if p == cw.pressed {
			click(cw.key, p)
		}
		cw.pressed = partNone
		return 0
	case wmRButtonUp:
		click(cw.key, partClose)
		return 0
	}
	return defWindowProc(h, msg, wParam, lParam)
}

func setHot(cw *cardWin, p part) {
	if cw.hot != p {
		cw.hot = p
		invalidate(cw.hwnd)
	}
}

// activeWorkArea returns the work area (physical px) of the monitor the
// focused window is on, and its DPI scale.
func activeWorkArea() (rect, float64) {
	return workAreaOf(monitorFromWindow(windows.GetForegroundWindow(), monDefaultToNear))
}

// rehome returns the current work area and scale of the monitor old is on,
// or the active monitor's when old is on none any more (unplugged, or moved
// out from under it by a resolution or layout change).
func rehome(old rect) (rect, float64) {
	r := winRect{Left: int32(old.L), Top: int32(old.T), Right: int32(old.R), Bottom: int32(old.B)}
	if mon := monitorFromRect(&r, monDefaultToNull); mon != 0 {
		return workAreaOf(mon)
	}
	return activeWorkArea()
}

// workAreaOf returns mon's work area (physical px) and DPI scale.
func workAreaOf(mon hmon) (rect, float64) {
	mi := monitorInfo{}
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	getMonitorInfo(mon, &mi)
	s := 1.0
	var dx, dy uint32
	if r, _, _ := procDpiForMon.Call(uintptr(mon), 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
		s = float64(dx) / 96
	}
	wa := mi.RcWork
	return rect{L: int(wa.Left), T: int(wa.Top), R: int(wa.Right), B: int(wa.Bottom)}, s
}

// newFont creates a font of sizeDip at the given DPI scale. The caller deletes
// it.
func newFont(face string, sizeDip float64, weight int32, scale float64) hfont {
	lf := logFont{Height: -int32(sizeDip*scale + 0.5), Weight: weight, Quality: 5 /* CLEARTYPE */}
	f := []rune(face)
	for i := 0; i < len(f) && i < len(lf.FaceName)-1; i++ {
		lf.FaceName[i] = uint16(f[i])
	}
	return createFont(&lf)
}

// textSize measures s in font (DT_CALCRECT) inside width px; width 0 means a
// single line.
func textSize(dc hdc, font hfont, s string, width int) (w, h int) {
	old := selectObject(dc, font)
	defer selectObject(dc, old)
	r := winRect{Right: int32(width)}
	format := uint32(dtCalcRect | dtNoPrefix | dtSingleLine)
	if width > 0 {
		format = dtCalcRect | dtNoPrefix | dtWordBreak
	}
	drawText(dc, s, &r, format)
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

// measure sizes cw for its content at cw.scale: body lines (with the real font
// via DT_CALCRECT, so it tracks DPI), height, the x and the buttons. Past two
// lines the body is ellipsized: a card is a glance, not a paragraph.
func measure(cw *cardWin) {
	sc := cw.scale
	cw.w = px(cardWidthDip, sc)
	cw.radius = px(cw.cfg.CornerRadius, sc)
	cw.lines = 0
	var labelW []int
	if dc := getDC(0); dc != 0 {
		if cw.body != "" {
			f := newFont("Segoe UI", bodySizeDip, 400, sc)
			_, one := textSize(dc, f, "X", 0)
			_, all := textSize(dc, f, cw.body, cw.w-px(textLeftDip, sc)-px(textRightDip, sc))
			deleteObject(f)
			cw.lines = 1
			if one > 0 && all > one+one/2 {
				cw.lines = maxBodyLines
			}
		}
		if len(cw.labels) > 0 {
			f := newFont("Segoe UI", buttonSizeDip, 600, sc)
			for _, l := range cw.labels {
				lw, _ := textSize(dc, f, l, 0)
				labelW = append(labelW, lw)
			}
			deleteObject(f)
		}
		releaseDC(0, dc)
	} else if cw.body != "" {
		cw.lines = 1
	}
	cw.h = px(cardHeightDip(cw.lines, len(cw.labels) > 0), sc)
	cw.close = closeRect(cw.w, sc)
	cw.buttons = buttonRects(cw.w, cw.h, labelW, sc)
	if len(cw.buttons) < len(cw.labels) { // GetDC failed: still clickable
		cw.buttons = buttonRects(cw.w, cw.h, make([]int, len(cw.labels)), sc)
	}
}

// paintCard draws cw into an off-screen bitmap and copies it in, so a hover
// repaint doesn't flicker.
func paintCard(cw *cardWin) {
	var ps paintStruct
	dc := beginPaint(cw.hwnd, &ps)
	defer endPaint(cw.hwnd, &ps)
	w, h := cw.w, cw.h
	mem := createCompatibleDC(dc)
	if mem == 0 {
		return
	}
	defer deleteDC(mem)
	bmp, _, _ := procCompatBmp.Call(uintptr(dc), uintptr(w), uintptr(h))
	if bmp == 0 {
		return
	}
	defer deleteObject(hgdiobj(bmp))
	old := selectObject(mem, hgdiobj(bmp))
	drawCard(mem, cw)
	_, _, _ = procBitBlt.Call(uintptr(dc), 0, 0, uintptr(w), uintptr(h), uintptr(mem), 0, 0, srcCopy)
	selectObject(mem, old)
}

func drawCard(dc hdc, cw *cardWin) {
	sc := cw.scale
	pal := cw.cfg.Palette
	p := func(dip float64) int32 { return int32(px(dip, sc)) }
	w, h := int32(cw.w), int32(cw.h)
	fill := func(r winRect, c Color) {
		b := solidBrush(c.colorref())
		fillRect(dc, &r, b)
		deleteObject(b)
	}
	frame := func(r winRect, c Color) {
		b := solidBrush(c.colorref())
		frameRect(dc, &r, b)
		deleteObject(b)
	}
	// box fills r with bg (when set) and outlines it with border (when set),
	// rounded by radius px; radius 0 is FillRect/FrameRect.
	box := func(r winRect, bg, border Color, radius int) {
		if radius <= 0 {
			if !bg.IsZero() {
				fill(r, bg)
			}
			if !border.IsZero() {
				frame(r, border)
			}
			return
		}
		brush := stockObject(nullBrush)
		if !bg.IsZero() {
			brush = solidBrush(bg.colorref())
		}
		edge := border
		if edge.IsZero() {
			edge = bg
		}
		pen := solidPen(edge.colorref())
		ob, op := selectObject(dc, brush), selectObject(dc, pen)
		roundRect(dc, r, radius)
		selectObject(dc, ob)
		selectObject(dc, op)
		if !bg.IsZero() {
			deleteObject(brush)
		}
		deleteObject(pen)
	}
	text := func(s, face string, sizeDip float64, weight int32, c Color, r winRect, format uint32) {
		f := newFont(face, sizeDip, weight, sc)
		old := selectObject(dc, f)
		setTextColor(dc, c.colorref())
		drawText(dc, s, &r, format|dtNoPrefix)
		selectObject(dc, old)
		deleteObject(f)
	}
	in := func(r rect) winRect {
		return winRect{Left: int32(r.L), Top: int32(r.T), Right: int32(r.R), Bottom: int32(r.B)}
	}
	setBkMode(dc, bkTransparent)

	all := winRect{Right: w, Bottom: h}
	accent := pal.Accent.For(cw.kind)
	if cw.radius <= 0 {
		fill(all, pal.Background)
		frame(all, pal.Border)
		fill(winRect{Right: p(stripeDip), Bottom: h}, accent)
	} else {
		// The window region clips the corners; the stripe goes under the
		// rounded border so the curve stays clean.
		fill(all, pal.Background)
		fill(winRect{Right: p(stripeDip), Bottom: h}, accent)
		box(all, Color(0), pal.Border, cw.radius)
	}

	top := p(padTopDip)
	text(iconFor(cw.kind), iconFace, iconSizeDip, 400, accent,
		winRect{Left: p(iconLeftDip), Top: top, Right: p(textLeftDip), Bottom: top + p(titleDip)},
		dtSingleLine|dtVCenter|dtNoClip)
	text(cw.title, "Segoe UI", titleSizeDip, 600, pal.Foreground,
		winRect{Left: p(textLeftDip), Top: top, Right: w - p(titleRightDip), Bottom: top + p(titleDip)},
		dtSingleLine|dtVCenter|dtEndEllipsis)
	if cw.lines > 0 {
		// The body wraps to the lines the card was sized for (measure);
		// DT_EDITCONTROL hides a partial extra line and DT_END_ELLIPSIS marks the cut.
		bt := top + p(titleDip+bodyGapDip)
		text(cw.body, "Segoe UI", bodySizeDip, 400, pal.Muted,
			winRect{Left: p(textLeftDip), Top: bt, Right: w - p(textRightDip), Bottom: bt + p(float64(cw.lines*bodyLineDip))},
			dtWordBreak|dtEndEllipsis|dtEditControl)
	}

	// Inner pieces (the x's hover fill, buttons) get a smaller radius.
	inner := cw.radius * 6 / 10
	xc := pal.Faint
	switch {
	case cw.hot == partClose:
		box(in(cw.close), pal.Hover, Color(0), inner)
		xc = pal.Foreground
	case hoverKey == cw.key:
		xc = pal.Muted
	}
	text(glyphX, iconFace, 9, 400, xc, in(cw.close), dtSingleLine|dtVCenter|dtCenter)

	for i, b := range cw.buttons {
		if i >= len(cw.labels) {
			break
		}
		hot := cw.hot == part(i)
		bg, fg, border := pal.Button, pal.Foreground, pal.Border
		if hot {
			bg = pal.Hover
		}
		if i == 0 { // primary
			bg, fg, border = pal.Primary, pal.OnPrimary, Color(0)
			if hot {
				bg = pal.PrimaryHover
			}
		}
		box(in(b), bg, border, inner)
		text(cw.labels[i], "Segoe UI", buttonSizeDip, 600, fg, in(b),
			dtSingleLine|dtVCenter|dtCenter|dtEndEllipsis)
	}
}

// cardWindows returns the card windows oldest first (tests).
func cardWindows() []hwnd {
	mu.Lock()
	defer mu.Unlock()
	return append([]hwnd(nil), hwnds...)
}
