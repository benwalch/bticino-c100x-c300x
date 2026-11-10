package webserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Inputs are the values read on a Classe 100X.
func TestSystemMetricParsers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	if got := formatUptime(255257 * time.Second); got != "2d 22h 54m" {
		t.Errorf("formatUptime = %q, want 2d 22h 54m", got)
	}

	celsius, err := readTemperature(write("temp", "45320\n"))
	if err != nil || celsius != 45.32 {
		t.Errorf("temperature = %v, %v; want 45.32", celsius, err)
	}

	if _, err := readTemperature(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file: want an error")
	}
}
