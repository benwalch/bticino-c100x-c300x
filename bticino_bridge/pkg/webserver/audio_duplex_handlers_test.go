package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/sip"
	"github.com/sirupsen/logrus"
)

// fakeDuplex implements DuplexController for the tests, without touching real
// SIP/RTP (pkg/sip/audio_call.go).
type fakeDuplex struct {
	opened, closed bool
	busy           bool
	active         bool // simulates the authoritative state of AudioCall.active
}

func (f *fakeDuplex) OpenDuplex(sink, talk string) (sip.AudioAnswer, error) {
	if f.busy {
		return sip.AudioAnswer{}, sip.ErrAudioLegBusy
	}
	f.opened = true
	f.active = true
	return sip.AudioAnswer{
		Port:        7078,
		PayloadType: 98,
		CryptoSuite: "AES_CM_128_HMAC_SHA1_80",
		CryptoKey:   "SECRET",
	}, nil
}

func (f *fakeDuplex) Close() error {
	f.closed = true
	f.active = false
	return nil
}

func (f *fakeDuplex) IsActive() bool { return f.active }

func newTestDuplexWS(enabled bool, fd *fakeDuplex) *WebServer {
	return &WebServer{
		logger: logrus.New(),
		config: &config.Config{
			Audio: config.AudioConfig{
				DuplexEnabled:  enabled,
				DuplexSinkPath: "/tmp/door_audio.wav",
			},
		},
		duplexController: fd,
	}
}

func TestAudioDuplexHandlers(t *testing.T) {
	t.Run("start OK: 202 and no SRTP key in the body", func(t *testing.T) {
		fd := &fakeDuplex{}
		ws := newTestDuplexWS(true, fd)
		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStart(rec, httptest.NewRequest(http.MethodPost, "/api/audio/duplex/start", nil))

		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected 202, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "SECRET") {
			t.Fatalf("the SRTP key must NOT appear in the body: %s", rec.Body.String())
		}
		var body map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		if body["port"] != float64(7078) {
			t.Errorf("expected port=7078, got %v", body["port"])
		}
		if body["payload_type"] != float64(98) {
			t.Errorf("expected payload_type=98, got %v", body["payload_type"])
		}
		if body["crypto_suite"] != "AES_CM_128_HMAC_SHA1_80" {
			t.Errorf("expected crypto_suite, got %v", body["crypto_suite"])
		}
		if _, ok := body["crypto_key"]; ok {
			t.Error("the body must not contain the key 'crypto_key'")
		}
		if !fd.opened {
			t.Error("expected OpenDuplex to be called")
		}
	})

	t.Run("disabled: 404", func(t *testing.T) {
		fd := &fakeDuplex{}
		ws := newTestDuplexWS(false, fd)
		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStart(rec, httptest.NewRequest(http.MethodPost, "/api/audio/duplex/start", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 with duplex_enabled=false, got %d", rec.Code)
		}
		if fd.opened {
			t.Error("OpenDuplex must not be called if duplex_enabled=false")
		}
	})

	t.Run("busy: 409", func(t *testing.T) {
		fd := &fakeDuplex{busy: true}
		ws := newTestDuplexWS(true, fd)
		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStart(rec, httptest.NewRequest(http.MethodPost, "/api/audio/duplex/start", nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409 with the leg busy, got %d", rec.Code)
		}
	})

	t.Run("start rejects GET", func(t *testing.T) {
		fd := &fakeDuplex{}
		ws := newTestDuplexWS(true, fd)
		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStart(rec, httptest.NewRequest(http.MethodGet, "/api/audio/duplex/start", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 on GET, got %d", rec.Code)
		}
	})

	t.Run("stop llama a Close", func(t *testing.T) {
		fd := &fakeDuplex{}
		ws := newTestDuplexWS(true, fd)
		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStop(rec, httptest.NewRequest(http.MethodPost, "/api/audio/duplex/stop", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 on stop, got %d", rec.Code)
		}
		if !fd.closed {
			t.Error("expected Close to be called")
		}
	})

	t.Run("stop disabled: 404", func(t *testing.T) {
		fd := &fakeDuplex{}
		ws := newTestDuplexWS(false, fd)
		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStop(rec, httptest.NewRequest(http.MethodPost, "/api/audio/duplex/stop", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if fd.closed {
			t.Error("Close must not be called if duplex_enabled=false")
		}
	})

	t.Run("status disabled: 404", func(t *testing.T) {
		fd := &fakeDuplex{}
		ws := newTestDuplexWS(false, fd)
		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStatus(rec, httptest.NewRequest(http.MethodGet, "/api/audio/duplex/status", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("status enabled: 200 with sink_path", func(t *testing.T) {
		fd := &fakeDuplex{}
		ws := newTestDuplexWS(true, fd)
		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStatus(rec, httptest.NewRequest(http.MethodGet, "/api/audio/duplex/status", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		if body["sink_path"] != "/tmp/door_audio.wav" {
			t.Errorf("expected sink_path, got %v", body["sink_path"])
		}
		if body["active"] != false {
			t.Errorf("expected active=false before start, got %v", body["active"])
		}
	})

	t.Run("status reflects active after start", func(t *testing.T) {
		fd := &fakeDuplex{}
		ws := newTestDuplexWS(true, fd)
		ws.handleAudioDuplexStart(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/audio/duplex/start", nil))

		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStatus(rec, httptest.NewRequest(http.MethodGet, "/api/audio/duplex/status", nil))
		var body map[string]interface{}
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if body["active"] != true {
			t.Errorf("expected active=true after start, got %v", body["active"])
		}
	})

	t.Run("status is authoritative: reflects the leg closing by itself without /stop", func(t *testing.T) {
		// /status must ask controller.IsActive() instead of a local flag: if
		// the leg closes by itself (natural end of the receiver after the 60s
		// timeout) without anyone calling /stop, a mirror flag in the webserver
		// would stay stale at active=true forever.
		fd := &fakeDuplex{}
		ws := newTestDuplexWS(true, fd)
		ws.handleAudioDuplexStart(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/audio/duplex/start", nil))

		// Simulate the self-close: the leg goes inactive by itself (as
		// AudioCall does after the natural end of the receiver), WITHOUT going
		// through handleAudioDuplexStop.
		fd.active = false

		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStatus(rec, httptest.NewRequest(http.MethodGet, "/api/audio/duplex/status", nil))
		var body map[string]interface{}
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if body["active"] != false {
			t.Errorf("expected active=false after the self-close (no /stop), got %v", body["active"])
		}
	})

	t.Run("status with nil controller: active=false without panic", func(t *testing.T) {
		// Built directly (NOT via newTestDuplexWS(true, nil)): passing a nil
		// *fakeDuplex through that helper would give a NON-nil DuplexController
		// interface with a nil underlying pointer (Go's classic "nil interface
		// gotcha"), which would panic on IsActive(). Here duplexController keeps
		// its real zero value (a nil interface), the case /status must tolerate.
		ws := &WebServer{
			logger: logrus.New(),
			config: &config.Config{
				Audio: config.AudioConfig{DuplexEnabled: true, DuplexSinkPath: "/tmp/door_audio.wav"},
			},
		}
		rec := httptest.NewRecorder()
		ws.handleAudioDuplexStatus(rec, httptest.NewRequest(http.MethodGet, "/api/audio/duplex/status", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var body map[string]interface{}
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if body["active"] != false {
			t.Errorf("expected active=false with nil controller, got %v", body["active"])
		}
	})
}
