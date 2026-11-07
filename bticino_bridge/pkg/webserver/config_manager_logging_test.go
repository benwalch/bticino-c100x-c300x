package webserver

import (
	"strings"
	"testing"

	"bticino_bridge/pkg/config"
)

func TestValidateConfigRejectsBadLogging(t *testing.T) {
	manager := &ConfigManager{}
	cfg := config.GetDefaultConfig()
	cfg.Logging.File = "/home/bticino/cfg/extra/config.yaml"
	cfg.Logging.MaxSize = "100GB"
	result := manager.ValidateConfig(cfg)
	joined := strings.Join(result.Errors, "; ")
	if result.Valid || !strings.Contains(joined, "logging.file") || !strings.Contains(joined, "exceeds") {
		t.Errorf("valid %v, errors %q", result.Valid, joined)
	}

	cfg = config.GetDefaultConfig()
	result = manager.ValidateConfig(cfg)
	if strings.Contains(strings.Join(result.Errors, "; "), "logging") {
		t.Errorf("default logging refused: %v", result.Errors)
	}
}
