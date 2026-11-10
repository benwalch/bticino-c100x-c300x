package sip

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const defaultAudioReceiverTimeout = 60 * time.Second

// AudioCapturer receives the unit's incoming SRTP audio and writes it to a sink.
// localHost/localPort = the bridge's LOCAL endpoint (the one in the offer),
// where the unit sends; ans provides the SRTP suite and key to decrypt.
type AudioCapturer interface {
	Capture(localHost string, localPort int, sinkPath string, ans AudioAnswer) error
	Stop()
}

// BuildInboundSDP generates the SDP ffmpeg reads to decode the incoming RTP
// (speex/8000) on the bridge's LOCAL port, decrypting with the answer's key.
func BuildInboundSDP(localHost string, localPort int, ans AudioAnswer) string {
	return fmt.Sprintf(
		"v=0\r\no=- 0 0 IN IP4 %s\r\ns=Listen\r\nc=IN IP4 %s\r\nt=0 0\r\n"+
			"m=audio %d RTP/SAVP %d\r\na=rtpmap:%d speex/8000\r\n"+
			"a=crypto:2 %s inline:%s\r\na=recvonly\r\n",
		localHost, localHost, localPort, ans.PayloadType, ans.PayloadType, ans.CryptoSuite, ans.CryptoKey,
	)
}

type AudioReceiver struct {
	ffmpeg  string
	timeout time.Duration
	logger  *logrus.Logger
	mu      sync.Mutex
	cancel  context.CancelFunc
	cmd     *exec.Cmd
}

func NewAudioReceiver(ffmpegPath string, logger *logrus.Logger) *AudioReceiver {
	if logger == nil {
		logger = logrus.New()
	}
	return &AudioReceiver{ffmpeg: ffmpegPath, timeout: defaultAudioReceiverTimeout, logger: logger}
}

func (r *AudioReceiver) buildArgs(sdpPath, sinkPath string) []string {
	return []string{
		"-hide_banner", "-loglevel", "warning",
		"-protocol_whitelist", "file,crypto,rtp,udp,srtp",
		"-i", sdpPath,
		"-c:a", "pcm_s16le", "-ar", "8000", "-ac", "1",
		"-f", "wav", "-y", sinkPath,
	}
}

// Capture writes the inbound SDP to a temp file, starts ffmpeg (with a hard
// timeout) and blocks until ffmpeg ends or Stop() cuts it. Errors are not fatal.
func (r *AudioReceiver) Capture(localHost string, localPort int, sinkPath string, ans AudioAnswer) error {
	sdp := BuildInboundSDP(localHost, localPort, ans)
	f, err := os.CreateTemp("", "inbound-*.sdp")
	if err != nil {
		return fmt.Errorf("sdp temp: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(sdp); err != nil {
		f.Close()
		return fmt.Errorf("sdp write: %w", err)
	}
	f.Close()

	r.logger.Infof("audio-receiver: listening on srtp %s:%d → %s (speex PT%d)", localHost, localPort, sinkPath, ans.PayloadType)
	return r.runFFmpeg(r.buildArgs(f.Name(), sinkPath))
}

// buildForwardArgs: like buildArgs, but the output is RTP to dstHostPort (speex copy),
// to feed the audio relay (go2rtc /doorbell) instead of writing a WAV.
func buildForwardArgs(sdpPath, dstHostPort string, ans AudioAnswer) []string {
	return []string{
		"-hide_banner", "-loglevel", "warning",
		"-protocol_whitelist", "file,crypto,rtp,udp,srtp",
		"-fflags", "nobuffer",
		"-i", sdpPath,
		"-c:a", "copy",
		"-f", "rtp",
		fmt.Sprintf("rtp://%s", dstHostPort),
	}
}

// CaptureForward decrypts the incoming SRTP (the entrance panel's key in ans) and
// forwards it as RTP to dstHostPort. It blocks until Stop() or the process ends.
// Same life cycle as Capture (ctx with timeout + Stop()).
func (r *AudioReceiver) CaptureForward(localHost string, localPort int, dstHostPort string, ans AudioAnswer) error {
	f, err := os.CreateTemp("", "bt-eye-in-*.sdp")
	if err != nil {
		return fmt.Errorf("sdp temp: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(BuildInboundSDP(localHost, localPort, ans)); err != nil {
		f.Close()
		return fmt.Errorf("sdp write: %w", err)
	}
	f.Close()

	r.logger.Infof("audio-receiver: forwarding srtp %s:%d → rtp://%s (speex PT%d)", localHost, localPort, dstHostPort, ans.PayloadType)
	return r.runFFmpeg(buildForwardArgs(f.Name(), dstHostPort, ans))
}

// runFFmpeg starts ffmpeg with a hard timeout and blocks until it ends or
// Stop() cuts it. Errors are not fatal (they are logged).
func (r *AudioReceiver) runFFmpeg(args []string) error {
	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultAudioReceiverTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	r.mu.Lock()
	cmd := exec.CommandContext(ctx, r.ffmpeg, args...)
	r.cmd = cmd
	r.cancel = cancel
	r.mu.Unlock()

	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() != context.Canceled {
		r.logger.WithError(err).Warnf("audio-receiver: ffmpeg ended with an error: %s", string(out))
	}
	return err
}

func (r *AudioReceiver) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
}
