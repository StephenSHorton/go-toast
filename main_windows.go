package toast

// Main just calls run on Windows, where the cards have a window thread of
// their own. On macOS a plain Go program needs it to give AppKit the main
// thread; call it from main to keep one main function for both.
func Main(run func()) { run() }
