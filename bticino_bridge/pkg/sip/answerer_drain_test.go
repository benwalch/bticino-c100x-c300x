package sip

import (
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// TestMediaDrain_ConsumesUDPWithoutError checks that the drain reads and drops UDP packets
// without error, counting audio packets. It uses ephemeral ports (0,0) so the test does
// not depend on fixed ports.
func TestMediaDrain_ConsumesUDPWithoutError(t *testing.T) {
	d, err := startMediaDrain(0, 0, logrus.New()) // 0,0 => ephemeral ports chosen by the OS
	if err != nil {
		t.Fatalf("startMediaDrain: %v", err)
	}
	// Find the real audio port (d.AudioAddr() is exposed for the test).
	addr := d.AudioAddr()
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	for i := 0; i < 10; i++ {
		conn.Write([]byte("simulated-srtp-packet"))
	}
	time.Sleep(50 * time.Millisecond)
	d.Stop()
	if got := d.PacketsAudio(); got < 1 {
		t.Fatalf("expected >=1 packet drained, got %d", got)
	}
}

// TestMediaDrain_VideoPortZero checks that with videoPort==0 no video socket is opened (no
// failure, there is just no video drain).
func TestMediaDrain_VideoPortZero(t *testing.T) {
	d, err := startMediaDrain(0, 0, logrus.New())
	if err != nil {
		t.Fatalf("startMediaDrain: %v", err)
	}
	defer d.Stop()
	if d.VideoAddr() != "" {
		t.Fatalf("expected an empty VideoAddr with videoPort=0, got %q", d.VideoAddr())
	}
}

// TestMediaDrain_StopIsIdempotent checks that calling Stop() twice does not panic.
func TestMediaDrain_StopIsIdempotent(t *testing.T) {
	d, err := startMediaDrain(0, 0, logrus.New())
	if err != nil {
		t.Fatalf("startMediaDrain: %v", err)
	}
	d.Stop()
	d.Stop()
}
