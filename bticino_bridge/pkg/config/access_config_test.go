package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatalf("could not write the temporary config: %v", err)
	}
	return p
}

func TestAccessDefaultsApplied(t *testing.T) {
	// No access block: durations take their defaults; Enabled stays false.
	cfg, err := LoadConfig(writeTempConfig(t, "bridge:\n  name: x\n"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Access.CorrelationWindow != 30*time.Second {
		t.Errorf("CorrelationWindow = %v, want 30s", cfg.Access.CorrelationWindow)
	}
	if cfg.Access.MaxHoldWait != 8*time.Second {
		t.Errorf("MaxHoldWait = %v, want 8s", cfg.Access.MaxHoldWait)
	}
	if cfg.Access.DurationVisitorMax != 3500*time.Millisecond {
		t.Errorf("DurationVisitorMax = %v, want 3500ms", cfg.Access.DurationVisitorMax)
	}
	if cfg.Access.Enabled {
		t.Errorf("Enabled = true without an access block; want false (fail-closed)")
	}
}

func TestAccessValuesRespected(t *testing.T) {
	body := "access:\n  enabled: true\n  correlation_window: 15s\n  max_hold_wait: 5s\n  duration_visitor_max: 2s\n"
	cfg, err := LoadConfig(writeTempConfig(t, body))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Access.Enabled {
		t.Errorf("Enabled = false; want true")
	}
	if cfg.Access.CorrelationWindow != 15*time.Second {
		t.Errorf("CorrelationWindow = %v, want 15s", cfg.Access.CorrelationWindow)
	}
	if cfg.Access.DurationVisitorMax != 2*time.Second {
		t.Errorf("DurationVisitorMax = %v, want 2s", cfg.Access.DurationVisitorMax)
	}
}
