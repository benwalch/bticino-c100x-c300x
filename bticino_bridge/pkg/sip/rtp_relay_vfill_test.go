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

type rtpOut struct {
	pt      byte
	marker  bool
	seq     uint16
	ts      uint32
	ssrc    uint32
	payload []byte
}

func readOut(t *testing.T, c *net.UDPConn, d time.Duration) rtpOut {
	t.Helper()
	buf := make([]byte, 2048)
	_ = c.SetReadDeadline(time.Now().Add(d))
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("the consumer received no RTP: %v", err)
	}
	return rtpOut{
		pt: buf[1] & 0x7f, marker: buf[1]&0x80 != 0,
		seq: binary.BigEndian.Uint16(buf[2:4]), ts: binary.BigEndian.Uint32(buf[4:8]),
		ssrc: binary.BigEndian.Uint32(buf[8:12]), payload: append([]byte(nil), buf[12:n]...),
	}
}

func newVFillRelay(t *testing.T) (*net.UDPConn, int) {
	t.Helper()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	port := freeUDPPort(t)
	r := NewRTPRelay("video", port, logger)
	r.EnableVideoFill(96)
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
	return cons, port
}

// readFrame reads packets up to the marker (end of frame).
func readFrame(t *testing.T, c *net.UDPConn) []rtpOut {
	t.Helper()
	var f []rtpOut
	for i := 0; i < 50; i++ {
		p := readOut(t, c, 2*time.Second)
		f = append(f, p)
		if p.marker {
			return f
		}
	}
	t.Fatal("frame without marker")
	return nil
}

// At rest the RTSP does not go silent: a black H.264 background (SPS+PPS+IDR) at
// ~1 fps with PT96, so go2rtc's ffmpeg stays connected and the real picture arrives
// at once when the panel view opens (before: a retry cycle of ~90 s).
func TestRelayVideoBackgroundAtRest(t *testing.T) {
	cons, _ := newVFillRelay(t)
	f1 := readFrame(t, cons)
	if f1[0].pt != 96 || f1[0].payload[0]&31 != 7 || f1[1].payload[0]&31 != 8 {
		t.Fatalf("the idle frame must start with SPS and PPS: PT%d nal %d,%d", f1[0].pt, f1[0].payload[0]&31, f1[1].payload[0]&31)
	}
	hasIDR := false
	for i, p := range f1 {
		if p.ts != f1[0].ts {
			t.Fatal("all packets of a frame share the timestamp")
		}
		if i > 0 && p.seq != f1[i-1].seq+1 {
			t.Fatal("sequence not continuous")
		}
		nt := p.payload[0] & 31
		if nt == 5 || (nt == 28 && p.payload[1]&31 == 5) {
			hasIDR = true
		}
		if nt == 6 {
			t.Fatal("the x264 SEI must not be emitted")
		}
	}
	if !hasIDR {
		t.Fatal("the idle frame must carry an IDR")
	}
	f2 := readFrame(t, cons)
	if d := f2[0].ts - f1[0].ts; d < 60000 || d > 120000 {
		t.Fatalf("the background must repeat at ~1 fps (Δts=%d, expected ~90000)", d)
	}
}

// Real video goes out rewritten (own SSRC/sequence) keeping the marker and the
// source's timestamp spacing, and while it flows the background is not interleaved.
func TestRelayVideoRealDisplacesBackground(t *testing.T) {
	cons, port := newVFillRelay(t)
	readFrame(t, cons) // initial background
	src := func(seq uint16, ts uint32, marker bool, payload []byte) []byte {
		b := mkRTP(96, seq, 0xabcdef01, payload)
		binary.BigEndian.PutUint32(b[4:8], ts)
		if marker {
			b[1] |= 0x80
		}
		return b
	}
	var got []rtpOut
	for i := 0; i < 30; i++ { // ~1.5 s of real video at 20 fps
		ts := uint32(1000 + i*4500)
		sendTo(t, port, src(uint16(i*2), ts, false, []byte{0x5c, 0x85, 0xAA}))
		sendTo(t, port, src(uint16(i*2+1), ts, true, []byte{0x5c, 0x45, 0xBB}))
		time.Sleep(50 * time.Millisecond)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(got) < 60 {
		got = append(got, readOut(t, cons, 500*time.Millisecond))
	}
	var real []rtpOut
	for _, p := range got {
		if bytes.Equal(p.payload, []byte{0x5c, 0x85, 0xAA}) || bytes.Equal(p.payload, []byte{0x5c, 0x45, 0xBB}) {
			real = append(real, p)
		} else if len(real) > 0 && p.payload[0]&31 == 7 {
			t.Fatal("the background was interleaved while real video arrived")
		}
	}
	if len(real) < 20 {
		t.Fatalf("%d real packets arrived", len(real))
	}
	for i := 1; i < len(real); i++ {
		if real[i].ssrc != real[0].ssrc || real[i].ssrc == 0xabcdef01 {
			t.Fatal("real video must go out with the relay's own SSRC")
		}
	}
	if !real[1].marker || real[0].marker {
		t.Fatal("the source's marker bit must be kept")
	}
	if d := real[2].ts - real[0].ts; d != 4500 {
		t.Fatalf("the source's timestamp spacing must be kept (Δ=%d, expected 4500)", d)
	}
}
