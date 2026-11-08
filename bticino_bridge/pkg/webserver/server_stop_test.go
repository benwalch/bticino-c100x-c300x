package webserver

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/config"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestStopReturnsWithOpenEventStream(t *testing.T) {
	cfg := &config.Config{}
	cfg.Web.Enabled = true
	cfg.Web.Port = freePort(t)
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	ws := &WebServer{config: cfg, logger: logger, sseClients: make(map[chan []byte]bool)}
	if err := ws.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The stream sends its headers only with the first event.
	connected := make(chan struct{})
	go func() {
		for {
			select {
			case <-connected:
				return
			case <-time.After(20 * time.Millisecond):
				ws.BroadcastEvent("test", map[string]interface{}{})
			}
		}
	}()
	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/events", cfg.Web.Port)
	var resp *http.Response
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var err error
		if resp, err = client.Get(url); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("event stream not reachable: %v", err)
		}
	}
	defer resp.Body.Close()
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatalf("event stream not open: %v", err)
	}
	close(connected)

	done := make(chan error, 1)
	go func() { done <- ws.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return while an event stream was open")
	}
}
