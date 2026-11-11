package input

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/bticino"
)

// InputMonitor monitors hardware inputs (buttons, GPIO)
type InputMonitor struct {
	EventDevices []EventDevice
	GPIOPins     []int
	Logger       *logrus.Logger

	// Event callbacks
	OnButtonPress  func(ButtonEvent)
	OnScreenChange func(ScreenEvent)

	// Internal state
	running   bool
	stopChan  chan struct{}
	wg        sync.WaitGroup
	mutex     sync.RWMutex
	openFiles map[*os.File]bool // closed by Stop to end blocked reads

	// GPIO state tracking
	gpioStates map[int]bool
}

// ButtonEvent represents a physical button press/release
type ButtonEvent struct {
	Device    string    `json:"device"`   // "/dev/input/event0"
	Key       int       `json:"key"`      // Linux key code
	KeyName   string    `json:"key_name"` // "key", "floor_doorbell", … or "KEY_<code>"
	Action    string    `json:"action"`   // "press" | "release"
	Value     int32     `json:"value"`    // 1 = press, 0 = release
	Timestamp time.Time `json:"timestamp"`
}

// ScreenEvent represents screen state change (on/off via GPIO)
type ScreenEvent struct {
	GPIO      int       `json:"gpio"`  // GPIO pin number
	State     bool      `json:"state"` // true = screen on, false = screen off
	Timestamp time.Time `json:"timestamp"`
}

// InputEventData represents raw Linux input event structure
// CRITICAL: On 32-bit ARM7, struct input_event uses struct timeval with
// two 32-bit fields (tv_sec, tv_usec), NOT 64 bits.
// Total size: 16 bytes (4+4+2+2+4), not 24 bytes.
type InputEventData struct {
	Time  [2]int32 // struct timeval: tv_sec (4 bytes) + tv_usec (4 bytes) on 32-bit ARM7
	Type  uint16   // EV_KEY, EV_ABS, etc.
	Code  uint16   // KEY_1, ABS_X, etc.
	Value int32    // 1=press, 0=release, or absolute value
}

// Event types
const (
	EV_SYN = 0x00 // Synchronization events
	EV_KEY = 0x01 // Key press/release events
)

// NewInputMonitor creates a new input monitor
func NewInputMonitor(logger *logrus.Logger) *InputMonitor {
	devices, err := discoverEventDevices()
	if err != nil {
		logger.WithError(err).Warn("Cannot list the input devices")
	}
	return &InputMonitor{
		EventDevices: devices,
		GPIOPins:     bticino.GPIOPinsForVariant(bticino.DeviceVariant),
		Logger:       logger,
		stopChan:     make(chan struct{}),
		openFiles:    make(map[*os.File]bool),
		gpioStates:   make(map[int]bool),
	}
}

// Start begins monitoring all input devices
func (im *InputMonitor) Start() error {
	im.mutex.Lock()
	defer im.mutex.Unlock()

	if im.running {
		return fmt.Errorf("input monitor already running")
	}

	im.running = true
	im.Logger.Info("Starting input monitor...")

	// Start monitoring each event device
	for _, device := range im.EventDevices {
		im.wg.Add(1)
		go im.monitorEventDevice(device)
	}

	// Start GPIO monitoring
	im.wg.Add(1)
	go im.monitorGPIO()

	im.Logger.Info("Input monitor started successfully")
	return nil
}

// Stop stops all input monitoring
func (im *InputMonitor) Stop() error {
	im.mutex.Lock()
	if !im.running {
		im.mutex.Unlock()
		return nil
	}

	im.Logger.Info("Stopping input monitor...")
	im.running = false
	close(im.stopChan)
	for file := range im.openFiles {
		file.Close()
	}
	im.mutex.Unlock()

	im.wg.Wait()
	im.Logger.Info("Input monitor stopped")
	return nil
}

// trackOpenFile registers file for Stop and reports false once stopping.
func (im *InputMonitor) trackOpenFile(file *os.File) bool {
	im.mutex.Lock()
	defer im.mutex.Unlock()
	if !im.running {
		return false
	}
	im.openFiles[file] = true
	return true
}

func (im *InputMonitor) untrackOpenFile(file *os.File) {
	im.mutex.Lock()
	delete(im.openFiles, file)
	im.mutex.Unlock()
}

// IsRunning returns true if the monitor is running
func (im *InputMonitor) IsRunning() bool {
	im.mutex.RLock()
	defer im.mutex.RUnlock()
	return im.running
}

// monitorEventDevice monitors a specific /dev/input/event* device
// Includes auto-reconnect logic: if a read fails, wait and retry
func (im *InputMonitor) monitorEventDevice(device EventDevice) {
	defer im.wg.Done()

	im.Logger.Infof("Monitoring input device: %s (%s)", device.Path, device.Name)

	const maxRetries = 0 // 0 = retry indefinitely
	const retryDelay = 3 * time.Second

	retryCount := 0

	for im.IsRunning() {
		// Check if device exists
		if _, err := os.Stat(device.Path); os.IsNotExist(err) {
			im.Logger.Warnf("Input device %s not found, skipping", device.Path)
			return
		}

		// Open device for reading
		file, err := os.Open(device.Path)
		if err != nil {
			im.Logger.Errorf("Failed to open input device %s: %v", device.Path, err)
			retryCount++
			if maxRetries > 0 && retryCount >= maxRetries {
				im.Logger.Errorf("Max retries reached for %s, giving up", device.Path)
				return
			}
			im.Logger.Infof("Retrying %s in %v (attempt %d)...", device.Path, retryDelay, retryCount)
			select {
			case <-time.After(retryDelay):
				continue
			case <-im.stopChan:
				return
			}
		}

		// Reset retry count on successful open
		retryCount = 0

		if !im.trackOpenFile(file) {
			file.Close()
			break
		}
		readErr := im.readEventLoop(device, file)
		im.untrackOpenFile(file)
		file.Close()

		if !im.IsRunning() {
			break
		}

		if readErr != nil {
			im.Logger.Warnf("Event loop ended for %s: %v. Reconnecting in %v...", device.Path, readErr, retryDelay)
			select {
			case <-time.After(retryDelay):
				continue
			case <-im.stopChan:
				return
			}
		}
	}

	im.Logger.Infof("Stopped monitoring device: %s", device.Path)
}

// readEventLoop reads events from a device file until error or stop
func (im *InputMonitor) readEventLoop(device EventDevice, file *os.File) error {
	for im.IsRunning() {
		var event InputEventData
		err := binary.Read(file, binary.LittleEndian, &event)
		if err != nil {
			if im.IsRunning() {
				return fmt.Errorf("read error: %v", err)
			}
			return nil
		}

		// Process the event
		im.processInputEvent(device, &event)
	}
	return nil
}

// processInputEvent processes a raw input event
func (im *InputMonitor) processInputEvent(device EventDevice, event *InputEventData) {
	timestamp := time.Unix(int64(event.Time[0]), int64(event.Time[1])*1000)

	switch event.Type {
	case EV_KEY:
		im.processKeyEvent(device, event.Code, event.Value, timestamp)
	case EV_SYN:
		// Synchronization event - ignore for now
	default:
		im.Logger.Debugf("Unknown event type %d from %s", event.Type, device.Path)
	}
}

// processKeyEvent processes key press/release events
func (im *InputMonitor) processKeyEvent(device EventDevice, code uint16, value int32, timestamp time.Time) {
	keyCode := int(code)
	action := "release"
	if value == 1 {
		action = "press"
	}

	buttonEvent := ButtonEvent{
		Device:    device.Path,
		Key:       keyCode,
		KeyName:   keyName(device.KeyNames, keyCode),
		Action:    action,
		Value:     value,
		Timestamp: timestamp,
	}

	im.Logger.Debugf("Button %s: %s (device: %s)", buttonEvent.KeyName, action, device.Path)

	// Call callback if set
	if im.OnButtonPress != nil {
		go im.OnButtonPress(buttonEvent)
	}
}

// monitorGPIO monitors GPIO pins for screen state changes
func (im *InputMonitor) monitorGPIO() {
	defer im.wg.Done()

	im.Logger.Info("Starting GPIO monitoring for screen state detection")

	// Initialize GPIO states
	for _, pin := range im.GPIOPins {
		state, err := im.readGPIOValue(pin)
		if err != nil {
			im.Logger.Warnf("Failed to read initial GPIO %d state: %v", pin, err)
			continue
		}
		im.updateGPIOState(pin, state)
		im.Logger.Debugf("GPIO %d initial state: %t", pin, state)
	}

	// Poll GPIO pins for changes
	ticker := time.NewTicker(100 * time.Millisecond) // 100ms polling
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			im.checkGPIOChanges()
		case <-im.stopChan:
			im.Logger.Info("GPIO monitoring stopped")
			return
		}
	}
}

// checkGPIOChanges checks all GPIO pins for state changes
func (im *InputMonitor) checkGPIOChanges() {
	for _, pin := range im.GPIOPins {
		newState, err := im.readGPIOValue(pin)
		if err != nil {
			// GPIO read error - skip this pin
			continue
		}

		oldState, known := im.updateGPIOState(pin, newState)
		if !known || oldState == newState {
			continue
		}
		im.Logger.Infof("GPIO %d state changed: %t -> %t", pin, oldState, newState)

		screenEvent := ScreenEvent{
			GPIO:      pin,
			State:     newState,
			Timestamp: time.Now(),
		}

		if im.OnScreenChange != nil {
			go im.OnScreenChange(screenEvent)
		}
	}
}

// updateGPIOState stores state and returns the previous one, if any.
func (im *InputMonitor) updateGPIOState(pin int, state bool) (old bool, known bool) {
	im.mutex.Lock()
	defer im.mutex.Unlock()
	old, known = im.gpioStates[pin]
	im.gpioStates[pin] = state
	return old, known
}

// readGPIOValue reads the current value of a GPIO pin
func (im *InputMonitor) readGPIOValue(pin int) (bool, error) {
	path := fmt.Sprintf("/sys/class/gpio/gpio%d/value", pin)

	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		value := strings.TrimSpace(scanner.Text())
		if value == "1" {
			return true, nil
		} else if value == "0" {
			return false, nil
		}
		return false, fmt.Errorf("unexpected GPIO value: %s", value)
	}

	return false, scanner.Err()
}

// keyName returns the device's name for a key code, or "KEY_<code>".
func keyName(names map[int]string, keyCode int) string {
	if name, ok := names[keyCode]; ok {
		return name
	}
	return fmt.Sprintf("KEY_%d", keyCode)
}

// GetCurrentGPIOStates returns current state of all monitored GPIO pins
func (im *InputMonitor) GetCurrentGPIOStates() map[int]bool {
	im.mutex.RLock()
	defer im.mutex.RUnlock()

	states := make(map[int]bool)
	for pin, state := range im.gpioStates {
		states[pin] = state
	}
	return states
}
