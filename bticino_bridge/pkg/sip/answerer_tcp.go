package sip

import (
	"bufio"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// TCPTransport is the answerer's concrete transport: a persistent TCP connection to the local
// Flexisip (implements AnswererTransport). Send serializes writes with a mutex; Close is
// idempotent (sync.Once) because Answerer.Stop() can call it more than once.
type TCPTransport struct {
	conn      net.Conn
	reader    *bufio.Reader
	logger    *logrus.Logger
	writeMu   sync.Mutex
	closeOnce sync.Once
	closeErr  error
	lhost     string
	lport     int
}

// NewTCPTransport opens the TCP connection to serverAddr (host:port) with a 10 s connect
// timeout, enables TCP_NODELAY (SIP frames are small and latency sensitive) and wraps the
// reader in a bufio.Reader for ReadSIPFrame.
func NewTCPTransport(serverAddr string, logger *logrus.Logger) (*TCPTransport, error) {
	if logger == nil {
		logger = logrus.New()
	}
	conn, err := net.DialTimeout("tcp", serverAddr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	host, port := splitHostPort(conn.LocalAddr().String())
	return &TCPTransport{
		conn:   conn,
		reader: bufio.NewReader(conn),
		logger: logger,
		lhost:  host,
		lport:  port,
	}, nil
}

// Send writes a raw SIP frame to the connection. Serialized with writeMu: run() (reading) and
// calls from Answer/Hangup/registerOnce (under a.mu) can call Send concurrently.
func (t *TCPTransport) Send(raw string) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err := t.conn.Write([]byte(raw))
	return err
}

// Recv blocks until it reads the next complete SIP frame (or error/EOF when the connection closes).
func (t *TCPTransport) Recv() (*SIPMessage, error) {
	return ReadSIPFrame(t.reader)
}

// LocalHost returns the local IP used for REGISTER/Contact (that of the outgoing connection).
func (t *TCPTransport) LocalHost() string { return t.lhost }

// LocalPort returns the ephemeral local port of the outgoing connection.
func (t *TCPTransport) LocalPort() int { return t.lport }

// Close closes the connection. Idempotent: a second call is a safe no-op (Answerer.Stop()
// can call it more than once and must not panic on a closed conn).
func (t *TCPTransport) Close() error {
	t.closeOnce.Do(func() {
		t.closeErr = t.conn.Close()
	})
	return t.closeErr
}

// splitHostPort splits "127.0.0.1:41000" into ("127.0.0.1", 41000). On a parse failure (should
// not happen for conn.LocalAddr()) it returns the whole string and port 0.
func splitHostPort(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return host, 0
	}
	return host, port
}
