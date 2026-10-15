// avmedia_capture.go holds the COOPERATIVE video capture shared by the
// snapshot and the diagnostic probe.
//
// Pattern (validated on a real device on 2026-07-07):
//   - A gst-launch pipeline is started that decodes H.264→JPEG reading RTP from
//     a local UDP port (udpsrc ! rtph264depay ! h264parse ! imxvpudec ! jpegenc).
//   - bt_av_media is asked A single time (no retries) to duplicate its video RTP
//     to that port (*7*300). NO self-INVITE is done and SIP is not touched.
//   - It only yields an image if a native video session is active ("eye"
//     button/self-switch-on or a real call); when idle bt_av_media answers
//     NACK and no RTP arrives.
//
// This is the safe pattern: blast radius of a single command, cooperative with the
// native firmware (it does not compete for /dev/video0).
package sip

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"bticino_bridge/pkg/bticino"
	"bticino_bridge/pkg/openwebnet"

	"github.com/sirupsen/logrus"
)

var (
	jpegSOI = []byte{0xFF, 0xD8}
	jpegEOI = []byte{0xFF, 0xD9}
)

// captureResult summarizes a cooperative capture.
type captureResult struct {
	ack      bool   // *7*300 answered *#*1## (ACK)
	ackError string // empty if ACK; otherwise the error/NACK
	frames   int    // decoded JPEG frames
	jpeg     []byte // best frame (the last complete one), nil if none
}

// jpegDecoder is a running H264→JPEG pipeline on one UDP port.
type jpegDecoder struct {
	cmd    *exec.Cmd
	tmp    string
	stderr *bytes.Buffer
}

// startJPEGDecoder binds the pipeline port and starts decoding into tmp.
func startJPEGDecoder(port int, logger *logrus.Logger) (*jpegDecoder, error) {
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("bticino_cap_%d.jpg.partial", port))
	_ = os.Remove(tmp)

	cmd := exec.Command("gst-launch-1.0",
		"udpsrc", fmt.Sprintf("port=%d", port),
		"caps=application/x-rtp,media=(string)video,clock-rate=(int)90000,encoding-name=(string)H264,payload=(int)96",
		"!", "rtph264depay",
		"!", "h264parse",
		"!", "imxvpudec",
		"!", "jpegenc", "quality=95",
		"!", "filesink", fmt.Sprintf("location=%s", tmp), "buffer-mode=2",
	)
	stderrBuf := &bytes.Buffer{}
	cmd.Stderr = stderrBuf
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to launch capture pipeline: %w", err)
	}
	// Margin for udpsrc to bind the port
	time.Sleep(300 * time.Millisecond)
	return &jpegDecoder{cmd: cmd, tmp: tmp, stderr: stderrBuf}, nil
}

func (d *jpegDecoder) stop() {
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = d.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = d.cmd.Process.Kill()
		<-done
	}
	_ = os.Remove(d.tmp)
}

// pollJPEGFile collects complete JPEG frames until warmup or timeout.
func pollJPEGFile(tmp string, warmupFrames int, timeout time.Duration, logger *logrus.Logger) (frames int, last []byte) {
	if warmupFrames <= 0 {
		warmupFrames = 12
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		data, err := os.ReadFile(tmp)
		if err != nil || len(data) < 4 {
			continue
		}
		n, complete := scanJPEGFrames(data)
		frames = n
		if complete != nil {
			last = complete
		}
		if n >= warmupFrames && last != nil {
			break // the decoder has already passed a clean IDR
		}
	}
	return frames, last
}

// captureCooperative decodes a frame by asking bt_av_media to duplicate
// its video RTP to the given UDP port (a single *7*300, no retries and no
// self-INVITE). warmupFrames is how many frames to accumulate before keeping
// the last one (the decoder produces noise until a clean IDR). timeout covers the
// polling after sending the command.
func captureCooperative(own *openwebnet.Client, port, warmupFrames int, timeout time.Duration, logger *logrus.Logger) (captureResult, error) {
	res := captureResult{}
	if own == nil {
		return res, fmt.Errorf("OpenWebNet client not available")
	}
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	if warmupFrames <= 0 {
		warmupFrames = 12
	}

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("bticino_cap_%d.jpg.partial", port))
	_ = os.Remove(tmp)

	// 1) Start the decoding pipeline FIRST (udpsrc must have the
	//    port bound before bt_av_media starts sending).
	dec, err := startJPEGDecoder(port, logger)
	if err != nil {
		return res, err
	}
	defer dec.stop()

	// 2) Send A SINGLE *7*300 (ActivateVideoStream no longer retries).
	// Resolution follows the unit: high-res (type 0) on 300X, low-res
	// (type 1) on 100X — high-res is ACKed with no RTP behind it there.
	logger.Infof("cooperative capture: single *7*300 → 127.0.0.1:%d", port)
	if err := own.ActivateVideoStream("127.0.0.1", port, bticino.VideoHighResForVariant(bticino.DeviceVariant)); err != nil {
		res.ackError = err.Error()
	} else {
		res.ack = true
	}

	// NACK means bt_av_media has no session to duplicate, so no RTP will
	// ever arrive: skip the poll and fail fast instead of burning the full
	// timeout. The caller may retry later once the session is up.
	if !res.ack {
		return res, nil
	}

	// 3) Poll the file looking for complete JPEG frames
	res.frames, res.jpeg = pollJPEGFile(tmp, warmupFrames, timeout, logger)

	if res.frames == 0 && dec.stderr.Len() > 0 {
		logger.Warnf("cooperative capture: pipeline stderr: %s", dec.stderr.String())
	}
	return res, nil
}

// scanJPEGFrames walks a buffer of concatenated JPEGs (SOI FFD8 ... EOI FFD9)
// and returns how many complete frames it holds and a copy of the last one.
func scanJPEGFrames(data []byte) (count int, last []byte) {
	pos := 0
	for {
		soi := bytes.Index(data[pos:], jpegSOI)
		if soi < 0 {
			break
		}
		soi += pos
		eoi := bytes.Index(data[soi+2:], jpegEOI)
		if eoi < 0 {
			break // Incomplete JPEG (frame being written)
		}
		end := soi + 2 + eoi + 2
		count++
		frame := make([]byte, end-soi)
		copy(frame, data[soi:end])
		last = frame
		pos = end
	}
	return count, last
}
