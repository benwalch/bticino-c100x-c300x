package sip

import (
	"fmt"
	"strings"
)

// BuildBackchannelSDP describes (for ffmpeg as the RECEIVER) the speex RTP that the go2rtc
// backchannel sends to 127.0.0.1:<port> with payload type <payloadType>. ffmpeg opens that
// port and forwards what arrives; with the format declared in the SDP it need not probe the stream.
func BuildBackchannelSDP(port, payloadType int) string {
	var b strings.Builder
	b.WriteString("v=0\r\n")
	b.WriteString("o=- 0 0 IN IP4 127.0.0.1\r\n")
	b.WriteString("s=talk\r\n")
	b.WriteString("c=IN IP4 127.0.0.1\r\n")
	b.WriteString("t=0 0\r\n")
	b.WriteString(fmt.Sprintf("m=audio %d RTP/AVP %d\r\n", port, payloadType))
	b.WriteString(fmt.Sprintf("a=rtpmap:%d speex/8000\r\n", payloadType))
	b.WriteString("a=recvonly\r\n")
	return b.String()
}
