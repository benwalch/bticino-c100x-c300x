package sip

import (
	"testing"
)

func TestSplitExactFillConsumesAll(t *testing.T) {
	raw := []byte("SIP/2.0 200 OK\r\nVia: a\r\nContent-Length: 0\r\n\r\n")
	msgs, consumed := splitSIPStream(raw)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if consumed != len(raw) {
		t.Fatalf("consumed=%d, want %d (replay hazard)", consumed, len(raw))
	}
	// Second pass over the leftover must yield nothing.
	msgs2, consumed2 := splitSIPStream(raw[consumed:])
	if len(msgs2) != 0 || consumed2 != 0 {
		t.Fatalf("leftover re-parsed: msgs=%d consumed=%d", len(msgs2), consumed2)
	}
}

// Trailing blank lines after a message must not rewind the offset.
func TestSplitTrailingBlanksAdvance(t *testing.T) {
	raw := []byte("SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n\r\n")
	msgs, consumed := splitSIPStream(raw)
	if len(msgs) != 1 || consumed != len(raw) {
		t.Fatalf("msgs=%d consumed=%d want 1/%d", len(msgs), consumed, len(raw))
	}
}

// Two coalesced messages split cleanly with no loss.
func TestSplitCoalescedPair(t *testing.T) {
	m1 := "OPTIONS sip:x SIP/2.0\r\nContent-Length: 0\r\n\r\n"
	m2 := "SIP/2.0 200 OK\r\nContent-Length: 0\r\n\r\n"
	msgs, consumed := splitSIPStream([]byte(m1 + m2))
	if len(msgs) != 2 || consumed != len(m1+m2) {
		t.Fatalf("msgs=%d consumed=%d", len(msgs), consumed)
	}
	if msgs[0].Method != "OPTIONS" || msgs[1].StatusCode != "200" {
		t.Fatalf("wrong parse: %+v %+v", msgs[0].Method, msgs[1].StatusCode)
	}
}
