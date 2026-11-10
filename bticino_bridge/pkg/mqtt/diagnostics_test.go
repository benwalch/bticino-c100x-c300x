package mqtt

import (
	"testing"

	"bticino_bridge/pkg/deviceconfig"
)

func TestDiagnosticSensorsAreDiagnostic(t *testing.T) {
	for _, id := range []string{
		"bticino_system_flash_usage", "bticino_system_memory_usage",
		"bticino_system_last_boot", "bticino_system_bridge_started",
		"bticino_bus_connection", "bticino_system_network",
		"bticino_system_bridge_version",
	} {
		if got := entityCategory(id); got != "diagnostic" {
			t.Errorf("entityCategory(%s) = %q, want diagnostic", id, got)
		}
	}
}

// Inputs are the meminfo values read on a Classe 100X (kB).
func TestUsagePercent(t *testing.T) {
	if got := usagePercent(508136-337216, 508136); got != "34" {
		t.Errorf("usagePercent = %s, want 34", got)
	}
	if got := usagePercent(1, 2); got != "50" {
		t.Errorf("usagePercent = %s, want 50", got)
	}
}

// The device block carries the native firmware and hardware versions (values
// read on a Classe 100X), and leaves them out until WHO=13 has answered.
func TestDeviceInfoVersions(t *testing.T) {
	b := &MQTTBridge{}
	d := b.deviceInfo()
	if _, has := d["sw_version"]; has {
		t.Errorf("sw_version before WHO=13: %v, want none", d["sw_version"])
	}
	if _, has := d["hw_version"]; has {
		t.Errorf("hw_version before WHO=13: %v, want none", d["hw_version"])
	}

	b.systemInfo = map[string]string{"firmware_version": "1.5.8", "hardware_version": "02.07.0"}
	d = b.deviceInfo()
	if d["sw_version"] != "1.5.8" || d["hw_version"] != "02.07.0" {
		t.Errorf("device = %v, want sw_version 1.5.8 and hw_version 02.07.0", d)
	}
}

// Every ringtone select must be a type the device can set.
func TestRingtoneSelectsAreSettable(t *testing.T) {
	if len(ringtoneSelects) != len(deviceconfig.SettableRingtones) {
		t.Errorf("%d selects for %d settable ringtones", len(ringtoneSelects), len(deviceconfig.SettableRingtones))
	}
	for _, r := range ringtoneSelects {
		if _, ok := deviceconfig.SettableRingtones[r.typ]; !ok {
			t.Errorf("select %q has no settings.xml object", r.typ)
		}
	}
}
