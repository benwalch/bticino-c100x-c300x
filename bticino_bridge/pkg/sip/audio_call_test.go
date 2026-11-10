package sip

import (
	"errors"
	"sync"
	"testing"
	"time"

	"bticino_bridge/pkg/bticino"

	"github.com/sirupsen/logrus"
)

type fakeOriginator struct {
	gotTarget, gotSDP string
	answer            string
	err               error
	hungUp            bool

	// hungUpCh, if not nil, is closed (once) when Hangup() is called. Like
	// fakeCapturer.done: it syncs with the test without races when a
	// background goroutine triggers Hangup() (the leg closing by itself at the
	// natural end of the receiver).
	hungUpCh   chan struct{}
	hungUpOnce sync.Once
}

func (f *fakeOriginator) InviteWithSDP(target, sdp string) (string, error) {
	f.gotTarget, f.gotSDP = target, sdp
	return f.answer, f.err
}

func (f *fakeOriginator) Hangup() error {
	f.hungUp = true
	if f.hungUpCh != nil {
		f.hungUpOnce.Do(func() { close(f.hungUpCh) })
	}
	return nil
}

type fakeCapturer struct {
	captured  bool
	localPort int
	sinkPath  string
	ans       AudioAnswer
	stopped   bool

	// block, if not nil, makes Capture() block on it until it is closed; it
	// simulates the capture ffmpeg process staying alive (as in production,
	// where Capture() does not return until ffmpeg ends). Stop() closes it
	// (once, via stopOnce), just as the real Stop() kills the process and that
	// makes the blocking Capture() call return. If nil, Capture() returns at
	// once after recording the parameters; it simulates the natural end of the
	// receiver (60s hard timeout or ffmpeg dying by itself).
	block    chan struct{}
	stopOnce sync.Once

	// done, if not nil, is closed right before Capture() returns. OpenDuplex
	// runs Capture in a goroutine: the test must wait on this channel (with a
	// timeout) BEFORE reading the fields above, or the race detector flags an
	// unsynchronized read/write between the goroutine and the test.
	done chan struct{}
}

func (f *fakeCapturer) Capture(localHost string, localPort int, sinkPath string, ans AudioAnswer) error {
	f.captured, f.localPort, f.sinkPath, f.ans = true, localPort, sinkPath, ans
	if f.block != nil {
		<-f.block
	}
	if f.done != nil {
		close(f.done)
	}
	return nil
}
func (f *fakeCapturer) Stop() {
	f.stopped = true
	if f.block != nil {
		f.stopOnce.Do(func() { close(f.block) })
	}
}

type fakeOWN struct{ cmds []string }

func (f *fakeOWN) SendOpenWebNetCommand(cmd string) error { f.cmds = append(f.cmds, cmd); return nil }

type fakePlayer struct {
	played bool
	host   string
	ans    AudioAnswer

	// done: same purpose as in fakeCapturer, for the OpenDuplex sender.
	done chan struct{}
}

func (f *fakePlayer) Play(wav, host string, ans AudioAnswer) error {
	f.played, f.host, f.ans = true, host, ans
	if f.done != nil {
		close(f.done)
	}
	return nil
}
func (f *fakePlayer) Stop() {}

const goodAnswer = "m=audio 7078 RTP/SAVP 98\r\na=rtpmap:98 speex/8000\r\na=crypto:2 AES_CM_128_HMAC_SHA1_80 inline:KEYXYZ\r\n"

func TestAudioCallSayHappyPath(t *testing.T) {
	orig := &fakeOriginator{answer: goodAnswer}
	own := &fakeOWN{}
	pl := &fakePlayer{}
	ac := NewAudioCall(orig, own, pl, &fakeCapturer{}, "127.0.0.1", 40100, logrus.New())

	if err := ac.Say("/tmp/hola.wav"); err != nil {
		t.Fatalf("Say: %v", err)
	}
	// invited c300x with a real audio SDP
	if orig.gotTarget != bticino.SIPUsernameForVariant(bticino.DeviceVariant) {
		t.Fatalf("target: %q", orig.gotTarget)
	}
	if !contains(orig.gotSDP, "m=audio 40100 RTP/SAVP 98") || !contains(orig.gotSDP, "AES_CM_128_HMAC_SHA1_80") {
		t.Fatalf("wrong SDP offer:\n%s", orig.gotSDP)
	}
	// armed the audio with WHO=8
	if len(own.cmds) == 0 || own.cmds[0] != "*8*2#5#4*16##" {
		t.Fatalf("did not arm WHO=8: %v", own.cmds)
	}
	// played to the intercom's port+key
	if !pl.played || pl.ans.Port != 7078 || pl.ans.CryptoKey != "KEYXYZ" {
		t.Fatalf("player called wrongly: %+v", pl)
	}
}

func TestAudioCallSayInviteError(t *testing.T) {
	ac := NewAudioCall(&fakeOriginator{err: errors.New("403")}, &fakeOWN{}, &fakePlayer{}, &fakeCapturer{}, "127.0.0.1", 40100, logrus.New())
	if err := ac.Say("/tmp/hola.wav"); err == nil {
		t.Fatal("expected an error if the INVITE fails")
	}
}

func TestAudioCallSayBadAnswer(t *testing.T) {
	// answer without supported crypto → error, and it must NOT play.
	pl := &fakePlayer{}
	ac := NewAudioCall(&fakeOriginator{answer: "m=audio 7078 RTP/SAVP 98\r\na=rtpmap:98 speex/8000\r\n"}, &fakeOWN{}, pl, &fakeCapturer{}, "127.0.0.1", 40100, logrus.New())
	if err := ac.Say("/tmp/hola.wav"); err == nil {
		t.Fatal("expected an error without supported crypto")
	}
	if pl.played {
		t.Fatal("must not play if the answer is invalid")
	}
}

func TestAudioCallOpenDuplexHappyPath(t *testing.T) {
	orig := &fakeOriginator{answer: goodAnswer}
	own := &fakeOWN{}
	pl := &fakePlayer{done: make(chan struct{})}
	cap := &fakeCapturer{done: make(chan struct{})}
	ac := NewAudioCall(orig, own, pl, cap, "127.0.0.1", 40100, logrus.New())

	ans, err := ac.OpenDuplex("/tmp/door.wav", "/tmp/hola.wav")
	if err != nil {
		t.Fatalf("OpenDuplex: %v", err)
	}
	// OpenDuplex runs receiver and sender in separate goroutines without waiting
	// for them; sync with their end BEFORE reading the fakes' fields (otherwise
	// there is an unsynchronized read/write → -race flags it).
	select {
	case <-cap.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for the receiver (capturer) to end")
	}
	select {
	case <-pl.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for the sender (player) to end")
	}
	if orig.gotTarget != bticino.SIPUsernameForVariant(bticino.DeviceVariant) || !contains(orig.gotSDP, "m=audio 40100 RTP/SAVP 98") {
		t.Fatalf("wrong offer: %q / %s", orig.gotTarget, orig.gotSDP)
	}
	if len(own.cmds) == 0 || own.cmds[0] != "*8*2#5#4*16##" {
		t.Fatalf("did not arm WHO=8: %v", own.cmds)
	}
	if !cap.captured || cap.localPort != 40100 || cap.sinkPath != "/tmp/door.wav" || cap.ans.CryptoKey != "KEYXYZ" {
		t.Fatalf("wrong capturer: %+v", cap)
	}
	if !pl.played || pl.ans.Port != 7078 {
		t.Fatalf("wrong player: %+v", pl)
	}
	if ans.Port != 7078 {
		t.Fatalf("wrong answer returned: %+v", ans)
	}
}

func TestAudioCallOpenDuplexBusy(t *testing.T) {
	// block: the receiver must not close the leg by itself during the test (if
	// Capture() returned at once, the automatic close could win the race against
	// the second OpenDuplex and the leg would no longer look busy: an
	// intermittent false negative). Blocking keeps the leg active
	// deterministically for the duration of the test.
	cap := &fakeCapturer{block: make(chan struct{})}
	ac := NewAudioCall(&fakeOriginator{answer: goodAnswer}, &fakeOWN{}, &fakePlayer{}, cap, "127.0.0.1", 40100, logrus.New())
	t.Cleanup(func() { _ = ac.Close() })
	if _, err := ac.OpenDuplex("/tmp/a.wav", ""); err != nil {
		t.Fatalf("primera: %v", err)
	}
	if _, err := ac.OpenDuplex("/tmp/b.wav", ""); err != ErrAudioLegBusy {
		t.Fatalf("expected ErrAudioLegBusy, got %v", err)
	}
}

func TestAudioCallCloseHangsUp(t *testing.T) {
	orig := &fakeOriginator{answer: goodAnswer}
	// block: the receiver stays "alive" (like a running ffmpeg) until Close()
	// calls capturer.Stop() explicitly, so the test's Close() always does the
	// real work (Hangup + capturer.Stop) in its own goroutine, with no race
	// against the receiver's automatic close (which only fires when block closes).
	cap := &fakeCapturer{block: make(chan struct{})}
	ac := NewAudioCall(orig, &fakeOWN{}, &fakePlayer{}, cap, "127.0.0.1", 40100, logrus.New())
	if _, err := ac.OpenDuplex("/tmp/a.wav", ""); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := ac.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !orig.hungUp || !cap.stopped {
		t.Fatalf("Close must Hangup and stop the capturer: hungUp=%v stopped=%v", orig.hungUp, cap.stopped)
	}
	if ac.IsActive() {
		t.Fatal("IsActive must be false after Close")
	}
	// idempotente
	if err := ac.Close(); err != nil {
		t.Fatalf("Close idempotente: %v", err)
	}
}

// TestAudioCallReceiverNaturalExitTearsDownLeg: if the receiver returns by
// itself (60s hard timeout in Capture, or ffmpeg dying by itself) WITHOUT
// /stop, the leg must still be freed (active=false, BYE sent); otherwise it is
// stuck forever and every later OpenDuplex returns ErrAudioLegBusy.
func TestAudioCallReceiverNaturalExitTearsDownLeg(t *testing.T) {
	orig := &fakeOriginator{answer: goodAnswer, hungUpCh: make(chan struct{})}
	// cap without block: Capture() returns at once after recording the
	// parameters, simulating the natural end of the receiver (no /stop).
	cap := &fakeCapturer{}
	ac := NewAudioCall(orig, &fakeOWN{}, &fakePlayer{}, cap, "127.0.0.1", 40100, logrus.New())

	if _, err := ac.OpenDuplex("/tmp/a.wav", ""); err != nil {
		t.Fatalf("open: %v", err)
	}

	// Sync with the automatic close (Hangup is the last thing Close() does
	// before returning) instead of reading the fields directly, so -race does
	// not flag an unsynchronized read with the receiver goroutine.
	select {
	case <-orig.hungUpCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for the automatic BYE after the natural end of the receiver")
	}

	if !orig.hungUp {
		t.Fatal("the natural end of the receiver must hang up the call (BYE)")
	}
	if !cap.stopped {
		t.Fatal("the natural end of the receiver must also stop the player/capturer (via Close)")
	}
	if ac.IsActive() {
		t.Fatal("the leg must be inactive after the natural end of the receiver (otherwise it gets stuck)")
	}
	// A following OpenDuplex must NOT return ErrAudioLegBusy: the leg freed
	// itself, without /stop.
	if _, err := ac.OpenDuplex("/tmp/b.wav", ""); err != nil {
		t.Fatalf("expected to reopen the leg after the natural end of the receiver, got %v", err)
	}
}

func TestAudioCallOpenDuplexInviteError(t *testing.T) {
	cap := &fakeCapturer{}
	ac := NewAudioCall(&fakeOriginator{err: errors.New("403")}, &fakeOWN{}, &fakePlayer{}, cap, "127.0.0.1", 40100, logrus.New())
	if _, err := ac.OpenDuplex("/tmp/a.wav", ""); err == nil {
		t.Fatal("expected an error if the INVITE fails")
	}
	if cap.captured {
		t.Fatal("must not capture if the INVITE fails")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
