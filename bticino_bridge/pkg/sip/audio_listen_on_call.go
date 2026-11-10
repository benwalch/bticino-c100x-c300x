package sip

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// AudioDecoder abstracts the blocking decoding of the RTP forwarded by the
// tap (AudioTapDecoder in production). Decode BLOCKS until the process ends
// by itself (internal hard timeout) or Stop cancels it.
type AudioDecoder interface {
	Decode(listenHost string, listenPort, payloadType int, sinkPath string) error
	Stop()
}

// audioDecoderTimeoutSetter is an OPTIONAL interface: if the injected
// AudioDecoder implements it (like *AudioTapDecoder), AudioListenOnCall raises
// its default hard limit (60s in AudioTapDecoder, meant as DoS protection for
// one-off calls) so audio is not cut in the middle of a longer real call. A
// decoder that does not implement it is simply not affected.
type audioDecoderTimeoutSetter interface {
	SetTimeout(d time.Duration)
}

// defaultAudioListenDecoderTimeout is the limit applied to the decoder when no
// guard is set (SetGuardTimeout). With a guard set, that value is used, since
// it is the real session limit.
const defaultAudioListenDecoderTimeout = 10 * time.Minute

// AudioListenOnCall sets up the intercom's "ear" during a real call with a
// NETWORK part: a passive mirror of the native audio leg (lo:5000, plain Speex
// RTP, see NativeAudioRTPPort) forwarded to a local relay port that an
// AudioDecoder decodes to WAV. Same lifecycle as LiveVideoOnCall (armed and
// disarmed on sip.call.connected/ended, wired in cmd/main.go), but it also runs
// a blocking decoder in its own goroutine.
// ZERO bus commands: like the video, it is a purely passive mirror.
type AudioListenOnCall struct {
	tap         VideoTap // reuses the minimal Start()/Stop() interface of the video
	decoder     AudioDecoder
	relayPort   int
	payloadType int
	sinkPath    string
	logger      *logrus.Logger

	mu         sync.Mutex
	active     bool
	generation uint64        // incremented on each Start; identifies the current session
	guard      time.Duration // 0 = no guard
	timer      *time.Timer
}

// SetGuardTimeout enables an automatic safety disarm: if the end of the call
// never arrives, Stop runs by itself d after Start. 0 disables the guard. Call
// before Start. Same behaviour as LiveVideoOnCall.
func (a *AudioListenOnCall) SetGuardTimeout(d time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.guard = d
}

// NewAudioListenOnCall creates the orchestrator. relayPort is the local port
// (127.0.0.1) the tap forwards to and the decoder listens on; payloadType is
// the RTP PT of the native Speex audio (110 in the firmware).
func NewAudioListenOnCall(tap VideoTap, decoder AudioDecoder, relayPort, payloadType int, sinkPath string, logger *logrus.Logger) *AudioListenOnCall {
	if logger == nil {
		logger = logrus.New()
	}
	return &AudioListenOnCall{
		tap:         tap,
		decoder:     decoder,
		relayPort:   relayPort,
		payloadType: payloadType,
		sinkPath:    sinkPath,
		logger:      logger,
	}
}

// Start arms the ear. Idempotent: does nothing if already active.
// ORDER: first the decoder (in a goroutine; Decode blocks listening on
// 127.0.0.1:relayPort), then the tap (which forwards lo:5000 →
// 127.0.0.1:relayPort), so the decoder is already listening when packets
// start to arrive. A tap failure degrades to no audio but keeps the session
// armed, like LiveVideoOnCall with the video.
func (a *AudioListenOnCall) Start(reason string) {
	a.mu.Lock()
	if a.active {
		a.mu.Unlock()
		return
	}
	a.active = true
	a.generation++
	gen := a.generation
	guard := a.guard
	if guard > 0 {
		a.timer = time.AfterFunc(guard, func() {
			a.logger.Warn("audio-listen: guard expired without end of call, disarming")
			a.Stop()
		})
	}
	a.mu.Unlock()

	a.logger.Infof("audio-listen: arming ear (reason=%s)", reason)

	if a.decoder != nil {
		if setter, ok := a.decoder.(audioDecoderTimeoutSetter); ok {
			timeout := defaultAudioListenDecoderTimeout
			if guard > 0 {
				timeout = guard
			}
			setter.SetTimeout(timeout)
		}
		go func() {
			if err := a.decoder.Decode("127.0.0.1", a.relayPort, a.payloadType, a.sinkPath); err != nil {
				a.logger.WithError(err).Debug("audio-listen: decoder ended")
			}
			// The decoder can return by itself (its own hard limit, raised above
			// but not infinite, or another reason) WITHOUT anyone calling Stop().
			// If we do not disarm here, IsActive() keeps saying true while the tap
			// forwards RTP to a socket nobody reads: a silent, false state.
			// selfStopIfCurrent only acts if `gen` is still the active session:
			// it keeps this goroutine, if late, from disarming a later session
			// by mistake (a Stop()+Start() would already have replaced it).
			a.selfStopIfCurrent(gen)
		}()
	}
	if a.tap != nil {
		if err := a.tap.Start(); err != nil {
			a.logger.WithError(err).Warn("audio-listen: tap unavailable, continuing without audio")
		} else {
			// tap.Start runs outside the lock because it can block. A Stop()
			// in that window found no tap to stop, so stop it here unless a
			// session owns it. Check `active`, not `generation`: tap and
			// decoder are shared, and a newer active session owns them now.
			a.mu.Lock()
			orphaned := !a.active
			a.mu.Unlock()
			if orphaned {
				a.logger.Warn("audio-listen: Stop() won the race against tap.Start() with no session claiming it; disarming the new tap so it does not leak")
				a.tap.Stop()
				if a.decoder != nil {
					a.decoder.Stop()
				}
			}
		}
	}
}

// beginTeardown decides, UNDER LOCK, whether to disarm, and if so sets
// active=false ATOMICALLY with that decision (together with the generation
// check, if checkGen). This closes the race window between "check" and
// "disarm": splitting them (check under one lock, disarm under another later)
// lets a full Stop()+Start() slip into the gap, and a late goroutine that had
// ALREADY passed its check would disarm the NEW session (the same bug that
// `generation` was meant to prevent, only with the opposite interleaving).
// With the decision and the flip in one critical section: if the concurrent
// Start() has not run yet, this call wins the race and stops the OLD session
// (correct); if it already ran and advanced `generation`, the generation check
// fails right here, with no gap to slip into (correct). It returns the timer
// to cancel (outside the lock), if any.
func (a *AudioListenOnCall) beginTeardown(checkGen bool, gen uint64) (proceed bool, timer *time.Timer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active || (checkGen && a.generation != gen) {
		return false, nil
	}
	a.active = false
	t := a.timer
	a.timer = nil
	return true, t
}

// Stop disarms (stops the tap and the decoder). Idempotent. Reverse order of
// Start: first the tap (stops forwarding), then the decoder (which unblocks
// its running Decode).
func (a *AudioListenOnCall) Stop() {
	proceed, timer := a.beginTeardown(false, 0)
	if !proceed {
		return
	}
	a.teardown(timer)
}

// selfStopIfCurrent disarms the session ONLY if `gen` is still the active
// generation (see the `generation` field, incremented on each Start). The
// decoder goroutine calls it when Decode() returns by itself. Without this
// check (and without the check being ATOMIC with the flip of `active`, see
// beginTeardown), a "late" goroutine of a session already stopped from
// outside could disarm a later session started meanwhile (quick re-Start
// after Stop).
func (a *AudioListenOnCall) selfStopIfCurrent(gen uint64) {
	proceed, timer := a.beginTeardown(true, gen)
	if !proceed {
		return
	}
	a.teardown(timer)
}

// teardown stops the timer/tap/decoder WITHOUT holding the lock (so other
// callers are not blocked while tap.Stop()/decoder.Stop(), which can take a
// while, run). Only call it after a beginTeardown that returned proceed=true
// (that is, with active ALREADY set to false).
func (a *AudioListenOnCall) teardown(timer *time.Timer) {
	if timer != nil {
		timer.Stop()
	}
	if a.tap != nil {
		a.tap.Stop()
	}
	if a.decoder != nil {
		a.decoder.Stop()
	}
	a.logger.Info("audio-listen: disarmed")
}

// IsActive reports whether the ear is armed.
func (a *AudioListenOnCall) IsActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active
}
