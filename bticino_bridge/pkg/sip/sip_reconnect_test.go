package sip

import (
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func TestNextReconnectDelay(t *testing.T) {
	want := []time.Duration{
		1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second,
	}
	for attempt, w := range want {
		if got := nextReconnectDelay(attempt); got != w {
			t.Fatalf("attempt %d: got %v, want %v", attempt, got, w)
		}
	}
}

// Errors from a connection that was already replaced must not start another
// reconnect (that is what turned one outage into a loop).
func TestConnErrorIgnoresStaleGeneration(t *testing.T) {
	c := &BTicinoSIPClient{logger: logrus.New(), stopCh: make(chan struct{})}
	id := &sipIdentity{username: "webrtc", gen: 7}

	if c.connError(id, 6, errors.New("EOF")) {
		t.Fatal("stale generation must not trigger a reconnect")
	}
	if id.reconnecting {
		t.Fatal("no reconnect loop must be started for a stale error")
	}
}
