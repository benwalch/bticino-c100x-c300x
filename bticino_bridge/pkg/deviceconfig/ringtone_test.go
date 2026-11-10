package deviceconfig

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// testdata/settings_c100x.xml is the settings.xml of a Classe 100X.
func copyFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/settings_c100x.xml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings.xml")
	if err := os.WriteFile(path, data, 0o664); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o664); err != nil { // WriteFile is subject to the umask
		t.Fatal(err)
	}
	return path
}

func TestReadSettingsXMLRingtonesByObjectID(t *testing.T) {
	settings, err := ReadSettingsXML("testdata/settings_c100x.xml")
	if err != nil {
		t.Fatal(err)
	}
	// <ist> wins over <obj> (S0, S1, Door); the others only have <obj>.
	want := Ringtones{S0: 1, S1: 1, S2: 10, S3: 14, Internal: 8, External: 7, Door: 18, Alarm: 1, Message: 13}
	if settings.Ringtones != want {
		t.Errorf("ringtones = %+v, want %+v", settings.Ringtones, want)
	}
}

func TestWriteRingtoneChangesOnlyTheIstAttribute(t *testing.T) {
	path := copyFixture(t)
	before, _ := os.ReadFile(path)

	if err := WriteRingtone(path, "s0", 4); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	want := bytes.Replace(before, []byte(`<ist id_ringtone="1" uii="52"/>`), []byte(`<ist id_ringtone="4" uii="52"/>`), 1)
	if !bytes.Equal(after, want) {
		t.Errorf("file differs beyond the S0 <ist>:\n%s", after)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o664 {
		t.Errorf("mode = %v, want 0664", info.Mode().Perm())
	}
}

func TestWriteRingtoneAddsMissingAttribute(t *testing.T) {
	path := copyFixture(t)
	if err := WriteRingtone(path, "internal", 3); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte(`<ist id_ringtone="3" uii="56"/>`)) {
		t.Errorf("internal <ist> not set:\n%s", data)
	}
	settings, err := ReadSettingsXML(path)
	if err != nil || settings.Ringtones.Internal != 3 || settings.Ringtones.External != 7 {
		t.Errorf("ringtones = %+v, %v", settings.Ringtones, err)
	}
}

func TestWriteRingtoneRejectsUnknownType(t *testing.T) {
	path := copyFixture(t)
	if err := WriteRingtone(path, "alarm", 2); !errors.Is(err, ErrInvalidRingtone) {
		t.Errorf("err = %v, want ErrInvalidRingtone", err)
	}
}

const ringtonesXML = `<?xml version="1.0" encoding="utf-8"?>
<device_ringtones><ringtones>
<item><id_ringtone>1</id_ringtone><path_ringtone>ring1.wav</path_ringtone></item>
<item><id_ringtone>4</id_ringtone><path_ringtone>ring4.wav</path_ringtone></item>
</ringtones></device_ringtones>`

// setupDevice points the device paths at test files and counts reloads.
func setupDevice(t *testing.T) (settingsPath string, reloads *int) {
	t.Helper()
	settingsPath = copyFixture(t)
	listPath := filepath.Join(t.TempDir(), "ringtones.xml")
	if err := os.WriteFile(listPath, []byte(ringtonesXML), 0o644); err != nil {
		t.Fatal(err)
	}
	oldSettings, oldList, oldReload := DevicePaths.SettingsXML, RingtonesXMLPath, sendReload
	t.Cleanup(func() { DevicePaths.SettingsXML, RingtonesXMLPath, sendReload = oldSettings, oldList, oldReload })
	DevicePaths.SettingsXML, RingtonesXMLPath = settingsPath, listPath
	count := 0
	sendReload = func() error { count++; return nil }
	return settingsPath, &count
}

func TestReadRingtoneList(t *testing.T) {
	_, _ = setupDevice(t)
	list, err := ReadRingtoneList(RingtonesXMLPath)
	if err != nil || len(list) != 2 || list[1] != (RingtoneFile{ID: 4, File: "ring4.wav"}) {
		t.Errorf("list = %+v, %v", list, err)
	}
}

func TestRingtoneFilePath(t *testing.T) {
	_, _ = setupDevice(t)
	path, err := RingtoneFilePath(4)
	if err != nil || path != filepath.Join(filepath.Dir(RingtonesXMLPath), "ring4.wav") {
		t.Errorf("path = %q, %v", path, err)
	}
	if _, err := RingtoneFilePath(7); !errors.Is(err, ErrInvalidRingtone) {
		t.Errorf("err = %v, want ErrInvalidRingtone", err)
	}
}

func TestSetRingtoneWritesAndReloads(t *testing.T) {
	path, reloads := setupDevice(t)
	if err := SetRingtone("floor", 4); err != nil {
		t.Fatal(err)
	}
	settings, _ := ReadSettingsXML(path)
	if settings.Ringtones.Door != 4 || *reloads != 1 {
		t.Errorf("door = %d, reloads = %d; want 4 and 1", settings.Ringtones.Door, *reloads)
	}
}

func TestSetRingtoneRejectsUnknownID(t *testing.T) {
	path, reloads := setupDevice(t)
	before, _ := os.ReadFile(path)
	if err := SetRingtone("s0", 7); !errors.Is(err, ErrInvalidRingtone) {
		t.Errorf("err = %v, want ErrInvalidRingtone", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || *reloads != 0 {
		t.Errorf("file changed or reload sent (%d) for an invalid id", *reloads)
	}
}
