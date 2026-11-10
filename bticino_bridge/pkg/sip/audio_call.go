package sip

import (
	"errors"
	"fmt"
	"sync"

	"bticino_bridge/pkg/bticino"

	"github.com/sirupsen/logrus"
)

// CallOriginator places an INVITE with an SDP and can close the dialog (Hangup
// exists on *BTicinoSIPClient: it sends a BYE with the current dialog's Call-ID/tag).
type CallOriginator interface {
	InviteWithSDP(target, sdp string) (answerSDP string, err error)
	Hangup() error
}

type OWNSender interface{ SendOpenWebNetCommand(cmd string) error }

type AudioPlayer interface {
	Play(wavPath, host string, ans AudioAnswer) error
	Stop()
}

// ErrAudioLegBusy: an audio leg is already active (coexistence: a single leg).
var ErrAudioLegBusy = errors.New("audio: an audio leg is already active")

type AudioCall struct {
	orig      CallOriginator
	own       OWNSender
	player    AudioPlayer
	capturer  AudioCapturer
	localIP   string
	audioPort int
	logger    *logrus.Logger

	mu     sync.Mutex
	active bool
}

func NewAudioCall(orig CallOriginator, own OWNSender, player AudioPlayer, capturer AudioCapturer, localIP string, audioPort int, logger *logrus.Logger) *AudioCall {
	if logger == nil {
		logger = logrus.New()
	}
	return &AudioCall{orig: orig, own: own, player: player, capturer: capturer, localIP: localIP, audioPort: audioPort, logger: logger}
}

// Say (talk-back v2, compat): a send-only leg that plays a WAV at the door.
// It takes the single leg (ErrAudioLegBusy) and ALWAYS hangs up at the end:
// before, the dialog stayed alive until the native side cut it, and the SIP
// client's watcher held the webrtc socket (re-REGISTER deferred) all that time.
func (a *AudioCall) Say(wavPath string) error {
	a.mu.Lock()
	if a.active {
		a.mu.Unlock()
		return ErrAudioLegBusy
	}
	a.active = true
	a.mu.Unlock()
	defer a.releaseActive()

	key, err := NewSRTPMasterKey()
	if err != nil {
		return fmt.Errorf("SRTP key: %w", err)
	}
	offer := BuildAudioOfferSDP(a.localIP, a.audioPort, key)
	answerSDP, err := a.orig.InviteWithSDP(bticino.SIPUsernameForVariant(bticino.DeviceVariant), offer)
	if err != nil {
		return fmt.Errorf("INVITE: %w", err)
	}
	// A dialog is alive (200 OK): hang up on the way out, whatever happens.
	defer func() {
		if herr := a.orig.Hangup(); herr != nil {
			a.logger.WithError(herr).Debug("audio-call: BYE at the end of Say")
		}
	}()
	ans, err := ParseAudioAnswer(answerSDP)
	if err != nil {
		return fmt.Errorf("parse answer SDP: %w", err)
	}
	if a.own != nil {
		if err := a.own.SendOpenWebNetCommand(bticino.CmdAnswerPickup); err != nil {
			a.logger.WithError(err).Debug("audio-call: WHO=8 arm without ACK (continuing)")
		}
	}
	return a.player.Play(wavPath, "127.0.0.1", ans)
}

// OpenDuplex opens the full-duplex leg: INVITE c300x → answer → arm WHO=8 →
// RECEIVER (always, → sinkPath) and SENDER (if talkWav != "") in parallel.
// It returns the negotiated AudioAnswer. A single active leg (ErrAudioLegBusy).
func (a *AudioCall) OpenDuplex(sinkPath, talkWav string) (AudioAnswer, error) {
	// Reserve the leg in the SAME critical section as the check (avoids TOCTOU:
	// two concurrent calls must not both pass, and a concurrent Close() must
	// not see active=false while the INVITE is in flight).
	a.mu.Lock()
	if a.active {
		a.mu.Unlock()
		return AudioAnswer{}, ErrAudioLegBusy
	}
	a.active = true
	a.mu.Unlock()

	key, err := NewSRTPMasterKey()
	if err != nil {
		a.releaseActive()
		return AudioAnswer{}, fmt.Errorf("SRTP key: %w", err)
	}
	offer := BuildAudioOfferSDP(a.localIP, a.audioPort, key)
	answerSDP, err := a.orig.InviteWithSDP(bticino.SIPUsernameForVariant(bticino.DeviceVariant), offer)
	if err != nil {
		a.releaseActive()
		return AudioAnswer{}, fmt.Errorf("INVITE: %w", err)
	}
	ans, err := ParseAudioAnswer(answerSDP)
	if err != nil {
		// The INVITE reached 200 OK (a SIP dialog is alive) but the answer could
		// not be parsed: hang up so no orphaned dialog stays on the device
		// (non-interference rule).
		if a.orig != nil {
			if herr := a.orig.Hangup(); herr != nil {
				a.logger.WithError(herr).Debug("audio-call: BYE after the answer failed to parse")
			}
		}
		a.releaseActive()
		return AudioAnswer{}, fmt.Errorf("parse answer SDP: %w", err)
	}
	if a.own != nil {
		if err := a.own.SendOpenWebNetCommand(bticino.CmdAnswerPickup); err != nil {
			a.logger.WithError(err).Debug("audio-call: WHO=8 arm without ACK (continuing)")
		}
	}

	// The receiver always runs; it blocks → goroutine. Close() cuts it, but it
	// can also return by itself (60 s hard timeout in Capture, or ffmpeg died
	// on its own). Then nobody else frees the leg, so it is closed here too;
	// otherwise it stays stuck (active=true forever, an orphaned SIP dialog,
	// and every later OpenDuplex returns ErrAudioLegBusy until the next manual
	// /stop). Close() is idempotent (guarded by the active flag under a.mu), so:
	//   - normal /stop: Close() already set active=false and called
	//     capturer.Stop() (which kills ffmpeg) BEFORE this Capture() returns;
	//     when it returns, this Close() sees active=false and is a no-op.
	//   - natural end (timeout/ffmpeg died): Capture() returns by itself, this
	//     Close() sees it first and does active=false + player.Stop() +
	//     Hangup() (BYE).
	// Only for the receiver (the continuous "listen" direction): the sender
	// (Say/Play) is a one-shot that must not take the leg down when it ends.
	go func() {
		if err := a.capturer.Capture(a.localIP, a.audioPort, sinkPath, ans); err != nil {
			a.logger.WithError(err).Debug("audio-call: receiver ended")
		}
		if err := a.Close(); err != nil {
			a.logger.WithError(err).Debug("audio-call: Close after the receiver ended")
		}
	}()
	// Optional sender.
	if talkWav != "" {
		go func() {
			if err := a.player.Play(talkWav, "127.0.0.1", ans); err != nil {
				a.logger.WithError(err).Debug("audio-call: sender ended")
			}
		}()
	}
	a.logger.Info("audio-call: full-duplex leg open")
	return ans, nil
}

// IsActive reports whether the full-duplex leg is still active. It is the
// source of truth for /api/audio/duplex/status: the leg can close by itself
// (natural end of the receiver, see OpenDuplex) without /stop, so a mirror
// flag in the webserver would go stale after that close.
func (a *AudioCall) IsActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active
}

// releaseActive frees the active leg reservation (rollback on an error after
// the check-and-set in OpenDuplex).
func (a *AudioCall) releaseActive() {
	a.mu.Lock()
	a.active = false
	a.mu.Unlock()
}

// Close stops receiver+sender, sends a BYE and frees the leg. Idempotent.
func (a *AudioCall) Close() error {
	a.mu.Lock()
	if !a.active {
		a.mu.Unlock()
		return nil
	}
	a.active = false
	a.mu.Unlock()

	if a.capturer != nil {
		a.capturer.Stop()
	}
	if a.player != nil {
		a.player.Stop()
	}
	if a.orig != nil {
		if err := a.orig.Hangup(); err != nil {
			a.logger.WithError(err).Debug("audio-call: BYE failed")
		}
	}
	a.logger.Info("audio-call: full-duplex leg closed")
	return nil
}
