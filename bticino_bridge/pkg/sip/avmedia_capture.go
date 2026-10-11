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
	cmd := exec.Command("gst-launch-1.0",
		"udpsrc", fmt.Sprintf("port=%d", port),
		"caps=application/x-rtp,media=(string)video,clock-rate=(int)90000,encoding-name=(string)H264,payload=(int)96",
		"!", "rtph264depay",
		"!", "h264parse",
		"!", "imxvpudec",
		"!", "jpegenc", "quality=90",
		"!", "filesink", fmt.Sprintf("location=%s", tmp), "buffer-mode=2",
	)
	stderrBuf := &bytes.Buffer{}
	cmd.Stderr = stderrBuf
	if err := cmd.Start(); err != nil {
		return res, fmt.Errorf("failed to launch capture pipeline: %w", err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = os.Remove(tmp)
	}()

	// Margin for udpsrc to bind the port
	time.Sleep(300 * time.Millisecond)

	// 2) Send A SINGLE *7*300 (ActivateVideoStream no longer retries)
	logger.Infof("cooperative capture: single *7*300 → 127.0.0.1:%d", port)
	if err := own.ActivateVideoStream("127.0.0.1", port, true); err != nil {
		res.ackError = err.Error()
	} else {
		res.ack = true
	}

	// 3) Poll the file looking for complete JPEG frames
	deadline := time.Now().Add(timeout)
	var lastComplete []byte
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		data, err := os.ReadFile(tmp)
		if err != nil || len(data) < 4 {
			continue
		}
		n, last := scanJPEGFrames(data)
		res.frames = n
		if last != nil {
			lastComplete = last
		}
		if n >= warmupFrames && lastComplete != nil {
			break // the decoder has already passed a clean IDR
		}
	}
	res.jpeg = lastComplete

	if res.frames == 0 && stderrBuf.Len() > 0 {
		logger.Warnf("cooperative capture: pipeline stderr: %s", stderrBuf.String())
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
