package system

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Inputs are the values read on a Classe 100X.
func TestMetricReaders(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	up, err := ReadUptime(write("uptime", "255257.52 230628.00\n"))
	if err != nil || up.Truncate(time.Second) != 255257*time.Second {
		t.Errorf("uptime = %v, %v; want 255257s", up, err)
	}
	if _, err := ReadUptime(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file: want an error")
	}

	total, available, err := ReadMeminfo(write("meminfo",
		"MemTotal:         508136 kB\nMemFree:          100000 kB\nMemAvailable:     337216 kB\n"))
	if err != nil || total != 508136*1024 || available != 337216*1024 {
		t.Errorf("meminfo = %d, %d, %v", total, available, err)
	}
	if _, _, err := ReadMeminfo(write("meminfo_old", "MemTotal: 508136 kB\nMemFree: 1 kB\n")); err == nil {
		t.Error("meminfo without MemAvailable: want an error")
	}

	total, used, err := FlashUsage(dir)
	if err != nil || total == 0 || used > total {
		t.Errorf("flash = %d, %d, %v", total, used, err)
	}
}
