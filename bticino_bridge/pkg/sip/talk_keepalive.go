package sip

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// speexSilenceFrame is a 38-byte speex NB frame (8 kHz, 20 ms) of silence. It
// was made by encoding silence with ffmpeg/libspeex and taking the RTP payload
// in steady state (the encoder's 1st packet differs slightly; from the 2nd on it is this one):
//
//	ffmpeg -f lavfi -i anullsrc=r=8000:cl=mono -acodec libspeex -ar 8000 -ac 1 \
//	       -payload_type 97 -f rtp rtp://127.0.0.1:<port>
var speexSilenceFrame = mustHex("2e9d1b9a2002017fffffffffff8100bfffffffffffc0805fffffffffffe0402ffffffffffff7")

const (
	keepaliveFrame   = 20 * time.Millisecond // duration of one speex frame
	keepaliveMicIdle = 60 * time.Millisecond // no mic voice for this long => silence is injected
	keepaliveTick    = 5 * time.Millisecond  // resolution of the injection clock
	keepaliveRate    = 8000                  // reloj RTP de speex NB
)

// upstreamKeepalive is an RTP relay that keeps the upstream audio to the
// entrance panel alive. It listens to the HA microphone (speex RTP sent by
// go2rtc) and sends a CONTINUOUS stream to ffmpeg: it forwards voice as soon as
// it arrives and, while the mic is quiet, a silence frame every 20 ms. So
// ffmpeg (which only copies and encrypts to SRTP) never runs out of packets,
// and the entrance panel gets upstream audio from the moment the call is
// answered (symmetric RTP assumption: it sends no downstream audio until it
// gets upstream audio). Mixing in ffmpeg with amix does NOT work: amix blocks
// when the mic input has no packets (tested).
type upstreamKeepalive struct {
	in     *net.UDPConn
	out    *net.UDPConn
	pt     byte
	ssrc   uint32
	logger *logrus.Logger
	start  time.Time

	mu       sync.Mutex
	seq      uint16
	lastSent time.Time
	lastMic  time.Time

	stopOnce sync.Once
	done     chan struct{}
	wg       sync.WaitGroup
}

// startUpstreamKeepalive listens on inHost:inPort (0 = ephemeral) and sends to
// 127.0.0.1:outPort with payload type pt. It starts sending silence at once.
func startUpstreamKeepalive(inHost string, inPort, outPort, pt int, logger *logrus.Logger) (*upstreamKeepalive, error) {
	in, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(inHost), Port: inPort})
	if err != nil {
		return nil, fmt.Errorf("keepalive: listen %s:%d: %w", inHost, inPort, err)
	}
	out, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: outPort})
	if err != nil {
		in.Close()
		return nil, fmt.Errorf("keepalive: output 127.0.0.1:%d: %w", outPort, err)
	}
	k := &upstreamKeepalive{
		in:     in,
		out:    out,
		pt:     byte(pt) & 0x7F,
		ssrc:   rand.Uint32(),
		seq:    uint16(rand.Uint32()),
		logger: logger,
		start:  time.Now(),
		done:   make(chan struct{}),
	}
	k.wg.Add(2)
	go k.readLoop()
	go k.tickLoop()
	return k, nil
}

// InPort returns the real port the mic is received on (useful with inPort=0).
func (k *upstreamKeepalive) InPort() int {
	return k.in.LocalAddr().(*net.UDPAddr).Port
}

// Stop stops the relay and frees the sockets. Idempotent.
func (k *upstreamKeepalive) Stop() {
	k.stopOnce.Do(func() {
		close(k.done)
		k.in.Close()
		k.out.Close()
		k.wg.Wait()
	})
}

// readLoop forwards each mic packet at once (with our sequence and SSRC).
func (k *upstreamKeepalive) readLoop() {
	defer k.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, _, err := k.in.ReadFromUDP(buf)
		if err != nil {
			return // socket closed by Stop
		}
		payload := rtpPayload(buf[:n])
		if payload == nil {
			continue
		}
		k.mu.Lock()
		k.lastMic = time.Now()
		k.sendLocked(payload)
		k.mu.Unlock()
	}
}

// tickLoop injects silence every 20 ms while the mic is quiet.
func (k *upstreamKeepalive) tickLoop() {
	defer k.wg.Done()
	t := time.NewTicker(keepaliveTick)
	defer t.Stop()
	for {
		select {
		case <-k.done:
			return
		case <-t.C:
			k.mu.Lock()
			now := time.Now()
			if now.Sub(k.lastMic) >= keepaliveMicIdle && now.Sub(k.lastSent) >= keepaliveFrame {
				k.sendLocked(speexSilenceFrame)
			}
			k.mu.Unlock()
		}
	}
}

// sendLocked sends an RTP packet with the given payload. The timestamp comes from
// the real clock so voice and silence share a continuous timeline. Assumes k.mu is held.
func (k *upstreamKeepalive) sendLocked(payload []byte) {
	now := time.Now()
	pkt := make([]byte, 12+len(payload))
	pkt[0] = 0x80 // version 2, no padding/extension/CSRC
	pkt[1] = k.pt
	binary.BigEndian.PutUint16(pkt[2:4], k.seq)
	binary.BigEndian.PutUint32(pkt[4:8], uint32(now.Sub(k.start).Nanoseconds()*keepaliveRate/int64(time.Second)))
	binary.BigEndian.PutUint32(pkt[8:12], k.ssrc)
	copy(pkt[12:], payload)
	if _, err := k.out.Write(pkt); err != nil {
		select {
		case <-k.done: // closing: expected error
		default:
			k.logger.Debugf("[TALK] keepalive: send failed: %v", err)
		}
		return
	}
	k.seq++
	k.lastSent = now
}

// rtpPayload extracts the payload of an RTP v2 packet (skips CSRC, extension and
// padding). It returns nil if the packet is not valid RTP or has no payload.
func rtpPayload(b []byte) []byte {
	if len(b) < 12 || b[0]>>6 != 2 {
		return nil
	}
	hdr := 12 + 4*int(b[0]&0x0F)
	if b[0]&0x10 != 0 { // extension
		if len(b) < hdr+4 {
			return nil
		}
		hdr += 4 + 4*int(binary.BigEndian.Uint16(b[hdr+2:hdr+4]))
	}
	end := len(b)
	if b[0]&0x20 != 0 { // padding: the last byte says how many
		end -= int(b[len(b)-1])
	}
	if hdr >= end {
		return nil
	}
	return b[hdr:end]
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}
