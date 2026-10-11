package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
)

func newTestSystemWS() (*WebServer, *int, *int) {
	ws := &WebServer{logger: logrus.New()}
	restarts, reboots := 0, 0
	ws.restartBridgeFunc = func() { restarts++ }
	ws.rebootDeviceFunc = func() { reboots++ }
	return ws, &restarts, &reboots
}

func TestSystemRestartBridgeRejectsGET(t *testing.T) {
	ws, restarts, _ := newTestSystemWS()
	rec := httptest.NewRecorder()
	ws.handleAPISystemRestartBridge(rec, httptest.NewRequest(http.MethodGet, "/api/system/restart-bridge", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 on GET, got %d", rec.Code)
	}
	if *restarts != 0 {
		t.Fatal("GET must not trigger the restart")
	}
}

func TestSystemRestartBridgePOST(t *testing.T) {
	ws, restarts, _ := newTestSystemWS()
	rec := httptest.NewRecorder()
	ws.handleAPISystemRestartBridge(rec, httptest.NewRequest(http.MethodPost, "/api/system/restart-bridge", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body["success"] != true {
		t.Fatalf("expected success=true, got %v (err=%v)", body, err)
	}
	if *restarts != 1 {
		t.Fatalf("expected 1 restart triggered, got %d", *restarts)
	}
}

func TestSystemRebootRejectsGET(t *testing.T) {
	ws, _, reboots := newTestSystemWS()
	rec := httptest.NewRecorder()
	ws.handleAPISystemReboot(rec, httptest.NewRequest(http.MethodGet, "/api/system/reboot", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 on GET, got %d", rec.Code)
	}
	if *reboots != 0 {
		t.Fatal("GET must not trigger the reboot")
	}
}

func TestSystemRebootPOST(t *testing.T) {
	ws, _, reboots := newTestSystemWS()
	rec := httptest.NewRecorder()
	ws.handleAPISystemReboot(rec, httptest.NewRequest(http.MethodPost, "/api/system/reboot", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if *reboots != 1 {
		t.Fatalf("expected 1 reboot triggered, got %d", *reboots)
	}
}
