package sip

import (
	"encoding/base64"
	"testing"
)

func TestNewSRTPMasterKeyLength(t *testing.T) {
	k, err := NewSRTPMasterKey()
	if err != nil {
		t.Fatalf("NewSRTPMasterKey: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(k)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	if len(raw) != 30 {
		t.Fatalf("expected 30 bytes (16 key + 14 salt), got %d", len(raw))
	}
}

func TestNewSRTPMasterKeyIsRandom(t *testing.T) {
	a, _ := NewSRTPMasterKey()
	b, _ := NewSRTPMasterKey()
	if a == b {
		t.Fatal("two consecutive keys should not match")
	}
}
