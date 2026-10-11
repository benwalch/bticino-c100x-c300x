package webserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"bticino_bridge/pkg/config"

	"github.com/sirupsen/logrus"
)

func newAuthTestWS(authRequired bool, user, pass string) *WebServer {
	cfg := &config.Config{}
	cfg.Security.WebAuthRequired = authRequired
	cfg.Security.WebUsername = user
	cfg.Security.WebPassword = pass
	return &WebServer{config: cfg, logger: logrus.New()}
}

func doAuthRequest(ws *WebServer, user, pass string) *httptest.ResponseRecorder {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	rec := httptest.NewRecorder()
	ws.authMiddleware(next).ServeHTTP(rec, req)
	return rec
}

func TestAuthDisabledPassesThrough(t *testing.T) {
	ws := newAuthTestWS(false, "", "")
	if rec := doAuthRequest(ws, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("with auth disabled expected 200, got %d", rec.Code)
	}
}

func TestAuthEnabledRejectsMissingCredentials(t *testing.T) {
	ws := newAuthTestWS(true, "admin", "s3cret")
	rec := doAuthRequest(ws, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without credentials expected 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("401 must include WWW-Authenticate so the browser prompts for login")
	}
}

func TestAuthEnabledRejectsWrongPassword(t *testing.T) {
	ws := newAuthTestWS(true, "admin", "s3cret")
	if rec := doAuthRequest(ws, "admin", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("with wrong password expected 401, got %d", rec.Code)
	}
}

func TestAuthEnabledAcceptsCorrectCredentials(t *testing.T) {
	ws := newAuthTestWS(true, "admin", "s3cret")
	if rec := doAuthRequest(ws, "admin", "s3cret"); rec.Code != http.StatusOK {
		t.Fatalf("with correct credentials expected 200, got %d", rec.Code)
	}
}

func TestAuthEnabledWithoutPasswordFailsClosed(t *testing.T) {
	// Enabled but without password: NEVER leave silently open (false security);
	// respond 503 until the config is consistent.
	ws := newAuthTestWS(true, "admin", "")
	if rec := doAuthRequest(ws, "admin", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("auth enabled without password must return 503, got %d", rec.Code)
	}
}

func TestValidateWebAuthRequiresPassword(t *testing.T) {
	cm := newTestCM(t)
	cfg := baseValidConfig()
	cfg.Security.WebAuthRequired = true
	cfg.Security.WebPassword = ""
	if r := cm.ValidateConfig(cfg); r.Valid || !hasErrorContaining(r, "web_password") {
		t.Fatalf("web_auth_required without password must be a validation error, got %+v", r)
	}
	cfg.Security.WebUsername = "admin"
	cfg.Security.WebPassword = "s3cret"
	if r := cm.ValidateConfig(cfg); !r.Valid {
		t.Fatalf("correctly configured auth must be valid: %v", r.Errors)
	}
}
