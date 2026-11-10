package sip

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// TestAudioSenderPlayHonorsTimeout: a process that does not end must be killed
// by the timeout, so Play RETURNS (does not hang the HTTP handler → anti-DoS).
func TestAudioSenderPlayHonorsTimeout(t *testing.T) {
	// Fake script that ignores its args and sleeps long (simulates a hung ffmpeg).
	dir := t.TempDir()
	fake := filepath.Join(dir, "fakeffmpeg.sh")
	// exec: sleep REPLACES the shell (a direct child, like the real ffmpeg), so
	// CommandContext kills it when the timeout expires, with no grandchildren
	// keeping the pipes open.
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec sleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := NewAudioSender(fake, logrus.New())
	s.timeout = 200 * time.Millisecond // short limit for the test

	ans := AudioAnswer{Port: 7078, PayloadType: 98, CryptoSuite: "AES_CM_128_HMAC_SHA1_80", CryptoKey: "K"}

	start := time.Now()
	err := s.Play(filepath.Join(dir, "x.wav"), "127.0.0.1", ans)
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("Play hung (%v): the timeout did not kill the process", elapsed)
	}
	if err == nil {
		t.Fatal("expected an error when the timeout kills the process")
	}
}
