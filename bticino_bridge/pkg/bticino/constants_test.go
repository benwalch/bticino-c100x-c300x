package bticino

import "testing"

func TestIsCallHangup(t *testing.T) {
	hangups := []string{
		"*8*3#1#4*416##", // incoming, 300X
		"*8*3#5#4*416##", // self, 300X
		"*8*3#5#4*420##", // self WHERE=420
		"*8*3#1#4*411##", // incoming WHERE=411, 100X
		"*8*3#1#4*999##", // any future WHERE
	}
	for _, raw := range hangups {
		if !IsCallHangup(raw) {
			t.Errorf("IsCallHangup(%q) = false, want true", raw)
		}
	}

	notHangups := []string{
		"",
		"*#*1##",
		"*8*19*20##",       // door open
		"*8*30*20##",       // mute ON (WHAT=30, must not match *8*3#)
		"*8*31*20##",       // mute OFF
		"*8*1#1#4#21*16##", // doorbell
		"*8*91##",          // voicemail on
		"*7*55*##",         // video stop
		"*8*3#1#4*411#",    // missing terminator
	}
	for _, raw := range notHangups {
		if IsCallHangup(raw) {
			t.Errorf("IsCallHangup(%q) = true, want false", raw)
		}
	}
}
