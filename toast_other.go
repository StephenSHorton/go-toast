//go:build !windows && !(darwin && cgo)

package toast

// The cards are drawn on Windows and on macOS (with cgo); everywhere else
// (Linux, or a macOS build with CGO_ENABLED=0) these keep cross-compiles and
// tests building.

// Post is a no-op on this platform.
func Post(Note) {}

// Close is a no-op on this platform.
func Close(string) {}

// Show is a no-op on this platform.
func Show(string, string, bool) {}

// HoldClickThrough is a no-op on this platform.
func HoldClickThrough() (release func()) { return func() {} }

// Visible always reports false on this platform.
func Visible() bool { return false }

// Main just calls run on this platform; see the macOS Main (which a Wails or
// other Cocoa app must not call).
func Main(run func()) { run() }

func optionsChanged(Options) {}

func playWAV([]byte) {}
