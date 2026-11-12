package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// usePstore points the package at a fake /proc and pstore and returns the crash log path.
func usePstore(t *testing.T, records map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	store := filepath.Join(dir, "pstore")
	if err := os.Mkdir(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range records {
		if err := os.WriteFile(filepath.Join(store, name), []byte(content), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	mounts := filepath.Join(dir, "mounts")
	writeFile(t, mounts, "/dev/mmcblk2p6 / ext4 ro 0 0\npstore "+store+" pstore ro,noatime 0 0\n")
	bootID := filepath.Join(dir, "boot_id")
	writeFile(t, bootID, "1234-abcd\n")
	stat := filepath.Join(dir, "stat")
	writeFile(t, stat, "cpu  1 2 3 4\nbtime 1794502613\nprocesses 900\n")
	crash := filepath.Join(dir, "crash.log")
	writeFile(t, crash, "--- bridge 1.0.0 started 2026-11-12T20:23:56+01:00\n")

	oldMounts, oldStat, oldBootID, oldCrash := procMounts, procStat, bootIDPath, crashPath
	t.Cleanup(func() { procMounts, procStat, bootIDPath, crashPath = oldMounts, oldStat, oldBootID, oldCrash })
	procMounts, procStat, bootIDPath, crashPath = mounts, stat, bootID, crash
	return crash
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRecordPreviousShutdownOncePerBoot(t *testing.T) {
	// The record the Classe 100X kept after the reboot of 2026-11-12 17:56.
	crash := usePstore(t, map[string]string{"console-ramoops-0": "reboot: Restarting system\n"})
	RecordPreviousShutdown()
	RecordPreviousShutdown() // a second bridge start in the same boot
	got := read(t, crash)
	booted := " at " + time.Unix(1794502613, 0).Format(time.RFC3339)
	if strings.Count(got, "boot 1234-abcd") != 1 ||
		!strings.Contains(got, "boot 1234-abcd"+booted+`: previous run ended with console "reboot: Restarting system"`) {
		t.Errorf("crash log = %q", got)
	}
	if lastCrash(crash) != "" {
		t.Error("the note must not count as a crash")
	}
}

func TestPstoreSummary(t *testing.T) {
	// Made-up records in the ramoops format: header line, then kernel lines.
	usePstore(t, map[string]string{
		"console-ramoops-0": "Rebooting in 2 seconds..\n",
		"dmesg-ramoops-0": "Panic#1 Part1\n<6>[  100.000000] usb 1-1: new device\n" +
			"<0>[  101.000000] Kernel panic - not syncing: Out of memory and no killable processes\n",
		"dmesg-ramoops-1": "Oops#1 Part1\n<1>[   50.000000] Unable to handle kernel NULL pointer dereference at virtual address 00000000\n",
	})
	want := `console "Rebooting in 2 seconds.."; ` +
		`dmesg-ramoops-0 "Panic#1 Part1" "Kernel panic - not syncing: Out of memory and no killable processes"; ` +
		`dmesg-ramoops-1 "Oops#1 Part1" "Unable to handle kernel NULL pointer dereference at virtual address 00000000"`
	if got := pstoreSummary(pstoreDir()); got != want {
		t.Errorf("summary =\n%s\nwant\n%s", got, want)
	}

	usePstore(t, nil)
	if got := pstoreSummary(pstoreDir()); got != "no pstore records (power loss or cold start)" {
		t.Errorf("empty pstore = %q", got)
	}
	if got := pstoreSummary(""); got != "no pstore mounted" {
		t.Errorf("no pstore = %q", got)
	}
}
