package config

import "testing"

func TestPanelViewDefaults(t *testing.T) {
	// A minimal YAML with panelview enabled but no timeout/caller: LoadConfig
	// must apply the defaults (30s, "webrtc") and keep enabled/devaddr.
	yaml := "streaming:\n  panelview:\n    enabled: true\n    devaddr: \"11\"\n"
	path := writeTempConfig(t, yaml)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	pv := cfg.Streaming.PanelView
	if !pv.Enabled || pv.DEVADDR != "11" {
		t.Fatalf("enabled/devaddr not kept: %+v", pv)
	}
	if pv.TimeoutSeconds != 30 {
		t.Fatalf("TimeoutSeconds default = %d, want 30", pv.TimeoutSeconds)
	}
	if pv.CallerUser != "webrtc" {
		t.Fatalf("CallerUser default = %q, want webrtc", pv.CallerUser)
	}
}
