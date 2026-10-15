package sip

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// VideoTap abstracts the passive video mirror (LoopbackRTPTap in production).
type VideoTap interface {
	Start() error
	Stop()
}

// CallActiveMarker allows marking that a native call is already in progress, so
// that the RTSP server does NOT fire a self-INVITE. Implemented by *EnhancedRTSPServer.
type CallActiveMarker interface {
	SetCallActive(active bool)
}

// LiveVideoOnCall arms the door's live video during an incoming call
// to OUR unit via a PASSIVE mirror (pcap tap of the native video leg
// on lo:<native-port> → relay 10002; 5007 high-res on c300x, 5002 low-res
// on c100x, selected via bticino.DeviceVariant). ZERO commands to the bus: Phase 0 (2026-08-18)
// showed that an own *7*300 during a real call gets ACKed but is
// ignored (the native side occupies both video slots with its SIP legs), and that the
// H.264 RTP on lo:<native-port> is in clear and sustained for the whole call.
type LiveVideoOnCall struct {
	tap    VideoTap
	marker CallActiveMarker // may be nil
	logger *logrus.Logger

	mu     sync.Mutex
	active bool
	guard  time.Duration // 0 = no guard
	timer  *time.Timer
}

// SetGuardTimeout enables an automatic safety disarm: if the end of
// the call never arrives (OWN frame detection is heuristic), Stop is invoked
// on its own once d has elapsed since Start. 0 disables the guard. Call before Start.
func (l *LiveVideoOnCall) SetGuardTimeout(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.guard = d
}

// NewLiveVideoOnCall creates the orchestrator.
func NewLiveVideoOnCall(tap VideoTap, marker CallActiveMarker, logger *logrus.Logger) *LiveVideoOnCall {
	if logger == nil {
		logger = logrus.New()
	}
	return &LiveVideoOnCall{tap: tap, marker: marker, logger: logger}
}

// Start arms the mirror and marks a native call. Idempotent: if already
// active, it does nothing.
// A tap failure (e.g. no permission for AF_PACKET) degrades without video but
// keeps the call marked as active (inhibits the RTSP self-INVITE).
func (l *LiveVideoOnCall) Start(reason string) {
	l.start(reason, true)
}

// StartTapOnly arms the mirror without marking a native call, for our own
// headless sessions: the reaper must keep seeing them as ours to stop them.
func (l *LiveVideoOnCall) StartTapOnly(reason string) {
	l.start(reason, false)
}

func (l *LiveVideoOnCall) start(reason string, mark bool) {
	l.mu.Lock()
	if l.active {
		l.mu.Unlock()
		return
	}
	l.active = true
	if l.guard > 0 {
		l.timer = time.AfterFunc(l.guard, func() {
			l.logger.Warn("live-video: guard expired with no call end — disarming")
			l.Stop()
		})
	}
	l.mu.Unlock()

	if mark && l.marker != nil {
		l.marker.SetCallActive(true)
	}
	l.logger.Infof("live-video: arming passive mirror (reason=%s)", reason)
	if l.tap != nil {
		if err := l.tap.Start(); err != nil {
			l.logger.WithError(err).Warn("live-video: tap unavailable — degrading without video")
		}
	}
}

// Stop disarms (stops the tap and releases the marker). Idempotent.
func (l *LiveVideoOnCall) Stop() {
	l.mu.Lock()
	if !l.active {
		l.mu.Unlock()
		return
	}
	l.active = false
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	l.mu.Unlock()

	if l.tap != nil {
		l.tap.Stop()
	}
	if l.marker != nil {
		l.marker.SetCallActive(false)
	}
	l.logger.Info("live-video: disarmed")
}

// IsActive reports whether live video is armed.
func (l *LiveVideoOnCall) IsActive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}
