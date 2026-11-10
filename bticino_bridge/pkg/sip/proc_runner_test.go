package sip

import (
	"testing"
	"time"
)

func TestExecRunner_StartsAndKills(t *testing.T) {
	r := NewExecRunner()
	// `sleep 30` as a harmless long-running process.
	p, err := r.Start("sleep", []string{"30"})
	if err != nil {
		t.Skipf("no 'sleep' in the test environment: %v", err)
	}
	// Kill it and confirm that Wait returns in a reasonable time.
	if err := p.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return after Kill")
	}
}

func TestExecRunner_MissingBinary(t *testing.T) {
	r := NewExecRunner()
	if _, err := r.Start("/no/existe/ffmpeg-xyz", []string{"-x"}); err == nil {
		t.Fatal("expected an error starting a missing binary")
	}
}
