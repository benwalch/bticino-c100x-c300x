package sip

import "testing"

// The official app 1.17.3 does not classify incoming calls; it only checks
// whether the remote side has video. Same signal here: video => entrance (the
// only case seen on a real unit), audio only => intercom (assumed: an indoor
// unit), nothing readable => unknown.
func TestClassifyCallType(t *testing.T) {
	cases := []struct {
		name string
		sdp  string
		want string
	}{
		{"entrance: audio+video (real entrance panel SDP)",
			"v=0\r\no=c300x 1 1 IN IP4 127.0.0.1\r\ns=Talk\r\nm=audio 7078 RTP/SAVP 96 18 101\r\nm=video 9078 RTP/SAVP 96 97\r\n",
			CallTypeEntrance},
		{"intercom: audio only",
			"v=0\r\ns=Talk\r\nm=audio 7078 RTP/SAVP 96\r\n",
			CallTypeIntercom},
		{"rejected video (port 0) counts as audio only",
			"v=0\r\nm=audio 7078 RTP/SAVP 96\r\nm=video 0 RTP/SAVP 96\r\n",
			CallTypeIntercom},
		{"empty SDP", "", CallTypeUnknown},
		{"no m= lines", "v=0\r\ns=Talk\r\n", CallTypeUnknown},
		{"LF only (no CR) works too",
			"v=0\nm=audio 7078 RTP/SAVP 96\nm=video 9078 RTP/SAVP 96\n",
			CallTypeEntrance},
	}
	for _, c := range cases {
		if got := ClassifyCallType(c.sdp); got != c.want {
			t.Errorf("%s: ClassifyCallType=%q, want %q", c.name, got, c.want)
		}
	}
}
