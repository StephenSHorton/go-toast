package toast

// Main just calls run on Windows, where the cards have a window thread of
// their own. On macOS a plain Go program (no Wails, systray or other Cocoa
// event loop) needs it to give AppKit the main thread, so such a program can
// call it from main to keep one main function for both. A Wails or other
// Cocoa app must not: see the macOS Main.
func Main(run func()) { run() }
