package sip

import (
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"syscall"

	"bticino_bridge/pkg/bticino"
	"github.com/sirupsen/logrus"
)

// Native video RTP ports: loopback ports to which bt_av_media sends the
// H.264 video (RTP PT=96, IN CLEAR) during a call, i.e. the local SIP legs
// the firmware itself sets up with its *7*300. The 300X serves high-res
// (type 0 → 5007); the 100X has no high-res leg, only low-res (type 1 →
// 5002). Verified in Phase 0 on 2026-08-18: sustained flow (~205 pps) from
// ring until hangup.
const (
	NativeVideoRTPPort300X = 5007
	NativeVideoRTPPort100X = 5002
)

// NativeVideoRTPPortForVariant returns the tap source port for the given
// device variant ("c300x" / "c100x", see bticino.DeviceVariant). Unknown
// variants fall back to the 300X high-res leg.
func NativeVideoRTPPortForVariant(variant string) int {
	if variant == bticino.DeviceVariant100X {
		return NativeVideoRTPPort100X
	}
	return NativeVideoRTPPort300X
}

// frameSource abstracts the raw frame source (real AF_PACKET on the device,
// a synthetic source in tests).
type frameSource interface {
	// ReadFrame fills buf and returns the size and the pkttype (PACKET_HOST,
	// PACKET_OUTGOING...) of the frame. Timeout error → retryable.
	ReadFrame(buf []byte) (n int, pkttype byte, err error)
	Close() error
}

// LoopbackRTPTap is a PASSIVE mirror: it captures on `lo` the UDP datagrams
// destined to srcPort (the native video leg) and forwards their RTP payload to
// 127.0.0.1:dstPort (the relay input). It sends NO command to the bus nor
// opens sockets competing with native processes (non-interference pattern):
// the AF_PACKET socket is listen-only, like tcpdump/minicap.
type LoopbackRTPTap struct {
	srcPort int
	dstPort int
	logger  *logrus.Logger

	// newSource is injectable in tests; by default it opens AF_PACKET on `lo`.
	newSource func() (frameSource, error)

	mu      sync.Mutex
	source  frameSource
	conn    *net.UDPConn
	running bool
}

// NewLoopbackRTPTap creates the tap (without opening anything yet).
func NewLoopbackRTPTap(srcPort, dstPort int, logger *logrus.Logger) *LoopbackRTPTap {
	if logger == nil {
		logger = logrus.New()
	}
	t := &LoopbackRTPTap{srcPort: srcPort, dstPort: dstPort, logger: logger}
	t.newSource = func() (frameSource, error) { return newAFPacketSource("lo") }
	return t
}

// Start opens the capture and starts the forwarding loop. Idempotent.
func (t *LoopbackRTPTap) Start() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running {
		return nil
	}
	src, err := t.newSource()
	if err != nil {
		return fmt.Errorf("tap: opening capture on lo: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: t.dstPort})
	if err != nil {
		src.Close()
		return fmt.Errorf("tap: opening destination 127.0.0.1:%d: %w", t.dstPort, err)
	}
	t.source = src
	t.conn = conn
	t.running = true
	go t.forwardLoop(src, conn)
	t.logger.Infof("rtp-tap: passive mirror lo:%d → 127.0.0.1:%d", t.srcPort, t.dstPort)
	return nil
}

// forwardLoop reads frames and forwards the UDP payload of those going to srcPort.
// On `lo` each packet is delivered twice (PACKET_OUTGOING when sent and
// PACKET_HOST when received); only the PACKET_HOST copy is processed.
func (t *LoopbackRTPTap) forwardLoop(src frameSource, conn *net.UDPConn) {
	buf := make([]byte, 65536)
	var forwarded uint64
	for {
		n, pkttype, err := src.ReadFrame(buf)
		if err != nil {
			if isRetryableReadError(err) {
				continue
			}
			t.mu.Lock()
			running := t.running
			t.mu.Unlock()
			if running {
				t.logger.WithError(err).Debug("rtp-tap: capture finished")
			}
			t.logger.Infof("rtp-tap: stopped (%d mirrored packets)", forwarded)
			return
		}
		if pkttype == syscall.PACKET_OUTGOING {
			continue
		}
		dstPort, payload, ok := extractUDPPayload(buf[:n])
		if !ok || dstPort != t.srcPort || len(payload) == 0 {
			continue
		}
		if _, err := conn.Write(payload); err == nil {
			forwarded++
		}
	}
}

// Stop closes the capture and the destination. Idempotent.
func (t *LoopbackRTPTap) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.running {
		return
	}
	t.running = false
	if t.source != nil {
		t.source.Close()
		t.source = nil
	}
	if t.conn != nil {
		t.conn.Close()
		t.conn = nil
	}
}

// extractUDPPayload parses an Ethernet+IPv4+UDP frame (`lo` format)
// and returns the destination port and the UDP payload. ok=false if the frame
// is not IPv4/UDP or is truncated. It does not verify checksums (loopback needs none).
func extractUDPPayload(frame []byte) (dstPort int, payload []byte, ok bool) {
	const ethLen = 14
	if len(frame) < ethLen+20+8 {
		return 0, nil, false
	}
	if frame[12] != 0x08 || frame[13] != 0x00 { // ethertype IPv4
		return 0, nil, false
	}
	ip := frame[ethLen:]
	if ip[0]>>4 != 4 {
		return 0, nil, false
	}
	ihl := int(ip[0]&0x0f) * 4
	if ihl < 20 || len(ip) < ihl+8 {
		return 0, nil, false
	}
	if ip[9] != 17 { // UDP
		return 0, nil, false
	}
	if binary.BigEndian.Uint16(ip[6:8])&0x1fff != 0 { // non-initial fragment
		return 0, nil, false
	}
	udp := ip[ihl:]
	dstPort = int(binary.BigEndian.Uint16(udp[2:4]))
	udpLen := int(binary.BigEndian.Uint16(udp[4:6]))
	if udpLen < 8 || len(udp) < udpLen {
		return 0, nil, false
	}
	return dstPort, udp[8:udpLen], true
}

// afpacketSource is the real source: AF_PACKET (read-only) bound to an
// interface, with a read timeout so it can stop cleanly.
type afpacketSource struct {
	fd int
}

func newAFPacketSource(iface string) (*afpacketSource, error) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}
	proto := htons(syscall.ETH_P_ALL)
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW, int(proto))
	if err != nil {
		return nil, err
	}
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: proto, Ifindex: ifi.Index}); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	// Read timeout: lets the loop observe the close without staying
	// blocked in Recvfrom forever.
	tv := syscall.Timeval{Sec: 0, Usec: 500000}
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	return &afpacketSource{fd: fd}, nil
}

func (s *afpacketSource) ReadFrame(buf []byte) (int, byte, error) {
	n, from, err := syscall.Recvfrom(s.fd, buf, 0)
	if err != nil {
		return 0, 0, err
	}
	var pkttype byte
	if sll, ok := from.(*syscall.SockaddrLinklayer); ok {
		pkttype = sll.Pkttype
	}
	return n, pkttype, nil
}

func (s *afpacketSource) Close() error {
	return syscall.Close(s.fd)
}

// isRetryableReadError distinguishes timeouts/interruptions (retry) from a
// real close of the source (exit the loop).
func isRetryableReadError(err error) bool {
	return err == syscall.EAGAIN || err == syscall.EWOULDBLOCK || err == syscall.EINTR
}

func htons(v uint16) uint16 {
	return v<<8 | v>>8
}
