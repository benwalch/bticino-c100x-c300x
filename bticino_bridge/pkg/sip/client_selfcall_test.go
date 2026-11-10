package sip

import (
	"testing"
	"time"
)

func TestShouldAutoAnswerIgnoresOwnCall(t *testing.T) {
	c := &BTicinoSIPClient{originatedCalls: map[string]time.Time{}, nativeCalls: map[string]time.Time{}}
	c.originatedCalls["my-call-id@127.0.0.1"] = time.Now()
	c.nativeCalls["my-call-id@127.0.0.1"] = time.Now() // InviteWithSDP (panel view)
	if c.shouldAutoAnswer("my-call-id@127.0.0.1") {
		t.Fatal("must not auto-answer an INVITE with our own Call-ID (loopback)")
	}
	if !c.shouldAutoAnswer("external@other") {
		t.Fatal("must auto-answer an external INVITE")
	}
}
