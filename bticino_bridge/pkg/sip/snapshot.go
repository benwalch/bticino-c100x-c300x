// snapshot.go implements on-demand JPEG frame capture using the
// cooperative pattern (*7*300 → bt_av_media duplicates its RTP), reusing
// captureCooperative. It does NO self-INVITE and does not compete for the camera.
//
// It requires an active native video session ("eye" button or a
// real call); when idle it returns an error (NACK of the *7*300).
package sip

import (
	"fmt"
	"sync"
	"time"

	"bticino_bridge/pkg/openwebnet"

	"github.com/sirupsen/logrus"
)

const (
	// SnapshotMirrorPort is the local UDP port to which we ask bt_av_media
	// to send the video RTP during a snapshot. Different from the probe one (10008).
	SnapshotMirrorPort = 10004

	// snapshotWarmupFrames: frames to accumulate before keeping the
	// last one. The VPU decoder produces noise until a clean IDR; ~12 frames at
	// ~7fps guarantee having passed a keyframe.
	snapshotWarmupFrames = 12
)

// SnapshotService captures JPEG frames from the camera cooperatively.
type SnapshotService struct {
	own    *openwebnet.Client
	logger *logrus.Logger

	mu       sync.Mutex // serializes captures
	lastJPEG []byte
	lastTime time.Time
}

// NewSnapshotService creates the snapshot service on top of the OpenWebNet client.
func NewSnapshotService(own *openwebnet.Client, logger *logrus.Logger) *SnapshotService {
	if logger == nil {
		logger = logrus.New()
	}
	return &SnapshotService{
		own:    own,
		logger: logger,
	}
}

// Capture returns a JPEG frame from the camera. If there is a capture more
// recent than maxAge it is reused (maxAge <= 0 disables the cache). timeout
// covers the polling after sending the *7*300.
func (s *SnapshotService) Capture(timeout, maxAge time.Duration) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if maxAge > 0 && s.lastJPEG != nil && time.Since(s.lastTime) < maxAge {
		s.logger.Debugf("Snapshot: returning cached capture (%s old)", time.Since(s.lastTime).Round(time.Millisecond))
		return s.lastJPEG, nil
	}

	if timeout <= 0 {
		timeout = 12 * time.Second
	}

	res, err := captureCooperative(s.own, SnapshotMirrorPort, snapshotWarmupFrames, timeout, s.logger)
	if err != nil {
		return nil, err
	}
	if len(res.jpeg) == 0 {
		if !res.ack {
			return nil, fmt.Errorf("*7*300 rejected (NACK): no active native video session (%s)", res.ackError)
		}
		return nil, fmt.Errorf("no decodable video arrived (ack=%v, frames=%d)", res.ack, res.frames)
	}

	s.lastJPEG = res.jpeg
	s.lastTime = time.Now()
	s.logger.Infof("Snapshot captured: %d bytes (%d frames)", len(res.jpeg), res.frames)
	return res.jpeg, nil
}

// LastSnapshot returns the last capture and its age (nil if none).
func (s *SnapshotService) LastSnapshot() ([]byte, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastJPEG, s.lastTime
}
