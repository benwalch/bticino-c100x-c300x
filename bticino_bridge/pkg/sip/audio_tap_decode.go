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

const defaultAudioTapTimeout = 60 * time.Second

// BuildTapDecodeSDP generates the SDP ffmpeg reads to decode the CLEARTEXT
// Speex RTP the tap forwards to listenHost:listenPort. No a=crypto (the
// internal 5000 leg is not encrypted, unlike the SRTP leg to the app).
func BuildTapDecodeSDP(listenHost string, listenPort int, payloadType int) string {
	return fmt.Sprintf(
		"v=0\r\no=- 0 0 IN IP4 %s\r\ns=Tap\r\nc=IN IP4 %s\r\nt=0 0\r\n"+
			"m=audio %d RTP/AVP %d\r\na=rtpmap:%d speex/8000\r\na=recvonly\r\n",
		listenHost, listenHost, listenPort, payloadType, payloadType,
	)
}

// AudioTapDecoder decodes the tap's Speex RTP to a WAV via ffmpeg.
type AudioTapDecoder struct {
	ffmpeg  string
	timeout time.Duration
	logger  *logrus.Logger
	mu      sync.Mutex
	cancel  context.CancelFunc
	cmd     *exec.Cmd
}

func NewAudioTapDecoder(ffmpegPath string, logger *logrus.Logger) *AudioTapDecoder {
	if logger == nil {
		logger = logrus.New()
	}
	return &AudioTapDecoder{ffmpeg: ffmpegPath, timeout: defaultAudioTapTimeout, logger: logger}
}

// SetTimeout extends (or shortens) the hard ffmpeg cap. The default is
// defaultAudioTapTimeout (60 s, a generic anti-DoS guard for one-off calls);
// callers that run longer sessions (e.g. AudioListenOnCall during a real
// call) extend it so the audio is not cut halfway. Only affects THIS
// instance.
func (d *AudioTapDecoder) SetTimeout(dur time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.timeout = dur
}

func (d *AudioTapDecoder) buildArgs(sdpPath, sinkPath string) []string {
	return []string{
		"-hide_banner", "-loglevel", "warning",
		"-protocol_whitelist", "file,rtp,udp",
		"-i", sdpPath,
		"-c:a", "pcm_s16le", "-ar", "8000", "-ac", "1",
		// ffmpeg buffers its output (AVIO buffer); without this the WAV ends up
		// with 0 bytes when the decoder is stopped by kill/cancel instead of a
		// clean exit. With -flush_packets 1, ffmpeg flushes after every packet,
		// so the WAV grows continuously and survives the teardown.
		"-flush_packets", "1",
		"-f", "wav", "-y", sinkPath,
	}
}

// Decode writes the SDP to a temp file, starts ffmpeg (hard timeout) and
// blocks until the end or Stop. The tap must forward to listenPort BEFORE or
// during this.
func (d *AudioTapDecoder) Decode(listenHost string, listenPort, payloadType int, sinkPath string) error {
	sdp := BuildTapDecodeSDP(listenHost, listenPort, payloadType)
	f, err := os.CreateTemp("", "tap-*.sdp")
	if err != nil {
		return fmt.Errorf("sdp temp: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(sdp); err != nil {
		f.Close()
		return fmt.Errorf("sdp write: %w", err)
	}
	f.Close()

	d.mu.Lock()
	timeout := d.timeout
	d.mu.Unlock()
	if timeout <= 0 {
		timeout = defaultAudioTapTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	d.mu.Lock()
	cmd := exec.CommandContext(ctx, d.ffmpeg, d.buildArgs(f.Name(), sinkPath)...)
	d.cmd = cmd
	d.cancel = cancel
	d.mu.Unlock()

	d.logger.Infof("audio-tap: decoding rtp %s:%d → %s (speex PT%d, limit %s)", listenHost, listenPort, sinkPath, payloadType, timeout)
	out, err := cmd.CombinedOutput()
	// DeadlineExceeded (hard end-of-session cap) and Canceled (Stop()) are
	// normal session ends, not real ffmpeg failures, so they are not logged
	// as errors. A genuine ffmpeg failure (ctx not yet expired or cancelled)
	// is logged.
	ctxErr := ctx.Err()
	if err != nil && ctxErr != context.Canceled && ctxErr != context.DeadlineExceeded {
		d.logger.WithError(err).Warnf("audio-tap: ffmpeg ended with an error: %s", string(out))
	}
	return err
}

func (d *AudioTapDecoder) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel != nil {
		d.cancel()
	}
	if d.cmd != nil && d.cmd.Process != nil {
		_ = d.cmd.Process.Kill()
	}
}
