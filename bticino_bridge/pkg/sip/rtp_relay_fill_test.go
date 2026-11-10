package sip

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func mkRTP(pt byte, seq uint16, ssrc uint32, payload []byte) []byte {
	b := make([]byte, 12+len(payload))
	b[0], b[1] = 0x80, pt
	binary.BigEndian.PutUint16(b[2:4], seq)
	binary.BigEndian.PutUint32(b[8:12], ssrc)
	copy(b[12:], payload)
	return b
}

// newFillRelay starts an audio relay with silence fill + a priority input and a
// test UDP consumer. It returns the relay, the consumer and the ports.
func newFillRelay(t *testing.T) (*RTPRelay, *net.UDPConn, int, int) {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	mainPort, prioPort := freeUDPPort(t), freeUDPPort(t)
	r := NewRTPRelay("audio", mainPort, logger)
	r.EnableSilenceFill(110, prioPort)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	cons, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cons.Close() })
	if err := r.AddUDPConsumer("c1", "127.0.0.1", cons.LocalAddr().(*net.UDPAddr).Port); err != nil {
		t.Fatal(err)
	}
	return r, cons, mainPort, prioPort
}

func readRTP(t *testing.T, c *net.UDPConn, d time.Duration) (pt byte, seq uint16, ssrc uint32, payload []byte) {
	t.Helper()
	buf := make([]byte, 2048)
	_ = c.SetReadDeadline(time.Now().Add(d))
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("the consumer received no RTP: %v", err)
	}
	return buf[1] & 0x7f, binary.BigEndian.Uint16(buf[2:4]), binary.BigEndian.Uint32(buf[8:12]), append([]byte(nil), buf[12:n]...)
}

func sendTo(t *testing.T, port int, pkt []byte) {
	t.Helper()
	c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write(pkt)
}

// With no audio source the track stays alive with speex PT110 silence: go2rtc's
// ffmpeg (which transcodes audio) emits NOTHING until the first audio packet, and
// without this the HA card stayed black even though video arrived (2026-10-16).
func TestRelayAudioFillsSilenceWithoutSource(t *testing.T) {
	_, cons, _, _ := newFillRelay(t)
	pt, seq1, ssrc1, payload := readRTP(t, cons, 500*time.Millisecond)
	if pt != 110 || !bytes.Equal(payload, speexSilenceFrame) {
		t.Fatalf("expected silencio speex PT110, got PT%d payload %x", pt, payload)
	}
	_, seq2, ssrc2, _ := readRTP(t, cons, 500*time.Millisecond)
	if seq2 != seq1+1 || ssrc2 != ssrc1 {
		t.Fatalf("stream not continuous: seq %d→%d ssrc %x→%x", seq1, seq2, ssrc1, ssrc2)
	}
}

// Real audio (even with another PT, e.g. 98 from the entrance panel's SRTP) goes
// out with PT110, our SSRC and a continuous sequence, and displaces the silence.
func TestRelayAudioRewritesSourceToPT110(t *testing.T) {
	_, cons, mainPort, _ := newFillRelay(t)
	readRTP(t, cons, 500*time.Millisecond) // initial silence
	voice := []byte("VOZ-REAL")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sendTo(t, mainPort, mkRTP(98, 1, 0xdeadbeef, voice))
		pt, _, ssrc, p := readRTP(t, cons, 200*time.Millisecond)
		if bytes.Equal(p, voice) {
			if pt != 110 || ssrc == 0xdeadbeef {
				t.Fatalf("the voice must go out rewritten (PT110, own SSRC): PT%d ssrc %x", pt, ssrc)
			}
			return
		}
	}
	t.Fatal("the main source voice did not reach the consumer")
}

// With the priority input active (panel view listening), the silent tap of the main
// source is dropped: two streams are never mixed on the same track.
func TestRelayAudioPriorityDropsMain(t *testing.T) {
	_, cons, mainPort, prioPort := newFillRelay(t)
	prio, mainP := []byte("PRIORIDAD"), []byte("TAP-MUDO")
	stop := make(chan struct{})
	go func() { // both sources sending at the same time every 20 ms
		tk := time.NewTicker(20 * time.Millisecond)
		defer tk.Stop()
		for i := uint16(0); ; i++ {
			select {
			case <-stop:
				return
			case <-tk.C:
				sendTo(t, prioPort, mkRTP(98, i, 1, prio))
				sendTo(t, mainPort, mkRTP(110, i, 2, mainP))
			}
		}
	}()
	time.Sleep(150 * time.Millisecond) // let the priority input start
	for i := 0; i < 20; i++ {
		_, _, _, p := readRTP(t, cons, 300*time.Millisecond)
		if bytes.Equal(p, mainP) {
			close(stop)
			t.Fatal("with the priority input active the main source audio must not pass")
		}
	}
	close(stop)
}
