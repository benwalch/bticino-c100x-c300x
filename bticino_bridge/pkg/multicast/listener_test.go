package multicast

import (
	"testing"
)

// buildDatagram builds a synthetic datagram with the binary format of the
// BTicino multicast syslog: 8-byte header, NUL-terminated system name,
// `meta` metadata bytes after the NUL and NUL-terminated message.
// The parser skips 13 bytes from the NUL inclusive → meta=12 for the standard one.
func buildDatagram(system string, meta int, message string) []byte {
	d := make([]byte, 0, 8+len(system)+1+meta+len(message)+1)
	d = append(d, []byte{0x1e, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}...) // header
	d = append(d, []byte(system)...)
	d = append(d, 0)
	for i := 0; i < meta; i++ {
		d = append(d, 0x02) // binary metadata (non-NUL)
	}
	d = append(d, []byte(message)...)
	d = append(d, 0)
	return d
}

func TestParseBinaryDatagramOpen(t *testing.T) {
	frame := "*8*19*20##"
	system, msg, ok := parseBinaryDatagram(buildDatagram("OPEN", 12, frame))
	if !ok {
		t.Fatal("expected ok=true for valid OPEN datagram")
	}
	if system != "OPEN" {
		t.Errorf("system = %q, want OPEN", system)
	}
	if msg != frame {
		t.Errorf("msg = %q, want %q", msg, frame)
	}
}

func TestParseBinaryDatagramRegistration(t *testing.T) {
	// REGISTRATION carries 4 extra metadata bytes
	system, msg, ok := parseBinaryDatagram(buildDatagram("REGISTRATION", 16, "registered"))
	if !ok {
		t.Fatal("expected ok=true for valid REGISTRATION datagram")
	}
	if system != "REGISTRATION" {
		t.Errorf("system = %q, want REGISTRATION", system)
	}
	// msgStart is sysEnd+13, so the first 4 bytes of extra metadata
	// are part of the prefix: the useful message is at the end
	if len(msg) == 0 || msg[len(msg)-len("registered"):] != "registered" {
		t.Errorf("msg = %q, want suffix %q", msg, "registered")
	}
}

func TestParseBinaryDatagramTooShort(t *testing.T) {
	if _, _, ok := parseBinaryDatagram([]byte{0x01, 0x02, 0x03}); ok {
		t.Error("expected ok=false for short datagram")
	}
}

func TestParseBinaryDatagramNoNul(t *testing.T) {
	d := make([]byte, 32)
	for i := range d {
		d[i] = 'A'
	}
	if _, _, ok := parseBinaryDatagram(d); ok {
		t.Error("expected ok=false when system name has no NUL terminator")
	}
}

func TestParseBinaryDatagramNonPrintableSystem(t *testing.T) {
	d := buildDatagram("OP\x01N", 13, "*1*1*1##")
	if _, _, ok := parseBinaryDatagram(d); ok {
		t.Error("expected ok=false for non-printable system name")
	}
}

func TestParseMessageBinaryOpenExtractsFrame(t *testing.T) {
	ml := NewMulticastListener(nil, nil)
	// The message contains text around the OpenWebNet frame
	msg := ml.parseMessage(buildDatagram("OPEN", 12, "rx frame *7*59#1*10## end"))
	if !msg.Parsed {
		t.Fatal("expected Parsed=true")
	}
	if msg.System != "OPEN" {
		t.Errorf("System = %q, want OPEN", msg.System)
	}
	if msg.Message != "*7*59#1*10##" {
		t.Errorf("Message = %q, want %q", msg.Message, "*7*59#1*10##")
	}
}

func TestParseMessageTextFallback(t *testing.T) {
	ml := NewMulticastListener(nil, nil)
	// Plain-text datagram (without valid binary structure)
	msg := ml.parseMessage([]byte("ASWM voicemail state changed"))
	if msg.System != "ASWM" {
		t.Errorf("System = %q, want ASWM (text fallback)", msg.System)
	}
}
