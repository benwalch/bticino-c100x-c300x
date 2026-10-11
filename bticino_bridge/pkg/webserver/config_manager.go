// Package webserver provides configuration management for the web dashboard
package webserver

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"bticino_bridge/pkg/config"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// homekitPinRe: the HomeKit PIN is exactly 8 digits.
var homekitPinRe = regexp.MustCompile(`^\d{8}$`)

// ConfigManager manages the bridge configuration with backup and hot reload support
type ConfigManager struct {
	configPath    string
	backupDir     string
	maxBackups    int
	config        *config.Config
	configMutex   sync.RWMutex
	history       []ConfigChange
	historyMutex  sync.RWMutex
	logger        *logrus.Logger
	autoReload    bool
	reloadChan    chan bool
}

// ConfigChange represents a configuration change
type ConfigChange struct {
	Timestamp   time.Time              `json:"timestamp"`
	User        string                 `json:"user,omitempty"`
	Sections    []string               `json:"sections"`
	Changes     map[string]interface{} `json:"changes,omitempty"`
	BackupFile  string                 `json:"backup_file"`
	Restarted   bool                   `json:"restarted"`
	Success     bool                   `json:"success"`
	Error       string                 `json:"error,omitempty"`
}

// ConfigValidationResult represents the result of a validation
type ConfigValidationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(configPath string, logger *logrus.Logger) *ConfigManager {
	backupDir := filepath.Join(filepath.Dir(configPath), "backups")
	
	logger.Infof("ConfigManager initialized with config path: %s", configPath)
	
	return &ConfigManager{
		configPath:  configPath,
		backupDir:   backupDir,
		maxBackups:  10,
		config:      nil,
		history:     make([]ConfigChange, 0),
		logger:      logger,
		autoReload:  false,
		reloadChan:  make(chan bool, 1),
	}
}

// LoadConfig loads the configuration from the YAML file
func (cm *ConfigManager) LoadConfig() (*config.Config, error) {
	cm.configMutex.Lock()
	defer cm.configMutex.Unlock()

	data, err := ioutil.ReadFile(cm.configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &config.Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config YAML: %w", err)
	}

	cm.config = cfg
	cm.logger.Infof("Configuration loaded from %s", cm.configPath)
	return cfg, nil
}

// GetConfig returns the current configuration
func (cm *ConfigManager) GetConfig() *config.Config {
	cm.configMutex.RLock()
	defer cm.configMutex.RUnlock()
	return cm.config
}

// SaveConfig saves the configuration to the YAML file
func (cm *ConfigManager) SaveConfig(cfg *config.Config, user string) error {
	cm.configMutex.Lock()
	defer cm.configMutex.Unlock()

	// Create backup before saving
	backupFile, err := cm.createBackup()
	if err != nil {
		cm.logger.WithError(err).Warn("Failed to create backup, proceeding anyway")
	}

	// Serialize to YAML
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write file
	if err := ioutil.WriteFile(cm.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	// Update in-memory configuration
	oldConfig := cm.config
	cm.config = cfg

	// Record change
	sections := cm.detectChanges(oldConfig, cfg)
	cm.addHistoryEntry(ConfigChange{
		Timestamp:  time.Now(),
		User:       user,
		Sections:   sections,
		BackupFile: backupFile,
		Restarted:  false,
		Success:    true,
	})

	cm.logger.Infof("Configuration saved to %s (backup: %s)", cm.configPath, backupFile)
	return nil
}

// ValidateConfig validates the configuration
func (cm *ConfigManager) ValidateConfig(cfg *config.Config) ConfigValidationResult {
	result := ConfigValidationResult{
		Valid:    true,
		Errors:   make([]string, 0),
		Warnings: make([]string, 0),
	}

	// Validate basic configuration
	if cfg.Bridge.Name == "" {
		result.Errors = append(result.Errors, "Bridge name is required")
		result.Valid = false
	}

	// Validate OpenWebNet
	if cfg.OpenWebNet.Port <= 0 || cfg.OpenWebNet.Port > 65535 {
		result.Errors = append(result.Errors, "OpenWebNet port must be between 1-65535")
		result.Valid = false
	}

	// Validate Web
	if cfg.Web.Port <= 0 || cfg.Web.Port > 65535 {
		result.Errors = append(result.Errors, "Web port must be between 1-65535")
		result.Valid = false
	}

	if cfg.Web.Port == cfg.OpenWebNet.Port {
		result.Errors = append(result.Errors, "Web port and OpenWebNet port cannot be the same")
		result.Valid = false
	}

	// Validate log level (enum; empty = use default)
	if cfg.Bridge.LogLevel != "" {
		switch cfg.Bridge.LogLevel {
		case "debug", "info", "warn", "error":
		default:
			result.Errors = append(result.Errors, "Invalid log level (use debug/info/warn/error)")
			result.Valid = false
		}
	}

	// Validate SIP
	if cfg.SIP.Enabled {
		if cfg.SIP.ServerHost == "" {
			result.Warnings = append(result.Warnings, "SIP enabled but no server host configured")
		}
		if cfg.SIP.Username == "" {
			result.Warnings = append(result.Warnings, "SIP enabled but no username configured")
		}
		if cfg.SIP.ServerPort <= 0 || cfg.SIP.ServerPort > 65535 {
			result.Errors = append(result.Errors, "SIP server port must be between 1-65535")
			result.Valid = false
		}
		if cfg.SIP.Transport != "" {
			switch cfg.SIP.Transport {
			case "tcp", "udp", "tls":
			default:
				result.Errors = append(result.Errors, "Invalid SIP transport (use tcp/udp/tls)")
				result.Valid = false
			}
		}
	}

	// Validate MQTT
	if cfg.MQTT.Enabled {
		if cfg.MQTT.Host == "" {
			result.Errors = append(result.Errors, "MQTT enabled but no host configured")
			result.Valid = false
		}
		if cfg.MQTT.Port <= 0 || cfg.MQTT.Port > 65535 {
			result.Errors = append(result.Errors, "MQTT port must be between 1-65535")
			result.Valid = false
		}
	}

	// Validate Streaming: with streaming enabled the RTSP port is required
	// (0 no longer skips the check); when disabled, only the range is validated.
	if cfg.Streaming.Enabled || cfg.Streaming.RTSPPort != 0 {
		if cfg.Streaming.RTSPPort <= 0 || cfg.Streaming.RTSPPort > 65535 {
			result.Errors = append(result.Errors, "RTSP port must be between 1-65535")
			result.Valid = false
		}
	}
	if cfg.Streaming.VideoBackend != "" {
		switch cfg.Streaming.VideoBackend {
		case config.VideoBackendAVMedia, config.VideoBackendGStreamer, config.VideoBackendAuto:
		default:
			result.Errors = append(result.Errors, "Invalid video backend (use avmedia/gstreamer/auto)")
			result.Valid = false
		}
	}
	if cfg.Streaming.VideoOnDemand {
		result.Warnings = append(result.Warnings, "video_on_demand competes with the native firmware and may reboot the device — enable only on a validated setup")
	}

	// Validate DNS (must be IPs)
	for _, s := range cfg.Network.DNS.Servers {
		if net.ParseIP(strings.TrimSpace(s)) == nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Invalid DNS server %q (must be an IP address)", s))
			result.Valid = false
		}
	}

	// Validate HomeKit
	if cfg.HomeKit.Enabled {
		if p, err := strconv.Atoi(cfg.HomeKit.Port); err != nil || p < 1 || p > 65535 {
			result.Errors = append(result.Errors, "HomeKit port must be a number between 1-65535")
			result.Valid = false
		}
		if !homekitPinRe.MatchString(cfg.HomeKit.Pin) {
			result.Errors = append(result.Errors, "HomeKit PIN must be exactly 8 digits")
			result.Valid = false
		}
	}

	// Validate web auth: enabling it without a password would leave the server in 503
	// (fail-closed) — better to reject saving that state from the dashboard.
	if cfg.Security.WebAuthRequired && cfg.Security.WebPassword == "" {
		result.Errors = append(result.Errors, "web_auth_required needs a non-empty web_password")
		result.Valid = false
	}
	if !cfg.Security.WebAuthRequired {
		result.Warnings = append(result.Warnings, "Web/API served without authentication (security.web_auth_required=false)")
	}

	// Validate external servers (privacy warnings)
	if cfg.Servers.Cloud.Enabled {
		result.Warnings = append(result.Warnings, "Cloud connection enabled - data will be sent to Netatmo servers")
	}

	if cfg.Servers.Logging.Enabled {
		result.Warnings = append(result.Warnings, "Remote logging enabled - telemetry will be sent to BTicino servers")
	}

	return result
}

// CreateBackup creates a backup of the current configuration
func (cm *ConfigManager) CreateBackup() (string, error) {
	cm.configMutex.RLock()
	defer cm.configMutex.RUnlock()
	return cm.createBackup()
}

func (cm *ConfigManager) createBackup() (string, error) {
	// Create backup directory if it does not exist
	if err := os.MkdirAll(cm.backupDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Read current file
	data, err := ioutil.ReadFile(cm.configPath)
	if err != nil {
		return "", fmt.Errorf("failed to read config file: %w", err)
	}

	// Generate timestamped file name
	timestamp := time.Now().Format("20060102_150405")
	backupFile := filepath.Join(cm.backupDir, fmt.Sprintf("config_%s.yaml", timestamp))

	// Write backup
	if err := ioutil.WriteFile(backupFile, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write backup file: %w", err)
	}

	// Clean up old backups
	cm.cleanupOldBackups()

	cm.logger.Infof("Backup created: %s", backupFile)
	return backupFile, nil
}

// RestoreBackup restores a backup
func (cm *ConfigManager) RestoreBackup(backupFile string) error {
	cm.configMutex.Lock()
	defer cm.configMutex.Unlock()

	// The backup MUST be inside the backups directory: the path arrives from
	// the unauthenticated API and without this check it would allow restoring
	// any YAML on the system (path traversal).
	absDir, err := filepath.Abs(cm.backupDir)
	if err != nil {
		return fmt.Errorf("failed to resolve backup dir: %w", err)
	}
	absFile, err := filepath.Abs(backupFile)
	if err != nil {
		return fmt.Errorf("failed to resolve backup path: %w", err)
	}
	if rel, err := filepath.Rel(absDir, absFile); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("backup file must be inside the backups directory")
	}

	// Verify that the backup exists
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		return fmt.Errorf("backup file does not exist: %s", backupFile)
	}

	// Read backup
	data, err := ioutil.ReadFile(backupFile)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	// Back up the current configuration before restoring
	_, err = cm.createBackup()
	if err != nil {
		cm.logger.WithError(err).Warn("Failed to create pre-restore backup")
	}

	// Write backup over current config
	if err := ioutil.WriteFile(cm.configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to restore config file: %w", err)
	}

	// Reload configuration
	cfg := &config.Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("failed to parse restored config: %w", err)
	}

	cm.config = cfg

	cm.addHistoryEntry(ConfigChange{
		Timestamp:  time.Now(),
		Sections:   []string{"restore"},
		BackupFile: backupFile,
		Restarted:  false,
		Success:    true,
	})

	cm.logger.Infof("Configuration restored from %s", backupFile)
	return nil
}

// GetHistory returns the change history
func (cm *ConfigManager) GetHistory() []ConfigChange {
	cm.historyMutex.RLock()
	defer cm.historyMutex.RUnlock()

	// Return a copy to avoid race conditions
	history := make([]ConfigChange, len(cm.history))
	copy(history, cm.history)
	return history
}

// GetBackups returns the list of available backups
func (cm *ConfigManager) GetBackups() ([]string, error) {
	files, err := ioutil.ReadDir(cm.backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read backup directory: %w", err)
	}

	// Filter and sort YAML files
	var backups []string
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".yaml") {
			backups = append(backups, filepath.Join(cm.backupDir, file.Name()))
		}
	}

	// Sort by name (timestamp)
	sort.Strings(backups)

	return backups, nil
}

// RequestReload requests a hot reload of the configuration
func (cm *ConfigManager) RequestReload() {
	if cm.autoReload {
		select {
		case cm.reloadChan <- true:
			cm.logger.Info("Configuration reload requested")
		default:
			cm.logger.Warn("Reload channel full, ignoring request")
		}
	}
}

// GetReloadChannel returns the reload channel
func (cm *ConfigManager) GetReloadChannel() <-chan bool {
	return cm.reloadChan
}

// EnableAutoReload enables automatic hot reload
func (cm *ConfigManager) EnableAutoReload() {
	cm.autoReload = true
}

// CleanupOldBackups removes old backups keeping only the last maxBackups
func (cm *ConfigManager) cleanupOldBackups() {
	backups, err := cm.GetBackups()
	if err != nil {
		cm.logger.WithError(err).Warn("Failed to get backups for cleanup")
		return
	}

	// Delete backups if they exceed the maximum
	for len(backups) > cm.maxBackups {
		oldFile := backups[0]
		if err := os.Remove(oldFile); err != nil {
			cm.logger.WithError(err).Warnf("Failed to delete old backup: %s", oldFile)
		} else {
			cm.logger.Infof("Deleted old backup: %s", oldFile)
		}
		backups = backups[1:]
	}
}

// addHistoryEntry adds an entry to the history
func (cm *ConfigManager) addHistoryEntry(entry ConfigChange) {
	cm.historyMutex.Lock()
	defer cm.historyMutex.Unlock()

	// Limit history to 100 entries
	if len(cm.history) >= 100 {
		cm.history = cm.history[1:]
	}

	cm.history = append(cm.history, entry)
}

// detectChanges detects which sections changed
func (cm *ConfigManager) detectChanges(old, new *config.Config) []string {
	var sections []string

	if old == nil || new == nil {
		return []string{"unknown"}
	}

	// Compare main sections
	if old.Bridge.Name != new.Bridge.Name {
		sections = append(sections, "bridge")
	}

	if old.OpenWebNet.Port != new.OpenWebNet.Port ||
		old.OpenWebNet.Host != new.OpenWebNet.Host {
		sections = append(sections, "openwebnet")
	}

	if old.SIP.ServerHost != new.SIP.ServerHost ||
		old.SIP.ServerPort != new.SIP.ServerPort ||
		old.SIP.Username != new.SIP.Username {
		sections = append(sections, "sip")
	}

	if old.MQTT.Host != new.MQTT.Host ||
		old.MQTT.Port != new.MQTT.Port {
		sections = append(sections, "mqtt")
	}

	if old.Web.Port != new.Web.Port {
		sections = append(sections, "web")
	}

	if old.Streaming.RTSPPort != new.Streaming.RTSPPort {
		sections = append(sections, "streaming")
	}

	if old.HomeKit.Pin != new.HomeKit.Pin {
		sections = append(sections, "homekit")
	}

	if len(sections) == 0 {
		sections = append(sections, "minor")
	}

	return sections
}

// ToJSON serializes the configuration to JSON
func (cm *ConfigManager) ToJSON() ([]byte, error) {
	cm.configMutex.RLock()
	defer cm.configMutex.RUnlock()

	return json.MarshalIndent(cm.config, "", "  ")
}

// FromJSON deserializes the configuration from JSON. The candidate starts from a
// COPY of the current config and the JSON is applied on top: a partial JSON (e.g.
// the legacy UI one) does not zero out fields it does not send. Does not mutate cm.config.
func (cm *ConfigManager) FromJSON(data []byte) (*config.Config, error) {
	cm.configMutex.RLock()
	current := cm.config
	cm.configMutex.RUnlock()

	cfg := &config.Config{}
	if current != nil {
		raw, err := yaml.Marshal(current)
		if err != nil {
			return nil, fmt.Errorf("failed to snapshot current config: %w", err)
		}
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("failed to copy current config: %w", err)
		}
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config JSON: %w", err)
	}
	return cfg, nil
}
