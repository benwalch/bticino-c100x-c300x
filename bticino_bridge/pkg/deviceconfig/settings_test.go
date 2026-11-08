package deviceconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// A settings.xml cut off mid-write, as when the firmware is rewriting it.
const truncatedSettingsXML = `<?xml version="1.0" encoding="UTF-8"?>
<settings>
<obj cid="14121" volume="5" id_ringtone="3"><ist uii="1"/></obj>
<obj cid="14122" volu`

func TestReadSettingsXMLReturnsErrorForBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.xml")
	if err := os.WriteFile(path, []byte(truncatedSettingsXML), 0o644); err != nil {
		t.Fatal(err)
	}

	settings, err := ReadSettingsXML(path)
	if err == nil {
		t.Fatalf("got settings %+v, want a parse error", settings)
	}
}
