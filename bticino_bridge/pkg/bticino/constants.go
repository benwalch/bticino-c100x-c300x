// Package bticino contains BTicino-specific constants, paths and commands
// derived from real analysis of the Classe 300X device (ARM7 firmware).
//
// All information in this file was verified through physical testing
// on the device during March 2026. OWN patterns,
// GPIO functions, button mappings and LED meanings were confirmed
// by observing real events on the OpenWebNet bus (port 20000) and on
// /dev/input/event0-2.
package bticino

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// BTicino Real Device Filesystem Paths
const (
	// Messages Directory (Answering Machine)
	MessagesDir       = "/home/bticino/cfg/extra/47/messages/"
	MessageDirPattern = "message_"
	MessageInfoFile   = "msg_info.ini"
	MessageImageFile  = "aswm.jpg"
	MessageVideoFile  = "aswm.avi"

	// Configuration Files
	DeviceConfigXML    = "/home/bticino/sp/dbfiles_ws.xml"
	SystemConfigXML    = "/var/tmp/conf.xml"
	StackOpenXML       = "/var/tmp/stack_open.xml"
	LicenseVersionFile = "/home/bticino/cfg/extra/.license_ver"
	FirmwareMetaXML    = "/home/bticino/cfg/extra/FW/meta.xml"

	// System Monitoring
	LEDsPath         = "/sys/class/leds"
	GPIOPath         = "/sys/class/gpio"
	InputEventDevice = "/dev/input/event0"
	TTYSerialDevice  = "/dev/ttymxc2"
	ThermalZonePath  = "/sys/class/thermal/thermal_zone0/temp"

	// Input devices (/dev/input/)
	InputDeviceKeypad      = "/dev/input/event0" // I2C_KB_TOUCH: physical keypad (4 buttons)
	InputDeviceTouchscreen = "/dev/input/event1" // TSC2005: touchscreen
	InputDeviceGPIOKeys    = "/dev/input/event2" // gpio-keys: GPIO keys

	// Answering Machine Temp Storage
	AnsweringMachineTemp = "/var/tmp/segreteria"

	// Network and Ports
	OpenWebNetMainPort   = 20000
	OpenWebNetLocalPort  = 30006
	OpenWebNetConfigPort = 30007
	SIPServerPort        = 5060
)

// ==================== OpenWebNet COMMANDS (verified on real device) ====================

const (
	// --- Answering machine (Voicemail / Answering Machine) ---
	CmdVoicemailOn          = "*8*91##"                 // Enable answering machine
	CmdVoicemailOff         = "*8*92##"                 // Disable answering machine
	CmdVoicemailOnApp       = "*#8**40*1*0*9815*1*25##" // Enable via mobile app protocol
	CmdVoicemailOffApp      = "*#8**40*0*0*9815*1*25##" // Disable via mobile app protocol
	CmdVoicemailStatusQuery = "*#8**40##"               // Query answering machine status (resp: *#8**40*VM*WM*XXXX*X*XX##)

	// --- Audio state (DIM 35, WHO=8) ---
	// Note: *#8**35*V1*V2*V3## is audio state, NOT voicemail.
	// V1=active channel (0=idle, 1=starting, 3=connecting, 12=channel 12 active), V2 and V3 = additional parameters
	CmdVoicemailStatus = "*#8**35*0*0*0##"  // DEPRECATED: wrong name, use CmdAudioStatusIdle
	CmdAudioStatusIdle = "*#8**35*0*0*0##"  // Audio idle (total silence)
	CmdAudioStatusInit = "*#8**35*1*0*0##"  // Audio channel 1: starting session — NEW 2026-03-12
	CmdAudioStatusConn = "*#8**35*3*0*0##"  // Audio channel 3: connecting/negotiating — NEW 2026-03-12
	CmdAudioStatusBusy = "*#8**35*12*0*0##" // Audio active on channel 12

	// --- Audio control (captured in real session) ---
	CmdAudioControl416 = "*8*3#5#4*416##" // Audio channel 416 control (hangup/call end)

	// --- Audio mode *8*87#N*## (WHO=8, WHAT=87#N) --- NEW 2026-03-12
	// Discovered during a test session while browsing the device menus.
	// Controls the system audio mode (active audio codec/channel).
	CmdAudioModeOff = "*8*87#0*##" // Audio OFF / idle (appears when stopping video/call)
	CmdAudioMode1   = "*8*87#1*##" // Audio mode 1: intercom / monitoring
	CmdAudioMode2   = "*8*87#2*##" // Audio mode 2: internal call
	CmdAudioMode4   = "*8*87#4*##" // Audio mode 4: Professional Studio
	CmdAudioMode5   = "*8*87#5*##" // Audio mode 5: video call (with active video stream)

	// --- Audio channel configuration (DIM 100#C and 101#C#S, WHO=8) --- NEW 2026-03-12
	// Discovered during a test session. Configures SIP/VoIP codec/audio parameters.
	// DIM 100#C: *#8**100#C*P1*P2*...## — Channel C with parameters
	// DIM 101#C#S: *#8**101#C#S*V## — Channel C, sub-parameter S, value V (0 or 1)
	// Real example: *#8**100#3*1*2## — Channel 3: params 1, 2
	// Real example: *#8**101#3#1*1## — Channel 3, sub-param 1 = 1

	// --- WiFi (WHO=8, WHAT=101#N) --- NEW 2026-03-12
	// Discovered when enabling/disabling WiFi from the device menu.
	CmdWiFiOff = "*8*101#0*##" // WiFi disabled (WHO=8, WHAT=101#0)
	CmdWiFiOn  = "*8*101#1*##" // WiFi enabled (WHO=8, WHAT=101#1)

	// --- MUTE ---
	CmdMuteOn  = "*8*30*20##" // Enable mute (no audio)
	CmdMuteOff = "*8*31*20##" // Disable mute

	// --- Doorbell sound (corrected per slyoldfox: # before 33) ---
	CmdBellOn    = "*#8**#33*1##" // Enable doorbell sound (SET dimension 33 = 1)
	CmdBellOff   = "*#8**#33*0##" // Disable doorbell sound (SET dimension 33 = 0)
	CmdBellQuery = "*#8**33##"    // Query doorbell status (resp: *#8**33*0## muted or *#8**33*1## unmuted)

	// --- Lock / door ---
	// Require press + 1s wait + release pattern
	CmdDoorOpenPress      = "*8*19*20##"  // Press door button (key)
	CmdDoorOpenRelease    = "*8*20*20##"  // Release door button
	CmdDoorOpenPress116   = "*8*19*116##" // Press lock button WHERE=116 — NEW 2026-03-12
	CmdDoorOpenRelease116 = "*8*20*116##" // Release lock button WHERE=116 — NEW 2026-03-12
	CmdLightOnPress       = "*8*21*16##"  // Press staircase light button (star)
	CmdLightOnRelease     = "*8*22*16##"  // Release staircase light button

	// --- Display / screen ---
	CmdDisplayOn  = "*7*73#1#100*##" // Turn display on (max brightness 100%)
	CmdDisplayDim = "*7*73#1#10*##"  // Dim display (10% brightness, screensaver mode)
	CmdDisplayOff = "*7*73#1#10*##"  // DEPRECATED: wrong name, does not turn off but dims. Use CmdDisplayDim

	// --- System state ---
	CmdStatusRequest = "*#130**1##" // Request system status
	CmdACK           = "*#*1##"     // Acknowledgment
	CmdNACK          = "*#*0##"     // Negative acknowledgment

	// --- Doorbell events (detected on bus port 20000) ---
	CmdDoorbellPress = "*8*1#1#4#21*16##" // Call from outdoor panel (doorbell)

	// --- Video/call events (captured in real sessions) ---
	CmdSelfCallCamera    = "*8*1#5#4#20*16##" // Self-call: eye button activates camera
	CmdAnswerPickup      = "*8*2#5#4*16##"    // Pick up: enable two-way audio
	CmdHangupIncoming    = "*8*3#1#4*416##"   // Hang up incoming call (WHERE=416)
	CmdHangupSelf        = "*8*3#5#4*416##"   // Hang up self-call (WHERE=416)
	CmdHangupSelf420     = "*8*3#5#4*420##"   // Hang up self-call (WHERE=420, variant) — NEW 2026-03-12
	CmdHangupIncoming411 = "*8*3#1#4*411##"   // Hang up incoming call (WHERE=411, Classe 100X — seen on answered and unanswered rings 2026-10-11)
	CmdRingIncoming      = "*8*9#1#4*20##"    // Doorbell notification (incoming call)
	CmdRingSelf          = "*8*9#5#4*20##"    // Self-activation notification (eye)
	CmdVoiceActive       = "*8*40#1#4*20##"   // Voice active on incoming call
	CmdIntercomActive    = "*8*100#5#4*20##"  // Two-way intercom active
	CmdSessionCleanup    = "*8*94#11#0*##"    // Session cleanup (channels 11, 0)
	CmdSessionCleanup23  = "*8*94#2#3*##"     // Session cleanup (channels 2, 3) — NEW 2026-03-12
	CmdSessionEnd        = "*8*96#9#0*##"     // Full session end

	// --- Video (WHO=7, captured in real sessions) ---
	CmdVideoDisplayOn  = "*7*73#1#100*##" // Turn display on (= CmdDisplayOn)
	CmdVideoDisplayOff = "*7*73#0#0*##"   // Turn display off (variant)
	CmdVideoStop       = "*7*55*##"       // Stop video
	CmdVideoDisconnect = "*7*219*##"      // Disconnect video
	CmdVideoStopFinal  = "*7*0*##"        // Stop video (final)
	CmdVideoKeepalive  = "*7*74*##"       // Keepalive during conversation
	CmdVideoTwoWay     = "*7*72*20##"     // Two-way video mode
	CmdVideoMode220    = "*7*220#0*##"    // Video display mode 220 (appears when activating camera) — NEW 2026-03-12

	// --- Video streams (WHO=7, WHAT=35/36/50) --- NEW 2026-03-12
	// Discovered during a test session when opening the camera and Professional Studio.
	// *7*35#T#S*## — Start video stream type T, source S
	// *7*36*## — Stop video stream
	// *7*50#1*## — Start camera/video (direct activation)
	CmdVideoStreamStart41 = "*7*35#4#1*##"  // Start stream type 4 source 1 (video call)
	CmdVideoStreamStart32 = "*7*35#3#2**##" // Start stream type 3 source 2 (note: double * before ##)
	CmdVideoStreamStop    = "*7*36*##"      // Stop video stream
	CmdVideoCameraStart   = "*7*50#1*##"    // Start camera/video (direct activation)

	// --- Camera picture settings (DIM 5/6/7/20, WHO=7) --- NEW 2026-03-12
	// Discovered when touching brightness/contrast/color settings in the camera view.
	// Dimension read format: *#7**DIM*VALUE##
	// DIM 5 = Brightness. Range 0-100. Example: *#7**5*40## = 40% brightness
	// DIM 6 = Contrast. Range 0-100. Example: *#7**6*50## = 50% contrast
	// DIM 7 = Color/Saturation. Range 0-100. Example: *#7**7*50## = 50% color
	// DIM 20 = Full picture config (WHERE=20). Format: *#7**20*BR*CO*CL*P4*P5##
	//          BR=brightness, CO=contrast, CL=color, P4=unknown (75), P5=unknown (50)
	//          Real example: *#7**20*40*50*50*75*50## = brightness 40, contrast 50, color 50

	// --- Volume control (DIM 31#1, WHO=7) --- NEW 2026-03-12
	// *#7**31#1*VALUE## — Current volume value (0-100)
	// Real example: *#7**31#1*80## = volume at 80%

	// --- System info (WHO=13, verified in slyoldfox c300x-controller) ---
	// These queries use dimension read: *#13**DIM##
	// Response: *#13**DIM*value1*value2*...##
	// On localhost (netcat 30006) they do not require HMAC authentication.
	CmdQueryIP      = "*#13**10##" // IP address (resp: *#13**10*192*0*2*10##)
	CmdQueryNetmask = "*#13**11##" // Netmask (resp: *#13**11*255*255*255*0##)
	CmdQueryMAC     = "*#13**12##" // MAC address (resp: *#13**12*D1*D2*D3*D4*D5*D6## in decimal)
	CmdQueryFWVer   = "*#13**16##" // Firmware version (resp: *#13**16*V1*V2*V3##)
	CmdQueryHWVer   = "*#13**17##" // Hardware version (resp: *#13**17*V1*V2*V3##)
	CmdQueryKernel  = "*#13**23##" // Kernel version (resp: *#13**23*V1*V2*V3##)
	CmdQueryDistro  = "*#13**24##" // Distro version (resp: *#13**24*V1*V2*V3##)

	// --- System state WHO=130 --- NEW 2026-03-12
	// *#130**1*2## — System state response (WHO=130, WHERE=1)
	CmdSystemStatus = "*#130**1*2##" // System state response (value 2)
)

// MQTT Topics for Home Assistant Integration
const (
	// Base topic prefix
	MQTTTopicPrefix = "video_intercom"

	// State topics
	TopicLockState      = "video_intercom/lock/state"
	TopicDisplayState   = "video_intercom/display/state"
	TopicVoicemailState = "video_intercom/voicemail/state"
	TopicDoorbellSound  = "video_intercom/doorbellsound/state"
	TopicDoorbellState  = "video_intercom/doorbell/state"
	TopicKeypadState    = "video_intercom/keypad/state"
	TopicLEDsState      = "video_intercom/leds/state"
	TopicGPIOState      = "video_intercom/gpio/state"

	// Command topics
	TopicLockSet          = "video_intercom/lock/set"
	TopicVoicemailSet     = "video_intercom/voicemail/set"
	TopicDoorbellSoundSet = "video_intercom/doorbellsound/set"

	// Message management command topics
	TopicMessagesRefresh     = "video_intercom/messages/commands/refresh"
	TopicMessagesMarkRead    = "video_intercom/messages/commands/mark_read"
	TopicMessagesDelete      = "video_intercom/messages/commands/delete"
	TopicMessagesMarkAllRead = "video_intercom/messages/commands/mark_all_read"
	TopicMessagesDownload    = "video_intercom/messages/commands/download"

	// Availability topic
	TopicAvailability = "video_intercom/state"

	// Home Assistant Discovery Topics
	HADiscoveryPrefix       = "homeassistant"
	HALockConfig            = "homeassistant/lock/intercom/door/config"
	HADisplayConfig         = "homeassistant/sensor/intercom/display/config"
	HAVoicemailConfig       = "homeassistant/switch/intercom/voicemail/config"
	HADoorbellSoundConfig   = "homeassistant/switch/intercom/doorbellsound/config"
	HADoorbellTriggerConfig = "homeassistant/device_automation/intercom/doorbell/config"
	HAKeypadConfig          = "homeassistant/event/intercom/keypad/config"
)

// ==================== PHYSICAL BUTTONS (verified on /dev/input/event0) ====================
//
// The physical keypad is an I2C_KB_TOUCH connected to /dev/input/event0.
// Each button generates an EV_KEY event with the following codes.
//
// | Button     | Code | Physical function                   | Generated OWN command                          |
// |------------|------|-------------------------------------|------------------------------------------------|
// | Key        | 2    | Open door (lock)                    | *8*19*20## + *8*20*20## (press+release)        |
// | Star       | 3    | Turn on staircase light             | *8*21*16## + *8*22*16## (press+release)        |
// | Eye        | 4    | Activate camera (see+hear)          | Starts *8*1#5#4#20*16## sequence               |
// | Phone      | 5    | Two-way communication (mic)         | *8*2#5#4*16## (only with active camera)        |
//
// Notes:
// - Long key press keeps the door relay open longer
// - Eye activates the camera ~30 seconds, then it turns off automatically
// - Phone only works when the camera is already active (via eye or incoming call)
const (
	KeyCodeKey   = 2 // Key: open door
	KeyCodeStar  = 3 // Star: staircase light
	KeyCodeEye   = 4 // Eye: activate camera
	KeyCodePhone = 5 // Phone: two-way communication

	EventTypeKeypress = 1
	EventValuePress   = 1
	EventValueRelease = 0
)

// KeyNames maps key codes to descriptive names in English
var KeyNames = map[int]string{
	KeyCodeKey:   "key",
	KeyCodeStar:  "star",
	KeyCodeEye:   "eye",
	KeyCodePhone: "phone",
}

// Message Types for Answering Machine
const (
	MessageTypeVoice  = "voice_message"
	MessageTypeDoor   = "door_event"
	MessageTypeSystem = "system_message"
)

// BTicino Device Information
const (
	DeviceManufacturer = "Bticino-Legrand"

	// DeviceVariant selects which indoor unit this bridge runs on.
	// Change to "c300x" for a Classe 300X, "c100x" for a Classe 100X,
	// then rebuild. It switches model-specific tables (LEDs, …).
	DeviceVariant = "c100x"

	// Supported variants (valid values for DeviceVariant).
	DeviceVariant300X = "c300x"
	DeviceVariant100X = "c100x"

	// Network settings
	DefaultNetworkInterface = "lo"

	// SIP Configuration
	SIPDomain = "bs.iotleg.com"
	SIPServer = "sipserver.bs.iotleg.com"

	// MyHome Web Portal
	MyHomePortal     = "www.myhomeweb.com"
	MyHomePortalPort = 25100
	MyHomeHTTPSPort  = 443
)

// Timeouts and Intervals
const (
	OpenWebNetTimeout      = 30 * time.Second
	CommandRetryDelay      = 310 * time.Millisecond // Critical: do not change from 0.31s
	StatusPollingInterval  = 5 * time.Minute
	HealthCheckInterval    = 1 * time.Minute
	MessageRefreshInterval = 30 * time.Second
)

// ==================== DECODED OWN PATTERNS (captured from real events) ====================
//
// OpenWebNet command structure for video door phone (WHO=8, WHO=7, WHO=13, WHO=130):
//
// WHO=8 (Intercom/Video door phone):
//   *8*WHAT*WHERE##  (normal command)
//   *#8**DIM*VAL##   (dimension read/status)
//   *#8**#DIM*VAL##  (dimension write/set — note the extra #)
//
//   WHAT encodes the action:
//     1#X#Y#Z  = Call (X=1 incoming, X=5 self-activation; Y#Z = source)
//     2#X#Y    = Pick up/answer
//     3#X#Y    = Hang up
//     9#X#Y    = Doorbell notification
//     19       = Press door button (press)
//     20       = Release door button (release)
//     21       = Press staircase light button (press)
//     22       = Release staircase light button (release)
//     30       = Mute ON
//     31       = Mute OFF
//     40#X#Y   = Voice active on call
//     87#N     = Audio mode (N: 0=off, 1=intercom, 2=internal, 4=studio, 5=video) [NEW]
//     91       = Answering machine ON
//     92       = Answering machine OFF
//     94#X#Y   = Session cleanup (X#Y = channels)
//     96#X#Y   = Session end
//     100#X#Y  = Active intercom
//     101#N    = WiFi (N: 0=off, 1=on) [NEW]
//
//   DIM (WHO=8 dimensions):
//     33       = Doorbell sound (read: *#8**33## / write: *#8**#33*V##, V: 0=muted, 1=unmuted)
//     35       = Audio state (*#8**35*CH*P1*P2##, CH: 0=idle, 1=starting, 3=connecting, 12=active)
//     40       = Answering machine state (*#8**40*VM*WM*XXXX*X*XX##, VM=voicemail, WM=welcome msg)
//     100#C    = Audio channel C config (*#8**100#C*P1*P2*...##) [NEW]
//     101#C#S  = Sub-param S channel C (*#8**101#C#S*V##, V: 0 or 1) [NEW]
//
// WHO=7 (Video/Multimedia):
//   *7*WHAT*WHERE##
//     0   = Stop video (final)
//     35#T#S = Start video stream type T source S [NEW]
//     36  = Stop video stream [NEW]
//     50#1 = Start camera/video (direct activation) [NEW]
//     55  = Stop video
//     72  = Two-way video mode
//     73#B#V = Display control (B=1 turn on; V=brightness percent 0-100)
//     74  = Video keepalive
//     77#... = Video resolution parameters (800x480, etc.)
//     219 = Disconnect video
//     220#M = Video display mode (M=0 normal) [NEW]
//     300#IP#PORT#T = RTP stream start (IP, port, type)
//
//   DIM (WHO=7 dimensions):
//     5    = Picture brightness (*#7**5*VALUE##, VALUE: 0-100%) [NEW]
//     6    = Picture contrast (*#7**6*VALUE##, VALUE: 0-100%) [NEW]
//     7    = Picture color/saturation (*#7**7*VALUE##, VALUE: 0-100%) [NEW]
//     20   = Full picture config (*#7**20*BR*CO*CL*P4*P5##) [NEW]
//            BR=brightness, CO=contrast, CL=color, P4/P5=unknown (75/50)
//     31#1 = Volume (*#7**31#1*VALUE##, VALUE: 0-100%) [NEW]
//
// WHO=13 (System Info):
//   *#13**DIM## -> *#13**DIM*V1*V2*...##
//     10 = IP, 11 = Netmask, 12 = MAC, 16 = FW, 17 = HW, 23 = Kernel, 24 = Distro
//
// WHO=130 (System State):
//   *#130**WHERE*VALUE## — General system state
//
// Note on empty WHERE: the device sometimes sends an empty WHERE (trailing *)
// Example: *8*91*## instead of *8*91##. Both are equivalent.

// CommandResponses maps known OWN commands to their readable description
var CommandResponses = map[string]string{
	// Display
	"*7*73#1#100*##": "display ON (brightness 100%)",
	"*7*73#1#10*##":  "display dimmed (brightness 10%, screensaver)",
	"*7*73#0#0*##":   "display OFF (variant)",
	// Video
	"*7*55*##":      "video stopped",
	"*7*219*##":     "video disconnected",
	"*7*0*##":       "video stopped (final)",
	"*7*74*##":      "video keepalive",
	"*7*72*20##":    "two-way video",
	"*7*220#0*##":   "video display mode (normal)",
	"*7*35#4#1*##":  "video stream type 4 source 1 (video call)",
	"*7*35#3#2**##": "video stream type 3 source 2",
	"*7*36*##":      "video stream stopped",
	"*7*50#1*##":    "camera/video started",
	// Doorbell sound
	"*#8**33*0##": "doorbell sound OFF",
	"*#8**33*1##": "doorbell sound ON",
	// Answering machine
	"*8*92##":                 "answering machine OFF",
	"*8*91##":                 "answering machine ON",
	"*#8**40*0*0*9815*1*25##": "answering machine OFF (app)",
	"*#8**40*1*0*9815*1*25##": "answering machine ON (app)",
	// Audio state (DIM 35)
	"*#8**35*0*0*0##":  "audio idle (muted)",
	"*#8**35*1*0*0##":  "audio starting session (channel 1)",
	"*#8**35*3*0*0##":  "audio connecting/negotiating (channel 3)",
	"*#8**35*12*0*0##": "audio active channel 12",
	// Audio modes (WHAT=87#N)
	"*8*87#0*##": "audio mode OFF / idle",
	"*8*87#1*##": "audio mode 1 (intercom)",
	"*8*87#2*##": "audio mode 2 (internal call)",
	"*8*87#4*##": "audio mode 4 (Professional Studio)",
	"*8*87#5*##": "audio mode 5 (video call)",
	// WiFi
	"*8*101#0*##": "WiFi disabled",
	"*8*101#1*##": "WiFi enabled",
	// Door
	"*8*19*20##": "door: button pressed (press)",
	"*8*20*20##": "door: button released (release)",
	// Staircase light
	"*8*21*16##": "staircase light: button pressed (press)",
	"*8*22*16##": "staircase light: button released (release)",
	// Mute
	"*8*30*20##": "mute ON",
	"*8*31*20##": "mute OFF",
	// Calls and sessions
	"*8*1#1#4#21*16##": "incoming call from entrance panel (doorbell)",
	"*8*1#5#4#20*16##": "self-call: camera activated (eye button)",
	"*8*2#5#4*16##":    "two-way communication enabled (phone button)",
	"*8*9#1#4*20##":    "doorbell notification (incoming call)",
	"*8*9#5#4*20##":    "self-activation notification (eye)",
	"*8*3#1#4*416##":   "incoming call ended (hangup)",
	"*8*3#1#4*411##":   "incoming call ended (hangup, WHERE=411, Classe 100X)",
	"*8*3#5#4*416##":   "self-call ended (hangup)",
	"*8*3#5#4*420##":   "self-call ended (hangup, WHERE=420 variant)",
	"*8*40#1#4*20##":   "voice active on incoming call",
	"*8*100#5#4*20##":  "two-way intercom active",
	"*8*94#11#0*##":    "session cleanup (channels 11, 0)",
	"*8*94#2#3*##":     "session cleanup (channels 2, 3)",
	"*8*96#9#0*##":     "full session end",
	// ACK/NACK
	"*#*1##": "ACK",
	"*#*0##": "NACK",
	// State
	"*#130**1##":   "status request",
	"*#130**1*2##": "system status response",
	// Doorbell status query (responses)
	"*#8**33##":    "doorbell status query",
	"*#8**#33*0##": "SET doorbell sound OFF",
	"*#8**#33*1##": "SET doorbell sound ON",
	// Voicemail status query
	"*#8**40##": "answering machine status query",
	// Additional locks
	"*8*19*21##": "lock 1: button pressed (press)",
	"*8*20*21##": "lock 1: button released (release)",
	"*8*19*22##": "lock 2: button pressed (press)",
	"*8*20*22##": "lock 2: button released (release)",
	// Lock WHERE=116
	"*8*19*116##": "lock 116: button pressed (press)",
	"*8*20*116##": "lock 116: button released (release)",
}

// OWNEventDescriptions maps OWN patterns to human-readable descriptions in English
// for the Home Assistant activity_log sensor.
// Patterns are matched with strings.HasPrefix or exact match.
var OWNEventDescriptions = map[string]string{
	"*8*1#1#4#21*16##": "Doorbell: call from entrance panel",
	"*8*1#5#4#20*16##": "Camera activated (eye button)",
	"*8*2#5#4*16##":    "Two-way communication active",
	"*8*9#1#4*20##":    "Incoming doorbell notification",
	"*8*9#5#4*20##":    "Self-activation notification",
	"*8*3#1#4*416##":   "Incoming call ended",
	"*8*3#1#4*411##":   "Incoming call ended (WHERE=411, Classe 100X)",
	"*8*3#5#4*416##":   "Self-call ended",
	"*8*3#5#4*420##":   "Self-call ended (WHERE=420 variant)",
	"*8*40#1#4*20##":   "Voice active on incoming call",
	"*8*100#5#4*20##":  "Two-way intercom active",
	"*8*19*20##":       "Door opened (key button)",
	"*8*20*20##":       "Door: button released",
	"*8*21*16##":       "Staircase light activated (star button)",
	"*8*22*16##":       "Staircase light: button released",
	"*8*91##":          "Answering machine enabled",
	"*8*92##":          "Answering machine disabled",
	"*8*30*20##":       "Mute enabled",
	"*8*31*20##":       "Mute disabled",
	"*8*94#11#0*##":    "Session cleanup (channels 11, 0)",
	"*8*94#2#3*##":     "Session cleanup (channels 2, 3)",
	"*8*96#9#0*##":     "Session ended",
	// Audio modes (WHAT=87#N) — NEW 2026-03-12
	"*8*87#0*##": "Audio mode OFF / idle",
	"*8*87#1*##": "Audio mode 1 (intercom)",
	"*8*87#2*##": "Audio mode 2 (internal call)",
	"*8*87#4*##": "Audio mode 4 (Studio Professional)",
	"*8*87#5*##": "Audio mode 5 (video call)",
	// WiFi — NEW 2026-03-12
	"*8*101#0*##": "WiFi disabled",
	"*8*101#1*##": "WiFi enabled",
	// Video streams — NEW 2026-03-12
	"*7*35#4#1*##":  "Video stream type 4 source 1 started",
	"*7*35#3#2**##": "Video stream type 3 source 2 started",
	"*7*36*##":      "Video stream stopped",
	"*7*50#1*##":    "Camera/video started",
	// Display and existing video
	"*7*73#1#100*##": "Display on (brightness 100%)",
	"*7*73#1#10*##":  "Display dimmed (brightness 10%, screensaver)",
	"*7*55*##":       "Video stopped",
	"*7*219*##":      "Video disconnected",
	"*7*220#0*##":    "Video display mode (normal)",
	// Additional locks
	"*8*19*21##": "Lock 1 opened",
	"*8*20*21##": "Lock 1: button released",
	"*8*19*22##": "Lock 2 opened",
	"*8*20*22##": "Lock 2: button released",
	// Lock WHERE=116 — NEW 2026-03-12
	"*8*19*116##": "Lock 116 opened",
	"*8*20*116##": "Lock 116: button released",
	// Doorbell sound state (bus events)
	"*#8**33*0##": "Doorbell sound disabled (muted)",
	"*#8**33*1##": "Doorbell sound enabled (unmuted)",
	// Audio state (DIM 35) — NEW 2026-03-12
	"*#8**35*0*0*0##":  "Audio idle (fully muted)",
	"*#8**35*1*0*0##":  "Audio starting session (channel 1)",
	"*#8**35*3*0*0##":  "Audio connecting/negotiating (channel 3)",
	"*#8**35*12*0*0##": "Audio active channel 12",
	// System state (WHO=130) — NEW 2026-03-12
	"*#130**1*2##": "System status (value 2)",
}

// OWNEventTypes maps OWN patterns to event types for HA automations
var OWNEventTypes = map[string]string{
	"*8*1#1#4#21*16##": "doorbell_ring",
	"*8*1#5#4#20*16##": "camera_activated",
	"*8*2#5#4*16##":    "intercom_active",
	"*8*3#1#4*416##":   "call_ended",
	"*8*3#1#4*411##":   "call_ended",
	"*8*3#5#4*416##":   "call_ended",
	"*8*3#5#4*420##":   "call_ended",
	"*8*19*20##":       "door_open",
	"*8*20*20##":       "door_release",
	"*8*21*16##":       "stairlight_on",
	"*8*22*16##":       "stairlight_release",
	"*8*91##":          "voicemail_on",
	"*8*92##":          "voicemail_off",
	"*8*94#11#0*##":    "session_cleanup",
	"*8*94#2#3*##":     "session_cleanup",
	"*8*96#9#0*##":     "session_end",
	// Additional locks
	"*8*19*21##": "door_open_lock1",
	"*8*20*21##": "door_release_lock1",
	"*8*19*22##": "door_open_lock2",
	"*8*20*22##": "door_release_lock2",
	// Lock WHERE=116 — NEW 2026-03-12
	"*8*19*116##": "door_open_lock116",
	"*8*20*116##": "door_release_lock116",
	// Doorbell state
	"*#8**33*0##": "bell_muted",
	"*#8**33*1##": "bell_unmuted",
	// Audio modes — NEW 2026-03-12
	"*8*87#0*##": "audio_mode_off",
	"*8*87#1*##": "audio_mode_intercom",
	"*8*87#2*##": "audio_mode_internal",
	"*8*87#4*##": "audio_mode_studio",
	"*8*87#5*##": "audio_mode_videocall",
	// WiFi — NEW 2026-03-12
	"*8*101#0*##": "wifi_off",
	"*8*101#1*##": "wifi_on",
	// Video streams — NEW 2026-03-12
	"*7*35#4#1*##":  "video_stream_start",
	"*7*35#3#2**##": "video_stream_start",
	"*7*36*##":      "video_stream_stop",
	"*7*50#1*##":    "camera_start",
	// Display
	"*7*73#1#100*##": "display_on",
	"*7*73#1#10*##":  "display_dim",
	// Video display mode — NEW 2026-03-12
	"*7*220#0*##": "video_mode_display",
	// Audio state — NEW 2026-03-12
	"*#8**35*0*0*0##":  "audio_idle",
	"*#8**35*1*0*0##":  "audio_initializing",
	"*#8**35*3*0*0##":  "audio_connecting",
	"*#8**35*12*0*0##": "audio_active",
	// System state — NEW 2026-03-12
	"*#130**1*2##": "system_status",
}

// ==================== GPIO (verified through physical testing) ====================
//
// Classe 300X (original target):
// | GPIO | Confirmed function                    | Evidence                                                |
// |------|-----------------------------------------|-------------------------------------------------------|
// | 12   | Audio amplifier (active speaker)        | ON with video/call, OFF on hangup                     |
// | 13   | Unknown                               | No changes in any test                                  |
// | 47   | Permanently ON                        | Hardware/power                                          |
// | 49   | Proximity / wake-up sensor            | Only triggers with outdoor doorbell (3s pulse)          |
// | 52   | Permanently ON                        | Hardware/power                                          |
// | 54   | Permanently ON                        | Hardware/power                                          |
// | 56   | Idle/standby mode                     | ON=idle. OFF when two-way comms active                  |
// | 58   | Unknown                               | No changes in any test                                  |
// | 60   | Two-way audio relay (microphone)        | ON only when phone pressed. OFF=listen only           |
// | 154  | Incoming call indicator               | Brief pulse only with outdoor doorbell                  |
// | 155  | Active conversation (incoming)        | ON during incoming conversation, not on self-activation |
// | 176  | Permanently ON                        | Hardware/SCS bus                                        |
// | 180  | Established communication             | ON during any active communication                      |
//
// Classe 100X (C100X), verified on-device 2026-10-11 (sysfs-logical levels):
// | GPIO | Kernel name      | Behavior                                              |
// |------|------------------|-------------------------------------------------------|
// | 64   | en_ampli         | 1 while answered (brief pulse on chime)               |
// | 67   | en_tcoil         | 1 only while answered                                 |
// | 71   | tda9885 power    | 1 during ring+call                                    |
// | 73   | en_suppl_ring    | ~1s pulse with chime, only when unmuted               |
// | 77   | en_phon          | 1 during ring+call                                    |
// | 148  | SECURE_LOCK      | static 0 (safe-lock service, not the release)         |
// | 176  | rst_microsemi    | 0 in session, 1 idle (inverted)                       |
// | 180+ | LCD_DEV*         | static 0                                              |
// | 47   | ADDITIONAL_PWR   | static 1                                              |
// | 60/61/128/191 | hw_ver_car* | static version strapping (1/1/1/0)               |

// GPIOPinsByModel lists the polled GPIO pins per device variant.
var GPIOPinsByModel = map[string][]int{
	DeviceVariant300X: {12, 13, 47, 49, 52, 54, 56, 58, 60, 154, 155, 176, 180},
	DeviceVariant100X: {47, 64, 67, 71, 73, 77, 148, 176, 180, 187, 189, 190},
}

// GPIOPinsForVariant returns the polled pins for the given variant,
// falling back to c300x for unknown values.
func GPIOPinsForVariant(variant string) []int {
	if pins, ok := GPIOPinsByModel[variant]; ok {
		return pins
	}
	return GPIOPinsByModel[DeviceVariant300X]
}

// GPIOFunctionsByModel maps device variant to pin → function description.
var GPIOFunctionsByModel = map[string]map[int]string{
	DeviceVariant300X: {
		12:  "Audio amplifier (speaker)",
		13:  "Unknown",
		47:  "Always ON (power)",
		49:  "Proximity sensor / wake-up",
		52:  "Always ON (power)",
		54:  "Always ON (power)",
		56:  "Idle/standby mode",
		58:  "Unknown",
		60:  "Two-way audio relay (microphone)",
		154: "Incoming call indicator",
		155: "Active conversation (incoming)",
		176: "Always ON (SCS bus)",
		180: "Communication established",
	},
	DeviceVariant100X: {
		47:  "Additional power rail (static ON)",
		64:  "Speaker amplifier (ON while answered)",
		67:  "Telecoil, answer-only",
		71:  "TV demodulator power (ON in session)",
		73:  "Supplementary ringer (chime pulse, unmuted only)",
		77:  "Phone path (ON during ring+call)",
		148: "Safe-lock service (static)",
		176: "PoE controller reset (inverted: 0 in session)",
		180: "LCD select DEV4 (static)",
		187: "LCD select DEV1 (static)",
		189: "LCD select DEV2 (static)",
		190: "LCD select DEV3 (static)",
	},
}

// ==================== LEDs (verified in /sys/class/leds/) ====================
//
// Classe 300X sysfs names
// | LED sysfs       | Confirmed function                                                  |
// |-----------------|---------------------------------------------------------------------|
// | led_lock        | Door open: lights up during long key press                          |
// | led_vct_green   | Active video communication: ON during video/audio session           |
// | led_memo        | Pending messages: turns off when opening the message menu           |
// | led_exc_call    | Unknown (never seen ON in tests; 300X-only, "missed call" is a guess)|
// | led_gwifi       | Unknown (never seen ON in tests)                                    |
// | led_ans_machine | Unknown (never seen ON in tests)                                    |
// | led_vct_red     | Unknown (never seen ON in tests)                                    |
//
// Classe 100X (C100X) sysfs names
// | LED sysfs       | Confirmed function                                                  |
// |-----------------|---------------------------------------------------------------------|
// | led_call_exp    | Ringer excluded (mute): ON while doorbell sound is muted            |
// | led_door_lock   | Door lock: ON while the door-release key is active                  |
// | led_star        | Staircase light: ON while the star key is active                    |
// | led_horn_r      | Red phone key (hangup): ON while active call                        |
// | led_horn_g      | Green phone key (answer), ON while active call                      |
// | led_p1..p4      | Page/dot indicators (p1, p2, p3 ,p4)               |
// | led_autosw      | Unknown                                                             |
// | led_wifi        | Unknown                                                             |
//
// Note: each physical button also has its own LED, but those are controlled
// by the keypad I2C chip, not via GPIO sysfs

// LEDFunctionsByModel maps device variant ("c300x" / "c100x", see
// DeviceVariant) to its sysfs LED name → function description table.
// The 300X set was never fully verified (led_exc_call etc. were never seen
// ON); the 100X set was verified on-device 2026-10-11 unless noted.
var LEDFunctionsByModel = map[string]map[string]string{
	DeviceVariant300X: {
		"led_lock":        "Door open (lock active)",
		"led_vct_green":   "Active video communication",
		"led_vct_red":     "Unknown",
		"led_memo":        "Pending messages",
		"led_exc_call":    "Unknown (missed call?)",
		"led_gwifi":       "Unknown (WiFi?)",
		"led_ans_machine": "Unknown (answering machine?)",
	},
	DeviceVariant100X: {
		"led_call_exp":  "Ringer excluded (mute)",
		"led_door_lock": "Door lock (release key active)",
		"led_star":      "Staircase light (star key active)",
		"led_horn_r":    "Red phone key (hangup)",
		"led_horn_g":    "Green phone key (answer, unconfirmed)",
		"led_p1":        "Programmable Button 1",
		"led_p2":        "Programmable Button 2",
		"led_p3":        "Programmable Button 3",
		"led_p4":        "Programmable Button 4",
		"led_autosw":    "Unknown",
		"led_wifi":      "Unknown",
	},
}

// LEDEntity describes one LED for MQTT discovery.
type LEDEntity struct {
	ObjectID string
	Name     string
	Sysfs    string
	Icon     string
}

// LEDEntitiesByModel lists the MQTT discovery entities per device variant.
var LEDEntitiesByModel = map[string][]LEDEntity{
	DeviceVariant300X: {
		{ObjectID: "bticino_hardware_led_memo", Name: "LED Message", Sysfs: "led_memo", Icon: "mdi:message-alert"},
		{ObjectID: "bticino_hardware_led_wifi", Name: "LED WiFi", Sysfs: "led_gwifi", Icon: "mdi:wifi"},
		{ObjectID: "bticino_hardware_led_answering", Name: "LED Answering Machine", Sysfs: "led_ans_machine", Icon: "mdi:voicemail"},
		{ObjectID: "bticino_hardware_led_lock", Name: "LED Lock", Sysfs: "led_lock", Icon: "mdi:lock"},
		{ObjectID: "bticino_hardware_led_missed_call", Name: "LED Missed Call", Sysfs: "led_exc_call", Icon: "mdi:phone-missed"},
		{ObjectID: "bticino_hardware_led_call_green", Name: "LED Green Call", Sysfs: "led_vct_green", Icon: "mdi:phone-in-talk"},
		{ObjectID: "bticino_hardware_led_call_red", Name: "LED Red Call", Sysfs: "led_vct_red", Icon: "mdi:phone-hangup"},
	},
	DeviceVariant100X: {
		{ObjectID: "bticino_hardware_led_muted", Name: "LED Muted", Sysfs: "led_call_exp", Icon: "mdi:bell-off"},
		{ObjectID: "bticino_hardware_led_door_lock", Name: "LED Door Lock", Sysfs: "led_door_lock", Icon: "mdi:lock"},
		{ObjectID: "bticino_hardware_led_staircase", Name: "LED Staircase Light", Sysfs: "led_star", Icon: "mdi:lightbulb"},
		{ObjectID: "bticino_hardware_led_phone_red", Name: "LED Phone Red", Sysfs: "led_horn_r", Icon: "mdi:phone-hangup"},
		{ObjectID: "bticino_hardware_led_phone_green", Name: "LED Phone Green", Sysfs: "led_horn_g", Icon: "mdi:phone-in-talk"},
		{ObjectID: "bticino_hardware_led_p1", Name: "LED Programmable Button 1", Sysfs: "led_p1", Icon: "mdi:numeric-1-circle"},
		{ObjectID: "bticino_hardware_led_p2", Name: "LED Programmable Button 2", Sysfs: "led_p2", Icon: "mdi:numeric-2-circle"},
		{ObjectID: "bticino_hardware_led_p3", Name: "LED Programmable Button 3", Sysfs: "led_p3", Icon: "mdi:numeric-3-circle"},
		{ObjectID: "bticino_hardware_led_p4", Name: "LED Programmable Button 4", Sysfs: "led_p4", Icon: "mdi:numeric-4-circle"},
		{ObjectID: "bticino_hardware_led_autosw", Name: "LED Autoswitch", Sysfs: "led_autosw", Icon: "mdi:swap-horizontal"},
		{ObjectID: "bticino_hardware_led_wifi_100x", Name: "LED WiFi", Sysfs: "led_wifi", Icon: "mdi:wifi"},
	},
}

// LEDEntitiesForVariant returns the discovery entities for the given variant,
// falling back to c300x for unknown values.
func LEDEntitiesForVariant(variant string) []LEDEntity {
	if entities, ok := LEDEntitiesByModel[variant]; ok {
		return entities
	}
	return LEDEntitiesByModel[DeviceVariant300X]
}

// Home Assistant Device Class Mappings
const (
	HADeviceClassLock    = "lock"
	HADeviceClassSensor  = "sensor"
	HADeviceClassSwitch  = "switch"
	HADeviceClassTrigger = "device_trigger"
	HADeviceClassEvent   = "event"

	// Icons
	HAIconLock        = "mdi:lock"
	HAIconTablet      = "mdi:tablet"
	HAIconVoicemail   = "mdi:voicemail"
	HAIconBell        = "mdi:bell"
	HAIconDoorbell    = "mdi:doorbell"
	HAIconDialpad     = "mdi:dialpad"
	HAIconMessage     = "mdi:phone-message"
	HAIconActivityLog = "mdi:timeline-text"
)

// ==================== HELPER FUNCTIONS ====================

// LocalOutboundIP returns the LAN IP of the default interface (trick: opening
// a UDP socket to a public IP sends nothing but pins the source IP of the
// route). Falls back to 127.0.0.1 when it cannot be determined. Use this
// instead of hardcoding the device IP (it changes with DHCP).
func LocalOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return "127.0.0.1"
}

// DeviceModelName returns the display name for the configured DeviceVariant.
func DeviceModelName() string {
	if DeviceVariant == DeviceVariant100X {
		return "BTicino Classe 100X"
	}
	return "BTicino Classe 300X"
}

// DeviceModelCode returns the model as the unit names it in conf.xml
// <modello>. The 300X value is the one upstream used.
func DeviceModelCode() string {
	if DeviceVariant == DeviceVariant100X {
		return "Classe 100 X16E"
	}
	return "Classe 300 X13E"
}

// SIPUsernameForVariant returns the intercom's SIP identity (INVITE target,
// answerer username, Contact user) for the given device variant ("c300x" /
// "c100x", see DeviceVariant). Unknown variants fall back to c300x.
func SIPUsernameForVariant(variant string) string {
	if variant == DeviceVariant100X {
		return DeviceVariant100X
	}
	return DeviceVariant300X
}

// RemoteSetCommandsWork reports whether dimension SET commands take effect.
// Verified ignored (ACKed but no state change) on Classe 100X, tested
// 2026-10-13: ringer mute (3 frame variants) and display power. Door lock,
// staircase light and raw commands are unaffected (separate paths).
func RemoteSetCommandsWork() bool {
	return DeviceVariant != DeviceVariant100X
}

// AnsweringMachineAvailable reports whether the unit has a working
// answering machine. The Classe 100X ships the binaries but exposes no UI
// for it (no AnsweringPage.qml, process never runs, memo dirs unused).
func AnsweringMachineAvailable() bool {
	return DeviceVariant != DeviceVariant100X
}

// VideoHighResForVariant reports whether the unit has a high-res video leg
// (type 0, e.g. lo:5007). The Classe 100X serves low-res only (type 1,
// e.g. lo:5002): requesting high-res there is ACKed with no RTP behind it
// (verified 2026-10-11). Unknown variants assume high-res (300X behavior).
func VideoHighResForVariant(variant string) bool {
	return variant != DeviceVariant100X
}

// IsCallHangup reports whether a raw OWN frame ends a call (drives the
// call.hangup event that disarms live video). Matches the WHO=8 WHAT=3#…
// hangup family with any WHERE (416 and 420 on Classe 300X, 411 observed on
// Classe 100X 2026-10-11) — model-agnostic, no variant branching needed.
func IsCallHangup(raw string) bool {
	switch raw {
	case CmdHangupIncoming, CmdHangupSelf, CmdHangupSelf420, CmdHangupIncoming411:
		return true
	}
	return strings.HasPrefix(raw, "*8*3#") && strings.HasSuffix(raw, "##")
}

// BuildDoorOpenPress builds the OWN press-door-button command for a given WHERE.
// Example: BuildDoorOpenPress(20) -> "*8*19*20##", BuildDoorOpenPress(21) -> "*8*19*21##"
func BuildDoorOpenPress(where int) string {
	return fmt.Sprintf("*8*19*%d##", where)
}

// BuildDoorOpenRelease builds the OWN release-door-button command for a given WHERE.
func BuildDoorOpenRelease(where int) string {
	return fmt.Sprintf("*8*20*%d##", where)
}
