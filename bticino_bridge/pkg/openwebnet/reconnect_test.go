package openwebnet

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

const ringFrame = "*8*1#1#4#21*11##"

// fakeMonitor serves monitor sessions on one address: ACK, ACK for the EVENT
// session request, then the frames sent on its channel.
type fakeMonitor struct {
	t        *testing.T
	addr     string
	frames   chan string
	sessions chan struct{} // one per completed EVENT session request

	mu    sync.Mutex
	ln    net.Listener
	conns []net.Conn
	gone  chan struct{} // closed by down
}

func newFakeMonitor(t *testing.T, addr string) *fakeMonitor {
	t.Helper()
	m := &fakeMonitor{t: t, addr: addr, frames: make(chan string), sessions: make(chan struct{}, 10)}
	m.up()
	return m
}

func (m *fakeMonitor) up() {
	ln, err := net.Listen("tcp", m.addr)
	if err != nil {
		m.t.Fatalf("listen %s: %v", m.addr, err)
	}
	gone := make(chan struct{})
	m.mu.Lock()
	m.ln = ln
	m.addr = ln.Addr().String()
	m.gone = gone
	m.mu.Unlock()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			m.mu.Lock()
			m.conns = append(m.conns, conn)
			m.mu.Unlock()
			go m.serve(conn, gone)
		}
	}()
}

func (m *fakeMonitor) serve(conn net.Conn, gone chan struct{}) {
	conn.Write([]byte(CMD_ACK))
	if _, err := readOWNMessage(bufio.NewReader(conn)); err != nil {
		return
	}
	conn.Write([]byte(CMD_ACK))
	m.sessions <- struct{}{}
	for {
		select {
		case frame := <-m.frames:
			conn.Write([]byte(frame))
		case <-gone:
			return
		}
	}
}

// down stops listening and drops every open session, like a restarting openserver.
func (m *fakeMonitor) down() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ln.Close()
	close(m.gone)
	for _, conn := range m.conns {
		conn.Close()
	}
	m.conns = nil
}

func (m *fakeMonitor) port() int {
	_, p, _ := net.SplitHostPort(m.addr)
	port, _ := strconv.Atoi(p)
	return port
}

func newMonitorClient(port int) (*Client, chan string) {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	c := NewClient(ClientConfig{
		Host: "127.0.0.1", Ports: []int{port}, MainPort: port,
		Timeout: time.Second, RetryCount: 1, RetryDelay: 20 * time.Millisecond, Logger: logger,
	})
	received := make(chan string, 10)
	c.OnMessage(func(cmd *Command) { received <- cmd.Raw })
	return c, received
}

func expectSession(t *testing.T, m *fakeMonitor, within time.Duration) {
	t.Helper()
	select {
	case <-m.sessions:
	case <-time.After(within):
		t.Fatalf("no monitor session within %v", within)
	}
}

func expectRing(t *testing.T, m *fakeMonitor, received chan string) {
	t.Helper()
	m.frames <- ringFrame
	select {
	case got := <-received:
		if got != ringFrame {
			t.Fatalf("got %q, want %q", got, ringFrame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bus event not delivered")
	}
}

func TestMonitorReconnectsAfterLongOutage(t *testing.T) {
	m := newFakeMonitor(t, "127.0.0.1:0")
	c, received := newMonitorClient(m.port())
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Disconnect()
	expectSession(t, m, time.Second)
	expectRing(t, m, received)

	// With RetryCount 1 and RetryDelay 20 ms the old client gave up after ~20 ms.
	m.down()
	time.Sleep(500 * time.Millisecond)
	m.up()
	defer m.down()

	expectSession(t, m, 5*time.Second)
	expectRing(t, m, received)
}

func TestMonitorConnectsWhenServerStartsLater(t *testing.T) {
	m := newFakeMonitor(t, "127.0.0.1:0")
	m.down()
	c, received := newMonitorClient(m.port())
	if err := c.Connect(); err == nil {
		t.Fatal("Connect to a closed port reported success")
	}
	defer c.Disconnect()

	time.Sleep(300 * time.Millisecond)
	m.up()
	defer m.down()

	expectSession(t, m, 5*time.Second)
	expectRing(t, m, received)
}

func TestDisconnectStopsReconnecting(t *testing.T) {
	m := newFakeMonitor(t, "127.0.0.1:0")
	c, _ := newMonitorClient(m.port())
	if err := c.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	expectSession(t, m, time.Second)

	m.down()
	time.Sleep(100 * time.Millisecond)
	c.Disconnect()
	m.up()
	defer m.down()

	select {
	case <-m.sessions:
		t.Fatal("client opened a monitor session after Disconnect")
	case <-time.After(2 * time.Second):
	}
}
