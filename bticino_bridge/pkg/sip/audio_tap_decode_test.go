package sip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
)

func TestBuildTapDecodeSDP(t *testing.T) {
	sdp := BuildTapDecodeSDP("127.0.0.1", 11000, 110)
	for _, want := range []string{
		"c=IN IP4 127.0.0.1",
		"m=audio 11000 RTP/AVP 110", // PLAIN RTP (AVP, not SAVP), local listening port
		"a=rtpmap:110 speex/8000",
	} {
		if !strings.Contains(sdp, want) {
			t.Fatalf("tap SDP without %q:\n%s", want, sdp)
		}
	}
	if strings.Contains(sdp, "a=crypto") {
		t.Fatalf("the tap is plain RTP: it must not carry a=crypto:\n%s", sdp)
	}
}

func TestAudioTapDecoderBuildArgs(t *testing.T) {
	d := NewAudioTapDecoder("/usr/local/bin/ffmpeg", logrus.New())
	args := d.buildArgs("/tmp/tap.sdp", "/tmp/out.wav")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-protocol_whitelist", "file,rtp,udp",
		"-i /tmp/tap.sdp",
		"-c:a pcm_s16le", "-ar 8000", "-ac 1",
		"-flush_packets", "1",
		"-f wav", "/tmp/out.wav",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args without %q:\n%s", want, joined)
		}
	}
}

func TestNativeAudioRTPPortIs5000(t *testing.T) {
	if NativeAudioRTPPort != 5000 {
		t.Fatalf("NativeAudioRTPPort must be 5000, got %d", NativeAudioRTPPort)
	}
}

func TestAudioTapDecoderDefaultTimeout(t *testing.T) {
	d := NewAudioTapDecoder("/usr/local/bin/ffmpeg", logrus.New())
	if d.timeout != defaultAudioTapTimeout {
		t.Fatalf("expected default timeout %s, got %s", defaultAudioTapTimeout, d.timeout)
	}
}

func TestAudioTapDecoderSetTimeout(t *testing.T) {
	d := NewAudioTapDecoder("/usr/local/bin/ffmpeg", logrus.New())
	d.SetTimeout(10 * time.Minute)
	if d.timeout != 10*time.Minute {
		t.Fatalf("expected timeout raised to 10m, got %s", d.timeout)
	}
	// Must not affect other instances (only THIS one, per SetTimeout's doc).
	other := NewAudioTapDecoder("/usr/local/bin/ffmpeg", logrus.New())
	if other.timeout != defaultAudioTapTimeout {
		t.Fatalf("SetTimeout must not affect other instances, got %s", other.timeout)
	}
}

// newFakeFfmpegScript creates a fake executable that ignores its arguments and
// sleeps past any test timeout, to force deterministically (without a real
// ffmpeg or RTP traffic) that Decode() ends through the context's hard limit
// (DeadlineExceeded), NOT through a genuine process failure. It uses `exec` so
// that sleep REPLACES the sh process (instead of a separate child): the
// context kill then ends the exact PID holding the stdout/stderr pipes, and
// CombinedOutput() returns at the kill instead of waiting 5s for an orphaned
// "sleep" to close those pipes by itself.
func newFakeFfmpegScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ffmpeg.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatalf("could not write the test script: %v", err)
	}
	return path
}

// TestAudioTapDecoderSuppressesDeadlineExceededLog: the normal end of a session
// through the hard limit (ctx.Err()==DeadlineExceeded) must not be logged as if
// it were a real ffmpeg failure.
func TestAudioTapDecoderSuppressesDeadlineExceededLog(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	d := NewAudioTapDecoder(newFakeFfmpegScript(t), logger)
	d.SetTimeout(150 * time.Millisecond)

	sink := filepath.Join(t.TempDir(), "out.wav")
	if err := d.Decode("127.0.0.1", 15099, 110, sink); err == nil {
		t.Fatal("expected an error: the process is killed when the hard limit expires")
	}

	for _, entry := range hook.AllEntries() {
		if entry.Level <= logrus.WarnLevel {
			t.Fatalf("a normal DeadlineExceeded must not be logged as warning/error: level=%s msg=%q", entry.Level, entry.Message)
		}
	}
}

// TestAudioTapDecoderLogsGenuineFfmpegFailure checks that a REAL process failure
// (not caused by the hard limit or by Stop()) IS STILL logged: only normal
// end-of-session terminations are suppressed.
func TestAudioTapDecoderLogsGenuineFfmpegFailure(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	d := NewAudioTapDecoder(filepath.Join(t.TempDir(), "no-such-binary"), logger)
	d.SetTimeout(5 * time.Second) // plenty: the missing binary must fail first

	sink := filepath.Join(t.TempDir(), "out.wav")
	if err := d.Decode("127.0.0.1", 15098, 110, sink); err == nil {
		t.Fatal("expected an error: missing ffmpeg binary")
	}

	found := false
	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.WarnLevel {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a Warn log for a genuine ffmpeg failure (missing binary, not deadline/cancel)")
	}
}
