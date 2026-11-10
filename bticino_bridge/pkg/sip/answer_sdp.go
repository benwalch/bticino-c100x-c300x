package sip

import (
	"fmt"
	"strconv"
	"strings"
)

// GenerateSRTPKey generates an SDES key for AES_CM_128_HMAC_SHA1_80 (30 bytes,
// base64). Alias of NewSRTPMasterKey to keep a single key source.
func GenerateSRTPKey() (string, error) {
	return NewSRTPMasterKey()
}

// BuildAnswerSDP builds the answerer's SDP answer. Speex audio PT96 over SRTP
// (AES_CM_128_HMAC_SHA1_80). If videoPort>0, it adds m=video H264 42801F. Deterministic.
func BuildAnswerSDP(localIP string, audioPort, videoPort int, srtpKeyB64 string) string {
	var b strings.Builder
	b.WriteString("v=0\r\n")
	b.WriteString(fmt.Sprintf("o=bridge 0 0 IN IP4 %s\r\n", localIP))
	b.WriteString("s=Talk\r\n")
	b.WriteString(fmt.Sprintf("c=IN IP4 %s\r\n", localIP))
	b.WriteString("t=0 0\r\n")
	// audio
	b.WriteString(fmt.Sprintf("m=audio %d RTP/SAVP 96\r\n", audioPort))
	b.WriteString("a=rtpmap:96 speex/8000\r\n")
	b.WriteString("a=fmtp:96 vbr=on\r\n")
	b.WriteString(fmt.Sprintf("a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:%s\r\n", srtpKeyB64))
	b.WriteString("a=sendrecv\r\n")
	if videoPort > 0 {
		b.WriteString(fmt.Sprintf("m=video %d RTP/SAVP 96\r\n", videoPort))
		b.WriteString("a=rtpmap:96 H264/90000\r\n")
		b.WriteString("a=fmtp:96 profile-level-id=42801F\r\n")
		b.WriteString(fmt.Sprintf("a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:%s\r\n", srtpKeyB64))
		// recvonly: the bridge only RECEIVES the entrance panel's video (it has no camera to send).
		// This is what the official app 1.17.3 declares when answering. It does not affect video to HA.
		b.WriteString("a=recvonly\r\n")
	}
	return b.String()
}

// ParseRemoteAudioPort extracts the port of the first m=audio line of the offer.
func ParseRemoteAudioPort(offer string) (int, error) {
	for _, line := range strings.Split(offer, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "m=audio ") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, fmt.Errorf("malformed m=audio: %q", line)
			}
			return strconv.Atoi(fields[1])
		}
	}
	return 0, fmt.Errorf("no m=audio in the offer")
}
