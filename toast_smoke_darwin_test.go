//go:build darwin && cgo

package toast

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func smokeOn() bool { return os.Getenv("GO_TOAST_SMOKE") == "1" || os.Getenv("TORU_SMOKE") == "1" }

// TestMain gives AppKit the main thread for the smoke test, the way a plain
// Go program uses Main. Without the smoke test the tests run as usual (no
// event loop, no window server needed).
func TestMain(m *testing.M) {
	code := 0
	if smokeOn() {
		Main(func() { code = m.Run() })
	} else {
		code = m.Run()
	}
	os.Exit(code)
}

// TestToastSmoke shows real cards, clicks them, and checks they never take
// focus. Opt-in (GO_TOAST_SMOKE=1): it draws on the desktop and plays the
// sounds. GO_TOAST_RADIUS=10 runs it with round corners and the Light palette.
func TestToastSmoke(t *testing.T) {
	if !smokeOn() {
		t.Skip("set GO_TOAST_SMOKE=1")
	}
	c := DefaultConfig()
	c.AppName = "go-toast smoke"
	if os.Getenv("GO_TOAST_RADIUS") != "" {
		c.Palette, c.CornerRadius = Light, 10
	}
	// GO_TOAST_SHOT=file.png also saves a screenshot of the stack, so a run
	// on a remote Mac (CI) shows what the cards look like. The cards must be
	// capturable for that.
	shot := os.Getenv("GO_TOAST_SHOT")
	if shot != "" {
		c.CaptureExclude = false
	}
	Configure(c)
	SetOptions(DefaultOptions())

	front := frontmostPID()
	active := infoOf(0).appActive
	assertFocus := func() {
		t.Helper()
		if pid := frontmostPID(); pid != front {
			t.Fatalf("toast changed the frontmost app: pid %d -> %d", front, pid)
		}
		i := infoOf(0)
		if i.appActive != active || i.appHasKey {
			t.Fatalf("toast activated the app (active %v -> %v) or made a key window (%v)", active, i.appActive, i.appHasKey)
		}
		for _, k := range cardKeys() {
			if p := infoOf(k); p.key || p.main {
				t.Fatalf("card %d became key (%v) or main (%v)", k, p.key, p.main)
			}
		}
	}

	Post(Note{ID: "smoke-save", Title: "Saving clip…", Body: "Test · Last 1 min", Kind: Info, Silent: true})
	time.Sleep(700 * time.Millisecond)
	Show("Clip saved", "Test · Last 1 min", true) // stacks: no ID
	Post(Note{ID: "smoke-save", Title: "Copied to clipboard", Kind: Success})
	if n := len(cardKeysSoon(2)); n != 2 {
		t.Fatalf("%d cards, want 2 (same ID replaces)", n)
	}

	ran := make(chan string, 2)
	Post(Note{
		Title:   "Toru v9.9.9 is ready",
		Body:    "Restart now, or it installs when you quit. This body is long enough to wrap past two lines, so the card has to cut it with an ellipsis.",
		Sticky:  true,
		Actions: []Action{{Label: "Restart", Run: func() { ran <- "restart" }}, {Label: "Later"}},
	})
	cards := cardKeysSoon(3)
	if len(cards) != 3 {
		t.Fatalf("%d cards, want 3", len(cards))
	}
	time.Sleep(400 * time.Millisecond)
	assertFocus()

	// Every card is up, on the screen's visible frame (clear of the menu bar
	// and the Dock), above normal windows, kept out of capture, and the
	// newest is nearest the top-right corner.
	var prev panelInfo
	for i, k := range cards {
		p := infoOf(k)
		if !p.exists || !p.visible || !p.onScreen || p.alpha < 0.5 {
			t.Fatalf("card %d: exists %v visible %v onScreen %v alpha %.2f", i, p.exists, p.visible, p.onScreen, p.alpha)
		}
		if p.level < statusLevel || p.sharingNone != (shot == "") || p.ignoresMouse {
			t.Fatalf("card %d: level %d sharingNone %v ignoresMouse %v", i, p.level, p.sharingNone, p.ignoresMouse)
		}
		if p.frame.R != p.work.R-cardMarginDip || p.frame.R-p.frame.L != cardWidthDip {
			t.Errorf("card %d frame %+v not %d pt wide at the right margin of %+v", i, p.frame, cardWidthDip, p.work)
		}
		if i > 0 && p.frame.B+cardGapDip != prev.frame.T {
			t.Errorf("card %d (%+v) should sit just above card %d (%+v)", i, p.frame, i-1, prev.frame)
		}
		prev = p
	}
	newest := cards[len(cards)-1]
	if shot != "" {
		mouseAt(newest, evMove, 200, 30) // hover: the x lights up
		time.Sleep(200 * time.Millisecond)
		if out, err := exec.Command("screencapture", "-x", shot).CombinedOutput(); err != nil {
			t.Logf("screencapture: %v %s", err, out)
		}
	}
	np := infoOf(newest)
	if np.frame.T != np.work.T+cardMarginDip {
		t.Errorf("newest card %+v should be at the top margin of %+v", np.frame, np.work)
	}
	if h := np.frame.B - np.frame.T; h != px(cardHeightDip(maxBodyLines, true), 1) {
		t.Errorf("update card is %d pt tall, want two body lines and a button row", h)
	}

	// Click-through holds.
	release := HoldClickThrough()
	if !soon(func() bool { return infoOf(newest).ignoresMouse }) {
		t.Error("HoldClickThrough didn't make the cards ignore the mouse")
	}
	release()
	if !soon(func() bool { return !infoOf(newest).ignoresMouse }) {
		t.Error("releasing the hold didn't give the cards the mouse back")
	}

	// Click the update card's primary button, as the user would. A point just
	// inside its left edge is on it (see the Windows smoke test).
	b := buttonRects(np.frame.R-np.frame.L, np.frame.B-np.frame.T, []int{30, 30}, 1)[0]
	mouseAt(newest, evMove, b.L+4, b.T+4)
	mouseAt(newest, evDown, b.L+4, b.T+4)
	mouseAt(newest, evUp, b.L+4, b.T+4)
	select {
	case got := <-ran:
		if got != "restart" {
			t.Fatalf("ran %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the Restart action never ran")
	}
	assertFocus()
	if n := len(cardKeysSoon(2)); n != 2 {
		t.Fatalf("%d cards after the click, want 2", n)
	}

	// A right click dismisses.
	dismissed := make(chan bool, 1)
	Post(Note{Title: "Right-click me", Sticky: true, OnDismiss: func() { dismissed <- true }})
	var right uint64
	if !soon(func() bool {
		ks := cardKeys()
		right = 0
		if len(ks) > 0 {
			right = ks[len(ks)-1]
		}
		return right > newest
	}) {
		t.Fatal("the right-click card never showed")
	}
	mouseAt(right, evRightDown, 60, 20)
	mouseAt(right, evRightUp, 60, 20)
	select {
	case <-dismissed:
	case <-time.After(2 * time.Second):
		t.Fatal("a right click didn't dismiss")
	}
	assertFocus()

	Close("smoke-save")
	for i := 0; i < 70 && Visible(); i++ { // the plain card's lifetime + fade
		time.Sleep(100 * time.Millisecond)
		assertFocus()
	}
	if Visible() {
		t.Error("cards should have faded out and closed")
	}
}

// cardKeysSoon waits briefly for the main thread to reach want cards.
func cardKeysSoon(want int) []uint64 {
	var got []uint64
	for i := 0; i < 40; i++ {
		if got = cardKeys(); len(got) == want {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	return got
}

func soon(ok func() bool) bool {
	for i := 0; i < 40; i++ {
		if ok() {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}
