package sip

import (
	"strings"
	"testing"
)

func TestBuildEyeOfferSDP(t *testing.T) {
	sdp := BuildEyeOfferSDP("127.0.0.1", 10102, 10104, "QUJDREVGR0hJSktMTU5PUFFSU1RVVg==", "11")
	checks := []string{
		"m=video 10104 RTP/SAVP",
		"a=recvonly",
		"m=audio 10102 RTP/SAVP",
		"a=sendrecv",
		"speex/8000",
		"a=crypto:",
		"a=DEVADDR:11",
		"c=IN IP4 127.0.0.1",
	}
	for _, c := range checks {
		if !strings.Contains(sdp, c) {
			t.Fatalf("SDP does not contain %q:\n%s", c, sdp)
		}
	}
	if strings.Contains(sdp, "TVCC") {
		t.Fatalf("SDP must not carry TVCC (not a camera):\n%s", sdp)
	}
}
