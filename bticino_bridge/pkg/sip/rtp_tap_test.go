package sip

import (
	"encoding/binary"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// buildLoopbackFrame builds an Ethernet+IPv4+UDP frame like the ones delivered
// by AF_PACKET on `lo` (zero MACs, ethertype 0x0800).
func buildLoopbackFrame(dstPort int, payload []byte, ipOptWords int) []byte {
	eth := make([]byte, 14)
	eth[12], eth[13] = 0x08, 0x00

	ihl := 5 + ipOptWords
	ip := make([]byte, ihl*4)
	ip[0] = byte(0x40 | ihl) // version 4 + IHL
	binary.BigEndian.PutUint16(ip[2:], uint16(len(ip)+8+len(payload)))
	ip[8] = 64 // TTL
	ip[9] = 17 // UDP
	copy(ip[12:16], []byte{127, 0, 0, 1})
	copy(ip[16:20], []byte{127, 0, 0, 1})

	udp := make([]byte, 8)
	binary.BigEndian.PutUint16(udp[0:], 49145)
	binary.BigEndian.PutUint16(udp[2:], uint16(dstPort))
	binary.BigEndian.PutUint16(udp[4:], uint16(8+len(payload)))

	frame := append(eth, ip...)
	frame = append(frame, udp...)
	return append(frame, payload...)
}

func TestExtractUDPPayload(t *testing.T) {
	want := []byte{0x80, 0x60, 0x19, 0xbf, 0x67, 0x42}
	port, payload, ok := extractUDPPayload(buildLoopbackFrame(5002, want, 0))
	if !ok {
		t.Fatal("expected ok=true for valid IPv4 UDP frame")
	}
	if port != 5002 {
		t.Fatalf("expected dstPort=5002, got %d", port)
	}
	if string(payload) != string(want) {
		t.Fatalf("wrong payload: % x", payload)
	}
}

func TestExtractUDPPayloadWithIPOptions(t *testing.T) {
	// IHL>5: the UDP offset must honor the IP options.
	want := []byte{0xde, 0xad}
	port, payload, ok := extractUDPPayload(buildLoopbackFrame(5002, want, 2))
	if !ok || port != 5002 || string(payload) != string(want) {
		t.Fatalf("failed with IP options: ok=%v port=%d payload=% x", ok, port, payload)
	}
}

func TestExtractUDPPayloadRejectsNonIPv4(t *testing.T) {
	frame := buildLoopbackFrame(5002, []byte{1}, 0)
	frame[12], frame[13] = 0x86, 0xdd // ethertype IPv6
	if _, _, ok := extractUDPPayload(frame); ok {
		t.Fatal("expected ok=false for non-IPv4 ethertype")
	}
}

func TestExtractUDPPayloadRejectsNonUDP(t *testing.T) {
	frame := buildLoopbackFrame(5002, []byte{1}, 0)
	frame[14+9] = 6 // TCP
	if _, _, ok := extractUDPPayload(frame); ok {
		t.Fatal("expected ok=false for non-UDP protocol")
	}
}

func TestExtractUDPPayloadTruncatedFrames(t *testing.T) {
	full := buildLoopbackFrame(5002, []byte{1, 2, 3}, 0)
	for n := 0; n < len(full); n++ {
		if _, _, ok := extractUDPPayload(full[:n]); ok {
			t.Fatalf("expected ok=false for frame truncated to %d bytes", n)
		}
	}
}

// fakeFrameSource serves prepared frames and then io.EOF.
type fakeFrameSource struct {
	frames []struct {
		data    []byte
		pkttype byte
	}
	closed chan struct{}
}

func (f *fakeFrameSource) ReadFrame(buf []byte) (int, byte, error) {
	if len(f.frames) == 0 {
		<-f.closed // blocks until Close, like a real socket with no traffic
		return 0, 0, io.EOF
	}
	fr := f.frames[0]
	f.frames = f.frames[1:]
	n := copy(buf, fr.data)
	return n, fr.pkttype, nil
}

func (f *fakeFrameSource) Close() error {
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
	return nil
}

func TestTapForwardsOnlyMatchingHostPackets(t *testing.T) {
	sink, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	sinkPort := sink.LocalAddr().(*net.UDPAddr).Port

	src := &fakeFrameSource{closed: make(chan struct{})}
	add := func(dstPort int, payload []byte, pkttype byte) {
		src.frames = append(src.frames, struct {
			data    []byte
			pkttype byte
		}{buildLoopbackFrame(dstPort, payload, 0), pkttype})
	}
	add(5002, []byte("video1"), syscall.PACKET_HOST)
	add(5002, []byte("video1"), syscall.PACKET_OUTGOING) // lo tx duplicate: discard
	add(5007, []byte("other"), syscall.PACKET_HOST)      // other port: discard
	add(5002, []byte("video2"), syscall.PACKET_HOST)

	tap := NewLoopbackRTPTap(5002, sinkPort, logrus.New())
	tap.newSource = func() (frameSource, error) { return src, nil }
	if err := tap.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tap.Stop()

	var got []string
	buf := make([]byte, 2048)
	for len(got) < 2 {
		sink.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := sink.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("sink did not receive the 2 expected packets (got %v): %v", got, err)
		}
		got = append(got, string(buf[:n]))
	}
	if got[0] != "video1" || got[1] != "video2" {
		t.Fatalf("wrong forwarded payloads: %v", got)
	}
	// no extra packet (the OUTGOING duplicate and the 5007 one are discarded)
	sink.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if n, _, err := sink.ReadFromUDP(buf); err == nil {
		t.Fatalf("unexpected forwarded packet: %q", buf[:n])
	}
}

func TestTapStopClosesSourceAndIsIdempotent(t *testing.T) {
	src := &fakeFrameSource{closed: make(chan struct{})}
	tap := NewLoopbackRTPTap(5002, 19999, logrus.New())
	tap.newSource = func() (frameSource, error) { return src, nil }
	if err := tap.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tap.Stop()
	select {
	case <-src.closed:
	case <-time.After(time.Second):
		t.Fatal("Stop did not close the source")
	}
	tap.Stop() // idempotent: must not panic
}

func TestTapStartIdempotent(t *testing.T) {
	opened := 0
	tap := NewLoopbackRTPTap(5002, 19999, logrus.New())
	tap.newSource = func() (frameSource, error) {
		opened++
		return &fakeFrameSource{closed: make(chan struct{})}, nil
	}
	if err := tap.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer tap.Stop()
	if err := tap.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if opened != 1 {
		t.Fatalf("expected 1 open source (idempotent Start), got %d", opened)
	}
}
