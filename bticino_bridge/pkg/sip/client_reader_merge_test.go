package sip

import (
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func readerTestClient(expiry int) *BTicinoSIPClient {
	return &BTicinoSIPClient{
		config: &SIPConfig{ExpirySeconds: expiry},
		logger: logrus.New(),
	}
}

// A renewal before half of the expiry must not touch the socket.
func TestRefreshRegistrationWaitsForHalfExpiry(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	c := readerTestClient(3600)
	id := &sipIdentity{username: "webrtc", conn: local, lastRegistered: time.Now()}

	c.refreshRegistration(id)

	remote.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, err := remote.Read(make([]byte, 16)); err == nil {
		t.Fatalf("unexpected write of %d bytes before half expiry", n)
	}
}

// While a camera session owns the reader the renewal is deferred: no REGISTER
// goes out and the connection is not treated as lost.
func TestRefreshRegistrationDefersWhileSocketIsOwned(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	c := readerTestClient(60)
	id := &sipIdentity{username: "webrtc", conn: local}
	id.readMu.Lock()
	defer id.readMu.Unlock()

	c.refreshRegistration(id)

	if id.reconnecting {
		t.Fatal("deferred renewal started a reconnect")
	}
	remote.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, err := remote.Read(make([]byte, 16)); err == nil {
		t.Fatalf("unexpected write of %d bytes while the socket is owned", n)
	}
}

// No renewal while a reconnect is registering on the same identity.
func TestRefreshRegistrationSkipsWhileReconnecting(t *testing.T) {
	local, remote := net.Pipe()
	defer local.Close()
	defer remote.Close()
	c := readerTestClient(60)
	id := &sipIdentity{username: "c100x", conn: local, reconnecting: true}

	c.refreshRegistration(id)

	remote.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if n, err := remote.Read(make([]byte, 16)); err == nil {
		t.Fatalf("unexpected write of %d bytes during a reconnect", n)
	}
}

func mustParseSIP(t *testing.T, raw string) *SIPMessage {
	t.Helper()
	msg, _, ok := parseSIPHeaderBlock([]byte(raw))
	if !ok {
		t.Fatalf("cannot parse SIP message: %q", raw)
	}
	return msg
}

// The session is the only reader of the webrtc socket from the INVITE until it ends.
func TestCameraSessionOwnsWebrtcReader(t *testing.T) {
	c, _, stopped := newSessionTestClient()
	ownedDuringDial := make(chan bool, 1)
	c.resolveNativeFunc = func() (string, error) { return "sip:native@127.0.0.1:1;transport=tcp", nil }
	c.establishDialogFunc = func(uri, sdp string) (*cameraDialog, error) {
		free := c.webrtcID.readMu.TryLock()
		if free {
			c.webrtcID.readMu.Unlock()
		}
		ownedDuringDial <- !free
		return &cameraDialog{callID: "c1", toTag: "t1", contact: uri, cseq: 1}, nil
	}
	c.terminateDialogFunc = func(*cameraDialog) {}

	if err := c.StartCameraSession(20*time.Millisecond, "test"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !<-ownedDuringDial {
		t.Fatal("reader not owned while the dialog is established")
	}
	if !waitEnded(stopped, 1) {
		t.Fatal("no stopped event")
	}
	if !c.webrtcID.readMu.TryLock() {
		t.Fatal("reader still owned after the session ended")
	}
	c.webrtcID.readMu.Unlock()
}
