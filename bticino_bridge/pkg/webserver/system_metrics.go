package webserver

import (
	"os"
	"strconv"
	"strings"

	"bticino_bridge/pkg/bticino"
	"bticino_bridge/pkg/deviceconfig"
	"bticino_bridge/pkg/system"
)

// firmwareVersionPath holds the firmware build stamp (tmpfs-backed read).
const firmwareVersionPath = "/etc/version"

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
	if up, err := system.ReadUptime(system.ProcUptimePath); err == nil {
		m["device_uptime"] = formatUptime(up)
	}
	if total, available, err := system.ReadMeminfo(system.ProcMeminfoPath); err == nil {
		m["memory"] = map[string]uint64{"total_bytes": total, "used_bytes": total - available}
	}
	if celsius, err := readTemperature(bticino.ThermalZonePath); err == nil {
		m["temperature_c"] = celsius
	}
	if total, used, err := system.FlashUsage(system.FlashMountPath); err == nil {
		m["flash"] = map[string]uint64{"total_bytes": total, "used_bytes": used}
	}
	return m
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
