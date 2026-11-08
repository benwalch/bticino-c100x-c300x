// Package webserver provides handlers for the device configuration API
package webserver

import (
	"encoding/json"
	"net/http"
	"time"

	"bticino_bridge/pkg/deviceconfig"
)

// @Summary Get device configuration
// @Description Returns the complete device configuration from system files
// @Tags Device Configuration
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/config/device [get]
func (ws *WebServer) handleAPIDeviceConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Use the device paths
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

	cfg, err := deviceconfig.ReadConfigFromPaths(paths)
	if err != nil {
		ws.writeJSON(w, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	ws.writeJSON(w, map[string]interface{}{
		"success": true,
		"config":  cfg,
	})
}

// @Summary Get device language
// @Description Returns the current device language setting
// @Tags Device Configuration
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/config/language [get]
func (ws *WebServer) handleAPILanguage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	lang, err := deviceconfig.GetLanguage("/var/tmp/conf.xml")
	ws.logger.WithError(err).WithField("language", lang).Info("Language request")
	if err != nil {
		ws.writeJSON(w, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	ws.writeJSON(w, map[string]interface{}{
		"success":  true,
		"language": lang,
	})
}

// ensure we implement http.Handler if needed (for future use)
var _ = func() {
	// This ensures the handlers are registered
	_ = json.Marshal
	_ = time.Now()
}
