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

// CallActiveMarker permite marcar que ya hay una llamada nativa en curso, para
// que el servidor RTSP NO dispare un self-INVITE. Lo implementa *EnhancedRTSPServer.
type CallActiveMarker interface {
	SetCallActive(active bool)
}

// LiveVideoOnCall arma el vídeo en vivo de la puerta durante una llamada entrante
// a NUESTRA unidad mediante un espejo PASIVO (tap pcap del leg de vídeo nativo
// en lo:5007 → relay 10002). CERO comandos al bus: la Fase 0 (2026-08-18)
// demostró que un *7*300 propio durante una llamada real se ACKea pero se
// ignora (el nativo ocupa ambos slots de vídeo con sus legs SIP), y que el RTP
// H.264 de lo:5007 va en claro y sostenido toda la llamada.
type LiveVideoOnCall struct {
	tap    VideoTap
	marker CallActiveMarker // puede ser nil
	logger *logrus.Logger

	mu     sync.Mutex
	active bool
	guard  time.Duration // 0 = sin guard
	timer  *time.Timer
}

// SetGuardTimeout activa un desarme automático de seguridad: si el fin de
// llamada no llega (la detección por tramas OWN es heurística), Stop se invoca
// solo pasado d desde el Start. 0 desactiva el guard. Llamar antes de Start.
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

// Start arms the mirror. Idempotent: if already active, it does nothing.
// A tap failure (e.g. no permission for AF_PACKET) degrades without video but
// keeps the call marked as active (inhibits the RTSP self-INVITE).
func (l *LiveVideoOnCall) Start(reason string) {
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

	if l.marker != nil {
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
