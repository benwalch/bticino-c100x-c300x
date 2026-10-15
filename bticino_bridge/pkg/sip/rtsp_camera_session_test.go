package sip

import (
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bticino_bridge/pkg/bticino"
	"bticino_bridge/pkg/events"

	"github.com/sirupsen/logrus"
)

func newCameraRTSPServer(t *testing.T) (*EnhancedRTSPServer, *BTicinoSIPClient, *int32, *int32) {
	return newCameraRTSPServerWithState(t, Registered)
}

func newCameraRTSPServerWithState(t *testing.T, state CallState) (*EnhancedRTSPServer, *BTicinoSIPClient, *int32, *int32) {
	t.Helper()
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	bus := events.NewEventBus(1, logger)
	var started, stopped int32
	bus.Subscribe("video.session.started", func(_ *events.Event) {
		atomic.AddInt32(&started, 1)
	})
	bus.Subscribe("video.session.stopped", func(_ *events.Event) {
		atomic.AddInt32(&stopped, 1)
	})
	c := &BTicinoSIPClient{
		config:     &SIPConfig{},
		callState:  state,
		eventBus:   bus,
		logger:     logger,
		answerUser: bticino.SIPUsernameForVariant(bticino.DeviceVariant),
	}
	// Blocked pipe keeps the hold loop parked until Stop, like a live dialog.
	near, far := net.Pipe()
	c.webrtcID = &sipIdentity{conn: near}
	t.Cleanup(func() { near.Close(); far.Close() })
	c.resolveNativeFunc = func() (string, error) { return "sip:x", nil }
	c.establishDialogFunc = func(string, string) (*cameraDialog, error) {
		return &cameraDialog{callID: "c1", toTag: "t1"}, nil
	}
	c.terminateDialogFunc = func(*cameraDialog) {}
	srv := NewEnhancedRTSPServer(6554, c, bus, logger)
	srv.SetCameraSession(true, time.Minute)
	return srv, c, &started, &stopped
}

// RTSP demand with the camera flag starts a headless session; a second
// demand joins it instead of dialing again.
func TestRTSPDemandStartsCameraSession(t *testing.T) {
	srv, _, started, _ := newCameraRTSPServer(t)

	if err := srv.ensureSIPCallActive(); err != nil {
		t.Fatalf("first demand: %v", err)
	}
	if !waitEnded(started, 1) {
		t.Fatal("no session started on first demand")
	}
	waitForFlag(t, srv, true)
	if err := srv.ensureSIPCallActive(); err != nil {
		t.Fatalf("second demand must join: %v", err)
	}
	if got := atomic.LoadInt32(started); got != 1 {
		t.Fatalf("started events = %d, want 1 (join, no redial)", got)
	}
}

// Last viewer out stops the session and clears the flag; the legacy
// Hangup path is not taken for camera dialogs.
func TestRTSPIdleStopsCameraSession(t *testing.T) {
	srv, _, started, stopped := newCameraRTSPServer(t)

	if err := srv.ensureSIPCallActive(); err != nil {
		t.Fatalf("demand: %v", err)
	}
	if !waitEnded(started, 1) {
		t.Fatal("no session started")
	}
	waitForFlag(t, srv, true)
	srv.mutex.Lock()
	srv.activeClients = 0
	srv.mutex.Unlock()
	srv.releaseSelfCallIfIdle("test")
	if !waitEnded(stopped, 1) {
		t.Fatal("no stopped event after last viewer left")
	}
	srv.mutex.RLock()
	active := srv.callActive
	srv.mutex.RUnlock()
	if active {
		t.Fatal("callActive must clear after release")
	}
}

// Camera flag off keeps the legacy gate: both off still rejects.
func TestRTSPDemandBothFlagsOffRejects(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	srv := NewEnhancedRTSPServer(6554, nil, nil, logger)
	if err := srv.ensureSIPCallActive(); err == nil {
		t.Fatal("demand with no flags must reject")
	}
}

func waitForFlag(t *testing.T, srv *EnhancedRTSPServer, want bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.mutex.RLock()
		got := srv.callActive
		srv.mutex.RUnlock()
		if got == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("callActive = %v, want %v", !want, want)
}

// The idle reaper only stops RTSP-owned sessions: an MQTT-started session
// with no viewers keeps running (regression: reaper killed those here).
func TestRTSPReleaseSparesMQTTSession(t *testing.T) {
	srv, c, _, _ := newCameraRTSPServer(t)
	c.mutex.Lock()
	c.cameraSessionActive = true
	c.cameraSessionSource = "mqtt"
	c.mutex.Unlock()
	srv.mutex.Lock()
	srv.callActive = true
	srv.activeClients = 0
	srv.mutex.Unlock()

	srv.releaseSelfCallIfIdle("test")

	if !c.CameraSessionActive() {
		t.Fatal("MQTT-owned session must survive the RTSP reaper")
	}
	srv.mutex.RLock()
	active := srv.callActive
	srv.mutex.RUnlock()
	if !active {
		t.Fatal("callActive must stay while the MQTT session lives")
	}
}

// A fresh dial right after a sync failure is paced, not counted again.
func TestRTSPDemandPacesRedial(t *testing.T) {
	srv, _, _, _ := newCameraRTSPServerWithState(t, Idle)
	srv.SetCameraSession(true, time.Minute)

	if err := srv.ensureSIPCallActive(); err == nil {
		t.Fatal("idle client must fail the dial")
	}
	srv.mutex.RLock()
	failures := srv.selfCallFailures
	srv.mutex.RUnlock()
	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}
	if err := srv.ensureSIPCallActive(); err == nil {
		t.Fatal("immediate redial must pace")
	} else if !strings.Contains(err.Error(), "too recently") {
		t.Fatalf("want pacing error, got: %v", err)
	}
	srv.mutex.RLock()
	failures = srv.selfCallFailures
	srv.mutex.RUnlock()
	if failures != 1 {
		t.Fatalf("paced refusal must not count, failures = %d", failures)
	}
}

// Three spaced sync failures open the breaker; async deaths count too.
func TestRTSPDemandBreakerTrips(t *testing.T) {
	srv, _, _, _ := newCameraRTSPServerWithState(t, Idle)
	srv.SetCameraSession(true, time.Minute)

	for i := 0; i < maxSelfCallFailures; i++ {
		srv.mutex.Lock()
		srv.lastAttempt = time.Time{}
		srv.mutex.Unlock()
		if err := srv.ensureSIPCallActive(); err == nil {
			t.Fatalf("attempt %d must fail", i)
		}
	}
	if err := srv.ensureSIPCallActive(); err == nil {
		t.Fatal("breaker must cool down")
	} else if !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("want cooling error, got: %v", err)
	}
}
