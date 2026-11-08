package sip

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"bticino_bridge/pkg/bticino"
)

// cameraDialog is a confirmed loopback dialog with the native unit.
type cameraDialog struct {
	callID  string
	fromTag string
	toTag   string
	contact string
	cseq    int
	offer   string
}

// SetFrameGrabber registers the end-of-session JPEG source. Best effort:
// a nil return skips the still without delaying teardown.
func (c *BTicinoSIPClient) SetFrameGrabber(grabber func() []byte) {
	c.mutex.Lock()
	c.frameGrabber = grabber
	c.mutex.Unlock()
}

func (c *BTicinoSIPClient) grabFrame() []byte {
	c.mutex.RLock()
	grabber := c.frameGrabber
	c.mutex.RUnlock()
	if grabber == nil {
		return nil
	}
	return grabber()
}

// StartCameraSession dials the native unit directly, holds the dialog for
// duration, then hangs up. The unit answers headless and streams to the
// loopback tap. Brackets the window with video.session.started/stopped.
// Source names the owner ("mqtt", "rtsp"): only the owner reaps the session.
func (c *BTicinoSIPClient) StartCameraSession(duration time.Duration, source string) error {
	c.mutex.Lock()
	if !c.running {
		c.mutex.Unlock()
		return fmt.Errorf("SIP client is not running")
	}
	if c.cameraSessionActive {
		c.mutex.Unlock()
		return fmt.Errorf("camera session already active")
	}
	if c.callState != Registered || c.sessionActive {
		state := c.callState
		c.mutex.Unlock()
		return fmt.Errorf("cannot start camera session (state=%s)", state)
	}
	c.cameraSessionActive = true
	c.cameraSessionSource = source
	stop := make(chan struct{})
	c.cameraSessionStop = stop
	c.mutex.Unlock()
	go c.runCameraSession(duration, stop)
	return nil
}

// StopCameraSession ends an active session early. Silent when idle.
func (c *BTicinoSIPClient) StopCameraSession() {
	c.mutex.Lock()
	if c.cameraSessionActive && c.cameraSessionStop != nil {
		close(c.cameraSessionStop)
		c.cameraSessionStop = nil
	}
	c.mutex.Unlock()
}

func (c *BTicinoSIPClient) runCameraSession(duration time.Duration, stop <-chan struct{}) {
	if duration <= 0 {
		duration = 30 * time.Second
	}
	defer func() {
		c.mutex.Lock()
		c.cameraSessionActive = false
		c.cameraSessionSource = ""
		c.cameraSessionStop = nil
		c.mutex.Unlock()
		c.eventBus.PublishWithSource("video.session.stopped", map[string]interface{}{}, "sip")
	}()
	uri, err := c.resolveNative()
	if err != nil {
		c.logger.WithError(err).Warn("Camera session: no native contact")
		return
	}
	nativeDevAddr := c.sessionNativeDevAddr()
	if nativeDevAddr == "" {
		c.logger.Warn("Camera session: sip.native_dev_addr is not configured")
		return
	}
	sdp, err := buildCameraSDP(nativeDevAddr)
	if err != nil {
		c.logger.WithError(err).Warn("Camera session: no media offer")
		return
	}
	c.stopDialogWatch()
	c.webrtcID.readMu.Lock() // this session is the socket's reader until it ends
	defer c.webrtcID.readMu.Unlock()
	dlg, err := c.establish(uri, sdp)
	if err != nil {
		c.logger.WithError(err).Warn("Camera session: dial failed")
		return
	}
	c.mutex.Lock()
	c.callID = dlg.callID
	c.toTag = dlg.toTag
	c.remoteTag = dlg.toTag
	c.sessionActive = true
	c.callState = Connected
	c.mutex.Unlock()
	c.eventBus.PublishWithSource("sip.call.connected", map[string]interface{}{
		"target":   c.answerUser,
		"dev_addr": c.config.DevAddr,
	}, "sip")
	c.logger.Info("Camera session connected, holding media window")
	c.eventBus.PublishWithSource("video.session.started", map[string]interface{}{}, "sip")
	reason := c.drainCameraDialog(dlg, stop, duration)
	c.logger.Infof("Camera session: stopping (%s)", reason)
	// A dialog ended by the native side has no video left worth capturing.
	if reason != sessionEndNative {
		if jpeg := c.grabFrame(); jpeg != nil {
			c.eventBus.PublishWithSource("video.session.frame", map[string]interface{}{"jpeg": jpeg}, "sip")
		}
	}
	c.terminate(dlg)
	c.mutex.Lock()
	c.callState = Registered
	c.sessionActive = false
	c.mutex.Unlock()
	c.eventBus.PublishWithSource("sip.call.ended", map[string]interface{}{}, "sip")
}

func (c *BTicinoSIPClient) resolveNative() (string, error) {
	if c.resolveNativeFunc != nil {
		return c.resolveNativeFunc()
	}
	return resolveNativeContact(c.ownLoopbackPorts())
}

func (c *BTicinoSIPClient) establish(uri, sdp string) (*cameraDialog, error) {
	if c.establishDialogFunc != nil {
		return c.establishDialogFunc(uri, sdp)
	}
	return c.establishCameraDialog(uri, sdp)
}

func (c *BTicinoSIPClient) terminate(dlg *cameraDialog) {
	if c.terminateDialogFunc != nil {
		c.terminateDialogFunc(dlg)
		return
	}
	c.terminateCameraDialog(dlg)
}

// ownLoopbackPorts returns the local ports of our own loopback SIP
// connections, so the resolver can tell them apart from the native unit.
func (c *BTicinoSIPClient) ownLoopbackPorts() map[int]bool {
	ports := map[int]bool{}
	for _, id := range []*sipIdentity{c.webrtcID, c.localID} {
		if id == nil || id.conn == nil {
			continue
		}
		if addr, ok := id.conn.LocalAddr().(*net.TCPAddr); ok {
			ports[addr.Port] = true
		}
	}
	return ports
}

// resolveNativeContact finds the native unit's loopback SIP contact by
// scanning established connections to 127.0.0.1:5060, excluding our own.
// Exactly one stranger may exist; anything else aborts the dial.
func resolveNativeContact(ownPorts map[int]bool) (string, error) {
	data, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return "", fmt.Errorf("no connection table: %w", err)
	}
	return parseProcNetTCP(string(data), ownPorts, bticino.SIPUsernameForVariant(bticino.DeviceVariant))
}

func parseProcNetTCP(data string, ownPorts map[int]bool, user string) (string, error) {
	strangers := map[int]bool{}
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[3] != "01" {
			continue
		}
		local, remote := fields[1], fields[2]
		li := strings.LastIndex(local, ":")
		ri := strings.LastIndex(remote, ":")
		if li < 0 || ri < 0 {
			continue
		}
		var peer int
		switch {
		case strings.EqualFold(local[li+1:], "13C4"):
			fmt.Sscanf(remote[ri+1:], "%X", &peer)
		case strings.EqualFold(remote[ri+1:], "13C4"):
			fmt.Sscanf(local[li+1:], "%X", &peer)
		default:
			continue
		}
		if peer != 0 && !ownPorts[peer] {
			strangers[peer] = true
		}
	}
	if len(strangers) != 1 {
		return "", fmt.Errorf("want exactly 1 native contact, found %d", len(strangers))
	}
	for port := range strangers {
		return fmt.Sprintf("sip:%s@127.0.0.1:%d;transport=tcp", user, port), nil
	}
	return "", fmt.Errorf("unreachable")
}

func (c *BTicinoSIPClient) sessionNativeDevAddr() string {
	if c.config == nil {
		return ""
	}
	return c.config.NativeDevAddr
}

// buildCameraSDP offers speex audio + H264 video with fresh crypto keys,
// mirroring the verified app-shaped dialog.
func buildCameraSDP(nativeDevAddr string) (string, error) {
	key := func() (string, error) {
		raw := make([]byte, 30)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(raw), nil
	}
	k1, err := key()
	if err != nil {
		return "", err
	}
	k2, err := key()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("v=0\r\n"+
		"o=webrtc 3747 461 IN IP4 127.0.0.1\r\n"+
		"s=Talk\r\n"+
		"c=IN IP4 127.0.0.1\r\n"+
		"b=AS:380\r\n"+
		"t=0 0\r\n"+
		"a=rtcp-xr:rcvr-rtt=all:10000 stat-summary=loss,dup,jitt,TTL voip-metrics\r\n"+
		"a=DEVADDR:%s\r\n"+
		"a=nortpproxy:yes\r\n"+
		"m=audio 65000 RTP/SAVP 98 101\r\n"+
		"a=rtpmap:98 speex/8000\r\n"+
		"a=fmtp:98 vbr=on\r\n"+
		"a=rtpmap:101 telephone-event/8000\r\n"+
		"a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:%s\r\n"+
		"m=video 65002 RTP/SAVP 97\r\n"+
		"a=rtpmap:97 H264/90000\r\n"+
		"a=fmtp:97 profile-level-id=42801F\r\n"+
		"a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:%s\r\n",
		nativeDevAddr, k1, k2), nil
}

func buildCameraInvite(config *SIPConfig, uri, callID, fromTag string, cseq int, sdp string) string {
	username := cUsername(config)
	target := bticino.SIPUsernameForVariant(bticino.DeviceVariant)
	lines := []string{
		fmt.Sprintf("INVITE %s SIP/2.0", uri),
		fmt.Sprintf("Via: SIP/2.0/TCP 127.0.0.1:%d;rport;branch=z9hG4bK%s", config.LocalPort, randHex(10)),
		"Max-Forwards: 70",
		fmt.Sprintf("From: <sip:%s@%s>;tag=%s", username, config.Domain, fromTag),
		fmt.Sprintf("To: <sip:%s@%s>", target, config.Domain),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %d INVITE", cseq),
		fmt.Sprintf("Contact: <sip:%s@127.0.0.1:%d;transport=tcp>", username, config.LocalPort),
		"Supported: replaces, outbound, gruu",
		"Allow: INVITE, ACK, CANCEL, OPTIONS, BYE, REFER, NOTIFY, MESSAGE, SUBSCRIBE, INFO, PRACK, UPDATE",
		"User-Agent: BTicino-Bridge/1.0",
		"Content-Type: application/sdp",
		fmt.Sprintf("Content-Length: %d", len(sdp)),
		"",
		sdp,
	}
	return strings.Join(lines, "\r\n")
}

func cUsername(config *SIPConfig) string {
	username := config.Username
	if idx := strings.Index(username, "@"); idx > 0 {
		username = username[:idx]
	}
	return username
}

func randHex(n int) string {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	const digits = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, n)
	for i, b := range raw {
		out[i] = digits[int(b)%len(digits)]
	}
	return string(out)
}

// isBenignReadError reports routine read timeouts (a quiet hold), as
// opposed to connection failures that deserve tearing the session down.
func isBenignReadError(err error) bool {
	if err == nil {
		return true
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "timeout")
}

// Camera session end reasons, logged by runCameraSession.
const (
	sessionEndStopRequested = "stop requested"
	sessionEndNative        = "native ended the dialog"
	sessionEndReadErrors    = "dialog connection lost"
	sessionEndHoldElapsed   = "hold elapsed"
)

// drainCameraDialog holds the dialog for duration, answering mid-dialog
// re-INVITEs (app calls carry a "Media change" one) and returns why it ended.
func (c *BTicinoSIPClient) drainCameraDialog(dlg *cameraDialog, stop <-chan struct{}, duration time.Duration) string {
	conn, gen := c.identityConn(c.webrtcID)
	if conn == nil {
		return sessionEndReadErrors
	}
	deadline := time.Now().Add(duration)
	var errs int
	for {
		select {
		case <-stop:
			return sessionEndStopRequested
		default:
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return sessionEndHoldElapsed
		}
		if remaining > 2*time.Second {
			remaining = 2 * time.Second
		}
		msgs, err := c.readSIPMessagesFrom(conn, remaining)
		if err != nil {
			if isBenignReadError(err) {
				errs = 0
				continue
			}
			errs++
			if errs >= 5 {
				c.logger.WithError(err).Warn("Camera session: dialog connection lost")
				c.connError(c.webrtcID, gen, err)
				return sessionEndReadErrors
			}
			continue
		}
		errs = 0
		for _, m := range msgs {
			switch {
			case m.Method == "INVITE":
				c.answerCameraReinvite(conn, dlg, m)
			case m.Method == "BYE":
				c.sendBYEResponse(c.webrtcID, m)
				return sessionEndNative
			}
		}
	}
}

func (c *BTicinoSIPClient) answerCameraReinvite(conn net.Conn, dlg *cameraDialog, invite *SIPMessage) {
	cseq := invite.Headers["CSeq"]
	via := []string{}
	via = append(via, invite.ViaHeaders...)
	if len(via) == 0 {
		if v := invite.Headers["Via"]; v != "" {
			via = append(via, v)
		}
	}
	lines := []string{
		"SIP/2.0 200 OK",
	}
	for _, v := range via {
		lines = append(lines, fmt.Sprintf("Via: %s", v))
	}
	lines = append(lines,
		fmt.Sprintf("From: %s", invite.Headers["From"]),
		fmt.Sprintf("To: %s;tag=%s", strings.Split(invite.Headers["To"], ";tag=")[0], dlg.toTag),
		fmt.Sprintf("Call-ID: %s", dlg.callID),
		fmt.Sprintf("CSeq: %s", cseq),
		fmt.Sprintf("Contact: <sip:%s@127.0.0.1:%d;transport=tcp>", cUsername(c.config), c.config.LocalPort),
		"Content-Type: application/sdp",
		fmt.Sprintf("Content-Length: %d", len(dlg.offer)),
		"",
		dlg.offer,
	)
	conn.Write([]byte(strings.Join(lines, "\r\n")))
}

// establishCameraDialog sends the INVITE over our webrtc connection, waits
// for the native 200 OK and ACKs it. No fork is involved: the Request-URI
// already names the native contact.
func (c *BTicinoSIPClient) establishCameraDialog(uri, sdp string) (*cameraDialog, error) {
	conn, gen := c.identityConn(c.webrtcID)
	if conn == nil {
		return nil, fmt.Errorf("sip not connected")
	}
	callID := c.generateCallID()
	c.webrtcID.mutex.Lock()
	if c.webrtcID.fromTag == "" {
		c.webrtcID.fromTag = c.generateTag()
	}
	fromTag := c.webrtcID.fromTag
	cseq := c.webrtcID.cseq
	c.webrtcID.cseq++
	c.webrtcID.mutex.Unlock()

	inv := buildCameraInvite(c.config, uri, callID, fromTag, cseq, sdp)
	if _, err := conn.Write([]byte(inv)); err != nil {
		c.connError(c.webrtcID, gen, err)
		return nil, fmt.Errorf("send INVITE: %w", err)
	}
	deadline := time.Now().Add(12 * time.Second)
	provisional := false
	for time.Now().Before(deadline) {
		msgs, err := c.readSIPMessagesFrom(conn, time.Until(deadline))
		if err != nil {
			c.connError(c.webrtcID, gen, err)
			return nil, err
		}
		for _, response := range msgs {
			switch response.StatusCode {
			case "100", "180", "183":
				provisional = true
				continue
			case "200":
				if responseCallID(response) != callID {
					continue
				}
				toHeader := response.Headers["To"]
				toTag := c.extractTagFromHeader(toHeader)
				contact := ""
				if header := response.Headers["Contact"]; header != "" {
					if start := strings.Index(header, "<"); start >= 0 {
						if end := strings.Index(header, ">"); end > start {
							contact = header[start+1 : end]
						}
					}
				}
				if idx := strings.Index(contact, ";verified"); idx >= 0 {
					contact = contact[:idx]
				}
				if toTag == "" || contact == "" {
					return nil, fmt.Errorf("200 OK without dialog handles")
				}
				ack := c.buildACK(c.answerUser, response)
				if _, err := conn.Write([]byte(ack)); err != nil {
					return nil, fmt.Errorf("send ACK: %w", err)
				}
				return &cameraDialog{callID: callID, fromTag: fromTag, toTag: toTag, contact: contact, cseq: cseq, offer: sdp}, nil
			}
			if response.StatusCode >= "400" && responseCallID(response) == callID {
				return nil, classifyReject(response.StatusCode, provisional)
			}
		}
	}
	return nil, fmt.Errorf("no 200 OK within 12s")
}

// VerifyNativeDevAddr test-dials with the candidate address: 180/200 accepts
// it, an instant 486 rejects it. Tears the dialog down immediately.
func (c *BTicinoSIPClient) VerifyNativeDevAddr(candidate string) error {
	if candidate == "" {
		return fmt.Errorf("empty candidate")
	}
	c.mutex.RLock()
	busy := c.callState != Registered || c.sessionActive || c.cameraSessionActive
	c.mutex.RUnlock()
	if busy {
		return fmt.Errorf("busy, try when idle")
	}
	uri, err := c.resolveNative()
	if err != nil {
		return err
	}
	sdp, err := buildCameraSDP(candidate)
	if err != nil {
		return err
	}
	c.stopDialogWatch()
	c.webrtcID.readMu.Lock()
	defer c.webrtcID.readMu.Unlock()
	dlg, err := c.establish(uri, sdp)
	if err != nil {
		return err
	}
	c.terminate(dlg)
	return nil
}

// classifyReject separates a busy unit (provisionals first) from a policy
// rejection of the offer itself (bare final response).
func classifyReject(statusCode string, provisional bool) error {
	if provisional {
		return fmt.Errorf("%w: INVITE rejected: %s", ErrNativeBusy, statusCode)
	}
	return fmt.Errorf("INVITE rejected: %s", statusCode)
}

// terminateCameraDialog sends an in-dialog BYE to the native contact.
func (c *BTicinoSIPClient) terminateCameraDialog(dlg *cameraDialog) {
	conn := c.webrtcID.conn
	if conn == nil || dlg == nil {
		return
	}
	bye := fmt.Sprintf("BYE %s SIP/2.0\r\n"+
		"Via: SIP/2.0/TCP 127.0.0.1:%d;rport;branch=z9hG4bK%s\r\n"+
		"Max-Forwards: 70\r\n"+
		"From: <sip:%s@%s>;tag=%s\r\n"+
		"To: <sip:%s@%s>;tag=%s\r\n"+
		"Call-ID: %s\r\n"+
		"CSeq: %d BYE\r\n"+
		"User-Agent: BTicino-Bridge/1.0\r\n"+
		"Content-Length: 0\r\n"+
		"\r\n",
		dlg.contact, c.config.LocalPort, randHex(10),
		cUsername(c.config), c.config.Domain, dlg.fromTag,
		c.answerUser, c.config.Domain, dlg.toTag, dlg.callID, dlg.cseq+1)
	conn.Write([]byte(bye))
}
