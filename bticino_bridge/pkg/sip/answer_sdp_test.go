package sip

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateSRTPKey(t *testing.T) {
	k, err := GenerateSRTPKey()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(k)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	if len(raw) != 30 {
		t.Fatalf("expected 30 bytes (16 key + 14 salt), got %d", len(raw))
	}
	k2, _ := GenerateSRTPKey()
	if k == k2 {
		t.Fatal("two equal keys: not random")
	}
}

func TestBuildAnswerSDP_AudioAndVideo(t *testing.T) {
	sdp := BuildAnswerSDP("127.0.0.1", 10100, 10102, "TESTKEYb64==")
	for _, want := range []string{
		"c=IN IP4 127.0.0.1\r\n",
		"m=audio 10100 RTP/SAVP 96\r\n",
		"a=rtpmap:96 speex/8000\r\n",
		"a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:TESTKEYb64==\r\n",
		"m=video 10102 RTP/SAVP 96\r\n",
		"a=rtpmap:96 H264/90000\r\n",
	} {
		if !strings.Contains(sdp, want) {
			t.Errorf("SDP does not contain %q\n---\n%s", want, sdp)
		}
	}
}

func TestBuildAnswerSDP_AudioOnly(t *testing.T) {
	sdp := BuildAnswerSDP("127.0.0.1", 10100, 0, "K==")
	if strings.Contains(sdp, "m=video") {
		t.Errorf("videoPort=0 must not emit m=video\n%s", sdp)
	}
	// Make sure it has m=audio with CRLF
	if !strings.Contains(sdp, "m=audio 10100 RTP/SAVP 96\r\n") {
		t.Errorf("SDP must contain m=audio with CRLF\n%s", sdp)
	}
}

func TestBuildAnswerSDP_VideoRecvOnly(t *testing.T) {
	// The bridge only RECEIVES video (it has no camera): the m=video section must
	// declare a=recvonly, as the official app 1.17.3 does (acceptWithParams with
	// VideoDirection RecvOnly). Audio stays sendrecv (talking to the entrance panel).
	sdp := BuildAnswerSDP("127.0.0.1", 10100, 10102, "K==")
	i := strings.Index(sdp, "m=video")
	if i < 0 {
		t.Fatalf("no m=video:\n%s", sdp)
	}
	audio, video := sdp[:i], sdp[i:]
	if !strings.Contains(video, "a=recvonly\r\n") || strings.Contains(video, "a=sendrecv") {
		t.Errorf("video must be recvonly:\n%s", video)
	}
	if !strings.Contains(audio, "a=sendrecv\r\n") {
		t.Errorf("audio must stay sendrecv:\n%s", audio)
	}
}

func TestParseRemoteAudioPort(t *testing.T) {
	tests := []struct {
		name    string
		offer   string
		want    int
		wantErr bool
	}{
		{
			name: "valid offer with port 7078",
			offer: "v=0\r\no=c300x 3703 703 IN IP4 127.0.0.1\r\ns=Talk\r\n" +
				"c=IN IP4 127.0.0.1\r\nt=0 0\r\n" +
				"m=audio 7078 RTP/SAVP 96 18 101\r\na=rtpmap:96 speex/8000\r\n" +
				"m=video 9078 RTP/SAVP 96 97\r\na=rtpmap:96 H264/90000\r\n",
			want:    7078,
			wantErr: false,
		},
		{
			name:    "offer without m=audio",
			offer:   "v=0\r\no=c300x 3703 703 IN IP4 127.0.0.1\r\ns=Talk\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\n",
			want:    0,
			wantErr: true,
		},
		{
			name:    "m=audio with a non-numeric port",
			offer:   "v=0\r\nm=audio abc RTP/SAVP 96\r\n",
			want:    0,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParseRemoteAudioPort(tt.offer)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRemoteAudioPort() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if p != tt.want {
				t.Errorf("ParseRemoteAudioPort() = %d, want %d", p, tt.want)
			}
		})
	}
}
