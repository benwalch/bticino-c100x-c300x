package config

import (
	"os"
	"path/filepath"
	"testing"
)

// network.firewall duplicated privacy and was dropped; old files must still load.
func TestLoadIgnoresDroppedFirewallSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "network:\n  ntp:\n    server: pool.ntp.org\n  firewall:\n    block_telemetry: true\n    block_cloud: false\n    allow_updates: false\n" +
		"privacy:\n  block_cloud: true\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Network.NTP.Server != "pool.ntp.org" || !loaded.Privacy.BlockCloud {
		t.Fatalf("neighbouring settings lost: network=%+v privacy=%+v", loaded.Network, loaded.Privacy)
	}
}
