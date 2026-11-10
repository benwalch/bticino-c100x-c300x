package webserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"bticino_bridge/pkg/bticino"
)

// Talk-back v1 (answering machine greeting → DOOR speaker).
//
// Cooperative path, validated live (2026-08-20): the native answering
// machine (bt_answering_machine, a liblinphone SIP client) plays its greeting
// towards the bus and the door (DSP state EN_AUDIO_PLAY_LOC_SCS) when it
// answers an unanswered door call by itself. If the greeting file is replaced
// with ours and the answering machine is on, the visitor at the entrance
// panel hears OUR audio.
//
// Unlike talk-back v2 (/api/talkback/say, a self-INVITE that only reaches the
// INDOOR speaker because of the ZL380 DSP topology), this path does reach the
// door, but only as a one-shot greeting when the answering machine picks up,
// not as a conversation.
//
// Endpoints:
//
//	POST   /api/talkback/greeting   (body = audio; ?activate=true|false)
//	GET    /api/talkback/greeting
//	DELETE /api/talkback/greeting   (?deactivate=true|false)

const (
	// greetingPath is the greeting file the answering machine plays
	// (referenced by bt_av_media). greetingAltPath is a variant some firmware
	// builds read; both are written to be safe.
	greetingPath    = "/home/bticino/cfg/extra/7/messaggio.wav"
	greetingAltPath = "/home/bticino/cfg/extra/7/audio.wav"
	// greetingSentinel marks that the current greeting was set by the
	// bridge, so it can be restored (deleted) without touching a user's own.
	greetingSentinel = "/home/bticino/cfg/extra/7/.bridge_greeting"
	// greetingFFmpeg is the device's ffmpeg (with libspeex/pcm), used to
	// normalize any input audio to the door pipeline's format: PCM s16le,
	// mono, 8000 Hz (filesrc!wavparse!audioconvert, no resampling, so the
	// rate must be exactly 8000).
	greetingFFmpeg   = "/usr/local/bin/ffmpeg"
	greetingMaxBytes = 8 << 20 // at most 8 MiB of input audio
)

// handleGreeting dispatches GET/POST/DELETE on /api/talkback/greeting.
func (ws *WebServer) handleGreeting(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		if ws.rejectWithoutAnsweringMachine(w) {
			return
		}
		ws.handleGreetingSet(w, r)
	case http.MethodGet:
		ws.handleGreetingStatus(w, r)
	case http.MethodDelete:
		if ws.rejectWithoutAnsweringMachine(w) {
			return
		}
		ws.handleGreetingClear(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (ws *WebServer) rejectWithoutAnsweringMachine(w http.ResponseWriter) bool {
	if bticino.AnsweringMachineAvailable() {
		return false
	}
	ws.writeTalkbackJSON(w, http.StatusNotImplemented, map[string]interface{}{
		"success": false, "error": "this unit has no answering machine",
	})
	return true
}

// handleGreetingSet takes audio in the body, normalizes it to PCM s16le mono
// 8000 and installs it as the answering machine greeting. Unless
// ?activate=false, it also turns the answering machine on (*8*91##) so the
// greeting is actually played.
func (ws *WebServer) handleGreetingSet(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, greetingMaxBytes+1))
	if err != nil {
		ws.writeTalkbackJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "cannot read the body: " + err.Error(),
		})
		return
	}
	if len(body) == 0 {
		ws.writeTalkbackJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "empty body: send the audio (WAV/MP3/…) in the request body",
		})
		return
	}
	if len(body) > greetingMaxBytes {
		ws.writeTalkbackJSON(w, http.StatusRequestEntityTooLarge, map[string]interface{}{
			"success": false, "error": fmt.Sprintf("audio too large (at most %d bytes)", greetingMaxBytes),
		})
		return
	}

	// Write the input to a temp file and transcode it with ffmpeg to the
	// exact format of the door pipeline.
	tmpIn, err := os.CreateTemp("/tmp", "greeting-in-*")
	if err != nil {
		ws.writeTalkbackJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "cannot create temp file: " + err.Error(),
		})
		return
	}
	defer os.Remove(tmpIn.Name())
	if _, err := tmpIn.Write(body); err != nil {
		tmpIn.Close()
		ws.writeTalkbackJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false, "error": "cannot write temp file: " + err.Error(),
		})
		return
	}
	tmpIn.Close()

	if err := ws.transcodeGreeting(tmpIn.Name(), greetingPath); err != nil {
		ws.logger.WithError(err).Warn("greeting: transcoding the audio failed")
		ws.writeTalkbackJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false, "error": "audio invalid or cannot be transcoded: " + err.Error(),
		})
		return
	}
	// Also copy to the audio.wav variant (best effort).
	if data, err := os.ReadFile(greetingPath); err == nil {
		_ = os.WriteFile(greetingAltPath, data, 0644)
	}
	// Mark the greeting as set by the bridge.
	_ = os.WriteFile(greetingSentinel, []byte(time.Now().Format(time.RFC3339)+"\n"), 0644)

	result := map[string]interface{}{
		"success": true,
		"path":    greetingPath,
		"message": "answering machine greeting updated; it plays at the entrance panel when the answering machine picks up",
	}

	// Turn the answering machine on unless asked not to.
	if r.URL.Query().Get("activate") != "false" {
		if err := ws.bridge.SendOpenWebNetCommand(bticino.CmdVoicemailOn); err != nil {
			ws.logger.WithError(err).Warn("greeting: turning the answering machine on failed")
			result["activate_error"] = err.Error()
		} else {
			result["answering_machine"] = "enabled"
		}
	}

	ws.logger.Info("Talk-back v1: answering machine greeting updated via web dashboard")
	ws.writeTalkbackJSON(w, http.StatusOK, result)
}

// handleGreetingStatus reports whether a bridge greeting is set and its size.
func (ws *WebServer) handleGreetingStatus(w http.ResponseWriter, r *http.Request) {
	res := map[string]interface{}{
		"bridge_greeting": fileExists(greetingSentinel),
		"greeting_exists": fileExists(greetingPath),
	}
	if fi, err := os.Stat(greetingPath); err == nil {
		res["size"] = fi.Size()
		res["modified"] = fi.ModTime().Format(time.RFC3339)
	}
	ws.writeTalkbackJSON(w, http.StatusOK, res)
}

// handleGreetingClear removes the bridge's greeting (restoring the firmware
// default). With ?deactivate=true it also turns the answering machine off.
// It only deletes a greeting the bridge set (sentinel present).
func (ws *WebServer) handleGreetingClear(w http.ResponseWriter, r *http.Request) {
	if !fileExists(greetingSentinel) {
		ws.writeTalkbackJSON(w, http.StatusOK, map[string]interface{}{
			"success": true, "message": "no greeting set by the bridge; nothing to restore",
		})
		return
	}
	_ = os.Remove(greetingPath)
	_ = os.Remove(greetingAltPath)
	_ = os.Remove(greetingSentinel)

	result := map[string]interface{}{
		"success": true, "message": "bridge greeting removed; default greeting restored",
	}
	if r.URL.Query().Get("deactivate") == "true" {
		if err := ws.bridge.SendOpenWebNetCommand(bticino.CmdVoicemailOff); err != nil {
			ws.logger.WithError(err).Warn("greeting: turning the answering machine off failed")
			result["deactivate_error"] = err.Error()
		} else {
			result["answering_machine"] = "disabled"
		}
	}
	ws.logger.Info("Talk-back v1: answering machine greeting restored via web dashboard")
	ws.writeTalkbackJSON(w, http.StatusOK, result)
}

// transcodeGreeting normalizes src to the door pipeline's format (PCM s16le,
// mono, 8000 Hz) and writes dst. It writes a temp file and renames it
// atomically, so a failed ffmpeg never leaves half a greeting.
func (ws *WebServer) transcodeGreeting(src, dst string) error {
	tmpOut := dst + ".tmp"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, greetingFFmpeg,
		"-hide_banner", "-loglevel", "error", "-y",
		"-i", src,
		"-ar", "8000", "-ac", "1", "-c:a", "pcm_s16le",
		"-f", "wav", tmpOut,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(tmpOut)
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ffmpeg timed out (30s)")
		}
		msg := string(out)
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		return fmt.Errorf("ffmpeg: %v: %s", err, msg)
	}
	if err := os.Rename(tmpOut, dst); err != nil {
		os.Remove(tmpOut)
		return fmt.Errorf("cannot install the greeting: %w", err)
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(filepath.Clean(p))
	return err == nil
}
