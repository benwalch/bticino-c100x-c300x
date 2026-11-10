package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnswererConfigDefaultsFailClosed(t *testing.T) {
	// A config without an answerer block => Enabled=false (fail-closed).
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte("sip:\n  enabled: true\n  domain: \"YOUR_GATEWAY_ID.bs.iotleg.com\"\n"), 0o644)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.SIP.Answerer.Enabled {
		t.Fatal("answerer must be fail-closed (Enabled=false) by default")
	}
}

func TestAnswererConfigParsed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte(
		"sip:\n"+
			"  enabled: true\n"+
			"  domain: \"YOUR_GATEWAY_ID.bs.iotleg.com\"\n"+
			"  answerer:\n"+
			"    enabled: true\n"+
			"    user: \"bridge\"\n"+
			"    audio_port: 10100\n"+
			"    video_port: 10102\n"+
			"    max_call_seconds: 120\n"+
			"    talk_enabled: false\n"), 0o644)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	a := cfg.SIP.Answerer
	if !a.Enabled || a.User != "bridge" || a.AudioPort != 10100 || a.VideoPort != 10102 || a.MaxCallSeconds != 120 {
		t.Fatalf("answerer parsed wrongly: %+v", a)
	}
}

func TestAnswererTalkKeepalive(t *testing.T) {
	// Fail-closed: without the key => false (current behaviour). With talk_keepalive: true => true.
	dir := t.TempDir()
	p := filepath.Join(dir, "off.yaml")
	os.WriteFile(p, []byte("sip:\n  enabled: true\n  domain: \"d\"\n  answerer:\n    enabled: true\n"), 0o644)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.SIP.Answerer.TalkKeepalive {
		t.Fatal("talk_keepalive must be false by default (fail-closed)")
	}
	p2 := filepath.Join(dir, "on.yaml")
	os.WriteFile(p2, []byte("sip:\n  enabled: true\n  domain: \"d\"\n  answerer:\n    enabled: true\n    talk_keepalive: true\n"), 0o644)
	cfg2, err := LoadConfig(p2)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg2.SIP.Answerer.TalkKeepalive {
		t.Fatal("talk_keepalive: true was not parsed")
	}
}

func TestAnswererRingTimeoutDefault70(t *testing.T) {
	// No ring_timeout_seconds in the YAML => 70 s (the value the official app 1.17.3 uses
	// for the incoming call). With the default in code, the device needs no config change.
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte("sip:\n  enabled: true\n  domain: \"d\"\n  answerer:\n    enabled: true\n"), 0o644)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.SIP.Answerer.RingTimeoutSeconds; got != 70 {
		t.Fatalf("RingTimeoutSeconds=%d, expected 70 by default", got)
	}
}
