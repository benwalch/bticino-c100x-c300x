package webserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/config"
)

// fakeAnswerer is a test double of *sip.Answerer for the webserver handlers
// (it does not import pkg/sip; the Answerer interface is structural).
type fakeAnswerer struct {
	state                string
	answerErr, hangupErr error
}

func (f *fakeAnswerer) Answer() error       { return f.answerErr }
func (f *fakeAnswerer) Hangup() error       { return f.hangupErr }
func (f *fakeAnswerer) StateString() string { return f.state }
func (f *fakeAnswerer) IsRegistered() bool  { return true }

func newWS(t *testing.T) *WebServer {
	return NewWebServer(&config.Config{}, nil, logrus.New())
}

func TestDoorcallAnswer_503WithoutAnswerer(t *testing.T) {
	ws := newWS(t)
	rec := httptest.NewRecorder()
	ws.handleDoorcallAnswer(rec, httptest.NewRequest(http.MethodPost, "/api/doorcall/answer", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestDoorcallAnswer_409IfNotRinging(t *testing.T) {
	ws := newWS(t)
	ws.SetAnswerer(&fakeAnswerer{state: "idle", answerErr: errors.New("no call in RINGING")})
	rec := httptest.NewRecorder()
	ws.handleDoorcallAnswer(rec, httptest.NewRequest(http.MethodPost, "/api/doorcall/answer", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestDoorcallAnswer_OK(t *testing.T) {
	ws := newWS(t)
	ws.SetAnswerer(&fakeAnswerer{state: "ringing"})
	rec := httptest.NewRecorder()
	ws.handleDoorcallAnswer(rec, httptest.NewRequest(http.MethodPost, "/api/doorcall/answer", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestDoorcallHangup_503WithoutAnswerer(t *testing.T) {
	ws := newWS(t)
	rec := httptest.NewRecorder()
	ws.handleDoorcallHangup(rec, httptest.NewRequest(http.MethodPost, "/api/doorcall/hangup", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestDoorcallHangup_409IfNotInCall(t *testing.T) {
	ws := newWS(t)
	ws.SetAnswerer(&fakeAnswerer{state: "idle", hangupErr: errors.New("no active call")})
	rec := httptest.NewRecorder()
	ws.handleDoorcallHangup(rec, httptest.NewRequest(http.MethodPost, "/api/doorcall/hangup", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
}

func TestDoorcallHangup_OK(t *testing.T) {
	ws := newWS(t)
	ws.SetAnswerer(&fakeAnswerer{state: "in_call"})
	rec := httptest.NewRecorder()
	ws.handleDoorcallHangup(rec, httptest.NewRequest(http.MethodPost, "/api/doorcall/hangup", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestDoorcallStatus_503WithoutAnswerer(t *testing.T) {
	ws := newWS(t)
	rec := httptest.NewRecorder()
	ws.handleDoorcallStatus(rec, httptest.NewRequest(http.MethodGet, "/api/doorcall/status", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestDoorcallStatus_OK(t *testing.T) {
	ws := newWS(t)
	ws.SetAnswerer(&fakeAnswerer{state: "in_call"})
	rec := httptest.NewRecorder()
	ws.handleDoorcallStatus(rec, httptest.NewRequest(http.MethodGet, "/api/doorcall/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
