package system

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Readings shared by the dashboard and the MQTT diagnostic sensors. Each is a
// small procfs read or a statfs call, so polling them writes nothing to the flash.
const (
	ProcUptimePath  = "/proc/uptime"
	ProcMeminfoPath = "/proc/meminfo"
	// The only writable flash partition (ext4), which also holds the bridge.
	FlashMountPath = "/home/bticino/cfg/extra"
)

// ReadUptime parses /proc/uptime: "<seconds since boot> <idle seconds>".
func ReadUptime(path string) (time.Duration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty %s", path)
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

// ReadMeminfo returns MemTotal and MemAvailable in bytes.
func ReadMeminfo(path string) (total, available uint64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	found := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text()) // "MemTotal:  508136 kB"
		if len(fields) < 2 {
			continue
		}
		var target *uint64
		switch fields[0] {
		case "MemTotal:":
			target = &total
		case "MemAvailable:":
			target = &available
		default:
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, 0, err
		}
		*target = kb * 1024
		found++
	}
	if found < 2 {
		return 0, 0, fmt.Errorf("MemTotal or MemAvailable missing in %s", path)
	}
	return total, available, scanner.Err()
}

// FlashUsage returns the size and the used bytes of the filesystem at path.
func FlashUsage(path string) (total, used uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	size := uint64(st.Bsize)
	return st.Blocks * size, (st.Blocks - st.Bfree) * size, nil
}
