package sip

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/events"
)

// The Flexisip fork branch that reaches OUR c300x listener with an INVITE we
// placed ourselves (the panel view) must get a FINAL response. If it is ignored,
// Flexisip waits for that branch and does not forward bt_vct's final response
// (e.g. its 486): the panel view timed out after 30 s (live gate 2026-10-16).
func TestListenerSendsFinalToOwnInvite(t *testing.T) {
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
	c.localID = &sipIdentity{username: "c300x", fromTag: "c3tag", cseq: 1, conn: local}
	c.callState = Registered
	const cid = "own-eye@127.0.0.1"
	c.originatedCalls[cid] = time.Now()
	c.nativeCalls[cid] = time.Now() // panel view INVITE (InviteWithSDP)

	raw := "INVITE sip:c300x@127.0.0.1:5090 SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKproxy\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:41796;branch=z9hG4bKorig;rport=41796\r\n" +
		"From: <sip:webrtc@example.com>;tag=fromtag123\r\nTo: <sip:c300x@example.com>\r\n" +
		"Call-ID: " + cid + "\r\nCSeq: 5 INVITE\r\nContent-Length: 0\r\n\r\n"
	inv := mustParseSIP(t, raw)
	c.handleLocalMessage(inv)

	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 8192)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal("the listener did not answer our own INVITE (the branch hangs in Flexisip)")
	}
	r := string(buf[:n])
	if !strings.HasPrefix(r, "SIP/2.0 480") {
		t.Fatalf("expected a final 480:\n%s", r)
	}
	if strings.Count(r, "\r\nVia: ") != 2 || sipHeader(r, "Call-ID") != cid || sipHeader(r, "CSeq") != "5 INVITE" {
		t.Fatalf("malformed response (Via/Call-ID/CSeq):\n%s", r)
	}
	if !strings.Contains(sipHeader(r, "To"), ";tag=") {
		t.Fatalf("a final response needs a To-tag:\n%s", r)
	}

	// Flexisip answers our 480 with a hop-by-hop ACK: it is NOT a session.
	ack := mustParseSIP(t, "ACK sip:c300x@127.0.0.1:5090 SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKproxy\r\n"+
		"From: <sip:webrtc@example.com>;tag=fromtag123\r\nTo: <sip:c300x@example.com>;tag=x\r\n"+
		"Call-ID: "+cid+"\r\nCSeq: 5 ACK\r\nContent-Length: 0\r\n\r\n")
	c.handleLocalMessage(ack)
	if c.GetCallState() == Connected {
		t.Fatal("the ACK to our 480 must not mark the session as connected")
	}
}
