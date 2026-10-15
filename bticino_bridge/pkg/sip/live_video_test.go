package sip

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

type mockTap struct {
	mu     sync.Mutex
	starts int
	stops  int
	err    error
}

func (m *mockTap) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.starts++
	return m.err
}

func (m *mockTap) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stops++
}

func (m *mockTap) counts() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.starts, m.stops
}

type mockMarker struct {
	mu     sync.Mutex
	states []bool
}

func (m *mockMarker) SetCallActive(a bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, a)
}

func (m *mockMarker) last() (bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.states) == 0 {
		return false, false
	}
	return m.states[len(m.states)-1], true
}

func TestStartArmsTapAndMarks(t *testing.T) {
	tap := &mockTap{}
	mk := &mockMarker{}
	l := NewLiveVideoOnCall(tap, mk, logrus.New())

	l.Start("test")

	if !l.IsActive() {
		t.Fatal("expected IsActive()=true after Start")
	}
	if starts, _ := tap.counts(); starts != 1 {
		t.Fatalf("expected 1 tap start, got %d", starts)
	}
	if v, ok := mk.last(); !ok || v != true {
		t.Fatalf("expected marker SetCallActive(true), got ok=%v v=%v", ok, v)
	}
}

func TestStartIdempotent(t *testing.T) {
	tap := &mockTap{}
	l := NewLiveVideoOnCall(tap, &mockMarker{}, logrus.New())
	l.Start("a")
	l.Start("b")
	if starts, _ := tap.counts(); starts != 1 {
		t.Fatalf("expected 1 start (idempotent), got %d", starts)
	}
}

func TestStopDisarmsAndAllowsRestart(t *testing.T) {
	tap := &mockTap{}
	mk := &mockMarker{}
	l := NewLiveVideoOnCall(tap, mk, logrus.New())
	l.Start("a")
	l.Stop()
	if l.IsActive() {
		t.Fatal("expected IsActive()=false after Stop")
	}
	if _, stops := tap.counts(); stops != 1 {
		t.Fatalf("expected 1 tap stop, got %d", stops)
	}
	if v, ok := mk.last(); !ok || v != false {
		t.Fatalf("expected marker SetCallActive(false), got ok=%v v=%v", ok, v)
	}
	l.Start("b")
	if starts, _ := tap.counts(); starts != 2 {
		t.Fatalf("expected 2 starts after re-Start, got %d", starts)
	}
}

func TestStopWithoutStartIsNoop(t *testing.T) {
	tap := &mockTap{}
	l := NewLiveVideoOnCall(tap, &mockMarker{}, logrus.New())
	l.Stop()
	if _, stops := tap.counts(); stops != 0 {
		t.Fatalf("Stop without Start must not stop the tap (got %d)", stops)
	}
}

func TestStartTapOnlySkipsMarker(t *testing.T) {
	tap := &mockTap{}
	mk := &mockMarker{}
	l := NewLiveVideoOnCall(tap, mk, logrus.New())
	l.StartTapOnly("own-session")
	if !l.IsActive() {
		t.Fatal("expected IsActive()=true after StartTapOnly")
	}
	if starts, _ := tap.counts(); starts != 1 {
		t.Fatalf("expected 1 tap start, got %d", starts)
	}
	mk.mu.Lock()
	n := len(mk.states)
	mk.mu.Unlock()
	if n != 0 {
		t.Fatalf("marker must stay untouched, got %d calls", n)
	}
}

func TestTapErrorIsNonFatal(t *testing.T) {
	// A failure opening the tap (e.g. no permission) degrades without video, but the
	// call stays marked as active (to inhibit the RTSP self-INVITE).
	tap := &mockTap{err: errors.New("operation not permitted")}
	mk := &mockMarker{}
	l := NewLiveVideoOnCall(tap, mk, logrus.New())
	l.Start("no-permissions") // must not panic
	if !l.IsActive() {
		t.Fatal("expected IsActive()=true even if the tap fails")
	}
	if v, ok := mk.last(); !ok || v != true {
		t.Fatalf("expected marker true even if the tap fails, got ok=%v v=%v", ok, v)
	}
}

func TestNilMarkerAndNilTapAreSafe(t *testing.T) {
	l := NewLiveVideoOnCall(nil, nil, logrus.New())
	l.Start("a") // must not panic
	l.Stop()
}

func TestGuardAutoStopsWhenHangupIsMissed(t *testing.T) {
	// The end of the call is detected via OWN frames (heuristic): if missed,
	// the guard must disarm on its own.
	tap := &mockTap{}
	mk := &mockMarker{}
	l := NewLiveVideoOnCall(tap, mk, logrus.New())
	l.SetGuardTimeout(30 * time.Millisecond)
	l.Start("a")

	deadline := time.Now().Add(2 * time.Second)
	for l.IsActive() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if l.IsActive() {
		t.Fatal("expected guard auto-stop, still active")
	}
	if _, stops := tap.counts(); stops != 1 {
		t.Fatalf("expected 1 tap stop by guard, got %d", stops)
	}
	if v, ok := mk.last(); !ok || v != false {
		t.Fatalf("expected marker false after guard, got ok=%v v=%v", ok, v)
	}
}

func TestGuardCanceledByStop(t *testing.T) {
	tap := &mockTap{}
	l := NewLiveVideoOnCall(tap, &mockMarker{}, logrus.New())
	l.SetGuardTimeout(30 * time.Millisecond)
	l.Start("a")
	l.Stop()
	time.Sleep(80 * time.Millisecond) // the guard would already have expired
	if _, stops := tap.counts(); stops != 1 {
		t.Fatalf("canceled guard must not stop again (stops=%d)", stops)
	}
	// a re-Start after the expired guard must work with a new guard
	l.Start("b")
	if !l.IsActive() {
		t.Fatal("expected active after re-Start")
	}
	l.Stop()
}
