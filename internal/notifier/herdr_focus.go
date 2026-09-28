package notifier

import (
	"encoding/json"
	"os"
	"strings"
)

// Herdr (https://herdr.dev) is a terminal workspace manager that runs each
// agent in a pane of its own. Unlike tmux, it does not forward the outer
// terminal's identity (TERM_PROGRAM / __CFBundleIdentifier) into panes, so the
// generic terminal-detection heuristics cannot identify the host here. These
// helpers detect a Herdr pane, read the pane's own focus flag, and build the
// click-to-focus command that raises the host terminal and lets Herdr switch to
// the originating pane itself.
const (
	herdrEnvVar        = "HERDR_ENV"
	herdrPaneIDEnv     = "HERDR_PANE_ID"
	herdrBinPathEnv    = "HERDR_BIN_PATH"
	herdrSocketPathEnv = "HERDR_SOCKET_PATH"
)

// herdrEnvironmentActive reports whether this process runs inside a Herdr pane.
// The pane id is required: HERDR_ENV alone is not enough to attribute focus.
func herdrEnvironmentActive() bool {
	return os.Getenv(herdrEnvVar) == "1" && herdrPaneID() != ""
}

// herdrPaneID returns the public pane id Herdr exports to this pane process.
func herdrPaneID() string {
	return strings.TrimSpace(os.Getenv(herdrPaneIDEnv))
}

// herdrBinaryPath returns the Herdr CLI to invoke. Herdr exports HERDR_BIN_PATH
// to panes; fall back to PATH for builds that predate it.
func herdrBinaryPath() string {
	if path := strings.TrimSpace(os.Getenv(herdrBinPathEnv)); path != "" {
		return path
	}
	return "herdr"
}

// herdrSocketPath returns the socket of the Herdr session this pane belongs to.
// Baking it into the click command keeps named sessions working, where the CLI
// would otherwise resolve the default session.
func herdrSocketPath() string {
	return strings.TrimSpace(os.Getenv(herdrSocketPathEnv))
}

// buildHerdrFocusScript builds the terminal-notifier -execute command for a
// Herdr pane: raise the host terminal (when known), then ask Herdr to focus the
// originating pane with `herdr agent focus <pane>`. Herdr performs the actual
// workspace/tab/pane switch, so agent-notifications never has to model Herdr's
// topology. Returns "" when the pane id is unavailable.
func buildHerdrFocusScript(hostBundleID, paneID, herdrBin, socketPath string) string {
	paneID = strings.TrimSpace(paneID)
	herdrBin = strings.TrimSpace(herdrBin)
	if paneID == "" || herdrBin == "" {
		return ""
	}

	envPrefix := ""
	if socketPath = strings.TrimSpace(socketPath); socketPath != "" {
		envPrefix = herdrSocketPathEnv + "=" + shellQuote(socketPath) + " "
	}
	focus := envPrefix + shellQuote(herdrBin) + " agent focus " + shellQuote(paneID)
	if host := strings.TrimSpace(hostBundleID); host != "" {
		// Raise the host terminal first so Herdr's switch is visible, then
		// select the pane. `;` keeps the pane focus even if activation fails.
		return "open -b " + shellQuote(host) + " ; " + focus
	}
	return focus
}

type herdrPaneCurrentResponse struct {
	Result struct {
		Pane struct {
			Focused bool `json:"focused"`
		} `json:"pane"`
	} `json:"result"`
}

// parseHerdrPaneFocused extracts the pane focus flag from `herdr pane current`
// (or `herdr pane get`) JSON output.
func parseHerdrPaneFocused(output []byte) (bool, error) {
	var resp herdrPaneCurrentResponse
	if err := json.Unmarshal(output, &resp); err != nil {
		return false, err
	}
	return resp.Result.Pane.Focused, nil
}
