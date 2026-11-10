package sip

import (
	"strings"
	"testing"
)

func TestBuildForwardArgs(t *testing.T) {
	ans := AudioAnswer{Port: 5000, PayloadType: 110, CryptoSuite: "AES_CM_128_HMAC_SHA1_80", CryptoKey: "QUJDRA=="}
	args := buildForwardArgs("/tmp/in.sdp", "127.0.0.1:10000", ans)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f rtp") {
		t.Fatalf("output is not RTP: %s", joined)
	}
	if !strings.Contains(joined, "rtp://127.0.0.1:10000") {
		t.Fatalf("wrong RTP destination: %s", joined)
	}
	if strings.Contains(joined, "-f wav") || strings.Contains(joined, ".wav") {
		t.Fatalf("must not write WAV: %s", joined)
	}
	if !strings.Contains(joined, "srtp") {
		t.Fatalf("whitelist must allow srtp (incoming decryption): %s", joined)
	}
}
