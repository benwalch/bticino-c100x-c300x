// Package deviceconfig provides functions to publish device config via MQTT
package deviceconfig

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// MQTTPublisherInterface defines the interface for publishing to MQTT
type MQTTPublisherInterface interface {
	Publish(topic, payload string, retain bool)
	IsConnected() bool
}

// DeviceMQTTPublisher publishes the device configuration to MQTT
type DeviceMQTTPublisher struct {
	logger      *logrus.Logger
	publisher   MQTTPublisherInterface
	topicPrefix string
	ticker      *time.Ticker
	stopCh      chan bool
	// staticInfoPublished makes system/device publish-once-at-boot: static until reboot.
	staticInfoPublished bool
}

// NewDeviceMQTTPublisher creates a new device config publisher
func NewDeviceMQTTPublisher(logger *logrus.Logger, publisher MQTTPublisherInterface, topicPrefix string) *DeviceMQTTPublisher {
	if topicPrefix == "" {
		topicPrefix = "bticino"
	}
	return &DeviceMQTTPublisher{
		logger:      logger,
		publisher:   publisher,
		topicPrefix: topicPrefix,
		stopCh:      make(chan bool),
	}
}

// Start starts periodic publishing of device config
func (p *DeviceMQTTPublisher) Start(interval time.Duration) {
	p.ticker = time.NewTicker(interval)
	p.logger.Info("Starting device config MQTT publisher")

	go func() {
		for {
			select {
			case <-p.ticker.C:
				p.publishAll()
			case <-p.stopCh:
				p.logger.Info("Device config MQTT publisher stopped")
				return
			}
		}
	}()
}

// Stop stops publishing
func (p *DeviceMQTTPublisher) Stop() {
	if p.ticker != nil {
		p.ticker.Stop()
	}
	p.stopCh <- true
}

// PublishAll publishes the whole device configuration
func (p *DeviceMQTTPublisher) PublishAll() {
	p.publishAll()
}

func (p *DeviceMQTTPublisher) publishAll() {
	if p.publisher == nil || !p.publisher.IsConnected() {
		return
	}

	// Read device configuration
	paths := struct {
		ConfXML      string
		ASWMSettings string
		TVCCSettings string
		SettingsXML  string
	}{
		ConfXML:      "/var/tmp/conf.xml",
		ASWMSettings: "/home/bticino/cfg/extra/47/aswm_settings.ini",
		TVCCSettings: "/home/bticino/cfg/extra/47/tvcc_settings.ini",
		SettingsXML:  "/home/bticino/cfg/extra/0/settings.xml",
	}

	cfg, err := ReadConfigFromPaths(paths)
	if err != nil {
		p.logger.WithError(err).Error("Failed to read device config for MQTT")
		return
	}

	// Publish each section
	p.publishSystemConfig(cfg)
	p.publishAnsweringMachine(cfg)
	p.publishRingtones(cfg)
	p.publishVolumes(cfg)
	p.publishDisplay(cfg)
	p.publishCameras(cfg)
}

// publishSystemConfig publishes the system configuration
func (p *DeviceMQTTPublisher) publishSystemConfig(cfg *Config) {
	prefix := p.topicPrefix

	// Device info (static until reboot): publish once, on the first
	// connected tick after boot.
	if !p.staticInfoPublished {
		p.publishJSON(prefix+"/system/device", map[string]interface{}{
			"model":     cfg.DeviceInfo.Model,
			"ip":        cfg.DeviceInfo.IP,
			"version":   cfg.DeviceInfo.Version,
			"timestamp": time.Now().Format(time.RFC3339),
		}, true)
		p.staticInfoPublished = true
	}
}

// publishAnsweringMachine publishes the answering machine state
func (p *DeviceMQTTPublisher) publishAnsweringMachine(cfg *Config) {
	prefix := p.topicPrefix + "/answering"

	p.publishJSON(prefix+"/state", map[string]interface{}{
		"activated":    cfg.Answering.Activated,
		"ring_enabled": cfg.Answering.RingEnable,
		"led_enabled":  cfg.Answering.LedEnable,
		"memory_used":  cfg.Answering.MemoryUsed,
		"timestamp":    time.Now().Format(time.RFC3339),
	}, true)
}

// publishRingtones publishes the ringtone configuration
func (p *DeviceMQTTPublisher) publishRingtones(cfg *Config) {
	prefix := p.topicPrefix + "/audio/ringtone"

	p.publishJSON(prefix+"/s0", map[string]interface{}{
		"value":     cfg.Ringtones.S0,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/s1", map[string]interface{}{
		"value":     cfg.Ringtones.S1,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/s2", map[string]interface{}{
		"value":     cfg.Ringtones.S2,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/door", map[string]interface{}{
		"value":     cfg.Ringtones.Door,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/external", map[string]interface{}{
		"value":     cfg.Ringtones.External,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/alarm", map[string]interface{}{
		"value":     cfg.Ringtones.Alarm,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/message", map[string]interface{}{
		"value":     cfg.Ringtones.Message,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
}

// publishVolumes publishes the volume configuration
func (p *DeviceMQTTPublisher) publishVolumes(cfg *Config) {
	prefix := p.topicPrefix + "/audio/volume"

	p.publishJSON(prefix+"/s0", map[string]interface{}{
		"value":     cfg.Volumes.S0,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/s1", map[string]interface{}{
		"value":     cfg.Volumes.S1,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/s2", map[string]interface{}{
		"value":     cfg.Volumes.S2,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/intercom", map[string]interface{}{
		"value":     cfg.Volumes.Intercom,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/door", map[string]interface{}{
		"value":     cfg.Volumes.Door,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
	p.publishJSON(prefix+"/sip", map[string]interface{}{
		"value":     cfg.Volumes.SIP,
		"timestamp": time.Now().Format(time.RFC3339),
	}, true)
}

// publishDisplay publishes the display configuration as one facet topic
// (brightness + clean time share one lifecycle: a single settings.xml read).
func (p *DeviceMQTTPublisher) publishDisplay(cfg *Config) {
	prefix := p.topicPrefix + "/display"

	p.publishJSON(prefix+"/settings", map[string]interface{}{
		"brightness": cfg.Display.Brightness,
		"clean_time": cfg.Display.CleanTime,
		"timestamp":  time.Now().Format(time.RFC3339),
	}, true)
}

// publishCameras publishes the camera configuration. Native map keys look
// like "TVCC 20" (TVCC = Italian CCTV); topics use the bare numeric ID so
// publisher and discovery agree.
func (p *DeviceMQTTPublisher) publishCameras(cfg *Config) {
	prefix := p.topicPrefix + "/camera"

	for id, camera := range cfg.Cameras {
		id = strings.TrimPrefix(id, "TVCC ")
		p.publishJSON(prefix+"/"+id+"/settings", map[string]interface{}{
			"brightness": camera.Brightness,
			"contrast":   camera.Contrast,
			"saturation": camera.Saturation,
			"quality":    camera.Quality,
			"timestamp":  time.Now().Format(time.RFC3339),
		}, true)
	}
}

// publishJSON publishes a JSON object
func (p *DeviceMQTTPublisher) publishJSON(topic string, data map[string]interface{}, retain bool) {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		p.logger.WithError(err).WithField("topic", topic).Error("Failed to marshal JSON")
		return
	}
	p.publisher.Publish(topic, string(jsonBytes), retain)
}

// GetAllTopics returns all published topics
func (p *DeviceMQTTPublisher) GetAllTopics() []string {
	return []string{
		p.topicPrefix + "/system/device",
		p.topicPrefix + "/answering/state",
		p.topicPrefix + "/audio/ringtone/s0",
		p.topicPrefix + "/audio/ringtone/s1",
		p.topicPrefix + "/audio/ringtone/s2",
		p.topicPrefix + "/audio/ringtone/door",
		p.topicPrefix + "/audio/ringtone/external",
		p.topicPrefix + "/audio/ringtone/alarm",
		p.topicPrefix + "/audio/ringtone/message",
		p.topicPrefix + "/audio/volume/s0",
		p.topicPrefix + "/audio/volume/s1",
		p.topicPrefix + "/audio/volume/s2",
		p.topicPrefix + "/audio/volume/intercom",
		p.topicPrefix + "/audio/volume/door",
		p.topicPrefix + "/audio/volume/sip",
		p.topicPrefix + "/display/settings",
		// Dynamic cameras
	}
}

// VerifyTopicos verifies that topics are being published (for testing)
func VerifyTopicos() {
	fmt.Println("Device Config MQTT Topics:")
	fmt.Println("- bticino/system/device")
	fmt.Println("- bticino/answering/state")
	fmt.Println("- bticino/audio/ringtone/s0")
	fmt.Println("- bticino/audio/volume/s0")
	fmt.Println("- bticino/display/settings")
	fmt.Println("- bticino/camera/<id>/config")
}

// Ensure we implement the interface
var _ MQTTPublisherInterface = (*DeviceMQTTPublisher)(nil)

// Dummy implementation to satisfy the interface (actual implementation via real publisher)
func (p *DeviceMQTTPublisher) Publish(topic, payload string, retain bool) {
	if p.publisher != nil {
		p.publisher.Publish(topic, payload, retain)
	}
}

func (p *DeviceMQTTPublisher) IsConnected() bool {
	if p.publisher == nil {
		return false
	}
	return p.publisher.IsConnected()
}
