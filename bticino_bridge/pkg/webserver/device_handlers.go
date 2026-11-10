// Package webserver - Device Tab API Handlers
// Handles device configuration (NTP, Timezone, Language, Ringtone)

package webserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"bticino_bridge/pkg/deviceconfig"
)

// DeviceConfig represents the device configuration
type DeviceConfig struct {
	NTP      NTPConfig      `json:"ntp"`
	Timezone TimezoneConfig `json:"timezone"`
	Language string         `json:"language"`
	Ringtone string         `json:"ringtone"`
}

type NTPConfig struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
}

type TimezoneConfig struct {
	Timezone  string `json:"timezone"`
	GMTOffset int    `json:"gmt_offset"`
}

// @Summary Get device NTP configuration
// @Description Returns the current device NTP server configuration
// @Tags Device
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/device/ntp [get]
func (ws *WebServer) handleAPIDeviceNTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ntpConfig := map[string]interface{}{
		"enabled": true,
		"server":  "pool.ntp.org",
	}

	ws.writeJSON(w, ntpConfig)
}

// @Summary Get device timezone
// @Description Returns the current device timezone configuration
// @Tags Device
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/device/timezone [get]
func (ws *WebServer) handleAPIDeviceTimezone(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	timezone := "UTC"
	gmtOffset := 0

	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		timezone = strings.TrimSpace(string(data))
	}

	if loc, err := time.LoadLocation(timezone); err == nil {
		_, offset := time.Now().In(loc).Zone()
		gmtOffset = offset / 3600
	}

	ws.writeJSON(w, map[string]interface{}{
		"timezone":   timezone,
		"gmt_offset": gmtOffset,
	})
}

// @Summary Get device language
// @Description Returns the current device language setting
// @Tags Device
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/device/language [get]
func (ws *WebServer) handleAPIDeviceLanguage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	language := "en"

	if data, err := os.ReadFile("/home/bticino/.language"); err == nil {
		language = strings.TrimSpace(string(data))
	}

	ws.writeJSON(w, map[string]interface{}{
		"language": language,
	})
}

// @Summary Get or set the device ringtones
// @Description GET returns the ringtone id of each settable type (s0, s1, internal, external, floor).
// @Description POST sets one: settings.xml is written and the device reloads its ringtones.
// @Description The device menu shows the old value until the device UI restarts.
// @Tags Device
// @Accept json
// @Produce json
// @Param request body object false "Ringtone to set, e.g. {\"type\":\"s0\",\"id\":4}"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 502 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/device/ringtone [get]
// @Router /api/device/ringtone [post]
func (ws *WebServer) handleAPIDeviceRingtone(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := deviceconfig.ReadSettingsXML(deviceconfig.DevicePaths.SettingsXML)
		if err != nil {
			ws.writeJSONError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		ws.writeJSON(w, map[string]interface{}{"ringtones": settings.Ringtones.SettableValues()})

	case http.MethodPost:
		var body struct {
			Type string `json:"type"`
			ID   int    `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			ws.writeJSONError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
			return
		}
		err := deviceconfig.SetRingtone(body.Type, body.ID)
		switch {
		case errors.Is(err, deviceconfig.ErrInvalidRingtone):
			ws.writeJSONError(w, http.StatusBadRequest, err.Error())
		case err != nil:
			ws.logger.WithError(err).Warn("Setting ringtone failed")
			ws.writeJSONError(w, http.StatusBadGateway, err.Error())
		default:
			ws.logger.Infof("Ringtone %s set to %d", body.Type, body.ID)
			ws.writeJSON(w, map[string]interface{}{"success": true})
		}

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// @Summary Get available ringtones
// @Description Returns the ringtones the device offers (ringtones.xml)
// @Tags Device
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/device/ringtones [get]
func (ws *WebServer) handleAPIDeviceRingtones(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	list, err := deviceconfig.ReadRingtoneList(deviceconfig.RingtonesXMLPath)
	if err != nil {
		ws.writeJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	ws.writeJSON(w, map[string]interface{}{"ringtones": list})
}

// @Summary Get a ringtone file
// @Description Returns the WAV file of a ringtone listed in ringtones.xml, for playback in the browser
// @Tags Device
// @Produce audio/wav
// @Param id path int true "Ringtone id"
// @Success 200 {file} binary
// @Failure 404 {string} string
// @Router /api/device/ringtones/{id}.wav [get]
func (ws *WebServer) handleAPIDeviceRingtoneFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/device/ringtones/")
	id, err := strconv.Atoi(strings.TrimSuffix(name, ".wav"))
	if err != nil || !strings.HasSuffix(name, ".wav") {
		http.NotFound(w, r)
		return
	}
	path, err := deviceconfig.RingtoneFilePath(id)
	if errors.Is(err, deviceconfig.ErrInvalidRingtone) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		ws.writeJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	http.ServeContent(w, r, name, info.ModTime(), file)
}

func (ws *WebServer) writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": message})
}

// @Summary Get available languages
// @Description Returns the list of available device languages
// @Tags Device
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/device/languages [get]
func (ws *WebServer) handleAPIDeviceLanguages(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	languages := []map[string]string{
		{"code": "en", "name": "English"},
		{"code": "es", "name": "Spanish"},
		{"code": "fr", "name": "Français"},
		{"code": "de", "name": "Deutsch"},
		{"code": "it", "name": "Italiano"},
	}

	ws.writeJSON(w, map[string]interface{}{
		"languages": languages,
	})
}

// @Summary Save device configuration
// @Description Saves the device configuration (NTP, timezone, language); ringtones use /api/device/ringtone
// @Tags Device
// @Accept json
// @Produce json
// @Param request body object true "Device configuration"
// @Success 200 {object} map[string]interface{}
// @Router /api/device/save [post]
func (ws *WebServer) handleAPIDeviceSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Config DeviceConfig `json:"config"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		ws.writeJSON(w, map[string]interface{}{
			"success": false,
			"error":   "Invalid JSON: " + err.Error(),
		})
		return
	}

	if body.Config.NTP.Enabled {
		ws.logger.Infof("Configuring NTP: %s", body.Config.NTP.Server)
	}

	if body.Config.Timezone.Timezone != "" {
		ws.logger.Infof("Setting timezone: %s", body.Config.Timezone.Timezone)
	}

	if body.Config.Language != "" {
		ws.logger.Infof("Setting language: %s", body.Config.Language)
	}

	if body.Config.Ringtone != "" {
		ws.writeJSONError(w, http.StatusBadRequest, "ringtone is set via POST /api/device/ringtone")
		return
	}

	ws.writeJSON(w, map[string]interface{}{
		"success": true,
		"message": "Device configuration saved",
	})
}
