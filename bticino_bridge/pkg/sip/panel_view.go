package sip

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"bticino_bridge/pkg/bticino"
	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/events"

	"github.com/sirupsen/logrus"
)

// ErrEyeBusy means a panel view ("Show door panel") is already running: Show is idempotent.
var ErrEyeBusy = errors.New("panelview: a panel view is already active")

// EyeOriginator places the panel view's SIP dialog against the local Flexisip.
// *BTicinoSIPClient satisfies it.
type EyeOriginator interface {
	InviteWithSDP(target, sdp string) (string, error)
	Hangup() error
	CallID() string
}

// AudioForwarder forwards the entrance panel's incoming SRTP to the audio relay
// (listening). *AudioReceiver satisfies it.
type AudioForwarder interface {
	CaptureForward(localHost string, localPort int, dstHostPort string, ans AudioAnswer) error
	Stop()
}

// CallActiveChecker asks whether a NATIVE call or answering machine is running,
// so the bridge does not fight the native side (non-interference). NOTE: this is
// not CallActiveMarker (that is the SetCallActive SETTER used by LiveVideoOnCall);
// here a READ is needed. *EnhancedRTSPServer satisfies it via IsCallActive().
type CallActiveChecker interface {
	IsCallActive() bool
}

// PanelView is the "Show door panel" FSM: it places the panel view (on-demand
// power-on), sets up the 3 media tracks (video via taps, listening via the
// forwarder, talking via TalkSender, which listens to panelview.connected) and
// switches itself off on a timeout. A single leg: check-and-set under a mutex,
// like AudioCall/Answerer.
type PanelView struct {
	cfg       config.PanelViewConfig
	orig      EyeOriginator
	fwd       AudioForwarder
	own       OWNSender
	marker    CallActiveChecker
	bus       events.EventBus
	localIP   string
	audioPort int
	videoPort int
	logger    *logrus.Logger

	timeout time.Duration

	mu     sync.Mutex
	active bool
	timer  *time.Timer
}

// NewPanelView builds the FSM. own and marker can be nil.
func NewPanelView(cfg config.PanelViewConfig, orig EyeOriginator, fwd AudioForwarder, own OWNSender, marker CallActiveChecker, bus events.EventBus, localIP string, audioPort, videoPort int, logger *logrus.Logger) *PanelView {
	if logger == nil {
		logger = logrus.New()
	}
	return &PanelView{
		cfg: cfg, orig: orig, fwd: fwd, own: own, marker: marker, bus: bus,
		localIP: localIP, audioPort: audioPort, videoPort: videoPort, logger: logger,
		timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
	}
}

// setTimeoutForTest sets the auto-off (tests only).
func (p *PanelView) setTimeoutForTest(d time.Duration) { p.timeout = d }

// Start subscribes the FSM to the bus events.
func (p *PanelView) Start() error {
	p.bus.Subscribe("panelview.request", func(*events.Event) { _ = p.Show() })
	p.bus.Subscribe("doorcall.hangup.request", func(*events.Event) { _ = p.Hangup() }) // existing Hang up button
	p.bus.Subscribe("doorcall.incoming", func(*events.Event) { _ = p.Hangup() })       // yields to a real ring
	// The native side hung up the panel view (remote BYE, seen by the SIP client's
	// watcher): tear down without our own BYE (the client already marked the session closed).
	p.bus.Subscribe("sip.dialog.ended", func(e *events.Event) {
		d, _ := e.Data.(map[string]interface{})
		if cid, _ := d["call_id"].(string); cid != "" && p.orig != nil && cid == p.orig.CallID() {
			_ = p.teardown("remote_bye")
		}
	})
	return nil
}

// Active reports whether a panel view is running.
func (p *PanelView) Active() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.active }

func (p *PanelView) reject(reason string) error {
	p.logger.Warnf("panelview: rejected (%s)", reason)
	p.bus.Publish("panelview.rejected", map[string]interface{}{"reason": reason})
	return fmt.Errorf("panelview: %s", reason)
}

// Show places the panel view. Idempotent: ErrEyeBusy if one is already active.
func (p *PanelView) Show() error {
	// Guardas fail-closed.
	if !p.cfg.Enabled || p.cfg.DEVADDR == "" {
		return p.reject("disabled or no devaddr")
	}
	p.mu.Lock()
	if p.active {
		// Our own panel view first: while it is open the native channel is up
		// (callActive); before, this was rejected as "native call active" and
		// panelview.rejected was published (HA sensor idle with the view open).
		p.mu.Unlock()
		p.logger.Info("panelview: a panel view is already active; press ignored")
		return ErrEyeBusy
	}
	if p.marker != nil && p.marker.IsCallActive() {
		p.mu.Unlock()
		return p.reject("native call/answering machine active")
	}
	p.active = true
	p.mu.Unlock()

	key, err := NewSRTPMasterKey()
	if err != nil {
		p.release()
		return fmt.Errorf("SRTP key: %w", err)
	}
	offer := BuildEyeOfferSDP(p.localIP, p.audioPort, p.videoPort, key, p.cfg.DEVADDR)
	answerSDP, err := p.orig.InviteWithSDP(bticino.SIPUsernameForVariant(bticino.DeviceVariant), offer)
	if err != nil {
		p.release()
		// Show() is called from the bus with the error dropped: leave a trace here.
		p.logger.WithError(err).Warn("panelview: panel view INVITE failed")
		return fmt.Errorf("panel view INVITE: %w", err)
	}
	ans, err := ParseAudioAnswer(answerSDP)
	if err != nil {
		_ = p.orig.Hangup() // leave no orphaned dialog
		p.release()
		p.logger.WithError(err).Warn("panelview: invalid panel view answer SDP; hung up")
		return fmt.Errorf("parse answer: %w", err)
	}
	if p.cfg.SendWho8Arm && p.own != nil {
		if err := p.own.SendOpenWebNetCommand(bticino.CmdAnswerPickup); err != nil {
			p.logger.WithError(err).Debug("panelview: WHO=8 arm without ACK (continuing)")
		}
	}
	callID := p.orig.CallID()

	// Critical section: the INVITE ran OUTSIDE the mutex; a Hangup (e.g. a
	// yielding doorcall.incoming, or the Hang up button) could slip in and set
	// active=false while we waited for the answer. Check active again and set
	// up media + timer under the SAME lock, atomically: so a yielding panel view
	// never leaves orphaned media/timers armed with active==false.
	p.mu.Lock()
	if !p.active {
		p.mu.Unlock()
		_ = p.orig.Hangup() // close the SIP dialog just opened (do not orphan it)
		p.logger.Info("panelview: panel view yielded during the INVITE; closed without media")
		return nil
	}
	p.bus.Publish("panelview.started", map[string]interface{}{"call_id": callID})
	// Video: arm the passive taps (lo:5007→10002 video, lo:5000→10000 audio, even if silent).
	p.bus.Publish("sip.call.connected", map[string]interface{}{"reason": "panelview"})
	// Listening: forward the incoming SRTP (panel key in ans) to the PRIORITY input
	// of the audio relay: it displaces the silent lo:5000 tap and goes out as PT110 of the SDP.
	go func() {
		if err := p.fwd.CaptureForward(p.localIP, p.audioPort, fmt.Sprintf("127.0.0.1:%d", EyeAudioRelayPort), ans); err != nil {
			p.logger.WithError(err).Debug("panelview: listening forwarder ended")
		}
	}()
	// Talking: TalkSender listens to panelview.connected (media_port MUST be an int).
	p.bus.Publish("panelview.connected", map[string]interface{}{
		"call_id": callID, "media_host": "127.0.0.1", "media_port": ans.Port, "srtp_key": key,
		"media_pt": ans.PayloadType, // negotiated PT (panel view speex); talking uses it in -payload_type
	})
	// Auto-off (inside the same critical section).
	p.timer = time.AfterFunc(p.timeout, func() { _ = p.Hangup() })
	p.mu.Unlock()
	p.logger.Infof("panelview: panel view open to entrance panel %s (auto-off %s)", p.cfg.DEVADDR, p.timeout)
	return nil
}

func (p *PanelView) release() { p.mu.Lock(); p.active = false; p.mu.Unlock() }

// Hangup closes the panel view. Idempotent teardown (no-op if already closed).
func (p *PanelView) Hangup() error { return p.teardown("panelview") }

// teardown tears the panel view down; reason travels in panelview.ended. Idempotent.
func (p *PanelView) teardown(reason string) error {
	p.mu.Lock()
	if !p.active {
		p.mu.Unlock()
		return nil
	}
	p.active = false
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.mu.Unlock()

	// Capture the call_id BEFORE the BYE: TalkSender.onEnded keeps it as
	// lastEndedCallID to break the orphaned-ffmpeg guard if the EventBus puts a
	// panelview.ended ahead of the panelview.connected of the same panel view.
	// With an empty call_id that guard would never match and talk-back would be orphaned.
	cid := ""
	if p.orig != nil {
		cid = p.orig.CallID()
	}
	if p.fwd != nil {
		p.fwd.Stop()
	}
	if p.orig != nil {
		if err := p.orig.Hangup(); err != nil {
			p.logger.WithError(err).Debug("panelview: BYE failed")
		}
	}
	p.bus.Publish("sip.call.ended", map[string]interface{}{"reason": "panelview"})
	p.bus.Publish("panelview.ended", map[string]interface{}{"reason": reason, "call_id": cid})
	p.logger.Infof("panelview: panel view closed (%s)", reason)
	return nil
}
