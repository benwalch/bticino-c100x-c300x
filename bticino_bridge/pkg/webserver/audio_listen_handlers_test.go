package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bticino_bridge/pkg/config"
	"github.com/sirupsen/logrus"
)

// fakeAudioListen implements AudioListenController for the tests, without
// touching real RTP/ffmpeg (pkg/sip/audio_listen_on_call.go).
type fakeAudioListen struct {
	startCalls int
	stopCalls  int
	lastReason string
	active     bool // simulates the authoritative state of AudioListenOnCall.active
}

func (f *fakeAudioListen) Start(reason string) {
	f.startCalls++
	f.lastReason = reason
	f.active = true
}

func (f *fakeAudioListen) Stop() {
	f.stopCalls++
	f.active = false
}

func (f *fakeAudioListen) IsActive() bool { return f.active }

func newTestAudioListenWS(enabled bool, fal *fakeAudioListen) *WebServer {
	return &WebServer{
		logger: logrus.New(),
		config: &config.Config{
			Audio: config.AudioConfig{
				ListenTapEnabled: enabled,
				// DEDICATED sink of the passive ear, not DuplexSinkPath.
				ListenSinkPath: "/tmp/door_audio_listen.wav",
				DuplexSinkPath: "/tmp/door_audio.wav",
			},
		},
		audioListenController: fal,
	}
}

func TestAudioListenHandlers(t *testing.T) {
	t.Run("start OK: 202, calls Start(\"api\") and reflects active", func(t *testing.T) {
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(true, fal)
		rec := httptest.NewRecorder()
		ws.handleAudioListenStart(rec, httptest.NewRequest(http.MethodPost, "/api/audio/listen/start", nil))

		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected 202, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		if fal.startCalls != 1 {
			t.Errorf("expected Start called once, got %d", fal.startCalls)
		}
		if fal.lastReason != "api" {
			t.Errorf("expected reason=api, got %q", fal.lastReason)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		if body["success"] != true {
			t.Errorf("expected success=true, got %v", body["success"])
		}
		if body["active"] != true {
			t.Errorf("expected active=true after start, got %v", body["active"])
		}
	})

	t.Run("start is idempotent (Start must not fail on a double start)", func(t *testing.T) {
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(true, fal)
		ws.handleAudioListenStart(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/audio/listen/start", nil))
		rec := httptest.NewRecorder()
		ws.handleAudioListenStart(rec, httptest.NewRequest(http.MethodPost, "/api/audio/listen/start", nil))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("expected 202 on the second start, got %d", rec.Code)
		}
		if fal.startCalls != 2 {
			t.Errorf("expected 2 calls to Start (the handler does not filter doubles), got %d", fal.startCalls)
		}
	})

	t.Run("disabled: 404 and Start is not called", func(t *testing.T) {
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(false, fal)
		rec := httptest.NewRecorder()
		ws.handleAudioListenStart(rec, httptest.NewRequest(http.MethodPost, "/api/audio/listen/start", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 with listen_tap_enabled=false, got %d", rec.Code)
		}
		if fal.startCalls != 0 {
			t.Error("Start must not be called if listen_tap_enabled=false")
		}
	})

	t.Run("start rejects GET", func(t *testing.T) {
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(true, fal)
		rec := httptest.NewRecorder()
		ws.handleAudioListenStart(rec, httptest.NewRequest(http.MethodGet, "/api/audio/listen/start", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 on GET, got %d", rec.Code)
		}
	})

	t.Run("start with nil controller: 503", func(t *testing.T) {
		ws := &WebServer{
			logger: logrus.New(),
			config: &config.Config{
				Audio: config.AudioConfig{ListenTapEnabled: true},
			},
		}
		rec := httptest.NewRecorder()
		ws.handleAudioListenStart(rec, httptest.NewRequest(http.MethodPost, "/api/audio/listen/start", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 with nil controller, got %d", rec.Code)
		}
	})

	t.Run("stop llama a Stop", func(t *testing.T) {
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(true, fal)
		rec := httptest.NewRecorder()
		ws.handleAudioListenStop(rec, httptest.NewRequest(http.MethodPost, "/api/audio/listen/stop", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 on stop, got %d", rec.Code)
		}
		if fal.stopCalls != 1 {
			t.Errorf("expected Stop called once, got %d", fal.stopCalls)
		}
	})

	t.Run("stop disabled: 404", func(t *testing.T) {
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(false, fal)
		rec := httptest.NewRecorder()
		ws.handleAudioListenStop(rec, httptest.NewRequest(http.MethodPost, "/api/audio/listen/stop", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if fal.stopCalls != 0 {
			t.Error("Stop must not be called if listen_tap_enabled=false")
		}
	})

	t.Run("stop rejects GET", func(t *testing.T) {
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(true, fal)
		rec := httptest.NewRecorder()
		ws.handleAudioListenStop(rec, httptest.NewRequest(http.MethodGet, "/api/audio/listen/stop", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 on GET, got %d", rec.Code)
		}
	})

	t.Run("status disabled: 404", func(t *testing.T) {
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(false, fal)
		rec := httptest.NewRecorder()
		ws.handleAudioListenStatus(rec, httptest.NewRequest(http.MethodGet, "/api/audio/listen/status", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("status enabled: 200 with sink_path and active=false before start", func(t *testing.T) {
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(true, fal)
		rec := httptest.NewRecorder()
		ws.handleAudioListenStatus(rec, httptest.NewRequest(http.MethodGet, "/api/audio/listen/status", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		if body["sink_path"] != "/tmp/door_audio_listen.wav" {
			t.Errorf("expected sink_path (ListenSinkPath, dedicated), got %v", body["sink_path"])
		}
		if body["active"] != false {
			t.Errorf("expected active=false before start, got %v", body["active"])
		}
	})

	t.Run("status reflects active after start and after stop", func(t *testing.T) {
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(true, fal)
		ws.handleAudioListenStart(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/audio/listen/start", nil))

		rec := httptest.NewRecorder()
		ws.handleAudioListenStatus(rec, httptest.NewRequest(http.MethodGet, "/api/audio/listen/status", nil))
		var body map[string]interface{}
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if body["active"] != true {
			t.Errorf("expected active=true after start, got %v", body["active"])
		}

		ws.handleAudioListenStop(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/audio/listen/stop", nil))
		rec2 := httptest.NewRecorder()
		ws.handleAudioListenStatus(rec2, httptest.NewRequest(http.MethodGet, "/api/audio/listen/status", nil))
		var body2 map[string]interface{}
		_ = json.NewDecoder(rec2.Body).Decode(&body2)
		if body2["active"] != false {
			t.Errorf("expected active=false after stop, got %v", body2["active"])
		}
	})

	t.Run("status is authoritative: reflects the self-disarm without /stop", func(t *testing.T) {
		// The internal guard (SetGuardTimeout) or the natural end of the decoder
		// can disarm the session without /stop; /status must ask IsActive()
		// instead of a mirror flag in the webserver.
		fal := &fakeAudioListen{}
		ws := newTestAudioListenWS(true, fal)
		ws.handleAudioListenStart(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/audio/listen/start", nil))

		fal.active = false // simulates the self-disarm

		rec := httptest.NewRecorder()
		ws.handleAudioListenStatus(rec, httptest.NewRequest(http.MethodGet, "/api/audio/listen/status", nil))
		var body map[string]interface{}
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if body["active"] != false {
			t.Errorf("expected active=false after the self-disarm (no /stop), got %v", body["active"])
		}
	})

	t.Run("status with nil controller: active=false without panic", func(t *testing.T) {
		// Built directly (NOT via newTestAudioListenWS(true, nil)): passing a
		// nil *fakeAudioListen through that helper would give a NON-nil
		// AudioListenController interface with a nil underlying pointer (nil
		// interface gotcha), which would panic on IsActive(). Here
		// audioListenController keeps its real zero value (a nil interface).
		ws := &WebServer{
			logger: logrus.New(),
			config: &config.Config{
				Audio: config.AudioConfig{ListenTapEnabled: true, ListenSinkPath: "/tmp/door_audio_listen.wav"},
			},
		}
		rec := httptest.NewRecorder()
		ws.handleAudioListenStatus(rec, httptest.NewRequest(http.MethodGet, "/api/audio/listen/status", nil))
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
