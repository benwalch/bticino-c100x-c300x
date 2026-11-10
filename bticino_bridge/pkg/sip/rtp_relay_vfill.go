package sip

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"math/rand"
	"sync"
	"time"
)

// placeholderH264 is a black IDR frame with the SAME header as the entrance
// panel (Constrained Baseline 3.0, 688x480 progressive, measured in lo:5007 pcaps):
//
//	ffmpeg -f lavfi -i color=c=black:s=688x480:r=1 -frames:v 1 -pix_fmt yuv420p \
//	       -c:v libx264 -profile:v baseline -level 3.0 -x264-params keyint=1:repeat-headers=1 -f h264
//
//go:embed placeholder_688x480.h264
var placeholderH264 []byte

const (
	vfillIdle     = 1500 * time.Millisecond // no real video for this long => idle background
	vfillInterval = time.Second             // background rate (1 fps)
	vfillTick     = 100 * time.Millisecond
	vfillMTU      = 1200 // largest RTP payload before FU-A fragmentation
	videoClock    = 90000
)

// videoFill is an RTPRelay's "video RTSP never silent" mode.
//
// Why: go2rtc's `doorbell` stream is an ffmpeg exec that dies after a 30 s
// timeout without video, and go2rtc restarts it every ~90 s. If the panel view
// (30 s) falls into that gap, the HA card shows nothing; if it falls inside,
// the picture arrives late. With a black 1 fps background at rest, ffmpeg
// stays connected and real video passes at once. Everything goes out with ONE
// SSRC and a continuous sequence; real video keeps its marker and timestamp
// spacing (shifted to continue the timeline).
type videoFill struct {
	pt     byte
	ssrc   uint32
	start  time.Time
	frame  [][]byte // RTP payloads of the background (SPS, PPS, fragmented IDR)
	seq    uint16
	mu     sync.Mutex
	srcID  uint32 // SSRC of the current real source
	inReal bool
	offset uint32 // timestamp offset of the real source

	lastReal, lastFill time.Time
}

// EnableVideoFill enables the idle background with video PT pt. Call before Start.
func (r *RTPRelay) EnableVideoFill(pt byte) {
	r.vfill = &videoFill{pt: pt, ssrc: rand.Uint32(), frame: packetizeH264(annexBNALs(placeholderH264), vfillMTU)}
}

func (v *videoFill) wallTS(now time.Time) uint32 {
	return uint32(now.Sub(v.start).Nanoseconds() * videoClock / int64(time.Second))
}

// vfillMain rewrites a real video packet.
func (r *RTPRelay) vfillMain(packet []byte) {
	v := r.vfill
	payload := rtpPayload(packet)
	if payload == nil {
		return
	}
	marker := packet[1]&0x80 != 0
	srcTS := binary.BigEndian.Uint32(packet[4:8])
	srcID := binary.BigEndian.Uint32(packet[8:12])
	v.mu.Lock()
	defer v.mu.Unlock()
	now := time.Now()
	if !v.inReal || srcID != v.srcID {
		// New source (or back from the background): continue the output timeline.
		v.offset = v.wallTS(now) - srcTS
		v.srcID, v.inReal = srcID, true
	}
	v.lastReal = now
	r.vemitLocked(payload, marker, srcTS+v.offset)
}

// vfillTickLoop sends the background when no real video arrives and there are consumers.
func (r *RTPRelay) vfillTickLoop() {
	t := time.NewTicker(vfillTick)
	defer t.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-t.C:
			if r.ConsumerCount() == 0 {
				continue
			}
			v := r.vfill
			v.mu.Lock()
			now := time.Now()
			if now.Sub(v.lastReal) >= vfillIdle && now.Sub(v.lastFill) >= vfillInterval {
				v.inReal = false
				ts := v.wallTS(now)
				for i, p := range v.frame {
					r.vemitLocked(p, i == len(v.frame)-1, ts)
				}
				v.lastFill = now
			}
			v.mu.Unlock()
		}
	}
}

// vemitLocked builds the outgoing RTP and fans it out. Assumes v.mu is held.
func (r *RTPRelay) vemitLocked(payload []byte, marker bool, ts uint32) {
	v := r.vfill
	pkt := make([]byte, 12+len(payload))
	pkt[0], pkt[1] = 0x80, v.pt
	if marker {
		pkt[1] |= 0x80
	}
	binary.BigEndian.PutUint16(pkt[2:4], v.seq)
	binary.BigEndian.PutUint32(pkt[4:8], ts)
	binary.BigEndian.PutUint32(pkt[8:12], v.ssrc)
	copy(pkt[12:], payload)
	v.seq++
	r.fanOut(pkt)
}

// annexBNALs splits an Annex-B stream into NALs, dropping SEI (type 6) and AUD (9).
func annexBNALs(b []byte) [][]byte {
	var out [][]byte
	for _, n := range bytes.Split(b, []byte{0, 0, 1}) {
		n = bytes.TrimRight(n, "\x00") // zeros of a 4-byte start code
		if len(n) == 0 {
			continue
		}
		if t := n[0] & 31; t == 6 || t == 9 {
			continue
		}
		out = append(out, n)
	}
	return out
}

// packetizeH264 packs NALs into RTP payloads (RFC 6184): a single NAL if it
// fits, FU-A fragmentation otherwise.
func packetizeH264(nals [][]byte, mtu int) [][]byte {
	var out [][]byte
	for _, n := range nals {
		if len(n) <= mtu {
			out = append(out, n)
			continue
		}
		hdr, body := n[0], n[1:]
		for first := true; len(body) > 0; first = false {
			chunk := mtu - 2
			if chunk > len(body) {
				chunk = len(body)
			}
			fuh := hdr & 31
			if first {
				fuh |= 0x80
			}
			if chunk == len(body) {
				fuh |= 0x40
			}
			p := append([]byte{hdr&0xe0 | 28, fuh}, body[:chunk]...)
			out = append(out, p)
			body = body[chunk:]
		}
	}
	return out
}
