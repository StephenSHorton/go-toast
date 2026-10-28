# go-toast

Small native notification cards for Go apps on Windows and macOS. Cards stack
in a corner of the screen you're working on, sit above every other window, and
never take focus, so they're safe to show over a game. Each card can play one
of three short sounds.

This is the notification system from [Toru](https://github.com/StephenSHorton/toru),
pulled out into its own module. The same API and the same cards work on both
platforms:

| Platform | Backend |
|---|---|
| Windows | Plain Win32/GDI windows (no webview, no cgo). Depends only on `golang.org/x/sys`. |
| macOS | AppKit panels via cgo and Objective-C. See [macOS notes](#macos-notes). |
| Anything else | Every call does nothing, so code that uses it still builds. A macOS build with `CGO_ENABLED=0` gets this no-op backend too. |

```
go get github.com/StephenSHorton/go-toast
```

## Example

```go
package main

import (
	"time"

	toast "github.com/StephenSHorton/go-toast"
)

func main() {
	// On macOS a plain Go program hands the main thread to AppKit with Main.
	// Elsewhere Main just calls the function. Do NOT do this in a Wails,
	// systray or other Cocoa app: call run (or wails.Run) directly there.
	toast.Main(run)
}

func run() {
	// Once, at startup, before the first Post. Start from DefaultConfig.
	c := toast.DefaultConfig()
	c.AppName = "MyApp"
	c.Palette = toast.Light
	c.CornerRadius = 10
	toast.Configure(c)

	// The user's settings (persist these however you like; they're JSON-tagged).
	toast.SetOptions(toast.Options{Enabled: true, Sound: true, Volume: 0.6, Corner: toast.CornerTopRight})

	// A card with an ID is replaced in place by the next one with that ID.
	toast.Post(toast.Note{ID: "save", Title: "Saving clip…", Silent: true})
	time.Sleep(time.Second)
	toast.Post(toast.Note{ID: "save", Title: "Clip saved", Body: "Last 1 min", Kind: toast.Success})

	// A sticky card with buttons. Callbacks run on their own goroutine.
	toast.Post(toast.Note{
		Title:  "Update ready",
		Body:   "Restart now, or it installs when you quit.",
		Sticky: true,
		Actions: []toast.Action{
			{Label: "Restart", Run: func() { /* ... */ }},
			{Label: "Later"},
		},
	})

	toast.Show("Copied", "", true) // a plain Success (true) or Error (false) card
	toast.PlaySound(toast.Error)   // a sound with no card
	time.Sleep(15 * time.Second)
}
```

## API

| | |
|---|---|
| `Post(Note)` | Shows a card, or replaces the live card that has the same `ID`. Plays its sound. Never blocks. |
| `Close(id)` | Removes the card with that ID without running its callbacks. |
| `Show(title, body, ok)` | Shortcut for a plain Success or Error card. |
| `PlaySound(Kind)` | Plays a kind's sound without a card. It uses `Volume` and ignores `Enabled` and `Sound`. |
| `SetOptions` / `CurrentOptions` | The user's settings: `Enabled`, `Sound`, `Volume` (0..1) and `Corner`. Out-of-range values are fixed. |
| `HoldClickThrough()` | Lets clicks pass through the cards until you call the returned release. Holds can overlap. |
| `Visible()` | Reports whether any card is on screen. |
| `Configure` / `CurrentConfig` | The app-level setup (below). |
| `Main(func())` | macOS: runs the function while the main thread runs AppKit's event loop. Only programs without a Cocoa event loop of their own need it, and a Wails, systray or other Cocoa app must **not** call it (see [macOS notes](#macos-notes)). Elsewhere it just calls the function. |

`Note` fields: `ID`, `Title`, `Body` (cut to two lines), `Kind`
(`Info`, `Success`, `Warn`, `Error`), `Silent`, `Expire`, `Sticky`,
`Actions` (up to two buttons, and the first is the primary), `OnClick` and
`OnDismiss`.

By default a card lasts 3 s, a Warn or Error card 6 s, and a card with buttons
10 s. A card won't expire while the cursor is over the stack. At most five
cards show at once; when a sixth arrives, the oldest non-sticky card is pushed
off.

## Config

Call `Configure` once at startup, before the first `Post`. The window classes
and title are fixed when the first card opens. After that, a new `Configure`
only affects cards created later.

| Field | Default | |
|---|---|---|
| `AppName` | `"Toast"` | The windows' title. Also the log prefix (`myapp: toast: …`). |
| `ClassPrefix` | from `AppName` | Windows only. Window class names: cards use `ClassPrefix` and the hidden host window uses `ClassPrefix+"Host"`. `"Toru"` gives `ToruToast` and `ToruToastHost`. |
| `Palette` | `Dark` | Card colors. |
| `CornerRadius` | `0` | Corner rounding in dip. `0` is square; about `10` gives soft cards. |
| `IgnoreInput` | `nil` | `func(extraInfo uintptr) bool`. It gets the tag of each mouse event a card receives; return true to drop that event. Use it to keep your app's own injected input from pressing a card. On Windows the tag is `GetMessageExtraInfo` (`SendInput` with a tagged `dwExtraInfo`); on macOS it is the CGEvent's `kCGEventSourceUserData` field (set it with `CGEventSetIntegerValueField` before `CGEventPost`). |
| `CaptureExclude` | `true` | Hides cards from screenshots and screen recordings while they stay on screen. Windows uses `WDA_EXCLUDEFROMCAPTURE` (Windows 10 2004+); macOS uses `NSWindowSharingNone`. |

Start from `DefaultConfig()` rather than `Config{}`: in a zero literal,
`CaptureExclude` is false.

### Palettes

A `Palette` has `Background`, `Foreground`, `Muted`, `Border` and
`Accent` (one color per kind). You can also set `Faint`, `Hover`, `Button`,
`Primary`, `PrimaryHover` and `OnPrimary`. Any of these you leave unset is
derived from the base colors. Any base color you leave unset comes from `Dark`.
Build colors with `toast.RGB(r, g, b)` or `toast.Hex("#rrggbb")`. The zero
`Color` means unset, so black is `RGB(0, 0, 0)`.

- `toast.Dark`: near-black cards, blue for info and success, amber for warn,
  red for error. This is Toru's look, with every color set explicitly.
- `toast.Light`: white cards and slate text (`#0f172a` / `#64748b`). The border
  is `rgb(203,213,225)`. Accents are info `#2563eb`, success `#15803d`, warn
  `#c2620a` and error `#ef4444`.

## Behaviour notes

- Cards never take focus. On Windows they are no-activate, tool-window,
  topmost popups. On macOS they are non-activating panels that can never
  become key or main. On both, clicking a card doesn't activate the app.
- Exclusive-fullscreen games skip the desktop compositor, so nothing can draw
  over them. In that case the sound is the notification.
- On Windows, one locked OS thread, started by the first `Post`, owns all the
  windows. On macOS the main thread owns them. Callbacks always run on their
  own goroutine, so a callback can call `Post` or `Close`, or block, without
  hanging the cards.

## macOS notes

- **cgo is required.** The backend is Objective-C (`toast_darwin.m`), so a
  macOS build needs cgo and the Xcode command line tools. Wails apps already
  build with cgo on the Mac. With `CGO_ENABLED=0` the package still builds,
  but every call is a no-op.
- **The main thread.** AppKit windows only work on the main thread, and only
  while it runs an event loop. Creating one on another thread raises
  `NSWindow should only be instantiated on the main thread!` and kills the
  process, so the cards can't have a thread of their own as they do on
  Windows. Instead the package queues its work on the main dispatch queue.
  - In a Wails app, or any program with its own Cocoa event loop, this just
    works. Cards posted before the loop starts appear when it does.
  - **Do not call `Main` in a Wails, systray or other Cocoa app.** At the
    top of `main` no event loop runs yet, so `Main` can't tell one will
    start later. It takes the main thread for its own loop and moves `run`
    (and so `wails.Run`) to another thread, where the app's own AppKit calls
    abort with `NSWindow should only be instantiated on the main thread!`.
    Windows and Linux never show this, so don't wrap `main` "to keep one main
    function for every platform" in such an app.
  - A plain Go program must hand over its main thread: wrap `main` in
    `toast.Main(run)`. `Main` runs `NSApp` on the main thread and runs `run`
    on another goroutine. It returns when `run` returns. A binary with no
    activation policy gets the Accessory one, which means no Dock icon and no
    menu bar. If nothing ever runs the event loop, the package logs a warning
    after 10 s and the cards don't show.
  - The package locks the main goroutine to the main thread in `init`, so
    `Main` can use that thread. Wails and other Cocoa bindings for Go do the
    same.
- **Placement.** The stack goes on the screen that has the mouse. It uses
  that screen's `visibleFrame`, so it stays clear of the menu bar and the
  Dock, and it moves if the screen layout changes. Cards are at the status
  window level and show on every Space, including full-screen ones.
  Geometry is in points, so Retina screens get sharp cards with no scaling
  code.
- **Input.** Hovering over the stack pauses expiry. A click on the body runs
  `OnClick`. The x, a right click and a Control-click dismiss the card.
  `Config.IgnoreInput` gets each event's `kCGEventSourceUserData`, so an app
  that tags the events it posts keeps them off the cards from the first one.
  `HoldClickThrough` sets `ignoresMouseEvents`, but through the main queue,
  so it takes effect only once a busy main thread gets to it. Use
  `IgnoreInput` for injected input that must never press a card. A press
  dragged off a card and released elsewhere is not a click.
- **Text.** Invalid UTF-8 in a title, body, button label or `AppName` shows
  as U+FFFD, as on Windows.
- **Capture.** `CaptureExclude` sets `NSWindowSharingNone`. Some recorders
  built on ScreenCaptureKit on recent macOS versions still capture such
  windows, so treat it as best effort there.
- **Sounds.** The same embedded WAVs play through `AVAudioPlayer` at
  `Volume`.
- **Look.** `Palette`, `CornerRadius` and `AppName` (the panels' title) work
  as on Windows. The text uses the system font, and the icons are SF Symbols
  (macOS 11+).

## Tests

```
go test ./...
```

The smoke test opens real cards on the desktop and plays their sounds, so it
only runs when you opt in: `GO_TOAST_SMOKE=1 go test -run Smoke .`. Add
`GO_TOAST_RADIUS=10` to run it with the Light palette and rounded corners.
There is one for Windows and one for macOS. The macOS one runs inside `Main`.
It checks placement, click-through and capture exclusion, clicks a button and
right-clicks a card, and checks that the app never becomes active and that no
card becomes key.

CI (`.github/workflows/ci.yml`) runs `go vet` and `go test` on Windows
(amd64 and 386) and on macOS. It runs the macOS smoke test on the runner's
desktop, and it cross-builds Linux, Windows, and macOS without cgo.

## Licence

MIT; see [LICENSE](LICENSE). The three sounds in `sounds/` (`success.wav`,
`fail.wav`, `published.wav`) come from the s&box editor. They are Copyright (c)
2025 Facepunch Studios Ltd under the MIT licence; see
[sounds/NOTICE.md](sounds/NOTICE.md). They are embedded in the binary, so keep
that notice with any distribution.
