package mqtt

import (
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"

	"bticino_bridge/pkg/config"
)

// fakeBroker answers every MQTT CONNECT with the next return code and then reads
// and discards whatever the client sends.
type fakeBroker struct {
	listener net.Listener
	mu       sync.Mutex
	codes    []byte
	connects int
}

func startFakeBroker(t *testing.T, address string, codes ...byte) *fakeBroker {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	broker := &fakeBroker{listener: listener, codes: codes}
	go broker.serve()
	t.Cleanup(func() { listener.Close() })
	return broker
}

func (f *fakeBroker) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeBroker) handle(conn net.Conn) {
	defer conn.Close()
	buffer := make([]byte, 1024)
	if _, err := conn.Read(buffer); err != nil {
		return
	}
	f.mu.Lock()
	code := byte(0)
	if f.connects < len(f.codes) {
		code = f.codes[f.connects]
	}
	f.connects++
	f.mu.Unlock()
	if _, err := conn.Write([]byte{0x20, 0x02, 0x00, code}); err != nil || code != 0 {
		return
	}
	io.Copy(io.Discard, conn)
}

func (f *fakeBroker) connectCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connects
}

// freeAddress returns a local address on which nothing listens.
func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func newTestBridge(t *testing.T, address string) (*MQTTBridge, *logtest.Hook) {
	t.Helper()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split address: %v", err)
	}
	cfg := &config.Config{}
	cfg.MQTT.Host = host
	cfg.MQTT.Port, err = strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	logger, hook := logtest.NewNullLogger()
	logger.SetLevel(logrus.DebugLevel)
	bridge, err := NewMQTTBridge(cfg, logger)
	if err != nil {
		t.Fatalf("new bridge: %v", err)
	}
	bridge.retryInterval, bridge.waitPoll = testRetryInterval, 10*time.Millisecond
	return bridge, hook
}

const testRetryInterval = 50 * time.Millisecond

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func warnings(hook *logtest.Hook, text string) int {
	count := 0
	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.WarnLevel && strings.Contains(entry.Message, text) {
			count++
		}
	}
	return count
}

func TestStartConnectsWhenBrokerComesLater(t *testing.T) {
	address := freeAddress(t)
	bridge, hook := newTestBridge(t, address)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start with an unreachable broker must not fail: %v", err)
	}
	if bridge.IsConnected() {
		t.Fatal("connected without a broker")
	}
	if warnings(hook, "not reachable, retrying") != 1 {
		t.Fatal("missing warning about the unreachable broker")
	}

	startFakeBroker(t, address)
	waitUntil(t, "the bridge is connected", bridge.IsConnected)
	bridge.Stop()
}

func TestRetryLogsEachErrorOnce(t *testing.T) {
	address := freeAddress(t)
	// Three refusals for bad credentials, then accepted.
	broker := startFakeBroker(t, address, 5, 5, 5, 0)
	bridge, hook := newTestBridge(t, address)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start with refused credentials must not fail: %v", err)
	}
	waitUntil(t, "the bridge is connected", bridge.IsConnected)
	if got := broker.connectCount(); got != 4 {
		t.Errorf("connect attempts = %d, want 4", got)
	}
	if got := warnings(hook, "retrying"); got != 1 {
		t.Errorf("warnings for the same error = %d, want 1", got)
	}
	bridge.Stop()
}

func TestRetryStopsWithContext(t *testing.T) {
	address := freeAddress(t)
	broker := startFakeBroker(t, address, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5)
	bridge, _ := newTestBridge(t, address)
	ctx, cancel := context.WithCancel(context.Background())

	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, "a retry happened", func() bool { return broker.connectCount() >= 2 })
	cancel()
	time.Sleep(3 * testRetryInterval)
	stopped := broker.connectCount()
	time.Sleep(5 * testRetryInterval)
	if got := broker.connectCount(); got != stopped {
		t.Errorf("connect attempts went on after cancel: %d, then %d", stopped, got)
	}
}

func TestDeviceConfigPublisherWaitsForConnection(t *testing.T) {
	address := freeAddress(t)
	bridge, hook := newTestBridge(t, address)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	started := make(chan struct{})
	go func() {
		bridge.SetDeviceConfigPublisher(time.Hour)
		close(started)
	}()
	select {
	case <-started:
		t.Fatal("publisher started without a connection")
	case <-time.After(100 * time.Millisecond):
	}

	startFakeBroker(t, address)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher did not start after the connection")
	}
	for _, entry := range hook.AllEntries() {
		if strings.Contains(entry.Message, "Cannot start device config publisher") {
			t.Fatal("publisher gave up")
		}
	}
	bridge.Stop()
}
