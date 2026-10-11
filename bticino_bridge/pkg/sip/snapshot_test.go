package sip

import (
	"bytes"
	"testing"
)

func TestScanJPEGFrames(t *testing.T) {
	// Three complete concatenated frames + garbage in between + one
	// incomplete fourth frame (SOI without EOI) at the end.
	frame := func(payload byte) []byte {
		return append(append([]byte{0xFF, 0xD8}, payload, payload, payload), 0xFF, 0xD9)
	}
	var buf bytes.Buffer
	buf.Write([]byte{0x00, 0x11}) // leading garbage
	buf.Write(frame(0xA1))
	buf.Write(frame(0xA2))
	buf.Write(frame(0xA3))
	buf.Write([]byte{0xFF, 0xD8, 0x99}) // incomplete frame

	count, last := scanJPEGFrames(buf.Bytes())
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
	want := frame(0xA3)
	if !bytes.Equal(last, want) {
		t.Errorf("last = %x, want %x (the last complete frame)", last, want)
	}
}

func TestScanJPEGFramesEmpty(t *testing.T) {
	if count, last := scanJPEGFrames([]byte{0x00, 0x01, 0x02}); count != 0 || last != nil {
		t.Errorf("got (%d, %v), want (0, nil)", count, last)
	}
}
