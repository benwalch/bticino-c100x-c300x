package sip

import (
	"errors"
	"net"
	"time"
)

// nextReconnectDelay returns the backoff before the next attempt: 1s, 2s,
// 4s, 8s, 16s, 30s and then 60s.
func nextReconnectDelay(attempt int) time.Duration {
	delay := time.Second
	for i := 0; i < attempt && delay < 60*time.Second; i++ {
		delay *= 2
	}
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	return delay
}

// identityConn returns an identity's connection and its generation. Errors
// reported together with an older generation come from a connection that was
// already replaced and must be ignored.
func (c *BTicinoSIPClient) identityConn(id *sipIdentity) (net.Conn, uint64) {
	if id == nil {
		return nil, 0
	}
	id.mutex.Lock()
	defer id.mutex.Unlock()
	return id.conn, id.gen
}

// connError handles an error seen on one connection generation: it starts a
// reconnect when that generation is still current and reports whether it did.
func (c *BTicinoSIPClient) connError(id *sipIdentity, gen uint64, err error) bool {
	if id == nil {
		return false
	}
	id.mutex.Lock()
	stale := gen != id.gen
	id.mutex.Unlock()
	if stale {
		return false
	}
	c.reconnect(id, err.Error())
	return true
}

// reconnect re-dials and re-registers one identity in the background.
// Concurrent calls for the same identity are dropped (single flight).
func (c *BTicinoSIPClient) reconnect(id *sipIdentity, reason string) {
	if id == nil {
		return
	}
	select {
	case <-c.stopCh:
		return
	default:
	}
	id.mutex.Lock()
	if id.reconnecting {
		id.mutex.Unlock()
		return
	}
	id.reconnecting = true
	old := id.conn
	id.mutex.Unlock()
	go c.reconnectLoop(id, reason, old)
}

func (c *BTicinoSIPClient) reconnectLoop(id *sipIdentity, reason string, old net.Conn) {
	defer func() {
		id.mutex.Lock()
		id.reconnecting = false
		id.mutex.Unlock()
	}()
	if old != nil {
		old.Close()
	}

	if id == c.webrtcID {
		c.mutex.Lock()
		c.callState = Reconnecting
		active := c.sessionActive
		c.mutex.Unlock()
		if active {
			c.StopCameraSession()
		}
		c.eventBus.PublishWithSource("sip.disconnected", map[string]interface{}{
			"reason": reason,
		}, "sip")
	}
	c.logger.WithField("reason", reason).Warnf("SIP %s connection lost — reconnecting", id.username)
	// The listener is gone: the REGISTER below reads its own responses.
	id.mutex.Lock()
	id.respCh = nil
	id.mutex.Unlock()

	for attempt := 0; ; attempt++ {
		select {
		case <-c.stopCh:
			return
		default:
		}
		id.readMu.Lock()
		err := c.registerIdentity(id)
		id.readMu.Unlock()
		if err == nil {
			id.markRegistered()
			c.onReconnected(id)
			return
		} else {
			c.logger.WithError(err).Warnf("SIP %s reconnect attempt %d failed", id.username, attempt+1)
		}
		select {
		case <-c.stopCh:
			return
		case <-time.After(nextReconnectDelay(attempt)):
		}
	}
}

// onReconnected restores the state that depends on a fresh connection.
func (c *BTicinoSIPClient) onReconnected(id *sipIdentity) {
	select {
	case <-c.stopCh:
		// Shutdown won the race: don't leave a live connection behind.
		if conn, _ := c.identityConn(id); conn != nil {
			conn.Close()
		}
		return
	default:
	}
	if id == c.localID {
		c.startLocalListener()
	}
	if id == c.webrtcID {
		conn, _ := c.identityConn(c.webrtcID)
		c.mutex.Lock()
		c.conn = conn
		c.callState = Registered
		c.mutex.Unlock()
	}
	c.logger.Infof("SIP %s reconnected", id.username)
	c.eventBus.PublishWithSource("sip.registered", map[string]interface{}{
		"username":    id.username,
		"reconnected": true,
	}, "sip")
}

// refreshRegistration renews a registration after half of its expiry, on the
// current connection, and reconnects when that connection turns out to be gone.
// A socket owned by another reader only defers the renewal.
func (c *BTicinoSIPClient) refreshRegistration(id *sipIdentity) {
	if id == nil {
		return
	}
	id.mutex.Lock()
	reconnecting := id.reconnecting
	since := time.Since(id.lastRegistered)
	id.mutex.Unlock()
	half := time.Duration(c.config.ExpirySeconds) * time.Second / 2
	if reconnecting || since < half {
		return
	}
	_, gen := c.identityConn(id)
	err := c.reRegisterIdentity(id)
	switch {
	case err == nil:
	case errors.Is(err, errSocketBusy):
		if since > half*3/2 {
			c.logger.Warnf("SIP %s re-REGISTER deferred and the registration expires soon (socket busy)", id.username)
		} else {
			c.logger.Debugf("SIP %s re-REGISTER deferred: socket busy", id.username)
		}
	case !c.connError(id, gen, err):
		c.logger.WithError(err).Debug("SIP re-registration failed on a replaced connection")
	}
}
