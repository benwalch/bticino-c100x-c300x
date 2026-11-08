package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"bticino_bridge/pkg/access"
	"bticino_bridge/pkg/config"
	"bticino_bridge/pkg/events"
	"bticino_bridge/pkg/homekit"
	"bticino_bridge/pkg/input"
	"bticino_bridge/pkg/logging"
	"bticino_bridge/pkg/openwebnet"
	"bticino_bridge/pkg/sip"
	versionpkg "bticino_bridge/pkg/version"
	"bticino_bridge/pkg/webserver"

	// Enhanced packages from BTicino device analysis
	"bticino_bridge/pkg/bticino"
	"bticino_bridge/pkg/bticino_commands"
	"bticino_bridge/pkg/messageparser"
	"bticino_bridge/pkg/mqtt"
	"bticino_bridge/pkg/multicast"
	"bticino_bridge/pkg/multicast/handlers"
	"bticino_bridge/pkg/udpproxy"
)

var (
	BuildTime = "unknown"
)

// SimpleMQTTBridge is a basic MQTT bridge interface for web server compatibility
type SimpleMQTTBridge struct {
	logger   *logrus.Logger
	owClient *openwebnet.Client
}

func (s *SimpleMQTTBridge) GetMulticastStats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":            false,
		"running":            false,
		"total_messages":     0,
		"unhandled_messages": 0,
		"error_count":        0,
	}
}

func (s *SimpleMQTTBridge) GetEventBusStats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":            false,
		"total_events":       0,
		"active_subscribers": 0,
	}
}

func (s *SimpleMQTTBridge) GetVideoStats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":              false,
		"sip_registered":       false,
		"rtsp_active_sessions": 0,
		"video_manager":        nil,
	}
}

// ExecuteCommand sends an OpenWebNet command via netcat (non-interfering method)
func (s *SimpleMQTTBridge) ExecuteCommand(command string) (bool, error) {
	s.logger.WithField("command", command).Info("Executing OpenWebNet command via netcat (SimpleMQTTBridge)")

	// Use netcat method to avoid interfering with native BTicino processes
	// This is the same method used in the working script
	cmd := exec.Command("sh", "-c", fmt.Sprintf("echo '%s' | nc 0 30006", command))

	if err := cmd.Run(); err != nil {
		s.logger.WithError(err).WithField("command", command).Error("Failed to execute OpenWebNet command via netcat")
		return false, fmt.Errorf("failed to send command %s via netcat: %w", command, err)
	}

	s.logger.WithFields(map[string]interface{}{
		"command": command,
		"method":  "netcat",
		"port":    "30006",
	}).Info("OpenWebNet command executed successfully via netcat")

	return true, nil
}

// SendOpenWebNetCommand implements BTicinoBridge interface for WebServer integration
func (s *SimpleMQTTBridge) SendOpenWebNetCommand(command string) error {
	s.logger.WithField("command", command).Info("Sending OpenWebNet command via SimpleMQTTBridge")

	_, err := s.ExecuteCommand(command)
	return err
}

// ownAdapter adapta *openwebnet.Client (SendCommand devuelve (*Command, error))
// a la interfaz sip.OWNSender (SendOpenWebNetCommand devuelve solo error) que
// necesita AudioCall para armar el audio bidireccional (WHO=8) antes del talk-back.
type ownAdapter struct{ client *openwebnet.Client }

func (a *ownAdapter) SendOpenWebNetCommand(cmd string) error {
	_, err := a.client.SendCommand(cmd)
	return err
}

func main() {
	// Parse command line flags
	var (
		configPath       = flag.String("config", "configs/config.yaml", "Path to configuration file")
		logLevel         = flag.String("log-level", "", "Log level (debug, info, warn, error); overrides logging.level")
		version          = flag.Bool("version", false, "Show version information")
		testMode         = flag.Bool("test", false, "Run in test mode (no actual device connection)")
		webPort          = flag.Int("web-port", 0, "Override web server port (0 = use config)")
		enableOpenWebNet = flag.Bool("enable-openwebnet", true, "Enable OpenWebNet client")
		enableWeb        = flag.Bool("enable-web", true, "Enable web interface")
		enableHomeKit    = flag.Bool("enable-homekit", true, "Enable HomeKit bridge")
		enableVideo      = flag.Bool("enable-video", true, "Enable video/SIP system")
		enableAnswerer   = flag.Bool("enable-answerer", true, "Enable SIP answerer (contestador)")
	)
	flag.Parse()

	if *version {
		fmt.Printf("BTicino Bridge v%s (built: %s)\n", versionpkg.GetVersion(), BuildTime)
		fmt.Println("🚀 BTicino Bridge - Real Device Integration:")
		fmt.Println("  ✅ OpenWebNet (50+ proven commands)")
		fmt.Println("  🌐 Web Dashboard")
		fmt.Println("  🏠 HomeKit + Home Assistant (MQTT integration)")
		fmt.Println("  📹 Video/SIP System (Streaming, RTSP)")
		fmt.Println("  📱 Answering Machine (Message parsing)")
		os.Exit(0)
	}

	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
		ForceColors:   true,
	})

	// Load the config first, so all start lines already follow logging.*
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		cfg = config.GetDefaultConfig()
	}

	logOptions := logging.Options{
		Level:    cfg.Logging.Level,
		File:     cfg.Logging.File,
		MaxSize:  cfg.Logging.MaxSize,
		MaxFiles: cfg.Logging.MaxFiles,
	}
	if *logLevel != "" {
		logOptions.Level = *logLevel
	}
	// A -test run must show its output and leave the log of a running bridge alone.
	if *testMode {
		logOptions.File = ""
	}

	// Before Configure, so that a log file problem can reach the crash log.
	var previousCrash string
	var crashErr error
	if !*testMode {
		var exe string
		if exe, crashErr = os.Executable(); crashErr == nil {
			// Next to the binary, which on the device means on flash.
			crashLog := filepath.Join(filepath.Dir(exe), "bticino_bridge.crash.log")
			previousCrash, crashErr = logging.EnableCrashLog(crashLog, versionpkg.GetVersion())
		}
	}

	logging.Configure(logger, logOptions)

	if crashErr != nil {
		logger.Errorf("Crash log: %v", crashErr)
	}
	if previousCrash != "" {
		logger.Errorf("Previous run crashed: %s (see bticino_bridge.crash.log)", previousCrash)
	}

	logger.Info("🚀 === BTicino Bridge ===")
	logger.Infof("Version: %s (built: %s)", versionpkg.GetVersion(), BuildTime)
	logger.Info("Starting BTicino Bridge:")
	logger.Info("  🔌 OpenWebNet (50+ proven commands)")
	logger.Info("  🌐 Web Dashboard")
	logger.Info("  🏠 HomeKit + Home Assistant (MQTT)")
	logger.Info("  📹 Video/SIP System (Streaming)")
	logger.Info("  📱 Answering Machine")
	logger.Info("  🔄 Enhanced EventBus & Real-time Processing")

	if err != nil {
		logger.Errorf("Failed to load config from %s, using defaults: %v", *configPath, err)
	}

	// Override web port if specified
	if *webPort > 0 {
		cfg.Web.Port = *webPort
		cfg.Web.Enabled = *enableWeb
		logger.WithField("port", *webPort).Info("Web server port overridden via command line")
	}

	// Apply component enable/disable flags
	cfg.Web.Enabled = cfg.Web.Enabled && *enableWeb
	cfg.HomeKit.Enabled = cfg.HomeKit.Enabled && *enableHomeKit
	cfg.SIP.Enabled = cfg.SIP.Enabled && *enableVideo
	cfg.SIP.Answerer.Enabled = cfg.SIP.Answerer.Enabled && cfg.SIP.Enabled && *enableAnswerer

	logger.Infof("Configuration loaded: device=%s", cfg.OpenWebNet.Host)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create central EventBus for all components
	eventBus := events.NewEventBus(10, logger)
	logger.Info("✅ Central EventBus created for component communication")

	// Clasificador de accesos (portal → telefonillo/llave). Suscriptor puro del
	// EventBus; no toca el bus OWN. Se gobierna con access.enabled del config.
	if cfg.Access.Enabled {
		accessClassifier := access.NewClassifier(cfg.Access, eventBus, logger)
		accessClassifier.Start()
		defer accessClassifier.Stop()
		logger.Info("Clasificador de accesos activado")
	}

	// Component variables
	var owClient *openwebnet.Client
	var webServer *webserver.WebServer
	var homekitBridge *homekit.BticinoBridge
	var sipClient *sip.BTicinoSIPClient
	var videoManager *sip.VideoStreamManager
	var rtspServer *sip.EnhancedRTSPServer
	var mqttBridge *mqtt.MQTTBridge

	// Enhanced components from BTicino device analysis
	var messageParser *messageparser.MessageParser
	var commandHandler *bticino_commands.BTicinoCommandHandler
	var inputMonitor *input.InputMonitor
	var multicastListener *multicast.MulticastListener

	// Simple MQTT bridge for web compatibility
	simpleMQTT := &SimpleMQTTBridge{logger: logger}

	// ==================== COMPONENT 1: Enhanced OpenWebNet Client ====================
	if *enableOpenWebNet {
		logger.Info("🔌 Initializing Enhanced OpenWebNet client...")
		logger.Info("🤝 Using non-interfering mode: monitoring only on port 20000, commands via netcat")

		owConfig := openwebnet.ClientConfig{
			Host:         cfg.OpenWebNet.Host,
			Ports:        []int{20000}, // ONLY port 20000 for monitoring - NO interference with native processes
			MainPort:     20000,
			VideoPort:    0, // Disabled - avoids competing with bt_vct (video control)
			ConfigPort:   0, // Disabled - avoids competing with openserver native process
			Timeout:      cfg.OpenWebNet.Timeout,
			RetryCount:   cfg.OpenWebNet.RetryAttempts,
			RetryDelay:   cfg.OpenWebNet.RetryDelay,
			EnableSafety: true, // Enable 4-level security system
			Logger:       logger,
		}

		owClient = openwebnet.NewClient(owConfig)

		if !*testMode {
			logger.Info("Connecting to BTicino device...")
			if err := owClient.Connect(); err != nil {
				logger.Errorf("Failed to connect to BTicino device: %v", err)
				logger.Warn("Continuing without OpenWebNet - features limited")
			} else {
				logger.Info("✅ OpenWebNet: Successfully connected to BTicino device")

				// Enable critical commands for door unlock functionality
				logger.Warn("🔓 Enabling critical commands for door unlock functionality")
				owClient.EnableCriticalCommands(true)
			}
		} else {
			logger.Info("Running in test mode - no OpenWebNet device connection")
		}
	}

	// ==================== COMPONENTS: BTicino Integration ====================
	logger.Info("🔧 Initializing Enhanced BTicino Components...")

	// Initialize Message Parser for answering machine (300X only: the
	// Classe 100X ships the binaries but has no answering machine).
	if bticino.AnsweringMachineAvailable() {
		messageParser = messageparser.NewMessageParser()
		logger.Info("✅ Enhanced Message Parser: Configured for real BTicino filesystem")
	} else {
		logger.Info("Message Parser disabled: no answering machine on this unit")
	}

	// Initialize Enhanced Command Handler
	if owClient != nil {
		commandHandler = bticino_commands.NewBTicinoCommandHandler(owClient, logger)
		logger.Info("✅ Enhanced Command Handler: 50+ proven BTicino commands ready")
	} else {
		logger.Warn("Enhanced Command Handler disabled: No OpenWebNet connection")
	}

	// ==================== MQTT INTEGRATION: Real MQTT Client ====================
	if cfg.MQTT.Enabled {
		logger.Info("📡 Initializing Real MQTT Bridge for Home Assistant...")

		// Create real MQTT bridge
		var err error
		mqttBridge, err = mqtt.NewMQTTBridge(cfg, logger)
		if err != nil {
			logger.WithError(err).Error("Failed to create MQTT bridge")
		} else {
			logger.Info("✅ MQTT Bridge: Real client created for Home Assistant integration")
			logger.Infof("📡 MQTT Broker: %s:%d (user: %s)", cfg.MQTT.Host, cfg.MQTT.Port, cfg.MQTT.Username)

			// Start the device config publisher via MQTT (Phase 3)
			go func() {
				time.Sleep(5 * time.Second)                           // Wait for MQTT to connect
				mqttBridge.SetDeviceConfigPublisher(60 * time.Second) // Publish every 60s
			}()
		}
	} else {
		logger.Info("MQTT Bridge disabled in configuration")
	}

	logger.Info("🔧 Enhanced BTicino Components initialization complete")

	// ==================== COMPONENT 2: Web Dashboard ====================
	// LAN IP for user-facing log URLs (auto-detected; the old 300X IP
	// 192.168.1.38 must not be hardcoded — DHCP changes it).
	deviceIP := bticino.LocalOutboundIP()

	if cfg.Web.Enabled {
		logger.Info("🌐 Initializing Web Dashboard...")

		// Update SimpleMQTTBridge with owClient for command execution
		simpleMQTT.owClient = owClient

		webServer = webserver.NewWebServer(cfg, simpleMQTT, logger, *configPath)
		webServer.SetGlobalWebServer()

		if err := webServer.Start(ctx); err != nil {
			logger.WithError(err).Error("Failed to start web server, continuing without web interface")
			webServer = nil
		} else {
			logger.Infof("✅ Web Dashboard: Started successfully on http://%s:%d", deviceIP, cfg.Web.Port)
			logger.Info("  - Enhanced with real BTicino filesystem integration")
			logger.Info("  - Message parsing from /home/bticino/cfg/extra/47/messages/")
		}
	} else {
		logger.Info("Web dashboard disabled in configuration")
	}

	// ==================== COMPONENT 3: HomeKit Bridge ====================
	if cfg.HomeKit.Enabled {
		logger.Info("🏠 Initializing HomeKit Bridge...")

		homekitConfig := &homekit.Config{
			Name:         cfg.HomeKit.Name,
			Manufacturer: cfg.HomeKit.Manufacturer,
			Model:        cfg.HomeKit.Model,
			Port:         cfg.HomeKit.Port,
			Pin:          cfg.HomeKit.Pin,
			StoragePath:  cfg.HomeKit.StoragePath,
			Enabled:      cfg.HomeKit.Enabled,
		}

		homekitBridge, err = homekit.NewBticinoBridge(homekitConfig, eventBus, logger)
		if err != nil {
			logger.WithError(err).Error("Failed to create HomeKit bridge, continuing without HomeKit")
		} else {
			if err := homekitBridge.Start(); err != nil {
				logger.WithError(err).Error("Failed to start HomeKit bridge, continuing without HomeKit")
				homekitBridge = nil
			} else {
				logger.Infof("✅ HomeKit: Bridge started on port %s (PIN: %s)", cfg.HomeKit.Port, cfg.HomeKit.Pin)
			}
		}
	} else {
		logger.Info("HomeKit bridge disabled in configuration")
	}

	// ==================== COMPONENT 4: Video/SIP System ====================
	if cfg.SIP.Enabled {
		logger.Info("📹 Initializing Video/SIP System...")

		// Determine local IP for SIP
		localIP := cfg.SIP.LocalHost
		if localIP == "" {
			localIP = deviceIP
		}

		// Create SIP configuration from main config
		// Uses unified SIP config (compatible with both old and new config formats)
		serverAddr := cfg.SIP.ServerHost
		if serverAddr == "" {
			serverAddr = "sipserver.bs.iotleg.com:5061"
		} else if !strings.Contains(serverAddr, ":") {
			serverAddr = fmt.Sprintf("%s:%d", serverAddr, cfg.SIP.ServerPort)
		}

		domain := cfg.SIP.Domain
		if domain == "" {
			domain = "YOUR_GATEWAY_ID.bs.iotleg.com"
		}

		username := cfg.SIP.Username
		if username == "" {
			username = "YOUR_SIP_USERNAME"
		}

		// SIP password: omitted on purpose — the client derives
		// MD5(username:domain:username) unless sip.password is set.
		password := cfg.SIP.Password

		devAddr := cfg.SIP.DevAddr
		if devAddr == "" {
			devAddr = "20"
		}

		logger.Infof("SIP Config: server=%s, domain=%s, username=%s, dev_addr=%s",
			serverAddr, domain, username, devAddr)

		sipConfig := &sip.SIPConfig{
			ServerAddr:    serverAddr,
			Domain:        domain,
			Username:      username,
			Password:      password,
			LocalIP:       localIP,
			LocalPort:     cfg.SIP.LocalPort,
			DevAddr:       devAddr,
			NativeDevAddr: cfg.SIP.NativeDevAddr,
			ExpirySeconds: 300,
			RTSPPort:      6554,
			UseHA1:        cfg.SIP.UseHA1,
			InsecureTLS:   cfg.SIP.InsecureTLS,
			Transport:     cfg.SIP.Transport,
			SIPTarget:     cfg.SIP.SIPTarget,
			MediaPorts: sip.MediaPortConfig{
				AudioRTP: 7076,
				VideoRTP: 9078,
				RTCPPort: 9079,
			},
		}

		// Create SIP client
		sipClient = sip.NewBTicinoSIPClient(sipConfig, eventBus, logger)

		sipStarted := true
		// Retry SIP registration with backoff — Flexisip may not be ready yet after reboot
		var sipErr error
		for attempt := 1; attempt <= 5; attempt++ {
			sipErr = sipClient.Start()
			if sipErr == nil {
				break
			}
			logger.WithError(sipErr).Warnf("SIP start attempt %d/5 failed, retrying in %ds...", attempt, attempt*3)
			time.Sleep(time.Duration(attempt*3) * time.Second)
			// Reset client for retry
			sipClient = sip.NewBTicinoSIPClient(sipConfig, eventBus, logger)
		}
		if sipErr != nil {
			logger.WithError(sipErr).Warn("Failed to start SIP client after 5 attempts, continuing without full video features")
			sipClient = nil
			sipStarted = false
		} else {
			logger.Infof("SIP: Client started successfully (dual-role: webrtc + %s)", bticino.SIPUsernameForVariant(bticino.DeviceVariant))
			// Expose SIP call state + hangup to the web API
			if webServer != nil {
				webServer.SetCallController(sipClient)
				logger.Info("   Call control: GET /api/call, POST /api/controls/call/hangup")
				webServer.SetNativeDevAddrSniffer(sip.NewNativeDevAddrDiscoverer(logger))
				webServer.SetNativeDevAddrVerifyFunc(sipClient.VerifyNativeDevAddr)
				webServer.SetNativeDevAddrSessionLive(sipClient.CameraSessionActive)
				logger.Info("   Native dev addr: GET /api/sip/native-dev-addr (+discover/verify)")
			}

			// Talk-back v2: el bridge posee una llamada de audio local a c300x
			// (INVITE con SDP de audio real → 200 OK → WAV en SRTP al altavoz).
			// Only the 300X: not verified on the 100X, where the endpoint stays unavailable.
			if webServer != nil && owClient != nil && bticino.DeviceVariant == bticino.DeviceVariant300X {
				audioSender := sip.NewAudioSender("/usr/local/bin/ffmpeg", logger)
				audioReceiver := sip.NewAudioReceiver("/usr/local/bin/ffmpeg", logger)
				audioPort := sip.DefaultGStreamerConfig().VideoPort + 100 // puerto local libre para el leg de audio, sin colisionar con relay/RTSP
				audioCall := sip.NewAudioCall(sipClient, &ownAdapter{client: owClient}, audioSender, audioReceiver, "127.0.0.1", audioPort, logger)
				webServer.SetAudioCall(audioCall)
				logger.Infof("   Talk-back v2: POST /api/talkback/say (audio local port=%d)", audioPort)

				// Audio full-duplex de validación local (sdd 2026-08-21-duplex-door-audio):
				// solo se inyecta el controlador si el flag está activo en config
				// (fail-closed — sin el flag, los handlers /api/audio/duplex/* siguen
				// devolviendo 404 porque ws.duplexController nunca se asigna).
				if cfg.Audio.DuplexEnabled {
					webServer.SetDuplexController(audioCall)
					logger.Info("   Audio full-duplex activado")
				}
			}
		}

		// Create Enhanced RTSP server - can work without SIP (for go2rtc integration)
		rtspServer = sip.NewEnhancedRTSPServer(cfg.Streaming.RTSPPort, sipClient, eventBus, logger)

		// Set OpenWebNet client for *7*300 video stream activation
		if owClient != nil {
			rtspServer.SetOpenWebNetClient(owClient)
			logger.Info("   RTSP: OpenWebNet client connected for video activation")
		}

		// Select the video backend (default avmedia = cooperative *7*300)
		rtspServer.SetVideoBackend(cfg.Streaming.VideoBackend)

		// SAFETY: on-demand video activation (self-INVITE, *7*300,
		// snapshots) disabled by default. With false the RTSP server listens
		// but never fires commands toward the native firmware.
		rtspServer.SetVideoActivation(cfg.Streaming.VideoOnDemand)

		// Headless camera sessions take precedence over the legacy path
		// when enabled (see flags). Presence-driven stop is primary; the
		// 10-minute hold only backstops leaked sessions.
		rtspServer.SetCameraSession(cfg.Streaming.CameraSession, 10*time.Minute)

		// Live video during an incoming call to our unit, via a PASSIVE
		// mirror (AF_PACKET tap of the native video leg on lo:<native-port>
		// → relay 10002; 5007 high-res on c300x, 5002 low-res on c100x, see
		// bticino.DeviceVariant).
		// ZERO commands to the bus (Phase 0 2026-08-18: an own *7*300 during a real
		// call is ignored). Independent of video_on_demand.
		// `sip.call.incoming` is specific to our unit (published by the SIP
		// client on receiving an INVITE addressed to us), not other floors' activity.
		if cfg.Streaming.LiveOnCall {
			nativePort := sip.NativeVideoRTPPortForVariant(bticino.DeviceVariant)
			tap := sip.NewLoopbackRTPTap(nativePort,
				sip.DefaultGStreamerConfig().VideoPort, logger)
			liveVideo := sip.NewLiveVideoOnCall(tap, rtspServer, logger)
			// Guard: call end is detected via OWN frames (heuristic);
			// if missed, disarm alone (the tap is passive, but callActive
			// would inhibit self-INVITE indefinitely).
			liveVideo.SetGuardTimeout(5 * time.Minute)

			// Main arming: doorbell.pressed comes from the OWN monitor (reliable
			// in production). The sip.call.* events are kept in case the SIP client
			// manages to register (in production the local REGISTER usually fails
			// and those events are not emitted). Start/Stop are idempotent.
			eventBus.Subscribe("doorbell.pressed", func(_ *events.Event) {
				liveVideo.Start("doorbell.pressed")
			})
			// Auto-activación de cámara/micro de la app: no genera INVITE SIP,
			// solo canal-up nativo en el bus OWN (ver 2026-08-22-media-arm-on-channel-up).
			eventBus.Subscribe("media.channel.up", func(_ *events.Event) {
				liveVideo.Start("media.channel.up")
			})
			eventBus.Subscribe("call.hangup", func(_ *events.Event) {
				// Our headless session ends via sip.call.ended; the bus
				// heuristic also matches its setup frames - ignore those.
				if sipClient != nil && sipClient.CameraSessionActive() {
					return
				}
				liveVideo.Stop()
			})
			eventBus.Subscribe("sip.call.incoming", func(_ *events.Event) {
				liveVideo.Start("sip.call.incoming")
			})
			eventBus.Subscribe("sip.call.connected", func(_ *events.Event) {
				liveVideo.StartTapOnly("sip.call.connected")
			})
			eventBus.Subscribe("sip.call.ended", func(_ *events.Event) {
				liveVideo.Stop()
			})
			logger.Infof("   Live video on call: enabled (passive mirror lo:%d, zero commands)", nativePort)
		}

		// Audio en vivo de la puerta durante una llamada entrante, por espejo
		// PASIVO del leg de audio nativo (lo:5000, Speex ya casado con el códec
		// del relay) hacia el relay de audio del RTSP (10000). Espejo exacto de
		// liveVideo pero SIN CallActiveMarker: el audio no dispara self-INVITE,
		// es puramente pasivo. CERO comandos al bus. Fail-closed: nuevo, off por
		// defecto salvo que se active explícitamente (sdd 2026-08-21-b1-audio-to-ha).
		if cfg.Streaming.LiveAudioOnCall {
			audioTap := sip.NewLoopbackRTPTap(sip.NativeAudioRTPPort,
				sip.DefaultGStreamerConfig().AudioPort, logger)
			liveAudio := sip.NewLiveAudioOnCall(audioTap, logger)
			// Guard: mismo criterio que liveVideo — desarme automático si se
			// pierde el fin de llamada (heurístico OWN).
			liveAudio.SetGuardTimeout(5 * time.Minute)

			// Mismo armado que liveVideo: doorbell.pressed es el disparador
			// fiable en producción (monitor OWN); los sip.call.* se mantienen
			// por si el cliente SIP llega a registrarse. Start/Stop idempotentes.
			eventBus.Subscribe("doorbell.pressed", func(_ *events.Event) {
				liveAudio.Start("doorbell.pressed")
			})
			// Auto-activación de cámara/micro de la app: no genera INVITE SIP,
			// solo canal-up nativo en el bus OWN (ver 2026-08-22-media-arm-on-channel-up).
			eventBus.Subscribe("media.channel.up", func(_ *events.Event) {
				liveAudio.Start("media.channel.up")
			})
			eventBus.Subscribe("call.hangup", func(_ *events.Event) {
				liveAudio.Stop()
			})
			eventBus.Subscribe("sip.call.incoming", func(_ *events.Event) {
				liveAudio.Start("sip.call.incoming")
			})
			eventBus.Subscribe("sip.call.connected", func(_ *events.Event) {
				liveAudio.Start("sip.call.connected")
			})
			eventBus.Subscribe("sip.call.ended", func(_ *events.Event) {
				liveAudio.Stop()
			})
			logger.Info("   Live audio on call: habilitado (espejo pasivo lo:5000 → relay 10000)")
		}

		// Audio-listen en llamada: mismo patrón que el vídeo, espejo PASIVO
		// del leg de audio de bajada nativo (lo:5000, RTP Speex en claro —
		// ver NativeAudioRTPPort) decodificado a WAV en local. CERO comandos
		// al bus. Gateado por audio.listen_tap_enabled (fail-closed): sin el
		// flag, nada de esto se cablea (sdd 2026-08-21-audio-tap-5000).
		if cfg.Audio.ListenTapEnabled {
			const audioListenRelayPort = 11000 // dedicado, no colisiona con el relay de vídeo (10000/10002)
			const nativeAudioPayloadType = 110 // PT Speex del leg nativo (ver AudioProbe)
			tap := sip.NewLoopbackRTPTap(sip.NativeAudioRTPPort, audioListenRelayPort, logger)
			decoder := sip.NewAudioTapDecoder("/usr/local/bin/ffmpeg", logger)
			audioListen := sip.NewAudioListenOnCall(tap, decoder, audioListenRelayPort, nativeAudioPayloadType, cfg.Audio.ListenSinkPath, logger)
			// Guard: igual que liveVideo — si el fin de llamada se pierde
			// (heurístico OWN), el desarme automático evita que el tap/decoder
			// queden armados indefinidamente.
			audioListen.SetGuardTimeout(5 * time.Minute)

			// Mismo armado que liveVideo: doorbell.pressed es el disparador
			// fiable en producción (monitor OWN); los sip.call.* se mantienen
			// por si el cliente SIP llega a registrarse. Start/Stop idempotentes.
			eventBus.Subscribe("doorbell.pressed", func(_ *events.Event) {
				audioListen.Start("doorbell.pressed")
			})
			// Auto-activación de cámara/micro de la app: no genera INVITE SIP,
			// solo canal-up nativo en el bus OWN (ver 2026-08-22-media-arm-on-channel-up).
			eventBus.Subscribe("media.channel.up", func(_ *events.Event) {
				audioListen.Start("media.channel.up")
			})
			eventBus.Subscribe("call.hangup", func(_ *events.Event) {
				audioListen.Stop()
			})
			eventBus.Subscribe("sip.call.incoming", func(_ *events.Event) {
				audioListen.Start("sip.call.incoming")
			})
			eventBus.Subscribe("sip.call.connected", func(_ *events.Event) {
				audioListen.Start("sip.call.connected")
			})
			eventBus.Subscribe("sip.call.ended", func(_ *events.Event) {
				audioListen.Stop()
			})

			if webServer != nil {
				webServer.SetAudioListenController(audioListen)
				logger.Infof("   Audio-listen: POST/GET /api/audio/listen/{start,stop,status} (relay 127.0.0.1:%d)", audioListenRelayPort)
			}
			logger.Info("   Audio-listen (tap 5000) activado")
		}

		// Cooperative video probe (diagnostics): a single *7*300, no retry or
		// self-INVITE. Endpoint gated by POST + ?confirm=yes. Only wired if
		// there is an OpenWebNet client. It is safe (a single command) even when video
		// is disabled, hence it does NOT depend on VideoOnDemand.
		if webServer != nil && owClient != nil {
			ownForProbe := owClient
			webServer.SetVideoProbeFunc(func() (map[string]interface{}, []byte, error) {
				res, jpeg, err := sip.VideoProbe(ownForProbe, 8*time.Second, logger)
				if err != nil {
					return nil, nil, err
				}
				return map[string]interface{}{
					"ack":        res.Ack,
					"ack_error":  res.AckError,
					"frames":     res.Frames,
					"jpeg_bytes": res.JPEGBytes,
					"note":       res.Note,
				}, jpeg, nil
			})
			logger.Info("   Video probe: POST /api/video/probe?confirm=yes (diagnostic, single *7*300)")

			// Cooperative audio probe (diagnostics): a single type-2 *7*300,
			// measures incoming Speex RTP. Equally safe (a single command).
			webServer.SetAudioProbeFunc(func() (map[string]interface{}, error) {
				res, err := sip.AudioProbe(ownForProbe, 6*time.Second, logger)
				if err != nil {
					return nil, err
				}
				return map[string]interface{}{
					"ack":          res.Ack,
					"ack_error":    res.AckError,
					"packets":      res.Packets,
					"bytes":        res.Bytes,
					"payload_type": res.PayloadType,
					"note":         res.Note,
				}, nil
			})
			logger.Info("   Audio probe: POST /api/audio/probe?confirm=yes (diagnostic, audio-type *7*300)")
		}

		// Set recording path if configured
		if cfg.Streaming.RecordingPath != "" {
			rtspServer.SetRecordingPath(cfg.Streaming.RecordingPath)
			logger.Infof("   RTSP recording enabled: %s", cfg.Streaming.RecordingPath)
		}

		if err := rtspServer.Start(); err != nil {
			logger.WithError(err).Error("Failed to start RTSP server, continuing without RTSP")
			rtspServer = nil
		} else {
			logger.Infof("✅ RTSP: Enhanced server started on port %d", cfg.Streaming.RTSPPort)
			logger.Infof("   RTSP streams:")
			logger.Infof("     - rtsp://%s:%d/doorbell (Full stream)", deviceIP, cfg.Streaming.RTSPPort)
			logger.Infof("     - rtsp://%s:%d/doorbell-video (Video only)", deviceIP, cfg.Streaming.RTSPPort)
			logger.Infof("     - rtsp://%s:%d/doorbell-recorder (HKSV recording)", deviceIP, cfg.Streaming.RTSPPort)

			// On-demand JPEG snapshot service (cooperative *7*300 pattern,
			// no self-INVITE). Only wired if video activation is
			// enabled and there is an OpenWebNet client.
			if webServer != nil && owClient != nil && cfg.Streaming.VideoOnDemand {
				snapshotSvc := sip.NewSnapshotService(owClient, logger)
				webServer.SetSnapshotFunc(snapshotSvc.Capture)
				logger.Info("   Snapshot: JPEG captures available at GET /api/snapshot")

				// Doorbell camera in HA: when the doorbell rings / a call comes in,
				// the MQTT bridge captures a cooperative snapshot and publishes it.
				if mqttBridge != nil {
					mqttBridge.SetCameraCapture(func() ([]byte, error) {
						return snapshotSvc.Capture(12*time.Second, 0)
					})
					logger.Info("   Camera: doorbell camera entity enabled in Home Assistant")
				}
			}
		}

		// Create video stream manager only if SIP started AND on-demand video is
		// enabled. This component auto-starts video (*7*32) on doorbell press,
		// so for safety it is NOT created when video_on_demand is disabled.
		if sipStarted && sipClient != nil && cfg.Streaming.VideoOnDemand {
			videoManager = sip.NewVideoStreamManager(sipClient, owClient, eventBus, logger)

			if err := videoManager.Start(); err != nil {
				logger.WithError(err).Warn("Failed to start video manager")
				videoManager = nil
			} else {
				logger.Info("✅ Video: Stream manager started successfully")
			}
		}

		// Camera session switch (bridge-initiated headless video, off by default).
		if mqttBridge != nil && sipStarted && sipClient != nil && cfg.Streaming.CameraSession {
			mqttBridge.SetVideoSession(func(d time.Duration) error {
				return sipClient.StartCameraSession(d, "mqtt")
			}, sipClient.StopCameraSession)
			logger.Info("MQTT: camera session switch wired (native answers loopback self-call)")
		}

		// End-of-session stills come from the live relay flow: prefer the one
		// captured during the teardown grace (8s matches the proven
		// cooperative timeout as fallback).
		if sipStarted && sipClient != nil && rtspServer != nil {
			sipClient.SetFrameGrabber(func() []byte {
				if jpeg := rtspServer.TakePrefetchedStill(20 * time.Second); jpeg != nil {
					return jpeg
				}
				return rtspServer.SnapshotRelayFrame(8 * time.Second)
			})
		}
	} else {
		logger.Info("Video/SIP system disabled in configuration")
	}

	// ==================== COMPONENT 4b: SIP Answerer (contestador, F1) ====================
	// Fuera del `if cfg.SIP.Enabled { ... }` para que compile igual con SIP off;
	// el gate de más arriba ya garantiza Answerer.Enabled ⟹ SIP.Enabled.
	var answerer *sip.Answerer
	var talkSender *sip.TalkSender
	if cfg.SIP.Answerer.Enabled {
		// Route-check read-only (no bloquea el arranque; solo informa).
		if ok, err := sip.CheckAnswererRoute(cfg.SIP.Answerer.RouteFile, cfg.SIP.Answerer.User, cfg.SIP.Domain); err != nil {
			logger.Warnf("[ANSWERER] route-check no disponible (%v); ¿ejecutaste setup_answerer_route.sh?", err)
		} else if !ok {
			logger.Warnf("[ANSWERER] el AOR %s@%s NO está en route_ext.conf: no llegará el fork hasta darlo de alta", cfg.SIP.Answerer.User, cfg.SIP.Domain)
		}
		serverAddr := fmt.Sprintf("%s:%d", cfg.SIP.ServerHost, cfg.SIP.ServerPort) // 127.0.0.1:5060
		tr, err := sip.NewTCPTransport(serverAddr, logger)
		if err != nil {
			logger.Errorf("[ANSWERER] no se pudo conectar al Flexisip local (%s): %v — contestador deshabilitado", serverAddr, err)
		} else {
			answerer = sip.NewAnswerer(cfg.SIP.Answerer, cfg.SIP.Domain, tr, eventBus, logger)
			if err := answerer.Start(); err != nil {
				logger.Errorf("[ANSWERER] Start falló: %v", err)
				answerer = nil
			} else {
				if webServer != nil {
					webServer.SetAnswerer(answerer)
				} else {
					logger.Warn("[ANSWERER] web dashboard deshabilitado: sin API REST para contestar/colgar desde HA")
				}
				logger.Info("[ANSWERER] contestador SIP activo (bajo demanda desde HA)")

				// F2: talk-back desde HA (fail-closed por talk_enabled).
				if cfg.SIP.Answerer.TalkEnabled {
					ffmpegPath := "/usr/local/bin/ffmpeg" // ffmpeg estático del device (mismo binario que audio_sender/receiver/tap)
					talkSender = sip.NewTalkSender(cfg.SIP.Answerer, sip.NewExecRunner(), eventBus, logger,
						ffmpegPath, "127.0.0.1", 40004, 97)
					if err := talkSender.Start(); err != nil {
						logger.Errorf("[TALK] Start falló: %v", err)
						talkSender = nil
					} else {
						logger.Info("[TALK] talk-back activo (hablar desde HA durante la llamada)")
					}
				}
			}
		}
	}

	// ==================== COMPONENT 4c: PanelView ('Ver placa') ====================
	// Fail-closed: requiere streaming.panelview.enabled + DEVADDR, y SIP, OWN y RTSP vivos
	// (punteros nil dentro de una interfaz no serían nil: se evalúan aquí).
	if cfg.Streaming.PanelView.Enabled && cfg.Streaming.PanelView.DEVADDR != "" &&
		sipClient != nil && owClient != nil && rtspServer != nil {
		eyeReceiver := sip.NewAudioReceiver("/usr/local/bin/ffmpeg", logger) // modo forward (CaptureForward)
		eyeAudioPort := sip.DefaultGStreamerConfig().VideoPort + 102         // 10104: distinto del 10102 del AudioCall
		eyeVideoPort := sip.DefaultGStreamerConfig().VideoPort + 104         // 10106: vídeo del offer (no se consume)
		panelView := sip.NewPanelView(
			cfg.Streaming.PanelView,
			sipClient,
			eyeReceiver,
			&ownAdapter{client: owClient},
			rtspServer, // CallActiveChecker
			eventBus,
			"127.0.0.1",
			eyeAudioPort, eyeVideoPort,
			logger,
		)
		if err := panelView.Start(); err != nil {
			logger.WithError(err).Warn("panelview: no se pudo iniciar; deshabilitado")
		} else {
			logger.Info("panelview: 'Ver placa' activo")
		}
	}

	// ==================== COMPONENT 5: MQTT Bridge ====================
	if mqttBridge != nil {
		logger.Info("Starting MQTT Bridge for Home Assistant...")
		if err := mqttBridge.Start(ctx); err != nil {
			logger.WithError(err).Error("Failed to start MQTT bridge, continuing without MQTT")
			mqttBridge = nil
		} else {
			logger.Infof("MQTT: Bridge started for %s:%d", cfg.MQTT.Host, cfg.MQTT.Port)

			// Wire EventBus to MQTT bridge for event forwarding
			mqttBridge.SetEventBus(eventBus)
			logger.Info("MQTT: EventBus connected - events will be forwarded to MQTT")

			// Wire MessageParser to MQTT bridge for answering machine stats
			if messageParser != nil {
				mqttBridge.SetMessageParser(messageParser)
				logger.Info("MQTT: MessageParser connected - message stats will be published to HA")
			}

			// Wire CommandHandler for full 4-step voicemail protocol
			if commandHandler != nil {
				mqttBridge.SetCommandHandler(commandHandler)
				logger.Info("MQTT: CommandHandler connected - 4-step voicemail protocol enabled")
			}
		}
	} else {
		logger.Info("MQTT bridge disabled in configuration")
	}

	// Wire MQTT and LED status providers to web server for dashboard display
	if webServer != nil {
		if mqttBridge != nil {
			webServer.SetMQTTStatusProvider(mqttBridge)
			logger.Info("Web Dashboard: MQTT status provider connected")
		}
		webServer.SetLEDStatusFunc(readLEDStates)
		logger.Info("Web Dashboard: LED status reader connected")
	}

	// ==================== INITIAL DEVICE STATE QUERIES ====================
	// Query real device state at startup (voicemail, doorbell)
	if mqttBridge != nil {
		go func() {
			// The answers are published once, so they need the MQTT connection.
			time.Sleep(5 * time.Second)
			mqttBridge.WaitForConnection()
			logger.Info("Querying initial device state...")

			mqttBridge.QueryBellStatus()
			time.Sleep(bticino.CommandRetryDelay)

			if bticino.AnsweringMachineAvailable() {
				mqttBridge.QueryVoicemailStatus()
			}
			time.Sleep(bticino.CommandRetryDelay)

			mqttBridge.QuerySystemInfo()

			logger.Info("Initial state query completed")
		}()
	}

	// ==================== COMPONENT 5.5: Multicast Listener (239.255.76.67:7667) ====================
	logger.Info("Initializing multicast listener (BTicino syslog on 239.255.76.67:7667)...")
	multicastListener = multicast.NewMulticastListener(eventBus, logger)

	// Register OpenWebNet handler for OPEN system messages
	ownHandler := handlers.NewOpenWebNetHandler(eventBus, logger)
	multicastListener.RegisterHandler("OPEN", ownHandler)

	if err := multicastListener.Start(); err != nil {
		logger.WithError(err).Warn("Failed to start multicast listener (non-critical)")
		multicastListener = nil
	} else {
		logger.Info("Multicast: Listening on 239.255.76.67:7667 with OPEN handler")
	}

	// ==================== COMPONENT 5.6: UDP Proxy (40004 -> 4000) ====================
	var udpProxy *udpproxy.UDPProxy
	if cfg.UDPProxy.Enabled {
		logger.Infof("Initializing UDP Proxy: port %d -> %d...", cfg.UDPProxy.ListenPort, cfg.UDPProxy.TargetPort)
		udpProxy = udpproxy.New(cfg.UDPProxy.ListenPort, cfg.UDPProxy.TargetPort, logger)
		if err := udpProxy.Start(); err != nil {
			logger.WithError(err).Warn("Failed to start UDP Proxy (non-critical)")
			udpProxy = nil
		} else {
			logger.Infof("UDP Proxy: Forwarding %d -> %d", cfg.UDPProxy.ListenPort, cfg.UDPProxy.TargetPort)
		}
	} else {
		logger.Info("UDP Proxy disabled in configuration")
	}

	// ==================== COMPONENT 6: Input Monitor (Physical Buttons) ====================
	logger.Info("Initializing physical input monitor...")
	inputMonitor = input.NewInputMonitor(logger)

	// Set button press callback -> publish to EventBus and MQTT
	inputMonitor.OnButtonPress = func(evt input.ButtonEvent) {
		logger.WithFields(logrus.Fields{
			"key":    evt.KeyName,
			"action": evt.Action,
			"device": evt.Device,
		}).Info("Physical button event detected")

		// Map key codes to BTicino button names
		buttonName := ""
		switch evt.Key {
		case bticino.KeyCodeKey:
			buttonName = "key"
		case bticino.KeyCodeStar:
			buttonName = "star"
		case bticino.KeyCodeEye:
			buttonName = "eye"
		case bticino.KeyCodePhone:
			buttonName = "phone"
		default:
			buttonName = evt.KeyName
		}

		// Publish to EventBus
		eventBus.PublishWithSource("button.pressed", map[string]interface{}{
			"key_code":    evt.Key,
			"key_name":    buttonName,
			"action":      evt.Action,
			"device":      evt.Device,
			"raw_keyname": evt.KeyName,
		}, "input_monitor")

		// Direct MQTT publish for button press
		if mqttBridge != nil && evt.Action == "press" {
			mqttBridge.PublishButtonPress(evt.Key, buttonName, evt.Action)

			// Publish a human-readable description to the activity_log
			var actDesc string
			switch evt.Key {
			case bticino.KeyCodeKey:
				actDesc = "Key button pressed (open door)"
			case bticino.KeyCodeStar:
				actDesc = "Star button pressed (staircase light)"
			case bticino.KeyCodeEye:
				actDesc = "Eye button pressed (activate camera)"
			case bticino.KeyCodePhone:
				actDesc = "Phone button pressed (intercom)"
			default:
				actDesc = fmt.Sprintf("Button %s pressed", buttonName)
			}
			mqttBridge.PublishActivityLog(actDesc, "button_press", buttonName)
		}
	}

	// Set screen change callback -> publish to EventBus
	inputMonitor.OnScreenChange = func(evt input.ScreenEvent) {
		logger.WithFields(logrus.Fields{
			"gpio":  evt.GPIO,
			"state": evt.State,
		}).Info("Screen state change detected")

		eventBus.PublishWithSource("screen.changed", map[string]interface{}{
			"gpio":   evt.GPIO,
			"active": evt.State,
		}, "input_monitor")
	}

	// Set touch event callback -> log for device discovery
	inputMonitor.OnTouchEvent = func(evt input.TouchEvent) {
		logger.WithFields(logrus.Fields{
			"device":   evt.Device,
			"action":   evt.Action,
			"x":        evt.X,
			"y":        evt.Y,
			"pressure": evt.Pressure,
		}).Info("Touch event detected (discovery)")
	}

	if err := inputMonitor.Start(); err != nil {
		logger.WithError(err).Warn("Failed to start input monitor (may not have /dev/input access)")
		inputMonitor = nil
	} else {
		logger.Info("Input Monitor: Monitoring /dev/input/event0 (keypad), event1 (touch), event2 (gpio)")
	}

	// ==================== COMPONENT 7: OpenWebNet Event Monitor (Port 20000) ====================
	if owClient != nil {
		logger.Info("Setting up OpenWebNet bus event monitor on port 20000...")

		owClient.OnMessage(func(cmd *openwebnet.Command) {
			logger.WithFields(logrus.Fields{
				"raw":   cmd.Raw,
				"who":   cmd.WHO,
				"what":  cmd.WHAT,
				"where": cmd.WHERE,
			}).Info("OpenWebNet bus event received from port 20000")

			// Publish all bus events to EventBus
			eventBus.PublishWithSource("openwebnet.event", map[string]interface{}{
				"raw":   cmd.Raw,
				"who":   cmd.WHO,
				"what":  cmd.WHAT,
				"where": cmd.WHERE,
			}, "openwebnet_monitor")

			// Detect doorbell events specifically
			// Doorbell pattern: WHO=8, WHAT contains doorbell activation
			// Known doorbell command: *8*1#1#4#21*16##
			if cmd.Raw == bticino.CmdDoorbellPress || (cmd.WHO == "8" && strings.Contains(cmd.WHAT, "1#1#4#21")) {
				logger.Info("DOORBELL PRESS DETECTED from bus!")

				eventBus.PublishWithSource("doorbell.pressed", map[string]interface{}{
					"raw":    cmd.Raw,
					"source": "openwebnet_bus",
					"who":    cmd.WHO,
					"what":   cmd.WHAT,
					"where":  cmd.WHERE,
				}, "openwebnet_monitor")

				// Direct MQTT publish for doorbell
				if mqttBridge != nil {
					mqttBridge.PublishDoorbellEvent("openwebnet_bus", cmd.Raw)
				}
			}

			// Detect call end (hangup) on the bus: disarms live
			// video (live_on_call). Covers incoming and self calls.
			// Structural match (bticino.IsCallHangup): WHO=8 WHAT=3#…,
			// any WHERE (416/420 on 300X, 411 on 100X).
			if bticino.IsCallHangup(cmd.Raw) {
				eventBus.PublishWithSource("call.hangup", map[string]interface{}{
					"raw": cmd.Raw,
				}, "openwebnet_monitor")
			}

			// Only the 300X: the bridge's own camera sessions announce a media channel on
			// the 100X, and arming the tracks there has not been verified.
			if bticino.DeviceVariant == bticino.DeviceVariant300X && bticino.IsMediaChannelUp(cmd.Raw) {
				eventBus.PublishWithSource("media.channel.up", map[string]interface{}{
					"raw":   cmd.Raw,
					"who":   cmd.WHO,
					"what":  cmd.WHAT,
					"where": cmd.WHERE,
				}, "openwebnet_monitor")
			}

			// Detect real-time doorbell state changes (bus events)
			if cmd.Raw == "*#8**33*0##" {
				logger.Info("Doorbell state detected on bus: muted (OFF)")
				if mqttBridge != nil {
					mqttBridge.UpdateBellState(false)
				}
			} else if cmd.Raw == "*#8**33*1##" {
				logger.Info("Doorbell state detected on bus: unmuted (ON)")
				if mqttBridge != nil {
					mqttBridge.UpdateBellState(true)
				}
			}

			// Detect real-time answering machine state changes
			// The device sends *8*91*## or *8*92*## (with empty WHERE),
			// besides the WHERE-less form *8*91## / *8*92##.
			// We use the already-parsed WHO=8 + WHAT=91/92 to cover both variants.
			if cmd.WHO == "8" && cmd.WHAT == "91" {
				logger.Info("Answering machine enabled detected on bus")
				if mqttBridge != nil {
					mqttBridge.UpdateVoicemailState(true)
				}
			} else if cmd.WHO == "8" && cmd.WHAT == "92" {
				logger.Info("Answering machine disabled detected on bus")
				if mqttBridge != nil {
					mqttBridge.UpdateVoicemailState(false)
				}
			}
			// Also detect via *#8**40*... status response
			if cmd.WHO == "8" && strings.HasPrefix(cmd.Raw, "*#8**40*") {
				re := regexp.MustCompile(`\*#8\*\*40\*([01])\*`)
				if m := re.FindStringSubmatch(cmd.Raw); len(m) >= 2 {
					vmEnabled := m[1] == "1"
					logger.WithField("voicemail_enabled", vmEnabled).Info("Answering machine state detected on bus via WHO=8/40")
					if mqttBridge != nil {
						mqttBridge.UpdateVoicemailState(vmEnabled)
					}
				}
			}

			// Detect additional lock events (WHERE=21, 22)
			if cmd.WHO == "8" && (cmd.WHAT == "19" || cmd.WHAT == "20") {
				if cmd.WHERE != "20" && cmd.WHERE != "16" {
					// It is an additional lock (not the main WHERE=20 nor light WHERE=16)
					logger.WithField("where", cmd.WHERE).Infof("Additional lock event detected: WHAT=%s", cmd.WHAT)
					if mqttBridge != nil {
						if cmd.WHAT == "19" {
							mqttBridge.PublishAdditionalLockEvent(cmd.WHERE, "UNLOCKED")
						} else {
							mqttBridge.PublishAdditionalLockEvent(cmd.WHERE, "LOCKED")
						}
					}
				}
			}

			// Forward all events to MQTT
			if mqttBridge != nil {
				// OWN pattern recognition -> activity_log sensor
				if desc, ok := bticino.OWNEventDescriptions[cmd.Raw]; ok {
					eventType := "unknown"
					if et, ok2 := bticino.OWNEventTypes[cmd.Raw]; ok2 {
						eventType = et
					}
					mqttBridge.PublishActivityLog(desc, eventType, cmd.Raw)
				}
			}
		})

		logger.Info("OpenWebNet: Bus event monitor active on port 20000 - doorbell detection enabled")
	}

	// ==================== STARTUP COMPLETE ====================
	logger.Info("===== BTicino Bridge v" + versionpkg.GetVersion() + " STARTUP COMPLETE =====")
	logger.Info("Active Components:")

	if owClient != nil {
		logger.Info("  OpenWebNet: Connected (port 20000 monitor + port 30006 commands)")
		if commandHandler != nil {
			logger.Info("  Command Handler: Active (50+ BTicino commands)")
		}
	}

	if messageParser != nil {
		messages, err := messageParser.GetAllMessages()
		if err != nil {
			logger.WithError(err).Warn("  Message Parser: Error reading messages")
		} else {
			logger.Infof("  Message Parser: Active (%d messages found)", len(messages))
		}
	}

	if mqttBridge != nil {
		state := "connected"
		if !mqttBridge.IsConnected() {
			state = "not connected yet, retrying"
		}
		logger.Infof("  MQTT: %s, %s:%d (39+%d HA entities, 8+%d command topics)", state, cfg.MQTT.Host, cfg.MQTT.Port, len(cfg.AdditionalLocks), len(cfg.AdditionalLocks))
	}

	if multicastListener != nil {
		logger.Info("  Multicast: Listening on 239.255.76.67:7667 (OPEN handler)")
	}

	if inputMonitor != nil {
		logger.Info("  Input Monitor: Active (keypad buttons, touchscreen, 13 GPIO pins)")
	}

	if udpProxy != nil {
		logger.Infof("  UDP Proxy: Forwarding port %d -> %d", cfg.UDPProxy.ListenPort, cfg.UDPProxy.TargetPort)
	}

	if webServer != nil {
		logger.Infof("  Web Dashboard: http://%s:%d", deviceIP, cfg.Web.Port)
	}

	if homekitBridge != nil {
		logger.Infof("  HomeKit: Port %s, PIN: %s", cfg.HomeKit.Port, cfg.HomeKit.Pin)
	}

	if sipClient != nil {
		logger.Infof("  SIP/Video: %s:%d", cfg.SIP.ServerHost, cfg.SIP.ServerPort)
	}

	logger.Info("  EventBus: Active (component coordination)")
	logger.Info("===================================================")

	// Physical device monitoring goroutine (GPIO/LED monitoring + MQTT publishing)
	// Track previous states for change detection
	var prevLEDStates map[string]bool
	var prevGPIOStates map[int]bool
	var tickCount int
	go func() {
		physicalMonitorTicker := time.NewTicker(30 * time.Second)
		defer physicalMonitorTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-physicalMonitorTicker.C:
				tickCount++
				// Monitor real BTicino device physical status
				monitorFields := logrus.Fields{
					"physical_monitoring": true,
				}

				// Check LED status (real BTicino device paths) — with change detection
				if ledStatus := checkLEDStatus(); ledStatus != "" {
					monitorFields["led_status"] = ledStatus
				}
				currentLEDs := readLEDStates()
				if prevLEDStates != nil {
					for name, on := range currentLEDs {
						if prevOn, exists := prevLEDStates[name]; exists && prevOn != on {
							logger.WithFields(logrus.Fields{
								"led":  name,
								"from": boolToOnOff(prevOn),
								"to":   boolToOnOff(on),
							}).Info("LED state changed")
						}
					}
				}
				prevLEDStates = currentLEDs

				// Check GPIO status — with individual pin change detection
				if gpioStatus := checkGPIOStatus(); gpioStatus != "" {
					monitorFields["gpio_status"] = gpioStatus
				}
				if inputMonitor != nil {
					currentGPIO := inputMonitor.GetCurrentGPIOStates()
					if prevGPIOStates != nil {
						for pin, on := range currentGPIO {
							if prevOn, exists := prevGPIOStates[pin]; exists && prevOn != on {
								logger.WithFields(logrus.Fields{
									"gpio_pin": pin,
									"from":     boolToOnOff(prevOn),
									"to":       boolToOnOff(on),
								}).Info("GPIO pin state changed (periodic check)")
							}
						}
					}
					prevGPIOStates = currentGPIO

					// Publish GPIO states to MQTT individually
					if mqttBridge != nil && len(currentGPIO) > 0 {
						logger.WithField("gpio", currentGPIO).Info("Publishing GPIO states to MQTT")
						mqttBridge.PublishGPIOStates(currentGPIO)

						// Broadcast GPIO states to web UI via SSE
						if ws := webserver.GetGlobalWebServer(); ws != nil {
							ws.BroadcastEvent("gpio", map[string]interface{}{"gpio": currentGPIO})
						}
					}
				}

				// Check thermal status and publish to MQTT
				if thermalTemp := checkThermalStatus(); thermalTemp != "" {
					monitorFields["device_temperature"] = thermalTemp

					// Publish temperature to MQTT
					if mqttBridge != nil {
						var tempMilli int
						data, err := os.ReadFile(bticino.ThermalZonePath)
						if err == nil {
							tempStr := strings.TrimSpace(string(data))
							if _, err := fmt.Sscanf(tempStr, "%d", &tempMilli); err == nil {
								mqttBridge.PublishTemperature(float64(tempMilli) / 1000.0)
							}
						}
					}
				}

				// Check LED status and publish to MQTT
				if mqttBridge != nil && len(currentLEDs) > 0 {
					logger.WithField("leds", currentLEDs).Debug("Publishing LED states to MQTT")
					mqttBridge.PublishLEDStatus(currentLEDs)

					// Broadcast LED states to web UI via SSE
					if ws := webserver.GetGlobalWebServer(); ws != nil {
						ws.BroadcastEvent("leds", map[string]interface{}{"leds": currentLEDs})
					}
				}

				// Publish answering machine message stats to MQTT
				if mqttBridge != nil {
					mqttBridge.PublishMessageStats()
				}

				// One liveness probe per cycle feeds both SIP status and
				// diagnostics. The bell query used here before (*#8**33##) has
				// its answer echoed on the monitor port and matched in the
				// activity-log table, so every poll looked like a mute event.
				if mqttBridge != nil {
					_, err := mqttBridge.ExecuteCommand(bticino.CmdSystemStatus)
					alive := err == nil
					mqttBridge.PublishSIPStatus(alive)
					if alive {
						monitorFields["sip_status"] = "registered"
						mqttBridge.PublishSystemDiagnostics("ok")
						monitorFields["system_diag"] = "ok"
					} else {
						monitorFields["sip_status"] = "unregistered"
						mqttBridge.PublishSystemDiagnostics("error")
						monitorFields["system_diag"] = "error"
					}
				}

				// Query device state every 5 minutes (every 10 ticks of 30s)
				if mqttBridge != nil && tickCount%10 == 0 {
					go func() {
						mqttBridge.QueryBellStatus()
						time.Sleep(bticino.CommandRetryDelay)
						if bticino.AnsweringMachineAvailable() {
							mqttBridge.QueryVoicemailStatus()
						}
					}()
				}

				// Check filesystem status for messages directory
				if fsStatus := checkBTicinoFilesystem(); fsStatus != "" {
					monitorFields["filesystem_status"] = fsStatus
				}

				logger.WithFields(monitorFields).Debug("Physical device monitoring")
			}
		}
	}()

	// Status monitoring goroutine
	go func() {
		statusTicker := time.NewTicker(5 * time.Minute)
		defer statusTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-statusTicker.C:
				logFields := logrus.Fields{
					"mega_bridge_version": versionpkg.GetVersion(),
				}

				// OpenWebNet stats
				if owClient != nil {
					logFields["openwebnet_connected"] = owClient.IsConnected()
					logFields["openwebnet_ports"] = "20000,30006,30007"
				}

				// Web server stats
				if webServer != nil {
					logFields["web_server_enabled"] = true
					logFields["web_server_port"] = cfg.Web.Port
					logFields["web_dashboard_url"] = fmt.Sprintf("http://%s:%d", deviceIP, cfg.Web.Port)
				}

				// HomeKit stats
				if homekitBridge != nil {
					homekitStats := homekitBridge.GetStats()
					logFields["homekit_events_processed"] = homekitStats.EventsProcessed
					logFields["homekit_accessories"] = homekitStats.AccessoriesCount
					logFields["homekit_running_since"] = time.Since(homekitStats.StartTime).Round(time.Second).String()
				}

				// Video/SIP stats
				if sipClient != nil {
					logFields["sip_registered"] = sipClient.IsRegistered()
					logFields["sip_call_state"] = sipClient.GetCallState().String()
				}
				if videoManager != nil {
					videoStats := videoManager.GetStats()
					logFields["video_active_streams"] = videoStats["active_streams"]
					logFields["video_rtp_port"] = videoStats["rtp_port"]
				}
				if rtspServer != nil {
					rtspStats := rtspServer.GetStats()
					logFields["rtsp_active_sessions"] = rtspStats["active_sessions"]
					logFields["rtsp_port"] = rtspStats["port"]
				}

				logger.WithFields(logFields).Info("BTicino Bridge Status")
			}
		}
	}()

	logger.Info("🚀 BTicino Bridge is fully operational. Press Ctrl+C to stop...")
	if webServer != nil {
		logger.Infof("🌐 Access the web dashboard at: http://%s:%d", deviceIP, cfg.Web.Port)
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logger.Infof("Received signal %v, shutting down BTicino Bridge...", sig)
	case <-ctx.Done():
		logger.Info("Context cancelled, shutting down BTicino Bridge...")
	}

	// ==================== GRACEFUL SHUTDOWN ====================
	logger.Info("🔄 Graceful shutdown of BTicino Bridge...")

	// Stop components in reverse order
	cancel()

	// Stop RTSP server
	if rtspServer != nil {
		logger.Info("Stopping RTSP server...")
		if err := rtspServer.Stop(); err != nil {
			logger.WithError(err).Error("Error stopping RTSP server")
		} else {
			logger.Info("✅ RTSP server stopped")
		}
	}

	// Stop video manager
	if videoManager != nil {
		logger.Info("Stopping video stream manager...")
		if err := videoManager.Stop(); err != nil {
			logger.WithError(err).Error("Error stopping video manager")
		} else {
			logger.Info("✅ Video manager stopped")
		}
	}

	// Stop talk-back (F2) antes que el contestador, para no dejar un ffmpeg
	// huérfano si el answerer cae primero.
	if talkSender != nil {
		logger.Info("Stopping talk-back sender...")
		talkSender.Stop()
		logger.Info("✅ Talk-back sender stopped")
	}

	// Stop SIP answerer (antes que el cliente SIP, para que nuestra pata mande
	// BYE primero si hay una llamada del contestador en curso).
	if answerer != nil {
		logger.Info("Stopping SIP answerer...")
		if err := answerer.Stop(); err != nil {
			logger.WithError(err).Error("Error stopping SIP answerer")
		} else {
			logger.Info("✅ SIP answerer stopped")
		}
	}

	// Stop SIP client
	if sipClient != nil {
		logger.Info("Stopping SIP client...")
		if err := sipClient.Stop(); err != nil {
			logger.WithError(err).Error("Error stopping SIP client")
		} else {
			logger.Info("✅ SIP client stopped")
		}
	}

	// Stop HomeKit bridge
	if homekitBridge != nil {
		logger.Info("Stopping HomeKit bridge...")
		if err := homekitBridge.Stop(); err != nil {
			logger.WithError(err).Error("Error stopping HomeKit bridge")
		} else {
			logger.Info("✅ HomeKit bridge stopped")
		}
	}

	// Stop web server
	if webServer != nil {
		logger.Info("Stopping web server...")
		if err := webServer.Stop(); err != nil {
			logger.WithError(err).Error("Error stopping web server")
		} else {
			logger.Info("Web server stopped")
		}
	}

	// Stop input monitor
	if inputMonitor != nil {
		logger.Info("Stopping input monitor...")
		if err := inputMonitor.Stop(); err != nil {
			logger.WithError(err).Error("Error stopping input monitor")
		} else {
			logger.Info("Input monitor stopped")
		}
	}

	// Stop MQTT bridge
	if mqttBridge != nil {
		logger.Info("Stopping MQTT bridge...")
		mqttBridge.Stop()
		logger.Info("MQTT bridge stopped")
	}

	// Stop multicast listener
	if multicastListener != nil {
		logger.Info("Stopping multicast listener...")
		if err := multicastListener.Stop(); err != nil {
			logger.WithError(err).Error("Error stopping multicast listener")
		} else {
			logger.Info("Multicast listener stopped")
		}
	}

	// Stop UDP proxy
	if udpProxy != nil {
		logger.Info("Stopping UDP Proxy...")
		if err := udpProxy.Stop(); err != nil {
			logger.WithError(err).Error("Error stopping UDP Proxy")
		} else {
			logger.Info("UDP Proxy stopped")
		}
	}

	// Stop OpenWebNet client
	if owClient != nil {
		logger.Info("Closing OpenWebNet connections...")
		owClient.Disconnect()
		logger.Info("✅ OpenWebNet client stopped")
	}

	logger.Info("🎉 ===== BTicino Bridge SHUTDOWN COMPLETE =====")
	logger.Info("Session Summary:")
	logger.Info("  - Enhanced OpenWebNet with 80+ commands: " + func() string {
		if owClient != nil {
			return "✅ ACTIVE"
		} else {
			return "❌ DISABLED"
		}
	}())
	logger.Info("  - Web Dashboard Interface: " + func() string {
		if webServer != nil {
			return "✅ ACTIVE"
		} else {
			return "❌ DISABLED"
		}
	}())
	logger.Info("  - HomeKit Apple Home Bridge: " + func() string {
		if homekitBridge != nil {
			return "✅ ACTIVE"
		} else {
			return "❌ DISABLED"
		}
	}())
	logger.Info("  - Video/SIP H.264 Streaming: " + func() string {
		if sipClient != nil || videoManager != nil || rtspServer != nil {
			return "✅ ACTIVE"
		} else {
			return "❌ DISABLED"
		}
	}())

	logger.Info("🏁 BTicino Bridge v" + versionpkg.GetVersion() + " shutdown successful!")
}

// Physical device monitoring functions for real BTicino hardware
func checkLEDStatus() string {
	// Read real LED brightness values from /sys/class/leds/*/brightness
	if _, err := os.Stat(bticino.LEDsPath); os.IsNotExist(err) {
		return "leds_unavailable"
	}

	entries, err := os.ReadDir(bticino.LEDsPath)
	if err != nil {
		return "leds_error"
	}

	active := 0
	for _, entry := range entries {
		brightnessPath := fmt.Sprintf("%s/%s/brightness", bticino.LEDsPath, entry.Name())
		data, err := os.ReadFile(brightnessPath)
		if err == nil {
			val := strings.TrimSpace(string(data))
			if val != "0" && val != "" {
				active++
			}
		}
	}
	return fmt.Sprintf("leds_ok: %d/%d active", active, len(entries))
}

func checkGPIOStatus() string {
	// Read real GPIO values from /sys/class/gpio/*/value
	if _, err := os.Stat(bticino.GPIOPath); os.IsNotExist(err) {
		return "gpio_unavailable"
	}

	entries, err := os.ReadDir(bticino.GPIOPath)
	if err != nil {
		return "gpio_error"
	}

	gpioCount := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "gpio") {
			gpioCount++
		}
	}
	return fmt.Sprintf("gpio_ok: %d pins exported", gpioCount)
}

func checkThermalStatus() string {
	// Read actual temperature from /sys/class/thermal/thermal_zone0/temp
	data, err := os.ReadFile(bticino.ThermalZonePath)
	if err != nil {
		return "thermal_unavailable"
	}

	tempStr := strings.TrimSpace(string(data))
	// Value is in millidegrees, convert to degrees
	var tempMilli int
	if _, err := fmt.Sscanf(tempStr, "%d", &tempMilli); err == nil {
		return fmt.Sprintf("%.1f C", float64(tempMilli)/1000.0)
	}
	return "thermal_read_error"
}

func checkBTicinoFilesystem() string {
	// Check BTicino filesystem paths availability
	paths := []string{
		bticino.MessagesDir,
		bticino.DeviceConfigXML,
		bticino.SystemConfigXML,
	}

	availableCount := 0
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			availableCount++
		}
	}

	if availableCount == len(paths) {
		return "full_integration_available"
	} else if availableCount > 0 {
		return fmt.Sprintf("partial_integration_%d_of_%d", availableCount, len(paths))
	}

	return "filesystem_unavailable"
}

// readLEDStates reads the brightness of all LEDs and returns a map of name -> on/off
func readLEDStates() map[string]bool {
	result := make(map[string]bool)

	if _, err := os.Stat(bticino.LEDsPath); os.IsNotExist(err) {
		return result
	}

	entries, err := os.ReadDir(bticino.LEDsPath)
	if err != nil {
		return result
	}

	for _, entry := range entries {
		brightnessPath := fmt.Sprintf("%s/%s/brightness", bticino.LEDsPath, entry.Name())
		data, err := os.ReadFile(brightnessPath)
		if err == nil {
			val := strings.TrimSpace(string(data))
			result[entry.Name()] = val != "0" && val != ""
		}
	}
	return result
}

// boolToOnOff converts a boolean to "ON"/"OFF" for logging
func boolToOnOff(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}
