package webserver

import (
	"encoding/json"
	"net/http"

	"bticino_bridge/pkg/bticino"
)

// Talk-back (upstream audio app/bridge → door station). EXPERIMENTAL.
//
// Mechanism (see _investigacion/docs/TALKBACK_AUDIO_ASCENDENTE_2026-07-09.md):
// the call media is plain RTP (the c300x uses "dummy" crypto in the SDP).
// Talk-back = (1) arm bidirectional audio with the OWN command
// *8*2#5#4*16## (bticino.CmdAnswerPickup), and (2) send Speex/8000 RTP (pt=110)
// to the door station's audio receive port (SDP-negotiated per session → passed
// as a parameter so the port observed in a live call can be tested).
//
// Endpoints:
//   POST /api/talkback/start   {host?, port, source, loop?, arm?}
//   POST /api/talkback/stop
//   GET  /api/talkback/status

func (ws *WebServer) handleTalkbackStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Host   string `json:"host"`   // RTP destination (default 127.0.0.1)
		Port   int    `json:"port"`   // door station audio receive port (required)
		PT     int    `json:"pt"`     // RTP payload type Speex/8000 (default 98, the c300x one)
		Source string `json:"source"` // path to an audio file (WAV, etc.) — required
		Loop   bool   `json:"loop"`   // repeat the file continuously
		Arm    *bool  `json:"arm"`    // send the arm command (default true)
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		ws.writeTalkbackJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "invalid JSON body",
		})
		return
	}
	if body.Port <= 0 || body.Source == "" {
		ws.writeTalkbackJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "'port' (>0) and 'source' (WAV path) are required",
		})
		return
	}

	arm := true
	if body.Arm != nil {
		arm = *body.Arm
	}

	result := map[string]interface{}{}

	// (1) Arm bidirectional audio (OWN *8*2#5#4*16##)
	if arm {
		if err := ws.bridge.SendOpenWebNetCommand(bticino.CmdAnswerPickup); err != nil {
			ws.logger.WithError(err).Warn("talkback: failed to send the arm command")
			result["arm_error"] = err.Error()
		} else {
			result["armed"] = bticino.CmdAnswerPickup
		}
	}

	// (2) Launch the uplink pipeline (Speex RTP → door station port)
	if err := ws.talkback.Start(body.Host, body.Port, body.PT, body.Source, body.Loop); err != nil {
		ws.writeTalkbackJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(), "arm": result,
		})
		return
	}

	result["success"] = true
	result["target"] = ws.talkback.Target()
	result["message"] = "talk-back started (experimental)"
	ws.writeTalkbackJSON(w, http.StatusOK, result)
}

func (ws *WebServer) handleTalkbackStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ws.talkback.Stop()
	ws.writeTalkbackJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "message": "talk-back stopped",
	})
}

func (ws *WebServer) handleTalkbackStatus(w http.ResponseWriter, r *http.Request) {
	ws.writeTalkbackJSON(w, http.StatusOK, map[string]interface{}{
		"active": ws.talkback.IsActive(),
		"target": ws.talkback.Target(),
	})
}

func (ws *WebServer) writeTalkbackJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
