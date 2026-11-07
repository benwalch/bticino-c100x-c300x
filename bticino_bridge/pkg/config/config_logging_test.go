package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoggingDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("openwebnet:\n  port: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]LoggingConfig{"LoadConfig": loaded.Logging, "GetDefaultConfig": GetDefaultConfig().Logging} {
		if got.Level != "error" || got.MaxSize != "2MB" || got.MaxFiles != 3 || got.File == "" {
			t.Errorf("%s logging = %+v", name, got)
		}
	}
}
