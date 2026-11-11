package input

import (
	"bufio"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"

	"bticino_bridge/pkg/bticino"
)

// Replaced in tests.
var (
	procInputDevices   = "/proc/bus/input/devices"
	deviceTreeGPIOKeys = "/proc/device-tree/gpio-keys"
)

const gpioKeysDeviceName = "gpio-keys"

// EventDevice is an input device the monitor reads, with the names of its keys.
type EventDevice struct {
	Name     string // as in /proc/bus/input/devices, e.g. "gpio-keys"
	Path     string // e.g. "/dev/input/event1"
	KeyNames map[int]string
}

// discoverEventDevices lists the input devices and names their keys:
// gpio-keys from the device tree, the keypad from the variant's table (its
// driver, not the device tree, defines its codes).
func discoverEventDevices() ([]EventDevice, error) {
	devices, err := listEventDevices(procInputDevices)
	gpioKeys := gpioKeyNames(deviceTreeGPIOKeys, bticino.GPIOKeyNamesForVariant(bticino.DeviceVariant))
	keypad := bticino.KeypadKeyNamesForVariant(bticino.DeviceVariant)
	for i := range devices {
		if devices[i].Name == gpioKeysDeviceName {
			devices[i].KeyNames = gpioKeys
		} else {
			devices[i].KeyNames = keypad
		}
	}
	return devices, err
}

// listEventDevices returns the devices in /proc/bus/input/devices that have an
// event handler. Devices with absolute axes ("B: ABS=", e.g. the 300X
// touchscreen, which also reports BTN_TOUCH as a key) are skipped.
func listEventDevices(path string) ([]EventDevice, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var devices []EventDevice
	var name string
	var handlers []string
	var hasAxes bool
	// Devices are blocks separated by an empty line.
	flush := func() {
		if !hasAxes {
			for _, handler := range handlers {
				if strings.HasPrefix(handler, "event") {
					devices = append(devices, EventDevice{Name: name, Path: "/dev/input/" + handler})
				}
			}
		}
		name, handlers, hasAxes = "", nil, false
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "N: Name="):
			name = strings.Trim(strings.TrimPrefix(line, "N: Name="), `"`)
		case strings.HasPrefix(line, "H: Handlers="):
			handlers = strings.Fields(strings.TrimPrefix(line, "H: Handlers="))
		case strings.HasPrefix(line, "B: ABS="):
			hasAxes = true
		}
	}
	flush()
	return devices, scanner.Err()
}

// gpioKeyNames reads label and linux,code of each gpio-keys entry in the
// device tree and renames labels found in aliases. It returns nil when the
// device tree has no gpio-keys.
func gpioKeyNames(dir string, aliases map[string]string) map[int]string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := map[int]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		code, err := os.ReadFile(filepath.Join(dir, entry.Name(), "linux,code"))
		if err != nil || len(code) != 4 {
			continue
		}
		label := entry.Name()
		if data, err := os.ReadFile(filepath.Join(dir, entry.Name(), "label")); err == nil {
			label = strings.TrimRight(string(data), "\x00")
		}
		if alias, ok := aliases[label]; ok {
			label = alias
		}
		names[int(binary.BigEndian.Uint32(code))] = label
	}
	return names
}
