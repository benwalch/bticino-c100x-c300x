package config

import (
	"fmt"
	"os"
	"time"

	"bticino_bridge/pkg/version"
	"gopkg.in/yaml.v2"
)

// Config represents the complete bridge configuration
type Config struct {
	Bridge          BridgeConfig        `yaml:"bridge"`
	OpenWebNet      OpenWebNetConfig    `yaml:"openwebnet"`
	SIP             SIPConfig           `yaml:"sip"`
	MQTT            MQTTConfig          `yaml:"mqtt"`
	HomeKit         HomeKitConfig       `yaml:"homekit"`
	Hardware        HardwareConfig      `yaml:"hardware"`
	Web             WebConfig           `yaml:"web"`
	Logging         LoggingConfig       `yaml:"logging"`
	AdditionalLocks []AdditionalLock    `yaml:"additional_locks"`
	UDPProxy        UDPProxyConfig      `yaml:"udp_proxy"`
	Streaming       StreamingConfig     `yaml:"streaming"`
	Network         NetworkConfig       `yaml:"network,omitempty"`
	Servers         ServersConfig       `yaml:"servers,omitempty"`
	Notifications   NotificationsConfig `yaml:"notifications,omitempty"`
	Privacy         PrivacyConfig       `yaml:"privacy,omitempty"`
	Security        SecurityConfig      `yaml:"security,omitempty"`
	Access          AccessConfig        `yaml:"access,omitempty"`
	Audio           AudioConfig         `yaml:"audio,omitempty"`
}

type BridgeConfig struct {
	Name     string `yaml:"name"`
	Version  string `yaml:"version"`
	LogLevel string `yaml:"log_level"`
}

type OpenWebNetConfig struct {
	Host          string        `yaml:"host"`
	Port          int           `yaml:"port"`
	Timeout       time.Duration `yaml:"timeout"`
	RetryAttempts int           `yaml:"retry_attempts"`
	RetryDelay    time.Duration `yaml:"retry_delay"`
}

type SIPConfig struct {
	Enabled    bool   `yaml:"enabled"`
	LocalHost  string `yaml:"local_host"`
	LocalPort  int    `yaml:"local_port"`
	ServerHost string `yaml:"server_host"`
	ServerPort int    `yaml:"server_port"`
	Transport  string `yaml:"transport"`
	Domain     string `yaml:"domain"`
	AuthFile   string `yaml:"auth_file"`
	CertFile   string `yaml:"cert_file"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	DevAddr    string `yaml:"dev_addr"`
	// NativeDevAddr is the unit address in session SDP (a=DEVADDR,
	// distinct from DevAddr below, the numeric WHO address).
	NativeDevAddr string `yaml:"native_dev_addr"`
	UseHA1        bool   `yaml:"use_ha1"`
	InsecureTLS   bool   `yaml:"insecure_tls"`
	SIPTarget     string `yaml:"sip_target"` // INVITE target user override (default: answerer per DeviceVariant)

	// Answerer: subsistema contestador SIP (F1). Fail-closed: omitido => Enabled=false.
	Answerer AnswererConfig `yaml:"answerer,omitempty"`
}

// AnswererConfig configura el contestador SIP (el bridge como parte de red del fork del timbre).
type AnswererConfig struct {
	Enabled        bool   `yaml:"enabled"`          // master switch (F1), fail-closed
	User           string `yaml:"user"`             // AOR: <user>@<sip.domain>; default "bridge"
	AudioPort      int    `yaml:"audio_port"`       // puerto de drenaje audio; default 10100
	VideoPort      int    `yaml:"video_port"`       // puerto de drenaje vídeo (-1 = audio-only, rechaza m=video); 0/omitido = default 10102
	MaxCallSeconds int    `yaml:"max_call_seconds"` // guard anti-placa-ocupada; default 120
	// RingTimeoutSeconds: guarda de timbre. Si la llamada sigue en RINGING (nadie contesta y
	// el CANCEL de la placa no llega), se cierra con 480 al vencer. Default 70 (app 1.17.3).
	RingTimeoutSeconds int  `yaml:"ring_timeout_seconds"`
	TalkEnabled        bool `yaml:"talk_enabled"` // F2 (hablar desde HA): lanza el TalkSender; fail-closed
	// TalkKeepalive: subida de audio continua (silencio speex cuando el micro de HA calla) para
	// que la placa abra el audio de bajada (hipótesis RTP simétrico). Requiere TalkEnabled. Fail-closed.
	TalkKeepalive bool   `yaml:"talk_keepalive"`
	RouteFile     string `yaml:"route_file"` // ruta de route_ext.conf para el route-check; default abajo
}

type MQTTConfig struct {
	Enabled     bool       `yaml:"enabled"`
	Host        string     `yaml:"host"`
	Port        int        `yaml:"port"`
	Broker      string     `yaml:"broker"` // For backward compatibility
	ClientID    string     `yaml:"client_id"`
	Username    string     `yaml:"username"`
	Password    string     `yaml:"password"`
	TopicPrefix string     `yaml:"topic_prefix"`
	Topics      MQTTTopics `yaml:"topics"`
	// EnableSystemButtons exposes restart buttons in HA (Configuration section):
	// restarting the bridge and rebooting the device. DISABLED by
	// default because they are powerful actions (reboot restarts the whole unit).
	EnableSystemButtons bool `yaml:"enable_system_buttons"`
}

type MQTTTopics struct {
	// Events FROM hardware TO MQTT
	DoorEvents     string `yaml:"door_events"`
	ButtonEvents   string `yaml:"button_events"`
	SIPEvents      string `yaml:"sip_events"`
	HardwareEvents string `yaml:"hardware_events"`
	SystemStatus   string `yaml:"system_status"`

	// Commands FROM MQTT TO hardware
	DoorCommands   string `yaml:"door_commands"`
	SystemCommands string `yaml:"system_commands"`
	SIPCommands    string `yaml:"sip_commands"`

	// HomeKit integration topics
	HomeKitEvents  string `yaml:"homekit_events"`
	HomeKitStatus  string `yaml:"homekit_status"`
	HomeKitPairing string `yaml:"homekit_pairing"`

	// Compatibility with existing MQTT scripts
	RX  string `yaml:"rx"`  // Commands TO BTicino
	TX  string `yaml:"tx"`  // Events FROM BTicino
	Key string `yaml:"key"` // Key events
}

type HardwareConfig struct {
	Enabled         bool          `yaml:"enabled"`
	InputDevice     string        `yaml:"input_device"`
	GPIOMonitoring  bool          `yaml:"gpio_monitoring"`
	I2CScanning     bool          `yaml:"i2c_scanning"`
	PollingInterval time.Duration `yaml:"polling_interval"`
}

type WebConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Port         int    `yaml:"port"`
	StaticDir    string `yaml:"static_dir"`
	TemplatesDir string `yaml:"templates_dir"`
}

type HomeKitConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Name         string `yaml:"name"`
	Manufacturer string `yaml:"manufacturer"`
	Model        string `yaml:"model"`
	Port         string `yaml:"port"`
	Pin          string `yaml:"pin"`
	StoragePath  string `yaml:"storage_path"`
}

type LoggingConfig struct {
	Level    string `yaml:"level"`
	File     string `yaml:"file"`
	MaxSize  string `yaml:"max_size"`
	MaxFiles int    `yaml:"max_files"`
}

// AdditionalLock represents an extra configurable lock.
// Each lock has a unique WHERE address on the OpenWebNet bus.
// Example YAML config:
//
//	additional_locks:
//	  - name: "Back Door"
//	    where: 21
//	  - name: "Side Door"
//	    where: 22
type AdditionalLock struct {
	Name  string `yaml:"name"`  // Human-readable name (e.g. "Back Door")
	Where int    `yaml:"where"` // WHERE address on OWN bus (e.g. 21, 22)
}

// UDPProxyConfig configures the UDP proxy that forwards packets from port 40004 to 4000.
// This lets external clients (like the BTicino mobile app) talk
// to the device through the bridge.
type UDPProxyConfig struct {
	Enabled    bool `yaml:"enabled"`
	ListenPort int  `yaml:"listen_port"` // Listen port (default: 40004)
	TargetPort int  `yaml:"target_port"` // Target port (default: 4000)
}

// StreamingConfig configures RTSP/WebRTC streaming
type StreamingConfig struct {
	Enabled              bool   `yaml:"enabled"`
	RTSPPort             int    `yaml:"rtsp_port"`
	WebRTCEnabled        bool   `yaml:"webrtc_enabled"`
	WebRCTPPort          int    `yaml:"webrtc_port"`
	RecordingPath        string `yaml:"recording_path"`
	MaxDuration          int    `yaml:"max_duration"`
	AutoStopOnLastClient bool   `yaml:"auto_stop_on_last_client"`
	// VideoBackend selects how the camera video is obtained:
	//   "avmedia"   → *7*300 command asking bt_av_media to duplicate its RTP
	//                 (cooperative with the native firmware; touches port 30007)
	//   "gstreamer" → direct GStreamer pipeline capturing /dev/video0 with the VPU
	//                 (competes with native processes; may fail on cold start)
	//   "auto"      → tries avmedia and, if no RTP flows, falls back to gstreamer
	VideoBackend string `yaml:"video_backend"`
	// VideoOnDemand enables on-demand video ACTIVATION: self-INVITE
	// from the RTSP server, automatic video start on doorbell press and
	// snapshots. DISABLED by default for safety: enabling video competes
	// with the native firmware and, if anything retries, may reboot the unit.
	// With false, none of those paths sends commands to the native device.
	VideoOnDemand bool `yaml:"video_on_demand"`
	// LiveOnCall enables live video during an incoming call to
	// our unit via a PASSIVE mirror (tap of the native video leg on
	// lo:<native-port> → relay; 5007 high-res on c300x, 5002 low-res on
	// c100x, selected via bticino.DeviceVariant). ZERO commands to the bus: even safer than the cooperative
	// snapshot. Independent of VideoOnDemand.
	LiveOnCall bool `yaml:"live_on_call"`
	// CameraSession exposes a bridge-initiated headless video session
	// (loopback self-INVITE answered natively, display stays off) as an
	// HA switch. Off by default; duration in seconds.
	CameraSession         bool `yaml:"camera_session"`
	CameraSessionDuration int  `yaml:"camera_session_duration"`
	// LiveAudioOnCall habilita el audio en vivo de la puerta durante una
	// llamada, mediante el mismo espejo PASIVO que LiveOnCall pero del leg de
	// audio nativo (lo:5000 → relay 10000, sin CallActiveMarker: el audio no
	// dispara self-INVITE). Fail-closed: a diferencia de LiveOnCall (vídeo,
	// probado en producción), es una feature nueva — NO se defaultea a true
	// ni aquí ni en GetStreamingConfig(); requiere el campo explícito en el
	// YAML (sdd 2026-08-21-b1-audio-to-ha).
	LiveAudioOnCall bool `yaml:"live_audio_on_call"`
	// PanelView configura "Ver placa" (ojo/autoaccensione a demanda). Fail-closed.
	PanelView PanelViewConfig `yaml:"panelview,omitempty"`
}

// PanelViewConfig configura "Ver placa" (ojo/autoaccensione a demanda). Fail-closed.
type PanelViewConfig struct {
	Enabled        bool   `yaml:"enabled"`         // si false, el subsistema no se instancia
	DEVADDR        string `yaml:"devaddr"`         // dirección OWN de la placa; vacío ⇒ no dispara
	TimeoutSeconds int    `yaml:"timeout_seconds"` // auto-off (default 30)
	CallerUser     string `yaml:"caller_user"`     // identidad From del INVITE (default "webrtc")
	SendWho8Arm    bool   `yaml:"send_who8_arm"`   // enviar *8*2#5#4*16## tras el ACK (default false)
}

// Supported video backends
const (
	VideoBackendAVMedia   = "avmedia"
	VideoBackendGStreamer = "gstreamer"
	VideoBackendAuto      = "auto"
)

// GetStreamingConfig returns default streaming config
func GetStreamingConfig() StreamingConfig {
	return StreamingConfig{
		Enabled:               true,
		RTSPPort:              6554,
		WebRTCEnabled:         false,
		WebRCTPPort:           8889,
		MaxDuration:           60,
		AutoStopOnLastClient:  true,
		VideoBackend:          VideoBackendAVMedia,
		LiveOnCall:            true,
		CameraSession:         false,
		CameraSessionDuration: 30,
	}
}

// NetworkConfig configures networking and external servers
type NetworkConfig struct {
	NTP      NTPConfig      `yaml:"ntp"`
	DNS      DNSConfig      `yaml:"dns"`
	Firewall FirewallConfig `yaml:"firewall"`
}

type NTPConfig struct {
	Enabled bool   `yaml:"enabled"`
	Server  string `yaml:"server"`
	Port    int    `yaml:"port"`
}

type DNSConfig struct {
	Servers []string `yaml:"servers"`
}

type FirewallConfig struct {
	BlockTelemetry bool `yaml:"block_telemetry"`
	BlockCloud     bool `yaml:"block_cloud"`
	AllowUpdates   bool `yaml:"allow_updates"`
}

// ServersConfig configures the official external servers
type ServersConfig struct {
	Cloud       CloudServerConfig       `yaml:"cloud"`
	Logging     LogServerConfig         `yaml:"logging"`
	Updates     UpdatesServerConfig     `yaml:"updates"`
	SIPOfficial SIPOfficialServerConfig `yaml:"sip_official"`
}

type CloudServerConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Protocol    string `yaml:"protocol"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

type LogServerConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Protocol    string `yaml:"protocol"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

type UpdatesServerConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Protocol    string `yaml:"protocol"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

type SIPOfficialServerConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	Protocol    string `yaml:"protocol"`
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
}

// NotificationsConfig configures notifications
type NotificationsConfig struct {
	MQTT     MQTTNotificationConfig     `yaml:"mqtt"`
	Email    EmailNotificationConfig    `yaml:"email"`
	Pushover PushoverNotificationConfig `yaml:"pushover"`
	Telegram TelegramNotificationConfig `yaml:"telegram"`
}

type MQTTNotificationConfig struct {
	Enabled bool   `yaml:"enabled"`
	Topic   string `yaml:"topic"`
}

type EmailNotificationConfig struct {
	Enabled    bool   `yaml:"enabled"`
	SMTPServer string `yaml:"smtp_server"`
	SMTPPort   int    `yaml:"smtp_port"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	From       string `yaml:"from"`
	To         string `yaml:"to"`
}

type PushoverNotificationConfig struct {
	Enabled bool   `yaml:"enabled"`
	APIKey  string `yaml:"api_key"`
	UserKey string `yaml:"user_key"`
}

type TelegramNotificationConfig struct {
	Enabled  bool   `yaml:"enabled"`
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
}

// PrivacyConfig configures privacy
type PrivacyConfig struct {
	BlockExternalTelemetry bool `yaml:"block_external_telemetry"`
	BlockLogServer         bool `yaml:"block_log_server"`
	BlockCloud             bool `yaml:"block_cloud"`
	DisableAutoUpdates     bool `yaml:"disable_auto_updates"`
	LocalLogging           bool `yaml:"local_logging"`
}

// SecurityConfig configures security
type SecurityConfig struct {
	// WebAuthRequired protects the whole web server (UI + API) with Basic Auth.
	// Requires a non-empty WebPassword; when enabled without a password the server
	// replies 503 (fail-closed, never silently open).
	WebAuthRequired bool   `yaml:"web_auth_required"`
	WebUsername     string `yaml:"web_username"`
	WebPassword     string `yaml:"web_password"`
	WebHTTPSEnabled bool   `yaml:"web_https_enabled"`
	APIRateLimit    int    `yaml:"api_rate_limit"`
	ConfigAuditLog  bool   `yaml:"config_audit_log"`
}

// AccessConfig configura el clasificador de accesos (apertura de portal →
// telefonillo/llave). Solo escucha el EventBus; no envía nada al bus.
type AccessConfig struct {
	Enabled            bool          `yaml:"enabled"`
	CorrelationWindow  time.Duration `yaml:"correlation_window"`   // apertura ≤ esto tras timbre/llamada → telefonillo
	MaxHoldWait        time.Duration `yaml:"max_hold_wait"`        // espera al cierre para medir la duración del pulso
	DurationVisitorMax time.Duration `yaml:"duration_visitor_max"` // umbral de confianza por duración
}

// AudioConfig gobierna el audio full-duplex de la pata propia del bridge.
type AudioConfig struct {
	DuplexEnabled  bool   `yaml:"duplex_enabled"`   // fail-closed: NO auto-default
	DuplexSinkPath string `yaml:"duplex_sink_path"` // sink WAV para validación local

	// ListenTapEnabled arma el espejo pasivo del audio de bajada nativo (tap
	// lo:5000, sdd 2026-08-21-audio-tap-5000). Fail-closed: NO auto-default,
	// igual que DuplexEnabled — si se omite el campo en el YAML queda false.
	ListenTapEnabled bool `yaml:"listen_tap_enabled"`

	// ListenSinkPath es el sink WAV DEDICADO del oído pasivo (tap lo:5000).
	// Distinto de DuplexSinkPath (door_audio.wav) para no compartir archivo
	// con la pata duplex, que sigue cableada en paralelo (revisión final de
	// rama feat/audio-tap-5000, hallazgo 2): escribir ambos al mismo WAV
	// haría que uno pisara al otro si llegan a coincidir en el tiempo. Con
	// auto-default si se omite (no fail-closed, como DuplexSinkPath).
	ListenSinkPath string `yaml:"listen_sink_path"`
}

// LoadConfig loads configuration from YAML file
func LoadConfig(configPath string) (*Config, error) {
	config := &Config{}

	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(config); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	// Set defaults if not specified
	if config.OpenWebNet.Host == "" {
		config.OpenWebNet.Host = "localhost"
	}
	if config.OpenWebNet.Port == 0 {
		config.OpenWebNet.Port = 30006
	}
	if config.Streaming.RTSPPort == 0 {
		config.Streaming.RTSPPort = 6554
	}
	// PanelView: solo defaults de timeout y caller; Enabled y DEVADDR quedan
	// vacíos/false (fail-closed).
	if config.Streaming.PanelView.TimeoutSeconds == 0 {
		config.Streaming.PanelView.TimeoutSeconds = 30
	}
	if config.Streaming.PanelView.CallerUser == "" {
		config.Streaming.PanelView.CallerUser = "webrtc"
	}
	if config.MQTT.ClientID == "" {
		config.MQTT.ClientID = "bticino_bridge"
	}
	if config.Access.CorrelationWindow == 0 {
		config.Access.CorrelationWindow = 30 * time.Second
	}
	if config.Access.MaxHoldWait == 0 {
		config.Access.MaxHoldWait = 8 * time.Second
	}
	if config.Access.DurationVisitorMax == 0 {
		config.Access.DurationVisitorMax = 3500 * time.Millisecond
	}
	if config.Audio.DuplexSinkPath == "" {
		config.Audio.DuplexSinkPath = "/home/bticino/cfg/extra/recordings/door_audio.wav"
	}
	if config.Audio.ListenSinkPath == "" {
		config.Audio.ListenSinkPath = "/home/bticino/cfg/extra/recordings/door_audio_listen.wav"
	}

	// Defaults del answerer (no fuerzan Enabled; solo rellenan campos a cero).
	if config.SIP.Answerer.User == "" {
		config.SIP.Answerer.User = "bridge"
	}
	if config.SIP.Answerer.AudioPort == 0 {
		config.SIP.Answerer.AudioPort = 10100
	}
	if config.SIP.Answerer.VideoPort == 0 {
		config.SIP.Answerer.VideoPort = 10102
	}
	if config.SIP.Answerer.MaxCallSeconds == 0 {
		config.SIP.Answerer.MaxCallSeconds = 120
	}
	if config.SIP.Answerer.RingTimeoutSeconds == 0 {
		config.SIP.Answerer.RingTimeoutSeconds = 70
	}
	if config.SIP.Answerer.RouteFile == "" {
		config.SIP.Answerer.RouteFile = "/etc/flexisip/users/route_ext.conf"
	}

	return config, nil
}

// GetDefaultConfig returns a configuration with sensible defaults
func GetDefaultConfig() *Config {
	// Initialize version system
	version.InitVersion()

	return &Config{
		Bridge: BridgeConfig{
			Name:     "BTicino Bridge",
			Version:  version.GetVersion(),
			LogLevel: "info",
		},
		OpenWebNet: OpenWebNetConfig{
			Host:          "localhost",
			Port:          30006,
			Timeout:       5 * time.Second,
			RetryAttempts: 3,
			RetryDelay:    1 * time.Second,
		},
		SIP: SIPConfig{
			Enabled:    true,
			LocalHost:  "127.0.0.1",
			LocalPort:  47300,
			ServerHost: "sipserver.bs.iotleg.com",
			ServerPort: 5061,
			Transport:  "tls",
			Domain:     "bs.iotleg.com",
		},
		MQTT: MQTTConfig{
			Enabled:     true,
			Host:        "localhost",
			Port:        1883,
			Broker:      "localhost:1883", // For backward compatibility
			ClientID:    "bticino_bridge",
			TopicPrefix: "bticino",
			Topics: MQTTTopics{
				DoorEvents:     "bticino/events/door",
				ButtonEvents:   "bticino/events/buttons",
				SIPEvents:      "bticino/events/sip",
				HardwareEvents: "bticino/events/hardware",
				SystemStatus:   "bticino/status/system",
				DoorCommands:   "bticino/commands/door",
				SystemCommands: "bticino/commands/system",
				SIPCommands:    "bticino/commands/sip",
				HomeKitEvents:  "bticino/homekit/events",
				HomeKitStatus:  "bticino/homekit/status",
				HomeKitPairing: "bticino/homekit/pairing",
				RX:             "Bticino/rx",
				TX:             "Bticino/tx",
				Key:            "Bticino/key",
			},
		},
		HomeKit: HomeKitConfig{
			Enabled:      true,
			Name:         "BTicino Bridge",
			Manufacturer: "BTicino",
			Model:        "Class 300X",
			Port:         "8080",
			Pin:          "12345678",
			StoragePath:  "./homekit_data",
		},
		Hardware: HardwareConfig{
			Enabled:         true,
			InputDevice:     "/dev/input/event0",
			GPIOMonitoring:  true,
			I2CScanning:     false,
			PollingInterval: 100 * time.Millisecond,
		},
		Web: WebConfig{
			Enabled:      true,
			Port:         8080,
			StaticDir:    "web/static",
			TemplatesDir: "web/templates",
		},
		Logging: LoggingConfig{
			Level:    "info",
			File:     "/var/log/bticino_bridge.log",
			MaxSize:  "10MB",
			MaxFiles: 5,
		},
		UDPProxy: UDPProxyConfig{
			Enabled:    false,
			ListenPort: 40004,
			TargetPort: 4000,
		},
		Streaming: StreamingConfig{
			Enabled:              true,
			RTSPPort:             6554,
			WebRTCEnabled:        false,
			WebRCTPPort:          8889,
			RecordingPath:        "/home/bticino/cfg/extra/recordings",
			MaxDuration:          60,
			AutoStopOnLastClient: true,
		},
	}
}
