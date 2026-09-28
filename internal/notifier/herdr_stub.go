//go:build !darwin

package notifier

// herdrHostTerminalBundleID is only resolvable on macOS. On other platforms the
// host terminal cannot be identified here, so callers fall back to their
// platform-specific click-to-focus path.
func herdrHostTerminalBundleID() string { return "" }
