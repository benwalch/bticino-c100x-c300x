package sip

import (
	"net"
	"time"
)

// dialogWatchPoll is the timeout of each watcher read: it bounds how long
// stopDialogWatch takes to get the socket back for another reader.
const dialogWatchPoll = 200 * time.Millisecond

// dialogWatch watches an outgoing dialog that is already connected (the
// panel view call). After the ACK nobody read the webrtc socket: a native BYE
// got no 200 (bt_vct retransmitted it) and the bridge never learned that the
// call had ended. The official app reacts to the remote BYE at once.
type dialogWatch struct {
	stop chan struct{}
	done chan struct{}
}

// startDialogWatch starts the watcher for dialog callID. PRECONDITION: the
// caller holds webrtcID.readMu (the INVITE loop) and HANDS IT OVER: the
// watcher releases it when it ends. So the reader never waits (no deadlock
// with stopDialogWatch) and nobody slips in between the ACK and the watch.
func (c *BTicinoSIPClient) startDialogWatch(callID string) {
	w := &dialogWatch{stop: make(chan struct{}), done: make(chan struct{})}
	c.watchMu.Lock()
	c.watch = w
	c.watchMu.Unlock()
	go c.runDialogWatch(w, c.webrtcID, callID)
}

// stopDialogWatch stops the watcher and WAITS until it releases the socket.
// Idempotent. Must not be called with c.mutex held: the watcher needs it to
// handle a BYE.
func (c *BTicinoSIPClient) stopDialogWatch() {
	c.watchMu.Lock()
	w := c.watch
	c.watch = nil
	c.watchMu.Unlock()
	if w == nil {
		return
	}
	close(w.stop)
	<-w.done
}

func (c *BTicinoSIPClient) runDialogWatch(w *dialogWatch, id *sipIdentity, callID string) {
	defer close(w.done)
	defer id.readMu.Unlock() // reader handed over by startDialogWatch
	conn := id.conn
	for {
		select {
		case <-w.stop:
			return
		default:
		}
		msgs, err := c.readSIPMessagesFrom(conn, dialogWatchPoll)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			c.logger.WithError(err).Debug("SIP: dialog watcher ends (socket)")
			return
		}
		for _, m := range msgs {
			if m.StatusCode != "" || m.Method != "BYE" {
				c.logger.Debugf("SIP: dialog watcher ignores %s%s", m.Method, m.StatusCode)
				continue
			}
			c.webrtcID.mutex.Lock()
			c.sendBYEResponse(c.webrtcID, m)
			c.webrtcID.mutex.Unlock()
			if sipCallID(m) != callID {
				continue // late BYE of another dialog: the 200 is enough
			}
			c.mutex.Lock()
			if c.callID == callID {
				c.sessionActive = false
				c.callState = Registered
			}
			c.mutex.Unlock()
			c.logger.Infof("SIP: the remote side hung up dialog %s (BYE answered)", callID)
			c.eventBus.PublishWithSource("sip.dialog.ended", map[string]interface{}{
				"call_id": callID,
				"reason":  "remote_bye",
			}, "sip")
			return
		}
	}
}

// sipCallID returns the Call-ID of a message (case-insensitive header).
func sipCallID(m *SIPMessage) string {
	for _, k := range []string{"Call-ID", "Call-Id", "call-id"} {
		if v := m.Headers[k]; v != "" {
			return v
		}
	}
	return ""
}
