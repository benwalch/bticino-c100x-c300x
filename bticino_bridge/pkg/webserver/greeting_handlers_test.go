package webserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bticino_bridge/pkg/bticino"
)

func TestGreetingWritesRejectedWithoutAnsweringMachine(t *testing.T) {
	if bticino.AnsweringMachineAvailable() {
		t.Skip("this variant has an answering machine")
	}
	ws := &WebServer{}
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		recorder := httptest.NewRecorder()
		ws.handleGreeting(recorder, httptest.NewRequest(method, "/api/talkback/greeting", strings.NewReader("audio")))
		if recorder.Code != http.StatusNotImplemented {
			t.Errorf("%s status = %d, want %d", method, recorder.Code, http.StatusNotImplemented)
		}
	}
}
