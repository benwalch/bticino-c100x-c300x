package sip

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/events"
)

func sipRegisterOK(callID, cseq string) string {
	return "SIP/2.0 200 Registration successful\r\nVia: SIP/2.0/UDP 127.0.0.1:5090;branch=z9hG4bKr\r\n" +
		"From: <sip:x@example.com>;tag=a\r\nTo: <sip:x@example.com>;tag=b\r\n" +
		"Call-ID: " + callID + "\r\nCSeq: " + cseq + "\r\nContent-Length: 0\r\n\r\n"
}

// webrtc: while the panel view watcher owns the socket, the re-REGISTER does NOT
// read (before, it stole/lost responses: "Failed to refresh webrtc registration").
// It returns errSocketBusy at once and is retried; with the socket free it works,
// also answering a stale BYE that arrives before the REGISTER's 200.
func TestReRegisterWebrtcDoesNotCompeteWithWatcher(t *testing.T) {
	c, peer, _ := newWatchedClient(t)
	connectEye(t, c, peer)

	start := time.Now()
	err := c.reRegisterIdentity(c.webrtcID)
	if !errors.Is(err, errSocketBusy) {
		t.Fatalf("with the watcher active expected errSocketBusy, got %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("the re-REGISTER must not block waiting for the socket")
	}
	if err := c.Hangup(); err != nil {
		t.Fatal(err)
	}
	peer.read(t) // BYE

	done := make(chan error, 1)
	go func() { done <- c.reRegisterIdentity(c.webrtcID) }()
	reg := peer.read(t)
	if !strings.HasPrefix(reg, "REGISTER ") {
		t.Fatalf("expected REGISTER:\n%s", reg)
	}
	peer.send(sipBye("viejo@127.0.0.1"))
	if r := peer.read(t); !strings.HasPrefix(r, "SIP/2.0 200") {
		t.Fatalf("expected 200 to the stale BYE:\n%s", r)
	}
	peer.send(sipRegisterOK(sipHeader(reg, "Call-ID"), sipHeader(reg, "CSeq")))
	if err := <-done; err != nil {
		t.Fatalf("re-REGISTER with a free socket: %v", err)
	}
}

// c300x: the listener is the ONLY reader of its socket; the re-REGISTER gets its
// response through it (before, the listener swallowed it: "Failed to refresh
// c300x registration ... i/o timeout" every ~4-5 min).
func TestReRegisterC300xGetsResponseViaListener(t *testing.T) {
	cfg := &SIPConfig{ServerAddr: "127.0.0.1:5060", Domain: "example.com", Username: "webrtc",
		LocalIP: "127.0.0.1", LocalPort: 5090, DevAddr: "20", Transport: "udp"}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	c := NewBTicinoSIPClient(cfg, events.NewEventBus(10, logger), logger)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	local, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	peer := &fakeUDPPeer{pc: pc, buf: make([]byte, 16384)}
	c.localID = &sipIdentity{username: "c300x", fromTag: "c3", callID: "reg-c300x@127.0.0.1", cseq: 1, conn: local}
	c.startLocalListener()
	defer close(c.stopCh)

	done := make(chan error, 1)
	go func() { done <- c.reRegisterIdentity(c.localID) }()
	reg := peer.read(t)
	if !strings.HasPrefix(reg, "REGISTER ") {
		t.Fatalf("expected REGISTER:\n%s", reg)
	}
	peer.send(sipRegisterOK(sipHeader(reg, "Call-ID"), sipHeader(reg, "CSeq")))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("re-REGISTER c300x: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the c300x re-REGISTER did not get its 200 (the listener swallowed it)")
	}
}

// The watcher receives the reader HANDED OVER (it never waits for it), so stopping
// it cannot block; before, a watcher waiting for a reader held by someone else +
// that someone asking to stop it = a permanent deadlock.
func TestStopDialogWatchDoesNotBlockIfReaderHeldElsewhere(t *testing.T) {
	c, _, _ := newWatchedClient(t)
	c.webrtcID.readMu.Lock()          // the INVITE loop owns the reader…
	c.startDialogWatch("x@127.0.0.1") // …and hands it over to the watcher
	done := make(chan struct{})
	go func() { c.stopDialogWatch(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stopDialogWatch blocks")
	}
	if !c.webrtcID.readMu.TryLock() {
		t.Fatal("when stopping, the watcher must release the handed-over reader")
	}
	c.webrtcID.readMu.Unlock()
}

// A late REGISTER response of an earlier refresh (another CSeq number) left in the
// channel must NOT be taken as the response of the current refresh.
func TestReRegisterC300xIgnoresStaleResponse(t *testing.T) {
	cfg := &SIPConfig{ServerAddr: "127.0.0.1:5060", Domain: "example.com", Username: "webrtc",
		LocalIP: "127.0.0.1", LocalPort: 5090, DevAddr: "20", Transport: "udp"}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	c := NewBTicinoSIPClient(cfg, events.NewEventBus(10, logger), logger)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	local, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	peer := &fakeUDPPeer{pc: pc, buf: make([]byte, 16384)}
	c.localID = &sipIdentity{username: "c300x", fromTag: "c3", callID: "reg-c300x@127.0.0.1", cseq: 7, conn: local}
	c.startLocalListener()
	defer close(c.stopCh)
	// stale 401 of an earlier refresh, already in the channel.
	c.localID.respCh <- mustParseSIP(t, "SIP/2.0 401 Unauthorized\r\nCall-ID: reg-c300x@127.0.0.1\r\nCSeq: 3 REGISTER\r\nContent-Length: 0\r\n\r\n")

	done := make(chan error, 1)
	go func() { done <- c.reRegisterIdentity(c.localID) }()
	reg := peer.read(t)
	peer.send(sipRegisterOK(sipHeader(reg, "Call-ID"), sipHeader(reg, "CSeq")))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("took the stale response: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("re-REGISTER without a response")
	}
}
