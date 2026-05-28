//go:build !windows

package tray

// Non-Windows builds (linux air-gap servers, macOS) have no system tray.
// Stubs let main.go call tray.Run/Stop unconditionally without build tags.

// Run is a no-op outside Windows. Air-gapped Linux servers are headless and
// have no tray; a real implementation there would need GTK/appindicator,
// which would break the project's zero-dependency single-binary property.
func Run(port int, openCb, quitCb func()) error { return nil }

// Stop is a no-op outside Windows.
func Stop() {}

// Active is always false outside Windows.
func Active() bool { return false }
