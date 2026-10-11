package sip

import (
	"context"
	"fmt"
	"os/exec"
	"sync"

	"github.com/sirupsen/logrus"
)

// TalkbackInjector sends upstream audio (talk-back) to the c300x door station.
//
// Mechanism (reverse-engineered 2026-07-09; see _investigacion/docs/
// TALKBACK_AUDIO_ASCENDENTE_2026-07-09.md): the call audio does NOT travel over
// real SRTP — the c300x uses a "dummy" crypto key in the SDP, so the media
// is PLAIN RTP. Talk-back consists of sending RTP Speex/8000 (payload type
// 110, the c300x audio codec) to the door station's audio receive port,
// with the bidirectional audio session ARMED via the OpenWebNet command
//   *8*2#5#4*16##  (CmdAnswerPickup — "enable bidirectional audio").
//
// This component handles ONLY the media pipeline (GStreamer). The arming
// command is sent by the caller (webserver / bridge) through the OWN client.
//
// Pipeline on the device (has speexenc + rtpspeexpay; does NOT have audiotestsrc):
//
//	filesrc/multifilesrc → decodebin → audioconvert → audioresample →
//	audio/x-raw,rate=8000,channels=1 → speexenc → rtpspeexpay pt=110 → udpsink
//
// The door station's receive port is SDP-negotiated per session (dynamic), so
// Start() takes it as a parameter: during an active call the
// observed port is tried (see the doc). It is a tool for closing the RE loop of
// talk-back — not a production path yet.
type TalkbackInjector struct {
	logger *logrus.Logger
	mu     sync.Mutex
	cmd    *exec.Cmd
	cancel context.CancelFunc
	active bool
	target string // "host:port" in progress (informational)
}

// NewTalkbackInjector creates the injector.
func NewTalkbackInjector(logger *logrus.Logger) *TalkbackInjector {
	return &TalkbackInjector{logger: logger}
}

// Start launches the upload pipeline towards host:port using `source` (path to a
// decodable audio file: WAV, etc.). If loop is true, the file is
// repeated continuously (multifilesrc). Arming bidirectional audio
// is NOT done here (the caller does it with the OWN command).
//
// pt is the RTP payload type of Speex/8000. IMPORTANT: the c300x advertises
// `a=rtpmap:98 speex/8000` in the audio SDP, so the correct PT is 98
// (if pt<=0, 98 is used). A different PT (e.g. 110) makes the door station IGNORE
// the stream — confirmed in the live test on 2026-07-09.
func (t *TalkbackInjector) Start(host string, port, pt int, source string, loop bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.active {
		return fmt.Errorf("talkback already active towards %s", t.target)
	}
	if source == "" {
		return fmt.Errorf("talkback: missing audio source (path to a WAV file)")
	}
	if port <= 0 {
		return fmt.Errorf("talkback: invalid destination port (%d)", port)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	if pt <= 0 {
		pt = 98 // c300x: a=rtpmap:98 speex/8000
	}

	var src []string
	if loop {
		src = []string{"multifilesrc", "location=" + source, "loop=true"}
	} else {
		src = []string{"filesrc", "location=" + source}
	}
	args := append([]string{}, src...)
	args = append(args,
		// The device has a minimal gstreamer (WITHOUT decodebin) → wavparse is used.
		// Therefore `source` must be a PCM WAV. Looping with multifilesrc is
		// best-effort (wavparse may not re-chain headers cleanly).
		"!", "wavparse",
		"!", "audioconvert",
		"!", "audioresample",
		"!", "audio/x-raw,rate=8000,channels=1",
		"!", "speexenc",
		"!", "rtpspeexpay", fmt.Sprintf("pt=%d", pt),
		"!", "udpsink", "host="+host, fmt.Sprintf("port=%d", port),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "gst-launch-1.0", args...)

	t.logger.Infof("talkback: uploading Speex/8000 audio (pt=%d) → %s:%d (source=%s, loop=%v)",
		pt, host, port, source, loop)
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("talkback: failed to launch gstreamer: %w", err)
	}

	t.cmd = cmd
	t.cancel = cancel
	t.active = true
	t.target = fmt.Sprintf("%s:%d", host, port)

	// Watch for the pipeline ending to clean up the state.
	go func() {
		_ = cmd.Wait()
		t.mu.Lock()
		t.active = false
		t.mu.Unlock()
		t.logger.Info("talkback: pipeline finished")
	}()

	return nil
}

// Stop stops the running pipeline (if any).
func (t *TalkbackInjector) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return
	}
	if t.cancel != nil {
		t.cancel()
	}
	t.active = false
	t.logger.Infof("talkback: stopped (%s)", t.target)
}

// IsActive reports whether a talk-back pipeline is running.
func (t *TalkbackInjector) IsActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active
}

// Target returns the running "host:port" destination (empty if inactive).
func (t *TalkbackInjector) Target() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return ""
	}
	return t.target
}
