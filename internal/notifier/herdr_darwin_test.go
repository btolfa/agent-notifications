//go:build darwin

package notifier

import (
	"errors"
	"strings"
	"testing"
)

func TestSplitPSLine(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantPID     string
		wantCommand string
		wantOK      bool
	}{
		{name: "typical", line: "  5380 herdr", wantPID: "5380", wantCommand: "herdr", wantOK: true},
		{name: "with args", line: "1930 herdr --remote host", wantPID: "1930", wantCommand: "herdr --remote host", wantOK: true},
		{name: "blank", line: "   ", wantOK: false},
		{name: "pid only", line: "5380", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pid, command, ok := splitPSLine(tt.line)
			if ok != tt.wantOK || pid != tt.wantPID || command != tt.wantCommand {
				t.Errorf("splitPSLine(%q) = (%q, %q, %v), want (%q, %q, %v)", tt.line, pid, command, ok, tt.wantPID, tt.wantCommand, tt.wantOK)
			}
		})
	}
}

func TestIsHerdrClientCommand(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"herdr", true},
		{"herdr client", true},
		{"herdr --remote host", true},
		{"/opt/homebrew/bin/herdr", true},
		{"/opt/homebrew/opt/herdr/bin/herdr server", false},
		{"herdr server", false},
		{"bash", false},
		{"node /tmp/herdr", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isHerdrClientCommand(tt.command); got != tt.want {
			t.Errorf("isHerdrClientCommand(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

func TestBundleIDFromPSEnv(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "cfbundleidentifier wins",
			text: "herdr TERM_PROGRAM=ghostty __CFBundleIdentifier=com.mitchellh.ghostty",
			want: "com.mitchellh.ghostty",
		},
		{
			name: "term_program fallback",
			text: "herdr TERM_PROGRAM=WezTerm",
			want: "com.github.wez.wezterm",
		},
		{
			name: "unmapped term_program",
			text: "herdr TERM_PROGRAM=some-unknown-terminal",
			want: "",
		},
		{
			name: "no terminal markers",
			text: "herdr server",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bundleIDFromPSEnv(tt.text); got != tt.want {
				t.Errorf("bundleIDFromPSEnv(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// stubHerdrFocus installs deterministic focus seams for the duration of a test.
func stubHerdrFocus(t *testing.T, hosts []string, front string, frontOK bool, focused bool, queryErr error) {
	t.Helper()

	restoreHosts := herdrClientBundleIDs
	restoreQuery := herdrPaneFocusQuery
	restoreFront := frontmostBundleID
	t.Cleanup(func() {
		herdrClientBundleIDs = restoreHosts
		herdrPaneFocusQuery = restoreQuery
		frontmostBundleID = restoreFront
	})

	herdrClientBundleIDs = func() []string { return hosts }
	herdrPaneFocusQuery = func(string) (bool, error) { return focused, queryErr }
	frontmostBundleID = func() (string, bool) { return front, frontOK }
}

func TestHerdrTerminalHasFocus(t *testing.T) {
	t.Setenv(herdrEnvVar, "1")
	t.Setenv(herdrPaneIDEnv, "w1:p1")
	t.Setenv(herdrBinPathEnv, "/opt/homebrew/opt/herdr/bin/herdr")

	t.Run("focused when host terminal frontmost and pane focused", func(t *testing.T) {
		stubHerdrFocus(t, []string{"com.mitchellh.ghostty"}, "com.mitchellh.ghostty", true, true, nil)
		if !herdrTerminalHasFocus() {
			t.Error("expected focus when the host terminal is frontmost and the pane is focused")
		}
	})

	t.Run("case-insensitive bundle match", func(t *testing.T) {
		stubHerdrFocus(t, []string{"com.mitchellh.ghostty"}, "COM.MITCHELLH.GHOSTTY", true, true, nil)
		if !herdrTerminalHasFocus() {
			t.Error("expected a case-insensitive host bundle match")
		}
	})

	t.Run("not focused when another app is frontmost", func(t *testing.T) {
		stubHerdrFocus(t, []string{"com.mitchellh.ghostty"}, "com.google.Chrome", true, true, nil)
		if herdrTerminalHasFocus() {
			t.Error("expected no focus when a different app is frontmost")
		}
	})

	t.Run("not focused when the pane is not the focused pane", func(t *testing.T) {
		stubHerdrFocus(t, []string{"com.mitchellh.ghostty"}, "com.mitchellh.ghostty", true, false, nil)
		if herdrTerminalHasFocus() {
			t.Error("expected no focus when the pane itself is not focused")
		}
	})

	t.Run("unknown host terminal notifies", func(t *testing.T) {
		stubHerdrFocus(t, nil, "com.mitchellh.ghostty", true, true, nil)
		if herdrTerminalHasFocus() {
			t.Error("expected no focus (deliver) when the host terminal cannot be identified")
		}
	})

	t.Run("frontmost query failure notifies", func(t *testing.T) {
		stubHerdrFocus(t, []string{"com.mitchellh.ghostty"}, "", false, true, nil)
		if herdrTerminalHasFocus() {
			t.Error("expected no focus (deliver) when the frontmost query fails")
		}
	})

	t.Run("pane query failure notifies", func(t *testing.T) {
		stubHerdrFocus(t, []string{"com.mitchellh.ghostty"}, "com.mitchellh.ghostty", true, true, errors.New("timeout"))
		if herdrTerminalHasFocus() {
			t.Error("expected no focus (deliver) when the pane query fails")
		}
	})

	t.Run("no pane id notifies", func(t *testing.T) {
		t.Setenv(herdrPaneIDEnv, "")
		stubHerdrFocus(t, []string{"com.mitchellh.ghostty"}, "com.mitchellh.ghostty", true, true, nil)
		if herdrTerminalHasFocus() {
			t.Error("expected no focus (deliver) without a pane id")
		}
	})
}

func TestHerdrHostTerminalBundleID(t *testing.T) {
	t.Run("prefers the frontmost known host", func(t *testing.T) {
		stubHerdrFocus(t, []string{"com.mitchellh.ghostty", "net.kovidgoyal.kitty"}, "net.kovidgoyal.kitty", true, false, nil)
		if got := herdrHostTerminalBundleID(); got != "net.kovidgoyal.kitty" {
			t.Errorf("herdrHostTerminalBundleID() = %q, want the frontmost host", got)
		}
	})

	t.Run("falls back to the first host", func(t *testing.T) {
		stubHerdrFocus(t, []string{"com.mitchellh.ghostty"}, "com.google.Chrome", true, false, nil)
		if got := herdrHostTerminalBundleID(); got != "com.mitchellh.ghostty" {
			t.Errorf("herdrHostTerminalBundleID() = %q, want the first host", got)
		}
	})

	t.Run("empty without hosts", func(t *testing.T) {
		stubHerdrFocus(t, nil, "", false, false, nil)
		if got := herdrHostTerminalBundleID(); got != "" {
			t.Errorf("herdrHostTerminalBundleID() = %q, want empty", got)
		}
	})
}

func TestBuildFocusScript_Herdr(t *testing.T) {
	t.Setenv(herdrEnvVar, "1")
	t.Setenv(herdrPaneIDEnv, "w1D:pT")
	t.Setenv(herdrBinPathEnv, "/opt/homebrew/opt/herdr/bin/herdr")
	t.Setenv(herdrSocketPathEnv, "/Users/me/.config/herdr/herdr.sock")
	stubHerdrFocus(t, []string{"com.mitchellh.ghostty"}, "com.mitchellh.ghostty", true, true, nil)

	script := buildFocusScript("com.apple.Terminal", "/home/user/my-project")
	if !strings.Contains(script, "open -b 'com.mitchellh.ghostty'") {
		t.Errorf("herdr focus script should raise the host terminal, got: %s", script)
	}
	if !strings.Contains(script, "HERDR_SOCKET_PATH='/Users/me/.config/herdr/herdr.sock' '/opt/homebrew/opt/herdr/bin/herdr' agent focus 'w1D:pT'") {
		t.Errorf("herdr focus script should target the session's pane, got: %s", script)
	}
	if strings.Contains(script, "focus-window") {
		t.Errorf("herdr focus script should not fall back to focus-window, got: %s", script)
	}
}

func TestBuildTerminalNotifierArgs_HerdrExecutesHerdrFocus(t *testing.T) {
	t.Setenv(herdrEnvVar, "1")
	t.Setenv(herdrPaneIDEnv, "w1D:pT")
	t.Setenv(herdrBinPathEnv, "/opt/homebrew/opt/herdr/bin/herdr")
	t.Setenv(herdrSocketPathEnv, "/Users/me/.config/herdr/herdr.sock")
	stubHerdrFocus(t, []string{"com.mitchellh.ghostty"}, "com.mitchellh.ghostty", true, true, nil)

	args := buildTerminalNotifierArgs("Title", "Message", "com.apple.Terminal", "/home/user/my-project", true)
	execVal := getArgValue(args, "-execute")
	if !strings.Contains(execVal, "agent focus 'w1D:pT'") {
		t.Errorf("-execute should focus the herdr pane, got: %q", execVal)
	}
	if !strings.Contains(execVal, "open -b 'com.mitchellh.ghostty'") {
		t.Errorf("-execute should raise the host terminal, got: %q", execVal)
	}
	if getArgValue(args, "-activate") != "" {
		t.Errorf("herdr click should not fall back to -activate, got: %v", args)
	}
}

// TestHerdrLiveHostDetection exercises the real process scan and herdr CLI when
// the test runner itself runs inside a Herdr pane. It is skipped elsewhere.
func TestHerdrLiveHostDetection(t *testing.T) {
	if hostHerdrEnv != "1" || hostHerdrPaneID == "" {
		t.Skip("host process did not run inside a Herdr pane")
	}
	t.Setenv(herdrEnvVar, hostHerdrEnv)
	t.Setenv(herdrPaneIDEnv, hostHerdrPaneID)
	t.Setenv(herdrBinPathEnv, hostHerdrBinPath)
	t.Setenv(herdrSocketPathEnv, hostHerdrSocketPath)

	hosts := detectHerdrClientBundleIDs()
	if len(hosts) == 0 {
		t.Fatal("no Herdr client host terminal detected on a live Herdr host")
	}
	t.Logf("detected Herdr host terminals: %v", hosts)

	focused, err := herdrPaneFocused(hostHerdrPaneID)
	if err != nil {
		t.Fatalf("herdr pane query failed: %v", err)
	}
	t.Logf("herdr pane %s focused=%v", hostHerdrPaneID, focused)

	script := buildFocusScript("com.apple.Terminal", "/some/project")
	t.Logf("herdr click script: %s", script)
	if !strings.Contains(script, "agent focus '"+hostHerdrPaneID+"'") {
		t.Errorf("live herdr click script should focus the pane, got: %s", script)
	}
	if !strings.Contains(script, "open -b '") {
		t.Errorf("live herdr click script should raise the host terminal, got: %s", script)
	}
}
