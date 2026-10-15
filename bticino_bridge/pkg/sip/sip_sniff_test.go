package sip

import (
	"encoding/binary"
	"testing"

	"github.com/sirupsen/logrus"
)

func tcpFrame(srcPort, dstPort int, payload []byte) []byte {
	frame := make([]byte, 14+20+20+len(payload))
	frame[12], frame[13] = 0x08, 0x00
	ip := frame[14:]
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(20+20+len(payload)))
	ip[9] = 6
	copy(ip[12:16], []byte{127, 0, 0, 1})
	copy(ip[16:20], []byte{127, 0, 0, 1})
	tcp := ip[20:]
	binary.BigEndian.PutUint16(tcp[0:2], uint16(srcPort))
	binary.BigEndian.PutUint16(tcp[2:4], uint16(dstPort))
	tcp[12] = 0x50
	copy(tcp[20:], payload)
	return frame
}

const sniffBody = "v=0\r\no=caller 1 1 IN IP4 192.168.1.9\r\ns=Talk\r\nt=0 0\r\n" +
	"a=DEVADDR:00000000-0000-4000-8000-000000000001\r\n"

var sniffOffer = "INVITE sip:c100x@127.0.0.1:54688;transport=tcp SIP/2.0\r\n" +
	"Via: SIP/2.0/TCP 127.0.0.1;rport\r\n" +
	"From: <sip:caller@domain>;tag=abc\r\n" +
	"To: <sip:c100x@domain>\r\n" +
	"Call-ID: test123\r\n" +
	"CSeq: 21 INVITE\r\n" +
	"Content-Type: application/sdp\r\n" +
	"Content-Length: 101\r\n" +
	"\r\n" + sniffBody

// A genuine offer split across two TCP segments reassembles and yields.
func TestSniffFindsSplitOffer(t *testing.T) {
	streams := map[flowKey]*streamBuf{}
	raw := []byte(sniffOffer)
	if _, ok := scanSIPFrame(tcpFrame(40000, 5060, raw[:100]), streams); ok {
		t.Fatal("partial message must not match yet")
	}
	value, ok := scanSIPFrame(tcpFrame(40000, 5060, raw[100:]), streams)
	if !ok || value != "00000000-0000-4000-8000-000000000001" {
		t.Fatalf("got %q, %v", value, ok)
	}
}

// Our own dialogs are skipped even when they carry a value.
func TestSniffSkipsBridgeDialogs(t *testing.T) {
	streams := map[flowKey]*streamBuf{}
	own := "INVITE sip:x SIP/2.0\r\nFrom: <sip:webrtc@domain>;tag=t\r\nContent-Length: 48\r\n\r\na=DEVADDR:11111111-2222-3333-4444-555555555555\r\n"
	if _, ok := scanSIPFrame(tcpFrame(40001, 5060, []byte(own)), streams); ok {
		t.Fatal("bridge dialog must be skipped")
	}
}

// Non-SIP ports and non-TCP frames are ignored.
func TestSniffIgnoresOtherTraffic(t *testing.T) {
	streams := map[flowKey]*streamBuf{}
	if _, ok := scanSIPFrame(tcpFrame(40002, 5002, []byte(sniffOffer)), streams); ok {
		t.Fatal("non-5060 traffic must be ignored")
	}
	if _, ok := scanSIPFrame([]byte{0x00, 0x01}, streams); ok {
		t.Fatal("runt frames must be ignored")
	}
}

// Discoverer state machine without a device (injectable source).
func TestDiscovererStatusFlow(t *testing.T) {
	d := NewNativeDevAddrDiscoverer(logrus.New())
	if state, _ := d.Status(); state != "idle" {
		t.Fatalf("fresh state = %q, want idle", state)
	}
	d.mutex.Lock()
	d.running = true
	d.mutex.Unlock()
	if state, _ := d.Status(); state != "waiting" {
		t.Fatalf("armed state = %q, want waiting", state)
	}
	d.Stop()
	d.mutex.Lock()
	d.running = false
	d.value = "00000000-0000-4000-8000-000000000001"
	d.mutex.Unlock()
	if state, value := d.Status(); state != "found" || value == "" {
		t.Fatalf("state = %q value = %q", state, value)
	}
}
