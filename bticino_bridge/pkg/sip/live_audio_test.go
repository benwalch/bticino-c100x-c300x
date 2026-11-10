package sip

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// fakeAudioTap is the test double for VideoTap in the LiveAudioOnCall tests.
// When entered/release are not nil, Start() blocks: it closes entered on entry
// and waits for the test to close release, so the race window with a
// concurrent Stop can be exercised (leak test).
type fakeAudioTap struct {
	mu            sync.Mutex
	starts, stops int
	err           error
	entered       chan struct{}
	release       chan struct{}
}

func (f *fakeAudioTap) Start() error {
	f.mu.Lock()
	f.starts++
	err := f.err
	f.mu.Unlock()
	if f.entered != nil {
		close(f.entered)
		<-f.release
	}
	return err
}

func (f *fakeAudioTap) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
}

func (f *fakeAudioTap) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops
}

func TestLiveAudioStartArmsTap(t *testing.T) {
	tap := &fakeAudioTap{}
	l := NewLiveAudioOnCall(tap, logrus.New())

	l.Start("test")

	if !l.IsActive() {
		t.Fatal("expected IsActive()=true after Start")
	}
	if starts, _ := tap.counts(); starts != 1 {
		t.Fatalf("expected 1 tap start, got %d", starts)
	}
}

func TestLiveAudioStartIdempotent(t *testing.T) {
	tap := &fakeAudioTap{}
	l := NewLiveAudioOnCall(tap, logrus.New())
	l.Start("a")
	l.Start("b")
	if starts, _ := tap.counts(); starts != 1 {
		t.Fatalf("expected 1 start (idempotent), got %d", starts)
	}
}

func TestLiveAudioStopDisarmsAndAllowsRestart(t *testing.T) {
	tap := &fakeAudioTap{}
	l := NewLiveAudioOnCall(tap, logrus.New())
	l.Start("a")
	l.Stop()
	if l.IsActive() {
		t.Fatal("expected IsActive()=false after Stop")
	}
	if _, stops := tap.counts(); stops != 1 {
		t.Fatalf("expected 1 tap stop, got %d", stops)
	}
	l.Start("b")
	if starts, _ := tap.counts(); starts != 2 {
		t.Fatalf("expected 2 starts after re-Start, got %d", starts)
	}
}

func TestLiveAudioStopWithoutStartIsNoop(t *testing.T) {
	tap := &fakeAudioTap{}
	l := NewLiveAudioOnCall(tap, logrus.New())
	l.Stop()
	if _, stops := tap.counts(); stops != 0 {
		t.Fatalf("Stop without Start must not stop the tap (got %d)", stops)
	}
}

func TestLiveAudioTapErrorIsNonFatal(t *testing.T) {
	tap := &fakeAudioTap{err: errors.New("operation not permitted")}
	l := NewLiveAudioOnCall(tap, logrus.New())
	l.Start("no-permissions") // must not panic
	if !l.IsActive() {
		t.Fatal("expected IsActive()=true even if the tap fails")
	}
}

func TestLiveAudioNilTapIsSafe(t *testing.T) {
	l := NewLiveAudioOnCall(nil, logrus.New())
	l.Start("a") // must not panic
	l.Stop()
}

func TestLiveAudioGuardAutoStopsWhenHangupIsMissed(t *testing.T) {
	tap := &fakeAudioTap{}
	l := NewLiveAudioOnCall(tap, logrus.New())
	l.SetGuardTimeout(30 * time.Millisecond)
	l.Start("a")

	deadline := time.Now().Add(2 * time.Second)
	for l.IsActive() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if l.IsActive() {
		t.Fatal("expected auto-stop by the guard, still active")
	}
	if _, stops := tap.counts(); stops != 1 {
		t.Fatalf("expected 1 tap stop by the guard, got %d", stops)
	}
}

func TestLiveAudioGuardCanceledByStop(t *testing.T) {
	tap := &fakeAudioTap{}
	l := NewLiveAudioOnCall(tap, logrus.New())
	l.SetGuardTimeout(30 * time.Millisecond)
	l.Start("a")
	l.Stop()
	time.Sleep(80 * time.Millisecond) // the guard would have expired by now
	if _, stops := tap.counts(); stops != 1 {
		t.Fatalf("the cancelled guard must not stop again (stops=%d)", stops)
	}
	// a re-Start after the guard expired must work with a new guard
	l.Start("b")
	if !l.IsActive() {
		t.Fatal("expected active after re-Start")
	}
	l.Stop()
}

// TestLiveAudioStopDuringStartDoesNotLeakTap exercises the leak fix: if Stop()
// wins the race while tap.Start() is running (blocked), Start must re-check the
// state after tap.Start() returns and stop the orphaned tap instead of leaving
// it running with active=false.
func TestLiveAudioStopDuringStartDoesNotLeakTap(t *testing.T) {
	tap := &fakeAudioTap{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	l := NewLiveAudioOnCall(tap, logrus.New())

	startDone := make(chan struct{})
	go func() {
		l.Start("race")
		close(startDone)
	}()

	// Wait until Start() is blocked inside tap.Start().
	<-tap.entered

	// Concurrent Stop during the blocking window: since the tap has not
	// returned yet, Stop() can only flip active=false (the real tap keeps
	// starting in the background until tap.Start() returns).
	l.Stop()

	// Release tap.Start() so it returns and Start() can re-check active.
	close(tap.release)
	<-startDone

	if l.IsActive() {
		t.Fatal("expected IsActive()=false after a concurrent Stop during Start")
	}

	starts, stops := tap.counts()
	if starts != 1 {
		t.Fatalf("expected 1 tap start, got %d", starts)
	}
	// Deterministic sequence given the channel sync: Stop() sees active=true
	// (Start set it before blocking in tap.Start) and stops the tap once
	// (stops=1) even though tap.Start() is still running; once released and
	// returned, the leak fix's re-check sees active=false (orphaned) and stops
	// the tap again (stops=2). Without the fix, stops would stay at 1 and the
	// real tap (whose Start finishes AFTER the first Stop) would leak, running.
	if stops != 2 {
		t.Fatalf("tap leaked: expected 2 stops (direct Stop + the fix's re-check), got %d (starts=%d)", stops, starts)
	}
}
