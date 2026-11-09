package webserver

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"bticino_bridge/pkg/bticino"
	"bticino_bridge/pkg/deviceconfig"
)

// Readings for the dashboard. Each is a small read of a procfs, sysfs or tmpfs
// file, flash usage a statfs call, so polling them writes nothing to the flash.
const (
	firmwareVersionPath = "/etc/version"
	procUptimePath      = "/proc/uptime"
	procMeminfoPath     = "/proc/meminfo"
	// The only writable flash partition (ext4), which also holds the bridge.
	flashMountPath = "/home/bticino/cfg/extra"
)

// deviceModel returns the exact model from conf.xml ("Classe 100 X16E"), or the
// variant's generic name when the file has none.
func deviceModel() string {
	if conf, err := deviceconfig.ReadConfXML(bticino.SystemConfigXML); err == nil && conf.Model != "" {
		return conf.Model
	}
	return bticino.DeviceModelName()
}

// firmwareStamp returns the firmware build stamp, e.g. "20250522064330".
func firmwareStamp() string {
	data, err := os.ReadFile(firmwareVersionPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// systemMetrics leaves out every value it cannot read.
func systemMetrics() map[string]interface{} {
	m := map[string]interface{}{}
	if up, err := readUptime(procUptimePath); err == nil {
		m["device_uptime"] = formatUptime(up)
	}
	if total, available, err := readMeminfo(procMeminfoPath); err == nil {
		m["memory"] = map[string]uint64{"total_bytes": total, "used_bytes": total - available}
	}
	if celsius, err := readTemperature(bticino.ThermalZonePath); err == nil {
		m["temperature_c"] = celsius
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(flashMountPath, &st); err == nil {
		size := uint64(st.Bsize)
		m["flash"] = map[string]uint64{"total_bytes": st.Blocks * size, "used_bytes": (st.Blocks - st.Bfree) * size}
	}
	return m
}

// readUptime parses /proc/uptime: "<seconds since boot> <idle seconds>".
func readUptime(path string) (time.Duration, error) {
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

// readMeminfo returns MemTotal and MemAvailable in bytes.
func readMeminfo(path string) (total, available uint64, err error) {
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

// readTemperature parses a thermal zone, which reports millidegrees Celsius.
func readTemperature(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	milli, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, err
	}
	return float64(milli) / 1000, nil
}
