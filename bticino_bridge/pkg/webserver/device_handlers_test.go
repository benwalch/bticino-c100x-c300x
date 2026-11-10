package webserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bticino_bridge/pkg/deviceconfig"

	"github.com/sirupsen/logrus"
)

// useDeviceFiles points the device paths at a copy of the C100X settings.xml
// and a ringtone list with the ids 1 and 4.
func useDeviceFiles(t *testing.T) (settingsPath string) {
	t.Helper()
	dir := t.TempDir()
	data, err := os.ReadFile("../deviceconfig/testdata/settings_c100x.xml")
	if err != nil {
		t.Fatal(err)
	}
	settingsPath = filepath.Join(dir, "settings.xml")
	listPath := filepath.Join(dir, "ringtones.xml")
	list := `<device_ringtones><ringtones>
<item><id_ringtone>1</id_ringtone><path_ringtone>ring1.wav</path_ringtone></item>
<item><id_ringtone>4</id_ringtone><path_ringtone>ring4.wav</path_ringtone></item>
</ringtones></device_ringtones>`
	if err := os.WriteFile(settingsPath, data, 0o664); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(listPath, []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	oldSettings, oldList := deviceconfig.DevicePaths.SettingsXML, deviceconfig.RingtonesXMLPath
	t.Cleanup(func() { deviceconfig.DevicePaths.SettingsXML, deviceconfig.RingtonesXMLPath = oldSettings, oldList })
	deviceconfig.DevicePaths.SettingsXML, deviceconfig.RingtonesXMLPath = settingsPath, listPath
	return settingsPath
}

func TestDeviceRingtoneGetReturnsSettableValues(t *testing.T) {
	useDeviceFiles(t)
	ws := &WebServer{logger: logrus.New()}
	rec := httptest.NewRecorder()
	ws.handleAPIDeviceRingtone(rec, httptest.NewRequest(http.MethodGet, "/api/device/ringtone", nil))

	var body struct{ Ringtones map[string]int }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	want := map[string]int{"s0": 1, "s1": 1, "internal": 8, "external": 7, "floor": 18}
	for typ, id := range want {
		if body.Ringtones[typ] != id || len(body.Ringtones) != len(want) {
			t.Fatalf("ringtones = %v, want %v", body.Ringtones, want)
		}
	}
}

func TestDeviceRingtonePostRejectsInvalidInput(t *testing.T) {
	path := useDeviceFiles(t)
	before, _ := os.ReadFile(path)
	ws := &WebServer{logger: logrus.New()}

	for _, body := range []string{`{"type":"s0","id":7}`, `{"type":"alarm","id":4}`, `not json`} {
		rec := httptest.NewRecorder()
		ws.handleAPIDeviceRingtone(rec, httptest.NewRequest(http.MethodPost, "/api/device/ringtone", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", body, rec.Code)
		}
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("settings.xml changed on invalid input")
	}
}

func TestDeviceRingtonesListsFiles(t *testing.T) {
	useDeviceFiles(t)
	ws := &WebServer{logger: logrus.New()}
	rec := httptest.NewRecorder()
	ws.handleAPIDeviceRingtones(rec, httptest.NewRequest(http.MethodGet, "/api/device/ringtones", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `{"id":4,"file":"ring4.wav"}`) {
		t.Errorf("status %d, body %s", rec.Code, rec.Body)
	}
}

func TestDeviceRingtoneFileServesListedFilesOnly(t *testing.T) {
	useDeviceFiles(t)
	wav := []byte("RIFF....WAVE")
	if err := os.WriteFile(filepath.Join(filepath.Dir(deviceconfig.RingtonesXMLPath), "ring4.wav"), wav, 0o644); err != nil {
		t.Fatal(err)
	}
	ws := &WebServer{logger: logrus.New()}

	rec := httptest.NewRecorder()
	ws.handleAPIDeviceRingtoneFile(rec, httptest.NewRequest(http.MethodGet, "/api/device/ringtones/4.wav", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/wav" || !bytes.Equal(rec.Body.Bytes(), wav) {
		t.Errorf("4.wav: status %d, type %q, body %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}

	for _, path := range []string{"/api/device/ringtones/7.wav", "/api/device/ringtones/4", "/api/device/ringtones/..%2fsettings.xml"} {
		rec := httptest.NewRecorder()
		ws.handleAPIDeviceRingtoneFile(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestDeviceSaveRejectsRingtone(t *testing.T) {
	ws := &WebServer{logger: logrus.New()}
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"config":{"ringtone":"4"}}`)
	ws.handleAPIDeviceSave(rec, httptest.NewRequest(http.MethodPost, "/api/device/save", body))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
}
