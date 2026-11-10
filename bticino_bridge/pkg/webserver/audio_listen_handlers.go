package webserver

import (
	"encoding/json"
	"net/http"
)

// AudioListenController is the minimal interface the /api/audio/listen/*
// endpoints need to force arming and disarming the passive door listening
// (pkg/sip/audio_listen_on_call.go: AudioListenOnCall). It is defined here
// instead of using *sip.AudioListenOnCall directly so webserver tests can
// inject a fake without real RTP/ffmpeg. It matches the exported methods of
// AudioListenOnCall exactly. Unlike DuplexController, Start returns no error:
// arming is idempotent and purely passive (tap + decoder), with no "busy"
// state to refuse.
type AudioListenController interface {
	Start(reason string)
	Stop()
	IsActive() bool
}

// SetAudioListenController injects the controller of the passive listening
// (tap lo:5000) used by POST/GET /api/audio/listen/*.
func (ws *WebServer) SetAudioListenController(alc AudioListenController) {
	ws.audioListenController = alc
}

// Local validation endpoints for audio listening (passive mirror of the
// native audio leg, see pkg/sip/audio_listen_on_call.go). Gated by
// audio.listen_tap_enabled: when it is off in the config, all three return
// 404 (fail-closed, they do not even reveal that the endpoint exists) instead
// of 503 "unavailable".
//
// These endpoints are for VALIDATION on the device (forcing the arming while
// testing calls from the app); the MAIN arming path is bus events
// (doorbell.pressed, sip.call.*), wired in cmd/main.go, exactly like
// LiveVideoOnCall.
//
//	POST /api/audio/listen/start
//	POST /api/audio/listen/stop
//	GET  /api/audio/listen/status

func (ws *WebServer) handleAudioListenStart(w http.ResponseWriter, r *http.Request) {
	if !ws.config.Audio.ListenTapEnabled {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ws.audioListenController == nil {
		ws.writeAudioListenJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "audio-listen unavailable (SIP not initialized)",
		})
		return
	}

	ws.audioListenController.Start("api")

	// 202: arming starts the decoder (goroutine) and the tap; capture keeps
	// running in the background. No sensitive data in the body.
	ws.writeAudioListenJSON(w, http.StatusAccepted, map[string]interface{}{
		"success": true,
		"active":  ws.audioListenController.IsActive(),
	})
}

func (ws *WebServer) handleAudioListenStop(w http.ResponseWriter, r *http.Request) {
	if !ws.config.Audio.ListenTapEnabled {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ws.audioListenController == nil {
		ws.writeAudioListenJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "audio-listen unavailable (SIP not initialized)",
		})
		return
	}

	ws.audioListenController.Stop()
	ws.writeAudioListenJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (ws *WebServer) handleAudioListenStatus(w http.ResponseWriter, r *http.Request) {
	if !ws.config.Audio.ListenTapEnabled {
		http.NotFound(w, r)
		return
	}
	// active is authoritative (controller.IsActive()), not a mirror flag:
	// disarming can come from the automatic guard or the natural end of the
	// decoder, without /stop.
	active := ws.audioListenController != nil && ws.audioListenController.IsActive()

	ws.writeAudioListenJSON(w, http.StatusOK, map[string]interface{}{
		"active":    active,
		"sink_path": ws.config.Audio.ListenSinkPath,
	})
}

func (ws *WebServer) writeAudioListenJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
