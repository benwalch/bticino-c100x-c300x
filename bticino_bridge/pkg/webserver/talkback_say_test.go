package webserver

import "testing"

func TestValidateAudioFilePath(t *testing.T) {
	ok := []string{
		"/tmp/hola.wav",
		"/tmp/sub/dir/audio.wav",
		"/home/bticino/cfg/extra/prompt.wav",
	}
	for _, p := range ok {
		if err := validateAudioFilePath(p); err != nil {
			t.Errorf("expected %q to be valid, error: %v", p, err)
		}
	}

	bad := []string{
		"",                      // empty
		"relative.wav",          // not absolute
		"../../etc/passwd",      // relative with traversal
		"/tmp/../etc/passwd",    // traversal escaping to /etc
		"/etc/passwd",           // outside the allowed directories
		"/home/bticino/.ssh/id", // outside the allowed subdirectory
	}
	for _, p := range bad {
		if err := validateAudioFilePath(p); err == nil {
			t.Errorf("expected %q to be rejected, but it passed", p)
		}
	}
}
