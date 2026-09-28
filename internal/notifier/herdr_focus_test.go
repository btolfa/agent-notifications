package notifier

import (
	"strings"
	"testing"
)

func TestHerdrEnvironmentActive(t *testing.T) {
	tests := []struct {
		name   string
		env    string
		paneID string
		want   bool
	}{
		{name: "active in pane", env: "1", paneID: "w1:p1", want: true},
		{name: "env flag without pane id", env: "1", paneID: "", want: false},
		{name: "pane id without env flag", env: "", paneID: "w1:p1", want: false},
		{name: "unset", env: "", paneID: "", want: false},
		{name: "unexpected env value", env: "0", paneID: "w1:p1", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(herdrEnvVar, tt.env)
			t.Setenv(herdrPaneIDEnv, tt.paneID)
			if got := herdrEnvironmentActive(); got != tt.want {
				t.Errorf("herdrEnvironmentActive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseHerdrPaneFocused(t *testing.T) {
	focusedJSON := `{"id":"cli:pane:current","result":{"pane":{"pane_id":"w1:p1","focused":true},"type":"pane_current"}}`
	unfocusedJSON := `{"id":"cli:pane:current","result":{"pane":{"pane_id":"w1:p1","focused":false},"type":"pane_current"}}`

	tests := []struct {
		name    string
		output  string
		want    bool
		wantErr bool
	}{
		{name: "focused", output: focusedJSON, want: true},
		{name: "unfocused", output: unfocusedJSON, want: false},
		{name: "missing field defaults to unfocused", output: `{"result":{"pane":{}}}`, want: false},
		{name: "malformed", output: `{"result":`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseHerdrPaneFocused([]byte(tt.output))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseHerdrPaneFocused(%q) expected error", tt.output)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseHerdrPaneFocused(%q) error: %v", tt.output, err)
			}
			if got != tt.want {
				t.Errorf("parseHerdrPaneFocused(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}

func TestBuildHerdrFocusScript(t *testing.T) {
	tests := []struct {
		name         string
		hostBundleID string
		paneID       string
		herdrBin     string
		socketPath   string
		wantParts    []string
		wantEmpty    bool
	}{
		{
			name:         "raises terminal then focuses pane",
			hostBundleID: "com.mitchellh.ghostty",
			paneID:       "w1D:pT",
			herdrBin:     "/opt/homebrew/opt/herdr/bin/herdr",
			socketPath:   "/Users/me/.config/herdr/herdr.sock",
			wantParts: []string{
				"open -b 'com.mitchellh.ghostty'",
				"HERDR_SOCKET_PATH='/Users/me/.config/herdr/herdr.sock' '/opt/homebrew/opt/herdr/bin/herdr' agent focus 'w1D:pT'",
			},
		},
		{
			name:         "without host terminal still focuses pane",
			hostBundleID: "",
			paneID:       "w1:p1",
			herdrBin:     "herdr",
			socketPath:   "",
			wantParts:    []string{"'herdr' agent focus 'w1:p1'"},
		},
		{
			name:         "missing pane id is empty",
			hostBundleID: "com.mitchellh.ghostty",
			paneID:       "",
			herdrBin:     "herdr",
			wantEmpty:    true,
		},
		{
			name:         "missing binary is empty",
			hostBundleID: "com.mitchellh.ghostty",
			paneID:       "w1:p1",
			herdrBin:     "",
			wantEmpty:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildHerdrFocusScript(tt.hostBundleID, tt.paneID, tt.herdrBin, tt.socketPath)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("buildHerdrFocusScript() = %q, want empty", got)
				}
				return
			}
			for _, part := range tt.wantParts {
				if !strings.Contains(got, part) {
					t.Errorf("buildHerdrFocusScript() = %q, missing %q", got, part)
				}
			}
		})
	}
}
