package sip

import (
	"io"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/events"
)

// TestBuildInviteRequestWithSDP checks that the resulting INVITE carries the
// SDP passed as a parameter (not the dummy buildSDP()), and that
// buildInviteRequest (video self-INVITE) still uses the dummy unchanged.
func TestBuildInviteRequestWithSDP(t *testing.T) {
	cfg := &SIPConfig{
		ServerAddr: "127.0.0.1:5060",
		Domain:     "example.com",
		Username:   "webrtc",
		LocalIP:    "127.0.0.1",
		LocalPort:  5090,
		DevAddr:    "20",
		Transport:  "udp",
	}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	c := NewBTicinoSIPClient(cfg, events.NewEventBus(1, logger), logger)
	c.webrtcID = &sipIdentity{username: cfg.Username, fromTag: "fromtag123", cseq: 1}
	c.callID = "call123@127.0.0.1"

	customSDP := "v=0\r\no=webrtc 1 1 IN IP4 127.0.0.1\r\ns=talkback\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 40100 RTP/SAVP 98\r\na=rtpmap:98 speex/8000\r\na=crypto:2 AES_CM_128_HMAC_SHA1_80 inline:SOMEKEY\r\n"

	invite := c.buildInviteRequestWithSDP("c300x", customSDP)

	if !strings.Contains(invite, customSDP) {
		t.Fatalf("the INVITE does not contain the injected SDP:\n%s", invite)
	}
	if !strings.HasPrefix(invite, "INVITE sip:c300x@example.com SIP/2.0") {
		t.Fatalf("unexpected request line:\n%s", invite)
	}

	// The video self-INVITE (buildInviteRequest) must still use the dummy SDP of
	// buildSDP(), not the customSDP above.
	c.callID = "call456@127.0.0.1"
	videoInvite := c.buildInviteRequest("c300x")
	if strings.Contains(videoInvite, "SOMEKEY") {
		t.Fatal("buildInviteRequest (video self-INVITE) must not be affected by buildInviteRequestWithSDP")
	}
	if !strings.Contains(videoInvite, "dummykey") {
		t.Fatalf("buildInviteRequest must still use the dummy SDP:\n%s", videoInvite)
	}
}

func TestExtractSIPUser(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"Doorbell" <sip:c300x@YOUR_GATEWAY_ID.bs.iotleg.com>;tag=abc123`, "c300x"},
		{`<sip:webrtc@example.com>`, "webrtc"},
		{`sip:alice@10.0.0.1;transport=tcp`, "alice"},
		{`sip:noatsign`, "noatsign"},
		{`Bob <sip:bob@host>`, "bob"},
		{`plain text no uri`, "plain text no uri"},
	}
	for _, c := range cases {
		if got := extractSIPUser(c.in); got != c.want {
			t.Errorf("extractSIPUser(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
