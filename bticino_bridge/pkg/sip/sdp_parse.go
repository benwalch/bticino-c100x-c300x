package sip

import (
	"fmt"
	"strconv"
	"strings"
)

// AudioAnswer holds the unit's audio leg data taken from its SDP.
type AudioAnswer struct {
	Port        int
	PayloadType int
	CryptoSuite string
	CryptoKey   string
}

const srtpSuiteAESCM = "AES_CM_128_HMAC_SHA1_80"

// ParseAudioAnswer extracts the audio port, the speex/8000 PT and the SRTP key
// of the AES_CM_128_HMAC_SHA1_80 suite from the unit's answer SDP. It accepts
// CRLF or LF and returns an error if m=audio or the supported suite is missing.
func ParseAudioAnswer(sdp string) (AudioAnswer, error) {
	var a AudioAnswer
	lines := strings.Split(strings.ReplaceAll(sdp, "\r\n", "\n"), "\n")

	inAudio := false
	speexPT := -1
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(ln, "m=audio"):
			inAudio = true
			// m=audio <port> RTP/SAVP <pts...>
			f := strings.Fields(ln)
			if len(f) >= 2 {
				a.Port, _ = strconv.Atoi(f[1])
			}
		case strings.HasPrefix(ln, "m="):
			inAudio = false // another media block
		case inAudio && strings.HasPrefix(ln, "a=rtpmap:"):
			// a=rtpmap:98 speex/8000
			if strings.Contains(strings.ToLower(ln), "speex/8000") {
				body := strings.TrimPrefix(ln, "a=rtpmap:")
				if sp := strings.Fields(body); len(sp) >= 1 {
					speexPT, _ = strconv.Atoi(sp[0])
				}
			}
		case inAudio && strings.HasPrefix(ln, "a=crypto:") && strings.Contains(ln, srtpSuiteAESCM):
			// a=crypto:<tag> AES_CM_128_HMAC_SHA1_80 inline:<key>[ ...]
			if idx := strings.Index(ln, "inline:"); idx >= 0 {
				key := ln[idx+len("inline:"):]
				key = strings.Fields(key)[0] // drop lifetime/MKI, if any
				a.CryptoSuite = srtpSuiteAESCM
				a.CryptoKey = key
			}
		}
	}

	if a.Port == 0 {
		return a, fmt.Errorf("SDP without m=audio or with an invalid port")
	}
	if a.CryptoKey == "" {
		return a, fmt.Errorf("SDP without a=crypto %s (the negotiated suite is not supported)", srtpSuiteAESCM)
	}
	if speexPT < 0 {
		return a, fmt.Errorf("SDP without rtpmap speex/8000")
	}
	a.PayloadType = speexPT
	return a, nil
}
