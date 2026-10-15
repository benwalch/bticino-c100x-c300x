package sip

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// nativeDevAddrPattern matches the SDP unit address in loopback SIP dialogs.
var nativeDevAddrPattern = regexp.MustCompile(`a=DEVADDR:([0-9a-fA-F-]{8}-[0-9a-fA-F-]{4}-[0-9a-fA-F-]{4}-[0-9a-fA-F-]{4}-[0-9a-fA-F-]{12})`)

// discoverTimeout bounds one passive discovery arm.
const discoverTimeout = 30 * time.Second

// DiscoverTimeout reports the discovery arm duration.
func DiscoverTimeout() time.Duration { return discoverTimeout }

// NativeDevAddrDiscoverer passively sniffs the loopback SIP leg for a genuine
// (non-bridge) dialog carrying a=DEVADDR, e.g. during an app call. Read-only:
// the AF_PACKET tap never transmits.
type NativeDevAddrDiscoverer struct {
	newSource func() (frameSource, error)
	logger    *logrus.Logger

	mutex   sync.Mutex
	running bool
	stop    chan struct{}
	value   string
}

// NewNativeDevAddrDiscoverer creates the sniffer (opens nothing yet).
func NewNativeDevAddrDiscoverer(logger *logrus.Logger) *NativeDevAddrDiscoverer {
	if logger == nil {
		logger = logrus.New()
	}
	return &NativeDevAddrDiscoverer{
		newSource: func() (frameSource, error) { return newAFPacketSource("lo") },
		logger:    logger,
	}
}

// Start arms the sniffer until timeout or Stop. Single-flight.
func (d *NativeDevAddrDiscoverer) Start(timeout time.Duration) error {
	d.mutex.Lock()
	if d.running {
		d.mutex.Unlock()
		return fmt.Errorf("discovery already running")
	}
	src, err := d.newSource()
	if err != nil {
		d.mutex.Unlock()
		return err
	}
	d.running = true
	d.value = ""
	d.stop = make(chan struct{})
	d.mutex.Unlock()
	go d.run(src, timeout)
	return nil
}

// Stop disarms early. Silent when idle.
func (d *NativeDevAddrDiscoverer) Stop() {
	d.mutex.Lock()
	if d.running && d.stop != nil {
		close(d.stop)
	}
	d.mutex.Unlock()
}

// Status reports state ("idle", "waiting", "found") and the value, if any.
func (d *NativeDevAddrDiscoverer) Status() (string, string) {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if !d.running {
		if d.value != "" {
			return "found", d.value
		}
		return "idle", ""
	}
	return "waiting", ""
}

func (d *NativeDevAddrDiscoverer) run(src frameSource, timeout time.Duration) {
	defer src.Close()
	defer func() {
		d.mutex.Lock()
		d.running = false
		d.mutex.Unlock()
	}()
	deadline := time.Now().Add(timeout)
	streams := map[flowKey]*streamBuf{}
	buf := make([]byte, 65536)
	for {
		select {
		case <-d.stop:
			return
		default:
		}
		if time.Now().After(deadline) {
			return
		}
		n, _, err := src.ReadFrame(buf)
		if err != nil {
			if isRetryableReadError(err) {
				continue
			}
			return
		}
		if value, ok := scanSIPFrame(buf[:n], streams); ok {
			d.mutex.Lock()
			d.value = value
			running := d.running
			d.mutex.Unlock()
			if running {
				d.logger.Infof("native dev addr discovery: found %s", value)
			}
			return
		}
	}
}

// flowKey identifies one TCP direction on the loopback leg.
type flowKey struct {
	srcPort, dstPort int
}

// streamBuf accumulates one direction until a full SIP message arrives.
type streamBuf struct {
	data []byte
}

// scanSIPFrame feeds one captured frame into the reassembly buffers and
// returns the first genuine DEVADDR found in a complete message.
func scanSIPFrame(frame []byte, streams map[flowKey]*streamBuf) (string, bool) {
	srcPort, dstPort, payload, ok := extractTCPPayload(frame)
	if !ok {
		return "", false
	}
	if srcPort != 5060 && dstPort != 5060 {
		return "", false
	}
	key := flowKey{srcPort: srcPort, dstPort: dstPort}
	sb, ok := streams[key]
	if !ok {
		sb = &streamBuf{}
		streams[key] = sb
	}
	sb.data = append(sb.data, payload...)
	for {
		msg, rest, ok := splitSIPMessage(sb.data)
		if !ok {
			sb.data = rest
			return "", false
		}
		sb.data = rest
		if isBridgeDialog(msg) {
			continue
		}
		if m := nativeDevAddrPattern.FindSubmatch(msg); m != nil {
			return string(m[1]), true
		}
	}
}

// isBridgeDialog skips our own loopback dialogs (we put our own,
// possibly wrong, value there).
func isBridgeDialog(msg []byte) bool {
	return bytes.Contains(msg, []byte("sip:webrtc@"))
}

// splitSIPMessage cuts one complete SIP message (headers + declared body)
// off the front of buf.
func splitSIPMessage(buf []byte) (msg, rest []byte, ok bool) {
	head := bytes.Index(buf, []byte("\r\n\r\n"))
	if head < 0 {
		return nil, buf, false
	}
	end := head + 4
	length := 0
	for _, line := range bytes.Split(buf[:head], []byte("\r\n")) {
		if len(line) > 15 && bytes.HasPrefix(bytes.ToLower(line), []byte("content-length:")) {
			fmt.Sscanf(string(line[15:]), "%d", &length)
		}
	}
	if len(buf) < end+length {
		return nil, buf, false
	}
	return buf[:end+length], buf[end+length:], true
}

// extractTCPPayload parses one AF_PACKET frame (Ethernet on lo) and returns
// the TCP ports plus payload. Non-IPv4/TCP or fragmented input is skipped.
func extractTCPPayload(frame []byte) (srcPort, dstPort int, payload []byte, ok bool) {
	const ethLen = 14
	if len(frame) < ethLen+20+20 {
		return 0, 0, nil, false
	}
	if frame[12] != 0x08 || frame[13] != 0x00 {
		return 0, 0, nil, false
	}
	ip := frame[ethLen:]
	if ip[0]>>4 != 4 {
		return 0, 0, nil, false
	}
	ihl := int(ip[0]&0x0f) * 4
	if ihl < 20 || len(ip) < ihl+20 {
		return 0, 0, nil, false
	}
	if ip[9] != 6 {
		return 0, 0, nil, false
	}
	if binary.BigEndian.Uint16(ip[6:8])&0x1fff != 0 {
		return 0, 0, nil, false
	}
	tcp := ip[ihl:]
	srcPort = int(binary.BigEndian.Uint16(tcp[0:2]))
	dstPort = int(binary.BigEndian.Uint16(tcp[2:4]))
	headerLen := int(tcp[12]>>4) * 4
	if headerLen < 20 || len(tcp) < headerLen {
		return 0, 0, nil, false
	}
	return srcPort, dstPort, tcp[headerLen:], true
}
