package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"bticino_bridge/pkg/bticino"
	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/messageparser"
	"bticino_bridge/pkg/openwebnet"
	"bticino_bridge/pkg/sip"
	"bticino_bridge/pkg/version"
	"github.com/sirupsen/logrus"
)

// ==================== LOG BUFFER (RING BUFFER FOR WEB VIEWER) ====================

// LogEntry represents a single log entry for the web viewer
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Fields    string `json:"fields,omitempty"`
}

// LogBuffer is a thread-safe ring buffer that stores recent log entries
type LogBuffer struct {
	entries []LogEntry
	mu      sync.RWMutex
	maxSize int
	pos     int
	full    bool
}

// NewLogBuffer creates a new log ring buffer with specified capacity
func NewLogBuffer(maxSize int) *LogBuffer {
	return &LogBuffer{
		entries: make([]LogEntry, maxSize),
		maxSize: maxSize,
	}
}

// Add appends a log entry to the ring buffer
func (lb *LogBuffer) Add(entry LogEntry) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.entries[lb.pos] = entry
	lb.pos = (lb.pos + 1) % lb.maxSize
	if lb.pos == 0 {
		lb.full = true
	}
}

// GetAll returns all log entries in chronological order
func (lb *LogBuffer) GetAll() []LogEntry {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	if !lb.full {
		result := make([]LogEntry, lb.pos)
		copy(result, lb.entries[:lb.pos])
		return result
	}
	result := make([]LogEntry, lb.maxSize)
	copy(result, lb.entries[lb.pos:])
	copy(result[lb.maxSize-lb.pos:], lb.entries[:lb.pos])
	return result
}

// GetLast returns the last n log entries in chronological order
func (lb *LogBuffer) GetLast(n int) []LogEntry {
	all := lb.GetAll()
	if n >= len(all) {
		return all
	}
	return all[len(all)-n:]
}

// LogBufferHook is a logrus hook that captures log entries into a LogBuffer
type LogBufferHook struct {
	buffer *LogBuffer
}

// NewLogBufferHook creates a logrus hook backed by the given LogBuffer
func NewLogBufferHook(buffer *LogBuffer) *LogBufferHook {
	return &LogBufferHook{buffer: buffer}
}

// Levels returns all log levels this hook should fire for
func (h *LogBufferHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire is called by logrus for each log entry
func (h *LogBufferHook) Fire(entry *logrus.Entry) error {
	fields := ""
	if len(entry.Data) > 0 {
		parts := make([]string, 0, len(entry.Data))
		for k, v := range entry.Data {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
		fields = strings.Join(parts, " ")
	}
	h.buffer.Add(LogEntry{
		Timestamp: entry.Time.Format("2006-01-02 15:04:05"),
		Level:     entry.Level.String(),
		Message:   entry.Message,
		Fields:    fields,
	})
	return nil
}

// BTicinoBridge interface defines the methods we need from the MQTT bridge
type BTicinoBridge interface {
	GetMulticastStats() map[string]interface{}
	GetEventBusStats() map[string]interface{}
	GetVideoStats() map[string]interface{}
	SendOpenWebNetCommand(command string) error // NEW: For sending OpenWebNet commands
}

// MQTTPublisher defines a function type for MQTT message publishing
type MQTTPublisher func(topic, payload string, retain bool) error

// MQTTStatusProvider interface for getting MQTT bridge status without import cycle
type MQTTStatusProvider interface {
	GetMQTTStatus() map[string]interface{}
	IsConnected() bool
}

// LEDStatusFunc returns LED states as map of name -> on/off
type LEDStatusFunc func() map[string]bool

// SnapshotFunc captures a JPEG frame from the camera.
// timeout covers the whole capture (including stream activation);
// maxAge > 0 allows returning a cached snapshot newer than maxAge.
type SnapshotFunc func(timeout, maxAge time.Duration) ([]byte, error)

// CallController exposes SIP call control to the web API.
type CallController interface {
	GetCallStateString() string // Idle/Registered/IncomingCall/Connected/...
	IsRegistered() bool
	Hangup() error // send BYE to end the active call
}

// Answerer exposes the SIP answerer controls to the web API. Optional (nil
// when the SIP answerer is not running). *sip.Answerer satisfies it
// structurally, without importing pkg/sip here, so tests can inject a fake.
type Answerer interface {
	Answer() error
	Hangup() error
	StateString() string
	IsRegistered() bool
}

// AudioSayer is the minimal interface the talk-back endpoint
// (POST /api/talkback/say) needs; *sip.AudioCall implements it. It is defined
// here instead of using the concrete type so webserver tests can inject a
// stub without importing pkg/sip.
type AudioSayer interface {
	Say(wavPath string) error
}

// VideoProbeFunc runs a single cooperative video probe (one *7*300, no retry,
// no self-INVITE). Returns a diagnostic report and, if any, the best JPEG frame.
type VideoProbeFunc func() (report map[string]interface{}, jpeg []byte, err error)

// AudioProbeFunc runs a single cooperative audio probe (one *7*300 type=2) and
// measures the incoming Speex RTP flow. Returns a diagnostic report.
type AudioProbeFunc func() (report map[string]interface{}, err error)

// WebServer provides HTTP interface for BTicino bridge management
type WebServer struct {
	config                   *config.Config
	bridge                   BTicinoBridge
	logger                   *logrus.Logger
	server                   *http.Server
	staticDir                string
	messageParser            *messageparser.MessageParser
	mqttPublisher            MQTTPublisher                // Optional MQTT publishing function
	mqttStatusProvider       MQTTStatusProvider           // Optional MQTT status provider
	ledStatusFunc            LEDStatusFunc                // Optional LED status reader
	snapshotFunc             SnapshotFunc                 // Optional camera JPEG snapshot provider
	callController           CallController               // Optional SIP call control
	videoProbeFunc           VideoProbeFunc               // Optional one-shot cooperative video probe
	audioProbeFunc           AudioProbeFunc               // Optional one-shot cooperative audio probe
	nativeDevAddrSniffer     *sip.NativeDevAddrDiscoverer // Optional passive native-dev-addr discoverer
	nativeDevAddrVerify      NativeDevAddrVerifyFunc      // Optional candidate verification dial
	nativeDevAddrSessionLive func() bool                  // Reports a running camera session, if known
	logBuffer                *LogBuffer                   // Ring buffer for web log viewer
	startTime                time.Time                    // Track when server started for real uptime
	configManager            *ConfigManager               // Configuration manager for web-based config
	configPath               string                       // Path to configuration file
	sseClients               map[chan []byte]bool         // SSE clients for real-time updates
	sseMu                    sync.Mutex
	shuttingDown             chan struct{}         // closed on Shutdown so SSE streams end
	talkback                 *sip.TalkbackInjector // Talk-back (upstream audio) injector — experimental
	audioCall                AudioSayer            // Talk-back v2: own local audio call
	restartBridgeFunc        func()                // Bridge restart action (injectable in tests)
	rebootDeviceFunc         func()                // Device reboot action (injectable in tests)
	duplexController         DuplexController      // full-duplex audio for local validation; IsActive() is authoritative for /status
	audioListenController    AudioListenController // passive listening (tap lo:5000); IsActive() is authoritative for /status
	answerer                 Answerer              // SIP answerer; optional
}

// NewWebServer creates a new web server instance
func NewWebServer(cfg *config.Config, bridge BTicinoBridge, logger *logrus.Logger, configPath ...string) *WebServer {
	logBuf := NewLogBuffer(5000)
	logger.AddHook(NewLogBufferHook(logBuf))

	// Determine config path: use provided path or default
	cfgPath := "configs/config.yaml"
	if len(configPath) > 0 && configPath[0] != "" {
		cfgPath = configPath[0]
	}

	// Initialize config manager
	configManager := NewConfigManager(cfgPath, logger)
	if _, err := configManager.LoadConfig(); err != nil {
		logger.WithError(err).Warnf("Failed to load config from %s, using provided config", cfgPath)
	}

	return &WebServer{
		config:        cfg,
		bridge:        bridge,
		logger:        logger,
		staticDir:     cfg.Web.StaticDir,
		messageParser: messageparser.NewMessageParser(),
		logBuffer:     logBuf,
		startTime:     time.Now(),
		configManager: configManager,
		configPath:    cfgPath,
		sseClients:    make(map[chan []byte]bool),
		talkback:      sip.NewTalkbackInjector(logger),
	}
}

// SetMQTTStatusProvider sets the MQTT status provider
func (ws *WebServer) SetMQTTStatusProvider(provider MQTTStatusProvider) {
	ws.mqttStatusProvider = provider
}

// SetLEDStatusFunc sets the function to read LED states
func (ws *WebServer) SetLEDStatusFunc(fn LEDStatusFunc) {
	ws.ledStatusFunc = fn
}

// SetSnapshotFunc sets the camera JPEG snapshot provider
func (ws *WebServer) SetSnapshotFunc(fn SnapshotFunc) {
	ws.snapshotFunc = fn
}

// SetCallController sets the SIP call controller
func (ws *WebServer) SetCallController(cc CallController) {
	ws.callController = cc
}

// SetAnswerer sets the SIP answerer (F1) controller.
func (ws *WebServer) SetAnswerer(a Answerer) {
	ws.answerer = a
}

// SetAudioCall sets the talk-back v2 audio call (POST /api/talkback/say).
func (ws *WebServer) SetAudioCall(ac AudioSayer) {
	ws.audioCall = ac
}

// SetVideoProbeFunc sets the one-shot cooperative video probe
func (ws *WebServer) SetVideoProbeFunc(fn VideoProbeFunc) {
	ws.videoProbeFunc = fn
}

// SetAudioProbeFunc sets the one-shot cooperative audio probe
func (ws *WebServer) SetAudioProbeFunc(fn AudioProbeFunc) {
	ws.audioProbeFunc = fn
}

// SetWebServer sets reference to self for callbacks (used for SSE broadcasting)
var globalWebServer *WebServer

func (ws *WebServer) SetGlobalWebServer() {
	globalWebServer = ws
}

// GetGlobalWebServer returns the global WebServer instance
func GetGlobalWebServer() *WebServer {
	return globalWebServer
}

// Start starts the web server
func (ws *WebServer) Start(ctx context.Context) error {
	if !ws.config.Web.Enabled {
		ws.logger.Info("Web server disabled in configuration")
		return nil
	}

	// Setup routes
	mux := http.NewServeMux()

	// API Routes
	mux.HandleFunc("/api/status", ws.handleAPIStatus)
	mux.HandleFunc("/api/snapshot", ws.handleAPISnapshot)
	mux.HandleFunc("/api/call", ws.handleAPICallState)
	mux.HandleFunc("/api/controls/call/hangup", ws.handleCallHangup)
	// SIP answerer. 503 when the subsystem is not running (answerer == nil);
	// Basic Auth comes from the global authMiddleware(mux).
	mux.HandleFunc("/api/doorcall/answer", ws.handleDoorcallAnswer)
	mux.HandleFunc("/api/doorcall/hangup", ws.handleDoorcallHangup)
	mux.HandleFunc("/api/doorcall/status", ws.handleDoorcallStatus)
	mux.HandleFunc("/api/video/probe", ws.handleVideoProbe)
	mux.HandleFunc("/api/audio/probe", ws.handleAudioProbe)
	mux.HandleFunc("/api/sip/native-dev-addr", ws.handleAPINativeDevAddr)
	mux.HandleFunc("/api/sip/domain/discover", ws.handleAPISIPDomainDiscover)
	mux.HandleFunc("/api/sip/native-dev-addr/discover", ws.handleAPINativeDevAddrDiscover)
	mux.HandleFunc("/api/sip/native-dev-addr/discover/status", ws.handleAPINativeDevAddrDiscoverStatus)
	mux.HandleFunc("/api/sip/native-dev-addr/discover/cancel", ws.handleAPINativeDevAddrDiscoverCancel)
	mux.HandleFunc("/api/sip/native-dev-addr/verify", ws.handleAPINativeDevAddrVerify)
	mux.HandleFunc("/api/messages", ws.handleAPIMessages)
	mux.HandleFunc("/api/system", ws.handleAPISystem)
	mux.HandleFunc("/api/system/restart-bridge", ws.handleAPISystemRestartBridge)
	mux.HandleFunc("/api/system/reboot", ws.handleAPISystemReboot)
	mux.HandleFunc("/api/logs", ws.handleAPILogs)
	mux.HandleFunc("/api/logs/download", ws.handleAPILogsDownload)
	mux.HandleFunc("/api/controls/door/unlock", ws.handleDoorUnlock)
	mux.HandleFunc("/api/controls/answering-machine/toggle", ws.handleAnsweringMachineToggle)
	mux.HandleFunc("/api/controls/display/on", ws.handleDisplayOn)
	mux.HandleFunc("/api/controls/display/off", ws.handleDisplayOff)
	mux.HandleFunc("/api/controls/mute/on", ws.handleMuteOn)
	mux.HandleFunc("/api/controls/mute/off", ws.handleMuteOff)
	mux.HandleFunc("/api/controls/doorbell/on", ws.handleDoorbellSoundOn)
	mux.HandleFunc("/api/controls/doorbell/off", ws.handleDoorbellSoundOff)
	mux.HandleFunc("/api/controls/light/on", ws.handleLightOn)
	mux.HandleFunc("/api/controls/command", ws.handleArbitraryCommand)
	mux.HandleFunc("/api/talkback/start", ws.handleTalkbackStart)
	mux.HandleFunc("/api/talkback/stop", ws.handleTalkbackStop)
	mux.HandleFunc("/api/talkback/status", ws.handleTalkbackStatus)
	mux.HandleFunc("/api/talkback/say", ws.handleTalkbackSay)
	mux.HandleFunc("/api/talkback/greeting", ws.handleGreeting) // talk-back v1: answering machine greeting → door

	// Full-duplex audio for local validation.
	// Gated by audio.duplex_enabled (404 when off, see audio_duplex_handlers.go).
	mux.HandleFunc("/api/audio/duplex/start", ws.handleAudioDuplexStart)
	mux.HandleFunc("/api/audio/duplex/stop", ws.handleAudioDuplexStop)
	mux.HandleFunc("/api/audio/duplex/status", ws.handleAudioDuplexStatus)

	// Passive door listening: mirror of the native audio leg (tap lo:5000).
	// Gated by audio.listen_tap_enabled (404 when off, see
	// audio_listen_handlers.go). This is the local validation path; in use it
	// is armed by bus events, wired in cmd/main.go.
	mux.HandleFunc("/api/audio/listen/start", ws.handleAudioListenStart)
	mux.HandleFunc("/api/audio/listen/stop", ws.handleAudioListenStop)
	mux.HandleFunc("/api/audio/listen/status", ws.handleAudioListenStatus)

	// Enhanced Message Management API Routes
	mux.HandleFunc("/api/messages/list", ws.handleAPIMessagesList)
	mux.HandleFunc("/api/messages/", ws.handleAPIMessageDetail)
	mux.HandleFunc("/api/messages/download/", ws.handleAPIMessageDownload)
	mux.HandleFunc("/api/messages/mark-all-read", ws.handleAPIMessageMarkAllRead)
	mux.HandleFunc("/api/messages/mark-read/", ws.handleAPIMessageMarkRead)
	mux.HandleFunc("/api/messages/delete/", ws.handleAPIMessageDelete)

	// Memos API (voice and text notes)
	mux.HandleFunc("/api/memos", ws.handleAPIMemos)
	mux.HandleFunc("/api/memos/", ws.handleAPIMemoDetail)
	mux.HandleFunc("/api/memos/audio/", ws.handleAPIMemoAudio)
	mux.HandleFunc("/api/memos/mark-read/", ws.handleAPIMemoMarkRead)
	mux.HandleFunc("/api/memos/delete/", ws.handleAPIMemoDelete)

	// Streaming API Routes (RTSP/WebRTC)
	mux.HandleFunc("/api/streaming", ws.handleAPIStreaming)
	mux.HandleFunc("/api/streaming/start", ws.handleAPIStreamingStart)
	mux.HandleFunc("/api/streaming/stop", ws.handleAPIStreamingStop)
	mux.HandleFunc("/api/streaming/sessions", ws.handleAPIStreamingSessions)
	mux.HandleFunc("/api/streaming/record", ws.handleAPIStreamingRecord)
	mux.HandleFunc("/api/streaming/config", ws.handleAPIStreamingConfig)
	mux.HandleFunc("/api/webrtc/start", ws.handleAPIWebRTCStart)
	mux.HandleFunc("/api/webrtc/stop", ws.handleAPIWebRTCStop)
	mux.HandleFunc("/api/webrtc/status", ws.handleAPIWebRTCStatus)

	// Serve Svelte static files. On the device the deploy copies the contents of
	// dist/ directly into web/; in development web/dist exists and THAT directory
	// is served (never the whole web/, which would expose src/ and node_modules/).
	webRoot := "web"
	if st, err := os.Stat("web/dist"); err == nil && st.IsDir() {
		webRoot = "web/dist"
	}
	fs := http.FileServer(http.Dir(webRoot))
	mux.Handle("/", fs)

	// Configuration Management API Routes (NEW in v0.13.0)
	mux.HandleFunc("/api/config", ws.handleAPIConfig)
	mux.HandleFunc("/api/config/save", ws.handleAPIConfigSave)
	mux.HandleFunc("/api/config/validate", ws.handleAPIConfigValidate)
	mux.HandleFunc("/api/config/backup", ws.handleAPIConfigBackup)
	mux.HandleFunc("/api/config/backups", ws.handleAPIConfigBackup)
	mux.HandleFunc("/api/config/restore", ws.handleAPIConfigRestore)
	mux.HandleFunc("/api/config/history", ws.handleAPIConfigHistory)
	mux.HandleFunc("/api/config/reload", ws.handleAPIConfigReload)

	// Device Configuration API Routes (NEW in v0.14.2)
	mux.HandleFunc("/api/device/ntp", ws.handleAPIDeviceNTP)
	mux.HandleFunc("/api/device/timezone", ws.handleAPIDeviceTimezone)
	mux.HandleFunc("/api/device/language", ws.handleAPIDeviceLanguage)
	mux.HandleFunc("/api/device/ringtone", ws.handleAPIDeviceRingtone)
	mux.HandleFunc("/api/device/ringtones", ws.handleAPIDeviceRingtones)
	mux.HandleFunc("/api/device/languages", ws.handleAPIDeviceLanguages)
	mux.HandleFunc("/api/device/save", ws.handleAPIDeviceSave)

	// Device config (Phase 1: QML → Bridge synchronization)
	mux.HandleFunc("/api/config/device", ws.handleAPIDeviceConfig)
	mux.HandleFunc("/api/config/language", ws.handleAPILanguage)

	// (Legacy UI removed: its snake_case keys did not match the backend
	// and a partial save could mangle the config — audit 2026-08-19)

	// Swagger UI
	mux.Handle("/api/docs/", http.StripPrefix("/api/docs/", http.FileServer(http.Dir("docs"))))

	// SSE for real-time updates (LEDs, GPIOs, events)
	mux.HandleFunc("/api/events", ws.handleSSEvents)

	ws.server = &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", ws.config.Web.Port),
		Handler: http.NewCrossOriginProtection().Handler(ws.authMiddleware(mux)),
	}
	// Shutdown waits for every active request, and an SSE stream never ends by itself.
	ws.shuttingDown = make(chan struct{})
	ws.server.RegisterOnShutdown(func() { close(ws.shuttingDown) })

	go func() {
		if err := ws.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			ws.logger.WithError(err).Error("Web server error")
		}
	}()

	return nil
}

// Stop stops the web server
func (ws *WebServer) Stop() error {
	if ws.server != nil {
		return ws.server.Shutdown(context.Background())
	}
	return nil
}

// @Summary Get application logs
// @Description Returns recent application logs with optional filtering by level and count
// @Tags Logs
// @Accept json
// @Produce json
// @Param count query int false "Number of logs to return (max 500)"
// @Param level query string false "Filter by log level (debug, info, warn, error)"
// @Success 200 {object} map[string]interface{}
// @Router /api/logs [get]
func (ws *WebServer) handleAPILogs(w http.ResponseWriter, r *http.Request) {
	countStr := r.URL.Query().Get("count")
	count := 200
	if countStr != "" {
		if n, err := strconv.Atoi(countStr); err == nil && n > 0 && n <= 500 {
			count = n
		}
	}
	levelFilter := r.URL.Query().Get("level")
	entries := ws.logBuffer.GetLast(count)
	if levelFilter != "" && levelFilter != "all" {
		filtered := make([]LogEntry, 0, len(entries))
		for _, e := range entries {
			if e.Level == levelFilter {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}
	ws.writeJSON(w, map[string]interface{}{
		"logs":       entries,
		"total":      len(entries),
		"max_buffer": 500,
	})
}

// @Summary Download application logs
// @Description Downloads application logs as a text file with optional count parameter
// @Tags Logs
// @Accept json
// @Produce text/plain
// @Param count query int false "Number of logs to download (max 2000)"
// @Success 200 {file} text/plain
// @Router /api/logs/download [get]
func (ws *WebServer) handleAPILogsDownload(w http.ResponseWriter, r *http.Request) {
	countStr := r.URL.Query().Get("count")
	count := 500
	if countStr != "" {
		if n, err := strconv.Atoi(countStr); err == nil && n > 0 && n <= 2000 {
			count = n
		}
	}

	entries := ws.logBuffer.GetAll()
	if len(entries) > count {
		entries = entries[len(entries)-count:]
	}

	// Generate text format
	filename := fmt.Sprintf("bticino_bridge_logs_%s.txt", time.Now().Format("2006-01-02_15-04-05"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	for _, e := range entries {
		fmt.Fprintf(w, "[%s] %s: %s %s\n", e.Timestamp, e.Level, e.Message, e.Fields)
	}
}

// @Summary Get system status
// @Description Returns the current system status including version, uptime, MQTT connection, and LED states
// @Tags Status
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/status [get]
func (ws *WebServer) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	components := map[string]interface{}{
		"openwebnet": map[string]interface{}{
			"status": "active",
			"port":   bticino.OpenWebNetLocalPort,
		},
		"web_dashboard": map[string]interface{}{
			"status": "active",
			"port":   ws.config.Web.Port,
		},
	}
	hasAnsweringMachine := bticino.AnsweringMachineAvailable()
	storageUsed := "0%"
	if hasAnsweringMachine {
		messageCount, newMessages := 0, 0
		if amStatus, err := ws.messageParser.GetAnsweringMachineStatus(); err == nil {
			messageCount = amStatus.TotalMessages
			newMessages = amStatus.NewMessages
			storageUsed = amStatus.StorageUsed
		}
		components["message_parser"] = map[string]interface{}{
			"status":         "active",
			"messages_found": messageCount,
			"new_messages":   newMessages,
		}
	}

	status := map[string]interface{}{
		"version":   version.GetVersion(),
		"timestamp": time.Now(),
		"uptime":    ws.getUptime(),
		"boot_time": ws.startTime.Format(time.RFC3339), // allows verifying restarts even if the restart times out
		"device": map[string]interface{}{
			"variant":  bticino.DeviceVariant,
			"model":    deviceModel(),
			"firmware": firmwareStamp(),
			// The web UI shows messages, notes, their storage and the voicemail
			// control only when true.
			"answering_machine": hasAnsweringMachine,
			// Display and doorbell-sound controls only when true.
			"remote_set_commands": bticino.RemoteSetCommandsWork(),
		},
		"components": components,
		"system":     systemMetrics(),
	}
	if hasAnsweringMachine {
		status["storage_used"] = storageUsed
	}

	// Add MQTT status if provider available
	if ws.mqttStatusProvider != nil {
		status["mqtt"] = map[string]interface{}{
			"connected":     ws.mqttStatusProvider.IsConnected(),
			"topics_prefix": ws.config.MQTT.TopicPrefix,
			"broker":        ws.config.MQTT.Host,
		}
	}

	// Add LED status if reader available
	if ws.ledStatusFunc != nil {
		ledStates := ws.ledStatusFunc()
		status["leds"] = ledStates
	}

	ws.writeJSON(w, status)
}

// @Summary Get camera snapshot
// @Description Captures a JPEG frame from the door camera, starting the video stream if needed. Query params: timeout (seconds, default 20) and max_age (seconds, default 2; 0 disables the snapshot cache).
// @Tags Streaming
// @Produce jpeg
// @Success 200 {string} binary "JPEG image"
// @Failure 503 {object} map[string]interface{}
// @Router /api/snapshot [get]
func (ws *WebServer) handleAPISnapshot(w http.ResponseWriter, r *http.Request) {
	if ws.snapshotFunc == nil {
		http.Error(w, "snapshot unavailable: video subsystem not active", http.StatusServiceUnavailable)
		return
	}

	timeout := 20 * time.Second
	if v := r.URL.Query().Get("timeout"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 && secs <= 120 {
			timeout = time.Duration(secs) * time.Second
		}
	}
	maxAge := 2 * time.Second
	if v := r.URL.Query().Get("max_age"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			maxAge = time.Duration(secs) * time.Second
		}
	}

	jpeg, err := ws.snapshotFunc(timeout, maxAge)
	if err != nil {
		ws.logger.WithError(err).Warn("Snapshot capture failed")
		http.Error(w, fmt.Sprintf("snapshot capture failed: %v", err), http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(jpeg)))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(jpeg)
}

// @Summary Cooperative video probe (diagnostic)
// @Description Sends a SINGLE *7*300 (no retry, no self-INVITE) asking bt_av_media to duplicate its video RTP, then tries to decode a JPEG. Use while native video is active (eye/auto-on button). Requires POST + ?confirm=yes. On success returns image/jpeg with X-Probe-* headers; otherwise a JSON report.
// @Tags Streaming
// @Produce jpeg
// @Produce json
// @Router /api/video/probe [post]
func (ws *WebServer) handleVideoProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed (use POST)", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Get("confirm") != "yes" {
		http.Error(w, "confirm required: POST /api/video/probe?confirm=yes", http.StatusBadRequest)
		return
	}
	if ws.videoProbeFunc == nil {
		http.Error(w, "video probe unavailable (no OpenWebNet client)", http.StatusServiceUnavailable)
		return
	}

	report, jpeg, err := ws.videoProbeFunc()
	if err != nil {
		ws.logger.WithError(err).Warn("Video probe failed")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		ws.writeJSON(w, map[string]interface{}{"error": err.Error(), "report": report})
		return
	}

	// Diagnostic headers always present
	if report != nil {
		if ack, ok := report["ack"].(bool); ok {
			w.Header().Set("X-Probe-Ack", strconv.FormatBool(ack))
		}
		if frames, ok := report["frames"].(int); ok {
			w.Header().Set("X-Probe-Frames", strconv.Itoa(frames))
		}
		if note, ok := report["note"].(string); ok {
			w.Header().Set("X-Probe-Note", note)
		}
	}

	if len(jpeg) > 0 {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(jpeg)))
		w.Header().Set("Cache-Control", "no-store")
		w.Write(jpeg)
		return
	}

	// No image: return the report as JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	ws.writeJSON(w, map[string]interface{}{"report": report})
}

// @Summary Cooperative audio probe (diagnostic)
// @Description Sends a SINGLE *7*300 type=2 asking bt_av_media to duplicate its Speex audio RTP, then measures the incoming flow. Use with the native session active (eye + phone buttons). Requires POST + ?confirm=yes.
// @Tags Streaming
// @Produce json
// @Router /api/audio/probe [post]
func (ws *WebServer) handleAudioProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed (use POST)", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Get("confirm") != "yes" {
		http.Error(w, "confirm required: POST /api/audio/probe?confirm=yes", http.StatusBadRequest)
		return
	}
	if ws.audioProbeFunc == nil {
		http.Error(w, "audio probe unavailable (no OpenWebNet client)", http.StatusServiceUnavailable)
		return
	}

	report, err := ws.audioProbeFunc()
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		ws.logger.WithError(err).Warn("Audio probe failed")
		w.WriteHeader(http.StatusServiceUnavailable)
		ws.writeJSON(w, map[string]interface{}{"error": err.Error(), "report": report})
		return
	}

	// 200 if RTP arrived, 503 if not
	packets := 0
	if report != nil {
		if p, ok := report["packets"].(int); ok {
			packets = p
		}
	}
	if packets == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	ws.writeJSON(w, map[string]interface{}{"report": report})
}

// @Summary Get SIP call state
// @Description Returns the current SIP registration and call state (Idle/Registered/IncomingCall/Connected/...).
// @Tags Streaming
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/call [get]
func (ws *WebServer) handleAPICallState(w http.ResponseWriter, r *http.Request) {
	if ws.callController == nil {
		ws.writeJSON(w, map[string]interface{}{"available": false, "state": "unavailable"})
		return
	}
	ws.writeJSON(w, map[string]interface{}{
		"available":  true,
		"state":      ws.callController.GetCallStateString(),
		"registered": ws.callController.IsRegistered(),
	})
}

// @Summary Hang up the active SIP call
// @Description Sends a SIP BYE to end the current call.
// @Tags Streaming
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /api/controls/call/hangup [post]
func (ws *WebServer) handleCallHangup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ws.callController == nil {
		http.Error(w, "call control unavailable: SIP subsystem not active", http.StatusServiceUnavailable)
		return
	}
	if err := ws.callController.Hangup(); err != nil {
		ws.logger.WithError(err).Warn("Call hangup failed")
		http.Error(w, fmt.Sprintf("hangup failed: %v", err), http.StatusServiceUnavailable)
		return
	}
	ws.writeJSON(w, map[string]interface{}{"success": true, "action": "hangup"})
}

// handleDoorcallAnswer picks up the incoming call through the SIP answerer.
// 503 when the subsystem is not running; 409 when the answerer refuses the
// action (e.g. ErrNotRinging: no call is RINGING).
func (ws *WebServer) handleDoorcallAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ws.answerer == nil {
		http.Error(w, "answerer not running", http.StatusServiceUnavailable)
		return
	}
	if err := ws.answerer.Answer(); err != nil {
		http.Error(w, fmt.Sprintf("answer: %v", err), http.StatusConflict)
		return
	}
	ws.writeJSON(w, map[string]interface{}{"success": true, "action": "answer", "state": ws.answerer.StateString()})
}

// handleDoorcallHangup hangs up the answerer's current call. 503 when the
// subsystem is not running; 409 when the answerer refuses the action (e.g.
// ErrNotInCall: no active call).
func (ws *WebServer) handleDoorcallHangup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ws.answerer == nil {
		http.Error(w, "answerer not running", http.StatusServiceUnavailable)
		return
	}
	if err := ws.answerer.Hangup(); err != nil {
		http.Error(w, fmt.Sprintf("hangup: %v", err), http.StatusConflict)
		return
	}
	ws.writeJSON(w, map[string]interface{}{"success": true, "action": "hangup", "state": ws.answerer.StateString()})
}

// handleDoorcallStatus reports the current SIP answerer state.
// 503 when the subsystem is not running.
func (ws *WebServer) handleDoorcallStatus(w http.ResponseWriter, r *http.Request) {
	if ws.answerer == nil {
		http.Error(w, "answerer not running", http.StatusServiceUnavailable)
		return
	}
	ws.writeJSON(w, map[string]interface{}{"state": ws.answerer.StateString(), "registered": ws.answerer.IsRegistered()})
}

// @Summary Get system information
// @Description Returns detailed system information including device info, filesystem paths, and network configuration
// @Tags Status
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/system [get]
func (ws *WebServer) handleAPISystem(w http.ResponseWriter, r *http.Request) {
	system := map[string]interface{}{
		"device_info": map[string]interface{}{
			"model":        bticino.DeviceModelName(),
			"ip":           bticino.LocalOutboundIP(),
			"architecture": "ARM v7l",
		},
		"filesystem": map[string]interface{}{
			"messages_dir":    "/home/bticino/cfg/extra/47/messages/",
			"config_files":    []string{"/var/tmp/conf.xml", "/home/bticino/sp/dbfiles_ws.xml"},
			"led_monitoring":  "/sys/class/leds",
			"gpio_monitoring": "/sys/class/gpio",
		},
		"network": map[string]interface{}{
			"openwebnet_ports": []int{20000, 30006, 30007},
			"web_port":         ws.config.Web.Port,
			"homekit_port":     8081,
		},
		"enhanced_features": []string{
			"Real BTicino filesystem integration",
			"Enhanced OpenWebNet with 50+ proven commands",
			"Home Assistant MQTT discovery",
			"Physical device monitoring (GPIO/LED)",
			"Message parsing with image/video support",
			"Cross-compiled ARM support",
		},
	}
	ws.writeJSON(w, system)
}

// @Summary Server-Sent Events for real-time updates
// @Description Provides real-time updates for LEDs, GPIOs, and system events via SSE
// @Tags Status
// @Accept json
// @Produce text/event-stream
// @Success 200 {object} stream
// @Router /api/events [get]
func (ws *WebServer) handleSSEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	clientChan := make(chan []byte, 100)
	ws.sseMu.Lock()
	ws.sseClients[clientChan] = true
	ws.sseMu.Unlock()

	defer func() {
		ws.sseMu.Lock()
		delete(ws.sseClients, clientChan)
		ws.sseMu.Unlock()
		close(clientChan)
	}()

	notify := r.Context().Done()

	for {
		select {
		case <-notify:
			return
		case <-ws.shuttingDown:
			return
		case data := <-clientChan:
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// BroadcastEvent sends an event to all connected SSE clients
func (ws *WebServer) BroadcastEvent(eventType string, data map[string]interface{}) {
	data["type"] = eventType
	data["timestamp"] = time.Now().Unix()

	jsonData, err := json.Marshal(data)
	if err != nil {
		ws.logger.WithError(err).Error("Failed to marshal SSE event")
		return
	}

	ws.sseMu.Lock()
	defer ws.sseMu.Unlock()

	for client := range ws.sseClients {
		select {
		case client <- jsonData:
		default:
		}
	}
}

// @Summary Get messages summary
// @Description Returns a summary of answering machine messages including total count, new messages, and preview of recent messages
// @Tags Messages
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/messages [get]
func (ws *WebServer) handleAPIMessages(w http.ResponseWriter, r *http.Request) {
	// Get real answering machine status
	status, err := ws.messageParser.GetAnsweringMachineStatus()
	if err != nil {
		ws.logger.WithError(err).Error("Failed to get answering machine status")
		status = &messageparser.AnsweringMachineStatus{
			Enabled:       false,
			TotalMessages: 0,
			NewMessages:   0,
			StorageUsed:   "0%",
			LastChecked:   time.Now(),
		}
	}

	// Get recent messages for preview (limit to 10 most recent)
	allMessages, err := ws.messageParser.GetAllMessages()
	if err != nil {
		ws.logger.WithError(err).Error("Failed to get messages")
		allMessages = []*messageparser.Message{}
	}

	// Limit to 10 most recent messages for the basic endpoint
	previewMessages := allMessages
	if len(allMessages) > 10 {
		previewMessages = allMessages[:10]
	}

	messages := map[string]interface{}{
		"answering_machine": map[string]interface{}{
			"enabled":        status.Enabled,
			"new_messages":   status.NewMessages,
			"total_messages": status.TotalMessages,
			"storage_used":   status.StorageUsed,
			"last_checked":   status.LastChecked.Format(time.RFC3339),
		},
		"messages": previewMessages, // Real messages from BTicino device
		"enhanced_integration": map[string]interface{}{
			"real_filesystem_paths": []string{
				"/home/bticino/cfg/extra/47/messages/",
				"/var/tmp/conf.xml",
				"/sys/class/leds",
				"/sys/class/gpio",
			},
			"proven_commands": []string{
				"*8*91##",        // Enable answering machine
				"*8*92##",        // Disable answering machine
				"*#8**33*1##",    // Audio status query
				"*7*73#1#100*##", // Display activation
			},
			"version":                version.GetVersion(),
			"real_messages_detected": status.TotalMessages,
			"filesystem_integration": "active",
			"features": []string{
				"Real message parsing with metadata",
				"Image and video file access",
				"Message pagination and filtering",
				"Read/unread status management",
				"File download functionality",
				"Message deletion capabilities",
			},
		},
	}
	ws.writeJSON(w, messages)
}

// @Summary Unlock door
// @Description Sends door unlock command to open the door lock (press + release sequence)
// @Tags Controls
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/controls/door/unlock [post]
func (ws *WebServer) handleDoorUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ws.logger.Info("Door unlock requested via web dashboard")

	// Send press command
	pressCmd := bticino.CmdDoorOpenPress // *8*19*20##
	err := ws.bridge.SendOpenWebNetCommand(pressCmd)
	if err != nil {
		ws.logger.WithError(err).Error("Failed to send door unlock press command")
		ws.writeJSON(w, map[string]interface{}{
			"success":   false,
			"message":   "Failed to send door unlock command: " + err.Error(),
			"command":   pressCmd,
			"timestamp": time.Now().Format(time.RFC3339),
		})
		return
	}

	// Wait 1 second then send release command (like the working script)
	go func() {
		time.Sleep(1 * time.Second)
		releaseCmd := bticino.CmdDoorOpenRelease // *8*20*20##
		if err := ws.bridge.SendOpenWebNetCommand(releaseCmd); err != nil {
			ws.logger.WithError(err).Error("Failed to send door unlock release command")
		} else {
			ws.logger.Info("Door unlock release command sent successfully")
		}
	}()

	ws.logger.Info("Door unlock press command sent, release scheduled in 1s")
	ws.writeJSON(w, map[string]interface{}{
		"success":   true,
		"message":   "Door unlock command sent (press + release)",
		"command":   pressCmd,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// @Summary Toggle answering machine
// @Description Enable or disable the answering machine (voicemail) feature
// @Tags Controls
// @Accept json
// @Produce json
// @Param action query string false "Action: enable or disable"
// @Success 200 {object} map[string]interface{}
// @Router /api/controls/answering-machine/toggle [post]
func (ws *WebServer) handleAnsweringMachineToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	action := r.URL.Query().Get("action") // "enable" or "disable"

	var command string
	var message string

	if action == "enable" {
		command = bticino.CmdVoicemailOn // *8*91##
		message = "Answering machine enabled"
	} else {
		command = bticino.CmdVoicemailOff // *8*92##
		message = "Answering machine disabled"
	}

	ws.logger.Infof("Answering machine %s requested via web dashboard", action)

	// Send real OpenWebNet command
	err := ws.bridge.SendOpenWebNetCommand(command)

	var result map[string]interface{}
	if err != nil {
		ws.logger.WithError(err).Errorf("Failed to send answering machine command: %s", command)
		result = map[string]interface{}{
			"success":   false,
			"message":   "Failed to " + message + ": " + err.Error(),
			"command":   command,
			"action":    action,
			"timestamp": time.Now().Format(time.RFC3339),
		}
	} else {
		ws.logger.Infof("Answering machine command sent successfully: %s", command)
		result = map[string]interface{}{
			"success":   true,
			"message":   message,
			"command":   command,
			"action":    action,
			"timestamp": time.Now().Format(time.RFC3339),
		}
	}

	ws.writeJSON(w, result)
}

// handleGenericOWNCommand is a helper that sends a single OWN command and returns JSON result
func (ws *WebServer) sendOWNCommand(w http.ResponseWriter, r *http.Request, command, description string) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ws.logger.Infof("%s requested via web dashboard", description)
	err := ws.bridge.SendOpenWebNetCommand(command)

	result := map[string]interface{}{
		"command":   command,
		"action":    description,
		"timestamp": time.Now().Format(time.RFC3339),
	}
	if err != nil {
		ws.logger.WithError(err).Errorf("Failed: %s", description)
		result["success"] = false
		result["message"] = "Failed: " + err.Error()
	} else {
		ws.logger.Infof("Success: %s", description)
		result["success"] = true
		result["message"] = description
	}
	ws.writeJSON(w, result)
}

// @Summary Turn display on
// @Description Turn on the device display
// @Tags Controls
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/controls/display/on [post]
func (ws *WebServer) handleDisplayOn(w http.ResponseWriter, r *http.Request) {
	ws.sendOWNCommand(w, r, bticino.CmdDisplayOn, "Display ON")
}

// @Summary Turn display off
// @Description Turn off the device display
// @Tags Controls
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/controls/display/off [post]
func (ws *WebServer) handleDisplayOff(w http.ResponseWriter, r *http.Request) {
	ws.sendOWNCommand(w, r, bticino.CmdDisplayOff, "Display OFF")
}

// @Summary Mute device
// @Description Mute the device (disable sounds)
// @Tags Controls
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/controls/mute/on [post]
func (ws *WebServer) handleMuteOn(w http.ResponseWriter, r *http.Request) {
	ws.sendOWNCommand(w, r, bticino.CmdMuteOn, "Mute ON")
}

// @Summary Unmute device
// @Description Unmute the device (enable sounds)
// @Tags Controls
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/controls/mute/off [post]
func (ws *WebServer) handleMuteOff(w http.ResponseWriter, r *http.Request) {
	ws.sendOWNCommand(w, r, bticino.CmdMuteOff, "Mute OFF")
}

// @Summary Enable doorbell sound
// @Description Enable the doorbell sound
// @Tags Controls
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/controls/doorbell/on [post]
func (ws *WebServer) handleDoorbellSoundOn(w http.ResponseWriter, r *http.Request) {
	ws.sendOWNCommand(w, r, bticino.CmdBellOn, "Doorbell sound ON")
}

// @Summary Disable doorbell sound
// @Description Disable the doorbell sound
// @Tags Controls
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/controls/doorbell/off [post]
func (ws *WebServer) handleDoorbellSoundOff(w http.ResponseWriter, r *http.Request) {
	ws.sendOWNCommand(w, r, bticino.CmdBellOff, "Doorbell sound OFF")
}

// @Summary Turn staircase light on
// @Description Turn on the staircase light (press + release sequence)
// @Tags Controls
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/controls/light/on [post]
func (ws *WebServer) handleLightOn(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ws.logger.Info("Staircase light ON requested via web dashboard")
	err := ws.bridge.SendOpenWebNetCommand(bticino.CmdLightOnPress)
	if err != nil {
		ws.writeJSON(w, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// Send release after 1 second (same as door unlock pattern)
	go func() {
		time.Sleep(1 * time.Second)
		if err := ws.bridge.SendOpenWebNetCommand(bticino.CmdLightOnRelease); err != nil {
			ws.logger.WithError(err).Error("Failed to send light release command")
		}
	}()
	ws.writeJSON(w, map[string]interface{}{
		"success":   true,
		"message":   "Staircase light ON (press + release)",
		"command":   bticino.CmdLightOnPress,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// @Summary Send arbitrary OpenWebNet command
// @Description Send one OpenWebNet frame (digits, '*' and '#'; starts with * and ends with ##)
// @Tags Controls
// @Accept json
// @Produce json
// @Param request body object true "Command payload"
// @Success 200 {object} map[string]interface{}
// @Router /api/controls/command [post]
// handleArbitraryCommand allows sending any OpenWebNet command (for advanced users / debugging)
func (ws *WebServer) handleArbitraryCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Command == "" {
		http.Error(w, "Invalid request: JSON body with 'command' field required", http.StatusBadRequest)
		return
	}

	if !openwebnet.IsSingleFrame(body.Command) {
		http.Error(w, "Invalid OpenWebNet command: exactly one frame of digits, '*' and '#', starting with * and ending with ##", http.StatusBadRequest)
		return
	}

	ws.logger.Infof("Arbitrary command requested: %s", body.Command)
	err := ws.bridge.SendOpenWebNetCommand(body.Command)

	result := map[string]interface{}{
		"command":   body.Command,
		"timestamp": time.Now().Format(time.RFC3339),
	}
	if err != nil {
		result["success"] = false
		result["message"] = "Failed: " + err.Error()
	} else {
		result["success"] = true
		result["message"] = "Command sent: " + body.Command
	}
	ws.writeJSON(w, result)
}

func (ws *WebServer) writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		ws.logger.WithError(err).Error("Failed to write JSON response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (ws *WebServer) getUptime() string {
	return formatUptime(time.Since(ws.startTime))
}

func formatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	return fmt.Sprintf("%dh %dm", hours, minutes)
}

// Enhanced Message Management API Handlers

// @Summary List messages
// @Description Returns a paginated list of messages with optional filters
// @Tags Messages
// @Accept json
// @Produce json
// @Param page query int false "Page number (default 1)"
// @Param limit query int false "Items per page (max 100, default 20)"
// @Param unread_only query bool false "Show only unread messages"
// @Success 200 {object} map[string]interface{}
// @Router /api/messages/list [get]
// handleAPIMessagesList returns paginated list of messages with optional filters
func (ws *WebServer) handleAPIMessagesList(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20 // Default page size
	}

	unreadOnly := r.URL.Query().Get("unread_only") == "true"

	// Get all messages
	messages, err := ws.messageParser.GetAllMessages()
	if err != nil {
		http.Error(w, "Failed to retrieve messages", http.StatusInternalServerError)
		ws.logger.WithError(err).Error("Failed to retrieve messages")
		return
	}

	// Filter unread messages if requested
	if unreadOnly {
		filteredMessages := make([]*messageparser.Message, 0)
		for _, msg := range messages {
			if !msg.Read {
				filteredMessages = append(filteredMessages, msg)
			}
		}
		messages = filteredMessages
	}

	// Calculate pagination
	totalMessages := len(messages)
	totalPages := (totalMessages + limit - 1) / limit
	start := (page - 1) * limit
	end := start + limit

	if start >= totalMessages {
		messages = []*messageparser.Message{}
	} else if end > totalMessages {
		messages = messages[start:]
	} else {
		messages = messages[start:end]
	}

	response := map[string]interface{}{
		"messages": messages,
		"pagination": map[string]interface{}{
			"page":         page,
			"limit":        limit,
			"total":        totalMessages,
			"total_pages":  totalPages,
			"has_next":     page < totalPages,
			"has_previous": page > 1,
		},
		"filters": map[string]interface{}{
			"unread_only": unreadOnly,
		},
	}

	// Publish to MQTT if configured
	if pagination, ok := response["pagination"].(map[string]interface{}); ok {
		ws.publishMessageListMQTT(messages, pagination)
		ws.publishMessageStatusMQTT()
	}

	ws.writeJSON(w, response)
}

// @Summary Get message detail
// @Description Returns detailed information about a specific message by ID
// @Tags Messages
// @Accept json
// @Produce json
// @Param id path int true "Message ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/messages/{id} [get]
// handleAPIMessageDetail returns detailed information about a specific message
func (ws *WebServer) handleAPIMessageDetail(w http.ResponseWriter, r *http.Request) {
	// Extract message ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/messages/")
	messageID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "Invalid message ID", http.StatusBadRequest)
		return
	}

	message, err := ws.messageParser.GetMessage(messageID)
	if err != nil {
		http.Error(w, "Message not found", http.StatusNotFound)
		return
	}

	ws.writeJSON(w, message)
}

// @Summary Download message media
// @Description Downloads the media file (image or video) for a specific message
// @Tags Messages
// @Accept json
// @Produce json
// @Param id path int true "Message ID"
// @Param type path string true "Media type: image or video"
// @Success 200 {file} file
// @Router /api/messages/download/{id}/{type} [get]
// handleAPIMessageDownload handles file downloads (image, video)
func (ws *WebServer) handleAPIMessageDownload(w http.ResponseWriter, r *http.Request) {
	// Parse URL: /api/messages/download/123/image or /api/messages/download/123/video
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/messages/download/"), "/")
	if len(pathParts) != 2 {
		http.Error(w, "Invalid download path", http.StatusBadRequest)
		return
	}

	messageID, err := strconv.Atoi(pathParts[0])
	if err != nil {
		http.Error(w, "Invalid message ID", http.StatusBadRequest)
		return
	}

	fileType := pathParts[1]

	var data []byte
	var filename string
	var contentType string

	switch fileType {
	case "image":
		data, err = ws.messageParser.GetImageData(messageID)
		filename = fmt.Sprintf("message_%d_image.jpg", messageID)
		contentType = "image/jpeg"
	case "video":
		data, err = ws.messageParser.GetVideoData(messageID)
		filename = fmt.Sprintf("message_%d_video.avi", messageID)
		contentType = "video/x-msvideo"
	default:
		http.Error(w, "Invalid file type. Use 'image' or 'video'", http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// Set headers for download
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))

	// Write file data
	w.Write(data)

	ws.logger.Infof("Downloaded %s for message %d", fileType, messageID)
}

// @Summary Mark message as read
// @Description Marks a message as read or unread
// @Tags Messages
// @Accept json
// @Produce json
// @Param id path int true "Message ID"
// @Param request body object false "Request body with read status"
// @Success 200 {object} map[string]interface{}
// @Router /api/messages/mark-read/{id} [post]
// handleAPIMessageMarkRead marks a message as read or unread
func (ws *WebServer) handleAPIMessageMarkRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" && r.Method != "PUT" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract message ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/messages/mark-read/")
	messageID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "Invalid message ID", http.StatusBadRequest)
		return
	}

	// Parse request body for read status
	type ReadStatusRequest struct {
		Read bool `json:"read"`
	}

	var req ReadStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Read = true // Default to marking as read
	}

	if req.Read {
		err = ws.messageParser.MarkMessageAsRead(messageID)
	} else {
		err = ws.messageParser.MarkMessageAsUnread(messageID)
	}

	if err != nil {
		http.Error(w, "Failed to update message status", http.StatusInternalServerError)
		ws.logger.WithError(err).Errorf("Failed to update read status for message %d", messageID)
		return
	}

	response := map[string]interface{}{
		"success":    true,
		"message_id": messageID,
		"read":       req.Read,
		"timestamp":  time.Now().Format(time.RFC3339),
	}

	// Publish to MQTT if configured
	ws.publishMessageOperationResultMQTT("marked_read", messageID, true, "")
	ws.publishMessageStatusMQTT()

	ws.writeJSON(w, response)
	ws.logger.Infof("Message %d marked as %s", messageID, map[bool]string{true: "read", false: "unread"}[req.Read])
}

// @Summary Mark all messages as read
// @Description Marks every answering-machine message as read
// @Tags Messages
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/messages/mark-all-read [post]
func (ws *WebServer) handleAPIMessageMarkAllRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	updated, err := ws.messageParser.MarkAllMessagesAsRead()
	if err != nil {
		http.Error(w, "Failed to mark all messages as read", http.StatusInternalServerError)
		ws.logger.WithError(err).Errorf("mark-all-read failed after %d updates", updated)
		return
	}

	ws.publishMessageStatusMQTT()
	ws.writeJSON(w, map[string]interface{}{
		"success":   true,
		"updated":   updated,
		"timestamp": time.Now().Format(time.RFC3339),
	})
	ws.logger.Infof("Marked all messages as read (%d updated)", updated)
}

// @Summary Delete message
// @Description Deletes a specific message by ID
// @Tags Messages
// @Accept json
// @Produce json
// @Param id path int true "Message ID"
// @Success 200 {object} map[string]interface{}
// @Router /api/messages/delete/{id} [delete]
// handleAPIMessageDelete deletes a message
func (ws *WebServer) handleAPIMessageDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract message ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/api/messages/delete/")
	messageID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "Invalid message ID", http.StatusBadRequest)
		return
	}

	err = ws.messageParser.DeleteMessage(messageID)
	if err != nil {
		http.Error(w, "Failed to delete message", http.StatusInternalServerError)
		ws.logger.WithError(err).Errorf("Failed to delete message %d", messageID)
		return
	}

	response := map[string]interface{}{
		"success":    true,
		"message_id": messageID,
		"deleted_at": time.Now().Format(time.RFC3339),
	}

	// Publish to MQTT if configured
	ws.publishMessageOperationResultMQTT("deleted", messageID, true, "")
	ws.publishMessageStatusMQTT()

	ws.writeJSON(w, response)
	ws.logger.Infof("Message %d deleted successfully", messageID)
}

// @Summary Get all memos
// @Description Returns all voice and text memos from the device
// @Tags Memos
// @Accept json
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/memos [get]
func (ws *WebServer) handleAPIMemos(w http.ResponseWriter, r *http.Request) {
	memoParser := messageparser.NewMemoParser()
	memos, err := memoParser.GetAllMemos()
	if err != nil {
		ws.logger.WithError(err).Error("Failed to get memos")
		http.Error(w, "Failed to get memos", http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"memos": memos,
		"total": len(memos),
		"voice": countMemosByType(memos, "voice"),
		"text":  countMemosByType(memos, "text"),
	}
	ws.writeJSON(w, response)
}

func countMemosByType(memos []*messageparser.Memo, memoType string) int {
	count := 0
	for _, m := range memos {
		if m.Type == memoType {
			count++
		}
	}
	return count
}

// handleAPIMemoDetail handles individual memo requests
func (ws *WebServer) handleAPIMemoDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/memos/")
	if path == "" {
		http.Error(w, "Invalid memo ID", http.StatusBadRequest)
		return
	}

	memoID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "Invalid memo ID", http.StatusBadRequest)
		return
	}

	memoParser := messageparser.NewMemoParser()
	memos, err := memoParser.GetAllMemos()
	if err != nil {
		http.Error(w, "Failed to get memo", http.StatusInternalServerError)
		return
	}

	for _, m := range memos {
		if m.ID == memoID {
			ws.writeJSON(w, m)
			return
		}
	}

	http.Error(w, "Memo not found", http.StatusNotFound)
}

// handleAPIMemoAudio serves audio files for voice memos
func (ws *WebServer) handleAPIMemoAudio(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/memos/audio/")
	memoID, err := strconv.Atoi(path)
	if err != nil {
		http.Error(w, "Invalid memo ID", http.StatusBadRequest)
		return
	}

	audioPath := fmt.Sprintf("/home/bticino/cfg/extra/47/memos_voice/memo_%d/audio.wav", memoID)

	if _, err := os.Stat(audioPath); os.IsNotExist(err) {
		http.Error(w, "Audio not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, audioPath)
}

// handleAPIMemoMarkRead marks a memo as read/unread
func (ws *WebServer) handleAPIMemoMarkRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/memos/mark-read/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid request: /api/memos/mark-read/{id}/{read}", http.StatusBadRequest)
		return
	}

	memoID, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "Invalid memo ID", http.StatusBadRequest)
		return
	}

	readStatus := parts[1] == "read"
	memoType := "voice"
	if len(parts) >= 3 {
		memoType = parts[2]
	}

	memoParser := messageparser.NewMemoParser()
	if readStatus {
		err = memoParser.MarkMemoAsRead(memoID, memoType)
	} else {
		err = memoParser.MarkMemoAsUnread(memoID, memoType)
	}

	if err != nil {
		http.Error(w, "Failed to update memo status", http.StatusInternalServerError)
		ws.logger.WithError(err).Errorf("Failed to mark memo %d as %s", memoID, parts[1])
		return
	}

	response := map[string]interface{}{
		"success":   true,
		"memo_id":   memoID,
		"memo_type": memoType,
		"read":      readStatus,
	}
	ws.writeJSON(w, response)
	ws.logger.Infof("Memo %d (%s) marked as %s", memoID, memoType, parts[1])
}

// handleAPIMemoDelete deletes a memo
func (ws *WebServer) handleAPIMemoDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/memos/delete/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		http.Error(w, "Invalid request: /api/memos/delete/{id}/{type}", http.StatusBadRequest)
		return
	}

	memoID, err := strconv.Atoi(parts[0])
	if err != nil {
		http.Error(w, "Invalid memo ID", http.StatusBadRequest)
		return
	}

	memoType := parts[1]
	if memoType != "voice" && memoType != "text" {
		memoType = "voice"
	}

	memoParser := messageparser.NewMemoParser()
	err = memoParser.DeleteMemo(memoID, memoType)
	if err != nil {
		http.Error(w, "Failed to delete memo", http.StatusInternalServerError)
		ws.logger.WithError(err).Errorf("Failed to delete memo %d", memoID)
		return
	}

	response := map[string]interface{}{
		"success":   true,
		"memo_id":   memoID,
		"memo_type": memoType,
	}
	ws.writeJSON(w, response)
	ws.logger.Infof("Memo %d (%s) deleted successfully", memoID, memoType)
}

// MQTT Publishing Helper Functions

// publishMessageStatusMQTT publishes message count and status updates to MQTT
func (ws *WebServer) publishMessageStatusMQTT() {
	if ws.mqttPublisher == nil {
		return // MQTT not configured
	}

	// Get current message statistics
	messages, err := ws.messageParser.GetAllMessages()
	if err != nil {
		ws.logger.Warnf("Failed to get messages for MQTT update: %v", err)
		return
	}

	totalCount := len(messages)
	unreadCount := 0
	latestID := 0
	latestTimestamp := ""

	for _, msg := range messages {
		if !msg.Read {
			unreadCount++
		}
		if msg.ID > latestID {
			latestID = msg.ID
			latestTimestamp = msg.Message // Timestamp is stored in message field
		}
	}

	// Publish individual statistics
	topicPrefix := "video_intercom/messages"
	ws.mqttPublisher(fmt.Sprintf("%s/total_count", topicPrefix), fmt.Sprintf("%d", totalCount), true)
	ws.mqttPublisher(fmt.Sprintf("%s/unread_count", topicPrefix), fmt.Sprintf("%d", unreadCount), true)
	ws.mqttPublisher(fmt.Sprintf("%s/latest_id", topicPrefix), fmt.Sprintf("%d", latestID), true)
	ws.mqttPublisher(fmt.Sprintf("%s/latest_timestamp", topicPrefix), latestTimestamp, true)

	// Calculate approximate storage usage (5MB per video + 5KB per image)
	storageMB := totalCount*5 + (totalCount*5)/1024 // Rough estimate
	ws.mqttPublisher(fmt.Sprintf("%s/storage_usage", topicPrefix), fmt.Sprintf("%d", storageMB), true)

	ws.logger.Debugf("Published message statistics to MQTT: total=%d, unread=%d", totalCount, unreadCount)
}

// publishMessageListMQTT publishes the current message list and pagination info to MQTT
func (ws *WebServer) publishMessageListMQTT(messages []*messageparser.Message, pagination map[string]interface{}) {
	if ws.mqttPublisher == nil {
		return
	}

	// Create simplified message list for MQTT (without base64 images)
	simplifiedMessages := make([]map[string]interface{}, len(messages))
	for i, msg := range messages {
		simplifiedMessages[i] = map[string]interface{}{
			"id":         msg.ID,
			"caller_id":  msg.CallerID,
			"message":    msg.Message,
			"read":       msg.Read,
			"has_image":  msg.HasImage,
			"has_video":  msg.HasVideo,
			"image_path": msg.ImagePath,
			"video_path": msg.VideoPath,
		}
	}

	// Marshal to JSON
	messagesJSON, err := json.Marshal(map[string]interface{}{
		"messages": simplifiedMessages,
	})
	if err != nil {
		ws.logger.Warnf("Failed to marshal messages for MQTT: %v", err)
		return
	}

	paginationJSON, err := json.Marshal(pagination)
	if err != nil {
		ws.logger.Warnf("Failed to marshal pagination for MQTT: %v", err)
		return
	}

	// Publish to MQTT
	topicPrefix := "video_intercom/messages"
	ws.mqttPublisher(fmt.Sprintf("%s/list", topicPrefix), string(messagesJSON), false)
	ws.mqttPublisher(fmt.Sprintf("%s/pagination", topicPrefix), string(paginationJSON), false)

	ws.logger.Debugf("Published message list to MQTT: %d messages", len(messages))
}

// publishMessageOperationResultMQTT publishes the result of message operations to MQTT
func (ws *WebServer) publishMessageOperationResultMQTT(operation string, messageID int, success bool, errorMsg string) {
	if ws.mqttPublisher == nil {
		return
	}

	resultData := map[string]interface{}{
		"message_id": messageID,
		"success":    success,
		"timestamp":  time.Now().Format(time.RFC3339),
	}

	if !success && errorMsg != "" {
		resultData["error"] = errorMsg
	}

	resultJSON, err := json.Marshal(resultData)
	if err != nil {
		ws.logger.Warnf("Failed to marshal operation result for MQTT: %v", err)
		return
	}

	topic := fmt.Sprintf("video_intercom/messages/events/%s", operation)
	ws.mqttPublisher(topic, string(resultJSON), false)

	ws.logger.Debugf("Published %s operation result to MQTT: success=%v", operation, success)
}
