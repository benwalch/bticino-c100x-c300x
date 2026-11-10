package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStreamingLiveAudioOnCallDefaultFailClosed covers the live_audio_on_call
// knob: unlike live_on_call (video, shipped as true in configs/config.yaml), it
// is a newer feature and must stay OFF if the field is omitted; the same
// fail-closed pattern as Audio.DuplexEnabled / Audio.ListenTapEnabled.
func TestStreamingLiveAudioOnCallDefaultFailClosed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("sip:\n  enabled: true\n"), 0644); err != nil {
		t.Fatalf("could not write the temporary config: %v", err)
	}

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Streaming.LiveAudioOnCall {
		t.Fatal("LiveAudioOnCall must be false if the streaming block is omitted (fail-closed)")
	}
}

func TestStreamingLiveAudioOnCallValueRespected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "streaming:\n  live_audio_on_call: true\n"
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatalf("could not write the temporary config: %v", err)
	}

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Streaming.LiveAudioOnCall {
		t.Fatal("LiveAudioOnCall must keep the YAML value true")
	}
}

// TestGetStreamingConfigDoesNotDefaultLiveAudioOnCall documents that, unlike
// LiveOnCall (video), GetStreamingConfig() must NOT default LiveAudioOnCall to
// true: it is a newer feature, fail-closed by design.
func TestGetStreamingConfigDoesNotDefaultLiveAudioOnCall(t *testing.T) {
	sc := GetStreamingConfig()
	if sc.LiveAudioOnCall {
		t.Fatal("GetStreamingConfig() must not default LiveAudioOnCall to true (fail-closed)")
	}
}
