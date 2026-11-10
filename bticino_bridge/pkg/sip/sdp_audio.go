package sip

import "fmt"

// BuildAudioOfferSDP builds an audio-ONLY offer SDP, speex/8000 PT98,
// SRTP AES_CM_128_HMAC_SHA1_80 (crypto tag 2, the one the unit renders and
// ffmpeg can encrypt). sendrecv: the real media flows both ways. The SSRC is
// in the RTP itself (set by ffmpeg), not in the SDP.
func BuildAudioOfferSDP(localIP string, audioPort int, cryptoKeyB64 string) string {
	return fmt.Sprintf(
		"v=0\r\n"+
			"o=bticino-bridge 0 0 IN IP4 %s\r\n"+
			"s=Talk\r\n"+
			"c=IN IP4 %s\r\n"+
			"t=0 0\r\n"+
			"m=audio %d RTP/SAVP 98\r\n"+
			"a=rtpmap:98 speex/8000\r\n"+
			"a=crypto:2 AES_CM_128_HMAC_SHA1_80 inline:%s\r\n"+
			"a=sendrecv\r\n",
		localIP, localIP, audioPort, cryptoKeyB64,
	)
}
