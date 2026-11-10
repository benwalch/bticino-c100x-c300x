package sip

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// LiveAudioOnCall arms live door audio during a call through a PASSIVE
// mirror: a tap of the native audio leg on lo:5000 (cleartext Speex PT110) →
// the RTSP audio relay (10000). NO decode/transcode (the codec already
// matches) and NO bus commands. Mirror of LiveVideoOnCall, without the
// CallActiveMarker (audio does not trigger a self-INVITE). go2rtc publishes
// /doorbell (audio=pcma) to HA.
type LiveAudioOnCall struct {
	tap    VideoTap
	logger *logrus.Logger

	mu     sync.Mutex
	active bool
	guard  time.Duration
	timer  *time.Timer
}

func NewLiveAudioOnCall(tap VideoTap, logger *logrus.Logger) *LiveAudioOnCall {
	if logger == nil {
		logger = logrus.New()
	}
	return &LiveAudioOnCall{tap: tap, logger: logger}
}

// SetGuardTimeout enables a safety disarm when the call end never arrives.
// 0 turns it off. Call before Start.
func (l *LiveAudioOnCall) SetGuardTimeout(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.guard = d
}

// Start arms the audio mirror. Idempotent. Leak-safe: if a Stop wins the race
// while tap.Start() is running, it checks again and stops the tap.
func (l *LiveAudioOnCall) Start(reason string) {
	l.mu.Lock()
	if l.active {
		l.mu.Unlock()
		return
	}
	l.active = true
	if l.guard > 0 {
		l.timer = time.AfterFunc(l.guard, func() {
			l.logger.Warn("live-audio: guard expired without a call end; disarming")
			l.Stop()
		})
	}
	l.mu.Unlock()

	l.logger.Infof("live-audio: arming passive mirror lo:5000 → relay (reason=%s)", reason)
	if l.tap != nil {
		if err := l.tap.Start(); err != nil {
			l.logger.WithError(err).Warn("live-audio: tap unavailable; continuing without audio")
		}
	}
	// Leak fix: if a concurrent Stop flipped active while tap.Start() was
	// running, the tap would have started with active=false, so stop it.
	l.mu.Lock()
	orphaned := !l.active
	l.mu.Unlock()
	if orphaned && l.tap != nil {
		l.logger.Warn("live-audio: Stop won the race during Start; stopping the orphaned tap")
		l.tap.Stop()
	}
}

// Stop disarms (stops the tap). Idempotent.
func (l *LiveAudioOnCall) Stop() {
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
	l.logger.Info("live-audio: disarmed")
}

func (l *LiveAudioOnCall) IsActive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}
