package toast

import (
	"math"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.AppName != "Toast" || c.Palette != Dark || c.CornerRadius != 0 || c.IgnoreInput != nil || !c.CaptureExclude {
		t.Fatalf("DefaultConfig = %+v", c)
	}
	n := c.normalized()
	if n.ClassPrefix != "Toast" || n.hostClassName() != "ToastHost" || n.logPrefix() != "toast: " {
		t.Errorf("default names: prefix %q host %q log %q", n.ClassPrefix, n.hostClassName(), n.logPrefix())
	}
	if got := CurrentConfig(); got.AppName != "Toast" || !got.CaptureExclude || got.Palette != Dark.resolved() {
		t.Errorf("without Configure the defaults should be in effect: %+v", got)
	}
}

func TestToruNames(t *testing.T) {
	c := Config{AppName: "Toru"}.normalized()
	if c.cardClassName() != "ToruToast" || c.hostClassName() != "ToruToastHost" {
		t.Errorf("classes %q, %q; want Toru's ToruToast, ToruToastHost", c.cardClassName(), c.hostClassName())
	}
	if c.logPrefix() != "toru: toast: " {
		t.Errorf("log prefix %q", c.logPrefix())
	}
	if p := (Config{AppName: "My App 2!", ClassPrefix: "Custom"}).normalized(); p.ClassPrefix != "Custom" {
		t.Errorf("explicit ClassPrefix overridden: %q", p.ClassPrefix)
	}
	if p := classPrefixFor("My App 2!"); p != "MyApp2Toast" {
		t.Errorf("classPrefixFor = %q", p)
	}
	if p := (Config{AppName: "  "}).normalized(); p.AppName != "Toast" || p.ClassPrefix != "Toast" {
		t.Errorf("blank AppName = %+v", p)
	}
}

func TestConfigNormalized(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{{-3, 0}, {math.NaN(), 0}, {10, 10}, {100, maxCornerRadius}} {
		if got := (Config{CornerRadius: tc.in}).normalized().CornerRadius; got != tc.want {
			t.Errorf("CornerRadius %v -> %v, want %v", tc.in, got, tc.want)
		}
	}
	if (Config{}).normalized().Palette != Dark {
		t.Error("zero palette should be Dark")
	}
	ignored := false
	Configure(Config{AppName: "X", IgnoreInput: func(e uintptr) bool { ignored = e == 7; return ignored }})
	defer Configure(DefaultConfig())
	if c := CurrentConfig(); c.AppName != "X" || c.CaptureExclude || !c.IgnoreInput(7) || !ignored {
		t.Errorf("Configure didn't take: %+v", c)
	}
}

func TestColor(t *testing.T) {
	if !Color(0).IsZero() || RGB(0, 0, 0).IsZero() {
		t.Error("black must be distinct from unset")
	}
	if Hex("#4cabfd") != RGB(0x4c, 0xab, 0xfd) || Hex("4CABFD") != Hex("#4cabfd") {
		t.Error("Hex")
	}
	// COLORREF is 0x00BBGGRR; getting it backwards swaps the palette.
	if got := Hex("#4cabfd").colorref(); got != 0x00fdab4c {
		t.Errorf("colorref = %#08x, want 0x00fdab4c", got)
	}
	if Hex("#0f172a").String() != "#0f172a" || Color(0).String() != "unset" {
		t.Error("String")
	}
	for _, bad := range []string{"", "#fff", "#gggggg", "#1234567"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Hex(%q) should panic", bad)
				}
			}()
			Hex(bad)
		}()
	}
}

// TestDarkIsToru pins every color of Toru's look, so moving Toru onto the
// module changes no pixel.
func TestDarkIsToru(t *testing.T) {
	d := Dark.resolved()
	for name, tc := range map[string]struct{ got, want Color }{
		"card":       {d.Background, RGB(0x11, 0x12, 0x13)},
		"border":     {d.Border, RGB(0x2c, 0x2e, 0x31)},
		"title":      {d.Foreground, RGB(0xfa, 0xfa, 0xfa)},
		"body":       {d.Muted, RGB(0x9e, 0xa1, 0xa7)},
		"faint":      {d.Faint, RGB(0x5c, 0x5f, 0x66)},
		"hot":        {d.Hover, RGB(0x26, 0x29, 0x2e)},
		"button":     {d.Button, RGB(0x20, 0x22, 0x25)},
		"primary":    {d.Primary, RGB(0x4c, 0xab, 0xfd)},
		"primaryHot": {d.PrimaryHover, RGB(0x6f, 0xbc, 0xff)},
		"onPrimary":  {d.OnPrimary, RGB(0x06, 0x0a, 0x13)},
		"info":       {d.Accent.For(Info), RGB(0x4c, 0xab, 0xfd)},
		"success":    {d.Accent.For(Success), RGB(0x4c, 0xab, 0xfd)}, // Toru's success is the blue, not a green
		"warn":       {d.Accent.For(Warn), RGB(0xf5, 0xae, 0x39)},
		"error":      {d.Accent.For(Error), RGB(0xea, 0x3c, 0x3f)},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", name, tc.got, tc.want)
		}
	}
	if d != Dark {
		t.Error("Dark should need no derivation")
	}
}

func TestLightPreset(t *testing.T) {
	l := Light.resolved()
	for name, tc := range map[string]struct{ got, want Color }{
		"background": {l.Background, Hex("#ffffff")},
		"foreground": {l.Foreground, Hex("#0f172a")},
		"muted":      {l.Muted, Hex("#64748b")},
		"border":     {l.Border, RGB(203, 213, 225)},
		"info":       {l.Accent.Info, Hex("#2563eb")},
		"success":    {l.Accent.Success, Hex("#15803d")},
		"warn":       {l.Accent.Warn, Hex("#c2620a")},
		"error":      {l.Accent.Error, Hex("#ef4444")},
		"primary":    {l.Primary, Hex("#2563eb")},
		"onPrimary":  {l.OnPrimary, Hex("#ffffff")},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", name, tc.got, tc.want)
		}
	}
	for name, c := range map[string]Color{"faint": l.Faint, "hover": l.Hover, "button": l.Button, "primaryHover": l.PrimaryHover} {
		if c.IsZero() || c == l.Background {
			t.Errorf("%s not derived: %v", name, c)
		}
	}
	// Derived fills sit near the card; text on the primary button is legible.
	if contrast(l.Hover, l.Background) >= contrast(l.Foreground, l.Background) || contrast(l.OnPrimary, l.Primary) < 4.5 {
		t.Error("derived colors out of order")
	}
}

func TestPartialPaletteFillsFromDark(t *testing.T) {
	p := Palette{Background: RGB(0, 0, 0), Accent: Accents{Error: Hex("#ff0000")}}.resolved()
	if p.Background != RGB(0, 0, 0) || p.Foreground != Dark.Foreground || p.Accent.Error != Hex("#ff0000") || p.Accent.Warn != Dark.Accent.Warn {
		t.Errorf("partial palette = %+v", p)
	}
	if p.Primary != Dark.Accent.Info || p.OnPrimary.IsZero() {
		t.Errorf("derived = %v / %v", p.Primary, p.OnPrimary)
	}
}
