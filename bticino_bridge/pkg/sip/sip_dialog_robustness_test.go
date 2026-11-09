package sip

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/events"
)

func readPipeMessage(t *testing.T, conn net.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16384)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(buf[:n])
}

// BYEs of earlier dialogs still reach the webrtc socket (see isOwnInviteResponse);
// they must not end the session that is running now.
func TestCameraDialogIgnoresByeOfOlderDialog(t *testing.T) {
	c, _, _ := newSessionTestClient()
	c.logger.SetOutput(io.Discard)
	near, far := net.Pipe()
	defer near.Close()
	defer far.Close()
	c.webrtcID = &sipIdentity{username: "webrtc", conn: near}
	dlg := &cameraDialog{callID: "current@127.0.0.1", toTag: "native1"}

	ended := make(chan string, 1)
	go func() { ended <- c.drainCameraDialog(dlg, make(chan struct{}), 10*time.Second) }()

	far.Write([]byte(sipBye("older@127.0.0.1")))
	if resp := readPipeMessage(t, far); !strings.HasPrefix(resp, "SIP/2.0 200") {
		t.Fatalf("older BYE not answered with 200:\n%s", resp)
	}
	select {
	case reason := <-ended:
		t.Fatalf("session ended by an older dialog's BYE (%s)", reason)
	case <-time.After(300 * time.Millisecond):
	}

	far.Write([]byte(sipBye(dlg.callID)))
	readPipeMessage(t, far)
	select {
	case reason := <-ended:
		if reason != sessionEndNative {
			t.Fatalf("reason %q, want %q", reason, sessionEndNative)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session did not end on its own BYE")
	}
}

// RFC 3261 8.2.6.2: the response carries the request's To header. In a
// re-INVITE from the native side that is our own tag, not the native one.
func TestCameraReinviteAnswerKeepsToHeader(t *testing.T) {
	c, _, _ := newSessionTestClient()
	const to = "<sip:webrtc@example.com>;tag=ours1"
	raw := "INVITE sip:webrtc@127.0.0.1:5090;transport=tcp SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bKre\r\n" +
		"From: <sip:c100x@example.com>;tag=native1\r\nTo: " + to + "\r\n" +
		"Call-ID: current@127.0.0.1\r\nCSeq: 2 INVITE\r\nContent-Length: 0\r\n\r\n"
	msgs, _ := splitSIPStream([]byte(raw))
	if len(msgs) != 1 {
		t.Fatalf("parsed %d messages", len(msgs))
	}
	near, far := net.Pipe()
	defer near.Close()
	defer far.Close()
	dlg := &cameraDialog{callID: "current@127.0.0.1", toTag: "native1", offer: "v=0\r\n"}

	go c.answerCameraReinvite(near, dlg, msgs[0])
	resp := readPipeMessage(t, far)

	if got := sipHeader(resp, "To"); got != to {
		t.Fatalf("To %q, want %q", got, to)
	}
}

// The REGISTER check read the whole message: from CSeq 200 on, any answer,
// a refusal too, counted as registered.
func TestRegisterChecksStatusCode(t *testing.T) {
	cases := map[string]bool{
		"SIP/2.0 403 Forbidden":               false,
		"SIP/2.0 200 Registration successful": true,
	}
	for status, wantOK := range cases {
		t.Run(status, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				req, _ := bufio.NewReader(conn).ReadString('\n')
				if !strings.HasPrefix(req, "REGISTER ") {
					return
				}
				time.Sleep(50 * time.Millisecond) // let the rest of the request arrive
				conn.Write([]byte(status + "\r\nVia: SIP/2.0/TCP 127.0.0.1:5090;branch=z9hG4bKr\r\n" +
					"From: <sip:webrtc@example.com>;tag=a\r\nTo: <sip:webrtc@example.com>\r\n" +
					"Call-ID: reg@127.0.0.1\r\nCSeq: 200 REGISTER\r\nContent-Length: 0\r\n\r\n"))
				io.Copy(io.Discard, conn)
			}()

			logger := logrus.New()
			logger.SetOutput(io.Discard)
			cfg := &SIPConfig{ServerAddr: ln.Addr().String(), Domain: "example.com", Username: "webrtc",
				LocalIP: "127.0.0.1", LocalPort: 5090, Transport: "tcp"}
			c := NewBTicinoSIPClient(cfg, events.NewEventBus(1, logger), logger)
			id := &sipIdentity{username: "webrtc", fromTag: "a", callID: "reg@127.0.0.1", cseq: 200}

			err = c.registerIdentity(id)
			if id.conn != nil {
				defer id.conn.Close()
			}
			if gotOK := err == nil; gotOK != wantOK {
				t.Fatalf("registered=%v (err=%v), want %v", gotOK, err, wantOK)
			}
		})
	}
}
