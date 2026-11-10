package sip

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// decodeCall records the arguments of one call to Decode.
type decodeCall struct {
	host string
	port int
	pt   int
	sink string
}

// fakeDecoder simulates AudioTapDecoder: Decode BLOCKS (like the real ffmpeg)
// until Stop() releases it. invoked is signalled (buffered channel) as soon as
// Decode is entered, so tests wait without sleeps or races. block is
// RECREATED on each Decode(): a fakeDecoder can live through several
// Start/Stop cycles (restart), and a closed channel from an earlier session
// must not unblock the next session at once.
type fakeDecoder struct {
	mu      sync.Mutex
	calls   []decodeCall
	stops   int
	invoked chan struct{}
	block   chan struct{} // nil between sessions; Decode() creates it, Stop() closes and clears it
	err     error
}

func newFakeDecoder() *fakeDecoder {
	return &fakeDecoder{invoked: make(chan struct{}, 8)}
}

func (f *fakeDecoder) Decode(host string, port, pt int, sink string) error {
	f.mu.Lock()
	f.calls = append(f.calls, decodeCall{host, port, pt, sink})
	if f.block == nil {
		f.block = make(chan struct{})
	}
	block := f.block
	f.mu.Unlock()
	f.invoked <- struct{}{}
	<-block
	return f.err
}

func (f *fakeDecoder) Stop() {
	f.mu.Lock()
	f.stops++
	block := f.block
	f.block = nil // the next Decode() of a future session creates a new one
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
			// already closed (Decode had already returned by itself)
		default:
			close(block)
		}
	}
}

func (f *fakeDecoder) counts() (calls int, stops int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls), f.stops
}

func (f *fakeDecoder) lastCall() (decodeCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return decodeCall{}, false
	}
	return f.calls[len(f.calls)-1], true
}

// waitInvoked blocks until Decode has been called at least once
// (goroutine↔assertion sync via a channel, no sleeps or races).
func (f *fakeDecoder) waitInvoked(t *testing.T) {
	t.Helper()
	select {
	case <-f.invoked:
	case <-time.After(2 * time.Second):
		t.Fatal("decoder.Decode was never called")
	}
}

// orderCheckTap is a test tap whose Start() checks that the decoder was
// already called (decoderInvoked channel) BEFORE marking itself started. If the
// producer reversed the order, startedAfterDecoder would stay false.
type orderCheckTap struct {
	decoderInvoked <-chan struct{}

	mu                  sync.Mutex
	starts              int
	startedAfterDecoder bool
}

func (t *orderCheckTap) Start() error {
	after := false
	select {
	case <-t.decoderInvoked:
		after = true
	case <-time.After(2 * time.Second):
	}
	t.mu.Lock()
	t.starts++
	t.startedAfterDecoder = after
	t.mu.Unlock()
	return nil
}

func (t *orderCheckTap) Stop() {}

func (t *orderCheckTap) snapshot() (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.starts, t.startedAfterDecoder
}

// TestAudioListenStartArmsDecoderBeforeTap checks the order the design
// requires: the decoder must already be called (listening) before the tap
// starts forwarding. The decoder signals a channel EXCLUSIVE to the test (via
// signalingDecoder) so it does not compete with other reads of fakeDecoder's
// internal channel.
func TestAudioListenStartArmsDecoderBeforeTap(t *testing.T) {
	decoderInvoked := make(chan struct{}, 1)
	dec := newFakeDecoder()
	// Wrapper: forwards the invocation signal to a channel of the test's own,
	// besides the internal channel fakeDecoder uses.
	wrapped := &signalingDecoder{inner: dec, extra: decoderInvoked}

	tap := &orderCheckTap{decoderInvoked: decoderInvoked}
	l := NewAudioListenOnCall(tap, wrapped, 15000, 110, "/tmp/audio-tap-test.wav", logrus.New())

	l.Start("test")

	deadline := time.Now().Add(2 * time.Second)
	for {
		starts, afterDecoder := tap.snapshot()
		if starts > 0 {
			if !afterDecoder {
				t.Fatal("the tap started BEFORE the decoder was called (wrong order)")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tap never started")
		}
		time.Sleep(2 * time.Millisecond)
	}

	l.Stop()
}

// signalingDecoder wraps fakeDecoder and also signals a channel of the test's
// own when Decode is called (before blocking).
type signalingDecoder struct {
	inner *fakeDecoder
	extra chan struct{}
}

func (s *signalingDecoder) Decode(host string, port, pt int, sink string) error {
	s.extra <- struct{}{}
	return s.inner.Decode(host, port, pt, sink)
}

func (s *signalingDecoder) Stop() { s.inner.Stop() }

func TestAudioListenStartCallsDecoderWithExpectedParams(t *testing.T) {
	dec := newFakeDecoder()
	tap := &mockTap{}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	l.Start("test")
	dec.waitInvoked(t)

	call, ok := dec.lastCall()
	if !ok {
		t.Fatal("expected a call to Decode")
	}
	if call.host != "127.0.0.1" {
		t.Fatalf("expected host 127.0.0.1, got %q", call.host)
	}
	if call.port != 15000 {
		t.Fatalf("expected port 15000, got %d", call.port)
	}
	if call.pt != 110 {
		t.Fatalf("expected payloadType 110, got %d", call.pt)
	}
	if call.sink != "/tmp/sink.wav" {
		t.Fatalf("expected sink /tmp/sink.wav, got %q", call.sink)
	}

	l.Stop()
}

func TestAudioListenStartIsIdempotent(t *testing.T) {
	dec := newFakeDecoder()
	tap := &mockTap{}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	l.Start("a")
	dec.waitInvoked(t)
	l.Start("b") // must not start anything again

	if starts, _ := tap.counts(); starts != 1 {
		t.Fatalf("expected 1 tap start (idempotent), got %d", starts)
	}
	if calls, _ := dec.counts(); calls != 1 {
		t.Fatalf("expected 1 decoder call (idempotent), got %d", calls)
	}
	if !l.IsActive() {
		t.Fatal("expected IsActive()=true")
	}

	l.Stop()
}

func TestAudioListenStopDisarmsBoth(t *testing.T) {
	dec := newFakeDecoder()
	tap := &mockTap{}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	l.Start("a")
	dec.waitInvoked(t)
	l.Stop()

	if l.IsActive() {
		t.Fatal("expected IsActive()=false after Stop")
	}
	if _, stops := tap.counts(); stops != 1 {
		t.Fatalf("expected 1 tap stop, got %d", stops)
	}
	if _, stops := dec.counts(); stops != 1 {
		t.Fatalf("expected 1 decoder stop, got %d", stops)
	}
}

func TestAudioListenStopWithoutStartIsNoop(t *testing.T) {
	dec := newFakeDecoder()
	tap := &mockTap{}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	l.Stop()

	if _, stops := tap.counts(); stops != 0 {
		t.Fatalf("Stop without Start must not stop the tap (got %d)", stops)
	}
	if _, stops := dec.counts(); stops != 0 {
		t.Fatalf("Stop without Start must not stop the decoder (got %d)", stops)
	}
}

func TestAudioListenRestartAfterStop(t *testing.T) {
	dec := newFakeDecoder()
	tap := &mockTap{}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	l.Start("a")
	dec.waitInvoked(t)
	l.Stop()

	l.Start("b")
	dec.waitInvoked(t)
	if !l.IsActive() {
		t.Fatal("expected IsActive()=true after re-Start")
	}
	if starts, _ := tap.counts(); starts != 2 {
		t.Fatalf("expected 2 tap starts, got %d", starts)
	}
	if calls, _ := dec.counts(); calls != 2 {
		t.Fatalf("expected 2 decoder calls, got %d", calls)
	}

	l.Stop()
}

func TestAudioListenTapErrorIsNonFatal(t *testing.T) {
	dec := newFakeDecoder()
	tap := &mockTap{err: errors.New("operation not permitted")}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	l.Start("no-permissions") // must not panic
	dec.waitInvoked(t)

	if !l.IsActive() {
		t.Fatal("expected IsActive()=true even if the tap fails")
	}

	l.Stop()
}

func TestAudioListenNilTapAndDecoderAreSafe(t *testing.T) {
	l := NewAudioListenOnCall(nil, nil, 15000, 110, "/tmp/sink.wav", logrus.New())
	l.Start("a") // must not panic
	if !l.IsActive() {
		t.Fatal("expected IsActive()=true even without tap/decoder")
	}
	l.Stop()
	if l.IsActive() {
		t.Fatal("expected IsActive()=false after Stop")
	}
}

func TestAudioListenGuardAutoStopsWhenHangupIsMissed(t *testing.T) {
	dec := newFakeDecoder()
	tap := &mockTap{}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())
	l.SetGuardTimeout(30 * time.Millisecond)
	l.Start("a")
	dec.waitInvoked(t)

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
	if _, stops := dec.counts(); stops != 1 {
		t.Fatalf("expected 1 decoder stop by the guard, got %d", stops)
	}
}

// selfEndingDecoder simulates a decoder whose Decode ENDS BY ITSELF (e.g. its
// internal hard limit) without anyone calling Stop(). invoked signals the
// entry; stops counts the Stop calls (to check that AudioListenOnCall still
// calls decoder.Stop() when it disarms itself).
type selfEndingDecoder struct {
	mu      sync.Mutex
	calls   int
	stops   int
	invoked chan struct{}
}

func newSelfEndingDecoder() *selfEndingDecoder {
	return &selfEndingDecoder{invoked: make(chan struct{}, 1)}
}

func (d *selfEndingDecoder) Decode(host string, port, pt int, sink string) error {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	d.invoked <- struct{}{}
	return nil // natural return, not caused by Stop()
}

func (d *selfEndingDecoder) Stop() {
	d.mu.Lock()
	d.stops++
	d.mu.Unlock()
}

func (d *selfEndingDecoder) counts() (calls, stops int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls, d.stops
}

// signalingTap is a test tap whose Stop() signals a (buffered) channel, to
// sync the test with the disarm without sleeps.
type signalingTap struct {
	mu      sync.Mutex
	starts  int
	stops   int
	stopped chan struct{}
}

func newSignalingTap() *signalingTap {
	return &signalingTap{stopped: make(chan struct{}, 1)}
}

func (t *signalingTap) Start() error {
	t.mu.Lock()
	t.starts++
	t.mu.Unlock()
	return nil
}

func (t *signalingTap) Stop() {
	t.mu.Lock()
	t.stops++
	t.mu.Unlock()
	select {
	case t.stopped <- struct{}{}:
	default:
	}
}

func (t *signalingTap) counts() (starts, stops int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.starts, t.stops
}

// TestAudioListenSelfDisarmsWhenDecoderReturnsNaturally: if Decode() returns
// by itself (e.g. its internal hard limit, which AudioListenOnCall raises but
// does not make infinite) without anyone calling Stop(), the session must
// disarm itself (stop the tap, mark inactive) instead of staying in a false
// state (IsActive()=true with the tap forwarding RTP to a socket nobody reads).
func TestAudioListenSelfDisarmsWhenDecoderReturnsNaturally(t *testing.T) {
	dec := newSelfEndingDecoder()
	tap := newSignalingTap()
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	l.Start("a")

	select {
	case <-tap.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the tap to stop by itself after the natural return of the decoder")
	}

	if l.IsActive() {
		t.Fatal("expected IsActive()=false after the natural return of the decoder")
	}
	if _, stops := tap.counts(); stops != 1 {
		t.Fatalf("expected 1 tap stop, got %d", stops)
	}
	if _, stops := dec.counts(); stops != 1 {
		t.Fatalf("expected 1 decoder stop (Stop called on self-disarm), got %d", stops)
	}
}

// racingDecoder simulates a Decode() whose return the TEST controls
// precisely, CALL BY CALL (a release channel of its own per call, not shared).
// Unlike fakeDecoder, Stop() does NOT release the running call: it models a
// real decoder whose process can take longer to die than the Stop() signal
// (e.g. ffmpeg has not yet received the OS signal), which allows delaying on
// purpose the "natural" return of an already disarmed session until after
// another session has started.
type racingDecoder struct {
	mu       sync.Mutex
	calls    int
	stops    int
	invoked  chan struct{}
	releases []chan struct{}
}

func newRacingDecoder() *racingDecoder {
	return &racingDecoder{invoked: make(chan struct{}, 8)}
}

func (d *racingDecoder) Decode(host string, port, pt int, sink string) error {
	d.mu.Lock()
	d.calls++
	release := make(chan struct{})
	d.releases = append(d.releases, release)
	d.mu.Unlock()
	d.invoked <- struct{}{}
	<-release
	return nil
}

func (d *racingDecoder) Stop() {
	d.mu.Lock()
	d.stops++
	d.mu.Unlock()
	// Deliberately releases no running call.
}

func (d *racingDecoder) counts() (calls, stops int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls, d.stops
}

// releaseCall releases call no. idx (0-based) to Decode().
func (d *racingDecoder) releaseCall(t *testing.T, idx int) {
	t.Helper()
	d.mu.Lock()
	if idx >= len(d.releases) {
		d.mu.Unlock()
		t.Fatalf("releaseCall(%d): only %d calls recorded", idx, len(d.releases))
	}
	ch := d.releases[idx]
	d.mu.Unlock()
	close(ch)
}

// TestAudioListenStaleSelfStopDoesNotTearDownNewerSession reproduces,
// deterministically: the Decode() of an ALREADY stopped session (gen=1)
// returns by itself AFTER a new session (gen=2) is armed. selfStopIfCurrent(1),
// running late, must not disarm session 2. Checked by the ABSENCE of an effect
// after a bounded margin (a negative assertion cannot simply be awaited via a
// channel: a fixed margin is used, not a sleep as the primary sync mechanism;
// the invocation signalling via `invoked` is that).
func TestAudioListenStaleSelfStopDoesNotTearDownNewerSession(t *testing.T) {
	dec := newRacingDecoder()
	tap := &mockTap{}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	l.Start("a") // gen=1
	select {
	case <-dec.invoked:
	case <-time.After(2 * time.Second):
		t.Fatal("the session 1 decoder was never called")
	}

	l.Stop() // disarms session 1: active=false, tap.Stop(), dec.Stop().
	// Session 1's Decode() STAYS blocked on purpose (releases[0] has not
	// been closed): its "natural" return is left pending deliberately.
	if _, stops := dec.counts(); stops != 1 {
		t.Fatalf("expected 1 decoder stop after Stop(), got %d", stops)
	}

	l.Start("b") // gen=2: a new active session, while 1 is still "in flight"
	select {
	case <-dec.invoked:
	case <-time.After(2 * time.Second):
		t.Fatal("the session 2 decoder was never called")
	}
	if !l.IsActive() {
		t.Fatal("expected IsActive()=true after the Start of session 2")
	}

	// Now let session 1's Decode() (gen=1) return, deliberately late: its
	// selfStopIfCurrent(1) runs with session 2 (gen=2) ALREADY active.
	// beginTeardown must reject it by generation.
	dec.releaseCall(t, 0)

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if !l.IsActive() {
		t.Fatal("BUG: session 2 was disarmed by session 1's late auto-stop")
	}
	if starts, stops := tap.counts(); starts != 2 || stops != 1 {
		t.Fatalf("expected tap start=2 stop=1 (only session 1 stopped), got start=%d stop=%d", starts, stops)
	}
	if calls, stops := dec.counts(); calls != 2 || stops != 1 {
		t.Fatalf("expected decoder calls=2 stops=1 (only session 1 stopped), got calls=%d stops=%d", calls, stops)
	}

	l.Stop() // cleanup: really disarms session 2
	dec.releaseCall(t, 1)
}

// decoderWithTimeout is a fake decoder that also records the timeout
// AudioListenOnCall sets on it (it implements audioDecoderTimeoutSetter).
// It blocks in Decode until Stop(), like fakeDecoder.
type decoderWithTimeout struct {
	*fakeDecoder // pointer: avoids copying the embedded sync.Mutex
	mu           sync.Mutex
	timeout      time.Duration
	set          chan struct{}
}

func newDecoderWithTimeout() *decoderWithTimeout {
	return &decoderWithTimeout{
		fakeDecoder: newFakeDecoder(),
		set:         make(chan struct{}, 1),
	}
}

func (d *decoderWithTimeout) SetTimeout(dur time.Duration) {
	d.mu.Lock()
	d.timeout = dur
	d.mu.Unlock()
	select {
	case d.set <- struct{}{}:
	default:
	}
}

func (d *decoderWithTimeout) getTimeout() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.timeout
}

func (d *decoderWithTimeout) waitSet(t *testing.T) {
	t.Helper()
	select {
	case <-d.set:
	case <-time.After(2 * time.Second):
		t.Fatal("SetTimeout was never called")
	}
}

func TestAudioListenSetsDefaultDecoderTimeoutWithoutGuard(t *testing.T) {
	dec := newDecoderWithTimeout()
	tap := &mockTap{}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	l.Start("a")
	dec.waitSet(t)

	if got := dec.getTimeout(); got != defaultAudioListenDecoderTimeout {
		t.Fatalf("expected default timeout %s, got %s", defaultAudioListenDecoderTimeout, got)
	}

	l.Stop()
}

func TestAudioListenSetsDecoderTimeoutFromGuard(t *testing.T) {
	dec := newDecoderWithTimeout()
	tap := &mockTap{}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())
	l.SetGuardTimeout(5 * time.Minute)

	l.Start("a")
	dec.waitSet(t)

	if got := dec.getTimeout(); got != 5*time.Minute {
		t.Fatalf("expected timeout = guard (5m), got %s", got)
	}

	l.Stop()
}

func TestAudioListenGuardCanceledByStop(t *testing.T) {
	dec := newFakeDecoder()
	tap := &mockTap{}
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())
	l.SetGuardTimeout(30 * time.Millisecond)
	l.Start("a")
	dec.waitInvoked(t)
	l.Stop()
	time.Sleep(80 * time.Millisecond) // the guard would have expired by now

	if _, stops := tap.counts(); stops != 1 {
		t.Fatalf("the cancelled guard must not stop again (stops=%d)", stops)
	}

	l.Start("b")
	dec.waitInvoked(t)
	if !l.IsActive() {
		t.Fatal("expected active after re-Start")
	}
	l.Stop()
}

// blockingStartTap is a tap whose Start() BLOCKS until the test closes
// release, to reproduce deterministically the race window: Start() sets
// active=true, releases the lock, and ONLY THEN calls tap.Start() (blocked
// here). entered is closed as soon as Start() is entered (BEFORE blocking), so
// the test knows it is in the window without sleeps.
type blockingStartTap struct {
	mu      sync.Mutex
	starts  int
	stops   int
	entered chan struct{}
	release chan struct{}
}

func newBlockingStartTap() *blockingStartTap {
	return &blockingStartTap{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (t *blockingStartTap) Start() error {
	close(t.entered)
	<-t.release
	t.mu.Lock()
	t.starts++
	t.mu.Unlock()
	return nil
}

func (t *blockingStartTap) Stop() {
	t.mu.Lock()
	t.stops++
	t.mu.Unlock()
}

func (t *blockingStartTap) counts() (starts, stops int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.starts, t.stops
}

// TestAudioListenStartStopRaceDoesNotLeakTap: if a concurrent Stop() runs
// fully (under lock, active=false, tap.Stop() included) WHILE the tap.Start()
// of an earlier Start() is still blocked (outside the lock, on purpose: an
// AF_PACKET socket open), that Stop()'s tap.Stop() is useless because the tap
// had not started yet from its point of view. Without the re-check after
// tap.Start(), the tap would stay started forever with active=false: the
// decoder disarms itself at its hard limit, but the tap (AF_PACKET socket +
// goroutine) has no self-disarm at all: a permanent leak. Synced entirely
// through channels (entered/release/done), no sleeps as the primary sync
// mechanism.
func TestAudioListenStartStopRaceDoesNotLeakTap(t *testing.T) {
	dec := newFakeDecoder()
	tap := newBlockingStartTap()
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	startDone := make(chan struct{})
	go func() {
		l.Start("a") // blocks inside tap.Start() until the test releases it
		close(startDone)
	}()

	// Wait until tap.Start() has been entered (and is blocked): now
	// active=true, but the real tap has not "started" yet, seen from outside.
	select {
	case <-tap.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("tap.Start() was never entered")
	}

	// A concurrent Stop() wins the race: it runs fully (beginTeardown +
	// teardown, including its own tap.Stop(), which does not increase stops
	// here because Start() is still blocked and starts is still 0) while the
	// session's tap.Start() is still blocked.
	l.Stop()

	if l.IsActive() {
		t.Fatal("expected IsActive()=false after the Stop() that won the race")
	}
	if starts, stops := tap.counts(); starts != 0 || stops != 1 {
		t.Fatalf("before release: expected tap start=0 stop=1 (the teardown Stop, without useful effect), got start=%d stop=%d", starts, stops)
	}

	// Release tap.Start(): it "starts" late, after Stop() already ran fully.
	// The fix must notice that the session is no longer active and stop the
	// tap at once, instead of leaking it.
	close(tap.release)

	select {
	case <-startDone:
	case <-time.After(2 * time.Second):
		t.Fatal("l.Start() never returned after releasing tap.Start()")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		starts, stops := tap.counts()
		if starts == 1 && stops == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("BUG: tap leaked after the race: expected start=1 stop=2 (the second stop from the re-check), got start=%d stop=%d", starts, stops)
		}
		time.Sleep(2 * time.Millisecond)
	}

	if l.IsActive() {
		t.Fatal("expected IsActive()=false after the race and the re-check")
	}
}

// staggeredTap is a tap whose FIRST call to Start() BLOCKS until the test
// closes release; LATER calls return at once. It models LoopbackRTPTap.Start
// (idempotent: starting an already running tap again does not block) to
// reproduce, deterministically, a session B that is armed and finishes its
// re-check BEFORE the late tap.Start() of an earlier session A finally returns.
type staggeredTap struct {
	mu      sync.Mutex
	calls   int
	starts  int
	stops   int
	entered chan struct{} // closed on the FIRST entry into Start()
	release chan struct{} // the test closes it to release that first call
}

func newStaggeredTap() *staggeredTap {
	return &staggeredTap{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (t *staggeredTap) Start() error {
	t.mu.Lock()
	t.calls++
	first := t.calls == 1
	t.mu.Unlock()
	if first {
		close(t.entered)
		<-t.release
	}
	t.mu.Lock()
	t.starts++
	t.mu.Unlock()
	return nil
}

func (t *staggeredTap) Stop() {
	t.mu.Lock()
	t.stops++
	t.mu.Unlock()
}

func (t *staggeredTap) counts() (starts, stops int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.starts, t.stops
}

// TestAudioListenGenerationMismatchDoesNotStopNewerSession: the re-check after
// tap.Start() must look ONLY at `active` (does anyone claim the shared tap
// right now?), NEVER at `generation`. a.tap/a.decoder are SINGLE instances
// shared by ALL generations (built once in cmd/main.go, not per session). If
// a full Stop()+Start() (session B, another generation) slips in while
// session A's tap.Start() is still blocked, the shared tap LEGITIMATELY
// belongs to B. When A's late tap.Start() finally returns, it must NOT stop
// it: that would silently cut the audio of B's LIVE call (IsActive() would
// still return true, because B set it), a worse regression than the leak the
// re-check fixed. Synced through channels (entered/release/waitInvoked); the
// only sleeps are bounded margins for NEGATIVE assertions (absence of a wrong
// Stop), not the primary sync mechanism.
func TestAudioListenGenerationMismatchDoesNotStopNewerSession(t *testing.T) {
	dec := newFakeDecoder()
	tap := newStaggeredTap()
	l := NewAudioListenOnCall(tap, dec, 15000, 110, "/tmp/sink.wav", logrus.New())

	startADone := make(chan struct{})
	go func() {
		l.Start("a") // session A (gen=1): its tap.Start() is the 1st call, blocked
		close(startADone)
	}()
	dec.waitInvoked(t) // session A decode called

	select {
	case <-tap.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("session A's tap.Start() was never entered")
	}

	// Disarm A fully (beginTeardown+teardown, including its own
	// decoder.Stop(), which releases A's blocked Decode()) WHILE its
	// tap.Start() is still blocked.
	l.Stop()
	if l.IsActive() {
		t.Fatal("expected IsActive()=false after the Stop() of session A")
	}

	// Start session B (gen=2): its tap.Start() is the 2nd call of the shared
	// tap, which in this fake does NOT block (it models idempotence);
	// l.Start("b") returns with the re-check already done.
	l.Start("b")
	dec.waitInvoked(t) // session B decode called

	if !l.IsActive() {
		t.Fatal("expected IsActive()=true after the Start of session B")
	}
	// stop=1 is the tap.Stop() of A's teardown (the earlier step, without
	// useful effect because the tap was still blocked): there must NOT be a
	// second stop here from arming B.
	if starts, stops := tap.counts(); starts != 1 || stops != 1 {
		t.Fatalf("after arming B (A still blocked): expected tap start=1 (B only) stop=1 (A's teardown), got start=%d stop=%d", starts, stops)
	}

	// Release A's late tap.Start(): its own re-check must see active=true
	// (session B active, even though generation is no longer A's) and must
	// NOT touch the shared tap.
	close(tap.release)

	select {
	case <-startADone:
	case <-time.After(2 * time.Second):
		t.Fatal("l.Start(a) never returned after releasing its tap.Start()")
	}

	// Margin to let A's late re-check run its tap.Stop()/decoder.Stop() (if
	// it wrongly did), and confirm by ABSENCE that session B was not affected.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if !l.IsActive() {
		t.Fatal("BUG: session B was disarmed by A's late tap.Start() (generation re-check regression)")
	}
	// stop is still 1 (the same as before, from A's teardown): A's late
	// re-check must NOT add a second stop on the shared tap, which by now
	// belongs to the active session B.
	if starts, stops := tap.counts(); starts != 2 || stops != 1 {
		t.Fatalf("expected tap start=2 (late A + B) stop=1 (WITHOUT a wrong second stop on B's tap), got start=%d stop=%d", starts, stops)
	}

	l.Stop() // real cleanup of session B
}
