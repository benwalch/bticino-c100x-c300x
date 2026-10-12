package sip

import (
	"bytes"
	"crypto/md5"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/bticino"
	"bticino_bridge/pkg/events"
)

type CallState int

const (
	Idle CallState = iota
	Registering
	Registered
	IncomingCall
	Ringing
	Connected
	VideoStreaming
	Terminating
)

func (c CallState) String() string {
	switch c {
	case Idle:
		return "Idle"
	case Registering:
		return "Registering"
	case Registered:
		return "Registered"
	case IncomingCall:
		return "IncomingCall"
	case Ringing:
		return "Ringing"
	case Connected:
		return "Connected"
	case VideoStreaming:
		return "VideoStreaming"
	case Terminating:
		return "Terminating"
	default:
		return "Unknown"
	}
}

type SIPConfig struct {
	Enabled    bool   `yaml:"enabled"`
	ServerAddr string `yaml:"server_addr"`
	Domain     string `yaml:"domain"`
	Username   string `yaml:"username"`
	// Password is the webrtc identity's HA1 hash. Empty (recommended: omit
	// it from config entirely) derives MD5(username:domain:username), the
	// same convention the answerer identity already uses. Set it only to
	// override with the hash from your flexisip users.db.txt.
	Password      string          `yaml:"password"`
	LocalIP       string          `yaml:"local_ip"`
	LocalPort     int             `yaml:"local_port"`
	DevAddr       string          `yaml:"dev_addr"`
	ExpirySeconds int             `yaml:"expiry_seconds"`
	RTSPPort      int             `yaml:"rtsp_port"`
	MediaPorts    MediaPortConfig `yaml:"media_ports"`
	TLSConfig     *tls.Config     `yaml:"-"`
	UseHA1        bool            `yaml:"use_ha1"`
	InsecureTLS   bool            `yaml:"insecure_tls"` // Skip TLS certificate verification
	Transport     string          `yaml:"transport"`    // "tcp", "udp", or "tls" (default: tls)
	SIPTarget     string          `yaml:"sip_target"`   // INVITE target user (default: per DeviceVariant)
}

type MediaPortConfig struct {
	AudioRTP int `yaml:"audio_rtp"`
	VideoRTP int `yaml:"video_rtp"`
	RTCPPort int `yaml:"rtcp_port"`
}

type SIPMessage struct {
	Method      string            `json:"method"`
	RequestURI  string            `json:"request_uri"`
	Version     string            `json:"version"`
	StatusCode  string            `json:"status_code"`
	Headers     map[string]string `json:"headers"`
	ViaHeaders  []string          `json:"via_headers"`  // All Via headers in order (SIP allows multiple)
	RecordRoute []string          `json:"record_route"` // All Record-Route headers
	Body        string            `json:"body"`
	Raw         string            `json:"raw"`
}

// sipIdentity represents one SIP registration (one TCP connection to Flexisip).
// We need two: "webrtc" (the caller) and the answerer (c300x/c100x per variant).
type sipIdentity struct {
	username string
	password string // HA1 hash
	conn     net.Conn
	fromTag  string
	callID   string
	cseq     int
	mutex    sync.Mutex
	// readBuf holds leftover TCP bytes from a previous read that did not
	// yet form a complete SIP message (framing, see readSIPMessagesFrom).
	readBuf []byte
}

type BTicinoSIPClient struct {
	config        *SIPConfig
	conn          net.Conn // webrtc connection (kept for backward compat)
	webrtcID      *sipIdentity
	localID       *sipIdentity
	callState     CallState
	callID        string
	fromTag       string
	toTag         string
	cseq          int
	eventBus      events.EventBus
	logger        *logrus.Logger
	mutex         sync.RWMutex
	stopCh        chan struct{}
	registerTick  *time.Ticker
	running       bool
	remoteTag     string
	sessionActive bool
	// callConnectedCh is signaled when MakeCall() completes with 200 OK + ACK
	callConnectedCh chan struct{}
	// originatedCalls tracks Call-IDs of self-INVITEs we originated (for video
	// streaming), so an incoming INVITE with an unknown Call-ID can be told
	// apart as a REAL external call (e.g. a companion/app calling the unit).
	originatedCalls map[string]time.Time
	// answerUser is the intercom's SIP identity (INVITE target / answerer),
	// derived from bticino.DeviceVariant ("c300x" or "c100x").
	answerUser string
}

type VideoStreamInfo struct {
	LocalIP    string    `json:"local_ip"`
	LocalPort  int       `json:"local_port"`
	RemoteIP   string    `json:"remote_ip"`
	RemotePort int       `json:"remote_port"`
	Codec      string    `json:"codec"`
	StartTime  time.Time `json:"start_time"`
	RTSPUrl    string    `json:"rtsp_url"`
	Active     bool      `json:"active"`
}

func NewBTicinoSIPClient(config *SIPConfig, eventBus events.EventBus, logger *logrus.Logger) *BTicinoSIPClient {
	if logger == nil {
		logger = logrus.New()
	}

	if config.MediaPorts.AudioRTP == 0 {
		config.MediaPorts.AudioRTP = 7076
	}
	if config.MediaPorts.VideoRTP == 0 {
		config.MediaPorts.VideoRTP = 9078
	}
	if config.MediaPorts.RTCPPort == 0 {
		config.MediaPorts.RTCPPort = config.MediaPorts.VideoRTP + 1
	}

	if config.ExpirySeconds == 0 {
		config.ExpirySeconds = 300
	}

	if config.DevAddr == "" {
		config.DevAddr = "20"
	}

	if config.LocalPort == 0 {
		config.LocalPort = 5090
	}

	if config.SIPTarget == "" {
		config.SIPTarget = bticino.SIPUsernameForVariant(bticino.DeviceVariant)
	}

	// No password configured: derive the webrtc HA1 the same way the
	// answerer identity does (MD5(username:domain:username)). Flexisip
	// accounts created with password == username authenticate with this.
	if config.Password == "" {
		config.Password = fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%s:%s:%s",
			config.Username, config.Domain, config.Username))))
	}

	if config.TLSConfig == nil {
		host := config.ServerAddr
		if strings.Contains(host, ":") {
			host = strings.Split(host, ":")[0]
		}
		config.TLSConfig = &tls.Config{
			ServerName:         host,
			InsecureSkipVerify: config.InsecureTLS,
		}
	}

	return &BTicinoSIPClient{
		config:          config,
		callState:       Idle,
		cseq:            1,
		eventBus:        eventBus,
		logger:          logger,
		stopCh:          make(chan struct{}),
		callConnectedCh: make(chan struct{}, 1),
		originatedCalls: make(map[string]time.Time),
		answerUser:      bticino.SIPUsernameForVariant(bticino.DeviceVariant),
	}
}

func (c *BTicinoSIPClient) Start() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.running {
		return fmt.Errorf("SIP client already running")
	}

	c.logger.Infof("Starting BTicino SIP client (dual-role: webrtc + %s)", c.answerUser)
	c.logger.Infof("SIP Config: server=%s, domain=%s, username=%s, devaddr=%s",
		c.config.ServerAddr, c.config.Domain, c.config.Username, c.config.DevAddr)

	// Compute HA1 for the answerer identity: MD5(<user>:<domain>:<user>)
	answerHA1 := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%s:%s:%s", c.answerUser, c.config.Domain, c.answerUser))))

	c.webrtcID = &sipIdentity{
		username: c.config.Username, // "webrtc"
		password: c.config.Password, // HA1 hash for webrtc
		fromTag:  c.generateTag(),
		callID:   c.generateCallID(),
		cseq:     1,
	}

	c.localID = &sipIdentity{
		username: c.answerUser,
		password: answerHA1,
		fromTag:  c.generateTag(),
		callID:   c.generateCallID(),
		cseq:     1,
	}

	// Keep legacy fields in sync
	c.fromTag = c.webrtcID.fromTag
	c.callID = c.webrtcID.callID

	// Register webrtc identity
	c.logger.Info("=== Registering webrtc identity ===")
	if err := c.registerIdentity(c.webrtcID); err != nil {
		return fmt.Errorf("failed to register webrtc: %v", err)
	}
	c.conn = c.webrtcID.conn // backward compat
	c.logger.Info("webrtc registered successfully")

	// Register answerer identity (separate TCP connection)
	c.logger.Infof("=== Registering %s identity ===", c.answerUser)
	if err := c.registerIdentity(c.localID); err != nil {
		// Not fatal — we can still try calls, but auto-answer won't work
		c.logger.WithError(err).Warnf("Failed to register %s identity — auto-answer disabled", c.answerUser)
		c.localID = nil
	} else {
		c.logger.Infof("%s registered successfully", c.answerUser)
		// Start listener for incoming INVITEs on the answerer connection
		go c.localListener()
	}

	c.callState = Registered

	c.eventBus.PublishWithSource("sip.registered", map[string]interface{}{
		"domain":    c.config.Domain,
		"username":  c.config.Username,
		"server":    c.config.ServerAddr,
		"dual_role": c.localID != nil,
	}, "sip")

	// Start re-registration timer
	c.registerTick = time.NewTicker(time.Duration(c.config.ExpirySeconds-30) * time.Second)
	go c.registrationManager()

	c.running = true
	c.logger.Info("BTicino SIP client started successfully (dual-role)")

	return nil
}

func (c *BTicinoSIPClient) Stop() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.running {
		return nil
	}

	c.logger.Info("Stopping BTicino SIP client")

	if c.registerTick != nil {
		c.registerTick.Stop()
	}

	// Unregister and close webrtc
	if c.webrtcID != nil && c.webrtcID.conn != nil {
		c.unregisterIdentity(c.webrtcID)
		c.webrtcID.conn.Close()
	}

	// Unregister and close answerer identity
	if c.localID != nil && c.localID.conn != nil {
		c.unregisterIdentity(c.localID)
		c.localID.conn.Close()
	}

	close(c.stopCh)

	c.running = false
	c.callState = Idle
	c.sessionActive = false

	c.logger.Info("BTicino SIP client stopped")
	return nil
}

// dialSIP opens a TCP/UDP/TLS connection to the SIP server
func (c *BTicinoSIPClient) dialSIP() (net.Conn, error) {
	transport := c.config.Transport
	if transport == "" {
		transport = "tls"
	}

	var conn net.Conn
	var err error

	switch transport {
	case "udp":
		udpAddr, resolveErr := net.ResolveUDPAddr("udp", c.config.ServerAddr)
		if resolveErr != nil {
			return nil, fmt.Errorf("failed to resolve UDP address: %v", resolveErr)
		}
		conn, err = net.DialUDP("udp", nil, udpAddr)
	case "tcp":
		conn, err = net.DialTimeout("tcp", c.config.ServerAddr, 10*time.Second)
	default:
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", c.config.ServerAddr, c.config.TLSConfig)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect to SIP server via %s: %v", transport, err)
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(true)
	}

	return conn, nil
}

// registerIdentity registers a single SIP identity with Flexisip
func (c *BTicinoSIPClient) registerIdentity(id *sipIdentity) error {
	conn, err := c.dialSIP()
	if err != nil {
		return err
	}
	id.conn = conn

	c.logger.Infof("registerIdentity(%s): connected to %s, local addr: %s",
		id.username, conn.RemoteAddr(), conn.LocalAddr())

	registerMsg := c.buildRegisterRequestForIdentity(id)
	c.logger.Debugf("registerIdentity(%s): sending REGISTER:\n%s", id.username, registerMsg)

	if _, err := id.conn.Write([]byte(registerMsg)); err != nil {
		return fmt.Errorf("failed to send REGISTER for %s: %v", id.username, err)
	}

	response, err := c.readSIPMessageFrom(id.conn, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to read REGISTER response for %s: %v", id.username, err)
	}

	c.logger.Infof("registerIdentity(%s): got response: status=%s", id.username, response.StatusCode)

	if strings.Contains(response.Raw, "200 ") || strings.Contains(response.Raw, "200 OK") || strings.Contains(response.Raw, "200 Registration") {
		c.logger.Infof("registerIdentity(%s): registered (no auth required, trusted-hosts)", id.username)
		return nil
	} else if strings.Contains(response.Raw, "401 Unauthorized") {
		return c.handleAuthChallengeForIdentity(id, response)
	}

	return fmt.Errorf("registration failed for %s: %s", id.username, response.Raw)
}

// handleAuthChallengeForIdentity handles 401 for a specific identity
func (c *BTicinoSIPClient) handleAuthChallengeForIdentity(id *sipIdentity, response *SIPMessage) error {
	wwwAuth := response.Headers["WWW-Authenticate"]
	if wwwAuth == "" {
		return fmt.Errorf("no WWW-Authenticate header in 401 response for %s", id.username)
	}

	realm := c.extractAuthParam(wwwAuth, "realm")
	nonce := c.extractAuthParam(wwwAuth, "nonce")
	qop := c.extractAuthParam(wwwAuth, "qop")

	if realm == "" || nonce == "" {
		return fmt.Errorf("missing realm or nonce in auth challenge for %s", id.username)
	}

	c.logger.Infof("registerIdentity(%s): handling auth challenge realm=%s", id.username, realm)

	authMsg := c.buildAuthenticatedRegisterForIdentity(id, realm, nonce, qop)

	if _, err := id.conn.Write([]byte(authMsg)); err != nil {
		return fmt.Errorf("failed to send authenticated REGISTER for %s: %v", id.username, err)
	}

	finalResponse, err := c.readSIPMessageFrom(id.conn, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to read auth response for %s: %v", id.username, err)
	}

	if strings.Contains(finalResponse.Raw, "200") {
		c.logger.Infof("registerIdentity(%s): authenticated and registered", id.username)
		return nil
	}

	return fmt.Errorf("authentication failed for %s: %s", id.username, finalResponse.Raw)
}

// unregisterIdentity sends REGISTER with expires=0
func (c *BTicinoSIPClient) unregisterIdentity(id *sipIdentity) {
	if id == nil || id.conn == nil {
		return
	}
	msg := c.buildUnregisterRequestForIdentity(id)
	id.conn.Write([]byte(msg))
}

// localListener listens for incoming SIP messages on the answerer identity
// connection. When an INVITE arrives (from Flexisip routing the webrtc
// INVITE to the answerer), it auto-answers with 200 OK.
func (c *BTicinoSIPClient) localListener() {
	c.logger.Infof("%s listener started — waiting for incoming INVITEs", c.answerUser)

	for {
		select {
		case <-c.stopCh:
			c.logger.Infof("%s listener stopping", c.answerUser)
			return
		default:
		}

		if c.localID == nil || c.localID.conn == nil {
			c.logger.Warnf("%s connection lost, listener exiting", c.answerUser)
			return
		}

		msg, err := c.readSIPMessageFrom(c.localID.conn, 60*time.Second)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Timeout is normal — just keep listening
				continue
			}
			c.logger.WithError(err).Warnf("%s listener: read error", c.answerUser)
			// Try to reconnect
			time.Sleep(2 * time.Second)
			continue
		}

		c.logger.Infof("%s listener: received %s (status=%s)", c.answerUser, msg.Method, msg.StatusCode)

		if msg.Method == "INVITE" {
			c.handleIncomingInvite(msg)
		} else if msg.Method == "OPTIONS" {
			// Flexisip keepalive — respond with 200 OK
			c.respondToOptions(c.localID, msg)
		} else if msg.Method == "ACK" {
			c.logger.Infof("%s listener: received ACK — SIP session fully established!", c.answerUser)
			// The ACK completes the 3-way handshake
			c.mutex.Lock()
			c.callState = Connected
			c.sessionActive = true
			c.mutex.Unlock()

			// Signal that call is connected
			select {
			case c.callConnectedCh <- struct{}{}:
			default:
			}

			c.eventBus.PublishWithSource("sip.call.connected", map[string]interface{}{
				"reason": "auto_answer_ack_received",
			}, "sip")
		} else if msg.Method == "BYE" {
			c.logger.Infof("%s listener: received BYE — call ended by remote", c.answerUser)
			// Send 200 OK for BYE
			c.sendBYEResponse(c.localID, msg)
			c.mutex.Lock()
			c.callState = Registered
			c.sessionActive = false
			c.mutex.Unlock()
			c.eventBus.PublishWithSource("sip.call.ended", map[string]interface{}{
				"reason": "remote_bye",
			}, "sip")
		} else {
			c.logger.Infof("%s listener: ignoring %s", c.answerUser, msg.Method)
		}
	}
}

// handleIncomingInvite auto-answers an INVITE received on the answerer identity
func (c *BTicinoSIPClient) handleIncomingInvite(invite *SIPMessage) {
	c.logger.Infof("=== %s: Auto-answering incoming INVITE ===", c.answerUser)
	c.logger.Debugf("INVITE:\n%s", invite.Raw)

	// Extract key headers from the INVITE
	callID := invite.Headers["Call-ID"]
	if callID == "" {
		callID = invite.Headers["Call-Id"]
	}
	if callID == "" {
		callID = invite.Headers["call-id"]
	}
	from := invite.Headers["From"]
	if from == "" {
		from = invite.Headers["from"]
	}
	to := invite.Headers["To"]
	if to == "" {
		to = invite.Headers["to"]
	}
	cseqHeader := invite.Headers["CSeq"]
	if cseqHeader == "" {
		cseqHeader = invite.Headers["Cseq"]
	}

	// Use ALL Via headers from the INVITE (critical for proxy routing)
	viaHeaders := invite.ViaHeaders
	if len(viaHeaders) == 0 {
		// Fallback to single Via from Headers map
		via := invite.Headers["Via"]
		if via == "" {
			via = invite.Headers["via"]
		}
		if via != "" {
			viaHeaders = []string{via}
		}
	}
	c.logger.Infof("%s auto-answer: Call-ID=%s, From=%s, To=%s, ViaCount=%d", c.answerUser, callID, from, to, len(viaHeaders))

	// Is this our own self-INVITE (video streaming) or a real external call?
	c.mutex.Lock()
	_, isOwn := c.originatedCalls[callID]
	c.mutex.Unlock()
	if !isOwn {
		caller := extractSIPUser(from)
		c.logger.Infof("=== %s: REAL incoming call from %q (Call-ID=%s) ===", c.answerUser, caller, callID)
		c.eventBus.PublishWithSource("sip.call.incoming", map[string]interface{}{
			"caller":  caller,
			"from":    from,
			"call_id": callID,
		}, "sip")
	}

	// Use Record-Route headers (critical for proxy routing)
	recordRoute := invite.RecordRoute

	// First send 100 Trying
	tryingMsg := c.build100Trying(callID, from, to, viaHeaders, recordRoute, cseqHeader)
	c.logger.Debugf("Sending 100 Trying:\n%s", tryingMsg)
	if _, err := c.localID.conn.Write([]byte(tryingMsg)); err != nil {
		c.logger.WithError(err).Error("Failed to send 100 Trying")
		return
	}

	// Small delay to simulate processing
	time.Sleep(50 * time.Millisecond)

	// Build and send 200 OK with SDP
	// Add our tag to the To header
	toTag := c.generateTag()
	toWithTag := to
	if !strings.Contains(to, "tag=") {
		toWithTag = to + ";tag=" + toTag
	}

	sdp := c.buildSDP()
	okMsg := c.build200OK(callID, from, toWithTag, viaHeaders, recordRoute, cseqHeader, sdp)

	c.logger.Infof("%s: Sending 200 OK with SDP (tag=%s)", c.answerUser, toTag)
	c.logger.Debugf("200 OK:\n%s", okMsg)

	if _, err := c.localID.conn.Write([]byte(okMsg)); err != nil {
		c.logger.WithError(err).Error("Failed to send 200 OK")
		return
	}

	// Store call info
	c.mutex.Lock()
	c.callID = callID
	c.toTag = toTag
	c.callState = IncomingCall
	c.mutex.Unlock()

	c.logger.Infof("%s: 200 OK sent, waiting for ACK from caller...", c.answerUser)
	// ACK will be received by the localListener loop
}

// extractSIPUser pulls the user part out of a From/To header value, e.g.
// `"Doorbell" <sip:c100x@domain>;tag=abc` -> "c100x" (c300x on Classe 300X).
// Falls back to the raw header if no SIP URI is found.
func extractSIPUser(header string) string {
	start := strings.Index(header, "sip:")
	if start < 0 {
		return strings.TrimSpace(header)
	}
	rest := header[start+4:]
	if at := strings.IndexByte(rest, '@'); at >= 0 {
		return rest[:at]
	}
	// No @ — cut at the first delimiter
	if end := strings.IndexAny(rest, ">;? "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// build100Trying builds a SIP 100 Trying response with all Via and Record-Route headers
func (c *BTicinoSIPClient) build100Trying(callID, from, to string, viaHeaders, recordRoute []string, cseq string) string {
	lines := []string{
		"SIP/2.0 100 Trying",
	}
	// Include ALL Via headers in order (critical for proxy routing)
	for _, via := range viaHeaders {
		lines = append(lines, fmt.Sprintf("Via: %s", via))
	}
	// Include Record-Route headers
	for _, rr := range recordRoute {
		lines = append(lines, fmt.Sprintf("Record-Route: %s", rr))
	}
	lines = append(lines,
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %s", cseq),
		"User-Agent: BTicino-Bridge/1.0",
		"Content-Length: 0",
		"",
		"",
	)
	return strings.Join(lines, "\r\n")
}

// build200OK builds a SIP 200 OK response with SDP and all Via/Record-Route headers
func (c *BTicinoSIPClient) build200OK(callID, from, to string, viaHeaders, recordRoute []string, cseq, sdp string) string {
	transport := c.config.Transport
	if transport == "" {
		transport = "tcp"
	}

	contact := fmt.Sprintf("<sip:%s@%s:%d;transport=%s>",
		c.answerUser, c.config.LocalIP, c.config.LocalPort, transport)

	lines := []string{
		"SIP/2.0 200 OK",
	}
	// Include ALL Via headers in order (critical for proxy routing)
	for _, via := range viaHeaders {
		lines = append(lines, fmt.Sprintf("Via: %s", via))
	}
	// Include Record-Route headers
	for _, rr := range recordRoute {
		lines = append(lines, fmt.Sprintf("Record-Route: %s", rr))
	}
	lines = append(lines,
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %s", cseq),
		fmt.Sprintf("Contact: %s", contact),
		"Allow: INVITE, ACK, CANCEL, OPTIONS, BYE, REFER, NOTIFY, MESSAGE, SUBSCRIBE, INFO, UPDATE",
		"Supported: replaces, outbound, gruu",
		"User-Agent: BTicino-Bridge/1.0",
		"Content-Type: application/sdp",
		fmt.Sprintf("Content-Length: %d", len(sdp)),
		"",
		sdp,
	)
	return strings.Join(lines, "\r\n")
}

// sendBYEResponse sends 200 OK in response to a BYE
func (c *BTicinoSIPClient) sendBYEResponse(id *sipIdentity, bye *SIPMessage) {
	callID := bye.Headers["Call-ID"]
	if callID == "" {
		callID = bye.Headers["Call-Id"]
	}
	from := bye.Headers["From"]
	to := bye.Headers["To"]
	cseq := bye.Headers["CSeq"]

	lines := []string{
		"SIP/2.0 200 OK",
	}
	// Include all Via headers
	for _, via := range bye.ViaHeaders {
		lines = append(lines, fmt.Sprintf("Via: %s", via))
	}
	if len(bye.ViaHeaders) == 0 {
		if via := bye.Headers["Via"]; via != "" {
			lines = append(lines, fmt.Sprintf("Via: %s", via))
		}
	}
	lines = append(lines,
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %s", cseq),
		"User-Agent: BTicino-Bridge/1.0",
		"Content-Length: 0",
		"",
		"",
	)
	msg := strings.Join(lines, "\r\n")
	if _, err := id.conn.Write([]byte(msg)); err != nil {
		c.logger.WithError(err).Error("Failed to send BYE response")
	}
}

// respondToOptions sends 200 OK to OPTIONS keepalive
func (c *BTicinoSIPClient) respondToOptions(id *sipIdentity, options *SIPMessage) {
	callID := options.Headers["Call-ID"]
	if callID == "" {
		callID = options.Headers["Call-Id"]
	}
	from := options.Headers["From"]
	to := options.Headers["To"]
	cseq := options.Headers["CSeq"]

	lines := []string{
		"SIP/2.0 200 OK",
	}
	// Include all Via headers
	for _, via := range options.ViaHeaders {
		lines = append(lines, fmt.Sprintf("Via: %s", via))
	}
	if len(options.ViaHeaders) == 0 {
		if via := options.Headers["Via"]; via != "" {
			lines = append(lines, fmt.Sprintf("Via: %s", via))
		}
	}
	lines = append(lines,
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %s", cseq),
		"Allow: INVITE, ACK, CANCEL, OPTIONS, BYE, REFER, NOTIFY, MESSAGE, SUBSCRIBE, INFO, UPDATE",
		"User-Agent: BTicino-Bridge/1.0",
		"Content-Length: 0",
		"",
		"",
	)
	msg := strings.Join(lines, "\r\n")
	if _, err := id.conn.Write([]byte(msg)); err != nil {
		c.logger.WithError(err).Warn("Failed to send OPTIONS response")
	}
}

// MakeCall sends INVITE from the webrtc identity and waits for the full
// SIP handshake to complete (including auto-answer from the answerer identity).
// Returns only when the call is Connected or an error occurs.
func (c *BTicinoSIPClient) MakeCall(target string) error {
	c.mutex.Lock()
	if c.callState != Registered {
		c.mutex.Unlock()
		return fmt.Errorf("not registered, cannot make call (state=%s)", c.callState)
	}
	c.mutex.Unlock()

	c.logger.Infof("=== MAKECALL: Initiating call to %s with DEVADDR:%s ===", target, c.config.DevAddr)

	// Generate new call identifiers
	c.mutex.Lock()
	c.callID = c.generateCallID()
	c.remoteTag = ""
	// Record this Call-ID as self-originated so the auto-answer path can tell
	// it apart from a real external incoming call. Prune entries older than 2m.
	now := time.Now()
	for id, t := range c.originatedCalls {
		if now.Sub(t) > 2*time.Minute {
			delete(c.originatedCalls, id)
		}
	}
	c.originatedCalls[c.callID] = now
	c.mutex.Unlock()

	// Drain the connected channel
	select {
	case <-c.callConnectedCh:
	default:
	}

	// Build and send INVITE from webrtc identity
	inviteMsg := c.buildInviteRequest(target)
	c.logger.Infof("=== MAKECALL: Sending INVITE ===\n%s", inviteMsg)

	c.webrtcID.mutex.Lock()
	_, err := c.webrtcID.conn.Write([]byte(inviteMsg))
	c.webrtcID.mutex.Unlock()
	if err != nil {
		return fmt.Errorf("failed to send INVITE: %v", err)
	}

	c.logger.Info("=== MAKECALL: INVITE sent, reading SIP responses ===")

	// Read responses in a loop until we get 200 OK or error
	deadline := time.Now().Add(30 * time.Second)
	gotOK := false

	for time.Now().Before(deadline) {
		messages, readErr := c.readSIPMessagesFrom(c.webrtcID.conn, time.Until(deadline))
		if readErr != nil {
			if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() {
				c.logger.Warn("=== MAKECALL: Timeout waiting for response ===")
				break
			}
			return fmt.Errorf("failed to read INVITE response: %v", readErr)
		}

		for _, response := range messages {
			c.logger.Infof("=== MAKECALL: Got response: status=%s method=%s ===", response.StatusCode, response.Method)
			c.logger.Debugf("=== MAKECALL: Full response:\n%s", response.Raw)

			// Handle provisional responses
			if response.StatusCode == "100" {
				c.logger.Info("=== MAKECALL: 100 Trying — continuing ===")
				continue
			}
			if response.StatusCode == "180" || response.StatusCode == "183" {
				c.logger.Info("=== MAKECALL: Ringing ===")
				c.mutex.Lock()
				c.callState = Ringing
				c.mutex.Unlock()
				continue
			}

			// Handle 200 OK — send ACK
			if response.StatusCode == "200" {
				c.logger.Info("=== MAKECALL: Got 200 OK! Sending ACK ===")

				// Extract To tag from response
				toHeader := response.Headers["To"]
				if toHeader == "" {
					toHeader = response.Headers["to"]
				}
				if toTag := c.extractTagFromHeader(toHeader); toTag != "" {
					c.mutex.Lock()
					c.remoteTag = toTag
					c.toTag = toTag
					c.mutex.Unlock()
				}

				// Send ACK
				ackMsg := c.buildACK(target, response)
				c.logger.Debugf("=== MAKECALL: ACK message:\n%s", ackMsg)

				c.webrtcID.mutex.Lock()
				_, ackErr := c.webrtcID.conn.Write([]byte(ackMsg))
				c.webrtcID.mutex.Unlock()
				if ackErr != nil {
					return fmt.Errorf("failed to send ACK: %v", ackErr)
				}

				c.logger.Info("=== MAKECALL: ACK sent! Call connected! ===")
				gotOK = true

				c.mutex.Lock()
				c.callState = Connected
				c.sessionActive = true
				c.mutex.Unlock()

				// Signal connected
				select {
				case c.callConnectedCh <- struct{}{}:
				default:
				}

				c.eventBus.PublishWithSource("sip.call.connected", map[string]interface{}{
					"target":  target,
					"devaddr": c.config.DevAddr,
				}, "sip")

				break
			}

			// Handle errors
			if response.StatusCode >= "400" {
				return fmt.Errorf("INVITE rejected: %s %s", response.StatusCode, response.Raw[:min(200, len(response.Raw))])
			}
		}

		if gotOK {
			break
		}
	}

	if !gotOK {
		// If we didn't get 200 OK from the webrtc side, wait for the answerer auto-answer
		// path to complete (the ACK received on the local listener signals Connected)
		c.logger.Infof("=== MAKECALL: No 200 OK on webrtc conn yet, waiting for %s auto-answer path ===", c.answerUser)

		select {
		case <-c.callConnectedCh:
			c.logger.Infof("=== MAKECALL: Connected via %s auto-answer! ===", c.answerUser)
		case <-time.After(15 * time.Second):
			c.logger.Warn("=== MAKECALL: Timeout waiting for call to connect ===")
			c.mutex.Lock()
			// Still set session active so we can try *7*300 anyway
			c.callState = IncomingCall
			c.sessionActive = true
			c.mutex.Unlock()
		}
	}

	c.eventBus.PublishWithSource("sip.call.initiated", map[string]interface{}{
		"target":    target,
		"devaddr":   c.config.DevAddr,
		"connected": gotOK || c.GetCallState() == Connected,
	}, "sip")

	c.logger.Infof("=== MAKECALL: Completed, state=%s ===", c.GetCallState())
	return nil
}

// WaitForConnected blocks until the call reaches Connected state or timeout
func (c *BTicinoSIPClient) WaitForConnected(timeout time.Duration) bool {
	if c.GetCallState() == Connected {
		return true
	}

	select {
	case <-c.callConnectedCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

// buildACK builds an ACK message for a 200 OK response
func (c *BTicinoSIPClient) buildACK(target string, okResponse *SIPMessage) string {
	requestURI := fmt.Sprintf("sip:%s@%s", target, c.config.Domain)

	// Use the contact from 200 OK as Request-URI if available
	if contactHeader := okResponse.Headers["Contact"]; contactHeader != "" {
		if start := strings.Index(contactHeader, "<"); start >= 0 {
			if end := strings.Index(contactHeader, ">"); end > start {
				requestURI = contactHeader[start+1 : end]
			}
		}
	}

	from := fmt.Sprintf("sip:%s@%s", c.config.Username, c.config.Domain)
	to := fmt.Sprintf("sip:%s@%s", target, c.config.Domain)

	// Add tags
	fromWithTag := fmt.Sprintf("%s;tag=%s", from, c.webrtcID.fromTag)
	toWithTag := to
	if toHeader := okResponse.Headers["To"]; toHeader != "" {
		toWithTag = toHeader
	} else if toHeader := okResponse.Headers["to"]; toHeader != "" {
		toWithTag = toHeader
	}

	callID := okResponse.Headers["Call-ID"]
	if callID == "" {
		callID = okResponse.Headers["Call-Id"]
	}
	if callID == "" {
		c.mutex.RLock()
		callID = c.callID
		c.mutex.RUnlock()
	}

	// CSeq for ACK must match the INVITE CSeq number
	cseqHeader := okResponse.Headers["CSeq"]
	cseqNum := "1"
	if cseqHeader != "" {
		parts := strings.Fields(cseqHeader)
		if len(parts) > 0 {
			cseqNum = parts[0]
		}
	}

	transport := c.config.Transport
	if transport == "" {
		transport = "tcp"
	}
	viaProtocol := "SIP/2.0/" + strings.ToUpper(transport)

	lines := []string{
		fmt.Sprintf("ACK %s SIP/2.0", requestURI),
		fmt.Sprintf("Via: %s %s:%d;branch=z9hG4bK%s;rport", viaProtocol, c.config.LocalIP, c.config.LocalPort, c.generateBranch()),
		"Max-Forwards: 70",
		fmt.Sprintf("From: %s", fromWithTag),
		fmt.Sprintf("To: %s", toWithTag),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %s ACK", cseqNum),
		"User-Agent: BTicino-Bridge/1.0",
		"Content-Length: 0",
		"",
		"",
	}

	return strings.Join(lines, "\r\n")
}

// extractTagFromHeader extracts tag=xxx from a From/To header
func (c *BTicinoSIPClient) extractTagFromHeader(header string) string {
	parts := strings.Split(header, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "tag=") {
			return strings.TrimPrefix(part, "tag=")
		}
	}
	return ""
}

// buildRegisterRequestForIdentity builds a REGISTER for a specific identity
func (c *BTicinoSIPClient) buildRegisterRequestForIdentity(id *sipIdentity) string {
	requestURI := fmt.Sprintf("sip:%s", c.config.Domain)
	from := fmt.Sprintf("sip:%s@%s", id.username, c.config.Domain)
	to := from

	transport := c.config.Transport
	if transport == "" {
		transport = "tls"
	}
	viaProtocol := "SIP/2.0/" + strings.ToUpper(transport)

	// Determine local address from connection if available
	localIP := c.config.LocalIP
	localPort := c.config.LocalPort

	contact := fmt.Sprintf("<sip:%s@%s:%d;transport=%s>;+sip.instance=\"<urn:uuid:19609c0e-f27b-7595-e9c8269557c4240b>\"",
		id.username, localIP, localPort, transport)

	id.mutex.Lock()
	cseq := id.cseq
	id.cseq++
	id.mutex.Unlock()

	lines := []string{
		fmt.Sprintf("REGISTER %s SIP/2.0", requestURI),
		fmt.Sprintf("Via: %s %s:%d;branch=z9hG4bK%s;rport", viaProtocol, localIP, localPort, c.generateBranch()),
		"Max-Forwards: 70",
		fmt.Sprintf("From: %s;tag=%s", from, id.fromTag),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Call-ID: %s", id.callID),
		fmt.Sprintf("CSeq: %d REGISTER", cseq),
		fmt.Sprintf("Contact: %s;expires=%d", contact, c.config.ExpirySeconds),
		"Supported: replaces, outbound, gruu",
		"Allow: INVITE, ACK, CANCEL, OPTIONS, BYE, REFER, NOTIFY, MESSAGE, SUBSCRIBE, INFO, UPDATE",
		"User-Agent: BTicino-Bridge/1.0",
		"Content-Length: 0",
		"",
		"",
	}

	return strings.Join(lines, "\r\n")
}

// buildAuthenticatedRegisterForIdentity builds an authenticated REGISTER
func (c *BTicinoSIPClient) buildAuthenticatedRegisterForIdentity(id *sipIdentity, realm, nonce, qop string) string {
	requestURI := fmt.Sprintf("sip:%s", c.config.Domain)
	from := fmt.Sprintf("sip:%s@%s", id.username, c.config.Domain)
	to := from

	transport := c.config.Transport
	if transport == "" {
		transport = "tls"
	}
	viaTransport := strings.ToUpper(transport)

	localIP := c.config.LocalIP
	localPort := c.config.LocalPort
	contact := fmt.Sprintf("<sip:%s@%s:%d;transport=%s>", id.username, localIP, localPort, strings.ToLower(viaTransport))

	// Calculate auth response using HA1
	ha1 := id.password // Already an HA1 hash
	ha2 := fmt.Sprintf("REGISTER:%s", requestURI)
	ha2Hash := fmt.Sprintf("%x", md5.Sum([]byte(ha2)))
	responseData := fmt.Sprintf("%s:%s:%s", ha1, nonce, ha2Hash)
	authResponse := fmt.Sprintf("%x", md5.Sum([]byte(responseData)))

	authHeader := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
		id.username, realm, nonce, requestURI, authResponse)
	if qop != "" {
		authHeader += fmt.Sprintf(", qop=%s, nc=00000001, cnonce=%s", qop, c.generateCNonce())
	}

	id.mutex.Lock()
	cseq := id.cseq
	id.cseq++
	id.mutex.Unlock()

	lines := []string{
		fmt.Sprintf("REGISTER %s SIP/2.0", requestURI),
		fmt.Sprintf("Via: SIP/2.0/%s %s:%d;branch=z9hG4bK%s;rport", viaTransport, localIP, localPort, c.generateBranch()),
		"Max-Forwards: 70",
		fmt.Sprintf("From: %s;tag=%s", from, id.fromTag),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Call-ID: %s", id.callID),
		fmt.Sprintf("CSeq: %d REGISTER", cseq),
		fmt.Sprintf("Contact: %s;expires=%d", contact, c.config.ExpirySeconds),
		fmt.Sprintf("Authorization: %s", authHeader),
		"User-Agent: BTicino-Bridge/1.0",
		"Allow: INVITE, ACK, CANCEL, OPTIONS, BYE, REGISTER, INFO, NOTIFY, MESSAGE",
		"Content-Length: 0",
		"",
		"",
	}

	return strings.Join(lines, "\r\n")
}

// buildUnregisterRequestForIdentity builds REGISTER with expires=0
func (c *BTicinoSIPClient) buildUnregisterRequestForIdentity(id *sipIdentity) string {
	requestURI := fmt.Sprintf("sip:%s", c.config.Domain)
	from := fmt.Sprintf("sip:%s@%s", id.username, c.config.Domain)
	to := from

	transport := c.config.Transport
	if transport == "" {
		transport = "tls"
	}
	viaProtocol := "SIP/2.0/" + strings.ToUpper(transport)
	localIP := c.config.LocalIP
	localPort := c.config.LocalPort
	contact := fmt.Sprintf("<sip:%s@%s:%d;transport=%s>", id.username, localIP, localPort, transport)

	id.mutex.Lock()
	cseq := id.cseq
	id.cseq++
	id.mutex.Unlock()

	lines := []string{
		fmt.Sprintf("REGISTER %s SIP/2.0", requestURI),
		fmt.Sprintf("Via: %s %s:%d;branch=z9hG4bK%s;rport", viaProtocol, localIP, localPort, c.generateBranch()),
		"Max-Forwards: 70",
		fmt.Sprintf("From: %s;tag=%s", from, id.fromTag),
		fmt.Sprintf("To: %s;tag=%s", to, id.fromTag),
		fmt.Sprintf("Call-ID: %s", id.callID),
		fmt.Sprintf("CSeq: %d REGISTER", cseq),
		fmt.Sprintf("Contact: %s;expires=0", contact),
		"User-Agent: BTicino-Bridge/1.0",
		"Content-Length: 0",
		"",
		"",
	}

	return strings.Join(lines, "\r\n")
}

func (c *BTicinoSIPClient) buildInviteRequest(target string) string {
	requestURI := fmt.Sprintf("sip:%s@%s", target, c.config.Domain)
	from := fmt.Sprintf("sip:%s@%s", c.config.Username, c.config.Domain)
	to := fmt.Sprintf("sip:%s@%s", target, c.config.Domain)

	sdp := c.buildSDP()

	transport := c.config.Transport
	if transport == "" {
		transport = "tls"
	}
	viaProtocol := "SIP/2.0/" + strings.ToUpper(transport)

	usernamePart := c.config.Username
	if idx := strings.Index(usernamePart, "@"); idx > 0 {
		usernamePart = usernamePart[:idx]
	}

	c.mutex.RLock()
	callID := c.callID
	c.mutex.RUnlock()

	c.webrtcID.mutex.Lock()
	cseq := c.webrtcID.cseq
	c.webrtcID.cseq++
	c.webrtcID.mutex.Unlock()

	lines := []string{
		fmt.Sprintf("INVITE %s SIP/2.0", requestURI),
		fmt.Sprintf("Via: %s %s:%d;branch=z9hG4bK%s;rport", viaProtocol, c.config.LocalIP, c.config.LocalPort, c.generateBranch()),
		"Max-Forwards: 70",
		fmt.Sprintf("From: %s;tag=%s", from, c.webrtcID.fromTag),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Call-ID: %s", callID),
		fmt.Sprintf("CSeq: %d INVITE", cseq),
		"Supported: replaces, outbound, gruu",
		"Allow: INVITE, ACK, CANCEL, OPTIONS, BYE, REFER, NOTIFY, MESSAGE, SUBSCRIBE, INFO, UPDATE",
		"Contact: " + fmt.Sprintf("<sip:%s@%s:%d;transport=%s>", usernamePart, c.config.LocalIP, c.config.LocalPort, transport),
		"User-Agent: BTicino-Bridge/1.0",
		"Content-Type: application/sdp",
		fmt.Sprintf("Content-Length: %d", len(sdp)),
		"",
		sdp,
	}

	return strings.Join(lines, "\r\n")
}

func (c *BTicinoSIPClient) buildSDP() string {
	// Per slyoldfox: Use RTP/SAVP with dummy crypto key and throwaway ports.
	// The real unencrypted RTP streams come via OpenWebNet *7*300 commands,
	// NOT from the SIP media path. Ports 65000/65002 are intentional black holes.
	usernamePart := c.config.Username
	if idx := strings.Index(usernamePart, "@"); idx > 0 {
		usernamePart = usernamePart[:idx]
	}
	return fmt.Sprintf("v=0\r\n"+
		"o=%s 3747 461 IN IP4 127.0.0.1\r\n"+
		"s=ScryptedSipPlugin\r\n"+
		"c=IN IP4 127.0.0.1\r\n"+
		"t=0 0\r\n"+
		"a=DEVADDR:%s\r\n"+
		"m=audio 65000 RTP/SAVP 110\r\n"+
		"a=rtpmap:110 speex/8000\r\n"+
		"a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:dummykey\r\n"+
		"m=video 65002 RTP/SAVP 96\r\n"+
		"a=rtpmap:96 H264/90000\r\n"+
		"a=fmtp:96 profile-level-id=42801F\r\n"+
		"a=crypto:1 AES_CM_128_HMAC_SHA1_80 inline:dummykey\r\n"+
		"a=recvonly\r\n",
		usernamePart,
		c.config.DevAddr)
}

func (c *BTicinoSIPClient) Hangup() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if !c.sessionActive {
		return nil
	}

	c.logger.Info("Sending BYE to end call")

	byeMsg := c.buildByeRequest()

	if c.webrtcID != nil && c.webrtcID.conn != nil {
		if _, err := c.webrtcID.conn.Write([]byte(byeMsg)); err != nil {
			c.logger.WithError(err).Error("Failed to send BYE")
			return err
		}
	}

	c.sessionActive = false
	c.callState = Registered

	c.eventBus.PublishWithSource("sip.call.ended", map[string]interface{}{}, "sip")

	return nil
}

func (c *BTicinoSIPClient) buildByeRequest() string {
	target := c.config.SIPTarget
	if target == "" {
		target = bticino.SIPUsernameForVariant(bticino.DeviceVariant)
	}
	requestURI := fmt.Sprintf("sip:%s@%s", target, c.config.Domain)
	from := fmt.Sprintf("sip:%s@%s;tag=%s", c.config.Username, c.config.Domain, c.webrtcID.fromTag)
	to := fmt.Sprintf("sip:%s@%s", target, c.config.Domain)
	if c.toTag != "" {
		to += ";tag=" + c.toTag
	}

	transport := c.config.Transport
	if transport == "" {
		transport = "tls"
	}
	viaProtocol := "SIP/2.0/" + strings.ToUpper(transport)

	c.webrtcID.mutex.Lock()
	cseq := c.webrtcID.cseq
	c.webrtcID.cseq++
	c.webrtcID.mutex.Unlock()

	lines := []string{
		fmt.Sprintf("BYE %s SIP/2.0", requestURI),
		fmt.Sprintf("Via: %s %s:%d;branch=z9hG4bK%s;rport", viaProtocol, c.config.LocalIP, c.config.LocalPort, c.generateBranch()),
		"Max-Forwards: 70",
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Call-ID: %s", c.callID),
		fmt.Sprintf("CSeq: %d BYE", cseq),
		"User-Agent: BTicino-Bridge/1.0",
		"Content-Length: 0",
		"",
		"",
	}

	return strings.Join(lines, "\r\n")
}

func (c *BTicinoSIPClient) registrationManager() {
	for {
		select {
		case <-c.stopCh:
			return
		case <-c.registerTick.C:
			c.mutex.RLock()
			state := c.callState
			c.mutex.RUnlock()

			// Only re-register when idle or registered (not during a call)
			if state == Registered || state == Idle {
				c.logger.Debug("Re-registering SIP identities")

				if c.webrtcID != nil {
					if err := c.reRegisterIdentity(c.webrtcID); err != nil {
						c.logger.WithError(err).Error("Failed to refresh webrtc registration")
					}
				}

				if c.localID != nil {
					if err := c.reRegisterIdentity(c.localID); err != nil {
						c.logger.WithError(err).Errorf("Failed to refresh %s registration", c.answerUser)
					}
				}
			}
		}
	}
}

// reRegisterIdentity sends a new REGISTER on an existing connection
func (c *BTicinoSIPClient) reRegisterIdentity(id *sipIdentity) error {
	if id.conn == nil {
		return fmt.Errorf("no connection for %s", id.username)
	}

	registerMsg := c.buildRegisterRequestForIdentity(id)
	if _, err := id.conn.Write([]byte(registerMsg)); err != nil {
		return fmt.Errorf("failed to send re-REGISTER for %s: %v", id.username, err)
	}

	response, err := c.readSIPMessageFrom(id.conn, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to read re-REGISTER response for %s: %v", id.username, err)
	}

	if strings.Contains(response.Raw, "200") {
		c.logger.Debugf("Re-registration successful for %s", id.username)
		return nil
	} else if strings.Contains(response.Raw, "401") {
		return c.handleAuthChallengeForIdentity(id, response)
	}

	return fmt.Errorf("re-registration failed for %s: %s", id.username, response.StatusCode)
}

// identityForConn returns the SIP identity owning conn, or nil.
func (c *BTicinoSIPClient) identityForConn(conn net.Conn) *sipIdentity {
	if c.webrtcID != nil && c.webrtcID.conn == conn {
		return c.webrtcID
	}
	if c.localID != nil && c.localID.conn == conn {
		return c.localID
	}
	return nil
}

// splitSIPStream parses as many COMPLETE SIP messages as possible from buf.
// Framing follows RFC 3261 §7.5 over TCP: headers end at the first empty
// line, then exactly Content-Length body bytes follow (case-insensitive
// header name, compact form "l:" accepted). Returns the messages and how
// many leading bytes were consumed; a trailing incomplete message stays
// unconsumed for the next read. Garbage before a valid start line is
// skipped (resync) so one malformed chunk cannot stall the stream.
func splitSIPStream(buf []byte) (msgs []*SIPMessage, consumed int) {
	for {
		// Skip leading blank lines.
		start := 0
		for start < len(buf) && (buf[start] == '\r' || buf[start] == '\n') {
			start++
		}
		if start >= len(buf) {
			// Rest is blank: consume it too (consumed, not len(buf) —
			// returning len(buf) here would rewind past already-returned
			// messages and replay them forever).
			return msgs, consumed + len(buf)
		}
		if start > 0 {
			buf = buf[start:]
			consumed += start
		}

		// Find the end of the header block.
		hdrEnd := indexDoubleCRLF(buf)
		if hdrEnd < 0 {
			return msgs, consumed // need more data
		}

		msg, bodyLen, ok := parseSIPHeaderBlock(buf[:hdrEnd])
		if !ok {
			// Not a SIP start line: drop this line and resync.
			if i := indexCRLF(buf); i >= 0 {
				consumed += i + 2
				buf = buf[i+2:]
				continue
			}
			return msgs, consumed
		}

		total := hdrEnd + 4 + bodyLen
		if len(buf) < total {
			return msgs, consumed // body incomplete: wait for more data
		}

		if bodyLen > 0 {
			msg.Body = string(buf[hdrEnd+4 : total])
		}
		msg.Raw = string(buf[:total])
		msgs = append(msgs, msg)
		buf = buf[total:]
		consumed += total
	}
}

// indexDoubleCRLF returns the index of the first "\r\n\r\n" (or "\n\n"
// fallback) in b, or -1.
func indexDoubleCRLF(b []byte) int {
	if i := bytes.Index(b, []byte("\r\n\r\n")); i >= 0 {
		return i
	}
	return bytes.Index(b, []byte("\n\n"))
}

// indexCRLF returns the index of the first line break in b, or -1.
func indexCRLF(b []byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			return i
		}
	}
	return -1
}

// parseSIPHeaderBlock parses the header block (without the trailing empty
// line) into a message and reports the declared body length. ok=false when
// the first line is not a SIP request/status line.
func parseSIPHeaderBlock(block []byte) (msg *SIPMessage, bodyLen int, ok bool) {
	// Normalize lone LF so Split works on both CRLF and LF streams.
	block = bytes.ReplaceAll(block, []byte("\r\n"), []byte("\n"))
	lines := bytes.Split(block, []byte("\n"))
	if len(lines) == 0 {
		return nil, 0, false
	}

	firstLine := strings.Fields(string(lines[0]))
	if len(firstLine) < 3 {
		return nil, 0, false
	}

	msg = &SIPMessage{
		Headers: make(map[string]string),
	}
	if strings.HasPrefix(firstLine[0], "SIP/") {
		msg.Version = firstLine[0]
		msg.StatusCode = firstLine[1]
		msg.Method = firstLine[1]
	} else {
		msg.Method = firstLine[0]
		msg.RequestURI = firstLine[1]
		msg.Version = firstLine[2]
	}

	for _, lb := range lines[1:] {
		line := string(lb)
		if line == "" {
			continue
		}
		// Folded continuation lines (RFC 3261 §7.3.1): appended to the
		// previous header. Tracked minimally — continuation without a
		// preceding header is ignored.
		if line[0] == ' ' || line[0] == '\t' {
			continue
		}
		colonIndex := strings.Index(line, ":")
		if colonIndex <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:colonIndex])
		value := strings.TrimSpace(line[colonIndex+1:])
		keyLower := strings.ToLower(key)
		switch keyLower {
		case "via", "v":
			msg.ViaHeaders = append(msg.ViaHeaders, value)
		case "record-route":
			msg.RecordRoute = append(msg.RecordRoute, value)
		case "content-length", "l":
			fmt.Sscanf(value, "%d", &bodyLen)
		}
		msg.Headers[key] = value
	}
	if bodyLen < 0 {
		bodyLen = 0
	}
	return msg, bodyLen, true
}

// readSIPMessagesFrom reads one or more SIP messages from a connection with
// a timeout. TCP may deliver fragments or several messages per read, so
// bytes accumulate in the owning identity's readBuf until at least one
// complete message is framed; leftovers are kept for the next call instead
// of being dropped.
func (c *BTicinoSIPClient) readSIPMessagesFrom(conn net.Conn, timeout time.Duration) ([]*SIPMessage, error) {
	deadline := time.Now().Add(timeout)
	conn.SetReadDeadline(deadline)

	var buf []byte
	if id := c.identityForConn(conn); id != nil {
		id.mutex.Lock()
		buf = append([]byte(nil), id.readBuf...)
		id.mutex.Unlock()
	}

	tmp := make([]byte, 16384)
	for {
		msgs, consumed := splitSIPStream(buf)
		if len(msgs) > 0 {
			leftover := append([]byte(nil), buf[consumed:]...)
			if id := c.identityForConn(conn); id != nil {
				id.mutex.Lock()
				id.readBuf = leftover
				id.mutex.Unlock()
			}
			c.logger.Debugf("readSIPMessages: %d message(s), %d bytes consumed, %d leftover",
				len(msgs), consumed, len(leftover))
			return msgs, nil
		}
		// No complete message yet (or only garbage, which splitSIPStream
		// already skipped): read more, honoring the original deadline.
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for complete SIP message")
		}
		conn.SetReadDeadline(deadline)
		n, err := conn.Read(tmp)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			// Spurious empty read: no progress possible, retry within deadline.
			continue
		}
		buf = append(buf, tmp[:n]...)
		// Sanity cap: a single SIP message over 256 KiB is not legitimate
		// here; drop the buffer rather than growing forever on garbage.
		if len(buf) > 256*1024 {
			buf = nil
			if id := c.identityForConn(conn); id != nil {
				id.mutex.Lock()
				id.readBuf = nil
				id.mutex.Unlock()
			}
			return nil, fmt.Errorf("SIP read buffer overflow without a complete message")
		}
	}
}

// readSIPMessageFrom reads a single SIP message (for backward compat).
// Leftovers stay buffered on the owning identity for the next call.
func (c *BTicinoSIPClient) readSIPMessageFrom(conn net.Conn, timeout time.Duration) (*SIPMessage, error) {
	messages, err := c.readSIPMessagesFrom(conn, timeout)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("no SIP messages parsed")
	}
	if len(messages) > 1 {
		c.logger.Debugf("readSIPMessageFrom: %d messages framed, returning first (rest buffered)", len(messages))
	}
	return messages[0], nil
}

// Legacy readSIPMessage for backward compatibility
func (c *BTicinoSIPClient) readSIPMessage() (*SIPMessage, error) {
	if c.conn != nil {
		return c.readSIPMessageFrom(c.conn, 10*time.Second)
	}
	if c.webrtcID != nil && c.webrtcID.conn != nil {
		return c.readSIPMessageFrom(c.webrtcID.conn, 10*time.Second)
	}
	return nil, fmt.Errorf("no connection available")
}

func (c *BTicinoSIPClient) GetCallState() CallState {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.callState
}

func (c *BTicinoSIPClient) IsRegistered() bool {
	return c.GetCallState() == Registered
}

// GetCallStateString returns the current call state as a human-readable string
// (implements the webserver.CallController interface).
func (c *BTicinoSIPClient) GetCallStateString() string {
	return c.GetCallState().String()
}

func (c *BTicinoSIPClient) GetConfig() SIPConfig {
	return *c.config
}

func (c *BTicinoSIPClient) IsSessionActive() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.sessionActive
}

// Helper methods

func (c *BTicinoSIPClient) generateTag() string {
	return fmt.Sprintf("tag%d", time.Now().UnixNano())
}

func (c *BTicinoSIPClient) generateCallID() string {
	return fmt.Sprintf("call%d@%s", time.Now().UnixNano()%10000000, c.config.LocalIP)
}

func (c *BTicinoSIPClient) generateBranch() string {
	return fmt.Sprintf("branch%d", time.Now().UnixNano()%1000000)
}

func (c *BTicinoSIPClient) generateCNonce() string {
	return fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%d", time.Now().UnixNano()))))[:16]
}

func (c *BTicinoSIPClient) extractAuthParam(authHeader, param string) string {
	cleanHeader := strings.ReplaceAll(authHeader, "\"", "")
	parts := strings.Split(cleanHeader, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, param+"=") {
			value := strings.TrimPrefix(part, param+"=")
			return strings.Trim(value, " ")
		}
	}
	return ""
}

// Legacy methods kept for backward compatibility

func (c *BTicinoSIPClient) register() error {
	if c.webrtcID != nil {
		return c.registerIdentity(c.webrtcID)
	}
	return fmt.Errorf("no webrtc identity configured")
}

func (c *BTicinoSIPClient) unregister() error {
	if c.webrtcID != nil {
		c.unregisterIdentity(c.webrtcID)
	}
	return nil
}

func (c *BTicinoSIPClient) buildRegisterRequest() string {
	if c.webrtcID != nil {
		return c.buildRegisterRequestForIdentity(c.webrtcID)
	}
	return ""
}

func (c *BTicinoSIPClient) buildUnregisterRequest() string {
	if c.webrtcID != nil {
		return c.buildUnregisterRequestForIdentity(c.webrtcID)
	}
	return ""
}

func (c *BTicinoSIPClient) handleAuthChallenge(response *SIPMessage) error {
	if c.webrtcID != nil {
		return c.handleAuthChallengeForIdentity(c.webrtcID, response)
	}
	return fmt.Errorf("no webrtc identity")
}

func (c *BTicinoSIPClient) buildAuthenticatedRegisterRequest(realm, nonce, qop string) string {
	if c.webrtcID != nil {
		return c.buildAuthenticatedRegisterForIdentity(c.webrtcID, realm, nonce, qop)
	}
	return ""
}
