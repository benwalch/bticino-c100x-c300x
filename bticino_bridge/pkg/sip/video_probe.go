// video_probe.go implements a MINIMAL and SAFE video probe for diagnostics.
//
// It reuses captureCooperative: it asks ONCE (no retries) bt_av_media to
// duplicate its video RTP and decodes whatever arrives. It does NO self-INVITE
// and does not touch SIP signaling. Meant to be used while native video is already
// active (e.g. after pressing the "eye"/self-switch-on button).
package sip

import (
	"time"

	"bticino_bridge/pkg/openwebnet"

	"github.com/sirupsen/logrus"
)

// VideoProbePort is the local UDP port for the probe. Different from snapshot
// (10004) and relay (10002) to avoid collisions.
const VideoProbePort = 10008

// probeWarmupFrames: the probe is a quick diagnostic, with less warmup.
const probeWarmupFrames = 8

// VideoProbeResult summarizes the result of a probe.
type VideoProbeResult struct {
	Ack       bool   `json:"ack"`        // *7*300 answered *#*1## (ACK)
	AckError  string `json:"ack_error"`  // empty if ACK; otherwise the error/NACK
	Frames    int    `json:"frames"`     // decoded JPEG frames
	JPEGBytes int    `json:"jpeg_bytes"` // size of the best frame
	Note      string `json:"note"`       // diagnostic hint
}

// VideoProbe sends a single *7*300 and checks whether decodable video RTP arrives.
// It returns the result and, if any, the best JPEG frame.
func VideoProbe(own *openwebnet.Client, timeout time.Duration, logger *logrus.Logger) (*VideoProbeResult, []byte, error) {
	if logger == nil {
		logger = logrus.New()
	}
	res := &VideoProbeResult{}

	cap, err := captureCooperative(own, VideoProbePort, probeWarmupFrames, timeout, logger)
	if err != nil {
		return res, nil, err
	}

	res.Ack = cap.ack
	res.AckError = cap.ackError
	res.Frames = cap.frames
	res.JPEGBytes = len(cap.jpeg)

	switch {
	case res.Frames > 0:
		res.Note = "video OK: RTP flowed and frames were decoded"
	case res.Ack && res.Frames == 0:
		res.Note = "*7*300 accepted but no decodable RTP arrived (native session active?)"
	default:
		res.Note = "*7*300 rejected (NACK): bt_av_media has no stream to duplicate; press the 'eye' first"
	}

	logger.Infof("VideoProbe result: ack=%v frames=%d bytes=%d — %s", res.Ack, res.Frames, res.JPEGBytes, res.Note)
	return res, cap.jpeg, nil
}
