package webserver

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	"bticino_bridge/pkg/bticino"
)

// audioAllowedDirs son los directorios bajo los cuales se permite un WAV para
// /api/talkback/say. Evita lectura de fichero arbitrario (aunque la API ya va
// tras Basic Auth): la ruta debe ser absoluta, sin `..`, y quedar dentro de
// uno de estos árboles.
var audioAllowedDirs = []string{"/tmp", "/home/bticino/cfg/extra"}

// validateAudioFilePath valida que file sea una ruta absoluta, limpia (sin
// traversal) y contenida en uno de los directorios permitidos.
func validateAudioFilePath(file string) error {
	if file == "" {
		return fmt.Errorf("ruta vacía")
	}
	if !filepath.IsAbs(file) {
		return fmt.Errorf("debe ser una ruta absoluta")
	}
	clean := filepath.Clean(file)
	for _, dir := range audioAllowedDirs {
		d := filepath.Clean(dir)
		if clean == d || strings.HasPrefix(clean, d+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("fuera de los directorios permitidos (%s)", strings.Join(audioAllowedDirs, ", "))
}

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
//   POST /api/talkback/say?file=<ruta_wav>   (talk-back v2, ver audioCall/handleTalkbackSay)

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
	if err := validateAudioFilePath(body.Source); err != nil {
		ws.writeTalkbackJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "source not allowed: " + err.Error(),
		})
		return
	}
	// The door station is reached through the local media leg only.
	if body.Host != "" && !net.ParseIP(body.Host).IsLoopback() {
		ws.writeTalkbackJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "'host' must be a loopback address",
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

// handleTalkbackSay implementa el talk-back v2: el bridge origina su propia
// llamada de audio local a c300x (SDP real, no el dummy del self-INVITE de
// vídeo) y reproduce el WAV indicado por el altavoz de la puerta. A
// diferencia de /api/talkback/start (que exige conocer de antemano el
// puerto/clave SRTP de una sesión ya viva), aquí AudioCall.Say negocia su
// propia sesión de principio a fin (ver pkg/sip/audio_call.go).
//
//	POST /api/talkback/say?file=/ruta/al/audio.wav
func (ws *WebServer) handleTalkbackSay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ws.audioCall == nil {
		ws.writeTalkbackJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"success": false, "error": "talk-back v2 no disponible (SIP/OpenWebNet no inicializados)",
		})
		return
	}

	file := r.URL.Query().Get("file")
	if file == "" {
		ws.writeTalkbackJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "se requiere el parámetro 'file' (ruta al WAV)",
		})
		return
	}
	if err := validateAudioFilePath(file); err != nil {
		ws.writeTalkbackJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "ruta de audio no permitida: " + err.Error(),
		})
		return
	}

	if err := ws.audioCall.Say(file); err != nil {
		ws.logger.WithError(err).Warn("talkback/say: fallo al originar la llamada de audio o reproducir")
		ws.writeTalkbackJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": err.Error(),
		})
		return
	}

	ws.writeTalkbackJSON(w, http.StatusOK, map[string]interface{}{
		"success": true, "file": file, "message": "talk-back v2: WAV enviado",
	})
}

func (ws *WebServer) writeTalkbackJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
