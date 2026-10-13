// Package deviceconfig provides a watcher for real-time synchronization
package deviceconfig

import (
	"fmt"
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

// FileWatcher monitors changes in device configuration files
type FileWatcher struct {
	logger      *logrus.Logger
	publisher   MQTTPublisherInterface
	topicPrefix string
	watchPaths  []string
	stopCh      chan bool
	lastState   map[string]fileState
	throttleDur time.Duration
	lastPublish time.Time
}

// fileState represents the state of a watched file
type fileState struct {
	modTime  time.Time
	checksum int64
	size     int64
}

// NewFileWatcher creates a new file watcher
func NewFileWatcher(logger *logrus.Logger, publisher MQTTPublisherInterface, topicPrefix string) *FileWatcher {
	if topicPrefix == "" {
		topicPrefix = "bticino"
	}

	return &FileWatcher{
		logger:      logger,
		publisher:   publisher,
		topicPrefix: topicPrefix,
		watchPaths: []string{
			"/var/tmp/conf.xml",
			"/home/bticino/cfg/extra/47/aswm_settings.ini",
			"/home/bticino/cfg/extra/47/tvcc_settings.ini",
			"/home/bticino/cfg/extra/0/settings.xml",
		},
		stopCh:      make(chan bool),
		lastState:   make(map[string]fileState),
		throttleDur: 2 * time.Second, // Throttle to avoid floods
	}
}

// Start starts the watcher with polling
func (w *FileWatcher) Start() {
	w.logger.Info("Starting device config file watcher")

	// Initialize state
	for _, path := range w.watchPaths {
		if stat, err := os.Stat(path); err == nil {
			w.lastState[path] = fileState{
				modTime:  stat.ModTime(),
				size:     stat.Size(),
				checksum: stat.Size(), // Use size as simple checksum
			}
		}
	}

	go w.pollLoop()
}

// Stop stops the watcher
func (w *FileWatcher) Stop() {
	w.stopCh <- true
	w.logger.Info("Device config file watcher stopped")
}

// pollLoop checks for changes periodically
func (w *FileWatcher) pollLoop() {
	ticker := time.NewTicker(2 * time.Second) // Check every 2 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.checkChanges()
		case <-w.stopCh:
			return
		}
	}
}

// checkChanges checks whether any file has changed
func (w *FileWatcher) checkChanges() {
	if w.publisher == nil || !w.publisher.IsConnected() {
		return
	}

	changed := false

	for _, path := range w.watchPaths {
		stat, err := os.Stat(path)
		if err != nil {
			// File missing or error
			continue
		}

		currentState := fileState{
			modTime:  stat.ModTime(),
			size:     stat.Size(),
			checksum: stat.Size(),
		}

		lastState, exists := w.lastState[path]
		if !exists || currentState.modTime.After(lastState.modTime) || currentState.size != lastState.size {
			// There is a change
			w.lastState[path] = currentState
			changed = true
			w.logger.Infof("Config file changed: %s", path)
		}
	}

	// Throttle: do not publish more than once per throttleDur
	if changed && time.Since(w.lastPublish) > w.throttleDur {
		w.publishConfigChange()
		w.lastPublish = time.Now()
	}
}

// publishConfigChange publishes the updated configuration
func (w *FileWatcher) publishConfigChange() {
	w.logger.Info("Publishing config change due to file modification")

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
		w.logger.WithError(err).Error("Failed to read config after change")
		return
	}

	// Publish immediately (no retain for change events)
	w.publishSystemChange(cfg)
	w.publishAnsweringChange(cfg)
	w.publishAudioChange(cfg)
	w.publishDisplayChange(cfg)

	// Publish change notification topic
	w.publisher.Publish(w.topicPrefix+"/system/config_changed", time.Now().Format(time.RFC3339), false)

	w.logger.Info("Config change published successfully")
}

func (w *FileWatcher) publishSystemChange(cfg *Config) {
	prefix := w.topicPrefix + "/system"
	w.publisher.Publish(prefix+"/language", fmt.Sprintf(`{"language":"%s","change":"file_watcher"}`, cfg.Language), false)
	w.publisher.Publish(prefix+"/timezone", fmt.Sprintf(`{"timezone":"%s","change":"file_watcher"}`, cfg.Timezone), false)
}

func (w *FileWatcher) publishAnsweringChange(cfg *Config) {
	prefix := w.topicPrefix + "/answering"
	activated := "false"
	if cfg.Answering.Activated {
		activated = "true"
	}
	w.publisher.Publish(prefix+"/state", fmt.Sprintf(`{"activated":%s,"change":"file_watcher"}`, activated), false)
}

func (w *FileWatcher) publishAudioChange(cfg *Config) {
	prefix := w.topicPrefix + "/audio"
	w.publisher.Publish(prefix+"/ringtone/s0", fmt.Sprintf(`{"value":%d,"change":"file_watcher"}`, cfg.Ringtones.S0), false)
	w.publisher.Publish(prefix+"/volume/s0", fmt.Sprintf(`{"value":%d,"change":"file_watcher"}`, cfg.Volumes.S0), false)
	w.publisher.Publish(prefix+"/volume/door", fmt.Sprintf(`{"value":%d,"change":"file_watcher"}`, cfg.Volumes.Door), false)
}

func (w *FileWatcher) publishDisplayChange(cfg *Config) {
	prefix := w.topicPrefix + "/display"
	w.publisher.Publish(prefix+"/settings", fmt.Sprintf(`{"brightness":%d,"clean_time":%d}`, cfg.Display.Brightness, cfg.Display.CleanTime), false)
}

// GetWatchedFiles returns the files being watched
func (w *FileWatcher) GetWatchedFiles() []string {
	return w.watchPaths
}

// VerifyFileWatcher verifies the watcher structure (for testing)
func VerifyFileWatcher() {
	fmt.Println("Device Config File Watcher")
	fmt.Println("- Watch interval: 2 seconds")
	fmt.Println("- Throttle: 2 seconds")
	fmt.Println("- Watched files:")
	fmt.Println("  * /var/tmp/conf.xml")
	fmt.Println("  * /home/bticino/cfg/extra/47/aswm_settings.ini")
	fmt.Println("  * /home/bticino/cfg/extra/47/tvcc_settings.ini")
	fmt.Println("  * /home/bticino/cfg/extra/0/settings.xml")
	fmt.Println("- Change notification topic: bticino/system/config_changed")
}
