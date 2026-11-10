package bticino

import "strings"

// IsMediaChannelUp reports whether a raw OpenWebNet bus frame (as it comes
// from the monitor on port 20000) announces a native media channel going up:
// audio (lo:5000) or video (lo:5007). Real form captured 2026-08-22:
//
//	*7*300#127#0#0#1#5000#2*##   (audio)
//	*7*300#127#0#0#1#5007#0*##   (video)
//
// It is the reliable "media is on that port" signal for the app's native
// auto-activation (which sends NO SIP INVITE). Pure function: it only
// classifies and sends nothing on the bus.
func IsMediaChannelUp(raw string) bool {
	// WHO=7, WHAT with the media channel prefix 300#.
	if !strings.HasPrefix(raw, "*7*300#") {
		return false
	}
	// Media port token (delimited by # on both sides so that 5002 or other
	// ports do not match a 5000/5007 substring).
	return strings.Contains(raw, "#5000#") || strings.Contains(raw, "#5007#")
}
