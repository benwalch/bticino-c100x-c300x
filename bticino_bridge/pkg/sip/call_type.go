package sip

import (
	"strconv"
	"strings"
)

// Incoming call types the answerer reports to HA.
const (
	CallTypeEntrance = "entrance" // ring from the entrance panel (carries video); seen on a real unit
	CallTypeIntercom = "intercom" // audio only: presumably another indoor unit; not verified
	CallTypeUnknown  = "unknown"  // empty SDP or no readable m= lines
)

// ClassifyCallType derives the type of an incoming call from the INVITE SDP.
// It uses the same signal as the official app 1.17.3 (isVideoEnabled of the
// remote side): an m=video line with a non-zero port is the entrance panel,
// audio only is intercom, no readable m= line is unknown. The app classifies
// incoming calls no further, and no capture supports telling the answering
// machine apart as a caller, so this does not try.
func ClassifyCallType(sdp string) string {
	audio, video := false, false
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		var media string
		switch {
		case strings.HasPrefix(line, "m=audio "):
			media = "audio"
		case strings.HasPrefix(line, "m=video "):
			media = "video"
		default:
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		port, err := strconv.Atoi(fields[1])
		if err != nil || port == 0 { // port 0 = media rejected or inactive
			continue
		}
		if media == "video" {
			video = true
		} else {
			audio = true
		}
	}
	switch {
	case video:
		return CallTypeEntrance
	case audio:
		return CallTypeIntercom
	default:
		return CallTypeUnknown
	}
}
