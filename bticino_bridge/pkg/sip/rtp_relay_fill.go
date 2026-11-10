package sip

import (
	"encoding/binary"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// RTSPAudioPT is the PT of the audio track announced in the RTSP SDP (speex/8000).
	RTSPAudioPT = 110
	// EyeAudioRelayPort is the PRIORITY input of the audio relay: the panel view's
	// listening forwards here and replaces the lo:5000 tap (silent in panel view mode).
	EyeAudioRelayPort = 10012

	// fillPrioHold: while the priority input has sent within this time, the main
	// source is dropped (two streams are never mixed in the track).
	fillPrioHold = 200 * time.Millisecond
	// fillIdle: no audio from any source for this long => silence is sent.
	fillIdle = 60 * time.Millisecond
)

// relayFill is the state of an RTPRelay's "audio track always alive" mode.
//
// Why: go2rtc's `doorbell` stream is an ffmpeg that transcodes the audio
// (-c:a pcm_alaw) and sends NOTHING, not even video, until it receives the
// first valid audio packet (reproduced 2026-10-16). If the RTSP audio track is
// empty or uses a PT other than the one in the SDP, go2rtc kills the exec on
// timeout and the HA card stays black although video is arriving.
//
// In fill mode everything that goes out is rewritten with ONE SSRC, a
// continuous sequence and the SDP's PT; without incoming audio, speex silence
// is injected every 20 ms. A priority input (the panel view's listening)
// replaces the main one (the lo:5000 tap, silent during panel view).
type relayFill struct {
	pt       byte
	prioPort int
	prioConn *net.UDPConn
	ssrc     uint32
	start    time.Time

	mu       sync.Mutex
	seq      uint16
	lastOut  time.Time
	lastIn   time.Time
	lastPrio time.Time
}

// EnableSilenceFill enables fill mode with output PT pt and a priority input
// on prioPort (0 = no priority input). Call before Start.
func (r *RTPRelay) EnableSilenceFill(pt byte, prioPort int) {
	r.fill = &relayFill{pt: pt, prioPort: prioPort, ssrc: rand.Uint32()}
}

// startFill opens the priority input and starts the silence clock.
func (r *RTPRelay) startFill() error {
	f := r.fill
	f.start = time.Now()
	if f.prioPort > 0 {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: f.prioPort})
		if err != nil {
			return err
		}
		f.prioConn = c
		go r.prioLoop(c)
	}
	go r.fillTickLoop()
	return nil
}

func (r *RTPRelay) stopFill() {
	if r.fill != nil && r.fill.prioConn != nil {
		r.fill.prioConn.Close()
	}
}

// fillMain handles a packet of the main input in fill mode.
func (r *RTPRelay) fillMain(packet []byte) {
	f := r.fill
	payload := rtpPayload(packet)
	if payload == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Since(f.lastPrio) < fillPrioHold {
		return // the priority input wins
	}
	f.lastIn = time.Now()
	r.emitLocked(payload)
}

// prioLoop reads the priority input (the panel view's listening).
func (r *RTPRelay) prioLoop(c *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			return // closed by Stop
		}
		payload := rtpPayload(buf[:n])
		if payload == nil {
			continue
		}
		atomic.AddUint64(&r.packetsReceived, 1)
		f := r.fill
		f.mu.Lock()
		now := time.Now()
		f.lastPrio, f.lastIn = now, now
		r.emitLocked(payload)
		f.mu.Unlock()
	}
}

// fillTickLoop injects silence when no source sends audio.
func (r *RTPRelay) fillTickLoop() {
	t := time.NewTicker(keepaliveTick)
	defer t.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-t.C:
			if r.ConsumerCount() == 0 {
				continue
			}
			f := r.fill
			f.mu.Lock()
			now := time.Now()
			if now.Sub(f.lastIn) >= fillIdle && now.Sub(f.lastOut) >= keepaliveFrame {
				r.emitLocked(speexSilenceFrame)
			}
			f.mu.Unlock()
		}
	}
}

// emitLocked builds the outgoing RTP (own PT/SSRC/sequence, timestamp from the
// real clock for a continuous timeline) and fans it out. Assumes fill.mu is held.
func (r *RTPRelay) emitLocked(payload []byte) {
	f := r.fill
	now := time.Now()
	pkt := make([]byte, 12+len(payload))
	pkt[0], pkt[1] = 0x80, f.pt
	binary.BigEndian.PutUint16(pkt[2:4], f.seq)
	binary.BigEndian.PutUint32(pkt[4:8], uint32(now.Sub(f.start).Nanoseconds()*keepaliveRate/int64(time.Second)))
	binary.BigEndian.PutUint32(pkt[8:12], f.ssrc)
	copy(pkt[12:], payload)
	f.seq++
	f.lastOut = now
	r.fanOut(pkt)
}
