package sip

import (
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"bticino_bridge/pkg/events"
)

// Say must hang up at the end: otherwise the dialog stays alive until the native
// side cuts it and the watcher holds the webrtc socket (re-REGISTER deferred).
func TestAudioCallSayHangsUpWhenDone(t *testing.T) {
	orig := &fakeOriginator{answer: goodAnswer}
	ac := NewAudioCall(orig, &fakeOWN{}, &fakePlayer{}, &fakeCapturer{}, "127.0.0.1", 40100, logrus.New())
	if err := ac.Say("/tmp/hola.wav"); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if !orig.hungUp {
		t.Fatal("Say must hang up (BYE) when playback ends")
	}
}

// With an invalid answer the INVITE already reached 200: hang up (leave no
// orphaned dialog on the device).
func TestAudioCallSayHangsUpOnInvalidAnswer(t *testing.T) {
	orig := &fakeOriginator{answer: "m=audio 7078 RTP/SAVP 98\r\na=rtpmap:98 speex/8000\r\n"}
	ac := NewAudioCall(orig, &fakeOWN{}, &fakePlayer{}, &fakeCapturer{}, "127.0.0.1", 40100, logrus.New())
	if err := ac.Say("/tmp/hola.wav"); err == nil {
		t.Fatal("expected an error")
	}
	if !orig.hungUp {
		t.Fatal("must hang up the dialog if the answer is invalid")
	}
}

// A single audio leg: Say with another leg active → ErrAudioLegBusy.
func TestAudioCallSayKeepsSingleLeg(t *testing.T) {
	ac := NewAudioCall(&fakeOriginator{answer: goodAnswer}, &fakeOWN{}, &fakePlayer{}, &fakeCapturer{}, "127.0.0.1", 40100, logrus.New())
	ac.mu.Lock()
	ac.active = true
	ac.mu.Unlock()
	if err := ac.Say("/tmp/hola.wav"); !errors.Is(err, ErrAudioLegBusy) {
		t.Fatalf("expected ErrAudioLegBusy, got %v", err)
	}
}

// Only InviteWithSDP INVITEs (panel view, AudioCall) go to the native side; MakeCall's
// video self-INVITE (video_on_demand) must be auto-answered by OUR listener.
func TestShouldAutoAnswerOnlyDivertsNativeCalls(t *testing.T) {
	c := &BTicinoSIPClient{originatedCalls: map[string]time.Time{}, nativeCalls: map[string]time.Time{}}
	c.originatedCalls["selfvideo@127.0.0.1"] = time.Now() // MakeCall
	c.originatedCalls["eye@127.0.0.1"] = time.Now()       // InviteWithSDP
	c.nativeCalls["eye@127.0.0.1"] = time.Now()
	if !c.shouldAutoAnswer("selfvideo@127.0.0.1") {
		t.Fatal("the video self-INVITE (MakeCall) must be auto-answered on loopback")
	}
	if c.shouldAutoAnswer("eye@127.0.0.1") {
		t.Fatal("the panel view INVITE must go to the native side, not be auto-answered")
	}
}

// Pressing "Show door panel" with a panel view ALREADY open → ErrEyeBusy with a clear
// log, and WITHOUT publishing panelview.rejected (it would set the HA sensor idle).
func TestPanelViewPressWithViewActive(t *testing.T) {
	bus := events.NewEventBus(2, logrusTestLogger())
	rejected := subscribeOnce(bus, "panelview.rejected")
	logger, hook := logrustest.NewNullLogger()
	marker := &fakeMarker{}
	pv := NewPanelView(cfgEnabled(), &fakeEyeOrig{answer: fakeEyeAnswer}, &fakeFwd{}, nil, marker, bus, "127.0.0.1", 10102, 10104, logger)
	defer func() { _ = pv.Hangup() }()
	if err := pv.Show(); err != nil {
		t.Fatalf("Show: %v", err)
	}
	marker.active = true // the panel view itself turns the native channel on (callActive)
	if err := pv.Show(); !errors.Is(err, ErrEyeBusy) {
		t.Fatalf("expected ErrEyeBusy, got %v", err)
	}
	select {
	case <-rejected:
		t.Fatal("panelview.rejected must not be published with the panel view open")
	case <-time.After(150 * time.Millisecond):
	}
	for _, e := range hook.AllEntries() {
		if e.Level <= logrus.WarnLevel {
			t.Fatalf("misleading log at level %s: %q", e.Level, e.Message)
		}
	}
}
