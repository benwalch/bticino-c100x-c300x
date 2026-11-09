package sip

import (
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"

	"bticino_bridge/pkg/events"
)

// Stop closes the socket before it signals the loop. A closed socket fails
// every call at once, so a loop that keeps going spins and floods the log.
func expectLoopEndsQuietly(t *testing.T, hook *logrustest.Hook, loop func(), closeSocket func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { loop(); close(done) }()
	time.Sleep(50 * time.Millisecond)
	closeSocket()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop still running after its socket was closed")
	}
	for _, e := range hook.AllEntries() {
		if e.Level <= logrus.ErrorLevel {
			t.Errorf("logged %q on a closed socket", e.Message)
		}
	}
}

func TestRTSPAcceptEndsOnClosedListener(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	r := NewEnhancedRTSPServer(0, nil, events.NewEventBus(1, logger), logger)
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	r.listener = ln
	expectLoopEndsQuietly(t, hook, r.acceptConnections, func() { ln.Close() })
}

func TestRTPReaderEndsOnClosedSocket(t *testing.T) {
	logger, hook := logrustest.NewNullLogger()
	v := NewVideoStreamManager(nil, events.NewEventBus(1, logger), logger)
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	v.rtpListener = pc
	v.running = true
	expectLoopEndsQuietly(t, hook, v.handleRTPPackets, func() { pc.Close() })
}
