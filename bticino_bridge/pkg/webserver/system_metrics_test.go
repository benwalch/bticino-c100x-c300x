package webserver

import (
	"os"
	"path/filepath"
	"testing"
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

	up, err := readUptime(write("uptime", "255257.52 230628.00\n"))
	if err != nil || formatUptime(up) != "2d 22h 54m" {
		t.Errorf("uptime = %q, %v; want 2d 22h 54m", formatUptime(up), err)
	}

	total, available, err := readMeminfo(write("meminfo",
		"MemTotal:         508136 kB\nMemFree:          100000 kB\nMemAvailable:     337216 kB\n"))
	if err != nil || total != 508136*1024 || available != 337216*1024 {
		t.Errorf("meminfo = %d, %d, %v", total, available, err)
	}
	if _, _, err := readMeminfo(write("meminfo_old", "MemTotal: 508136 kB\nMemFree: 1 kB\n")); err == nil {
		t.Error("meminfo without MemAvailable: want an error")
	}

	celsius, err := readTemperature(write("temp", "45320\n"))
	if err != nil || celsius != 45.32 {
		t.Errorf("temperature = %v, %v; want 45.32", celsius, err)
	}

	if _, err := readUptime(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file: want an error")
	}
}
