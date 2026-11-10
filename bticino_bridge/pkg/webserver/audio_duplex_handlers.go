package webserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"bticino_bridge/pkg/sip"
)

// DuplexController is the minimal interface the /api/audio/duplex/*
// endpoints need to open and close the full-duplex validation leg
// (pkg/sip/audio_call.go: AudioCall.OpenDuplex/Close). It is defined here
// instead of using *sip.AudioCall directly so webserver tests can inject a
// fake without real SIP/RTP.
type DuplexController interface {
	OpenDuplex(sinkPath, talkWav string) (sip.AudioAnswer, error)
	Close() error
	// IsActive is the source of truth for /status: the leg can close by
	// itself (natural end of the receiver, see OpenDuplex in
	// pkg/sip/audio_call.go) without going through /stop, so a mirror flag in
	// the webserver would go stale after that close.
	IsActive() bool
}

// SetDuplexController injects the controller of the full-duplex validation
// leg used by POST/GET /api/audio/duplex/*.
func (ws *WebServer) SetDuplexController(dc DuplexController) {
	ws.duplexController = dc
}

// Local validation endpoints for full-duplex audio on the bridge's own leg
// (WHO=8, see pkg/sip/audio_call.go). Gated by audio.duplex_enabled: when it
// is off in the config, all three return 404 (fail-closed, they do not even
// reveal that the endpoint exists) instead of 503 "unavailable".
//
//	POST /api/audio/duplex/start?talk=<optional wav>
//	POST /api/audio/duplex/stop
//	GET  /api/audio/duplex/status

func (ws *WebServer) handleAudioDuplexStart(w http.ResponseWriter, r *http.Request) {
	if !ws.config.Audio.DuplexEnabled {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ws.duplexController == nil {
		ws.writeAudioDuplexJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "audio duplex unavailable (SIP not initialized)",
		})
		return
	}

	talk := r.URL.Query().Get("talk")
	if talk != "" {
		if err := validateAudioFilePath(talk); err != nil {
			ws.writeAudioDuplexJSON(w, http.StatusBadRequest, map[string]interface{}{
				"success": false, "error": "talk not allowed: " + err.Error(),
			})
			return
		}
	}
	answer, err := ws.duplexController.OpenDuplex(ws.config.Audio.DuplexSinkPath, talk)
	if err != nil {
		if errors.Is(err, sip.ErrAudioLegBusy) {
			ws.writeAudioDuplexJSON(w, http.StatusConflict, map[string]interface{}{
				"success": false, "error": err.Error(),
			})
			return
		}
		ws.logger.WithError(err).Warn("audio/duplex/start: opening the full-duplex leg failed")
		ws.writeAudioDuplexJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	// 202: the leg is open (OpenDuplex returns after the setup:
	// INVITE/answer/arming), but audio capture and playback keep running in
	// the background. crypto_key is never included: it is the session's SRTP
	// key and must not leave over HTTP.
	ws.writeAudioDuplexJSON(w, http.StatusAccepted, map[string]interface{}{
		"success":      true,
		"port":         answer.Port,
		"payload_type": answer.PayloadType,
		"crypto_suite": answer.CryptoSuite,
	})
}

func (ws *WebServer) handleAudioDuplexStop(w http.ResponseWriter, r *http.Request) {
	if !ws.config.Audio.DuplexEnabled {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ws.duplexController == nil {
		ws.writeAudioDuplexJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "audio duplex unavailable (SIP not initialized)",
		})
		return
	}

	err := ws.duplexController.Close()

	if err != nil {
		ws.logger.WithError(err).Warn("audio/duplex/stop: closing the full-duplex leg failed")
		ws.writeAudioDuplexJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}
	ws.writeAudioDuplexJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

func (ws *WebServer) handleAudioDuplexStatus(w http.ResponseWriter, r *http.Request) {
	if !ws.config.Audio.DuplexEnabled {
		http.NotFound(w, r)
		return
	}
	// active is authoritative (controller.IsActive()), not a mirror flag: the
	// leg can close by itself (natural end of the receiver) without /stop, and
	// a local flag would go stale in that case.
	active := ws.duplexController != nil && ws.duplexController.IsActive()

	ws.writeAudioDuplexJSON(w, http.StatusOK, map[string]interface{}{
		"active":    active,
		"sink_path": ws.config.Audio.DuplexSinkPath,
	})
}

func (ws *WebServer) writeAudioDuplexJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
