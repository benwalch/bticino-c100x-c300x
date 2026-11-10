// Package access classifies door openings (visitor vs resident) by correlating
// them with earlier rings and calls. It only subscribes to the EventBus: it
// opens no sockets and sends nothing on the bus.
package access

import "time"

// Access labels.
const (
	TypeVisitor  = "visitor"  // opening after a ring or call
	TypeResident = "resident" // opening without an earlier ring or call (RFID, resident)
	TypeUnknown  = "unknown"  // conflicting signals
)

// Confidence levels (how well correlation and duration agree).
const (
	ConfHigh   = "high"
	ConfLow    = "low"
	ConfMedium = "medium" // no duration to decide
)

// ClassifyInput holds the signals known at decision time.
type ClassifyInput struct {
	HadCall       bool          // a ring or call within the window, before the opening
	Duration      time.Duration // time the lock was energized (19→20)
	DurationKnown bool          // false if the close did not arrive in time
	VisitorMax    time.Duration // threshold for a "visitor" pulse
}

// ClassifyResult is the label and its confidence.
type ClassifyResult struct {
	Type       string
	Confidence string
}

// Classify is deterministic and pure. The correlation sets the label, the
// duration sets the confidence; the only real conflict (no call but a short
// pulse) gives "unknown".
func Classify(in ClassifyInput) ClassifyResult {
	if in.HadCall {
		if !in.DurationKnown {
			return ClassifyResult{TypeVisitor, ConfMedium}
		}
		if in.Duration <= in.VisitorMax {
			return ClassifyResult{TypeVisitor, ConfHigh}
		}
		return ClassifyResult{TypeVisitor, ConfLow}
	}
	if !in.DurationKnown {
		return ClassifyResult{TypeResident, ConfMedium}
	}
	if in.Duration > in.VisitorMax {
		return ClassifyResult{TypeResident, ConfHigh}
	}
	return ClassifyResult{TypeUnknown, ConfLow}
}
