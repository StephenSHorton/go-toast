package toast

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

// Color is an sRGB color made with RGB or Hex. The zero Color is "unset" (a
// Palette fills it in), so black is RGB(0, 0, 0), not Color(0).
type Color uint32

const colorSet = 0xFF000000

// RGB returns the color r, g, b.
func RGB(r, g, b uint8) Color {
	return Color(colorSet | uint32(r)<<16 | uint32(g)<<8 | uint32(b))
}

// Hex parses "#rrggbb" or "rrggbb". It panics on anything else; it is meant
// for palette literals.
func Hex(s string) Color {
	t := strings.TrimPrefix(s, "#")
	v, err := strconv.ParseUint(t, 16, 32)
	if len(t) != 6 || err != nil {
		panic(fmt.Sprintf("toast: bad hex color %q", s))
	}
	return Color(colorSet | uint32(v))
}

// IsZero reports whether c is unset.
func (c Color) IsZero() bool { return c&colorSet == 0 }

// RGB returns c's channels.
func (c Color) RGB() (r, g, b uint8) { return uint8(c >> 16), uint8(c >> 8), uint8(c) }

// String is "#rrggbb", or "unset".
func (c Color) String() string {
	if c.IsZero() {
		return "unset"
	}
	return fmt.Sprintf("#%06x", uint32(c)&0xFFFFFF)
}

// colorref is c as a Win32 COLORREF (0x00BBGGRR).
func (c Color) colorref() uint32 {
	r, g, b := c.RGB()
	return uint32(r) | uint32(g)<<8 | uint32(b)<<16
}

// mix returns a moved fraction t of the way toward b.
func mix(a, b Color, t float64) Color {
	ar, ag, ab := a.RGB()
	br, bg, bb := b.RGB()
	m := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return RGB(m(ar, br), m(ag, bg), m(ab, bb))
}

// luminance is c's WCAG relative luminance.
func (c Color) luminance() float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	r, g, b := c.RGB()
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrast is the WCAG contrast ratio between a and b (1..21).
func contrast(a, b Color) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Accents are the per-Kind colors of a card's stripe and icon.
type Accents struct {
	Info, Success, Warn, Error Color
}

// For returns k's accent.
func (a Accents) For(k Kind) Color {
	switch k {
	case Success:
		return a.Success
	case Warn:
		return a.Warn
	case Error:
		return a.Error
	default:
		return a.Info
	}
}

// Palette is the cards' colors. Background, Foreground, Muted, Border and
// Accent are the ones to set; the rest are derived from them when left unset.
// Any of the five left unset comes from Dark, and a wholly zero Palette is Dark.
type Palette struct {
	Background Color // the card
	Foreground Color // the title, secondary button text, the hovered x
	Muted      Color // the body, the x on a hovered card
	Border     Color // the card's and secondary buttons' 1px border
	Accent     Accents

	Faint        Color // the x on a card the cursor isn't over; default Muted 40% toward Background
	Hover        Color // hover fill (the x, secondary buttons); default Background 8% toward Foreground
	Button       Color // secondary button fill; default Background 5% toward Foreground
	Primary      Color // the first button; default Accent.Info
	PrimaryHover Color // default Primary 15% toward Foreground
	OnPrimary    Color // text on the first button; default whichever of Background/Foreground contrasts more with Primary
}

// Dark is Toru's palette (its frontend's oklch tokens converted to sRGB):
// near-black cards with a blue accent. Info and Success share the blue; Warn
// is amber and Error red.
var Dark = Palette{
	Background:   Hex("#111213"),
	Foreground:   Hex("#fafafa"),
	Muted:        Hex("#9ea1a7"),
	Border:       Hex("#2c2e31"),
	Accent:       Accents{Info: Hex("#4cabfd"), Success: Hex("#4cabfd"), Warn: Hex("#f5ae39"), Error: Hex("#ea3c3f")},
	Faint:        Hex("#5c5f66"),
	Hover:        Hex("#26292e"),
	Button:       Hex("#202225"),
	Primary:      Hex("#4cabfd"),
	PrimaryHover: Hex("#6fbcff"),
	OnPrimary:    Hex("#060a13"),
}

// Light is white cards with slate text (Irori's yardline tokens). Pair it
// with a CornerRadius of about 10.
var Light = Palette{
	Background: Hex("#ffffff"),
	Foreground: Hex("#0f172a"),
	Muted:      Hex("#64748b"),
	Border:     RGB(203, 213, 225),
	Accent:     Accents{Info: Hex("#2563eb"), Success: Hex("#15803d"), Warn: Hex("#c2620a"), Error: Hex("#ef4444")},
}

// resolved fills every unset color.
func (p Palette) resolved() Palette {
	if p == (Palette{}) {
		p = Dark
	}
	fill := func(c *Color, def Color) {
		if c.IsZero() {
			*c = def
		}
	}
	fill(&p.Background, Dark.Background)
	fill(&p.Foreground, Dark.Foreground)
	fill(&p.Muted, Dark.Muted)
	fill(&p.Border, Dark.Border)
	fill(&p.Accent.Info, Dark.Accent.Info)
	fill(&p.Accent.Success, Dark.Accent.Success)
	fill(&p.Accent.Warn, Dark.Accent.Warn)
	fill(&p.Accent.Error, Dark.Accent.Error)
	fill(&p.Faint, mix(p.Muted, p.Background, 0.4))
	fill(&p.Hover, mix(p.Background, p.Foreground, 0.08))
	fill(&p.Button, mix(p.Background, p.Foreground, 0.05))
	fill(&p.Primary, p.Accent.Info)
	fill(&p.PrimaryHover, mix(p.Primary, p.Foreground, 0.15))
	on := p.Background
	if contrast(p.Foreground, p.Primary) > contrast(p.Background, p.Primary) {
		on = p.Foreground
	}
	fill(&p.OnPrimary, on)
	return p
}

// Config is the app-level setup, set once at startup with Configure before the
// first Post. Start from DefaultConfig: its CaptureExclude is true, which a
// zero Config literal would turn off.
type Config struct {
	// AppName is the windows' title and the log prefix ("toru: toast: ...").
	AppName string
	// ClassPrefix names the window classes: the cards are ClassPrefix, the
	// hidden host window ClassPrefix+"Host". Empty derives it from AppName
	// (its letters and digits plus "Toast": "Toru" gives "ToruToast").
	ClassPrefix string
	// Palette is the cards' colors; the zero Palette is Dark.
	Palette Palette
	// CornerRadius rounds the cards' corners, in dip. 0 is square (Toru);
	// about 10 matches Irori's cards.
	CornerRadius float64
	// IgnoreInput, when set, is asked about every mouse message a card gets,
	// with the event's GetMessageExtraInfo value; true drops it (no hover, no
	// press). An app that injects input of its own (an auto clicker) tags it
	// and ignores it here so it never presses a card. Nil ignores nothing.
	IgnoreInput func(extraInfo uintptr) bool
	// CaptureExclude keeps the cards out of screenshots and screen recordings
	// (WDA_EXCLUDEFROMCAPTURE, Windows 10 2004+) while they stay on screen.
	CaptureExclude bool
}

// DefaultConfig is AppName "Toast", the Dark palette, square corners, and
// cards excluded from capture.
func DefaultConfig() Config {
	return Config{AppName: "Toast", Palette: Dark, CaptureExclude: true}
}

const maxCornerRadius = 24

// normalized fills defaults and clamps out-of-range values.
func (c Config) normalized() Config {
	c.AppName = strings.TrimSpace(c.AppName)
	if c.AppName == "" {
		c.AppName = DefaultConfig().AppName
	}
	if c.ClassPrefix == "" {
		c.ClassPrefix = classPrefixFor(c.AppName)
	}
	c.Palette = c.Palette.resolved()
	switch {
	case math.IsNaN(c.CornerRadius) || c.CornerRadius < 0:
		c.CornerRadius = 0
	case c.CornerRadius > maxCornerRadius:
		c.CornerRadius = maxCornerRadius
	}
	return c
}

// classPrefixFor derives a window class prefix from an app name.
func classPrefixFor(app string) string {
	var b strings.Builder
	for _, r := range app {
		if r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if !strings.HasSuffix(s, "Toast") {
		s += "Toast"
	}
	return s
}

// hostClassName and cardClassName are the window classes.
func (c Config) hostClassName() string { return c.ClassPrefix + "Host" }
func (c Config) cardClassName() string { return c.ClassPrefix }

// logPrefix is "<app>: toast: ", lowercased.
func (c Config) logPrefix() string {
	a := strings.ToLower(c.AppName)
	if a == "toast" {
		return "toast: "
	}
	return a + ": toast: "
}

var (
	cfgMu sync.Mutex
	cfg   = DefaultConfig().normalized()
)

// Configure sets the app-level setup. Call it once at startup, before the
// first Post: the window classes and title are fixed when the first card
// opens, and later calls only affect cards created after them.
func Configure(c Config) {
	c = c.normalized()
	cfgMu.Lock()
	cfg = c
	cfgMu.Unlock()
}

// CurrentConfig returns the setup in effect, with defaults filled in.
func CurrentConfig() Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return cfg
}
