package toast

import "testing"

// Every user32 entry point the window thread calls must resolve on each
// Windows architecture. GetWindowLongPtrW/SetWindowLongPtrW are not exported
// by 32-bit user32.dll, so binding to them panics on windows/386.
// Run with GOARCH=386 as well as amd64 to cover both.
func TestExStyleProcsResolve(t *testing.T) {
	for _, p := range []interface {
		Find() error
	}{procGetWindowLong, procSetWindowLong} {
		if err := p.Find(); err != nil {
			t.Errorf("user32 proc does not resolve: %v", err)
		}
	}
}
