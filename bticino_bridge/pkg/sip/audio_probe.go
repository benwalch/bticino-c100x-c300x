// audio_probe.go implements a MINIMAL and safe AUDIO probe for diagnostics.
//
// It asks ONCE (no retries) bt_av_media to duplicate its Speex audio RTP
// to a local UDP port (*7*300 type 2) and MEASURES the arriving flow (packets,
// bytes, payload type). It neither decodes nor plays — it only confirms that audio
// flows. It does NO self-INVITE.
//
// BTicino audio requires an active video session first: the normal
// sequence is pressing the "eye" (video) and then the "phone" (audio). When idle or
// with phone only, the audio *7*300 brings no RTP.
package sip

import (
	"fmt"
	"net"
	"time"

	"bticino_bridge/pkg/openwebnet"

	"github.com/sirupsen/logrus"
)

// AudioProbePort is the local UDP port for the audio probe. Different from
// video (10004/snapshot, 10008/probe) to avoid collisions.
const AudioProbePort = 10010

// AudioProbeResult summarizes the result of an audio probe.
type AudioProbeResult struct {
	Ack         bool   `json:"ack"`          // *7*300 answered *#*1## (ACK)
	AckError    string `json:"ack_error"`    // empty if ACK; otherwise the error/NACK
	Packets     int    `json:"packets"`      // RTP packets received
	Bytes       int    `json:"bytes"`        // RTP bytes received
	PayloadType int    `json:"payload_type"` // PT of the first packet (-1 if none; Speex ≈ 110)
	Note        string `json:"note"`         // diagnostic hint
}

// AudioProbe sends a single audio *7*300 and measures how much Speex RTP arrives.
func AudioProbe(own *openwebnet.Client, timeout time.Duration, logger *logrus.Logger) (*AudioProbeResult, error) {
	if logger == nil {
		logger = logrus.New()
	}
	res := &AudioProbeResult{PayloadType: -1}
	if own == nil {
		return res, fmt.Errorf("OpenWebNet client not available")
	}
	if timeout <= 0 {
		timeout = 6 * time.Second
	}

	// Listen for the incoming audio RTP
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: AudioProbePort})
	if err != nil {
		return res, fmt.Errorf("failed to listen on :%d: %w", AudioProbePort, err)
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(256 * 1024)

	// Margin and single audio stream request
	time.Sleep(200 * time.Millisecond)
	logger.Infof("AudioProbe: single *7*300 (audio) → 127.0.0.1:%d", AudioProbePort)
	if err := own.ActivateAudioStream("127.0.0.1", AudioProbePort); err != nil {
		res.AckError = err.Error()
	} else {
		res.Ack = true
	}

	// Measure the flow during the window
	buf := make([]byte, 2048)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			break
		}
		if n < 12 { // minimum RTP header
			continue
		}
		res.Packets++
		res.Bytes += n
		if res.PayloadType < 0 {
			res.PayloadType = int(buf[1] & 0x7f) // PT = low 7 bits of the second byte
		}
	}

	switch {
	case res.Packets > 0:
		res.Note = fmt.Sprintf("audio OK: %d RTP packets received (PT=%d)", res.Packets, res.PayloadType)
	case res.Ack:
		res.Note = "*7*300 audio accepted but no RTP arrived (did you press eye + phone?)"
	default:
		res.Note = "*7*300 audio rejected (NACK): activate video (eye) and call (phone) first"
	}

	logger.Infof("AudioProbe: ack=%v packets=%d bytes=%d pt=%d — %s", res.Ack, res.Packets, res.Bytes, res.PayloadType, res.Note)
	return res, nil
}
