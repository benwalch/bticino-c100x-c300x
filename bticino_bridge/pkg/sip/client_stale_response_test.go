package sip

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/events"
)

// sipHeader extracts a header value from a raw SIP message.
func sipHeader(raw, name string) string {
	for _, ln := range strings.Split(raw, "\r\n") {
		if strings.HasPrefix(strings.ToLower(ln), strings.ToLower(name)+":") {
			return strings.TrimSpace(ln[len(name)+1:])
		}
	}
	return ""
}

func sipResponse(status, callID, cseq, body string) string {
	ct := ""
	if body != "" {
		ct = "Content-Type: application/sdp\r\n"
	}
	return fmt.Sprintf("SIP/2.0 %s\r\nVia: SIP/2.0/UDP 127.0.0.1:5090;branch=z9hG4bKx\r\n"+
		"From: <sip:webrtc@example.com>;tag=fromtag123\r\nTo: <sip:c300x@example.com>;tag=remote1\r\n"+
		"Call-ID: %s\r\nCSeq: %s\r\n%sContent-Length: %d\r\n\r\n%s", status, callID, cseq, ct, len(body), body)
}

func sipBye(callID string) string {
	return fmt.Sprintf("BYE sip:webrtc@127.0.0.1:5090 SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bKbye\r\n"+
		"From: <sip:c300x@example.com>;tag=remote0\r\nTo: <sip:webrtc@example.com>;tag=fromtag123\r\n"+
		"Call-ID: %s\r\nCSeq: 20 BYE\r\nContent-Length: 0\r\n\r\n", callID)
}

// TestInviteWithSDPIgnoresForeignResponses reproduces the panel view teardown
// bug (2026-10-13): responses of earlier dialogs were left on the webrtc socket
// (the 200 of the previous BYE, retransmitted 200s of an unACKed INVITE, native
// BYEs). InviteWithSDP took the FIRST "200" as its own → empty/foreign SDP.
// It must accept only the response with ITS Call-ID and INVITE CSeq, and answer
// 200 to the BYEs the native side sends on that socket.
func TestInviteWithSDPIgnoresForeignResponses(t *testing.T) {
	cfg := &SIPConfig{ServerAddr: "127.0.0.1:5060", Domain: "example.com", Username: "webrtc",
		LocalIP: "127.0.0.1", LocalPort: 5090, DevAddr: "20", Transport: "udp"}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	c := NewBTicinoSIPClient(cfg, events.NewEventBus(10, logger), logger)
	// Real UDP on loopback (as in production): datagrams are queued and a Write
	// does not block waiting for a read (net.Pipe would deadlock).
	peer, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	local, err := net.Dial("udp", peer.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	_ = peer.SetDeadline(time.Now().Add(5 * time.Second))
	c.webrtcID = &sipIdentity{username: cfg.Username, fromTag: "fromtag123", cseq: 1, conn: local}
	c.callState = Registered

	realSDP := "v=0\r\nm=audio 9078 RTP/SAVP 98\r\na=rtpmap:98 speex/8000\r\n"
	staleSDP := "v=0\r\nm=audio 1111 RTP/SAVP 98\r\na=rtpmap:98 speex/8000\r\n"

	byeReply := make(chan string, 1)
	ack := make(chan string, 1)
	go func() {
		buf := make([]byte, 16384)
		n, from, err := peer.ReadFrom(buf)
		if err != nil {
			return
		}
		send := func(m string) { _, _ = peer.WriteTo([]byte(m), from) }
		cid := sipHeader(string(buf[:n]), "Call-ID")
		// 1) 200 of the previous panel view BYE (no SDP).
		send(sipResponse("200 OK", "old1@127.0.0.1", "538 BYE", ""))
		// 2) retransmitted 200 of an earlier INVITE never ACKed (another Call-ID).
		send(sipResponse("200 OK", "old2@127.0.0.1", "537 INVITE", staleSDP))
		// 3) native BYE of an earlier dialog: it must get a 200.
		send(sipBye("old2@127.0.0.1"))
		n, _, err = peer.ReadFrom(buf)
		if err != nil {
			return
		}
		byeReply <- string(buf[:n])
		// 4) The right response.
		send(sipResponse("100 Trying", cid, "1 INVITE", ""))
		send(sipResponse("200 OK", cid, "1 INVITE", realSDP))
		n, _, err = peer.ReadFrom(buf)
		if err != nil {
			return
		}
		ack <- string(buf[:n])
	}()

	got, err := c.InviteWithSDP("c300x", "v=0\r\n")
	if err != nil {
		t.Fatalf("InviteWithSDP: %v", err)
	}
	if got != realSDP {
		t.Fatalf("wrong SDP (took a foreign response):\n%q", got)
	}
	select {
	case r := <-byeReply:
		if !strings.HasPrefix(r, "SIP/2.0 200") || sipHeader(r, "Call-ID") != "old2@127.0.0.1" {
			t.Fatalf("unexpected response to the native BYE:\n%s", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no 200 was sent to the native BYE")
	}
	select {
	case a := <-ack:
		if !strings.HasPrefix(a, "ACK ") {
			t.Fatalf("expected ACK, got:\n%s", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ACK was sent to the right response")
	}
}
