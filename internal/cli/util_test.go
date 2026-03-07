package cli

import "testing"

func TestBuildViewerOptions_DefaultsToEnabled(t *testing.T) {
	webEnabled = true
	noWeb = false
	openBrowser = true
	webPort = 0

	opts := buildViewerOptions("claude-test")
	if !opts.Enabled {
		t.Fatal("expected viewer to be enabled by default")
	}
	if !opts.OpenBrowser {
		t.Fatal("expected browser opening to remain enabled")
	}
	if opts.InitialSessionID != "claude-test" {
		t.Fatalf("unexpected initial session id: %q", opts.InitialSessionID)
	}
}

func TestBuildViewerOptions_NoWebDisablesViewer(t *testing.T) {
	webEnabled = true
	noWeb = true
	openBrowser = true
	webPort = 4317

	opts := buildViewerOptions("codex-test")
	if opts.Enabled {
		t.Fatal("expected --no-web to disable the viewer")
	}
	if !opts.OpenBrowser {
		t.Fatal("expected browser flag to remain unchanged")
	}
	if opts.Port != 4317 {
		t.Fatalf("unexpected port: %d", opts.Port)
	}
}
