//go:build darwin && cgo

package toast

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework AVFoundation -framework QuartzCore
#include <stdlib.h>
#include "toast_darwin.h"
*/
import "C"

import (
	"log"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// Each card is a borderless, non-activating NSPanel (toast_darwin.m) that can
// never become key or main, is shown with orderFrontRegardless, sits at the
// status-window level on every Space (full-screen ones too), and doesn't hide
// when the app is in the background. So, as on Windows, a card never takes
// focus, and clicking one doesn't activate the app.
//
// AppKit only works on the main thread, so that is the window thread here.
// Other goroutines change the model under mu and queue a sync on the main
// dispatch queue (toast_wake); the main thread runs syncCards, the 30 ms
// tick timer and the mouse handlers, and is the only one that touches the
// main-thread state below. As on Windows it never runs a note's callbacks
// itself: each gets its own goroutine once the card is out of the model.
//
// The main queue only runs while the main thread runs an AppKit event loop.
// A Wails app (or any Cocoa app) already does. A plain Go program has to hand
// its main thread over with Main. Cards posted before the loop starts show
// when it does.
//
// Geometry is in points, so the scale is always 1: AppKit draws the text and
// shapes at the screen's backing scale by itself.

func init() {
	// Main needs the main goroutine on the main thread (thread 0), where
	// AppKit's event loop must run. Locking it in init keeps it there; Wails
	// and every other Cocoa binding for Go does the same.
	runtime.LockOSThread()
}

// Placement requests for the next sync, in increasing strength.
const (
	relocNone   = iota
	relocRehome // re-place the stack on its screen (it may have changed)
	relocActive // a new stack: the screen with the mouse
)

// Shared state, guarded by mu.
var (
	mu       sync.Mutex
	model    stack
	relocate int
	raise    bool     // re-assert the cards' order on the next sync
	through  int      // HoldClickThrough holds; cards are click-through while > 0
	keys     []uint64 // card keys on screen, oldest first, for tests
	shown    atomic.Int32
	started  atomic.Bool // a Post has happened
	queued   atomic.Bool // a sync is queued on the main thread
	mainRan  atomic.Bool // the main thread has run a sync
	watchdog sync.Once
)

// Main-thread state.
var (
	wins     = map[uint64]*cardWin{}
	work     rect // the stack's screen's visible frame, top-down points
	hoverKey uint64
	ticking  bool
	setup    bool
)

// cardWin is a card's panel and its copy of the card's content.
type cardWin struct {
	key, rev     uint64
	cfg          Config
	title, body  string
	kind         Kind
	labels       []string
	clickable    bool
	lines        int
	w, h         int
	close        rect
	buttons      []rect
	x, y, pw, ph int
	placed       bool
	alpha        byte
	hot, pressed part
	hovered      bool // the state last sent to the view
	through      bool
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
		relocate = relocActive
	} else if relocate < relocRehome { // a screen unplugged or resized under a sticky card moves the stack
		relocate = relocRehome
	}
	model.post(n, time.Now())
	raise = true
	mu.Unlock()
	if !n.Silent && o.Sound {
		play(soundFor(n.Kind, len(n.Actions) > 0), o.Volume)
	}
	started.Store(true)
	wake()
}

// Close removes the card with id, if it is up, without running its callbacks.
func Close(id string) {
	mu.Lock()
	closed := model.closeID(id)
	mu.Unlock()
	if closed {
		wake()
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

// HoldClickThrough makes the cards click-through (ignoresMouseEvents) until
// the returned release is called. Holds nest.
func HoldClickThrough() (release func()) {
	mu.Lock()
	through++
	mu.Unlock()
	wakeIfStarted()
	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			through--
			mu.Unlock()
			wakeIfStarted()
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
	mu.Unlock()
	wakeIfStarted() // a corner change moves the stack
}

// Main runs run with the main thread handed to AppKit, and returns when run
// does. A plain Go program (one without Wails or another Cocoa event loop)
// needs it for cards to show on macOS, because AppKit windows only work on
// the main thread and only while it runs an event loop:
//
//	func main() { toast.Main(realMain) }
//
// Call it from main (the main goroutine). When an event loop is already
// running, or Main isn't called on the main thread, it just calls run. On
// other platforms it just calls run.
func Main(run func()) {
	if C.toast_main_thread() == 0 || C.toast_app_running() != 0 {
		run()
		return
	}
	go func() {
		defer C.toast_main_stop()
		run()
	}()
	C.toast_main_run()
}

func wakeIfStarted() {
	if started.Load() {
		wake()
	}
}

// wake queues a sync on the main thread, unless one is queued already. The
// first time, it also watches for a main thread that never runs it (a plain
// Go program that didn't call Main) and says so in the log.
func wake() {
	watchdog.Do(func() {
		time.AfterFunc(10*time.Second, func() {
			if !mainRan.Load() {
				log.Printf("%sno AppKit event loop is running on the main thread, so cards can't show; run the program inside toast.Main", CurrentConfig().logPrefix())
			}
		})
	})
	if queued.CompareAndSwap(false, true) {
		C.toast_wake()
	}
}

//export goToastSync
func goToastSync() {
	mainRan.Store(true)
	queued.Store(false) // before syncing: a change made during it queues another
	syncCards()
}

//export goToastTick
func goToastTick() {
	mu.Lock()
	model.tick(time.Now(), hoverKey != 0)
	mu.Unlock()
	syncCards()
}

//export goToastRehome
func goToastRehome() {
	mu.Lock()
	if len(model.cards) > 0 && relocate < relocRehome {
		relocate = relocRehome
		raise = true
	}
	mu.Unlock()
	syncCards()
}

func playWAV(b []byte) {
	if len(b) > 0 {
		C.toast_play(unsafe.Pointer(&b[0]), C.int(len(b)))
	}
}

// syncCards makes the panels match the model: creates, re-measures, places,
// fades and destroys them, and runs the tick timer while any card is fading
// or counting down. Main thread only.
func syncCards() {
	if !setup {
		C.toast_setup()
		setup = true
	}
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
	reloc, up, pass := relocate, raise, through > 0
	relocate, raise = relocNone, false
	mu.Unlock()

	if reloc != relocNone && len(views) > 0 {
		r := C.toast_work_area(boolInt(reloc == relocActive), C.ToastRect{l: C.double(work.L), t: C.double(work.T), r: C.double(work.R), b: C.double(work.B)})
		work = rect{L: round(r.l), T: round(r.t), R: round(r.r), B: round(r.b)}
	}

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
		if fresh || cw.rev != v.rev {
			cw.rev = v.rev
			cw.title, cw.body, cw.kind, cw.labels, cw.clickable = v.title, v.body, v.kind, v.labels, v.clickable
			measure(cw)
			if fresh && !createCard(cw) {
				continue
			}
			pushContent(cw)
			pushState(cw)
		}
		if cw.through != pass {
			setClickThrough(cw, pass)
		}
		cards, heights, alphas = append(cards, cw), append(heights, cw.h), append(alphas, v.alpha)
	}

	origins := stackOrigins(work, corner, cardWidthDip, heights, cardGapDip, cardMarginDip)
	for i, cw := range cards {
		p := origins[i]
		moved := !cw.placed || p.X != cw.x || p.Y != cw.y || cw.w != cw.pw || cw.h != cw.ph
		if moved || up {
			C.toast_card_frame(C.uint64_t(cw.key), C.double(p.X), C.double(p.Y), C.double(cw.w), C.double(cw.h), boolInt(up))
			cw.x, cw.y, cw.pw, cw.ph = p.X, p.Y, cw.w, cw.h
		}
		if alphas[i] != cw.alpha || !cw.placed {
			cw.alpha = alphas[i]
			C.toast_card_alpha(C.uint64_t(cw.key), C.double(float64(cw.alpha)/255))
		}
		if !cw.placed {
			cw.placed = true
			C.toast_card_show(C.uint64_t(cw.key))
		}
	}

	list := make([]uint64, len(cards))
	for i, cw := range cards {
		list[i] = cw.key
	}
	mu.Lock()
	keys = list
	mu.Unlock()
	switch {
	case timed && !ticking:
		C.toast_timer(1)
		ticking = true
	case !timed && ticking:
		C.toast_timer(0)
		ticking = false
	}
}

func boolInt(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

func round(f C.double) int { return int(math.Round(float64(f))) }

// measure sizes cw for its content: body lines (measured with the real font),
// height, the x and the buttons. Past two lines the body is ellipsized.
func measure(cw *cardWin) {
	cw.w = cardWidthDip
	cw.lines = 0
	if cw.body != "" {
		s := C.CString(cw.body)
		cw.lines = int(C.toast_body_lines(s, C.double(cardWidthDip-textLeftDip-textRightDip), C.double(bodySizeDip)))
		C.free(unsafe.Pointer(s))
		cw.lines = max(1, min(cw.lines, maxBodyLines))
	}
	var labelW []int
	for _, l := range cw.labels {
		s := C.CString(l)
		labelW = append(labelW, int(math.Ceil(float64(C.toast_text_width(s, C.double(buttonSizeDip))))))
		C.free(unsafe.Pointer(s))
	}
	cw.h = px(cardHeightDip(cw.lines, len(cw.labels) > 0), 1)
	cw.close = closeRect(cw.w, 1)
	cw.buttons = buttonRects(cw.w, cw.h, labelW, 1)
}

func createCard(cw *cardWin) bool {
	t := C.CString(cw.cfg.AppName)
	defer C.free(unsafe.Pointer(t))
	if C.toast_card_create(C.uint64_t(cw.key), t, boolInt(cw.cfg.CaptureExclude), C.double(cw.w), C.double(cw.h)) == 0 {
		return false
	}
	wins[cw.key] = cw
	shown.Add(1)
	return true
}

func cRect(r rect) C.ToastRect {
	return C.ToastRect{l: C.double(r.L), t: C.double(r.T), r: C.double(r.R), b: C.double(r.B)}
}

// pushContent hands cw's content, look and geometry to its view.
func pushContent(cw *cardWin) {
	pal := cw.cfg.Palette
	col := func(c Color) C.uint32_t { return C.uint32_t(uint32(c) & 0xFFFFFF) }
	colors := C.ToastColors{
		bg: col(pal.Background), fg: col(pal.Foreground), muted: col(pal.Muted), border: col(pal.Border),
		accent: col(pal.Accent.For(cw.kind)), faint: col(pal.Faint), hover: col(pal.Hover), button: col(pal.Button),
		primary: col(pal.Primary), primaryHover: col(pal.PrimaryHover), onPrimary: col(pal.OnPrimary),
	}
	geom := C.ToastGeom{
		stripe: stripeDip, iconLeft: iconLeftDip, iconSize: iconSizeDip, textLeft: textLeftDip,
		textRight: textRightDip, titleRight: titleRightDip, padTop: padTopDip, titleH: titleDip,
		titleSize: titleSizeDip, bodyGap: bodyGapDip, bodySize: bodySizeDip, buttonSize: buttonSizeDip,
	}
	var labels [2]*C.char
	var brects [2]C.ToastRect
	for i := 0; i < len(cw.labels) && i < 2; i++ {
		labels[i] = C.CString(cw.labels[i])
		defer C.free(unsafe.Pointer(labels[i]))
		if i < len(cw.buttons) {
			brects[i] = cRect(cw.buttons[i])
		}
	}
	title, body := C.CString(cw.title), C.CString(cw.body)
	defer C.free(unsafe.Pointer(title))
	defer C.free(unsafe.Pointer(body))
	C.toast_card_content(C.uint64_t(cw.key), title, body, C.int(cw.kind), C.int(cw.lines),
		labels[0], labels[1], C.int(min(len(cw.labels), 2)), cRect(cw.close), brects[0], brects[1],
		C.double(px(cw.cfg.CornerRadius, 1)), colors, geom)
}

// pushState sends cw's hover state to its view (it redraws only on a change).
func pushState(cw *cardWin) {
	cw.hovered = hoverKey == cw.key
	C.toast_card_state(C.uint64_t(cw.key), C.int(cw.hot), boolInt(cw.hovered), boolInt(cw.clickable))
}

func setHot(cw *cardWin, p part) {
	cw.hot = p
	pushState(cw)
}

// setClickThrough turns ignoresMouseEvents on or off for cw. A card going
// click-through gets no mouseExited, so its hover is dropped here.
func setClickThrough(cw *cardWin, on bool) {
	if on {
		if hoverKey == cw.key {
			hoverKey = 0
		}
		cw.pressed = partNone
		setHot(cw, partNone)
	}
	C.toast_card_through(C.uint64_t(cw.key), boolInt(on))
	cw.through = on
}

func destroyCard(cw *cardWin) {
	delete(wins, cw.key)
	if hoverKey == cw.key { // no mouseExited comes for a removed panel
		hoverKey = 0
	}
	C.toast_card_destroy(C.uint64_t(cw.key))
	shown.Add(-1)
}

//export goToastMouse
func goToastMouse(key C.uint64_t, ev C.int, fx, fy C.double) {
	cw := wins[uint64(key)]
	if cw == nil || cw.through {
		return
	}
	x, y := int(math.Floor(float64(fx))), int(math.Floor(float64(fy)))
	switch ev {
	case C.toastMove:
		hoverKey = cw.key
		setHot(cw, hitPart(x, y, cw.close, cw.buttons))
	case C.toastLeave:
		if hoverKey == cw.key {
			hoverKey = 0
		}
		cw.pressed = partNone
		setHot(cw, partNone)
	case C.toastDown:
		cw.pressed = hitPart(x, y, cw.close, cw.buttons)
	case C.toastUp:
		// Pressed and released on the same part, like a button.
		if p := hitPart(x, y, cw.close, cw.buttons); p == cw.pressed {
			click(cw.key, p)
		}
		cw.pressed = partNone
	case C.toastRightUp:
		click(cw.key, partClose)
	}
}

// click handles a left click on p (or a right click, as partClose): the card
// leaves the model first, then its callback gets its own goroutine. The panels
// catch up on the next sync, queued rather than run here so the clicked panel
// isn't removed inside its own event.
func click(key uint64, p part) {
	mu.Lock()
	c := model.take(key)
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
	wake()
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

// Test hooks: _test.go files can't use cgo.

// cardKeys returns the cards on screen, oldest first.
func cardKeys() []uint64 {
	mu.Lock()
	defer mu.Unlock()
	return append([]uint64(nil), keys...)
}

type panelInfo struct {
	exists, visible, key, main, ignoresMouse, sharingNone, onScreen bool
	appActive, appHasKey                                            bool
	level                                                           int
	alpha                                                           float64
	frame, work                                                     rect
}

func infoOf(key uint64) panelInfo {
	i := C.toast_test_info(C.uint64_t(key))
	r := func(r C.ToastRect) rect { return rect{L: round(r.l), T: round(r.t), R: round(r.r), B: round(r.b)} }
	return panelInfo{
		exists: i.exists != 0, visible: i.visible != 0, key: i.key != 0, main: i.main != 0,
		ignoresMouse: i.ignoresMouse != 0, sharingNone: i.sharingNone != 0, onScreen: i.onScreen != 0,
		appActive: i.appActive != 0, appHasKey: i.appHasKey != 0, level: int(i.level), alpha: float64(i.alpha),
		frame: r(i.frame), work: r(i.work),
	}
}

// mouseAt sends a synthetic mouse event at (x, y) in key's top-down view
// coordinates.
func mouseAt(key uint64, ev int, x, y int) {
	C.toast_test_mouse(C.uint64_t(key), C.int(ev), C.double(x), C.double(y))
}

const (
	evMove      = C.toastMove
	evDown      = C.toastDown
	evUp        = C.toastUp
	evRightUp   = C.toastRightUp
	evRightDown = C.toastRightDown
)

func frontmostPID() int { return int(C.toast_test_frontmost_pid()) }

// statusLevel is NSStatusWindowLevel.
const statusLevel = 25
