package mqtt

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/bticino"
	"bticino_bridge/pkg/bticino_commands"
	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/deviceconfig"
	"bticino_bridge/pkg/events"
	"bticino_bridge/pkg/messageparser"
	"bticino_bridge/pkg/system"
	"bticino_bridge/pkg/openwebnet"
	"bticino_bridge/pkg/version"
)

// ==================== MQTT BRIDGE ====================

// MQTTBridge manages MQTT communication with Home Assistant
type MQTTBridge struct {
	config         *config.Config
	logger         *logrus.Logger
	client         pahomqtt.Client
	eventBus       events.EventBus
	openwebnet     *openwebnet.Client
	messageParser  *messageparser.MessageParser
	commandHandler *bticino_commands.BTicinoCommandHandler
	isConnected    bool
	mu             sync.RWMutex

	// State tracking for HA entities
	voicemailEnabled   bool
	doorbellSoundOn    bool
	muteOn             bool
	displayOn          bool
	sipRegistered      bool
	lastDoorbellTime   time.Time
	lastButtonPress    time.Time
	lastButtonKey      string
	eventsPublished    int64
	commandsReceived   int64
	connectTime        time.Time
	lastDisconnectTime time.Time
	reconnectAttempts  int32
	connectionEvents   []ConnectionEvent // Ring buffer of recent events
	cePos              int
	ceFull             bool

	// Doorbell camera (optional): cooperative snapshot capture on doorbell
	// ring / incoming call, published to <prefix>/camera/doorbell.
	cameraEnabled bool
	cameraCapture func() ([]byte, error)
	cameraMu      sync.Mutex // serializes captures (avoids doorbell+call overlap)

	// systemInfo caches the last published WHO=13 facts for refreshNetworkInfo.
	systemInfo map[string]string

	wifiOnce sync.Once // starts the WiFi monitor only once

	// lastBusEventRaw / lastActivityRaw suppress back-to-back duplicate OWN
	// frames (e.g. the 5-minute bell-state keepalive) on event-style topics.
	// Separate baselines per output: a new frame must still reach both.
	lastBusEventRaw string
	lastActivityRaw string
}

// SetCameraCapture enables the doorbell camera entity and registers the
// capture function (cooperative snapshot). Must be called BEFORE Start()
// so the entity is included in discovery. When the doorbell rings or a call
// comes in, the bridge captures a JPEG and publishes it to <prefix>/camera/doorbell.
func (b *MQTTBridge) SetCameraCapture(fn func() ([]byte, error)) {
	b.mu.Lock()
	b.cameraCapture = fn
	b.cameraEnabled = fn != nil
	b.mu.Unlock()
}

// captureDoorbellPhoto captures a cooperative snapshot (if the camera is
// enabled) and publishes it to <prefix>/camera/doorbell in base64. It runs in
// the background and is serialized: if a capture is already in progress, it is
// ignored (avoids overlapping doorbell + call, which would fire two *7*300 at once).
func (b *MQTTBridge) captureDoorbellPhoto(reason string) {
	b.mu.RLock()
	enabled := b.cameraEnabled
	capture := b.cameraCapture
	b.mu.RUnlock()
	if !enabled || capture == nil {
		return
	}

	go func() {
		if !b.cameraMu.TryLock() {
			b.logger.Debug("Camera: capture in progress, ignoring new request")
			return
		}
		defer b.cameraMu.Unlock()

		// The native video session may come up delayed (~1-2s after the ring, so a
		// capture fired immediately is most likely NACKed. Retry gently in 1.5s gaps.
		var jpeg []byte
		var err error
		for attempt := 1; attempt <= 5; attempt++ {
			time.Sleep(1500 * time.Millisecond)
			b.logger.Infof("Camera: capturing doorbell snapshot (reason: %s, attempt %d/5)", reason, attempt)
			jpeg, err = capture()
			if err == nil {
				break
			}
			if !strings.Contains(err.Error(), "NACK") {
				break // not a timing issue — retrying won't help
			}
			b.logger.WithError(err).Infof("Camera: snapshot NACKed (attempt %d/5)", attempt)
		}
		if err != nil {
			b.logger.WithError(err).Warn("Camera: could not capture doorbell snapshot")
			return
		}
		encoded := base64.StdEncoding.EncodeToString(jpeg)
		b.publish(b.dataPrefix()+"/camera/doorbell", encoded, true)
		b.logger.Infof("Camera: doorbell photo published (%d bytes)", len(jpeg))
	}()
}

// ConnectionEvent represents an MQTT connection event
type ConnectionEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"` // "connect", "disconnect", "reconnect"
	Success   bool      `json:"success"`
	Message   string    `json:"message,omitempty"`
}

// NewMQTTBridge creates a new MQTT bridge instance
func NewMQTTBridge(cfg *config.Config, logger *logrus.Logger) (*MQTTBridge, error) {
	return &MQTTBridge{
		config:           cfg,
		logger:           logger,
		voicemailEnabled: true,  // Default: enabled
		doorbellSoundOn:  true,  // Default: enabled
		displayOn:        false, // Default: off (screensaver)
	}, nil
}

// dataPrefix returns the data topic prefix (for state/command topics).
// This is separate from the HA discovery prefix ("homeassistant").
func (b *MQTTBridge) dataPrefix() string {
	p := b.config.MQTT.TopicPrefix
	// Avoid using "homeassistant" as data prefix — it collides with HA discovery
	if p == "" || p == "homeassistant" || p == "homeassistant/sensor/bticino" {
		return "bticino"
	}
	return p
}

// Start initializes and starts the MQTT bridge
func (b *MQTTBridge) Start(ctx context.Context) error {
	b.logger.Info("Starting MQTT Bridge for Home Assistant...")

	opts := pahomqtt.NewClientOptions()
	brokerURL := fmt.Sprintf("tcp://%s:%d", b.config.MQTT.Host, b.config.MQTT.Port)
	opts.AddBroker(brokerURL)
	opts.SetClientID(fmt.Sprintf("bticino_bridge_%d", time.Now().Unix()))
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(30 * time.Second)
	opts.SetKeepAlive(60 * time.Second)

	// LWT — mark device offline when disconnected
	opts.SetWill(b.dataPrefix()+"/bridge/state", "offline", 1, true)

	if b.config.MQTT.Username != "" {
		opts.SetUsername(b.config.MQTT.Username)
	}
	if b.config.MQTT.Password != "" {
		opts.SetPassword(b.config.MQTT.Password)
	}

	opts.SetConnectionLostHandler(func(client pahomqtt.Client, err error) {
		b.logger.WithError(err).Error("MQTT connection lost")
		b.mu.Lock()
		b.isConnected = false
		b.lastDisconnectTime = time.Now()
		b.addConnectionEvent("disconnect", false, err.Error())
		b.mu.Unlock()
	})

	opts.SetOnConnectHandler(func(client pahomqtt.Client) {
		b.mu.Lock()
		// Check if this is a reconnect (was previously connected)
		isReconnect := b.connectTime.IsZero() == false && b.lastDisconnectTime.IsZero() == false
		b.isConnected = true
		b.connectTime = time.Now()

		eventType := "connect"
		if isReconnect {
			b.reconnectAttempts++
			eventType = "reconnect"
			b.logger.Infof("MQTT reconnected to Home Assistant (attempt %d)", b.reconnectAttempts)
		} else {
			b.logger.Info("MQTT Connected to Home Assistant")
		}

		b.addConnectionEvent(eventType, true, "")
		b.mu.Unlock()

		// Publish availability
		b.publish(b.dataPrefix()+"/bridge/state", "online", true)

		// Publish HA discovery entities, subscribe to commands, publish initial states
		b.publishHomeAssistantDiscovery()
		b.subscribeToCommands()
		b.publishAllStates()
	})

	mqttClient := pahomqtt.NewClient(opts)
	b.client = mqttClient // Assign BEFORE Connect() to avoid nil pointer in OnConnectHandler callback
	if token := mqttClient.Connect(); token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to connect to MQTT broker: %w", token.Error())
	}

	b.logger.Infof("MQTT Bridge connected to %s", brokerURL)
	return nil
}

// IsConnected returns the MQTT connection state
func (b *MQTTBridge) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.isConnected
}

// addConnectionEvent adds a connection event to the ring buffer
func (b *MQTTBridge) addConnectionEvent(eventType string, success bool, message string) {
	if b.connectionEvents == nil {
		b.connectionEvents = make([]ConnectionEvent, 20) // Max 20 events
	}
	b.connectionEvents[b.cePos] = ConnectionEvent{
		Timestamp: time.Now(),
		Type:      eventType,
		Success:   success,
		Message:   message,
	}
	b.cePos = (b.cePos + 1) % 20
	if b.cePos == 0 {
		b.ceFull = true
	}
}

// GetConnectionEvents returns recent connection events
func (b *MQTTBridge) GetConnectionEvents() []ConnectionEvent {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.connectionEvents == nil {
		return nil
	}

	result := make([]ConnectionEvent, 0, 20)
	if !b.ceFull {
		for i := 0; i < b.cePos; i++ {
			if b.connectionEvents[i].Timestamp.IsZero() == false {
				result = append(result, b.connectionEvents[i])
			}
		}
	} else {
		// Return in chronological order
		for i := b.cePos; i < 20; i++ {
			if b.connectionEvents[i].Timestamp.IsZero() == false {
				result = append(result, b.connectionEvents[i])
			}
		}
		for i := 0; i < b.cePos; i++ {
			if b.connectionEvents[i].Timestamp.IsZero() == false {
				result = append(result, b.connectionEvents[i])
			}
		}
	}
	return result
}

// SetEventBus sets the event bus and configures subscriptions
func (b *MQTTBridge) SetEventBus(eventBus events.EventBus) {
	b.eventBus = eventBus
	b.setupEventSubscriptions()
}

// SetOpenWebNet sets the OpenWebNet client for command execution
func (b *MQTTBridge) SetOpenWebNet(client *openwebnet.Client) {
	b.openwebnet = client
}

// SetMessageParser sets the message parser for answering machine stats
func (b *MQTTBridge) SetMessageParser(mp *messageparser.MessageParser) {
	b.messageParser = mp
}

// SetCommandHandler sets the enhanced command handler for full voicemail protocol
func (b *MQTTBridge) SetCommandHandler(ch *bticino_commands.BTicinoCommandHandler) {
	b.commandHandler = ch
}

// SetDeviceConfigPublisher initializes the device config publisher via MQTT
func (b *MQTTBridge) SetDeviceConfigPublisher(interval time.Duration) {
	if !b.IsConnected() {
		b.logger.Warn("Cannot start device config publisher: MQTT not connected")
		return
	}

	publisher := deviceconfig.NewDeviceMQTTPublisher(b.logger, b, b.dataPrefix())
	publisher.Start(interval)
	b.logger.Infof("Device config MQTT publisher started with interval: %v", interval)

	// Also start the file watcher for real-time sync
	watcher := deviceconfig.NewFileWatcher(b.logger, b, b.dataPrefix())
	watcher.Start()
	b.logger.Info("Device config file watcher started for real-time sync")
}

// SetFileWatcher optionally start file watcher separately
func (b *MQTTBridge) SetFileWatcher() {
	if !b.IsConnected() {
		b.logger.Warn("Cannot start file watcher: MQTT not connected")
		return
	}

	watcher := deviceconfig.NewFileWatcher(b.logger, b, b.dataPrefix())
	watcher.Start()
	b.logger.Info("Device config file watcher started for real-time sync")
}

// ==================== HOME ASSISTANT DISCOVERY ====================

// publishHomeAssistantDiscovery sends MQTT Discovery config for all entities.
// Discovery topics use the HA standard prefix "homeassistant/{component}/{objectID}/config".
// State/command topics use the data prefix "bticino/..." to avoid collisions.
func (b *MQTTBridge) publishHomeAssistantDiscovery() {
	dp := b.dataPrefix()

	device := map[string]interface{}{
		"identifiers":  []string{"bticlasse100x"},
		"name":         "BTicino Classe 100X",
		"model":        bticino.DeviceModel,
		"manufacturer": bticino.DeviceManufacturer,
		"sw_version":   "Bridge v" + version.GetVersion(),
	}

	// configuration_url: "Visit" link in HA to the device web dashboard.
	// We detect the LAN IP automatically.
	if ip := localOutboundIP(); ip != "" {
		device["configuration_url"] = fmt.Sprintf("http://%s:%d/", ip, b.config.Web.Port)
	}

	avail := map[string]interface{}{
		"topic":                 dp + "/bridge/state",
		"payload_available":     "online",
		"payload_not_available": "offline",
	}

	entityCount := 0

	// --- 1. Lock: Door ---
	b.discovery("lock", "bticino_door", map[string]interface{}{
		"name":           "Door",
		"unique_id":      "bticino_intercom_door_lock",
		"object_id":      "bticino_door",
		"command_topic":  dp + "/lock/set",
		"state_topic":    dp + "/lock/state",
		"payload_lock":   "LOCK",
		"payload_unlock": "UNLOCK",
		"state_locked":   "LOCKED",
		"state_unlocked": "UNLOCKED",
		"optimistic":     false,
		"icon":           "mdi:door",
		"device":         device,
		"availability":   avail,
	})
	entityCount++

	// --- 2. Switch: Answering Machine (300X only; the 100X has no UI for it) ---
	if bticino.AnsweringMachineAvailable() {
		b.discovery("switch", "bticino_voicemail", map[string]interface{}{
			"name":          "Answering Machine",
			"unique_id":     "bticino_intercom_voicemail",
			"object_id":     "bticino_voicemail",
			"command_topic": dp + "/voicemail/set",
			"state_topic":   dp + "/voicemail/state",
			"payload_on":    "ON",
			"payload_off":   "OFF",
			"state_on":      "ON",
			"state_off":     "OFF",
			"icon":          "mdi:voicemail",
			"device":        device,
			"availability":  avail,
		})
		entityCount++
	}

	// --- 3. Switch: Doorbell Sound (300X only; SET ignored on 100X) ---
	if bticino.RemoteSetCommandsWork() {
		b.discovery("switch", "bticino_doorbell_sound", map[string]interface{}{
			"name":          "Ringtone",
			"unique_id":     "bticino_intercom_doorbell_sound",
			"object_id":     "bticino_doorbell_sound",
			"command_topic": dp + "/doorbellsound/set",
			"state_topic":   dp + "/doorbellsound/state",
			"payload_on":    "ON",
			"payload_off":   "OFF",
			"state_on":      "ON",
			"state_off":     "OFF",
			"icon":          "mdi:bell",
			"device":        device,
			"availability":  avail,
		})
		entityCount++
	}

	// --- 4. Switch: Display (300X only; SET ignored on 100X) ---
	if bticino.RemoteSetCommandsWork() {
		b.discovery("switch", "bticino_display", map[string]interface{}{
			"name":          "Display",
			"unique_id":     "bticino_intercom_display",
			"object_id":     "bticino_display",
			"command_topic": dp + "/display/set",
			"state_topic":   dp + "/display/state",
			"payload_on":    "ON",
			"payload_off":   "OFF",
			"state_on":      "ON",
			"state_off":     "OFF",
			"icon":          "mdi:tablet",
			"device":        device,
			"availability":  avail,
		})
		entityCount++
	}

	// --- 5. Switch: Mute (REMOVED - merged into doorbell_sound) ---
	// Speaker mute control is already integrated into doorbell_sound
	// when a voice note is recorded, GPIO 180 is deactivated automatically
	// entityCount++

	// --- 6. Button: Staircase Light ---
	b.discovery("button", "bticino_staircase_light", map[string]interface{}{
		"name":          "Staircase Light",
		"unique_id":     "bticino_intercom_staircase_light",
		"object_id":     "bticino_staircase_light",
		"command_topic": dp + "/light/set",
		"payload_press": "PRESS",
		"icon":          "mdi:stairs",
		"device":        device,
		"availability":  avail,
	})
	entityCount++

	// --- 8. Sensor: Temperature ---
	b.discovery("sensor", "bticino_temperature", map[string]interface{}{
		"name":                "Device Temperature",
		"unique_id":           "bticino_intercom_temperature",
		"object_id":           "bticino_temperature",
		"state_topic":         dp + "/sensor/temperature",
		"unit_of_measurement": "\u00b0C",
		"device_class":        "temperature",
		"state_class":         "measurement",
		"icon":                "mdi:thermometer",
		"device":              device,
		"availability":        avail,
	})
	entityCount++

	// --- 9-11. Message sensors (300X only; no answering machine on 100X) ---
	if bticino.AnsweringMachineAvailable() {
		b.discovery("sensor", "bticino_new_messages", map[string]interface{}{
			"name":         "New Messages",
			"unique_id":    "bticino_intercom_new_messages",
			"object_id":    "bticino_new_messages",
			"state_topic":  dp + "/sensor/new_messages",
			"icon":         "mdi:message-badge",
			"device":       device,
			"availability": avail,
		})
		entityCount++

		b.discovery("sensor", "bticino_total_messages", map[string]interface{}{
			"name":         "Total Messages",
			"unique_id":    "bticino_intercom_total_messages",
			"object_id":    "bticino_total_messages",
			"state_topic":  dp + "/sensor/total_messages",
			"icon":         "mdi:message-text-clock",
			"device":       device,
			"availability": avail,
		})
		entityCount++

		b.discovery("sensor", "bticino_storage", map[string]interface{}{
			"name":                "Answering Machine Storage",
			"unique_id":           "bticino_intercom_storage",
			"object_id":           "bticino_storage",
			"state_topic":         dp + "/sensor/storage_used",
			"unit_of_measurement": "%",
			"icon":                "mdi:harddisk",
			"device":              device,
			"availability":        avail,
		})
		entityCount++
	}

	// --- 12. Sensor: Keypad (last button pressed) ---
	b.discovery("sensor", "bticino_keypad", map[string]interface{}{
		"name":         "Keypad",
		"unique_id":    "bticino_intercom_keypad",
		"object_id":    "bticino_keypad",
		"state_topic":  dp + "/sensor/keypad",
		"icon":         "mdi:dialpad",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- 13. Binary Sensor: Doorbell ---
	b.discovery("binary_sensor", "bticino_doorbell", map[string]interface{}{
		"name":         "Entrance Doorbell",
		"unique_id":    "bticino_intercom_doorbell",
		"object_id":    "bticino_doorbell",
		"state_topic":  dp + "/doorbell/state",
		"payload_on":   "ON",
		"payload_off":  "OFF",
		"device_class": "occupancy",
		"off_delay":    5,
		"icon":         "mdi:doorbell",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- LEDs as binary sensors (model-specific: c300x vs c100x) ---
	// Entity table comes from bticino.LEDEntitiesByModel, selected via
	// bticino.DeviceVariant (change the const + rebuild to switch units).
	ledEntities := bticino.LEDEntitiesForVariant(bticino.DeviceVariant)
	b.logger.Infof("MQTT discovery: advertising %d LED entities for variant %s",
		len(ledEntities), bticino.DeviceVariant)

	for _, led := range ledEntities {
		b.discovery("binary_sensor", led.ObjectID, map[string]interface{}{
			"name":         led.Name,
			"unique_id":    led.ObjectID,
			"object_id":    led.ObjectID,
			"state_topic":  dp + "/led/" + led.Sysfs + "/state",
			"payload_on":   "ON",
			"payload_off":  "OFF",
			"icon":         led.Icon,
			"device":       device,
			"availability": avail,
		})
		entityCount++
	}

	// --- 22. Binary Sensor: SIP Status ---
	b.discovery("binary_sensor", "bticino_sip_status", map[string]interface{}{
		"name":         "SIP Status",
		"unique_id":    "bticino_intercom_sip_status",
		"object_id":    "bticino_sip_status",
		"state_topic":  dp + "/sensor/sip_status",
		"payload_on":   "ON",
		"payload_off":  "OFF",
		"device_class": "connectivity",
		"icon":         "mdi:phone-voip",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- 23. Sensor: System Diagnostics ---
	b.discovery("sensor", "bticino_system_diag", map[string]interface{}{
		"name":         "System Diagnostics",
		"unique_id":    "bticino_intercom_system_diag",
		"object_id":    "bticino_system_diag",
		"state_topic":  dp + "/sensor/system_diag",
		"icon":         "mdi:heart-pulse",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- GPIO individual sensors (model-specific pin list) ---
	gpioPins := bticino.GPIOPinsForVariant(bticino.DeviceVariant)
	b.logger.Infof("MQTT discovery: advertising %d GPIO entities for variant %s",
		len(gpioPins), bticino.DeviceVariant)
	for _, pin := range gpioPins {
		objID := fmt.Sprintf("bticino_gpio_%d", pin)
		name := fmt.Sprintf("GPIO %d", pin)
		if fn, ok := bticino.GPIOFunctionsByModel[bticino.DeviceVariant][pin]; ok {
			name = fmt.Sprintf("GPIO %d (%s)", pin, fn)
		}
		b.discovery("binary_sensor", objID, map[string]interface{}{
			"name":         name,
			"unique_id":    fmt.Sprintf("bticino_intercom_gpio_%d", pin),
			"object_id":    objID,
			"state_topic":  fmt.Sprintf("%s/gpio/%d/state", dp, pin),
			"payload_on":   "ON",
			"payload_off":  "OFF",
			"icon":         "mdi:chip",
			"device":       device,
			"availability": avail,
		})
		entityCount++
	}

	// --- 38. Sensor: Activity Log ---
	b.discovery("sensor", "bticino_activity_log", map[string]interface{}{
		"name":                  "Activity Log",
		"unique_id":             "bticino_intercom_activity_log",
		"object_id":             "bticino_activity_log",
		"state_topic":           dp + "/sensor/activity_log",
		"json_attributes_topic": dp + "/sensor/activity_log/attributes",
		"icon":                  "mdi:timeline-text",
		"device":                device,
		"availability":          avail,
	})
	entityCount++

	// --- 39. Sensor: System Information (WHO=13) ---
	b.discovery("sensor", "bticino_system_info", map[string]interface{}{
		"name":                  "System Information",
		"unique_id":             "bticino_intercom_system_info",
		"object_id":             "bticino_system_info",
		"state_topic":           dp + "/sensor/system_info",
		"json_attributes_topic": dp + "/sensor/system_info/attributes",
		"icon":                  "mdi:information-outline",
		"device":                device,
		"availability":          avail,
	})
	entityCount++

	// --- Additional locks (dynamic, per configuration) ---
	for i, lock := range b.config.AdditionalLocks {
		whereStr := fmt.Sprintf("%d", lock.Where)
		objID := fmt.Sprintf("bticino_lock_%d", lock.Where)
		name := lock.Name
		if name == "" {
			name = fmt.Sprintf("Lock %d", i+1)
		}
		b.discovery("lock", objID, map[string]interface{}{
			"name":           name,
			"unique_id":      fmt.Sprintf("bticino_intercom_lock_%d", lock.Where),
			"object_id":      objID,
			"command_topic":  dp + "/lock/" + whereStr + "/set",
			"state_topic":    dp + "/lock/" + whereStr + "/state",
			"payload_lock":   "LOCK",
			"payload_unlock": "UNLOCK",
			"state_locked":   "LOCKED",
			"state_unlocked": "UNLOCKED",
			"optimistic":     false,
			"icon":           "mdi:door",
			"device":         device,
			"availability":   avail,
		})
		entityCount++
	}

	b.logger.Infof("MQTT: Published %d Home Assistant discovery entities", entityCount)

	// ==================== DEVICE CONFIG ENTITIES (Phase 4) ====================

	// --- Sensors: Answering Machine Status + Memory (300X only) ---
	if bticino.AnsweringMachineAvailable() {
		b.discovery("sensor", "bticino_answering_status", map[string]interface{}{
			"name":           "Answering Machine Status",
			"unique_id":      "bticino_intercom_answering_status",
			"object_id":      "bticino_answering_status",
			"state_topic":    dp + "/answering/state",
			"value_template": "{{ value_json.activated }}",
			"icon":           "mdi:voicemail",
			"device":         device,
			"availability":   avail,
		})
		entityCount++

		b.discovery("sensor", "bticino_answering_memory", map[string]interface{}{
			"name":           "Answering Machine Memory",
			"unique_id":      "bticino_intercom_answering_memory",
			"object_id":      "bticino_answering_memory",
			"state_topic":    dp + "/answering/state",
			"value_template": "{{ value_json.memory_used }}",
			"icon":           "mdi:memory",
			"device":         device,
			"availability":   avail,
		})
		entityCount++
	}

	// --- Sensor: Ringtone S0 (entrance panel) ---
	b.discovery("sensor", "bticino_ringtone_s0", map[string]interface{}{
		"name":           "Entrance Doorbell Ringtone",
		"unique_id":      "bticino_intercom_ringtone_s0",
		"object_id":      "bticino_ringtone_s0",
		"state_topic":    dp + "/audio/ringtone/s0",
		"value_template": "Ringtone #{{ value_json.value }}",
		"icon":           "mdi:bell",
		"device":         device,
		"availability":   avail,
	})
	entityCount++

	// --- Sensor: Ringtone Door (flat/floor door) ---
	b.discovery("sensor", "bticino_ringtone_door", map[string]interface{}{
		"name":           "Floor Doorbell Ringtone",
		"unique_id":      "bticino_intercom_ringtone_door",
		"object_id":      "bticino_ringtone_door",
		"state_topic":    dp + "/audio/ringtone/door",
		"value_template": "Ringtone #{{ value_json.value }}",
		"icon":           "mdi:bell",
		"device":         device,
		"availability":   avail,
	})
	entityCount++

	// --- Sensor: Ringtone S1 (secondary entrance station) ---
	b.discovery("sensor", "bticino_ringtone_s1", map[string]interface{}{
		"name":           "Secondary Doorbell Ringtone",
		"unique_id":      "bticino_intercom_ringtone_s1",
		"object_id":      "bticino_ringtone_s1",
		"state_topic":    dp + "/audio/ringtone/s1",
		"value_template": "Ringtone #{{ value_json.value }}",
		"icon":           "mdi:bell",
		"device":         device,
		"availability":   avail,
	})
	entityCount++

	// --- Sensor: Volume S0 ---
	b.discovery("sensor", "bticino_volume_s0", map[string]interface{}{
		"name":           "Entrance Doorbell Volume",
		"unique_id":      "bticino_intercom_volume_s0",
		"object_id":      "bticino_volume_s0",
		"state_topic":    dp + "/audio/volume/s0",
		"value_template": "{{ value_json.value }}",
		"icon":           "mdi:volume-high",
		"device":         device,
		"availability":   avail,
	})
	entityCount++

	// --- Sensor: Volume S1 (secondary entrance station) ---
	b.discovery("sensor", "bticino_volume_s1", map[string]interface{}{
		"name":           "Secondary Doorbell Volume",
		"unique_id":      "bticino_intercom_volume_s1",
		"object_id":      "bticino_volume_s1",
		"state_topic":    dp + "/audio/volume/s1",
		"value_template": "{{ value_json.value }}",
		"icon":           "mdi:volume-high",
		"device":         device,
		"availability":   avail,
	})
	entityCount++

	// --- Sensor: Volume Door ---
	b.discovery("sensor", "bticino_volume_door", map[string]interface{}{
		"name":           "Floor Doorbell Volume",
		"unique_id":      "bticino_intercom_volume_door",
		"object_id":      "bticino_volume_door",
		"state_topic":    dp + "/audio/volume/door",
		"value_template": "{{ value_json.value }}",
		"icon":           "mdi:doorbell",
		"device":         device,
		"availability":   avail,
	})
	entityCount++

	// --- Sensor: Display Brightness ---
	b.discovery("sensor", "bticino_display_brightness", map[string]interface{}{
		"name":           "Display Brightness",
		"unique_id":      "bticino_intercom_display_brightness",
		"object_id":      "bticino_display_brightness",
		"state_topic":    dp + "/display/settings",
		"value_template": "{{ value_json.brightness }}",
		"icon":           "mdi:brightness-6",
		"device":         device,
		"availability":   avail,
	})
	entityCount++

	// --- Sensors: Camera Brightness (one per configured TVCC camera ID) ---
	camIDs, err := deviceconfig.GetAllCameraIDs("/home/bticino/cfg/extra/47/tvcc_settings.ini")
	if err != nil {
		b.logger.WithError(err).Warn("Camera discovery: cannot read TVCC IDs, skipping camera entities")
	}
	for _, camID := range camIDs {
		objID := fmt.Sprintf("bticino_camera_%s_brightness", camID)
		b.discovery("sensor", objID, map[string]interface{}{
			"name":           fmt.Sprintf("Camera %s Brightness", camID),
			"unique_id":      fmt.Sprintf("bticino_intercom_camera_%s_brightness", camID),
			"object_id":      objID,
			"state_topic":    dp + "/camera/" + camID + "/settings",
			"value_template": "{{ value_json.brightness }}",
			"icon":           "mdi:cctv",
			"device":         device,
			"availability":   avail,
		})
		entityCount++

		objID = fmt.Sprintf("bticino_camera_%s_contrast", camID)
		b.discovery("sensor", objID, map[string]interface{}{
			"name":           fmt.Sprintf("Camera %s Contrast", camID),
			"unique_id":      fmt.Sprintf("bticino_intercom_camera_%s_contrast", camID),
			"object_id":      objID,
			"state_topic":    dp + "/camera/" + camID + "/settings",
			"value_template": "{{ value_json.contrast }}",
			"icon":           "mdi:contrast",
			"device":         device,
			"availability":   avail,
		})
		entityCount++
	}

	// --- Sensor: Call state (INCOMING/CONNECTED/IDLE) ---
	b.discovery("sensor", "bticino_call_state", map[string]interface{}{
		"name":         "Call Status",
		"unique_id":    "bticino_intercom_call_state",
		"object_id":    "bticino_call_state",
		"state_topic":  dp + "/call/state",
		"icon":         "mdi:phone",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- Sensor: Caller ---
	b.discovery("sensor", "bticino_call_caller", map[string]interface{}{
		"name":         "Caller",
		"unique_id":    "bticino_intercom_call_caller",
		"object_id":    "bticino_call_caller",
		"state_topic":  dp + "/call/caller",
		"icon":         "mdi:account-voice",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- Binary Sensor: Landing doorbell (floor doorbell) ---
	b.discovery("binary_sensor", "bticino_doorbell_floor", map[string]interface{}{
		"name":         "Floor Doorbell",
		"unique_id":    "bticino_intercom_doorbell_floor",
		"object_id":    "bticino_doorbell_floor",
		"state_topic":  dp + "/doorbell_floor/state",
		"payload_on":   "ON",
		"payload_off":  "OFF",
		"device_class": "occupancy",
		"icon":         "mdi:bell-ring",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- Event: Doorbell (entrance panel) ---
	b.discovery("event", "bticino_event_doorbell", map[string]interface{}{
		"name":         "Entrance Doorbell (Event)",
		"unique_id":    "bticino_intercom_event_doorbell",
		"object_id":    "bticino_event_doorbell",
		"state_topic":  dp + "/event/doorbell",
		"event_types":  []string{"pressed"},
		"device_class": "doorbell",
		"icon":         "mdi:bell",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- Event: Landing doorbell ---
	b.discovery("event", "bticino_event_doorbell_floor", map[string]interface{}{
		"name":         "Floor Doorbell (Event)",
		"unique_id":    "bticino_intercom_event_doorbell_floor",
		"object_id":    "bticino_event_doorbell_floor",
		"state_topic":  dp + "/event/doorbell_floor",
		"event_types":  []string{"pressed"},
		"device_class": "doorbell",
		"icon":         "mdi:bell-ring",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- Event: Call (incoming/connected/ended) ---
	b.discovery("event", "bticino_event_call", map[string]interface{}{
		"name":         "Call (Event)",
		"unique_id":    "bticino_intercom_event_call",
		"object_id":    "bticino_event_call",
		"state_topic":  dp + "/event/call",
		"event_types":  []string{"incoming", "connected", "ended"},
		"icon":         "mdi:phone-ring",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- Event: OpenWebNet bus frame (raw) ---
	b.discovery("event", "bticino_event_bus_own", map[string]interface{}{
		"name":         "OWN Bus Event",
		"unique_id":    "bticino_intercom_event_bus_own",
		"object_id":    "bticino_event_bus_own",
		"state_topic":  dp + "/event/bus_own",
		"event_types":  []string{"frame"},
		"icon":         "mdi:bus-alert",
		"device":       device,
		"availability": avail,
	})
	entityCount++

	// --- Sensor: IP (Diagnostic) — extracted from system_info attributes ---
	b.discovery("sensor", "bticino_ip_address", map[string]interface{}{
		"name":           "IP Address",
		"unique_id":      "bticino_intercom_ip",
		"object_id":      "bticino_ip_address",
		"state_topic":    dp + "/sensor/system_info/attributes",
		"value_template": "{{ value_json.ip_address | default('unknown') }}",
		"icon":           "mdi:ip-network",
		"device":         device,
		"availability":   avail,
	})
	entityCount++

	// --- Sensor: MAC (Diagnostic) ---
	b.discovery("sensor", "bticino_mac_address", map[string]interface{}{
		"name":           "MAC Address",
		"unique_id":      "bticino_intercom_mac",
		"object_id":      "bticino_mac_address",
		"state_topic":    dp + "/sensor/system_info/attributes",
		"value_template": "{{ value_json.mac_address | default('unknown') }}",
		"icon":           "mdi:network",
		"device":         device,
		"availability":   avail,
	})
	entityCount++

	// --- Sensor: WiFi Signal (Diagnostic) ---
	b.discovery("sensor", "bticino_wifi_signal", map[string]interface{}{
		"name":                "Wi-Fi Signal",
		"unique_id":           "bticino_intercom_wifi_signal",
		"object_id":           "bticino_wifi_signal",
		"state_topic":         dp + "/sensor/wifi_signal",
		"unit_of_measurement": "%",
		"icon":                "mdi:wifi",
		"device":              device,
		"availability":        avail,
	})
	entityCount++

	// --- Button: Mark all messages as read (300X only) ---
	if bticino.AnsweringMachineAvailable() {
		b.discovery("button", "bticino_mark_all_read", map[string]interface{}{
			"name":          "Mark All as Read",
			"unique_id":     "bticino_intercom_mark_all_read",
			"object_id":     "bticino_mark_all_read",
			"command_topic": dp + "/messages/markallread/set",
			"payload_press": "PRESS",
			"icon":          "mdi:email-open-multiple",
			"device":        device,
			"availability":  avail,
		})
		entityCount++
	}

	// --- Buttons: system restart (Configuration) — only if enabled ---
	if b.config.MQTT.EnableSystemButtons {
		b.discovery("button", "bticino_restart_bridge", map[string]interface{}{
			"name":            "Restart Bridge",
			"unique_id":       "bticino_intercom_restart_bridge",
			"object_id":       "bticino_restart_bridge",
			"command_topic":   dp + "/system/restart_bridge/set",
			"payload_press":   "PRESS",
			"entity_category": "config",
			"icon":            "mdi:restart",
			"device":          device,
			"availability":    avail,
		})
		entityCount++

		b.discovery("button", "bticino_reboot_device", map[string]interface{}{
			"name":            "Restart Device",
			"unique_id":       "bticino_intercom_reboot_device",
			"object_id":       "bticino_reboot_device",
			"command_topic":   dp + "/system/reboot/set",
			"payload_press":   "PRESS",
			"entity_category": "config",
			"device_class":    "restart",
			"icon":            "mdi:power-cycle",
			"device":          device,
			"availability":    avail,
		})
		entityCount++
	}

	// --- Camera: doorbell snapshot (only if capture is wired up) ---
	b.mu.RLock()
	cameraEnabled := b.cameraEnabled
	b.mu.RUnlock()
	if cameraEnabled {
		b.discovery("camera", "bticino_doorbell_camera", map[string]interface{}{
			"name":           "Doorbell Camera",
			"unique_id":      "bticino_intercom_doorbell_camera",
			"object_id":      "bticino_doorbell_camera",
			"topic":          dp + "/camera/doorbell",
			"image_encoding": "b64",
			"icon":           "mdi:doorbell-video",
			"device":         device,
			"availability":   avail,
		})
		entityCount++
	}

	b.logger.Infof("MQTT: Published %d Home Assistant discovery entities (total with device config)", entityCount)
}

// diagnosticEntities are the object_ids that belong in the "Diagnostic"
// section of Home Assistant. Everything else (locks, switches, action buttons
// and operational sensors like messages/doorbell/call) stays in Controls/Sensors.
var diagnosticEntities = map[string]bool{
	"bticino_temperature": true, "bticino_storage": true, "bticino_keypad": true,
	"bticino_system_diag":  true,
	"bticino_activity_log": true, "bticino_system_info": true, "bticino_sip_status": true,
	"bticino_answering_status": true, "bticino_answering_memory": true,
	"bticino_ringtone_s0": true, "bticino_ringtone_door": true, "bticino_ringtone_s1": true, "bticino_volume_s0": true, "bticino_volume_s1": true, "bticino_volume_door": true,
	"bticino_display_brightness": true,
	"bticino_ip_address":         true, "bticino_mac_address": true, "bticino_wifi_signal": true,
}

// entityCategory decides the HA entity_category from the object_id. The
// LEDs (bticino_led_*) and GPIO (bticino_gpio_*) are diagnostic too.
func entityCategory(objectID string) string {
	if diagnosticEntities[objectID] ||
		strings.HasPrefix(objectID, "bticino_led_") ||
		strings.HasPrefix(objectID, "bticino_gpio_") ||
		strings.HasPrefix(objectID, "bticino_camera_") {
		return "diagnostic"
	}
	return ""
}

// localOutboundIP returns the LAN IP of the default interface.
// Delegates to bticino.LocalOutboundIP (single implementation).
func localOutboundIP() string {
	return bticino.LocalOutboundIP()
}

// discovery publishes one HA MQTT discovery config message
func (b *MQTTBridge) discovery(component, objectID string, cfg map[string]interface{}) {
	// Assign entity_category automatically if none was specified.
	if _, has := cfg["entity_category"]; !has {
		if cat := entityCategory(objectID); cat != "" {
			cfg["entity_category"] = cat
		}
	}

	topic := fmt.Sprintf("homeassistant/%s/%s/config", component, objectID)
	jsonData, err := json.Marshal(cfg)
	if err != nil {
		b.logger.WithError(err).WithField("object_id", objectID).Error("Failed to marshal discovery config")
		return
	}
	b.publish(topic, string(jsonData), true)
}

// ==================== COMMAND SUBSCRIPTIONS ====================

func (b *MQTTBridge) subscribeToCommands() {
	dp := b.dataPrefix()
	subs := 0

	sub := func(topic string, handler pahomqtt.MessageHandler) {
		if token := b.client.Subscribe(topic, 1, handler); token.Wait() && token.Error() != nil {
			b.logger.WithError(token.Error()).WithField("topic", topic).Error("Failed to subscribe")
		} else {
			subs++
		}
	}

	sub(dp+"/lock/set", b.handleDoorCommand)
	if bticino.AnsweringMachineAvailable() {
		sub(dp+"/voicemail/set", b.handleVoicemailCommand)
	}
	if bticino.RemoteSetCommandsWork() {
		sub(dp+"/doorbellsound/set", b.handleDoorbellSoundCommand)
		sub(dp+"/display/set", b.handleDisplayCommand)
		sub(dp+"/mute/set", b.handleMuteCommand)
	}
	sub(dp+"/light/set", b.handleLightCommand)
	sub(dp+"/command/send", b.handleRawCommand)
	if bticino.AnsweringMachineAvailable() {
		sub(dp+"/messages/markallread/set", b.handleMarkAllReadCommand)
	}

	// System restart buttons (only if enabled)
	if b.config.MQTT.EnableSystemButtons {
		sub(dp+"/system/restart_bridge/set", b.handleRestartBridgeCommand)
		sub(dp+"/system/reboot/set", b.handleRebootDeviceCommand)
	}

	// Subscribe to additional lock commands
	for _, lock := range b.config.AdditionalLocks {
		whereStr := fmt.Sprintf("%d", lock.Where)
		lockWhere := lock.Where // capture for closure
		sub(dp+"/lock/"+whereStr+"/set", func(client pahomqtt.Client, msg pahomqtt.Message) {
			b.handleAdditionalLockCommand(lockWhere, string(msg.Payload()))
		})
	}

	b.logger.Infof("MQTT: Subscribed to %d command topics", subs)
}

// --- Command Handlers ---

func (b *MQTTBridge) handleDoorCommand(client pahomqtt.Client, msg pahomqtt.Message) {
	payload := string(msg.Payload())
	b.logger.WithField("payload", payload).Info("MQTT: Door lock command")
	b.incCommands()

	if payload == "UNLOCK" || payload == "OPEN" {
		b.executeDoorUnlock()
	}
}

func (b *MQTTBridge) executeDoorUnlock() {
	if _, err := b.executeOWN(bticino.CmdDoorOpenPress); err != nil {
		b.logger.WithError(err).Error("Failed door unlock press")
		return
	}
	dp := b.dataPrefix()
	b.publish(dp+"/lock/state", "UNLOCKED", true)

	go func() {
		time.Sleep(1 * time.Second)
		if _, err := b.executeOWN(bticino.CmdDoorOpenRelease); err != nil {
			b.logger.WithError(err).Error("Failed door unlock release")
		}
		// Reset to locked after 5s
		time.Sleep(5 * time.Second)
		b.publish(dp+"/lock/state", "LOCKED", true)
	}()
}

func (b *MQTTBridge) handleVoicemailCommand(client pahomqtt.Client, msg pahomqtt.Message) {
	payload := string(msg.Payload())
	b.logger.WithField("payload", payload).Info("MQTT: Voicemail command")
	b.incCommands()

	on := payload == "ON" || payload == "1"

	// Try 4-step protocol via commandHandler (full app-like sequence)
	if b.commandHandler != nil {
		var err error
		if on {
			b.logger.Info("MQTT: Enabling voicemail via 4-step protocol")
			err = b.commandHandler.EnableAnsweringMachine()
		} else {
			b.logger.Info("MQTT: Disabling voicemail via 4-step protocol")
			err = b.commandHandler.DisableAnsweringMachine()
		}
		if err != nil {
			b.logger.WithError(err).Warn("4-step voicemail protocol failed, falling back to simple command")
		} else {
			b.mu.Lock()
			b.voicemailEnabled = on
			b.mu.Unlock()
			b.publish(b.dataPrefix()+"/voicemail/state", boolState(on), true)
			return
		}
	}

	// Fallback: simple 2-command via netcat
	cmd := bticino.CmdVoicemailOff
	if on {
		cmd = bticino.CmdVoicemailOn
	}

	if _, err := b.executeOWN(cmd); err != nil {
		b.logger.WithError(err).Error("Failed voicemail toggle")
		return
	}

	b.mu.Lock()
	b.voicemailEnabled = on
	b.mu.Unlock()
	b.publish(b.dataPrefix()+"/voicemail/state", boolState(on), true)
}

func (b *MQTTBridge) handleDoorbellSoundCommand(client pahomqtt.Client, msg pahomqtt.Message) {
	payload := string(msg.Payload())
	b.logger.WithField("payload", payload).Info("MQTT: Doorbell sound command")
	b.incCommands()

	on := payload == "ON" || payload == "1"
	cmd := bticino.CmdBellOff
	if on {
		cmd = bticino.CmdBellOn
	}

	if _, err := b.executeOWN(cmd); err != nil {
		b.logger.WithError(err).Error("Failed doorbell sound toggle")
		return
	}

	b.mu.Lock()
	b.doorbellSoundOn = on
	b.mu.Unlock()
	b.publish(b.dataPrefix()+"/doorbellsound/state", boolState(on), true)
}

func (b *MQTTBridge) handleDisplayCommand(client pahomqtt.Client, msg pahomqtt.Message) {
	payload := string(msg.Payload())
	b.logger.WithField("payload", payload).Info("MQTT: Display command")
	b.incCommands()

	on := payload == "ON" || payload == "1"
	cmd := bticino.CmdDisplayOff
	if on {
		cmd = bticino.CmdDisplayOn
	}

	if _, err := b.executeOWN(cmd); err != nil {
		b.logger.WithError(err).Error("Failed display toggle")
		return
	}

	b.mu.Lock()
	b.displayOn = on
	b.mu.Unlock()
	b.publish(b.dataPrefix()+"/display/state", boolState(on), true)
}

func (b *MQTTBridge) handleMuteCommand(client pahomqtt.Client, msg pahomqtt.Message) {
	payload := string(msg.Payload())
	b.logger.WithField("payload", payload).Info("MQTT: Mute command")
	b.incCommands()

	on := payload == "ON" || payload == "1"
	cmd := bticino.CmdMuteOff
	if on {
		cmd = bticino.CmdMuteOn
	}

	if _, err := b.executeOWN(cmd); err != nil {
		b.logger.WithError(err).Error("Failed mute toggle")
		return
	}

	b.mu.Lock()
	b.muteOn = on
	b.mu.Unlock()
	b.publish(b.dataPrefix()+"/mute/state", boolState(on), true)
}

func (b *MQTTBridge) handleLightCommand(client pahomqtt.Client, msg pahomqtt.Message) {
	b.logger.Info("MQTT: Staircase light command")
	b.incCommands()

	if _, err := b.executeOWN(bticino.CmdLightOnPress); err != nil {
		b.logger.WithError(err).Error("Failed staircase light press")
		return
	}
	go func() {
		time.Sleep(1 * time.Second)
		if _, err := b.executeOWN(bticino.CmdLightOnRelease); err != nil {
			b.logger.WithError(err).Error("Failed staircase light release")
		}
	}()
}

// publishWiFiSignal reads the WiFi signal strength via connmanctl and publishes
// it (%). connmanctl services lists the services; the connected one starts with
// '*'; connmanctl services <id> includes a "Strength = NN" line.
func (b *MQTTBridge) publishWiFiSignal() {
	out, err := exec.Command("connmanctl", "services").Output()
	if err != nil {
		b.logger.WithError(err).Debug("WiFi: 'connmanctl services' failed")
		return
	}
	svc := ""
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "wifi_") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		svc = fields[len(fields)-1]
		if strings.HasPrefix(strings.TrimSpace(line), "*") {
			break // the connected one (marked with *) takes priority
		}
	}
	if svc == "" {
		return
	}

	det, err := exec.Command("connmanctl", "services", svc).Output()
	if err != nil {
		b.logger.WithError(err).Debug("WiFi: 'connmanctl services <id>' failed")
		return
	}
	for _, line := range strings.Split(string(det), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Strength") {
			if parts := strings.SplitN(line, "=", 2); len(parts) == 2 {
				b.publish(b.dataPrefix()+"/sensor/wifi_signal", strings.TrimSpace(parts[1]), true)
				return
			}
		}
	}
}

// wifiMonitorLoop republishes the WiFi signal periodically.
func (b *MQTTBridge) wifiMonitorLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if !b.IsConnected() {
			continue
		}
		b.publishWiFiSignal()
		b.refreshNetworkInfo()
	}
}

// refreshNetworkInfo re-reads the local IP (DHCP can change it while
// running) and republishes system_info on actual change. Local read,
// zero bus traffic.
func (b *MQTTBridge) refreshNetworkInfo() {
	ip := bticino.LocalOutboundIP()
	if ip == "" || ip == "127.0.0.1" {
		return
	}

	b.mu.RLock()
	if b.systemInfo == nil || b.systemInfo["ip_address"] == ip {
		b.mu.RUnlock()
		return
	}
	b.mu.RUnlock()

	b.mu.Lock()
	b.systemInfo["ip_address"] = ip
	jsonData, err := json.Marshal(b.systemInfo)
	summary := b.systemInfo["firmware_version"]
	b.mu.Unlock()
	if err != nil {
		return
	}
	if summary == "" {
		summary = "unknown"
	}
	dp := b.dataPrefix()
	b.publish(dp+"/sensor/system_info", summary, true)
	b.publish(dp+"/sensor/system_info/attributes", string(jsonData), true)
	b.logger.WithField("ip", ip).Info("IP changed, system_info republished")
}

// handleRestartBridgeCommand restarts the bticino_bridge process itself: launches a
// detached relaunch (setsid) after a brief pause and exits. There is a ~2s gap
// with no instance (no overlap).
func (b *MQTTBridge) handleRestartBridgeCommand(client pahomqtt.Client, msg pahomqtt.Message) {
	if !b.config.MQTT.EnableSystemButtons {
		return
	}
	b.logger.Warn("MQTT: bridge restart requested")
	b.incCommands()
	system.RestartBridgeAsync(b.logger)
}

// handleRebootDeviceCommand reboots the whole video door phone (/sbin/reboot).
func (b *MQTTBridge) handleRebootDeviceCommand(client pahomqtt.Client, msg pahomqtt.Message) {
	if !b.config.MQTT.EnableSystemButtons {
		return
	}
	b.logger.Warn("MQTT: DEVICE REBOOT requested (/sbin/reboot)")
	b.incCommands()
	system.RebootDeviceAsync(b.logger)
}

// publishEvent publishes an HA Event entity trigger to <prefix>/event/<sub>.
// The payload is JSON with event_type (mandatory for the HA event platform)
// plus optional attributes. Not retained (events are momentary).
func (b *MQTTBridge) publishEvent(sub, eventType string, extra map[string]interface{}) {
	payload := map[string]interface{}{"event_type": eventType}
	for k, v := range extra {
		payload[k] = v
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	b.publish(b.dataPrefix()+"/event/"+sub, string(data), false)
}

// handleMarkAllReadCommand marks all answering machine messages as read.
func (b *MQTTBridge) handleMarkAllReadCommand(client pahomqtt.Client, msg pahomqtt.Message) {
	b.logger.Info("MQTT: Mark all messages as read")
	b.incCommands()
	if b.messageParser == nil {
		b.logger.Warn("MarkAllRead: messageParser not available")
		return
	}
	updated, err := b.messageParser.MarkAllMessagesAsRead()
	if err != nil {
		b.logger.WithError(err).Warnf("MarkAllRead: failed after %d updated", updated)
	}
	b.logger.Infof("MarkAllRead: %d messages marked as read", updated)
	b.PublishMessageStats()
}

func (b *MQTTBridge) handleRawCommand(client pahomqtt.Client, msg pahomqtt.Message) {
	cmd := string(msg.Payload())
	b.logger.WithField("command", cmd).Info("MQTT: Raw OWN command")
	b.incCommands()

	if _, err := b.executeOWN(cmd); err != nil {
		b.logger.WithError(err).Error("Failed raw command")
	}
}

// handleAdditionalLockCommand handles the unlock command of an additional lock.
// Uses the same press+release pattern as the main lock (WHERE=20).
func (b *MQTTBridge) handleAdditionalLockCommand(where int, payload string) {
	b.logger.WithFields(logrus.Fields{
		"where":   where,
		"payload": payload,
	}).Info("MQTT: Additional lock command")
	b.incCommands()

	if payload != "UNLOCK" && payload != "OPEN" {
		return
	}

	pressCmd := bticino.BuildDoorOpenPress(where)
	releaseCmd := bticino.BuildDoorOpenRelease(where)
	whereStr := fmt.Sprintf("%d", where)
	dp := b.dataPrefix()

	if _, err := b.executeOWN(pressCmd); err != nil {
		b.logger.WithError(err).WithField("where", where).Error("Failed additional lock press")
		return
	}
	b.publish(dp+"/lock/"+whereStr+"/state", "UNLOCKED", true)

	go func() {
		time.Sleep(1 * time.Second)
		if _, err := b.executeOWN(releaseCmd); err != nil {
			b.logger.WithError(err).WithField("where", where).Error("Failed additional lock release")
		}
		// Re-lock after 5s
		time.Sleep(5 * time.Second)
		b.publish(dp+"/lock/"+whereStr+"/state", "LOCKED", true)
	}()
}

// ==================== EVENT BUS SUBSCRIPTIONS ====================

func (b *MQTTBridge) setupEventSubscriptions() {
	if b.eventBus == nil {
		return
	}
	dp := b.dataPrefix()

	// Door unlock events
	b.eventBus.Subscribe("door.unlocked", func(event *events.Event) {
		b.publish(dp+"/lock/state", "UNLOCKED", true)
		b.incEvents()
		go func() {
			time.Sleep(5 * time.Second)
			b.publish(dp+"/lock/state", "LOCKED", true)
		}()
	})

	// Doorbell events (from port 20000 bus monitoring)
	b.eventBus.Subscribe("doorbell.pressed", func(event *events.Event) {
		b.mu.Lock()
		b.lastDoorbellTime = time.Now()
		b.mu.Unlock()
		b.publish(dp+"/doorbell/state", "ON", false)
		b.publishEvent("doorbell", "pressed", nil)
		b.incEvents()
		// When the doorbell rings the native video session is usually active: this is
		// when the cooperative snapshot works.
		b.captureDoorbellPhoto("doorbell")
	})

	// Physical button presses (from /dev/input/event0)
	b.eventBus.Subscribe("button.pressed", func(event *events.Event) {
		if data, ok := event.Data.(map[string]interface{}); ok {
			keyName, _ := data["key_name"].(string)
			b.mu.Lock()
			b.lastButtonPress = time.Now()
			b.lastButtonKey = keyName
			b.mu.Unlock()
			b.publish(dp+"/sensor/keypad", keyName, false)
		}
		b.incEvents()
	})

	// OpenWebNet bus events (any event from port 20000)
	b.eventBus.Subscribe("openwebnet.event", func(event *events.Event) {
		if data, ok := event.Data.(map[string]interface{}); ok {
			raw, _ := data["raw"].(string)
			if raw != "" && !b.isRepeatBusRaw(&b.lastBusEventRaw, raw) {
				b.publishEvent("bus_own", "frame", map[string]interface{}{"raw": raw})
			}
		}
		b.incEvents()
	})

	// Screen state changes
	b.eventBus.Subscribe("screen.changed", func(event *events.Event) {
		if data, ok := event.Data.(map[string]interface{}); ok {
			if state, ok := data["active"].(bool); ok {
				b.mu.Lock()
				b.displayOn = state
				b.mu.Unlock()
				b.publish(dp+"/display/state", boolState(state), true)
			}
		}
		b.incEvents()
	})

	// Floor/landing doorbell (landing doorbell), distinct from the entrance panel
	b.eventBus.Subscribe("doorbell.floor.pressed", func(event *events.Event) {
		b.publish(dp+"/doorbell_floor/state", "ON", false)
		b.publishEvent("doorbell_floor", "pressed", nil)
		b.incEvents()
		go func() {
			time.Sleep(3 * time.Second)
			b.publish(dp+"/doorbell_floor/state", "OFF", false)
		}()
	})

	// Real incoming SIP call (someone is calling the unit)
	b.eventBus.Subscribe("sip.call.incoming", func(event *events.Event) {
		caller := ""
		if data, ok := event.Data.(map[string]interface{}); ok {
			caller, _ = data["caller"].(string)
		}
		b.publish(dp+"/call/state", "INCOMING", false)
		if caller != "" {
			b.publish(dp+"/call/caller", caller, false)
		}
		b.publishEvent("call", "incoming", map[string]interface{}{"caller": caller})
		b.incEvents()
		b.captureDoorbellPhoto("incoming_call")
	})

	// SIP call ended
	b.eventBus.Subscribe("sip.call.ended", func(event *events.Event) {
		b.publish(dp+"/call/state", "IDLE", false)
		b.publishEvent("call", "ended", nil)
		b.incEvents()
	})

	// SIP call connected
	b.eventBus.Subscribe("sip.call.connected", func(event *events.Event) {
		b.publish(dp+"/call/state", "CONNECTED", false)
		b.publishEvent("call", "connected", nil)
		b.incEvents()
	})

	b.logger.Info("EventBus: Subscribed to 9 event types for MQTT publishing")

	// Multicast event subscriptions (if multicast listener is running)
	b.eventBus.Subscribe("multicast.message.raw", func(event *events.Event) {
		b.incEvents()
	})

	b.eventBus.Subscribe("multicast.message.OPEN", func(event *events.Event) {
		b.logger.Debug("Multicast OPEN message received via EventBus")
		b.incEvents()
	})

	b.logger.Info("EventBus: Subscribed to 7 event types (5 core + 2 multicast)")
}

// ==================== STATE PUBLISHING ====================

// publishAllStates publishes current state of all entities
func (b *MQTTBridge) publishAllStates() {
	dp := b.dataPrefix()

	// Lock default state
	b.publish(dp+"/lock/state", "LOCKED", true)

	// Switch states
	b.mu.RLock()
	voicemail := b.voicemailEnabled
	bell := b.doorbellSoundOn
	mute := b.muteOn
	display := b.displayOn
	sip := b.sipRegistered
	b.mu.RUnlock()

	b.publish(dp+"/voicemail/state", boolState(voicemail), true)
	b.publish(dp+"/doorbellsound/state", boolState(bell), true)
	b.publish(dp+"/mute/state", boolState(mute), true)
	b.publish(dp+"/display/state", boolState(display), true)

	// Doorbell default off
	b.publish(dp+"/doorbell/state", "OFF", true)

	// Call + floor doorbell initial states (so the HA entities show a value
	// immediately instead of "unknown" until the first event)
	b.publish(dp+"/call/state", "IDLE", true)
	b.publish(dp+"/doorbell_floor/state", "OFF", true)

	// WiFi signal: initial publish + start the periodic monitor once
	go b.publishWiFiSignal()
	b.wifiOnce.Do(func() { go b.wifiMonitorLoop(60 * time.Second) })

	// New entities: SIP status, system diagnostics
	b.publish(dp+"/sensor/sip_status", boolState(sip), true)
	b.publish(dp+"/sensor/system_diag", "starting", true)

	// GPIO pins initial state (all OFF until first read)
	gpioPins := []int{12, 13, 47, 49, 52, 54, 56, 58, 60, 154, 155, 176, 180}
	for _, pin := range gpioPins {
		b.publish(fmt.Sprintf("%s/gpio/%d/state", dp, pin), "OFF", true)
	}

	// Publish message stats if parser available
	b.PublishMessageStats()

	// Additional locks initial state (all locked)
	for _, lock := range b.config.AdditionalLocks {
		whereStr := fmt.Sprintf("%d", lock.Where)
		b.publish(dp+"/lock/"+whereStr+"/state", "LOCKED", true)
	}
}

// ==================== DEVICE STATE QUERIES ====================

// QueryVoicemailStatus queries the real answering machine state via *#8**40##.
// Parses the *#8**40*<vm_on>*<wm_on>*XXXX*X*XX## response and updates the MQTT state.
func (b *MQTTBridge) QueryVoicemailStatus() {
	response, err := b.executeOWNWithResponse(bticino.CmdVoicemailStatusQuery)
	if err != nil {
		b.logger.WithError(err).Warn("Could not query answering machine state")
		return
	}

	// Regex to parse: *#8**40*<vm_on>*<wm_on>*XXXX*X*XX##
	re := regexp.MustCompile(`\*#8\*\*40\*([01])\*([01])\*`)
	matches := re.FindStringSubmatch(response)
	if len(matches) >= 3 {
		vmEnabled := matches[1] == "1"
		wmEnabled := matches[2] == "1"

		b.mu.Lock()
		changed := b.voicemailEnabled != vmEnabled
		b.voicemailEnabled = vmEnabled
		b.mu.Unlock()

		b.publish(b.dataPrefix()+"/voicemail/state", boolState(vmEnabled), true)

		if changed {
			b.logger.Infof("Answering machine state updated from device: enabled=%v, welcome_msg=%v", vmEnabled, wmEnabled)
		} else {
			b.logger.Debugf("Answering machine state: enabled=%v, welcome_msg=%v (unchanged)", vmEnabled, wmEnabled)
		}
	} else {
		b.logger.WithField("response", response).Debug("Unrecognized voicemail query response")
	}
}

// QueryBellStatus queries the real doorbell sound state via *#8**33##.
// Response: *#8**33*0## (muted) or *#8**33*1## (unmuted).
func (b *MQTTBridge) QueryBellStatus() {
	response, err := b.executeOWNWithResponse(bticino.CmdBellQuery)
	if err != nil {
		b.logger.WithError(err).Warn("Could not query doorbell state")
		return
	}

	if strings.Contains(response, "*#8**33*0##") {
		b.mu.Lock()
		changed := b.doorbellSoundOn != false
		b.doorbellSoundOn = false
		b.mu.Unlock()
		b.publish(b.dataPrefix()+"/doorbellsound/state", "OFF", true)
		if changed {
			b.logger.Info("Doorbell state updated from device: muted (OFF)")
		}
	} else if strings.Contains(response, "*#8**33*1##") {
		b.mu.Lock()
		changed := b.doorbellSoundOn != true
		b.doorbellSoundOn = true
		b.mu.Unlock()
		b.publish(b.dataPrefix()+"/doorbellsound/state", "ON", true)
		if changed {
			b.logger.Info("Doorbell state updated from device: unmuted (ON)")
		}
	} else {
		b.logger.WithField("response", response).Debug("Unrecognized doorbell query response")
	}
}

// UpdateBellState updates the doorbell state from a real-time OWN bus event.
func (b *MQTTBridge) UpdateBellState(on bool) {
	b.mu.Lock()
	b.doorbellSoundOn = on
	b.mu.Unlock()
	b.publish(b.dataPrefix()+"/doorbellsound/state", boolState(on), true)
}

// UpdateVoicemailState updates the answering machine state from a real-time OWN bus event.
func (b *MQTTBridge) UpdateVoicemailState(enabled bool) {
	b.mu.Lock()
	b.voicemailEnabled = enabled
	b.mu.Unlock()
	b.publish(b.dataPrefix()+"/voicemail/state", boolState(enabled), true)
}

// PublishAdditionalLockEvent publishes an additional lock event detected on the OWN bus.
func (b *MQTTBridge) PublishAdditionalLockEvent(whereID string, state string) {
	dp := b.dataPrefix()
	b.publish(fmt.Sprintf("%s/lock/%s/state", dp, whereID), state, true)

	// Auto re-lock after 5 seconds (same pattern as main lock)
	if state == "UNLOCKED" {
		go func() {
			time.Sleep(5 * time.Second)
			b.publish(fmt.Sprintf("%s/lock/%s/state", dp, whereID), "LOCKED", true)
		}()
	}
}

// QuerySystemInfo queries system information via WHO=13 commands.
// Parses the responses and publishes them as JSON on the system_info sensor.
func (b *MQTTBridge) QuerySystemInfo() {
	info := make(map[string]string)

	// Query definition: {constant, key, response_prefix}
	queries := []struct {
		cmd    string
		key    string
		prefix string
		isMAC  bool
	}{
		{bticino.CmdQueryIP, "ip_address", "*#13**10*", false},
		{bticino.CmdQueryNetmask, "netmask", "*#13**11*", false},
		{bticino.CmdQueryMAC, "mac_address", "*#13**12*", true},
		{bticino.CmdQueryFWVer, "firmware_version", "*#13**16*", false},
		{bticino.CmdQueryHWVer, "hardware_version", "*#13**17*", false},
		{bticino.CmdQueryKernel, "kernel_version", "*#13**23*", false},
		{bticino.CmdQueryDistro, "distro_version", "*#13**24*", false},
	}

	for _, q := range queries {
		response, err := b.executeOWNWithResponse(q.cmd)
		if err != nil {
			b.logger.WithError(err).WithField("query", q.key).Warn("Error in WHO=13 query")
			continue
		}

		// Parse response: look for the line starting with the prefix
		for _, line := range strings.Split(response, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, q.prefix) {
				// Extract value: strip prefix and trailing ##
				value := strings.TrimPrefix(line, q.prefix)
				value = strings.TrimSuffix(value, "##")

				if q.isMAC {
					// MAC: convert decimal octets to hex with ':'
					info[q.key] = parseMAC(value)
				} else {
					// IP and versions: replace * with .
					info[q.key] = strings.ReplaceAll(value, "*", ".")
				}
				break
			}
		}

		// Delay between commands to respect BTicino timing
		time.Sleep(bticino.CommandRetryDelay)
	}

	// Static bridge info, alongside the firmware facts above.
	info["bridge_version"] = version.GetVersion()

	// Keep the facts for the periodic network refresh (IP can change under
	// DHCP while everything else here is reboot-static).
	b.mu.Lock()
	b.systemInfo = info
	b.mu.Unlock()

	// Publish as JSON on the sensor
	jsonData, err := json.Marshal(info)
	if err != nil {
		b.logger.WithError(err).Error("Error serializing system info")
		return
	}

	dp := b.dataPrefix()
	// Publish summary as the main sensor state
	summary := "unknown"
	if fw, ok := info["firmware_version"]; ok {
		summary = fw
	}
	b.publish(dp+"/sensor/system_info", summary, true)
	b.publish(dp+"/sensor/system_info/attributes", string(jsonData), true)

	b.logger.WithField("info", info).Info("System information published")
}

// parseMAC converts *-separated decimal octets to hex MAC format (AA:BB:CC:DD:EE:FF)
func parseMAC(starSeparated string) string {
	parts := strings.Split(starSeparated, "*")
	hexParts := make([]string, 0, len(parts))
	for _, p := range parts {
		var dec int
		fmt.Sscanf(p, "%d", &dec)
		hexParts = append(hexParts, fmt.Sprintf("%02x", dec))
	}
	return strings.Join(hexParts, ":")
}

// PublishTemperature publishes device temperature to MQTT
func (b *MQTTBridge) PublishTemperature(tempCelsius float64) {
	b.publish(b.dataPrefix()+"/sensor/temperature", fmt.Sprintf("%.1f", tempCelsius), true)
}

// PublishLEDStatus publishes individual LED states as binary_sensors
func (b *MQTTBridge) PublishLEDStatus(leds map[string]bool) {
	dp := b.dataPrefix()
	for name, on := range leds {
		// Skip mmc LEDs (SD card activity, not useful)
		if name == "mmc0::" || name == "mmc1::" || name == "mmc2::" {
			continue
		}
		b.publish(dp+"/led/"+name+"/state", boolState(on), true)
	}
}

// PublishButtonPress publishes a button press event
func (b *MQTTBridge) PublishButtonPress(keyCode int, keyName, action string) {
	b.publish(b.dataPrefix()+"/sensor/keypad", keyName, false)
	b.incEvents()
}

// PublishDoorbellEvent publishes a doorbell trigger
func (b *MQTTBridge) PublishDoorbellEvent(source, rawCommand string) {
	b.mu.Lock()
	b.lastDoorbellTime = time.Now()
	b.mu.Unlock()
	b.publish(b.dataPrefix()+"/doorbell/state", "ON", false)
	b.incEvents()
}

// PublishMessageStats publishes answering machine message stats
func (b *MQTTBridge) PublishMessageStats() {
	if b.messageParser == nil {
		return
	}
	dp := b.dataPrefix()

	status, err := b.messageParser.GetAnsweringMachineStatus()
	if err != nil {
		b.logger.WithError(err).Debug("Failed to get message stats for MQTT")
		return
	}

	b.publish(dp+"/sensor/new_messages", fmt.Sprintf("%d", status.NewMessages), true)
	b.publish(dp+"/sensor/total_messages", fmt.Sprintf("%d", status.TotalMessages), true)
	b.publish(dp+"/sensor/storage_used", fmt.Sprintf("%d", status.StorageUsedMB), true)
}

// PublishSIPStatus publishes SIP registration status to MQTT
func (b *MQTTBridge) PublishSIPStatus(registered bool) {
	b.mu.Lock()
	b.sipRegistered = registered
	b.mu.Unlock()
	b.publish(b.dataPrefix()+"/sensor/sip_status", boolState(registered), true)
}

// PublishSystemDiagnostics publishes system diagnostic info to MQTT
func (b *MQTTBridge) PublishSystemDiagnostics(status string) {
	b.publish(b.dataPrefix()+"/sensor/system_diag", status, true)
}

// isRepeatBusRaw reports whether raw repeats the previous OWN frame for one
// event-style output (separate baselines per output so a new frame still
// reaches every topic). Back-to-back duplicates carry no information.
func (b *MQTTBridge) isRepeatBusRaw(baseline *string, raw string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if raw != "" && raw == *baseline {
		return true
	}
	*baseline = raw
	return false
}

// PublishGPIOStates publishes individual GPIO pin states to MQTT
func (b *MQTTBridge) PublishGPIOStates(states map[int]bool) {
	dp := b.dataPrefix()
	for pin, on := range states {
		b.publish(fmt.Sprintf("%s/gpio/%d/state", dp, pin), boolState(on), true)
	}
}

// PublishActivityLog publishes a human-readable activity event to the activity_log sensor.
// description: human-readable text (e.g. "Doorbell: call from entrance panel")
// eventType: machine type (e.g. "doorbell_ring", "door_open", "camera_activated")
// rawCommand: original OWN command (e.g. "*8*1#1#4#21*16##")
func (b *MQTTBridge) PublishActivityLog(description, eventType, rawCommand string) {
	// Skip back-to-back duplicates of the same frame (idle keepalive noise).
	if rawCommand != "" && b.isRepeatBusRaw(&b.lastActivityRaw, rawCommand) {
		return
	}
	dp := b.dataPrefix()

	// Publish main state (readable text, not retained so it acts as an event)
	b.publish(dp+"/sensor/activity_log", description, false)

	// Publish JSON attributes for automations
	attrs := map[string]interface{}{
		"event_type":  eventType,
		"raw_command": rawCommand,
		"timestamp":   time.Now().Format(time.RFC3339),
	}
	jsonData, err := json.Marshal(attrs)
	if err == nil {
		b.publish(dp+"/sensor/activity_log/attributes", string(jsonData), false)
	}

	b.logger.WithFields(map[string]interface{}{
		"description": description,
		"event_type":  eventType,
		"raw_command": rawCommand,
	}).Info("MQTT: Activity log published")
	b.incEvents()
}

// PublishMessage is a public wrapper for external callers
func (b *MQTTBridge) PublishMessage(topic, payload string, retain bool) {
	b.publish(topic, payload, retain)
}

// ==================== COMMAND EXECUTION ====================

// executeOWN sends an OpenWebNet command via netcat (non-interfering)
func (b *MQTTBridge) executeOWN(command string) (bool, error) {
	b.logger.WithField("command", command).Debug("Executing OWN command via netcat")

	cmd := exec.Command("sh", "-c", fmt.Sprintf("echo '%s' | nc 0 30006", command))
	if err := cmd.Run(); err != nil {
		b.logger.WithError(err).WithField("command", command).Error("OWN command failed")
		return false, fmt.Errorf("netcat command %s failed: %w", command, err)
	}

	b.logger.WithField("command", command).Info("OWN command executed via netcat")
	return true, nil
}

// ExecuteCommand public wrapper (for compatibility)
func (b *MQTTBridge) ExecuteCommand(command string) (bool, error) {
	return b.executeOWN(command)
}

// executeOWNWithResponse sends an OWN command by opening a dedicated TCP command session
// to port 20000. Each call opens a new connection, sends *99*0## to open a session,
// then sends the command and reads the response (data + ACK) on the same socket.
// This is needed because port 30006 only returns ACK/NACK without response data.
func (b *MQTTBridge) executeOWNWithResponse(command string) (string, error) {
	b.logger.WithField("command", command).Debug("Executing OWN query via TCP command session (port 20000)")

	// Connect to port 20000 with a 5s timeout
	conn, err := net.DialTimeout("tcp", "127.0.0.1:20000", 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("error connecting to port 20000: %w", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)

	// readMessage reads a ##-terminated OWN message from the socket
	readMessage := func(timeout time.Duration) (string, error) {
		conn.SetReadDeadline(time.Now().Add(timeout))
		var msg strings.Builder
		for {
			b, err := reader.ReadByte()
			if err != nil {
				return msg.String(), err
			}
			msg.WriteByte(b)
			s := msg.String()
			if strings.HasSuffix(s, "##") {
				return s, nil
			}
		}
	}

	// Step 1: Read server greeting
	greeting, err := readMessage(3 * time.Second)
	if err != nil {
		return "", fmt.Errorf("error reading greeting: %w", err)
	}
	b.logger.WithField("greeting", greeting).Debug("OWN query session greeting")

	// If it requires authentication (*98*2##), we do not support queries via TCP in that case
	if strings.Contains(greeting, "*98*") {
		return "", fmt.Errorf("server requires HMAC authentication, query not supported")
	}

	// Step 2: Send command-session open *99*0##
	_, err = conn.Write([]byte("*99*0##"))
	if err != nil {
		return "", fmt.Errorf("error sending command session: %w", err)
	}

	// Step 3: Read session response (must be *#*1## = ACK)
	sessionResp, err := readMessage(3 * time.Second)
	if err != nil {
		return "", fmt.Errorf("error reading session response: %w", err)
	}
	if !strings.Contains(sessionResp, "*#*1##") {
		return "", fmt.Errorf("command session rejected: %s", sessionResp)
	}

	// Step 4: Send the query command
	_, err = conn.Write([]byte(command))
	if err != nil {
		return "", fmt.Errorf("error sending command %s: %w", command, err)
	}

	// Step 5: Read response(s) - may be data + ACK or NACK
	// Read first message (may be the data or directly ACK/NACK)
	firstResp, err := readMessage(3 * time.Second)
	if err != nil {
		return "", fmt.Errorf("error reading response to %s: %w", command, err)
	}

	// If NACK, the command was not understood
	if firstResp == "*#*0##" {
		return "", fmt.Errorf("command %s rejected (NACK)", command)
	}

	// If plain ACK (no data), return empty
	if firstResp == "*#*1##" {
		return "", nil
	}

	// It is the response data. Try to read the ACK that follows
	result := firstResp
	ackResp, err := readMessage(2 * time.Second)
	if err == nil {
		b.logger.WithField("ack", ackResp).Debug("ACK received after query data")
	}

	b.logger.WithFields(logrus.Fields{
		"command":  command,
		"response": result,
	}).Info("OWN query response received via TCP session")
	return result, nil
}

// ==================== MQTT HELPERS ====================

// Publish sends a message to an MQTT topic (implements MQTTPublisherInterface)
func (b *MQTTBridge) Publish(topic, payload string, retain bool) {
	if b.client == nil || !b.IsConnected() {
		return
	}
	token := b.client.Publish(topic, 1, retain, payload)
	go func() {
		if token.Wait() && token.Error() != nil {
			b.logger.WithError(token.Error()).WithField("topic", topic).Error("MQTT publish failed")
		}
	}()
}

// publish sends a message to an MQTT topic (internal wrapper)
func (b *MQTTBridge) publish(topic, payload string, retain bool) {
	b.Publish(topic, payload, retain)
}

func boolState(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}

func (b *MQTTBridge) incEvents() {
	b.mu.Lock()
	b.eventsPublished++
	b.mu.Unlock()
}

func (b *MQTTBridge) incCommands() {
	b.mu.Lock()
	b.commandsReceived++
	b.mu.Unlock()
}

// ==================== STATUS AND LIFECYCLE ====================

// GetMQTTStatus returns detailed MQTT status for web API
func (b *MQTTBridge) GetMQTTStatus() map[string]interface{} {
	b.mu.RLock()
	defer b.mu.RUnlock()

	status := map[string]interface{}{
		"connected":          b.isConnected,
		"broker":             fmt.Sprintf("%s:%d", b.config.MQTT.Host, b.config.MQTT.Port),
		"data_prefix":        b.dataPrefix(),
		"events_published":   b.eventsPublished,
		"commands_received":  b.commandsReceived,
		"entities":           39 + len(b.config.AdditionalLocks),
		"reconnect_attempts": b.reconnectAttempts,
	}

	if b.isConnected && !b.connectTime.IsZero() {
		status["connected_since"] = b.connectTime.Format(time.RFC3339)
		status["uptime"] = time.Since(b.connectTime).Round(time.Second).String()
	}
	if !b.lastDisconnectTime.IsZero() {
		status["last_disconnect"] = b.lastDisconnectTime.Format(time.RFC3339)
	}
	if !b.lastDoorbellTime.IsZero() {
		status["last_doorbell"] = b.lastDoorbellTime.Format(time.RFC3339)
	}
	if !b.lastButtonPress.IsZero() {
		status["last_button_press"] = b.lastButtonPress.Format(time.RFC3339)
		status["last_button_key"] = b.lastButtonKey
	}

	// Add connection events (no lock needed - already holding lock)
	if b.connectionEvents != nil {
		events := make([]ConnectionEvent, 0, 20)
		if !b.ceFull {
			for i := 0; i < b.cePos; i++ {
				if b.connectionEvents[i].Timestamp.IsZero() == false {
					events = append(events, b.connectionEvents[i])
				}
			}
		} else {
			for i := b.cePos; i < 20; i++ {
				if b.connectionEvents[i].Timestamp.IsZero() == false {
					events = append(events, b.connectionEvents[i])
				}
			}
			for i := 0; i < b.cePos; i++ {
				if b.connectionEvents[i].Timestamp.IsZero() == false {
					events = append(events, b.connectionEvents[i])
				}
			}
		}
		status["connection_events"] = events
	}

	return status
}

// Stop gracefully shuts down the MQTT bridge
func (b *MQTTBridge) Stop() {
	if b.client != nil {
		b.publish(b.dataPrefix()+"/bridge/state", "offline", true)
		time.Sleep(100 * time.Millisecond)
		b.client.Disconnect(250)
	}
}

// ==================== WEB SERVER COMPATIBILITY INTERFACE ====================

// SendOpenWebNetCommand implements BTicinoBridge interface for WebServer
func (b *MQTTBridge) SendOpenWebNetCommand(command string) error {
	_, err := b.executeOWN(command)
	return err
}

func (b *MQTTBridge) GetMulticastStats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":   b.IsConnected(),
		"running":   b.IsConnected(),
		"broker":    fmt.Sprintf("%s:%d", b.config.MQTT.Host, b.config.MQTT.Port),
		"connected": b.IsConnected(),
	}
}

func (b *MQTTBridge) GetEventBusStats() map[string]interface{} {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return map[string]interface{}{
		"enabled":            b.eventBus != nil,
		"total_events":       b.eventsPublished,
		"active_subscribers": 5,
	}
}

func (b *MQTTBridge) GetVideoStats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":              false,
		"sip_registered":       false,
		"rtsp_active_sessions": 0,
		"video_manager":        nil,
	}
}
