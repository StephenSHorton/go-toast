# go-toast

Small native notification cards for Windows apps written in Go. Cards stack in
a corner of the monitor you're working on, sit above every other window, and
never take focus, so they're safe to show over a game. Each card can play one
of three short sounds.

This is the notification system from [Toru](https://github.com/StephenSHorton/toru),
pulled out into its own module. It depends only on `golang.org/x/sys`.
It draws with plain Win32/GDI (no webview, no cgo). On other platforms every
call does nothing, so code that uses it still builds everywhere.

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
| `ClassPrefix` | from `AppName` | Window class names: cards use `ClassPrefix` and the hidden host window uses `ClassPrefix+"Host"`. `"Toru"` gives `ToruToast` and `ToruToastHost`. |
| `Palette` | `Dark` | Card colors. |
| `CornerRadius` | `0` | Corner rounding in dip. `0` is square; about `10` gives soft cards. |
| `IgnoreInput` | `nil` | `func(extraInfo uintptr) bool`. It gets the `GetMessageExtraInfo` value of each mouse message a card receives; return true to drop that message. Use it to keep your app's own injected input (from `SendInput` with a tagged `dwExtraInfo`) from pressing a card. |
| `CaptureExclude` | `true` | Hides cards from screenshots and screen recordings while they stay on screen (`WDA_EXCLUDEFROMCAPTURE`, Windows 10 2004+). |

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

- Cards never take focus. They are no-activate, tool-window, topmost popups.
  A click on a card doesn't activate it either.
- Exclusive-fullscreen games skip the desktop compositor, so nothing can draw
  over them. In that case the sound is the notification.
- One locked OS thread, started by the first `Post`, owns all the windows.
  Callbacks always run on their own goroutine, so a callback can call `Post`
  or `Close`, or block, without hanging the cards.

## Tests

```
go test ./...
```

The smoke test opens real cards on the desktop and plays their sounds, so it
only runs when you opt in: `GO_TOAST_SMOKE=1 go test -run Smoke .`. Add
`GO_TOAST_RADIUS=10` to run it with the Light palette and rounded corners.

## Licence

MIT; see [LICENSE](LICENSE). The three sounds in `sounds/` (`success.wav`,
`fail.wav`, `published.wav`) come from the s&box editor. They are Copyright (c)
2025 Facepunch Studios Ltd under the MIT licence; see
[sounds/NOTICE.md](sounds/NOTICE.md). They are embedded in the binary, so keep
that notice with any distribution.
