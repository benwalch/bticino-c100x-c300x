package sip

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// mediaDrainBufSize is the read buffer size per packet. A typical SRTP packet
// (audio or packetized H.264 video) fits easily in 2048 bytes; anything larger
// is simply truncated by ReadFrom (the content does not matter, only draining it).
const mediaDrainBufSize = 2048

// mediaDrainLogInterval is the interval of the drained packets/bytes summary.
const mediaDrainLogInterval = 2 * time.Second

// mediaDrain drains (reads and discards) the SRTP media bt_vct sends directly to the ports
// negotiated in the answerer's SDP. The bridge neither decrypts nor uses this traffic (the
// useful part arrives in cleartext through the lo:5000/5007 taps); the drain only exists so
// the kernel does not answer with ICMP port-unreachable, which could cut the entrance panel's
// RTP/SRTP call.
type mediaDrain struct {
	audioConn *net.UDPConn
	videoConn *net.UDPConn
	logger    *logrus.Logger

	// Atomic counters. atomic.Int64 is used (not a raw int64 + atomic.AddInt64)
	// because on 32-bit ARM (GOARCH=arm, our target) an int64 is only 4-byte
	// aligned unless it is the first struct field, and atomic.AddInt64 on an
	// address not aligned to 8 causes "panic: unaligned 64-bit atomic
	// operation" (a process crash when the drain is armed on a real call).
	// atomic.Int64 is 8-byte aligned on all platforms.
	packetsAudio atomic.Int64
	bytesAudio   atomic.Int64
	packetsVideo atomic.Int64
	bytesVideo   atomic.Int64

	ticker   *time.Ticker
	done     chan struct{}
	stopOnce sync.Once
}

// startMediaDrain opens loopback UDP sockets (127.0.0.1) for audioPort and, if videoPort>0,
// also for videoPort, and starts one read goroutine per socket plus a periodic log goroutine.
// If audioPort==0 (or videoPort==0 together with audioPort==0, see note) an ephemeral port
// chosen by the OS is used, meant for tests. In use, the concrete ports of AnswererConfig are
// passed (10100/10102 by default).
func startMediaDrain(audioPort, videoPort int, logger *logrus.Logger) (*mediaDrain, error) {
	if logger == nil {
		logger = logrus.New()
	}
	audioConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: audioPort})
	if err != nil {
		return nil, err
	}
	d := &mediaDrain{
		audioConn: audioConn,
		logger:    logger,
		ticker:    time.NewTicker(mediaDrainLogInterval),
		done:      make(chan struct{}),
	}
	if videoPort > 0 {
		videoConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: videoPort})
		if err != nil {
			d.ticker.Stop()
			audioConn.Close()
			return nil, err
		}
		d.videoConn = videoConn
	}
	go d.readLoop(d.audioConn, &d.packetsAudio, &d.bytesAudio)
	if d.videoConn != nil {
		go d.readLoop(d.videoConn, &d.packetsVideo, &d.bytesVideo)
	}
	// note: readLoop gets pointers to atomic.Int64 (they are not copied:
	// atomic.Int64 contains noCopy), so &d.field is passed directly.
	go d.logLoop()
	return d, nil
}

// readLoop reads and discards packets from conn until Stop() closes the socket (ReadFrom returns
// an error and the goroutine exits).
func (d *mediaDrain) readLoop(conn *net.UDPConn, packets, bytes *atomic.Int64) {
	buf := make([]byte, mediaDrainBufSize)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return // socket closed by Stop(): normal end of the goroutine
		}
		packets.Add(1)
		bytes.Add(int64(n))
	}
}

// logLoop summarizes every ~2 s how much was drained, or exits when done closes (Stop()).
func (d *mediaDrain) logLoop() {
	for {
		select {
		case <-d.done:
			return
		case <-d.ticker.C:
			d.logger.Debugf(
				"[ANSWERER] media drain: audio %d packets/%d bytes, video %d packets/%d bytes",
				d.packetsAudio.Load(), d.bytesAudio.Load(),
				d.packetsVideo.Load(), d.bytesVideo.Load(),
			)
		}
	}
}

// Stop closes the sockets (unblocks ReadFrom, so the read goroutines exit) and stops the log
// ticker. Idempotent: calling it twice does not panic.
func (d *mediaDrain) Stop() {
	d.stopOnce.Do(func() {
		close(d.done)
		d.ticker.Stop()
		if d.audioConn != nil {
			d.audioConn.Close()
		}
		if d.videoConn != nil {
			d.videoConn.Close()
		}
	})
}

// AudioAddr returns the local address of the audio socket (useful in tests with an ephemeral port).
func (d *mediaDrain) AudioAddr() string {
	if d.audioConn == nil {
		return ""
	}
	return d.audioConn.LocalAddr().String()
}

// VideoAddr returns the local address of the video socket, or "" if it was not opened (videoPort<=0).
func (d *mediaDrain) VideoAddr() string {
	if d.videoConn == nil {
		return ""
	}
	return d.videoConn.LocalAddr().String()
}

// PacketsAudio returns the number of audio packets drained so far.
func (d *mediaDrain) PacketsAudio() int64 {
	return d.packetsAudio.Load()
}

// PacketsVideo returns the number of video packets drained so far.
func (d *mediaDrain) PacketsVideo() int64 {
	return d.packetsVideo.Load()
}
