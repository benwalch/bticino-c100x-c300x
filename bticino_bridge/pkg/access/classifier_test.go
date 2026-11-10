package access

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/events"
	"bticino_bridge/pkg/multicast/handlers"
)

func testCfg() config.AccessConfig {
	return config.AccessConfig{
		Enabled:            true,
		CorrelationWindow:  30 * time.Second,
		MaxHoldWait:        8 * time.Second,
		DurationVisitorMax: 3500 * time.Millisecond,
	}
}

// collect subscribes to access.classified and returns a channel with the events.
func collect(t *testing.T, bus events.EventBus) <-chan AccessEvent {
	t.Helper()
	ch := make(chan AccessEvent, 4)
	bus.Subscribe("access.classified", func(e *events.Event) {
		if ev, ok := e.Data.(AccessEvent); ok {
			ch <- ev
		}
	})
	return ch
}

func waitEvent(t *testing.T, ch <-chan AccessEvent) AccessEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for access.classified")
		return AccessEvent{}
	}
}

// newTestClassifier creates a Classifier with a controlled clock and an
// afterFunc that captures ALL armed timeout functions (one per onUnlocked), in
// order, so tests can fire them by hand, including an old timer that was
// already superseded, to check that it stays inert.
func newTestClassifier(bus events.EventBus) (*Classifier, *time.Time, *[]func()) {
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	c := NewClassifier(testCfg(), bus, logger)
	nowVal := time.Unix(1000, 0)
	fired := []func(){}
	c.now = func() time.Time { return nowVal }
	c.afterFunc = func(_ time.Duration, f func()) stopTimer {
		fired = append(fired, f)
		return noopTimer{}
	}
	return c, &nowVal, &fired
}

func TestClassifier_Visitor(t *testing.T) {
	bus := events.NewEventBus(4, nil)
	defer bus.Close()
	ch := collect(t, bus)
	c, now, _ := newTestClassifier(bus)

	c.recordRing("doorbell_entrance")
	*now = now.Add(2 * time.Second)
	c.onUnlocked("20")
	*now = now.Add(2500 * time.Millisecond)
	c.onLocked("20")

	ev := waitEvent(t, ch)
	if ev.Type != TypeVisitor || ev.Confidence != ConfHigh {
		t.Errorf("got {%s,%s}, want {visitor,high}", ev.Type, ev.Confidence)
	}
	if !ev.HadCall || ev.RingSource != "doorbell_entrance" {
		t.Errorf("wrong had_call/ring_source: %+v", ev)
	}
	if ev.DurationS < 2.4 || ev.DurationS > 2.6 {
		t.Errorf("duration_s = %v, want ~2.5", ev.DurationS)
	}
}

func TestClassifier_Resident(t *testing.T) {
	bus := events.NewEventBus(4, nil)
	defer bus.Close()
	ch := collect(t, bus)
	c, now, _ := newTestClassifier(bus)

	c.onUnlocked("20") // no ring before
	*now = now.Add(4500 * time.Millisecond)
	c.onLocked("20")

	ev := waitEvent(t, ch)
	if ev.Type != TypeResident || ev.Confidence != ConfHigh {
		t.Errorf("got {%s,%s}, want {resident,high}", ev.Type, ev.Confidence)
	}
	if ev.HadCall {
		t.Errorf("had_call = true, want false")
	}
}

func TestClassifier_TimeoutWithoutClose(t *testing.T) {
	bus := events.NewEventBus(4, nil)
	defer bus.Close()
	ch := collect(t, bus)
	c, _, fired := newTestClassifier(bus)

	c.onUnlocked("20") // no ring
	if len(*fired) == 0 {
		t.Fatal("max_hold_wait timer was not armed")
	}
	(*fired)[len(*fired)-1]() // simulate expiry without a close

	ev := waitEvent(t, ch)
	if ev.Type != TypeResident || ev.Confidence != ConfMedium {
		t.Errorf("got {%s,%s}, want {resident,medium}", ev.Type, ev.Confidence)
	}
	if ev.DurationS != 0 {
		t.Errorf("duration_s = %v, want 0 (no close)", ev.DurationS)
	}
}

func TestClassifier_WindowExpired(t *testing.T) {
	bus := events.NewEventBus(4, nil)
	defer bus.Close()
	ch := collect(t, bus)
	c, now, _ := newTestClassifier(bus)

	c.recordRing("doorbell_entrance")
	*now = now.Add(40 * time.Second) // outside the 30 s window
	c.onUnlocked("20")
	*now = now.Add(2 * time.Second)
	c.onLocked("20")

	ev := waitEvent(t, ch)
	if ev.HadCall {
		t.Errorf("had_call = true; the ring is outside the window")
	}
	if ev.Type != TypeUnknown { // no call + short pulse = conflict
		t.Errorf("type = %s, want unknown", ev.Type)
	}
}

// TestClassifier_DoubleOpeningSameWhere covers supersede: a second opening of
// the same WHERE arrives before the first one closes (two RFID openings or a
// relay bounce, back to back). The first access must not be lost: it is
// flushed with an unknown duration before the second entry starts.
func TestClassifier_DoubleOpeningSameWhere(t *testing.T) {
	bus := events.NewEventBus(4, nil)
	defer bus.Close()
	ch := collect(t, bus)
	c, now, _ := newTestClassifier(bus)

	c.recordRing("doorbell_entrance")
	*now = now.Add(1 * time.Second)
	c.onUnlocked("20") // first opening, correlated with the ring

	*now = now.Add(3 * time.Second)
	c.onUnlocked("20") // second opening of the same WHERE, the first never closed

	*now = now.Add(2 * time.Second)
	c.onLocked("20") // closes the second

	first := waitEvent(t, ch)
	second := waitEvent(t, ch)

	// The bus runs each handler in its own goroutine (several workers) with no
	// order between consecutive events, even two of the same name. Each event
	// is identified by its content (duration_s), not by arrival order.
	flushed, closed := first, second
	if flushed.DurationS != 0 {
		flushed, closed = second, first
	}

	if flushed.DurationS != 0 {
		t.Errorf("superseded flush event duration_s = %v, want 0", flushed.DurationS)
	}
	if !flushed.HadCall {
		t.Errorf("flush event should have had_call=true (correlated with the ring)")
	}
	if flushed.Confidence != ConfMedium {
		t.Errorf("flush event confidence = %s, want medium (unknown duration)", flushed.Confidence)
	}
	if closed.DurationS < 1.9 || closed.DurationS > 2.1 {
		t.Errorf("real close event duration_s = %v, want ~2", closed.DurationS)
	}
}

// TestClassifier_OldTimerInertAfterSupersede: the max_hold_wait timer of a
// superseded entry must not publish an extra event or touch the new entry
// when it fires (pointer identity in finalize).
func TestClassifier_OldTimerInertAfterSupersede(t *testing.T) {
	bus := events.NewEventBus(4, nil)
	defer bus.Close()
	ch := collect(t, bus)
	c, now, fired := newTestClassifier(bus)

	c.onUnlocked("20") // first opening, no ring
	if len(*fired) != 1 {
		t.Fatalf("expected 1 armed timer, have %d", len(*fired))
	}
	oldTimer := (*fired)[0]

	*now = now.Add(1 * time.Second)
	c.onUnlocked("20") // second opening of the same WHERE supersedes the first

	if len(*fired) != 2 {
		t.Fatalf("expected 2 armed timers, have %d", len(*fired))
	}

	// The first opening was already flushed (supersede event).
	ev1 := waitEvent(t, ch)
	if ev1.DurationS != 0 {
		t.Errorf("supersede event duration_s = %v, want 0", ev1.DurationS)
	}

	// Fire the OLD timer by hand: it must publish nothing and leave the new
	// entry alone.
	oldTimer()

	select {
	case ev := <-ch:
		t.Fatalf("the old timer published after supersede: %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// expected: nothing arrives
	}

	// The new entry is still alive: closing it gives its own normal event.
	*now = now.Add(2 * time.Second)
	c.onLocked("20")
	ev2 := waitEvent(t, ch)
	if ev2.DurationS < 1.9 || ev2.DurationS > 2.1 {
		t.Errorf("second event (real close after supersede) duration_s = %v, want ~2", ev2.DurationS)
	}
}

func TestWhereOf(t *testing.T) {
	e := &events.Event{Name: "door.unlocked", Data: &handlers.OpenWebNetEvent{Where: "20"}}
	if got := whereOf(e); got != "20" {
		t.Errorf("whereOf = %q, want 20", got)
	}
	if got := whereOf(&events.Event{Data: "not-an-own-event"}); got != "" {
		t.Errorf("whereOf with an odd type = %q, want empty", got)
	}
}
