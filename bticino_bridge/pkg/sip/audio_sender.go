package sip

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// defaultAudioSenderTimeout caps the ffmpeg run time. It keeps a WAV that
// never ends (e.g. /dev/zero, a pipe) from hanging Play or the HTTP handler
// forever (anti-DoS). A normal talk-back WAV lasts a few seconds.
const defaultAudioSenderTimeout = 30 * time.Second

// AudioSender sends a WAV as speex/8000 SRTP to the unit's audio port,
// leaving the SRTP layer to ffmpeg (srtp protocol + libspeex). Its own SSRC
// (ffmpeg picks a random one) in a legitimate session, so the unit renders it.
type AudioSender struct {
	ffmpeg  string
	timeout time.Duration
	logger  *logrus.Logger
	mu      sync.Mutex
	cancel  context.CancelFunc
	cmd     *exec.Cmd
}

func NewAudioSender(ffmpegPath string, logger *logrus.Logger) *AudioSender {
	if logger == nil {
		logger = logrus.New()
	}
	return &AudioSender{ffmpeg: ffmpegPath, timeout: defaultAudioSenderTimeout, logger: logger}
}

// buildArgs builds the ffmpeg arguments (testable helper, runs nothing).
func (s *AudioSender) buildArgs(wavPath, host string, ans AudioAnswer) []string {
	url := fmt.Sprintf("srtp://%s:%d?srtp_out_suite=%s&srtp_out_params=%s",
		host, ans.Port, ans.CryptoSuite, ans.CryptoKey)
	return []string{
		"-hide_banner", "-loglevel", "warning", "-re",
		"-i", wavPath,
		"-c:a", "libspeex", "-ar", "8000", "-ac", "1",
		"-payload_type", fmt.Sprintf("%d", ans.PayloadType),
		"-f", "rtp", url,
	}
}

// Play starts ffmpeg (with a hard timeout) and blocks until the WAV ends or the
// cap is reached. Errors are not fatal. The timeout ensures Play returns even
// if the source never ends (anti-DoS for the calling handler).
func (s *AudioSender) Play(wavPath, host string, ans AudioAnswer) error {
	timeout := s.timeout
	if timeout <= 0 {
		timeout = defaultAudioSenderTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	s.mu.Lock()
	cmd := exec.CommandContext(ctx, s.ffmpeg, s.buildArgs(wavPath, host, ans)...)
	s.cmd = cmd
	s.cancel = cancel
	s.mu.Unlock()

	s.logger.Infof("audio-sender: sending %s → srtp %s:%d (speex PT%d, limit %s)", wavPath, host, ans.Port, ans.PayloadType, timeout)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.logger.WithError(err).Warnf("audio-sender: ffmpeg ended with an error: %s", string(out))
	}
	return err
}

// Stop kills the running ffmpeg, if any (cancels its context).
func (s *AudioSender) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}
