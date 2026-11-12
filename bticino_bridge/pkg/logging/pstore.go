package logging

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Replaced in tests.
var (
	procMounts = "/proc/mounts"
	procStat   = "/proc/stat"
	bootIDPath = "/proc/sys/kernel/random/boot_id"
)

// crashReasons start the line of a kernel crash record that names the cause.
var crashReasons = []string{"Kernel panic", "Internal error", "Unable to handle", "BUG:"}

// RecordPreviousShutdown notes in the crash log, once per device boot, when the
// device booted and what pstore (ramoops) kept from the previous run. ramoops
// lives in RAM, so it survives a warm reset but not a power loss, and the next
// reboot replaces it. Verified readings (Linux 4.9 kernel/reboot.c, panic.c,
// fs/pstore/ram.c):
//   - console ending in "reboot: Restarting system": a process asked for a reboot
//   - dmesg-ramoops-*: the kernel recorded a panic or oops
//   - nothing: power loss or a cold start
func RecordPreviousShutdown() {
	id, err := os.ReadFile(bootIDPath)
	if err != nil {
		return
	}
	marker := "boot " + strings.TrimSpace(string(id))

	crashMu.Lock()
	path := crashPath
	crashMu.Unlock()
	if path == "" {
		return
	}
	// The bridge may start several times per boot; one note per boot is enough.
	if tail, err := readTail(path, crashKeep); err == nil && strings.Contains(string(tail), marker) {
		return
	}
	RecordNote(marker + bootTime() + ": previous run ended with " + pstoreSummary(pstoreDir()))
}

// bootTime returns " at <time>" from btime in /proc/stat, or "".
func bootTime() string {
	file, err := os.Open(procStat)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "btime "); ok {
			if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				return " at " + time.Unix(seconds, 0).Format(time.RFC3339)
			}
		}
	}
	return ""
}

// pstoreDir returns where pstore is mounted, or "" without pstore.
func pstoreDir() string {
	file, err := os.Open(procMounts)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if fields := strings.Fields(scanner.Text()); len(fields) > 2 && fields[2] == "pstore" {
			return fields[1]
		}
	}
	return ""
}

// pstoreSummary describes the pstore records in dir in one line.
func pstoreSummary(dir string) string {
	if dir == "" {
		return "no pstore mounted"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "pstore unreadable: " + err.Error()
	}
	var console string
	var crashes []string
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, "console-ramoops"):
			console = `console "` + lastLine(filepath.Join(dir, name)) + `"`
		case strings.HasPrefix(name, "dmesg-ramoops"):
			crashes = append(crashes, name)
		}
	}
	sort.Strings(crashes)
	var parts []string
	if console != "" {
		parts = append(parts, console)
	}
	for _, name := range crashes {
		parts = append(parts, name+" "+crashRecord(filepath.Join(dir, name)))
	}
	if len(parts) == 0 {
		return "no pstore records (power loss or cold start)"
	}
	return strings.Join(parts, "; ")
}

// crashRecord returns the header ("Panic#1 Part1") and the cause of a kernel
// crash record, which are lost with the next reboot.
func crashRecord(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unreadable"
	}
	lines := strings.Split(string(data), "\n")
	summary := `"` + strings.TrimSpace(lines[0]) + `"`
	for _, line := range lines[1:] {
		for _, reason := range crashReasons {
			// Kernel lines start with "<level>[ timestamp] ".
			if i := strings.Index(line, reason); i >= 0 {
				return summary + ` "` + clip(strings.TrimSpace(line[i:]), 200) + `"`
			}
		}
	}
	return summary
}

// lastLine returns the last non-empty line of a small file.
func lastLine(path string) string {
	data, err := readTail(path, 4096)
	if err != nil {
		return "unreadable"
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	return clip(strings.TrimSpace(lines[len(lines)-1]), 200)
}

func clip(s string, max int) string {
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
