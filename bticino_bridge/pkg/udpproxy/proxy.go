// Package udpproxy implements a simple UDP proxy that forwards packets between two ports.
// This lets external clients (like the BTicino mobile app or scrcpy) talk
// to the device through the bridge, forwarding packets from port 40004 to 4000.
//
// Based on the reference implementation from slyoldfox/c300x-controller (lib/udp-proxy.js).
package udpproxy

import (
	"fmt"
	"net"
	"sync"

	"github.com/sirupsen/logrus"
)

// UDPProxy forwards UDP packets from a listen port to a target port on localhost.
type UDPProxy struct {
	listenPort int
	targetPort int
	logger     *logrus.Logger

	conn    *net.UDPConn
	mu      sync.Mutex
	running bool
	done    chan struct{}
}

// New creates a new UDPProxy instance.
func New(listenPort, targetPort int, logger *logrus.Logger) *UDPProxy {
	return &UDPProxy{
		listenPort: listenPort,
		targetPort: targetPort,
		logger:     logger,
		done:       make(chan struct{}),
	}
}

// Start starts the UDP proxy in a goroutine.
func (p *UDPProxy) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return fmt.Errorf("UDP proxy already running")
	}

	listenAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("0.0.0.0:%d", p.listenPort))
	if err != nil {
		return fmt.Errorf("error resolving listen address: %w", err)
	}

	p.conn, err = net.ListenUDP("udp4", listenAddr)
	if err != nil {
		return fmt.Errorf("error opening UDP socket on port %d: %w", p.listenPort, err)
	}

	p.running = true

	p.logger.Infof("UDP Proxy listening on 0.0.0.0:%d -> forwarding to 127.0.0.1:%d", p.listenPort, p.targetPort)

	go p.forwardLoop()

	return nil
}

// forwardLoop reads packets from the listen socket and forwards them to the target port.
func (p *UDPProxy) forwardLoop() {
	defer func() {
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		close(p.done)
	}()

	buf := make([]byte, 65535)

	targetAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("127.0.0.1:%d", p.targetPort))
	if err != nil {
		p.logger.WithError(err).Error("Error resolving UDP destination address")
		return
	}

	for {
		n, _, err := p.conn.ReadFromUDP(buf)
		if err != nil {
			// Check if it was closed intentionally
			p.mu.Lock()
			running := p.running
			p.mu.Unlock()
			if !running {
				p.logger.Info("UDP Proxy: socket closed, stopping")
				return
			}
			p.logger.WithError(err).Warn("Error reading UDP packet")
			continue
		}

		if n == 0 {
			continue
		}

		// Create a temporary socket to forward to the destination
		fwdConn, err := net.DialUDP("udp4", nil, targetAddr)
		if err != nil {
			p.logger.WithError(err).Warn("Error connecting to UDP destination")
			continue
		}

		_, err = fwdConn.Write(buf[:n])
		fwdConn.Close()
		if err != nil {
			p.logger.WithError(err).Warn("Error forwarding UDP packet")
			continue
		}

		p.logger.WithField("bytes", n).Debug("UDP packet forwarded")
	}
}

// Stop stops the UDP proxy.
func (p *UDPProxy) Stop() error {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return nil
	}
	p.running = false
	p.mu.Unlock()

	if p.conn != nil {
		p.conn.Close()
	}

	// Wait for the goroutine to finish
	<-p.done

	p.logger.Info("UDP Proxy stopped")
	return nil
}

// IsRunning returns whether the proxy is running.
func (p *UDPProxy) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}
