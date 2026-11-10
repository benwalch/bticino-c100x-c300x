package sip

import (
	"fmt"
	"os"
	"sync"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/events"
)

// Proc wraps a running external process (real ffmpeg or a fake in tests).
type Proc interface {
	Kill() error
	Wait() error
}

// ProcRunner wraps starting an external process, injectable for offline tests.
type ProcRunner interface {
	Start(name string, args []string) (Proc, error)
}

// TalkSender starts and kills an ffmpeg that sends the HA mic audio (local backchannel on
// bcHost:bcPort) to the entrance panel as SRTP, driven by doorcall.answered/doorcall.ended.
// A runner failure must NEVER take the call down: it is logged and ignored.
type TalkSender struct {
	cfg        config.AnswererConfig
	runner     ProcRunner
	bus        events.EventBus
	logger     *logrus.Logger
	ffmpegPath string
	bcHost     string
	bcPort     int
	bcPT       int

	mu              sync.Mutex
	proc            Proc
	keepalive       *upstreamKeepalive // continuous upstream relay (only with cfg.TalkKeepalive)
	sdpPath         string
	stopped         bool
	lastEndedCallID string // last call_id seen in doorcall.ended; avoids an orphaned ffmpeg if
	// ended arrives BEFORE answered for the same call-id (EventBus goroutine order).
}

// NewTalkSender builds the sender. backchannelHost/Port/PT describe where ffmpeg must listen
// for the local mic audio (the go2rtc end), not the SRTP target at the entrance panel.
func NewTalkSender(cfg config.AnswererConfig, runner ProcRunner, bus events.EventBus, logger *logrus.Logger, ffmpegPath, backchannelHost string, backchannelPort, backchannelPT int) *TalkSender {
	return &TalkSender{
		cfg:        cfg,
		runner:     runner,
		bus:        bus,
		logger:     logger,
		ffmpegPath: ffmpegPath,
		bcHost:     backchannelHost,
		bcPort:     backchannelPort,
		bcPT:       backchannelPT,
	}
}

// Start subscribes to doorcall.answered (starts ffmpeg) and doorcall.ended (kills it).
func (s *TalkSender) Start() error {
	s.bus.Subscribe("doorcall.answered", func(e *events.Event) { s.onAnswered(e) })
	s.bus.Subscribe("doorcall.ended", func(e *events.Event) { s.onEnded(e) })
	// Panel view mode: same payload as doorcall.*, so the handlers are reused.
	s.bus.Subscribe("panelview.connected", func(e *events.Event) { s.onAnswered(e) })
	s.bus.Subscribe("panelview.ended", func(e *events.Event) { s.onEnded(e) })
	return nil
}

// onAnswered starts the talk-back ffmpeg if TalkEnabled and the payload has a valid
// media_port/srtp_key. Any failure (temp file, runner) is logged and ignored: the call goes on without talk.
func (s *TalkSender) onAnswered(e *events.Event) {
	if !s.cfg.TalkEnabled {
		return
	}
	data, _ := e.Data.(map[string]interface{})
	port, _ := data["media_port"].(int)
	key, _ := data["srtp_key"].(string)
	host, _ := data["media_host"].(string)
	callID, _ := data["call_id"].(string)
	// media_pt is optional: only the panel view sends it. Doorcall does not → 96.
	pt, _ := data["media_pt"].(int)
	if pt <= 0 {
		pt = 96
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if port <= 0 || key == "" {
		s.logger.Warnf("[TALK] doorcall.answered without a valid media_port/srtp_key: no talk")
		return
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	// EventBus reordering race: if the doorcall.ended of THIS call already arrived (the onEnded
	// goroutine got the mutex first), do not start an orphaned ffmpeg that no later ended would
	// kill. A NEW call has another call-id and is not affected.
	if callID != "" && callID == s.lastEndedCallID {
		s.mu.Unlock()
		s.logger.Infof("[TALK] call %s had already ended; talk-back not started", callID)
		return
	}
	if s.proc != nil { // just in case: kill a previous sender (should not happen, one call at a time)
		s.killLocked()
	}

	// With talk_keepalive, a relay listens to the go2rtc backchannel and sends a continuous
	// stream (voice or silence) to bcPort+2, which is what ffmpeg reads. If the relay does not
	// start, it falls back to the classic behavior (ffmpeg reads the backchannel directly):
	// talking without keepalive beats not talking.
	sdpPort := s.bcPort
	if s.cfg.TalkKeepalive {
		k, kerr := startUpstreamKeepalive(s.bcHost, s.bcPort, s.bcPort+2, s.bcPT, s.logger)
		if kerr != nil {
			s.logger.Warnf("[TALK] keepalive unavailable (%v): talk-back without continuous upstream", kerr)
		} else {
			s.keepalive = k
			sdpPort = s.bcPort + 2
		}
	}

	// Write the backchannel SDP (where ffmpeg reads the local mic audio) to a temp file.
	sdp := BuildBackchannelSDP(sdpPort, s.bcPT)
	f, err := os.CreateTemp("", "bt-talk-*.sdp")
	if err != nil {
		s.killLocked() // stops the relay if it started
		s.mu.Unlock()
		s.logger.Warnf("[TALK] cannot create the temp SDP: %v; no talk", err)
		return
	}
	if _, werr := f.WriteString(sdp); werr != nil {
		f.Close()
		os.Remove(f.Name())
		s.killLocked() // stops the relay if it started
		s.mu.Unlock()
		s.logger.Warnf("[TALK] cannot write the temp SDP: %v; no talk", werr)
		return
	}
	f.Close()

	args := buildFFmpegArgs(f.Name(), key, host, port, pt)
	proc, err := s.runner.Start(s.ffmpegPath, args)
	if err != nil {
		os.Remove(f.Name())
		s.killLocked() // stops the relay if it started
		s.mu.Unlock()
		s.logger.Warnf("[TALK] cannot start ffmpeg (%v); the call goes on without talk-back", err)
		return
	}
	s.proc = proc
	s.sdpPath = f.Name()
	s.mu.Unlock()

	s.logger.Infof("[TALK] talk-back ffmpeg started → srtp://%s:%d", host, port)
	// Wait() blocks until the process ends: NEVER with s.mu held.
	go func() {
		werr := proc.Wait()
		s.logger.Infof("[TALK] talk-back ffmpeg ended: %v", werr)
	}()
}

// onEnded kills the running ffmpeg (if any) and removes the temp file. It records the call_id
// BEFORE killing and cleaning, so a doorcall.answered of the SAME call-id that arrives later
// (reordered by the EventBus) knows the call has ended and starts no orphaned ffmpeg.
func (s *TalkSender) onEnded(e *events.Event) {
	data, _ := e.Data.(map[string]interface{})
	cid, _ := data["call_id"].(string)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastEndedCallID = cid
	s.killLocked()
}

// killLocked kills the process and removes the temp file. Assumes s.mu is held. Killing twice is a no-op.
func (s *TalkSender) killLocked() {
	if s.proc != nil {
		s.proc.Kill()
		s.proc = nil
	}
	if s.keepalive != nil {
		s.keepalive.Stop()
		s.keepalive = nil
	}
	if s.sdpPath != "" {
		os.Remove(s.sdpPath)
		s.sdpPath = ""
	}
}

// Stop kills ffmpeg if it is still alive and marks the sender as stopped; idempotent.
func (s *TalkSender) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.killLocked()
}

// buildFFmpegArgs returns the arguments (without the binary) for the talk-back sender.
func buildFFmpegArgs(sdpPath, srtpKeyB64, dstHost string, dstPort, outPT int) []string {
	return []string{
		"-hide_banner", "-loglevel", "warning",
		"-protocol_whitelist", "file,udp,rtp",
		"-fflags", "nobuffer",
		"-i", sdpPath,
		"-acodec", "copy",
		"-payload_type", fmt.Sprintf("%d", outPT), // 96 (the speex PT of our answer)
		"-srtp_out_suite", "AES_CM_128_HMAC_SHA1_80",
		"-srtp_out_params", srtpKeyB64,
		"-f", "rtp",
		fmt.Sprintf("srtp://%s:%d", dstHost, dstPort),
	}
}
