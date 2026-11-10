package sip

import "testing"

const answerSDP = `v=0
o=belle-sip 123 456 IN IP4 127.0.0.1
s=Talk
c=IN IP4 127.0.0.1
t=0 0
m=audio 7078 RTP/SAVP 98 100
a=rtpmap:98 speex/8000
a=rtpmap:100 telephone-event/8000
a=crypto:2 AES_CM_128_HMAC_SHA1_80 inline:lc6j6XEp0E237/+J1EBO+WtNDfIUD+gRTyVih5qy
a=sendrecv
`

func TestParseAudioAnswer(t *testing.T) {
	a, err := ParseAudioAnswer(answerSDP)
	if err != nil {
		t.Fatalf("ParseAudioAnswer: %v", err)
	}
	if a.Port != 7078 {
		t.Fatalf("port: expected 7078, got %d", a.Port)
	}
	if a.PayloadType != 98 {
		t.Fatalf("PT: expected 98 (speex/8000), got %d", a.PayloadType)
	}
	if a.CryptoSuite != "AES_CM_128_HMAC_SHA1_80" {
		t.Fatalf("suite: %q", a.CryptoSuite)
	}
	if a.CryptoKey != "lc6j6XEp0E237/+J1EBO+WtNDfIUD+gRTyVih5qy" {
		t.Fatalf("key: %q", a.CryptoKey)
	}
}

func TestParseAudioAnswerNoAudio(t *testing.T) {
	if _, err := ParseAudioAnswer("v=0\r\ns=x\r\n"); err == nil {
		t.Fatal("expected an error without m=audio")
	}
}

func TestParseAudioAnswerNoSupportedCrypto(t *testing.T) {
	// GCM only (unsupported): must fail clearly.
	sdp := "m=audio 7078 RTP/SAVP 98\r\na=rtpmap:98 speex/8000\r\na=crypto:1 AEAD_AES_128_GCM inline:xxxx\r\n"
	if _, err := ParseAudioAnswer(sdp); err == nil {
		t.Fatal("expected an error without AES_CM_128_HMAC_SHA1_80")
	}
}

// TestParseAudioAnswerSelectsAudioSection: the panel view's answer has TWO m=
// (video recvonly + audio sendrecv). ParseAudioAnswer must take the port and key
// of the AUDIO leg, never of the video one (which comes first).
func TestParseAudioAnswerSelectsAudioSection(t *testing.T) {
	sdp := "v=0\r\n" +
		"m=video 7000 RTP/SAVP 96\r\n" +
		"a=rtpmap:96 H264/90000\r\n" +
		"a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:VIDEOvideoVIDEOvideoVIDEOvideoVIDEOvideo\r\n" +
		"m=audio 5000 RTP/SAVP 98\r\n" +
		"a=rtpmap:98 speex/8000\r\n" +
		"a=crypto:2 AES_CM_128_HMAC_SHA1_80 inline:QUJDRA==\r\n"
	a, err := ParseAudioAnswer(sdp)
	if err != nil {
		t.Fatalf("ParseAudioAnswer: %v", err)
	}
	if a.Port != 5000 {
		t.Fatalf("Port=%d, expected 5000 (audio leg, not 7000 of video)", a.Port)
	}
	if a.PayloadType != 98 {
		t.Fatalf("PayloadType=%d, expected 98 (speex)", a.PayloadType)
	}
	if a.CryptoKey != "QUJDRA==" {
		t.Fatalf("CryptoKey=%q, expected the audio leg key", a.CryptoKey)
	}
}
