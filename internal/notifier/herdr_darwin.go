//go:build darwin

package notifier

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/777genius/agent-notifications/internal/logging"
)

// herdrPaneFocusTimeout bounds the herdr CLI query so a stalled server cannot
// hang notification dispatch on the hook path. A timeout is treated as
// "unknown" and the notification is delivered.
const herdrPaneFocusTimeout = 2 * time.Second

// Seams so tests can stub Herdr process discovery and the pane query.
var (
	herdrClientBundleIDs = detectHerdrClientBundleIDs
	herdrPaneFocusQuery  = herdrPaneFocused
)

// herdrTerminalHasFocus reports whether the Herdr pane running this session is
// the one the user is looking at.
//
// Herdr's per-pane `focused` flag is server-global: it stays true for the last
// focused pane even while the host terminal is in the background, so on its own
// it is not OS focus. We therefore also require that a terminal which hosts a
// Herdr client is the frontmost application. Any uncertainty - no resolvable
// host terminal, an lsappinfo failure, or a failed pane query - returns false so
// the caller still delivers the notification.
func herdrTerminalHasFocus() bool {
	paneID := herdrPaneID()
	if paneID == "" {
		return false
	}

	hosts := herdrClientBundleIDs()
	if len(hosts) == 0 {
		logging.Debug("Herdr focus unknown: could not identify the host terminal")
		return false
	}

	front, ok := frontmostBundleID()
	if !ok || front == "" {
		return false
	}
	if !bundleIDInList(front, hosts) {
		return false
	}

	focused, err := herdrPaneFocusQuery(paneID)
	if err != nil {
		logging.Debug("Herdr focus unknown: pane query failed: %v", err)
		return false
	}
	return focused
}

// herdrHostTerminalBundleID returns the bundle ID of the terminal hosting a
// Herdr client, or "" when it cannot be determined. It is baked into the
// click-to-focus command so a click raises the correct terminal.
func herdrHostTerminalBundleID() string {
	hosts := herdrClientBundleIDs()
	if len(hosts) == 0 {
		return ""
	}
	// Prefer the frontmost known host so a click raises the window the user is
	// actually looking at when several terminals host Herdr clients.
	if front, ok := frontmostBundleID(); ok && front != "" && bundleIDInList(front, hosts) {
		return front
	}
	return hosts[0]
}

func bundleIDInList(bundleID string, list []string) bool {
	bundleID = strings.TrimSpace(bundleID)
	for _, candidate := range list {
		if strings.EqualFold(strings.TrimSpace(candidate), bundleID) {
			return true
		}
	}
	return false
}

// herdrPaneFocused asks the Herdr server whether paneID is the focused pane.
func herdrPaneFocused(paneID string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), herdrPaneFocusTimeout)
	defer cancel()

	output, err := exec.CommandContext(ctx, herdrBinaryPath(), "pane", "current", "--current").Output()
	if err != nil {
		return false, err
	}
	return parseHerdrPaneFocused(output)
}

// detectHerdrClientBundleIDs scans running Herdr clients and reads the outer
// terminal each inherited. Herdr strips the terminal identity from pane
// processes, so the client process environment is the only reliable source of
// the host terminal's bundle ID.
func detectHerdrClientBundleIDs() []string {
	output, err := exec.Command("ps", "-Ao", "pid=,command=").Output()
	if err != nil {
		logging.Debug("Herdr host detection failed: ps: %v", err)
		return nil
	}

	seen := make(map[string]struct{})
	var bundles []string
	add := func(bundleID string) {
		bundleID = strings.TrimSpace(bundleID)
		if bundleID == "" {
			return
		}
		key := strings.ToLower(bundleID)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		bundles = append(bundles, bundleID)
	}

	for _, line := range strings.Split(string(output), "\n") {
		pid, command, ok := splitPSLine(line)
		if !ok || !isHerdrClientCommand(command) {
			continue
		}
		if bundleID := bundleIDFromProcessEnv(pid); bundleID != "" {
			add(bundleID)
		}
	}
	return bundles
}

// splitPSLine splits a `ps -Ao pid=,command=` line into its pid and command.
func splitPSLine(line string) (pid, command string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	space := strings.IndexAny(line, " \t")
	if space < 0 {
		return "", "", false
	}
	pid = strings.TrimSpace(line[:space])
	command = strings.TrimSpace(line[space+1:])
	if pid == "" || command == "" {
		return "", "", false
	}
	return pid, command, true
}

// isHerdrClientCommand reports whether a ps command line is a Herdr client
// rather than the background server (whose environment may be stale or absent).
func isHerdrClientCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	if filepath.Base(fields[0]) != "herdr" {
		return false
	}
	for _, arg := range fields[1:] {
		if arg == "server" {
			return false
		}
	}
	return true
}

func bundleIDFromProcessEnv(pid string) string {
	output, err := exec.Command("ps", "eww", "-p", pid).Output()
	if err != nil {
		return ""
	}
	return bundleIDFromPSEnv(string(output))
}

// bundleIDFromPSEnv extracts a terminal bundle ID from the environment block
// printed by `ps eww`. __CFBundleIdentifier wins when present; TERM_PROGRAM is
// mapped through the same table the generic terminal detection uses.
func bundleIDFromPSEnv(text string) string {
	fields := strings.Fields(text)
	for _, field := range fields {
		if value, ok := strings.CutPrefix(field, "__CFBundleIdentifier="); ok && value != "" {
			return value
		}
	}
	for _, field := range fields {
		if value, ok := strings.CutPrefix(field, "TERM_PROGRAM="); ok {
			if bundleID, ok := terminalBundleIDMap[value]; ok {
				return bundleID
			}
		}
	}
	return ""
}
