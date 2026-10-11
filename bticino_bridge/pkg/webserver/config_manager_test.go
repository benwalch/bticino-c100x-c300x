package webserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bticino_bridge/pkg/config"

	"github.com/sirupsen/logrus"
)

func newTestCM(t *testing.T) *ConfigManager {
	t.Helper()
	dir := t.TempDir()
	return NewConfigManager(filepath.Join(dir, "config.yaml"), logrus.New())
}

func baseValidConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Bridge.Name = "test-bridge"
	cfg.OpenWebNet.Port = 30006
	cfg.Web.Port = 8082
	return cfg
}

// --- FromJSON: merge over the current config (avoids zeroing fields not sent) ---

func TestFromJSONPartialPreservesCurrentValues(t *testing.T) {
	cm := newTestCM(t)
	current := baseValidConfig()
	current.MQTT.Password = "secret"
	current.Streaming.LiveOnCall = true
	cm.config = current

	cfg, err := cm.FromJSON([]byte(`{"Bridge":{"Name":"new-name"}}`))
	if err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if cfg.Bridge.Name != "new-name" {
		t.Fatalf("expected updated name, got %q", cfg.Bridge.Name)
	}
	if cfg.MQTT.Password != "secret" {
		t.Fatal("a partial JSON must not erase the current MQTT password")
	}
	if !cfg.Streaming.LiveOnCall {
		t.Fatal("a partial JSON must not turn off streaming.live_on_call")
	}
}

func TestFromJSONDoesNotMutateCurrent(t *testing.T) {
	cm := newTestCM(t)
	cm.config = baseValidConfig()
	if _, err := cm.FromJSON([]byte(`{"Bridge":{"Name":"other"}}`)); err != nil {
		t.Fatalf("FromJSON: %v", err)
	}
	if cm.config.Bridge.Name != "test-bridge" {
		t.Fatal("FromJSON must not mutate the in-memory config (it only returns the candidate)")
	}
}

func TestFromJSONWithoutCurrentConfig(t *testing.T) {
	cm := newTestCM(t) // cm.config == nil
	cfg, err := cm.FromJSON([]byte(`{"Bridge":{"Name":"single"}}`))
	if err != nil || cfg.Bridge.Name != "single" {
		t.Fatalf("FromJSON without prior config must work: cfg=%v err=%v", cfg, err)
	}
}

// --- ValidateConfig: new rules ---

func hasErrorContaining(r ConfigValidationResult, sub string) bool {
	for _, e := range r.Errors {
		if strings.Contains(e, sub) {
			return true
		}
	}
	return false
}

func hasWarningContaining(r ConfigValidationResult, sub string) bool {
	for _, w := range r.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestValidateBaseConfigIsValid(t *testing.T) {
	cm := newTestCM(t)
	r := cm.ValidateConfig(baseValidConfig())
	if !r.Valid {
		t.Fatalf("base config should be valid, errors: %v", r.Errors)
	}
}

func TestValidateLogLevelEnum(t *testing.T) {
	cm := newTestCM(t)
	cfg := baseValidConfig()
	cfg.Bridge.LogLevel = "verbose"
	if r := cm.ValidateConfig(cfg); r.Valid || !hasErrorContaining(r, "log level") {
		t.Fatalf("expected log level error, got %+v", r)
	}
	cfg.Bridge.LogLevel = "debug"
	if r := cm.ValidateConfig(cfg); !r.Valid {
		t.Fatalf("'debug' must be valid: %v", r.Errors)
	}
}

func TestValidateSIPTransportAndPort(t *testing.T) {
	cm := newTestCM(t)
	cfg := baseValidConfig()
	cfg.SIP.Enabled = true
	cfg.SIP.ServerHost = "127.0.0.1"
	cfg.SIP.Username = "u"
	cfg.SIP.Transport = "sctp"
	cfg.SIP.ServerPort = 0
	r := cm.ValidateConfig(cfg)
	if r.Valid || !hasErrorContaining(r, "transport") || !hasErrorContaining(r, "SIP server port") {
		t.Fatalf("expected transport and SIP port errors, got %+v", r)
	}
	cfg.SIP.Transport = "tcp"
	cfg.SIP.ServerPort = 5060
	if r := cm.ValidateConfig(cfg); !r.Valid {
		t.Fatalf("SIP tcp:5060 must be valid: %v", r.Errors)
	}
}

func TestValidateMQTTPortRange(t *testing.T) {
	cm := newTestCM(t)
	cfg := baseValidConfig()
	cfg.MQTT.Enabled = true
	cfg.MQTT.Host = "192.168.1.3"
	cfg.MQTT.Port = 0
	if r := cm.ValidateConfig(cfg); r.Valid || !hasErrorContaining(r, "MQTT port") {
		t.Fatalf("expected MQTT port error, got %+v", r)
	}
}

func TestValidateRTSPPortZeroWhenStreamingEnabled(t *testing.T) {
	cm := newTestCM(t)
	cfg := baseValidConfig()
	cfg.Streaming.Enabled = true
	cfg.Streaming.RTSPPort = 0
	if r := cm.ValidateConfig(cfg); r.Valid || !hasErrorContaining(r, "RTSP port") {
		t.Fatalf("RTSPPort=0 with streaming enabled must be an error, got %+v", r)
	}
}

func TestValidateVideoBackendEnum(t *testing.T) {
	cm := newTestCM(t)
	cfg := baseValidConfig()
	cfg.Streaming.VideoBackend = "vlc"
	if r := cm.ValidateConfig(cfg); r.Valid || !hasErrorContaining(r, "video backend") {
		t.Fatalf("expected video backend error, got %+v", r)
	}
	cfg.Streaming.VideoBackend = config.VideoBackendAVMedia
	if r := cm.ValidateConfig(cfg); !r.Valid {
		t.Fatalf("'avmedia' must be valid: %v", r.Errors)
	}
}

func TestValidateDNSServersMustBeIPs(t *testing.T) {
	cm := newTestCM(t)
	cfg := baseValidConfig()
	cfg.Network.DNS.Servers = []string{"1.1.1.1", "not-an-ip"}
	if r := cm.ValidateConfig(cfg); r.Valid || !hasErrorContaining(r, "DNS") {
		t.Fatalf("expected DNS error, got %+v", r)
	}
}

func TestValidateHomeKitPortNumericString(t *testing.T) {
	cm := newTestCM(t)
	cfg := baseValidConfig()
	cfg.HomeKit.Enabled = true
	cfg.HomeKit.Pin = "12345678"
	cfg.HomeKit.Port = "eighty"
	if r := cm.ValidateConfig(cfg); r.Valid || !hasErrorContaining(r, "HomeKit port") {
		t.Fatalf("expected HomeKit port error, got %+v", r)
	}
	cfg.HomeKit.Port = "8081"
	if r := cm.ValidateConfig(cfg); !r.Valid {
		t.Fatalf("'8081' must be valid: %v", r.Errors)
	}
}

func TestValidateHomeKitPinDigits(t *testing.T) {
	cm := newTestCM(t)
	cfg := baseValidConfig()
	cfg.HomeKit.Enabled = true
	cfg.HomeKit.Port = "8081"
	cfg.HomeKit.Pin = "abcdefgh"
	if r := cm.ValidateConfig(cfg); r.Valid || !hasErrorContaining(r, "PIN") {
		t.Fatalf("non-numeric PIN must be an error, got %+v", r)
	}
}

func TestValidateVideoOnDemandWarning(t *testing.T) {
	cm := newTestCM(t)
	cfg := baseValidConfig()
	cfg.Streaming.VideoOnDemand = true
	r := cm.ValidateConfig(cfg)
	if !r.Valid {
		t.Fatalf("video_on_demand is valid (with warning): %v", r.Errors)
	}
	if !hasWarningContaining(r, "video_on_demand") {
		t.Fatalf("expected video_on_demand warning, got %+v", r.Warnings)
	}
}

// --- RestoreBackup: path traversal ---

func TestRestoreBackupRejectsPathOutsideBackupDir(t *testing.T) {
	cm := newTestCM(t)
	// Valid YAML file but OUTSIDE the backups directory
	outside := filepath.Join(t.TempDir(), "evil.yaml")
	if err := os.WriteFile(outside, []byte("bridge:\n  name: evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cm.RestoreBackup(outside); err == nil {
		t.Fatal("RestoreBackup must reject paths outside the backups directory")
	}
}
