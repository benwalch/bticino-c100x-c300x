package sip

import (
	"testing"

	"github.com/sirupsen/logrus"
)

// TestVideoActivationGateDisabled is the key safety assertion: with
// video activation disabled (the default), ensureSIPCallActive
// rejects the attempt WITHOUT touching the SIP client (nil here). That way neither an
// RTSP client nor a snapshot can fire commands (self-INVITE / *7*300) at the
// native firmware. Regression of the incident that rebooted the device.
func TestVideoActivationGateDisabled(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel) // silence

	srv := NewEnhancedRTSPServer(6554, nil /*sipClient*/, nil /*eventBus*/, logger)

	// videoActivation is false by default (SetVideoActivation was not called).
	err := srv.ensureSIPCallActive()
	if err == nil {
		t.Fatal("ensureSIPCallActive should reject with activation disabled")
	}
	if err.Error() != "video on-demand activation is disabled" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestVideoActivationGateEnables checks that SetVideoActivation(true) lifts
// the gate (it stops being what blocks).
func TestVideoActivationGateEnables(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)

	srv := NewEnhancedRTSPServer(6554, nil, nil, logger)
	srv.SetVideoActivation(true)
	if !srv.videoActivation {
		t.Fatal("SetVideoActivation(true) did not enable activation")
	}
}
