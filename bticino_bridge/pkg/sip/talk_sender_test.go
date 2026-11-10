package sip

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/events"
)

// fakeProc and fakeRunner record the launch and control Kill/Wait.
type fakeProc struct {
	mu     sync.Mutex
	killed bool
	waitCh chan struct{}
}

func newFakeProc() *fakeProc { return &fakeProc{waitCh: make(chan struct{})} }
func (p *fakeProc) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.killed {
		p.killed = true
		close(p.waitCh)
	}
	return nil
}
func (p *fakeProc) Wait() error { <-p.waitCh; return nil }
func (p *fakeProc) isKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

type fakeRunner struct {
	mu       sync.Mutex
	name     string
	args     []string
	procs    []*fakeProc
	starts   int
	failNext bool
}

func (r *fakeRunner) Start(name string, args []string) (Proc, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	if r.failNext {
		r.failNext = false
		return nil, errTransportClosed // any error
	}
	r.name = name
	r.args = append([]string{}, args...)
	p := newFakeProc()
	r.procs = append(r.procs, p)
	return p, nil
}
func (r *fakeRunner) lastArgs() []string { r.mu.Lock(); defer r.mu.Unlock(); return r.args }
func (r *fakeRunner) startCount() int    { r.mu.Lock(); defer r.mu.Unlock(); return r.starts }
func (r *fakeRunner) setFailNext(v bool) { r.mu.Lock(); defer r.mu.Unlock(); r.failNext = v }

func newTalkSenderTest(runner ProcRunner, bus events.EventBus, enabled bool) *TalkSender {
	cfg := config.AnswererConfig{Enabled: true, TalkEnabled: enabled, AudioPort: 10100}
	return NewTalkSender(cfg, runner, bus, logrus.New(), "ffmpeg", "127.0.0.1", 40004, 97)
}

func answeredEvent(bus events.EventBus, port int, key string) {
	bus.Publish("doorcall.answered", map[string]interface{}{
		"call_id": "c1", "media_host": "127.0.0.1", "media_port": port, "srtp_key": key,
	})
}

func TestTalkSender_StartsFFmpegOnAnswer(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{}
	s := newTalkSenderTest(r, bus, true)
	s.Start()
	defer s.Stop()
	answeredEvent(bus, 7078, "KEYb64==")
	waitFor2(t, func() bool { return r.startCount() == 1 }, "ffmpeg started")
	args := strings.Join(r.lastArgs(), " ")
	for _, want := range []string{
		"-payload_type 96",
		"-srtp_out_suite AES_CM_128_HMAC_SHA1_80",
		"-srtp_out_params KEYb64==",
		"srtp://127.0.0.1:7078",
		"-i", // followed by the temporary SDP path
	} {
		if !strings.Contains(args, want) {
			t.Errorf("ffmpeg args do not contain %q\n%s", want, args)
		}
	}
}

func TestTalkSender_KillsFFmpegOnHangup(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{}
	s := newTalkSenderTest(r, bus, true)
	s.Start()
	defer s.Stop()
	answeredEvent(bus, 7078, "K==")
	waitFor2(t, func() bool { return r.startCount() == 1 }, "started")
	bus.Publish("doorcall.ended", map[string]interface{}{"reason": "hangup_local"})
	waitFor2(t, func() bool { return len(r.procs) == 1 && r.procs[0].isKilled() }, "killed on hangup")
}

// TestTalkSender_EndedBeforeAnswered_SameCallID_DoesNotStart reproduces the EventBus
// reordering race: if a call's doorcall.ended arrives BEFORE its own doorcall.answered
// (e.g. the visitor hangs up right after the answer), no orphaned ffmpeg may be
// started that no later ended would kill.
func TestTalkSender_EndedBeforeAnswered_SameCallID_DoesNotStart(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{}
	s := newTalkSenderTest(r, bus, true)
	s.Start()
	defer s.Stop()

	bus.Publish("doorcall.ended", map[string]interface{}{"reason": "hangup_remote", "call_id": "c1"})
	// Slack so onEnded is handled (records lastEndedCallID) BEFORE the answered.
	time.Sleep(50 * time.Millisecond)
	bus.Publish("doorcall.answered", map[string]interface{}{
		"call_id": "c1", "media_host": "127.0.0.1", "media_port": 7078, "srtp_key": "K==",
	})
	time.Sleep(80 * time.Millisecond)
	if r.startCount() != 0 {
		t.Fatal("ended before answered of the SAME call_id: must NOT start ffmpeg (orphan)")
	}
}

// TestTalkSender_EndedOtherCallID_Starts confirms that the call_id correlation does not
// wrongly block a NEW call (another call_id) that arrives after the ended of an earlier one.
func TestTalkSender_EndedOtherCallID_Starts(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{}
	s := newTalkSenderTest(r, bus, true)
	s.Start()
	defer s.Stop()

	bus.Publish("doorcall.ended", map[string]interface{}{"reason": "hangup_remote", "call_id": "c1"})
	time.Sleep(50 * time.Millisecond)
	bus.Publish("doorcall.answered", map[string]interface{}{
		"call_id": "c2", "media_host": "127.0.0.1", "media_port": 7078, "srtp_key": "K==",
	})
	waitFor2(t, func() bool { return r.startCount() == 1 }, "a new call (other call_id) must start ffmpeg")
}

func TestTalkSender_DisabledDoesNotStart(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{}
	s := newTalkSenderTest(r, bus, false /* talk_enabled=false */)
	s.Start()
	defer s.Stop()
	answeredEvent(bus, 7078, "K==")
	time.Sleep(80 * time.Millisecond)
	if r.startCount() != 0 {
		t.Fatal("with talk_enabled=false ffmpeg must NOT start")
	}
}

func TestTalkSender_InvalidPortDoesNotStart(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{}
	s := newTalkSenderTest(r, bus, true)
	s.Start()
	defer s.Stop()
	answeredEvent(bus, 0, "K==") // media_port=0 (offer without parsable audio)
	time.Sleep(80 * time.Millisecond)
	if r.startCount() != 0 {
		t.Fatal("with media_port=0 ffmpeg must NOT start")
	}
}

func TestTalkSender_LaunchFailureDoesNotCrash(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{failNext: true}
	s := newTalkSenderTest(r, bus, true)
	s.Start()
	defer s.Stop()
	answeredEvent(bus, 7078, "K==") // the runner fails: must not panic or block
	time.Sleep(80 * time.Millisecond)
	// Still alive and accepting another answered afterwards.
	r.setFailNext(false)
	answeredEvent(bus, 7078, "K==")
	waitFor2(t, func() bool { return r.startCount() == 2 }, "retry after failure")
}

// freeUDPPort returns a free UDP port on loopback (so it does not clash with the real 40004).
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	p := c.LocalAddr().(*net.UDPAddr).Port
	c.Close()
	return p
}

// sdpFromArgs reads the temporary SDP passed to ffmpeg after "-i".
func sdpFromArgs(t *testing.T, args []string) string {
	t.Helper()
	for i, a := range args {
		if a == "-i" && i+1 < len(args) {
			b, err := os.ReadFile(args[i+1])
			if err != nil {
				t.Fatalf("could not read the SDP %s: %v", args[i+1], err)
			}
			return string(b)
		}
	}
	t.Fatal("args without -i")
	return ""
}

func newTalkSenderKeepalive(runner ProcRunner, bus events.EventBus, keepalive bool, bcPort int) *TalkSender {
	cfg := config.AnswererConfig{Enabled: true, TalkEnabled: true, TalkKeepalive: keepalive, AudioPort: 10100}
	return NewTalkSender(cfg, runner, bus, logrus.New(), "ffmpeg", "127.0.0.1", bcPort, 97)
}

// Without talk_keepalive: ffmpeg reads go2rtc's backchannel directly (current behaviour).
func TestTalkSender_NoKeepalive_SDPPointsToBackchannel(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{}
	bc := freeUDPPort(t)
	s := newTalkSenderKeepalive(r, bus, false, bc)
	s.Start()
	defer s.Stop()
	answeredEvent(bus, 7078, "K==")
	waitFor2(t, func() bool { return r.startCount() == 1 }, "ffmpeg started")
	if sdp := sdpFromArgs(t, r.lastArgs()); !strings.Contains(sdp, fmt.Sprintf("m=audio %d ", bc)) {
		t.Fatalf("without keepalive the SDP must point to the backchannel %d:\n%s", bc, sdp)
	}
}

// With talk_keepalive: the relay listens on the backchannel and ffmpeg reads the relay's output (bc+2).
func TestTalkSender_Keepalive_SDPPointsToRelay(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{}
	bc := freeUDPPort(t)
	s := newTalkSenderKeepalive(r, bus, true, bc)
	s.Start()
	defer s.Stop()
	answeredEvent(bus, 7078, "K==")
	waitFor2(t, func() bool { return r.startCount() == 1 }, "ffmpeg started")
	if sdp := sdpFromArgs(t, r.lastArgs()); !strings.Contains(sdp, fmt.Sprintf("m=audio %d ", bc+2)) {
		t.Fatalf("with keepalive the SDP must point to the relay's output %d:\n%s", bc+2, sdp)
	}
	// The relay holds the backchannel port for the duration of the call.
	if c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: bc}); err == nil {
		c.Close()
		t.Fatalf("the relay should be listening on %d", bc)
	}
}

// On hangup the relay stops and the backchannel port is freed.
func TestTalkSender_Keepalive_HangupStopsRelay(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{}
	bc := freeUDPPort(t)
	s := newTalkSenderKeepalive(r, bus, true, bc)
	s.Start()
	defer s.Stop()
	answeredEvent(bus, 7078, "K==")
	waitFor2(t, func() bool { return r.startCount() == 1 }, "ffmpeg started")
	bus.Publish("doorcall.ended", map[string]interface{}{"call_id": "c1", "reason": "hangup_local"})
	waitFor2(t, func() bool {
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: bc})
		if err != nil {
			return false
		}
		c.Close()
		return true
	}, "backchannel port freed on hangup")
}

// waitFor2 polls for up to 3s (slack under -race).
func waitFor2(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting: %s", what)
}

// TestTalkSender_ArmsOnPanelViewConnected: in panel view mode (Show door panel) talking
// arms on panelview.connected and disarms on panelview.ended (same payload as doorcall.*).
func TestTalkSender_ArmsOnPanelViewConnected(t *testing.T) {
	bus := events.NewEventBus(2, logrus.New())
	defer bus.Close()
	r := &fakeRunner{}
	s := newTalkSenderTest(r, bus, true)
	s.Start()
	defer s.Stop()
	bus.Publish("panelview.connected", map[string]interface{}{
		"call_id": "eye1", "media_host": "127.0.0.1", "media_port": 5000, "srtp_key": "QUJDRA==",
	})
	waitFor2(t, func() bool { return r.startCount() == 1 }, "ffmpeg started on panelview.connected")
	bus.Publish("panelview.ended", map[string]interface{}{"call_id": "eye1"})
	waitFor2(t, func() bool { return len(r.procs) == 1 && r.procs[0].isKilled() }, "killed on panelview.ended")
}

// TestTalkSender_PanelViewMediaPT: the negotiated PT (media_pt) is passed to -payload_type;
// without media_pt (doorcall) it stays 96.
func TestTalkSender_PanelViewMediaPT(t *testing.T) {
	for _, tc := range []struct {
		name string
		data map[string]interface{}
		want string
	}{
		{"ojo98", map[string]interface{}{"call_id": "e", "media_port": 5000, "srtp_key": "QUJDRA==", "media_pt": 98}, "-payload_type 98"},
		{"doorcall96", map[string]interface{}{"call_id": "d", "media_port": 5000, "srtp_key": "QUJDRA=="}, "-payload_type 96"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := events.NewEventBus(2, logrus.New())
			defer bus.Close()
			r := &fakeRunner{}
			s := newTalkSenderTest(r, bus, true)
			s.Start()
			defer s.Stop()
			topic := "panelview.connected"
			if tc.name == "doorcall96" {
				topic = "doorcall.answered"
			}
			bus.Publish(topic, tc.data)
			waitFor2(t, func() bool { return r.startCount() == 1 }, "ffmpeg started")
			if args := strings.Join(r.lastArgs(), " "); !strings.Contains(args, tc.want) {
				t.Errorf("args without %q: %s", tc.want, args)
			}
		})
	}
}
