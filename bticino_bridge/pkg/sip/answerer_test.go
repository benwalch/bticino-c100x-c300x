package sip

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/events"
)

// fakeTransport implements AnswererTransport with scriptable channels.
type fakeTransport struct {
	mu     sync.Mutex
	sent   []string
	in     chan *SIPMessage
	closed bool
}

func newFakeTransport() *fakeTransport { return &fakeTransport{in: make(chan *SIPMessage, 16)} }
func (f *fakeTransport) Send(raw string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, raw)
	return nil
}
func (f *fakeTransport) Recv() (*SIPMessage, error) {
	m, ok := <-f.in
	if !ok {
		return nil, errTransportClosed
	}
	return m, nil
}
func (f *fakeTransport) LocalHost() string { return "127.0.0.1" }
func (f *fakeTransport) LocalPort() int    { return 41000 }
func (f *fakeTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		close(f.in)
		f.closed = true
	}
	return nil
}
func (f *fakeTransport) lastSentContaining(s string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if strings.Contains(f.sent[i], s) {
			return f.sent[i]
		}
	}
	return ""
}
func (f *fakeTransport) sentMatching(pred func(string) bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if pred(f.sent[i]) {
			return f.sent[i]
		}
	}
	return ""
}
func (f *fakeTransport) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}
func (f *fakeTransport) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return ""
	}
	return f.sent[len(f.sent)-1]
}

func inviteMsg(callID, cseq string) *SIPMessage {
	body := "v=0\r\nm=audio 44590 RTP/SAVP 96\r\nm=video 13876 RTP/SAVP 96\r\n"
	return &SIPMessage{
		Method: "INVITE", RequestURI: "sip:alluser@YOUR_GATEWAY_ID.bs.iotleg.com",
		Headers: map[string]string{"Call-ID": callID, "CSeq": cseq + " INVITE",
			"From": "<sip:c300x@YOUR_GATEWAY_ID.bs.iotleg.com>;tag=aa", "To": "<sip:alluser@YOUR_GATEWAY_ID.bs.iotleg.com>"},
		ViaHeaders:  []string{"SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bKaaa"},
		RecordRoute: []string{"<sip:127.0.0.1:5060;transport=tcp;lr>"},
		Body:        body,
	}
}
func msg(method string, hdr map[string]string) *SIPMessage {
	return &SIPMessage{Method: method, Headers: hdr}
}

func newTestAnswerer(f *fakeTransport, bus events.EventBus) *Answerer {
	cfg := config.AnswererConfig{Enabled: true, User: "bridge", AudioPort: 10100, VideoPort: 10102, MaxCallSeconds: 120}
	a := NewAnswerer(cfg, "YOUR_GATEWAY_ID.bs.iotleg.com", f, bus, logrus.New())
	return a
}

func TestAnswerer_UnansweredRing_CANCEL(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	a := newTestAnswerer(f, bus)
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("c1", "20")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180 Ringing")
	if a.StateString() != "ringing" {
		t.Fatalf("state=%q, expected ringing", a.StateString())
	}
	// CANCEL with its own Via, other than the INVITE's, and its CSeq "20 CANCEL".
	cancel := &SIPMessage{Method: "CANCEL",
		Headers:    map[string]string{"Call-ID": "c1", "CSeq": "20 CANCEL"},
		ViaHeaders: []string{"SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bKcancel"}}
	f.in <- cancel
	waitFor(t, func() bool { return f.lastSentContaining("487 Request Terminated") != "" }, "487")
	waitFor(t, func() bool { return a.StateString() == "idle" }, "back to idle")
	// The 200 to the CANCEL must carry the CANCEL's own CSeq and Via, not the INVITE's.
	ok200 := f.sentMatching(func(s string) bool {
		return strings.Contains(s, "SIP/2.0 200 OK") && strings.Contains(s, "CSeq: 20 CANCEL")
	})
	if ok200 == "" {
		t.Fatal("no 200 OK was sent to the CANCEL with CSeq: 20 CANCEL")
	}
	if !strings.Contains(ok200, "z9hG4bKcancel") {
		t.Fatalf("the 200 to CANCEL does not echo the CANCEL's Via:\n%s", ok200)
	}
	// The 487 (response to the INVITE) DOES use the INVITE's CSeq.
	if !strings.Contains(f.lastSentContaining("487 Request Terminated"), "CSeq: 20 INVITE") {
		t.Fatal("the 487 must keep CSeq: 20 INVITE")
	}
}

func TestAnswerer_Answer_200_ACK_IN_CALL_then_BYE(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	a := newTestAnswerer(f, bus)
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("c2", "21")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180")
	if err := a.Answer(); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	got := f.lastSentContaining("200 OK")
	if got == "" || !strings.Contains(got, "m=audio 10100") {
		t.Fatalf("200 OK without our own SDP:\n%s", got)
	}
	f.in <- msg("ACK", map[string]string{"Call-ID": "c2", "CSeq": "21 ACK"})
	waitFor(t, func() bool { return a.StateString() == "in_call" }, "in_call")
	if err := a.Hangup(); err != nil {
		t.Fatalf("Hangup: %v", err)
	}
	waitFor(t, func() bool { return f.lastSentContaining("BYE ") != "" }, "BYE saliente")
	waitFor(t, func() bool { return a.StateString() == "idle" }, "idle")
}

func TestAnswerer_DoorcallAnsweredCarriesMediaParams(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	// Capture the doorcall.answered payload.
	got := make(chan map[string]interface{}, 1)
	bus.Subscribe("doorcall.answered", func(e *events.Event) {
		if m, ok := e.Data.(map[string]interface{}); ok {
			got <- m
		}
	})
	a := newTestAnswerer(f, bus)
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("cm", "50") // inviteMsg has m=audio 44590 in the offer
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180")
	if err := a.Answer(); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	select {
	case m := <-got:
		if m["media_host"] != "127.0.0.1" {
			t.Errorf("media_host=%v", m["media_host"])
		}
		if mp, _ := m["media_port"].(int); mp != 44590 {
			t.Errorf("media_port=%v, expected 44590", m["media_port"])
		}
		if k, _ := m["srtp_key"].(string); k == "" {
			t.Error("empty srtp_key")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("doorcall.answered did not arrive")
	}
}

// doorcall.incoming must carry call_type classified from the INVITE's SDP
// (inviteMsg has m=audio + m=video => entrance).
func TestAnswerer_DoorcallIncomingCarriesCallType(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	got := make(chan map[string]interface{}, 1)
	bus.Subscribe("doorcall.incoming", func(e *events.Event) {
		if m, ok := e.Data.(map[string]interface{}); ok {
			got <- m
		}
	})
	a := newTestAnswerer(f, bus)
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("ct", "70")
	select {
	case m := <-got:
		if m["call_type"] != CallTypeEntrance {
			t.Errorf("call_type=%v, expected %q", m["call_type"], CallTypeEntrance)
		}
		if m["call_id"] != "ct" {
			t.Errorf("call_id=%v, expected ct", m["call_id"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("doorcall.incoming did not arrive")
	}
}

func TestAnswerer_AnswerAfterCancel_FailsCleanly(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	a := newTestAnswerer(f, bus)
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("c3", "22")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180")
	f.in <- msg("CANCEL", map[string]string{"Call-ID": "c3", "CSeq": "22 CANCEL"})
	waitFor(t, func() bool { return a.StateString() == "idle" }, "idle")
	if err := a.Answer(); err == nil {
		t.Fatal("Answer after CANCEL must fail (ErrNotRinging)")
	}
}

func TestAnswerer_SecondInviteWhileBusy_486(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	a := newTestAnswerer(f, bus)
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("c4", "23")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180")
	f.in <- inviteMsg("c5", "1") // another Call-ID
	waitFor(t, func() bool { return f.lastSentContaining("486 Busy Here") != "" }, "486")
	if a.StateString() != "ringing" {
		t.Fatalf("the 2nd INVITE must not change the state, got %q", a.StateString())
	}
}

func TestAnswerer_IncomingBYE_200_and_idle(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	a := newTestAnswerer(f, bus)
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("c6", "24")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180")
	a.Answer()
	f.in <- msg("ACK", map[string]string{"Call-ID": "c6", "CSeq": "24 ACK"})
	waitFor(t, func() bool { return a.StateString() == "in_call" }, "in_call")
	before := f.count()
	f.in <- msg("BYE", map[string]string{"Call-ID": "c6", "CSeq": "25 BYE",
		"From": "<sip:c300x@YOUR_GATEWAY_ID.bs.iotleg.com>;tag=aa", "To": "<sip:alluser@YOUR_GATEWAY_ID.bs.iotleg.com>;tag=x"})
	waitFor(t, func() bool { return a.StateString() == "idle" }, "idle after an incoming BYE")
	// The incoming BYE must get its own 200 (not confused with the 200 of the earlier Answer):
	// the last message sent after the BYE must carry its Call-ID + CSeq and the 200.
	last := f.last()
	if f.count() <= before {
		t.Fatal("the incoming BYE produced no response")
	}
	if !strings.Contains(last, "200") || !strings.Contains(last, "Call-ID: c6") || !strings.Contains(last, "CSeq: 25 BYE") {
		t.Fatalf("the 200 to the incoming BYE does not match (Call-ID c6 / CSeq 25 BYE):\n%s", last)
	}
}

func TestAnswerer_DedupeINVITERetransmission(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	a := newTestAnswerer(f, bus)
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("c7", "26")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180")
	f.in <- inviteMsg("c7", "26") // retransmission: same Call-ID+CSeq
	time.Sleep(50 * time.Millisecond)
	// Still ringing, no 486 (not treated as a new call).
	if a.StateString() != "ringing" {
		t.Fatalf("retransmission broke the state: %q", a.StateString())
	}
	if f.lastSentContaining("486 Busy Here") != "" {
		t.Fatal("retransmission must not give 486")
	}
}

// TestAnswerer_RingGuard_NoCancel_480AndBackToIdle: if nobody answers from HA and the entrance
// panel's CANCEL is lost, the answerer must not stay RINGING forever (it would answer 486 to every
// later ring). After ring_timeout_seconds it closes the INVITE with 480 and returns to idle; the
// next ring gets 180 again.
func TestAnswerer_RingGuard_NoCancel_480AndBackToIdle(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	cfg := config.AnswererConfig{Enabled: true, User: "bridge", AudioPort: 10100, VideoPort: 10102,
		MaxCallSeconds: 120, RingTimeoutSeconds: 1}
	a := NewAnswerer(cfg, "YOUR_GATEWAY_ID.bs.iotleg.com", f, bus, logrus.New())
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("cr", "40")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180")
	waitFor(t, func() bool { return f.lastSentContaining("480 Temporarily Unavailable") != "" }, "480 from the ring guard")
	if !strings.Contains(f.lastSentContaining("480 Temporarily Unavailable"), "CSeq: 40 INVITE") {
		t.Fatal("the 480 must answer the INVITE (CSeq: 40 INVITE)")
	}
	waitFor(t, func() bool { return a.StateString() == "idle" }, "back to idle")
	// A new ring must ring again (180), not 486 Busy.
	f.in <- inviteMsg("cr2", "41")
	waitFor(t, func() bool {
		return strings.Contains(f.last(), "180 Ringing") && strings.Contains(f.last(), "cr2")
	}, "180 on the second ring")
}

// TestAnswerer_RingGuard_DoesNotFireAfterAnswer: answering disarms the ring guard; the
// running call is only limited by max_call_seconds.
func TestAnswerer_RingGuard_DoesNotFireAfterAnswer(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	cfg := config.AnswererConfig{Enabled: true, User: "bridge", AudioPort: 10100, VideoPort: 10102,
		MaxCallSeconds: 120, RingTimeoutSeconds: 1}
	a := NewAnswerer(cfg, "YOUR_GATEWAY_ID.bs.iotleg.com", f, bus, logrus.New())
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("ca", "50")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180")
	if err := a.Answer(); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	f.in <- msg("ACK", map[string]string{"Call-ID": "ca", "CSeq": "50 ACK"})
	waitFor(t, func() bool { return a.StateString() == "in_call" }, "in_call")
	time.Sleep(1500 * time.Millisecond)
	if a.StateString() != "in_call" || f.lastSentContaining("480") != "" {
		t.Fatalf("the ring guard fired after the answer: state=%s", a.StateString())
	}
}

// TestAnswerer_StopWithPendingRing_NoPanic: if the bridge stops while it rings, the ring guard
// must not fire after Stop (it would publish on a closed bus → panic).
func TestAnswerer_StopWithPendingRing_NoPanic(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	cfg := config.AnswererConfig{Enabled: true, User: "bridge", AudioPort: 10100, VideoPort: 10102,
		MaxCallSeconds: 120, RingTimeoutSeconds: 1}
	a := NewAnswerer(cfg, "YOUR_GATEWAY_ID.bs.iotleg.com", f, bus, logrus.New())
	a.Start()
	f.in <- inviteMsg("cs", "60")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180")
	a.Stop()
	bus.Close()
	time.Sleep(1500 * time.Millisecond) // longer than RingTimeoutSeconds: the guard must NOT fire
}

func TestAnswerer_GuardTimeoutHangsUp(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	cfg := config.AnswererConfig{Enabled: true, User: "bridge", AudioPort: 10100, VideoPort: 10102, MaxCallSeconds: 1}
	a := NewAnswerer(cfg, "YOUR_GATEWAY_ID.bs.iotleg.com", f, bus, logrus.New())
	a.Start()
	defer a.Stop()
	f.in <- inviteMsg("cg", "30")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180")
	a.Answer()
	f.in <- msg("ACK", map[string]string{"Call-ID": "cg", "CSeq": "30 ACK"})
	waitFor(t, func() bool { return a.StateString() == "in_call" }, "in_call")
	// MaxCallSeconds=1 → after ~1s the guard must send BYE and return to idle.
	waitFor(t, func() bool { return f.lastSentContaining("BYE ") != "" && a.StateString() == "idle" }, "guard BYE")
}

// TestAnswerer_RegisterResponse_DoesNotBlockReadLoop reproduces a critical bug: dispatch()
// already holds a.mu, so the REGISTER 200 handler must set a.registered directly (NOT call a
// helper that does a.mu.Lock() again, because sync.Mutex is not reentrant and the read loop would
// block forever). The test first checks that IsRegistered() flips and THEN, with the same
// Answerer/goroutine, injects an INVITE and checks that 180 Ringing is still sent: if the read
// loop had hung on the earlier REGISTER, this second waitFor would time out.
func TestAnswerer_RegisterResponse_DoesNotBlockReadLoop(t *testing.T) {
	f := newFakeTransport()
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	a := newTestAnswerer(f, bus)
	a.Start()
	defer a.Stop()

	f.in <- &SIPMessage{StatusCode: "200", Headers: map[string]string{"CSeq": "1 REGISTER"}}
	waitFor(t, func() bool { return a.IsRegistered() }, "registered")

	// The read loop must stay alive: a later INVITE must be dispatched normally.
	f.in <- inviteMsg("creg", "40")
	waitFor(t, func() bool { return f.lastSentContaining("180 Ringing") != "" }, "180 Ringing after REGISTER 200")
}

// waitFor polls cond for up to 5s. The generous margin avoids timing flakes under load /
// -race instrumentation (the 1s guard and the INVITE→200→BYE round trip take longer with -race).
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting: %s", what)
}
