package sip

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bticino_bridge/pkg/events"

	"github.com/sirupsen/logrus"
)

func newTestRTSP(t *testing.T) (*EnhancedRTSPServer, *int32) {
	t.Helper()
	bus := events.NewEventBus(1, logrus.New())
	var ended int32
	bus.Subscribe("sip.call.ended", func(_ *events.Event) {
		atomic.AddInt32(&ended, 1)
	})
	// nil sipClient on purpose: in production the local SIP may fail to register.
	r := NewEnhancedRTSPServer(0, nil, bus, logrus.New())
	return r, &ended
}

func waitEnded(c *int32, want int32) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(c) == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return atomic.LoadInt32(c) == want
}

func TestReleaseSelfCallIgnoresNativeCall(t *testing.T) {
	// A NATIVE call (marked by LiveVideoOnCall via SetCallActive) is not
	// ours: when the last RTSP client leaves, it is NOT hung up nor cleaned up.
	r, ended := newTestRTSP(t)
	r.SetCallActive(true)

	r.releaseSelfCallIfIdle("test") // must not panic (nil sipClient)

	r.mutex.RLock()
	active := r.callActive
	r.mutex.RUnlock()
	if !active {
		t.Fatal("native call must not be cleaned up when RTSP clients leave")
	}
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(ended) != 0 {
		t.Fatal("sip.call.ended must not be published for a native call")
	}
}

func TestReleaseSelfCallHangsUpOwnCallWithNilClient(t *testing.T) {
	// Self-INVITE call (internal state) with nil sipClient: must clean up the
	// state and publish sip.call.ended WITHOUT panicking.
	r, ended := newTestRTSP(t)
	r.mutex.Lock()
	r.callActive = true // interna: no marcada por SetCallActive
	r.mutex.Unlock()

	r.releaseSelfCallIfIdle("test")

	r.mutex.RLock()
	active := r.callActive
	r.mutex.RUnlock()
	if active {
		t.Fatal("expected callActive=false after releasing own call")
	}
	if !waitEnded(ended, 1) {
		t.Fatalf("expected 1 sip.call.ended, got %d", atomic.LoadInt32(ended))
	}
}

func TestReleaseSelfCallSkipsWhenClientsRemain(t *testing.T) {
	r, ended := newTestRTSP(t)
	r.mutex.Lock()
	r.callActive = true
	r.activeClients = 1
	r.mutex.Unlock()

	r.releaseSelfCallIfIdle("test")

	r.mutex.RLock()
	active := r.callActive
	r.mutex.RUnlock()
	if !active {
		t.Fatal("call must not be released with active clients")
	}
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(ended) != 0 {
		t.Fatal("sip.call.ended must not be published with active clients")
	}
}

func TestSetupUDPWithNilSIPClientDoesNotPanic(t *testing.T) {
	// SETUP with UDP transport queries the sipClient config for the
	// server_port; with a nil sipClient it must still answer (fallback).
	r, _ := newTestRTSP(t)
	resp := r.handleSetup(&RTSPRequest{
		Method:    "SETUP",
		URL:       "rtsp://127.0.0.1:6554/doorbell-video/track1",
		CSeq:      2,
		Transport: "RTP/AVP;unicast;client_port=5000-5001",
	}, "127.0.0.1:34567", nil)
	if !strings.Contains(resp, "200 OK") {
		t.Fatalf("expected 200 OK in UDP SETUP with nil sipClient, got: %q", resp)
	}
}

func TestSetCallActiveFalseClearsExternalMark(t *testing.T) {
	// After unmarking the native call, a future self-INVITE call must
	// be releasable normally (the external flag must not stay stuck).
	r, ended := newTestRTSP(t)
	r.SetCallActive(true)
	r.SetCallActive(false)

	r.mutex.Lock()
	r.callActive = true // self-INVITE posterior
	r.mutex.Unlock()
	r.releaseSelfCallIfIdle("test")

	if !waitEnded(ended, 1) {
		t.Fatalf("expected 1 sip.call.ended after own call, got %d", atomic.LoadInt32(ended))
	}
}
