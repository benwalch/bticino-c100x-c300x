package input

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"bticino_bridge/pkg/bticino"
)

// testdata/proc_input_devices_c100x.txt is /proc/bus/input/devices of a Classe 100X.
func TestListEventDevicesC100X(t *testing.T) {
	devices, err := listEventDevices("testdata/proc_input_devices_c100x.txt")
	want := []EventDevice{
		{Name: "C100_KB_TOUCH keypad", Path: "/dev/input/event0"},
		{Name: "gpio-keys", Path: "/dev/input/event1"},
	}
	if err != nil || !reflect.DeepEqual(devices, want) {
		t.Errorf("devices = %+v, %v; want %+v", devices, err, want)
	}
}

// testdata/proc_input_devices_synthetic_touch.txt is made up (no 300X at hand):
// a keypad, a touchscreen with absolute axes, gpio-keys, no trailing empty line.
func TestListEventDevicesSkipsTouchscreen(t *testing.T) {
	devices, err := listEventDevices("testdata/proc_input_devices_synthetic_touch.txt")
	want := []EventDevice{
		{Name: "keypad", Path: "/dev/input/event0"},
		{Name: "gpio-keys", Path: "/dev/input/event2"},
	}
	if err != nil || !reflect.DeepEqual(devices, want) {
		t.Errorf("devices = %+v, %v; want %+v", devices, err, want)
	}
}

// writeGPIOKeys builds a gpio-keys device-tree directory: label → code.
func writeGPIOKeys(t *testing.T, keys map[string]uint32) string {
	t.Helper()
	dir := t.TempDir()
	for label, code := range keys {
		node := filepath.Join(dir, label)
		if err := os.Mkdir(node, 0o755); err != nil {
			t.Fatal(err)
		}
		raw := binary.BigEndian.AppendUint32(nil, code)
		if err := os.WriteFile(filepath.Join(node, "linux,code"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(node, "label"), []byte(label+"\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Properties of the gpio-keys node itself are files, not entries.
	if err := os.WriteFile(filepath.Join(dir, "compatible"), []byte("gpio-keys\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGPIOKeyNamesFromDeviceTree(t *testing.T) {
	c100x := writeGPIOKeys(t, map[string]uint32{
		"floor_call": 213, "wheel_up": 103, "wheel_down": 108, "wheel_push": 28,
		"horn_up": 218, "horn_down": 206, "unknown": 1,
	})
	want := map[int]string{
		213: "floor_doorbell", 103: "joystick_up", 108: "joystick_down", 28: "joystick_press",
		218: "phone_answer", 206: "phone_hangup", 1: "unknown",
	}
	if got := gpioKeyNames(c100x, bticino.GPIOKeyNamesForVariant(bticino.DeviceVariant100X)); !reflect.DeepEqual(got, want) {
		t.Errorf("c100x = %v, want %v", got, want)
	}

	c300x := writeGPIOKeys(t, map[string]uint32{"ch_piano": 6, "powerfail": 7})
	if got := gpioKeyNames(c300x, bticino.GPIOKeyNamesForVariant(bticino.DeviceVariant300X)); !reflect.DeepEqual(got, map[int]string{6: "floor_doorbell", 7: "powerfail"}) {
		t.Errorf("c300x = %v", got)
	}

	if got := gpioKeyNames(filepath.Join(t.TempDir(), "missing"), nil); got != nil {
		t.Errorf("without device tree = %v, want nil", got)
	}
}

func TestDiscoverEventDevicesNamesKeysPerDevice(t *testing.T) {
	oldDevices, oldKeys := procInputDevices, deviceTreeGPIOKeys
	t.Cleanup(func() { procInputDevices, deviceTreeGPIOKeys = oldDevices, oldKeys })
	procInputDevices = "testdata/proc_input_devices_c100x.txt"
	// The floor call label of the variant this bridge is built for.
	var floorLabel string
	for label, name := range bticino.GPIOKeyNamesForVariant(bticino.DeviceVariant) {
		if name == "floor_doorbell" {
			floorLabel = label
		}
	}
	deviceTreeGPIOKeys = writeGPIOKeys(t, map[string]uint32{floorLabel: 213})

	devices, err := discoverEventDevices()
	if err != nil || len(devices) != 2 {
		t.Fatalf("devices = %+v, %v", devices, err)
	}
	if name := keyName(devices[1].KeyNames, 213); name != "floor_doorbell" {
		t.Errorf("gpio-keys 213 = %q, want floor_doorbell", name)
	}
	if !reflect.DeepEqual(devices[0].KeyNames, bticino.KeypadKeyNamesForVariant(bticino.DeviceVariant)) {
		t.Errorf("keypad names = %v", devices[0].KeyNames)
	}
	if name := keyName(devices[0].KeyNames, 213); name != "KEY_213" {
		t.Errorf("keypad 213 = %q, want KEY_213", name)
	}
}

func TestKeypadKeyNamesForVariant(t *testing.T) {
	if names := bticino.KeypadKeyNamesForVariant(bticino.DeviceVariant300X); names[2] != "key" {
		t.Errorf("c300x = %v", names)
	}
	want100X := map[int]string{2: "p1", 3: "p2", 4: "p3", 5: "p4", 6: "star", 7: "key", 8: "eye"}
	if names := bticino.KeypadKeyNamesForVariant(bticino.DeviceVariant100X); !reflect.DeepEqual(names, want100X) {
		t.Errorf("c100x = %v, want %v", names, want100X)
	}
}
