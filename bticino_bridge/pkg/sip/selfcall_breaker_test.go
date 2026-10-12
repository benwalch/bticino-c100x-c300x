package sip

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bticino_bridge/pkg/events"

	"github.com/sirupsen/logrus"
)

func newTestBreaker(t *testing.T) (*EnhancedRTSPServer, *int32) {
	t.Helper()
	bus := events.NewEventBus(1, logrus.New())
	var ended int32
	bus.Subscribe("sip.call.ended", func(_ *events.Event) {
		atomic.AddInt32(&ended, 1)
	})
	r := NewEnhancedRTSPServer(0, &BTicinoSIPClient{}, bus, logrus.New())
	r.SetVideoActivation(true)
	return r, &ended
}

// After maxSelfCallFailures no-media dials, further dials pause instead of
// storming flexisip on every RTSP reconnect.
func TestSelfCallCooldownSkipsDial(t *testing.T) {
	r, _ := newTestBreaker(t)
	r.mutex.Lock()
	r.selfCallFailures = maxSelfCallFailures
	r.selfCallCooldownUntil = time.Now().Add(time.Minute)
	r.mutex.Unlock()

	err := r.ensureSIPCallActive()
	if err == nil || !strings.Contains(err.Error(), "cooling down") {
		t.Fatalf("expected cooling-down error, got %v", err)
	}
}

// A second concurrent dial joins the in-flight one instead of sending a
// duplicate INVITE.
func TestSelfCallSingleFlightJoins(t *testing.T) {
	r, _ := newTestBreaker(t)
	r.mutex.Lock()
	r.selfDialing = true
	r.mutex.Unlock()

	if err := r.ensureSIPCallActive(); err != nil {
		t.Fatalf("joining an in-flight dial must succeed, got %v", err)
	}
}

// Hangup always resets to Registered, even when the state already diverged
// (Connected with sessionActive=false after a dropped self-call). Without
// this, every later MakeCall failed and only a process restart helped.
func TestHangupResetsDivergedState(t *testing.T) {
	bus := events.NewEventBus(1, logrus.New())
	var ended int32
	bus.Subscribe("sip.call.ended", func(_ *events.Event) {
		atomic.AddInt32(&ended, 1)
	})
	c := &BTicinoSIPClient{
		callState: Connected,
		eventBus:  bus,
		logger:    logrus.New(),
	}
	if err := c.Hangup(); err != nil {
		t.Fatalf("Hangup: %v", err)
	}
	if c.GetCallState() != Registered {
		t.Fatalf("expected Registered after Hangup, got %s", c.GetCallState())
	}
	if !waitEnded(&ended, 1) {
		t.Fatalf("expected 1 sip.call.ended, got %d", atomic.LoadInt32(&ended))
	}
}

// Hangup on an already-idle client stays silent (no spurious ended event
// flapping the HA call sensor).
func TestHangupIdleStaysSilent(t *testing.T) {
	bus := events.NewEventBus(1, logrus.New())
	var ended int32
	bus.Subscribe("sip.call.ended", func(_ *events.Event) {
		atomic.AddInt32(&ended, 1)
	})
	c := &BTicinoSIPClient{
		callState: Idle,
		eventBus:  bus,
		logger:    logrus.New(),
	}
	if err := c.Hangup(); err != nil {
		t.Fatalf("Hangup: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&ended) != 0 {
		t.Fatalf("idle Hangup must not publish, got %d", atomic.LoadInt32(&ended))
	}
}

// A message that exactly fills the buffer must be fully consumed —
// returning consumed < len with messages attached replays it forever
// (this exact bug spun the listener at ~1800 msgs/sec on an idle socket).
