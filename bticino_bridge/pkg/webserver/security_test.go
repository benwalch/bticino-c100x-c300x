package webserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/config"
)

type recordingBridge struct {
	mu   sync.Mutex
	sent []string
}

func (b *recordingBridge) GetMulticastStats() map[string]interface{} { return nil }
func (b *recordingBridge) GetEventBusStats() map[string]interface{}  { return nil }
func (b *recordingBridge) GetVideoStats() map[string]interface{}     { return nil }
func (b *recordingBridge) SendOpenWebNetCommand(command string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, command)
	return nil
}

func (b *recordingBridge) commands() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.sent...)
}

func startTestServer(t *testing.T) (*WebServer, string) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Web.Enabled = true
	cfg.Web.Port = freePort(t)
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	ws := &WebServer{config: cfg, logger: logger, sseClients: make(map[chan []byte]bool), bridge: &recordingBridge{}}
	if err := ws.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { ws.Stop() })
	base := fmt.Sprintf("http://127.0.0.1:%d", cfg.Web.Port)
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if resp, err := http.Get(base + "/api/talkback/stop"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test server not reachable")
		}
	}
	return ws, base
}

func TestCrossSiteRequestsAreRejected(t *testing.T) {
	ws, base := startTestServer(t)

	request := func(method, fetchSite string) int {
		req, _ := http.NewRequest(method, base+"/api/controls/command", strings.NewReader(`{"command":"*8*19*20##"}`))
		if fetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if got := request(http.MethodPost, "cross-site"); got != http.StatusForbidden {
		t.Errorf("cross-site POST: status %d, want 403", got)
	}
	if sent := ws.bridge.(*recordingBridge).commands(); len(sent) != 0 {
		t.Errorf("cross-site POST reached the bus: %v", sent)
	}
	if got := request(http.MethodPost, "same-origin"); got != http.StatusOK {
		t.Errorf("same-origin POST: status %d, want 200", got)
	}
	if got := request(http.MethodPost, ""); got != http.StatusOK {
		t.Errorf("POST without browser headers (curl, Home Assistant): status %d, want 200", got)
	}
	if got := request(http.MethodGet, "cross-site"); got == http.StatusForbidden {
		t.Errorf("cross-site GET: status 403, safe methods must pass")
	}
}

func TestTalkbackStartRefusesForeignTargets(t *testing.T) {
	cases := map[string]string{
		"file outside allowed dirs": `{"port":5000,"source":"/etc/shadow"}`,
		"remote host":               `{"port":5000,"source":"/tmp/a.wav","host":"192.0.2.1"}`,
		"host name":                 `{"port":5000,"source":"/tmp/a.wav","host":"example.com"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			bridge := &recordingBridge{}
			ws := &WebServer{logger: logrus.New(), bridge: bridge}
			rec := httptest.NewRecorder()
			ws.handleTalkbackStart(rec, httptest.NewRequest(http.MethodPost, "/api/talkback/start", strings.NewReader(body)))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			if sent := bridge.commands(); len(sent) != 0 {
				t.Fatalf("bus command sent despite refusal: %v", sent)
			}
		})
	}
}

func TestAudioDuplexStartRefusesTalkFileOutsideAllowedDirs(t *testing.T) {
	fd := &fakeDuplex{}
	ws := newTestDuplexWS(true, fd)
	rec := httptest.NewRecorder()
	ws.handleAudioDuplexStart(rec, httptest.NewRequest(http.MethodPost, "/api/audio/duplex/start?talk=/etc/shadow", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	if fd.opened {
		t.Fatal("duplex leg opened despite refusal")
	}
}
