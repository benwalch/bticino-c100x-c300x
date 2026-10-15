package sip

import (
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bticino_bridge/pkg/bticino"
	"bticino_bridge/pkg/events"

	"github.com/sirupsen/logrus"
)

type timeoutError string

func (e timeoutError) Error() string   { return string(e) }
func (e timeoutError) Timeout() bool   { return true }
func (e timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError("")

func newSessionTestClient() (*BTicinoSIPClient, *int32, *int32) {
	bus := events.NewEventBus(1, logrus.New())
	var started, stopped int32
	bus.Subscribe("video.session.started", func(_ *events.Event) {
		atomic.AddInt32(&started, 1)
	})
	bus.Subscribe("video.session.stopped", func(_ *events.Event) {
		atomic.AddInt32(&stopped, 1)
	})
	c := &BTicinoSIPClient{
		config:     &SIPConfig{},
		callState:  Registered,
		eventBus:   bus,
		logger:     logrus.New(),
		answerUser: bticino.SIPUsernameForVariant(bticino.DeviceVariant),
	}
	return c, &started, &stopped
}

// Full happy path with stubbed network: started, terminate on timer expiry,
// stopped, state back to Registered.
func TestCameraSessionHappyPath(t *testing.T) {
	c, started, stopped := newSessionTestClient()
	var terminated int32
	c.resolveNativeFunc = func() (string, error) {
		return "sip:" + bticino.SIPUsernameForVariant(bticino.DeviceVariant) + "@127.0.0.1:54688;transport=tcp", nil
	}
	c.establishDialogFunc = func(uri, sdp string) (*cameraDialog, error) {
		if !strings.HasPrefix(uri, "sip:"+bticino.SIPUsernameForVariant(bticino.DeviceVariant)+"@127.0.0.1:") {
			t.Errorf("uri = %q, want direct native contact", uri)
		}
		return &cameraDialog{callID: "c1", toTag: "t1", contact: uri, cseq: 1}, nil
	}
	c.terminateDialogFunc = func(*cameraDialog) { atomic.AddInt32(&terminated, 1) }

	if err := c.StartCameraSession(60*time.Millisecond, "test"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitEnded(started, 1) {
		t.Fatal("no started event")
	}
	if !waitEnded(stopped, 1) {
		t.Fatal("no stopped event")
	}
	if got := atomic.LoadInt32(&terminated); got != 1 {
		t.Fatalf("terminate calls = %d, want 1", got)
	}
	if c.GetCallState() != Registered {
		t.Fatalf("state = %s, want Registered", c.GetCallState())
	}
}

// A second start while active is rejected; a failed dial terminates with a
// single stopped event and never calls terminate.
func TestCameraSessionSingleFlightAndDialError(t *testing.T) {
	c, started, stopped := newSessionTestClient()
	unblock := make(chan struct{})
	var terminated int32
	c.resolveNativeFunc = func() (string, error) { return "sip:x", nil }
	c.establishDialogFunc = func(string, string) (*cameraDialog, error) {
		<-unblock
		return nil, errors.New("no route")
	}
	c.terminateDialogFunc = func(*cameraDialog) { atomic.AddInt32(&terminated, 1) }

	if err := c.StartCameraSession(time.Minute, "test"); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := c.StartCameraSession(time.Minute, "test"); err == nil {
		t.Fatal("second Start while active must fail")
	}
	close(unblock)
	if !waitEnded(stopped, 1) {
		t.Fatal("no stopped event after dial error")
	}
	if got := atomic.LoadInt32(started); got != 0 {
		t.Fatalf("started events = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&terminated); got != 0 {
		t.Fatalf("terminate calls = %d, want 0 (never connected)", got)
	}
}

// Stop on an idle client is silent.
func TestCameraSessionStopIdleSilent(t *testing.T) {
	c, _, stopped := newSessionTestClient()
	c.StopCameraSession()
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(stopped); got != 0 {
		t.Fatalf("stopped events = %d, want 0", got)
	}
}

// The offer must carry two fresh valid crypto keys per build.
func TestBuildCameraSDPKeys(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		sdp, err := buildCameraSDP("test-devaddr")
		if err != nil {
			t.Fatalf("buildCameraSDP: %v", err)
		}
		if strings.Contains(sdp, "dummykey") || strings.Contains(sdp, "recvonly") {
			t.Fatalf("offer must not contain dummykey/recvonly:\n%s", sdp)
		}
		for _, line := range strings.Split(sdp, "\r\n") {
			if !strings.HasPrefix(line, "a=crypto:") {
				continue
			}
			key := line[strings.LastIndex(line, ":")+1:]
			raw, err := base64.StdEncoding.DecodeString(key)
			if err != nil || len(raw) != 30 {
				t.Fatalf("crypto key invalid: %q", key)
			}
			if seen[key] {
				t.Fatalf("crypto key reused: %q", key)
			}
			seen[key] = true
		}
	}
	if len(seen) != 6 {
		t.Fatalf("want 6 distinct keys across 3 builds, got %d", len(seen))
	}
}

// Resolver: exactly one stranger wins; zero or many abort.
func TestParseProcNetTCP(t *testing.T) {
	fixture := "  sl  local_address rem_address   st\n" +
		"   4: 0100007F:13C4 00000000:0000 0A\n" +
		"  30: 0100007F:13C4 0100007F:D5A0 01\n" +
		"  21: 0100007F:9900 0100007F:13C4 01\n" +
		"  33: 0100007F:9902 0100007F:13C4 01\n" +
		"  99: 0100007F:9C42 0100007F:13C4 01\n"
	uri, err := parseProcNetTCP(fixture, map[int]bool{0x9900: true, 0x9902: true, 0x9C42: true}, bticino.SIPUsernameForVariant(bticino.DeviceVariant))
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if uri != "sip:"+bticino.SIPUsernameForVariant(bticino.DeviceVariant)+"@127.0.0.1:54688;transport=tcp" {
		t.Fatalf("uri = %q", uri)
	}
	if _, err := parseProcNetTCP(fixture, map[int]bool{}, bticino.SIPUsernameForVariant(bticino.DeviceVariant)); err == nil {
		t.Fatal("ambiguous contacts must error")
	}
	if _, err := parseProcNetTCP("  sl  local_address rem_address   st\n", map[int]bool{1: true}, bticino.SIPUsernameForVariant(bticino.DeviceVariant)); err == nil {
		t.Fatal("no contacts must error")
	}
}

// Timeouts (quiet hold, both typed and framer plain errors) never count
// toward the abort; anything else does.
func TestIsBenignReadError(t *testing.T) {
	if !isBenignReadError(timeoutError("i/o timeout")) {
		t.Fatal("net timeout must be benign")
	}
	if !isBenignReadError(errors.New("timeout waiting for complete SIP message")) {
		t.Fatal("framer timeout must be benign")
	}
	if isBenignReadError(errors.New("connection reset by peer")) {
		t.Fatal("reset must not be benign")
	}
	if isBenignReadError(errors.New("EOF")) {
		t.Fatal("EOF must not be benign")
	}
}

// Empty config falls back to the validated address; a set value wins.
func TestSessionNativeDevAddrFallback(t *testing.T) {
	c := &BTicinoSIPClient{config: &SIPConfig{}}
	if got := c.sessionNativeDevAddr(); got != defaultNativeDevAddr {
		t.Fatalf("fallback = %q", got)
	}
	c.config.NativeDevAddr = "custom-devaddr"
	if got := c.sessionNativeDevAddr(); got != "custom-devaddr" {
		t.Fatalf("override = %q", got)
	}
}

// A 486 after provisionals means busy; a bare one means the offer itself
// was rejected.
func TestClassifyReject(t *testing.T) {
	if err := classifyReject("486", true); !errors.Is(err, ErrNativeBusy) {
		t.Fatalf("provisional 486 must be busy, got %v", err)
	}
	if err := classifyReject("486", false); errors.Is(err, ErrNativeBusy) {
		t.Fatalf("bare 486 must not be busy, got %v", err)
	}
	if err := classifyReject("606", false); errors.Is(err, ErrNativeBusy) {
		t.Fatalf("bare 606 must not be busy, got %v", err)
	}
}
