package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAudioDuplexDefaults(t *testing.T) {
	// No audio block: fail-closed and default sink.
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("sip:\n  enabled: true\n"), 0644); err != nil {
		t.Fatalf("could not write the temporary config: %v", err)
	}

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Audio.DuplexEnabled {
		t.Fatal("DuplexEnabled must be false if the block is omitted (fail-closed)")
	}
	if cfg.Audio.DuplexSinkPath == "" {
		t.Fatal("DuplexSinkPath must have a non-empty default")
	}
	if cfg.Audio.ListenSinkPath == "" {
		t.Fatal("ListenSinkPath must have a non-empty default")
	}
	if cfg.Audio.ListenSinkPath == cfg.Audio.DuplexSinkPath {
		t.Fatal("ListenSinkPath must DIFFER from DuplexSinkPath by default (dedicated sink)")
	}
}

func TestAudioDuplexValuesRespected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "audio:\n  duplex_enabled: true\n  duplex_sink_path: /tmp/x.wav\n"
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatalf("could not write the temporary config: %v", err)
	}

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Audio.DuplexEnabled || cfg.Audio.DuplexSinkPath != "/tmp/x.wav" {
		t.Fatalf("values not kept: %+v", cfg.Audio)
	}
}

func TestAudioListenTapDefaultFailClosed(t *testing.T) {
	// No audio block: ListenTapEnabled fail-closed (like DuplexEnabled).
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte("sip:\n  enabled: true\n"), 0644); err != nil {
		t.Fatalf("could not write the temporary config: %v", err)
	}

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Audio.ListenTapEnabled {
		t.Fatal("ListenTapEnabled must be false if the block is omitted (fail-closed)")
	}
}

func TestAudioListenTapValueRespected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "audio:\n  listen_tap_enabled: true\n  duplex_sink_path: /tmp/x.wav\n  listen_sink_path: /tmp/listen.wav\n"
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatalf("could not write the temporary config: %v", err)
	}

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Audio.ListenTapEnabled {
		t.Fatal("ListenTapEnabled must keep the YAML value true")
	}
	if cfg.Audio.DuplexSinkPath != "/tmp/x.wav" {
		t.Fatalf("DuplexSinkPath not kept: %+v", cfg.Audio)
	}
	// Dedicated sink of the passive ear: it does NOT share a file with
	// DuplexSinkPath.
	if cfg.Audio.ListenSinkPath != "/tmp/listen.wav" {
		t.Fatalf("ListenSinkPath not kept: %+v", cfg.Audio)
	}
}

func TestAudioListenSinkPathDefault(t *testing.T) {
	// No explicit listen_sink_path: auto-default (not fail-closed, like
	// DuplexSinkPath) to a WAV other than the duplex leg's.
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	body := "audio:\n  listen_tap_enabled: true\n"
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatalf("could not write the temporary config: %v", err)
	}

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	const want = "/home/bticino/cfg/extra/recordings/door_audio_listen.wav"
	if cfg.Audio.ListenSinkPath != want {
		t.Fatalf("expected default ListenSinkPath=%q, got %q", want, cfg.Audio.ListenSinkPath)
	}
}
