package sip

import (
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

// connectedSessionClient returns a running client whose camera session holds
// on a live pipe, like a real dialog, until it is stopped.
func connectedSessionClient(t *testing.T) (*BTicinoSIPClient, *int32, *int32) {
	t.Helper()
	c, _, stopped := newSessionTestClient()
	c.stopCh = make(chan struct{})
	near, far := net.Pipe()
	go io.Copy(io.Discard, far)
	c.webrtcID = &sipIdentity{username: "webrtc", conn: near}
	t.Cleanup(func() { near.Close(); far.Close() })

	var terminated int32
	c.resolveNativeFunc = func() (string, error) { return "sip:x", nil }
	c.establishDialogFunc = func(string, string) (*cameraDialog, error) {
		return &cameraDialog{callID: "c1", toTag: "t1"}, nil
	}
	c.terminateDialogFunc = func(*cameraDialog) { atomic.AddInt32(&terminated, 1) }
	return c, stopped, &terminated
}

func stopWithin(t *testing.T, limit time.Duration, stop func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(limit):
		t.Fatalf("Stop did not return within %v", limit)
	}
}

func TestStopEndsActiveCameraSession(t *testing.T) {
	c, stopped, terminated := connectedSessionClient(t)
	if err := c.StartCameraSession(time.Minute, "test"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	stopWithin(t, 5*time.Second, c.Stop)

	if !waitEnded(stopped, 1) {
		t.Fatal("no stopped event")
	}
	if got := atomic.LoadInt32(terminated); got != 1 {
		t.Fatalf("terminate calls = %d, want 1 (BYE before shutdown)", got)
	}
	if c.CameraSessionActive() {
		t.Fatal("camera session still active after Stop")
	}
}

func TestCameraSessionStoppedWhileDialingEndsAfterDial(t *testing.T) {
	c, stopped, terminated := connectedSessionClient(t)
	dialing := make(chan struct{})
	answer := make(chan struct{})
	c.establishDialogFunc = func(string, string) (*cameraDialog, error) {
		close(dialing)
		<-answer
		return &cameraDialog{callID: "c1", toTag: "t1"}, nil
	}

	if err := c.StartCameraSession(time.Minute, "test"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-dialing
	c.StopCameraSession()
	close(answer)

	if !waitEnded(stopped, 1) {
		t.Fatal("session held on after a stop during the dial")
	}
	if got := atomic.LoadInt32(terminated); got != 1 {
		t.Fatalf("terminate calls = %d, want 1", got)
	}
}

func TestStartCameraSessionRefusedAfterStop(t *testing.T) {
	c, _, _ := connectedSessionClient(t)
	stopWithin(t, 5*time.Second, c.Stop)

	c.mutex.Lock()
	c.callState = Registered
	c.mutex.Unlock()
	if err := c.StartCameraSession(time.Minute, "test"); err == nil {
		t.Fatal("StartCameraSession succeeded on a stopped client")
	}
}

// Shutdown closes the SIP sockets on purpose: their readers must not take
// that for a lost connection and start re-registering.
func TestStopDoesNotReconnect(t *testing.T) {
	c, _, _ := newSessionTestClient()
	c.stopCh = make(chan struct{})
	logger, hook := logrustest.NewNullLogger()
	c.logger = logger
	near, far := net.Pipe()
	go io.Copy(io.Discard, far)
	t.Cleanup(func() { far.Close() })
	c.localID = &sipIdentity{username: c.answerUser, conn: near}

	listening := make(chan struct{})
	go func() { close(listening); c.localListener() }()
	<-listening
	time.Sleep(50 * time.Millisecond)

	stopWithin(t, 5*time.Second, c.Stop)
	time.Sleep(100 * time.Millisecond)

	for _, entry := range hook.AllEntries() {
		if entry.Level <= logrus.WarnLevel || strings.Contains(entry.Message, "reconnect") {
			t.Errorf("unexpected log on shutdown: %s %q", entry.Level, entry.Message)
		}
	}
	c.localID.mutex.Lock()
	reconnecting := c.localID.reconnecting
	c.localID.mutex.Unlock()
	if reconnecting {
		t.Fatal("reconnect loop started by Stop")
	}
}
