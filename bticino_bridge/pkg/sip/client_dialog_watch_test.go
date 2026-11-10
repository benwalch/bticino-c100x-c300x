package sip

import (
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/events"
)

// fakeUDPPeer simulates Flexisip/bt_vct on the other side of the webrtc socket (UDP loopback).
type fakeUDPPeer struct {
	pc   net.PacketConn
	from net.Addr
	buf  []byte
}

func (p *fakeUDPPeer) read(t *testing.T) string {
	t.Helper()
	_ = p.pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, from, err := p.pc.ReadFrom(p.buf)
	if err != nil {
		t.Fatalf("peer: no message arrived: %v", err)
	}
	p.from = from
	return string(p.buf[:n])
}

func (p *fakeUDPPeer) send(m string) { _, _ = p.pc.WriteTo([]byte(m), p.from) }

func newWatchedClient(t *testing.T) (*BTicinoSIPClient, *fakeUDPPeer, events.EventBus) {
	t.Helper()
	cfg := &SIPConfig{ServerAddr: "127.0.0.1:5060", Domain: "example.com", Username: "webrtc",
		LocalIP: "127.0.0.1", LocalPort: 5090, DevAddr: "20", Transport: "udp"}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	bus := events.NewEventBus(10, logger)
	c := NewBTicinoSIPClient(cfg, bus, logger)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	local, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.stopDialogWatch(); local.Close(); pc.Close() })
	c.webrtcID = &sipIdentity{username: cfg.Username, fromTag: "fromtag123", cseq: 1, conn: local}
	c.callState = Registered
	return c, &fakeUDPPeer{pc: pc, buf: make([]byte, 16384)}, bus
}

// connectEye completes an INVITE→200→ACK and returns the dialog's Call-ID.
func connectEye(t *testing.T, c *BTicinoSIPClient, peer *fakeUDPPeer) string {
	t.Helper()
	done := make(chan error, 1)
	go func() { _, err := c.InviteWithSDP("c300x", "v=0\r\n"); done <- err }()
	cid := sipHeader(peer.read(t), "Call-ID")
	peer.send(sipResponse("200 OK", cid, "1 INVITE", "v=0\r\nm=audio 9078 RTP/SAVP 98\r\n"))
	if ack := peer.read(t); !strings.HasPrefix(ack, "ACK ") {
		t.Fatalf("expected ACK:\n%s", ack)
	}
	if err := <-done; err != nil {
		t.Fatalf("InviteWithSDP: %v", err)
	}
	return cid
}

// The native side hangs up the panel view: the watcher answers 200 to the BYE and
// publishes sip.dialog.ended with the Call-ID, without the bridge sending its own BYE.
func TestDialogWatchRemoteBYE(t *testing.T) {
	c, peer, bus := newWatchedClient(t)
	ended := subscribeOnce(bus, "sip.dialog.ended")
	cid := connectEye(t, c, peer)

	peer.send(sipBye(cid))
	r := peer.read(t)
	if !strings.HasPrefix(r, "SIP/2.0 200") || sipHeader(r, "Call-ID") != cid {
		t.Fatalf("unexpected response to the remote BYE:\n%s", r)
	}
	select {
	case e := <-ended:
		d := e.Data.(map[string]interface{})
		if d["call_id"] != cid {
			t.Fatalf("call_id=%v, expected %s", d["call_id"], cid)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("sip.dialog.ended was not published")
	}
	if c.GetCallState() == Connected {
		t.Fatal("after the remote BYE the session must not stay Connected")
	}
	if err := c.Hangup(); err != nil { // already closed: no-op, no own BYE
		t.Fatalf("Hangup: %v", err)
	}
	_ = peer.pc.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, _, err := peer.pc.ReadFrom(peer.buf); err == nil {
		t.Fatalf("no own BYE should be sent after a remote BYE, got:\n%s", string(peer.buf[:n]))
	}
}

// Never two readers on the socket: after Hangup (which stops the watcher) a new
// panel view reads its 200 without the previous watcher stealing it.
func TestDialogWatchStopsBeforeNextInvite(t *testing.T) {
	c, peer, _ := newWatchedClient(t)
	connectEye(t, c, peer)
	if err := c.Hangup(); err != nil {
		t.Fatalf("Hangup: %v", err)
	}
	if bye := peer.read(t); !strings.HasPrefix(bye, "BYE ") {
		t.Fatalf("expected BYE:\n%s", bye)
	}
	for i := 0; i < 3; i++ { // several panel views in a row
		connectEye(t, c, peer)
		if err := c.Hangup(); err != nil {
			t.Fatalf("Hangup %d: %v", i, err)
		}
		peer.read(t) // BYE
	}
}

// sipOKWithRoute is bt_vct's 200 as it arrives through Flexisip (real log of
// 2026-10-16): the native Contact + the proxy's Record-Route.
func sipOKWithRoute(callID, body string) string {
	return "SIP/2.0 200 Ok\r\nVia: SIP/2.0/UDP 127.0.0.1:5090;branch=z9hG4bKx\r\n" +
		"Record-Route: <sip:127.0.0.1:5060;transport=tcp;lr>\r\n" +
		"Record-Route: <sip:127.0.0.1:5060;lr>\r\n" +
		"From: <sip:webrtc@example.com>;tag=fromtag123\r\nTo: <sip:c300x@example.com>;tag=NAhcCga\r\n" +
		"Call-ID: " + callID + "\r\nCSeq: 1 INVITE\r\n" +
		"Contact: <sip:c300x@127.0.0.1:57102;transport=tcp>;+sip.instance=\"<urn:uuid:x>\"\r\n" +
		"Content-Type: application/sdp\r\n" +
		"Content-Length: " + itoa(len(body)) + "\r\n\r\n" + body
}

func itoa(n int) string { return strconv.Itoa(n) }

// The BYE (and the ACK) must go to the Contact of the 200 with Route = reversed
// Record-Route (RFC 3261 §12.1.2). Before, they went to sip:c300x@domain without a
// Route: Flexisip did not deliver them to bt_vct, which stayed in the call ~30 s and
// answered 486 to the next panel view.
func TestByeAndAckGoToContactWithRoute(t *testing.T) {
	c, peer, _ := newWatchedClient(t)
	done := make(chan error, 1)
	go func() { _, err := c.InviteWithSDP("c300x", "v=0\r\n"); done <- err }()
	cid := sipHeader(peer.read(t), "Call-ID")
	peer.send(sipOKWithRoute(cid, "v=0\r\nm=audio 9078 RTP/SAVP 98\r\n"))
	ack := peer.read(t)
	if err := <-done; err != nil {
		t.Fatalf("InviteWithSDP: %v", err)
	}
	const target = "sip:c300x@127.0.0.1:57102;transport=tcp"
	wantRoutes := "Route: <sip:127.0.0.1:5060;lr>\r\nRoute: <sip:127.0.0.1:5060;transport=tcp;lr>\r\n"
	for name, m := range map[string]string{"ACK": ack} {
		if !strings.HasPrefix(m, name+" "+target+" SIP/2.0") {
			t.Fatalf("%s with the wrong Request-URI:\n%s", name, m)
		}
		if !strings.Contains(m, wantRoutes) {
			t.Fatalf("%s without Route (reversed Record-Route):\n%s", name, m)
		}
	}
	if err := c.Hangup(); err != nil {
		t.Fatalf("Hangup: %v", err)
	}
	bye := peer.read(t)
	if !strings.HasPrefix(bye, "BYE "+target+" SIP/2.0") {
		t.Fatalf("BYE with the wrong Request-URI:\n%s", bye)
	}
	if !strings.Contains(bye, wantRoutes) {
		t.Fatalf("BYE without Route:\n%s", bye)
	}
	if sipHeader(bye, "Call-ID") != cid || !strings.Contains(sipHeader(bye, "To"), "tag=NAhcCga") {
		t.Fatalf("BYE outside the dialog (Call-ID/To-tag):\n%s", bye)
	}
}
