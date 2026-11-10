package sip

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/events"
)

var (
	// ErrNotRinging is returned by Answer when no call is RINGING.
	ErrNotRinging = errors.New("no call in RINGING")
	// ErrNotInCall is returned by Hangup when no call is in progress.
	ErrNotInCall = errors.New("no call in progress")
	// errTransportClosed marks the transport closing (like io.EOF on the socket).
	errTransportClosed = errors.New("transport closed")
)

type answererState int

const (
	stIdle answererState = iota
	stRinging
	stAnswering
	stInCall
)

// AnswererTransport abstracts the SIP socket (allows a fake in tests). Recv blocks and returns
// a message or an error (errTransportClosed once closed).
type AnswererTransport interface {
	Send(raw string) error
	Recv() (*SIPMessage, error)
	LocalHost() string
	LocalPort() int
	Close() error
}

// Answerer is the SIP answerer state machine: it registers the AOR with the local Flexisip,
// takes the INVITE of the ring fork (100/180), and on the user's command answers (200 OK with
// our own SDP) or hangs up (BYE). A single read loop (run) dispatches incoming messages;
// Answer/Hangup change the state under the same mutex.
type Answerer struct {
	cfg    config.AnswererConfig
	domain string
	tr     AnswererTransport
	bus    events.EventBus
	logger *logrus.Logger
	now    func() time.Time // injectable for guard/branch tests

	mu         sync.Mutex
	state      answererState
	dialog     AnswererDialog // the current call (RINGING+)
	offer      string         // SDP offer of the current INVITE
	lastResp   string         // last response sent to the INVITE (to dedupe retransmissions)
	registered bool
	byeCSeq    int
	regCSeq    int
	guardTimer *time.Timer
	ringTimer  *time.Timer // ring guard: closes a RINGING without CANCEL with 480 (ring_timeout_seconds)
	drain      *mediaDrain // UDP drain of the direct SRTP media (best effort, see Answer())
	stopCh     chan struct{}
	stopped    bool
	branchSeq  int
	tagSeq     int
	regWG      sync.WaitGroup // syncs the re-REGISTER goroutine shutdown in Stop()
}

// answererRegisterExpires is the Expires announced in each REGISTER (seconds). The re-REGISTER
// fires at half this interval to refresh the binding before the registrar expires it.
const answererRegisterExpires = 3600

// NewAnswerer builds the answerer. The concrete transport (TCP to Flexisip) is passed in;
// tests inject a fake.
func NewAnswerer(cfg config.AnswererConfig, domain string, transport AnswererTransport, bus events.EventBus, logger *logrus.Logger) *Answerer {
	if logger == nil {
		logger = logrus.New()
	}
	return &Answerer{
		cfg: cfg, domain: domain, tr: transport, bus: bus, logger: logger,
		now: time.Now, state: stIdle, stopCh: make(chan struct{}),
	}
}

// Start starts the read loop, sends the initial REGISTER and subscribes to the bus commands.
func (a *Answerer) Start() error {
	if err := a.registerOnce(); err != nil {
		a.logger.Warnf("[ANSWERER] initial registration failed: %v (retried on the next re-REGISTER tick)", err)
	}
	if a.bus != nil {
		a.bus.Subscribe("doorcall.answer.request", func(*events.Event) { _ = a.Answer() })
		a.bus.Subscribe("doorcall.hangup.request", func(*events.Event) { _ = a.Hangup() })
	}
	go a.run()
	a.regWG.Add(1)
	go a.reregisterLoop()
	return nil
}

// reregisterLoop refreshes the AOR registration periodically (every expires/2) so the binding
// does not expire at the registrar. It stops cleanly when stopCh is closed (Stop()).
func (a *Answerer) reregisterLoop() {
	defer a.regWG.Done()
	interval := (answererRegisterExpires / 2) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			if err := a.registerOnce(); err != nil {
				a.logger.Warnf("[ANSWERER] re-REGISTER failed: %v", err)
			}
		}
	}
}

// run reads messages from the transport and dispatches them until the transport closes.
func (a *Answerer) run() {
	for {
		select {
		case <-a.stopCh:
			return
		default:
		}
		m, err := a.tr.Recv()
		if err != nil {
			if a.isStopped() {
				return
			}
			a.logger.Warnf("[ANSWERER] Recv error: %v", err)
			// No automatic reconnect: the initial TCP connection is the only one in the
			// Answerer's lifetime. If the local Flexisip dies or restarts, the registration is
			// lost and the bridge must be restarted to register again (out of scope for now).
			return
		}
		a.dispatch(m)
	}
}

// dispatch handles an incoming message under the mutex.
func (a *Answerer) dispatch(m *SIPMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch strings.ToUpper(m.Method) {
	case "INVITE":
		a.onInvite(m)
	case "CANCEL":
		a.onCancel(m)
	case "ACK":
		if a.state == stAnswering && a.sameDialog(m) {
			a.toInCall()
		}
	case "BYE":
		if a.sameDialog(m) && (a.state == stInCall || a.state == stAnswering) {
			// The 200 to a BYE must echo the BYE's own CSeq, not the INVITE's.
			byeDlg := a.dialog
			if c := m.Headers["CSeq"]; c != "" {
				byeDlg.CSeq = c
			}
			if err := a.tr.Send(BuildAnswererResponse("200", "OK", byeDlg, "", "")); err != nil {
				a.logger.Warnf("[ANSWERER] Send 200 to BYE failed: %v", err)
			}
			a.endCall("hangup_remote")
		}
	case "":
		// A response (to REGISTER/BYE/...) or a keepalive (bare CRLF, no CSeq). Only the
		// REGISTER response matters here: it confirms or withdraws the AOR registration. The
		// rest (200 to the BYE, etc.) needs no action in the read loop.
		// NOTE: dispatch already holds a.mu (defer above): set the field directly, NEVER
		// a.setRegistered() (it takes the lock itself: with a non-reentrant sync.Mutex,
		// calling it here would block this goroutine forever on every REGISTER 200).
		if cseq := m.Headers["CSeq"]; strings.Contains(cseq, "REGISTER") {
			a.registered = strings.HasPrefix(m.StatusCode, "2")
		}
	}
}

// onInvite handles an incoming INVITE. Assumes the mutex is held.
func (a *Answerer) onInvite(m *SIPMessage) {
	callID := m.Headers["Call-ID"]
	cseq := m.Headers["CSeq"]
	if a.state != stIdle {
		if callID == a.dialog.CallID && cseq == a.dialog.CSeq {
			// Retransmission (same Call-ID+CSeq): resend the last response, no state change.
			if a.lastResp != "" {
				if err := a.tr.Send(a.lastResp); err != nil {
					a.logger.Warnf("[ANSWERER] Send retransmission (resending the last response) failed: %v", err)
				}
			}
			return
		}
		// Another call while we are busy → 486 Busy Here (no state change).
		busy := a.dialogFromInvite(m)
		if err := a.tr.Send(BuildAnswererResponse("486", "Busy Here", busy, "", "")); err != nil {
			a.logger.Warnf("[ANSWERER] Send 486 failed: %v", err)
		}
		return
	}
	// New call: capture the dialog and answer 100/180.
	a.dialog = a.dialogFromInvite(m)
	a.offer = m.Body
	if err := a.tr.Send(BuildAnswererResponse("100", "Trying", a.dialog, "", "")); err != nil {
		a.logger.Warnf("[ANSWERER] Send 100 Trying failed: %v", err)
	}
	ringing := BuildAnswererResponse("180", "Ringing", a.dialog, "", "")
	if err := a.tr.Send(ringing); err != nil {
		a.logger.Warnf("[ANSWERER] Send 180 Ringing failed: %v", err)
	}
	a.lastResp = ringing
	a.state = stRinging
	// Ring guard: if nobody answers and the entrance panel's CANCEL is lost, we must not
	// stay RINGING (we would answer 486 to every later ring).
	if secs := a.cfg.RingTimeoutSeconds; secs > 0 {
		a.ringTimer = time.AfterFunc(time.Duration(secs)*time.Second, a.ringTimeout)
	}
	a.publish("doorcall.incoming", map[string]interface{}{
		"call_id":   callID,
		"call_type": ClassifyCallType(a.offer), // entrance / intercom / unknown
	})
}

// onCancel handles an incoming CANCEL. Assumes the mutex is held.
func (a *Answerer) onCancel(m *SIPMessage) {
	if a.state == stRinging && a.sameDialog(m) {
		// The 200 to a CANCEL must echo the CANCEL's own CSeq ("20 CANCEL") and Vias,
		// not those of the INVITE stored in the dialog.
		cancelDlg := a.dialog
		cancelDlg.Via = m.ViaHeaders
		if c := m.Headers["CSeq"]; c != "" {
			cancelDlg.CSeq = c
		}
		if err := a.tr.Send(BuildAnswererResponse("200", "OK", cancelDlg, "", "")); err != nil { // 200 to the CANCEL
			a.logger.Warnf("[ANSWERER] Send 200 to CANCEL failed: %v", err)
		}
		// The 487 STILL uses a.dialog (it is the response to the original INVITE, correct).
		if err := a.tr.Send(BuildAnswererResponse("487", "Request Terminated", a.dialog, "", "")); err != nil { // 487 to the INVITE
			a.logger.Warnf("[ANSWERER] Send 487 failed: %v", err)
		}
		a.endCall("cancelled")
	}
}

// Answer answers the RINGING call with a 200 OK and our own SDP. The ACK arrives via the loop.
func (a *Answerer) Answer() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != stRinging {
		return ErrNotRinging
	}
	key, err := GenerateSRTPKey()
	if err != nil {
		return err
	}
	vp := a.cfg.VideoPort
	if vp < 0 {
		vp = 0 // -1 = audio only: we do not announce m=video
	}
	sdp := BuildAnswerSDP("127.0.0.1", a.cfg.AudioPort, vp, key)
	contact := fmt.Sprintf("<sip:%s@%s:%d;transport=tcp>", a.cfg.User, a.tr.LocalHost(), a.tr.LocalPort())
	resp := BuildAnswererResponse("200", "OK", a.dialog, sdp, contact)
	// The 200 goes on the wire BEFORE the transition: if Send fails we stay RINGING so we can
	// retry or hang up, and return the error instead of announcing a false success.
	if err := a.tr.Send(resp); err != nil {
		a.logger.Warnf("[ANSWERER] Send 200 OK failed: %v", err)
		return err
	}
	a.lastResp = resp
	a.state = stAnswering
	a.stopRingTimer() // answered: the ring guard no longer applies
	// Busy-panel/no-ACK guard: armed in Answer (not in toInCall) so that a stuck
	// stAnswering (200 sent, ACK never arrives) also heals itself after
	// max_call_seconds. guardHangup already covers stAnswering and stInCall.
	if secs := a.cfg.MaxCallSeconds; secs > 0 {
		a.guardTimer = time.AfterFunc(time.Duration(secs)*time.Second, a.guardHangup)
	}
	// Best-effort media drain: if the port is already taken (e.g. between tests, or in
	// production if something holds it), do NOT fail the response already sent; just warn.
	if d, derr := startMediaDrain(a.cfg.AudioPort, vp, a.logger); derr != nil {
		a.logger.Warnf("[ANSWERER] drain unavailable: %v", derr)
	} else {
		a.drain = d
	}
	remotePort := 0
	if rp, perr := ParseRemoteAudioPort(a.offer); perr == nil {
		remotePort = rp
	} else {
		a.logger.Warnf("[ANSWERER] could not parse the offer's audio port (talking will not work): %v", perr)
	}
	a.publish("doorcall.answered", map[string]interface{}{
		"call_id":    a.dialog.CallID,
		"media_host": "127.0.0.1",
		"media_port": remotePort,
		"srtp_key":   key, // the key we announce in the 200 OK (encrypts what we SEND)
	})
	return nil
}

// toInCall confirms the call after the ACK. The guard was already armed in Answer (covers
// the no-ACK case), so this only makes the transition. Assumes the mutex is held.
func (a *Answerer) toInCall() {
	a.state = stInCall
}

// Hangup hangs up the current call by sending a BYE to the entrance panel.
func (a *Answerer) Hangup() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != stInCall && a.state != stAnswering {
		return ErrNotInCall
	}
	a.sendBYE()
	a.endCall("hangup_local")
	return nil
}

// guardHangup is fired by guardTimer in its own goroutine: it takes the mutex and hangs up if a call is still up.
func (a *Answerer) guardHangup() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != stInCall && a.state != stAnswering {
		return // already hung up another way: no double BYE
	}
	a.sendBYE()
	a.endCall("guard_timeout")
}

// ringTimeout is fired by ringTimer in its own goroutine. If the call is still RINGING
// (nobody answered and no CANCEL arrived), it closes the INVITE with 480 and returns to idle.
func (a *Answerer) ringTimeout() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped || a.state != stRinging {
		return // answerer stopped, or call already answered/cancelled/hung up another way
	}
	if err := a.tr.Send(BuildAnswererResponse("480", "Temporarily Unavailable", a.dialog, "", "")); err != nil {
		a.logger.Warnf("[ANSWERER] Send 480 (ring timeout) failed: %v", err)
	}
	a.endCall("ring_timeout")
}

// stopRingTimer disarms the ring guard if it is armed. Assumes the mutex is held.
func (a *Answerer) stopRingTimer() {
	if a.ringTimer != nil {
		a.ringTimer.Stop()
		a.ringTimer = nil
	}
}

// sendBYE builds and sends the in-dialog BYE. Assumes the mutex is held.
func (a *Answerer) sendBYE() {
	a.byeCSeq++
	branch := a.nextBranch()
	bye := BuildAnswererBYE(a.dialog, a.cfg.User, a.domain, a.tr.LocalHost(), a.tr.LocalPort(), a.byeCSeq, branch)
	if err := a.tr.Send(bye); err != nil {
		a.logger.Warnf("[ANSWERER] Send BYE failed: %v", err)
	}
}

// endCall clears the state after the call closes and publishes doorcall.ended. Assumes the mutex is held.
func (a *Answerer) endCall(reason string) {
	callID := a.dialog.CallID // capture BEFORE clearing the dialog, to correlate with doorcall.answered
	if a.guardTimer != nil {
		a.guardTimer.Stop()
		a.guardTimer = nil
	}
	a.stopRingTimer()
	if a.drain != nil {
		a.drain.Stop()
		a.drain = nil
	}
	a.state = stIdle
	a.dialog = AnswererDialog{}
	a.offer = ""
	a.lastResp = ""
	a.publish("doorcall.ended", map[string]interface{}{"reason": reason, "call_id": callID})
}

// dialogFromInvite builds an AnswererDialog from an incoming INVITE, with our own to-tag.
func (a *Answerer) dialogFromInvite(m *SIPMessage) AnswererDialog {
	return AnswererDialog{
		CallID:      m.Headers["Call-ID"],
		FromHdr:     m.Headers["From"],
		ToHdr:       m.Headers["To"],
		CSeq:        m.Headers["CSeq"],
		Via:         m.ViaHeaders,
		RecordRoute: m.RecordRoute,
		ToTag:       a.newTag(),
	}
}

// sameDialog reports whether the message belongs to the current dialog (by Call-ID).
func (a *Answerer) sameDialog(m *SIPMessage) bool {
	id := m.Headers["Call-ID"]
	return id != "" && id == a.dialog.CallID
}

// registerOnce sends a REGISTER for the answerer's AOR. The read loop confirms the 200
// (dispatch, case "") by setting a.registered directly under the mutex it already holds.
func (a *Answerer) registerOnce() error {
	a.mu.Lock()
	a.regCSeq++
	cseq := a.regCSeq
	branch := a.nextBranch()
	callID := fmt.Sprintf("reg-%d@%s", a.now().UnixNano(), a.tr.LocalHost())
	fromTag := fmt.Sprintf("t%d", a.now().UnixNano())
	a.mu.Unlock()
	reg := BuildAnswererRegister(a.cfg.User, a.domain, a.tr.LocalHost(), a.tr.LocalPort(), answererRegisterExpires, branch, callID, fromTag, cseq)
	return a.tr.Send(reg)
}

// Stop hangs up if a call is up, marks the stop and closes the transport.
func (a *Answerer) Stop() error {
	a.mu.Lock()
	if a.state == stInCall || a.state == stAnswering {
		a.sendBYE()
		a.endCall("hangup_local")
	}
	a.stopRingTimer() // a pending ring guard must not fire after Stop
	if !a.stopped {
		a.stopped = true
		close(a.stopCh)
	}
	a.mu.Unlock()
	a.regWG.Wait() // wait for reregisterLoop to exit cleanly before closing the transport
	return a.tr.Close()
}

// StateString returns the answerer's public state.
func (a *Answerer) StateString() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch a.state {
	case stRinging:
		return "ringing"
	case stAnswering, stInCall:
		return "in_call"
	default:
		return "idle"
	}
}

// IsRegistered reports whether the AOR is registered (dispatch sets it on the REGISTER 200).
func (a *Answerer) IsRegistered() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.registered
}

func (a *Answerer) isStopped() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopped
}

// nextBranch generates a unique RFC 3261 branch. Assumes the mutex is held.
func (a *Answerer) nextBranch() string {
	a.branchSeq++
	return fmt.Sprintf("z9hG4bK%d%d", a.now().UnixNano(), a.branchSeq)
}

// newTag generates a unique tag for our own To. Assumes the mutex is held.
func (a *Answerer) newTag() string {
	a.tagSeq++
	return fmt.Sprintf("bt%d%d", a.now().UnixNano(), a.tagSeq)
}

// publish emits an event on the bus if there is a bus.
func (a *Answerer) publish(name string, data interface{}) {
	if a.bus != nil {
		a.bus.Publish(name, data)
	}
}
