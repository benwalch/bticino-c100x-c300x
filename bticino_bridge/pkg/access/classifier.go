package access

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/events"
	"bticino_bridge/pkg/multicast/handlers"
)

// stopTimer wraps *time.Timer so tests can inject a fake one.
type stopTimer interface{ Stop() bool }

// noopTimer is an inert stopTimer (for tests).
type noopTimer struct{}

func (noopTimer) Stop() bool { return true }

// AccessEvent is the Data of the "access.classified" event.
type AccessEvent struct {
	Type       string    `json:"type"`
	Confidence string    `json:"confidence"`
	Where      string    `json:"where"`
	HadCall    bool      `json:"had_call"`
	RingSource string    `json:"ring_source,omitempty"`
	DurationS  float64   `json:"duration_s"`
	Timestamp  time.Time `json:"timestamp"`
}

type ringInfo struct {
	at     time.Time
	source string
	set    bool
}

type pendingOpen struct {
	openedAt  time.Time
	hadCall   bool
	ringSrc   string
	timer     stopTimer
	finalized bool
}

// Classifier listens on the EventBus and publishes "access.classified".
type Classifier struct {
	cfg    config.AccessConfig
	bus    events.EventBus
	logger *logrus.Logger

	mu       sync.Mutex
	lastRing ringInfo
	pending  map[string]*pendingOpen
	subs     []string

	// Injectable for tests:
	now       func() time.Time
	afterFunc func(time.Duration, func()) stopTimer
}

// NewClassifier creates the classifier. now/afterFunc default to the real ones.
func NewClassifier(cfg config.AccessConfig, bus events.EventBus, logger *logrus.Logger) *Classifier {
	if logger == nil {
		logger = logrus.New()
	}
	return &Classifier{
		cfg:       cfg,
		bus:       bus,
		logger:    logger,
		pending:   make(map[string]*pendingOpen),
		now:       time.Now,
		afterFunc: func(d time.Duration, f func()) stopTimer { return time.AfterFunc(d, f) },
	}
}

// Start subscribes to the EventBus.
func (c *Classifier) Start() {
	c.subs = append(c.subs,
		c.bus.Subscribe("doorbell.pressed", func(*events.Event) { c.recordRing("doorbell_entrance") }),
		c.bus.Subscribe("doorbell.floor.pressed", func(*events.Event) { c.recordRing("doorbell_floor") }),
		c.bus.Subscribe("sip.call.incoming", func(*events.Event) { c.recordRing("sip") }),
		c.bus.Subscribe("door.unlocked", func(e *events.Event) { c.onUnlocked(whereOf(e)) }),
		c.bus.Subscribe("door.locked", func(e *events.Event) { c.onLocked(whereOf(e)) }),
	)
	c.logger.Info("Access classifier: subscriptions active")
}

// Stop cancels the subscriptions.
func (c *Classifier) Stop() {
	for _, id := range c.subs {
		c.bus.Unsubscribe(id)
	}
	c.subs = nil
}

// recordRing stores the last ring or call seen.
func (c *Classifier) recordRing(source string) {
	c.mu.Lock()
	c.lastRing = ringInfo{at: c.now(), source: source, set: true}
	c.mu.Unlock()
}

// onUnlocked opens a pending entry for WHERE and arms the max_hold_wait timer.
//
// If an open entry for the same WHERE is still pending (the same relay
// opened twice without the close in between, e.g. two RFID taps within
// max_hold_wait), it is superseded: it is flushed first as if its timeout had
// expired (unknown duration), so that access is not lost, then the new entry
// is armed. Deciding about the old entry (mark it finalized, stop its timer)
// and replacing it happen under the SAME lock, so no concurrent
// onUnlocked/onLocked/timer can see the old entry "alive" in the map after its
// replacement was decided. Only publishing the flush (I/O) happens outside
// the lock.
func (c *Classifier) onUnlocked(where string) {
	c.mu.Lock()
	old, hadOld := c.pending[where]
	var oldHadCall bool
	var oldRingSrc string
	var oldTs time.Time
	if hadOld {
		old.finalized = true
		if old.timer != nil {
			old.timer.Stop()
		}
		oldHadCall, oldRingSrc = old.hadCall, old.ringSrc
		oldTs = c.now()
	}

	hadCall, ringSrc := false, ""
	if c.lastRing.set && c.now().Sub(c.lastRing.at) <= c.cfg.CorrelationWindow {
		hadCall = true
		ringSrc = c.lastRing.source
	}
	p := &pendingOpen{openedAt: c.now(), hadCall: hadCall, ringSrc: ringSrc}
	// The timer captures the pointer p (identity), not just WHERE: if this
	// entry is superseded later, the old timer stays inert in finalize
	// (cur != p) instead of touching the entry that replaced it.
	p.timer = c.afterFunc(c.cfg.MaxHoldWait, func() { c.finalize(where, p, 0, false) })
	c.pending[where] = p
	c.mu.Unlock()

	if hadOld {
		c.publishAccess(where, oldHadCall, oldRingSrc, 0, false, oldTs)
	}
}

// onLocked closes the pending entry of WHERE and measures the pulse duration.
func (c *Classifier) onLocked(where string) {
	c.mu.Lock()
	p, ok := c.pending[where]
	if !ok {
		c.mu.Unlock()
		return
	}
	dur := c.now().Sub(p.openedAt)
	c.mu.Unlock()
	c.finalize(where, p, dur, true)
}

// finalize classifies and publishes the pending entry p of WHERE, but only if
// p is still the current entry in the map (compared by pointer identity, not
// just by WHERE) and not finalized yet. This guard makes the race safe between
// the real close (onLocked), the max_hold_wait timer of THIS entry, and a
// supersede by a new opening of the same WHERE (which has already set
// p.finalized=true and replaced the map entry before this finalize runs).
func (c *Classifier) finalize(where string, p *pendingOpen, dur time.Duration, durKnown bool) {
	c.mu.Lock()
	cur, ok := c.pending[where]
	if !ok || cur != p || p.finalized {
		c.mu.Unlock()
		return
	}
	p.finalized = true
	if p.timer != nil {
		p.timer.Stop()
	}
	delete(c.pending, where)
	hadCall, ringSrc := p.hadCall, p.ringSrc
	ts := c.now()
	c.mu.Unlock()

	c.publishAccess(where, hadCall, ringSrc, dur, durKnown, ts)
}

// publishAccess classifies (Classify is pure) and publishes "access.classified".
// Always called outside c.mu.
func (c *Classifier) publishAccess(where string, hadCall bool, ringSrc string, dur time.Duration, durKnown bool, ts time.Time) {
	res := Classify(ClassifyInput{
		HadCall:       hadCall,
		Duration:      dur,
		DurationKnown: durKnown,
		VisitorMax:    c.cfg.DurationVisitorMax,
	})
	ev := AccessEvent{
		Type:       res.Type,
		Confidence: res.Confidence,
		Where:      where,
		HadCall:    hadCall,
		RingSource: ringSrc,
		Timestamp:  ts,
	}
	if durKnown {
		ev.DurationS = dur.Seconds()
	}
	c.logger.WithFields(logrus.Fields{
		"type": ev.Type, "confidence": ev.Confidence, "where": where,
		"had_call": hadCall, "duration_s": ev.DurationS,
	}).Info("Access classified")
	c.bus.PublishWithSource("access.classified", ev, "access")
}

// whereOf extracts WHERE from a door.* event (defensive: "" if it does not fit).
func whereOf(e *events.Event) string {
	if e == nil {
		return ""
	}
	if owe, ok := e.Data.(*handlers.OpenWebNetEvent); ok {
		return owe.Where
	}
	return ""
}
