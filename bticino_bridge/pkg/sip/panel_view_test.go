package sip

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/events"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

// --- Injected fakes (atomic counters: waitFor reads them from another goroutine) ---

type fakeEyeOrig struct {
	answer    string
	inviteErr error
	invites   atomic.Int64
	hangups   atomic.Int64
	onInvite  func() // called INSIDE InviteWithSDP (simulates a yield in the middle of the round trip)
}

func (f *fakeEyeOrig) InviteWithSDP(t, s string) (string, error) {
	f.invites.Add(1)
	if f.onInvite != nil {
		f.onInvite()
	}
	return f.answer, f.inviteErr
}
func (f *fakeEyeOrig) Hangup() error  { f.hangups.Add(1); return nil }
func (f *fakeEyeOrig) CallID() string { return "eye-call-1" }

type fakeFwd struct {
	started atomic.Int64
	stopped atomic.Int64
	dst     atomic.Value // destination of the last CaptureForward
}

func (f *fakeFwd) CaptureForward(h string, p int, d string, a AudioAnswer) error {
	f.dst.Store(d)
	f.started.Add(1)
	return nil
}
func (f *fakeFwd) Stop() { f.stopped.Add(1) }

type fakeMarker struct{ active bool }

func (f *fakeMarker) IsCallActive() bool { return f.active }

// Realistic panel view answer SDP: TWO m= sections (video recvonly + audio sendrecv).
// Checks that ParseAudioAnswer picks the AUDIO leg (5000), not the video one (7000).
const fakeEyeAnswer = "v=0\r\n" +
	"m=video 7000 RTP/SAVP 96\r\n" +
	"a=rtpmap:96 H264/90000\r\n" +
	"a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:VIDEOvideoVIDEOvideoVIDEOvideoVIDEOvideo\r\n" +
	"m=audio 5000 RTP/SAVP 98\r\n" +
	"a=rtpmap:98 speex/8000\r\n" +
	"a=crypto:2 AES_CM_128_HMAC_SHA1_80 inline:QUJDRA==\r\n"

func logrusTestLogger() *logrus.Logger {
	l := logrus.New()
	l.SetLevel(logrus.PanicLevel) // silent in the tests
	return l
}

// subscribeOnce captures the first Event published with that name in a channel of cap 1.
func subscribeOnce(bus events.EventBus, name string) chan *events.Event {
	ch := make(chan *events.Event, 1)
	bus.Subscribe(name, func(e *events.Event) {
		select {
		case ch <- e:
		default:
		}
	})
	return ch
}

func newPV(orig *fakeEyeOrig, fwd *fakeFwd, m *fakeMarker, bus events.EventBus, cfg config.PanelViewConfig) *PanelView {
	return NewPanelView(cfg, orig, fwd, nil, m, bus, "127.0.0.1", 10102, 10104, logrusTestLogger())
}

func cfgEnabled() config.PanelViewConfig {
	return config.PanelViewConfig{Enabled: true, DEVADDR: "11", TimeoutSeconds: 30, CallerUser: "webrtc"}
}

func TestPanelViewHappyPathEmitsEvents(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	gotConnected := subscribeOnce(bus, "panelview.connected")
	gotSipConn := subscribeOnce(bus, "sip.call.connected")
	orig := &fakeEyeOrig{answer: fakeEyeAnswer}
	fwd := &fakeFwd{}
	pv := newPV(orig, fwd, &fakeMarker{active: false}, bus, cfgEnabled())
	_ = pv.Start()
	defer func() { _ = pv.Hangup() }() // stop the auto-off timer (30 s) on exit

	if err := pv.Show(); err != nil {
		t.Fatalf("Show: %v", err)
	}
	if n := orig.invites.Load(); n != 1 {
		t.Fatalf("invites=%d", n)
	}
	ev := <-gotConnected
	data := ev.Data.(map[string]interface{})
	if data["call_id"] != "eye-call-1" {
		t.Fatalf("unexpected call_id: %+v", data)
	}
	if data["srtp_key"] == "" {
		t.Fatalf("empty srtp_key: %+v", data)
	}
	port, ok := data["media_port"].(int)
	if !ok || port != 5000 {
		t.Fatalf("media_port should be the AUDIO leg (5000), got %+v", data["media_port"])
	}
	<-gotSipConn // video: arms the passive taps
	waitFor(t, func() bool { return fwd.started.Load() == 1 }, "the listening forwarder should start")
}

func TestPanelViewRejectsWhenCallActive(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	orig := &fakeEyeOrig{answer: fakeEyeAnswer}
	pv := newPV(orig, &fakeFwd{}, &fakeMarker{active: true}, bus, cfgEnabled())
	_ = pv.Start()
	if err := pv.Show(); err == nil {
		t.Fatal("should reject: native call active")
	}
	if n := orig.invites.Load(); n != 0 {
		t.Fatalf("should not place an INVITE with callActive (invites=%d)", n)
	}
}

func TestPanelViewRejectsCleanlyOnInviteError(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	orig := &fakeEyeOrig{inviteErr: errors.New("486 Busy")}
	fwd := &fakeFwd{}
	pv := newPV(orig, fwd, &fakeMarker{}, bus, cfgEnabled())
	_ = pv.Start()
	if err := pv.Show(); err == nil {
		t.Fatal("should pass on the INVITE error")
	}
	if pv.Active() {
		t.Fatal("must not stay active after an INVITE failure")
	}
	if n := fwd.started.Load(); n != 0 {
		t.Fatalf("should not arm media without a connection (started=%d)", n)
	}
}

func TestPanelViewAutoOffTimerHangsUp(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	cfg := cfgEnabled()
	cfg.TimeoutSeconds = 0 // overwritten below
	orig := &fakeEyeOrig{answer: fakeEyeAnswer}
	fwd := &fakeFwd{}
	pv := newPV(orig, fwd, &fakeMarker{}, bus, cfg)
	pv.setTimeoutForTest(20 * time.Millisecond)
	_ = pv.Start()
	if err := pv.Show(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return orig.hangups.Load() == 1 && fwd.stopped.Load() == 1 }, "auto-off should hang up and stop media")
	if pv.Active() {
		t.Fatal("should return to IDLE after the auto-off")
	}
}

func TestPanelViewRejectsWhenDisabledOrNoDevaddr(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	for _, cfg := range []config.PanelViewConfig{
		{Enabled: false, DEVADDR: "11"},
		{Enabled: true, DEVADDR: ""},
	} {
		orig := &fakeEyeOrig{answer: fakeEyeAnswer}
		pv := newPV(orig, &fakeFwd{}, &fakeMarker{}, bus, cfg)
		_ = pv.Start()
		if err := pv.Show(); err == nil {
			t.Fatalf("should reject with cfg=%+v", cfg)
		}
		if n := orig.invites.Load(); n != 0 {
			t.Fatalf("should not place an INVITE (cfg=%+v)", cfg)
		}
	}
}

func TestPanelViewCedesToIncomingDoorbell(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	orig := &fakeEyeOrig{answer: fakeEyeAnswer}
	fwd := &fakeFwd{}
	pv := newPV(orig, fwd, &fakeMarker{}, bus, cfgEnabled())
	_ = pv.Start()
	if err := pv.Show(); err != nil {
		t.Fatal(err)
	}
	bus.Publish("doorcall.incoming", map[string]interface{}{"call_id": "ring1"})
	waitFor(t, func() bool { return orig.hangups.Load() == 1 }, "the panel view should yield to a real ring")
}

// If a yield (doorcall.incoming / Hang up button) wins the mutex while the
// blocking INVITE is in flight, Show must NOT arm media or timer; it must
// close the dialog just opened and return to IDLE.
func TestPanelViewCedesDuringBlockingInvite(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	orig := &fakeEyeOrig{answer: fakeEyeAnswer}
	fwd := &fakeFwd{}
	pv := newPV(orig, fwd, &fakeMarker{}, bus, cfgEnabled())
	_ = pv.Start()
	// The originator hangs up right before returning the answer (as if
	// doorcall.incoming slipped in during the INVITE round trip).
	orig.onInvite = func() { _ = pv.Hangup() }
	_ = pv.Show()
	if pv.Active() {
		t.Fatal("must not stay active after yielding during the INVITE")
	}
	if n := fwd.started.Load(); n != 0 {
		t.Fatalf("should not arm the forwarder when yielding (started=%d)", n)
	}
	if n := orig.hangups.Load(); n < 1 {
		t.Fatalf("should hang up the SIP dialog just opened (hangups=%d)", n)
	}
}

// panelview.ended must carry the real call_id (not empty), so that
// TalkSender.onEnded's orphaned-ffmpeg guard works.
func TestPanelViewEndedCarriesCallID(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	gotEnded := subscribeOnce(bus, "panelview.ended")
	orig := &fakeEyeOrig{answer: fakeEyeAnswer}
	pv := newPV(orig, &fakeFwd{}, &fakeMarker{}, bus, cfgEnabled())
	_ = pv.Start()
	if err := pv.Show(); err != nil {
		t.Fatal(err)
	}
	_ = pv.Hangup()
	ev := <-gotEnded
	data := ev.Data.(map[string]interface{})
	if data["call_id"] != "eye-call-1" {
		t.Fatalf("panelview.ended should carry the panel view's real call_id, got %+v", data["call_id"])
	}
}

func TestPanelViewHangsUpOnDoorcallHangupRequest(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	orig := &fakeEyeOrig{answer: fakeEyeAnswer}
	fwd := &fakeFwd{}
	pv := newPV(orig, fwd, &fakeMarker{}, bus, cfgEnabled())
	_ = pv.Start()
	if err := pv.Show(); err != nil {
		t.Fatal(err)
	}
	bus.Publish("doorcall.hangup.request", nil) // existing Hang up button
	waitFor(t, func() bool { return orig.hangups.Load() == 1 && fwd.stopped.Load() == 1 }, "the Hang up button should close the panel view")
}

// An invalid answer (e.g. the 200 WITHOUT SDP of the previous BYE that slipped in
// on 2026-10-13) must leave a trace at Warn level: Show() is called from the bus
// with the error dropped, so without this log the immediate BYE was invisible.
func TestPanelViewInvalidAnswerIsLogged(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	orig := &fakeEyeOrig{answer: "v=0\r\n"}
	logger, hook := logrustest.NewNullLogger()
	pv := NewPanelView(cfgEnabled(), orig, &fakeFwd{}, nil, &fakeMarker{}, bus, "127.0.0.1", 10102, 10104, logger)

	if err := pv.Show(); err == nil {
		t.Fatal("expected a parse error")
	}
	if orig.hangups.Load() != 1 {
		t.Fatalf("hangups=%d, expected 1 (leave no orphaned dialog)", orig.hangups.Load())
	}
	var found bool
	for _, e := range hook.AllEntries() {
		if e.Level <= logrus.WarnLevel && strings.Contains(e.Message, "answer") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Warn log for the invalid answer; entries: %d", len(hook.AllEntries()))
	}
	if pv.Active() {
		t.Fatal("the panel view must be released after the failure")
	}
}

// If the native side hangs up the panel view (sip.dialog.ended with ITS call_id),
// PanelView tears down: it stops the forwarder and publishes panelview.ended (the
// HA sensor returns to idle). A dialog.ended of ANOTHER dialog leaves the view alone.
func TestPanelViewTearsDownOnRemoteBYE(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	gotEnded := subscribeOnce(bus, "panelview.ended")
	orig := &fakeEyeOrig{answer: fakeEyeAnswer}
	fwd := &fakeFwd{}
	pv := newPV(orig, fwd, &fakeMarker{}, bus, cfgEnabled())
	_ = pv.Start()
	defer func() { _ = pv.Hangup() }()
	if err := pv.Show(); err != nil {
		t.Fatalf("Show: %v", err)
	}

	bus.Publish("sip.dialog.ended", map[string]interface{}{"call_id": "other-dialog", "reason": "remote_bye"})
	time.Sleep(100 * time.Millisecond)
	if !pv.Active() {
		t.Fatal("another dialog's dialog.ended must not close the panel view")
	}

	bus.Publish("sip.dialog.ended", map[string]interface{}{"call_id": "eye-call-1", "reason": "remote_bye"})
	select {
	case e := <-gotEnded:
		if d := e.Data.(map[string]interface{}); d["reason"] != "remote_bye" {
			t.Fatalf("reason=%v, expected remote_bye", d["reason"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("panelview.ended was not published after the remote BYE")
	}
	if pv.Active() {
		t.Fatal("the panel view must be closed")
	}
	if fwd.stopped.Load() < 1 {
		t.Fatal("the listening forwarder must stop")
	}
}

// The panel view's listening goes to the PRIORITY input of the audio relay (not to
// 10000, shared with the silent lo:5000 tap): that is what is heard in HA.
func TestPanelViewListeningUsesPriorityInput(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	fwd := &fakeFwd{}
	pv := newPV(&fakeEyeOrig{answer: fakeEyeAnswer}, fwd, &fakeMarker{}, bus, cfgEnabled())
	defer func() { _ = pv.Hangup() }()
	if err := pv.Show(); err != nil {
		t.Fatalf("Show: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for fwd.started.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got, want := fwd.dst.Load(), fmt.Sprintf("127.0.0.1:%d", EyeAudioRelayPort); got != want {
		t.Fatalf("listening forwards to %v, expected %s", got, want)
	}
}
