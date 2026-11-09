package sip

import (
	"net"
	"time"
)

type RTSPSession struct {
	ID           string
	ClientAddr   string
	StreamPath   string
	State        RTSPSessionState
	SetupTime    time.Time
	LastActivity time.Time
	CSeq         int
	SessionID    string
	ClientPorts  RTSPPortPair
	ServerPorts  RTSPPortPair
	Transport    string
	RTPConn      *net.UDPConn

	// Transport mode for RTP relay
	IsInterleaved    bool     // true for TCP interleaved, false for UDP
	TCPConn          net.Conn // TCP connection for interleaved mode
	InterleavedVideo uint8    // RTP channel for video (interleaved)
	InterleavedAudio uint8    // RTP channel for audio (interleaved)
	TrackSetup       string   // "video", "audio", or "" (both)

	// Per-track UDP ports (set during individual SETUP requests)
	VideoClientPorts RTSPPortPair // client RTP/RTCP ports for video track
	AudioClientPorts RTSPPortPair // client RTP/RTCP ports for audio track
}

type RTSPSessionState int

const (
	RTSPInit RTSPSessionState = iota
	RTSPReady
	RTSPPlaying
)

func (s RTSPSessionState) String() string {
	switch s {
	case RTSPInit:
		return "INIT"
	case RTSPReady:
		return "READY"
	case RTSPPlaying:
		return "PLAYING"
	default:
		return "UNKNOWN"
	}
}

type RTSPPortPair struct {
	RTP  int
	RTCP int
}

type RTSPRequest struct {
	Method    string
	URL       string
	Version   string
	Headers   map[string]string
	Body      string
	CSeq      int
	Session   string
	Transport string
}
