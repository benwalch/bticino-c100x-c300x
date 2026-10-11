# Home Assistant integration

The bridge integrates with Home Assistant over **MQTT with auto-discovery**.
You do **not** need to write any YAML: once the bridge is running and the MQTT
broker is configured, the entities appear automatically under a single device
(**BTicino Classe 300X**).

- **Discovery prefix:** `homeassistant/` (HA standard — do not change)
- **Data prefix:** `bticino` (configurable via `mqtt.topic_prefix`; never set it to
  `homeassistant`, it collides with discovery)
- **Availability:** every entity uses `bticino/bridge/state` (LWT) — entities show
  *unavailable* when the bridge is down.

All topics below assume the default data prefix `bticino`.

Entities are sorted into HA's standard sections via `entity_category`:
**Controls** and **Sensors** (main) hold the things you act on and the
operational readouts; **Diagnostic** holds LEDs, GPIO, network info and the
device-config mirror; **Configuration** holds the optional restart buttons.

---

## Controls (you can act on these)

| Entity | Domain | State topic | Command topic | Notes |
|---|---|---|---|---|
| Door | `lock` | `bticino/lock/state` | `bticino/lock/set` | `LOCK`/`UNLOCK`; opens the main door |
| Answering machine | `switch` | `bticino/voicemail/state` | `bticino/voicemail/set` | `ON`/`OFF` |
| Doorbell sound | `switch` | `bticino/doorbellsound/state` | `bticino/doorbellsound/set` | Ringer mute/unmute |
| Display | `switch` | `bticino/display/state` | `bticino/display/set` | Screen on/off |
| Staircase light | `button` | — | `bticino/light/set` | Momentary (press+release) |
| Door open | `button` | — | `bticino/door/open` | Momentary door pulse |
| Mark all as read | `button` | — | `bticino/messages/markallread/set` | Marks every message read |
| Additional locks | `lock` | `bticino/lock/<id>/state` | `bticino/lock/<id>/set` | Only if configured (secondary gates) |

## Sensors (read-only)

| Entity | Domain | State topic | Notes |
|---|---|---|---|
| Temperature | `sensor` | `bticino/sensor/temperature` | °C, device internal |
| New messages | `sensor` | `bticino/sensor/new_messages` | Unread answering-machine messages |
| Total messages | `sensor` | `bticino/sensor/total_messages` | |
| Storage used | `sensor` | `bticino/sensor/storage_used` | % used |
| Keypad | `sensor` | `bticino/sensor/keypad` | Last physical button pressed |
| OWN bus event | `sensor` | `bticino/sensor/bus_event` | Last raw OWN frame seen |
| System diagnostics | `sensor` | `bticino/sensor/system_diag` | |
| Multicast | `sensor` | `bticino/sensor/multicast_count` | Multicast messages seen |
| Activity log | `sensor` | `bticino/sensor/activity_log` | + `.../attributes` for history |
| System information | `sensor` | `bticino/sensor/system_info` | WHO=13 (IP/MAC/firmware…) in attributes |

### Diagnostic — network (`entity_category: diagnostic`)

| Entity | State topic | Notes |
|---|---|---|
| IP address | `bticino/sensor/system_info/attributes` | via `value_json.ip_address` |
| MAC address | `bticino/sensor/system_info/attributes` | via `value_json.mac_address` |
| Wi-Fi signal (%) | `bticino/sensor/wifi_signal` | from `connmanctl` Strength, refreshed every 60 s |

### Device configuration (read-only mirror of the native settings)

| Entity | State topic |
|---|---|
| Language | `bticino/system/language` |
| Time zone | `bticino/system/timezone` |
| NTP server | `bticino/system/timezone` *(currently shares the timezone topic)* |
| Date/time | `bticino/system/datetime` |
| Answering machine status | `bticino/answering/state` |
| Answering machine memory | `bticino/answering/state` |
| S0 ringtone (entrance panel) | `bticino/audio/ringtone/s0` |
| S0 volume | `bticino/audio/volume/s0` |
| Door volume | `bticino/audio/volume/door` |
| Display brightness | `bticino/display/brightness` |
| Camera 20 brightness | `bticino/camera/20/config` |

## Binary sensors

| Entity | Domain | State topic | Notes |
|---|---|---|---|
| Doorbell | `binary_sensor` | `bticino/doorbell/state` | Entrance-panel ring |
| Landing doorbell (floor) | `binary_sensor` | `bticino/doorbell_floor/state` | Landing/floor button (`*7*59#`), `device_class: occupancy` |
| SIP status | `binary_sensor` | `bticino/sensor/sip_status` | SIP registration up/down |
| LEDs (300X ×7: memo, wifi, answering, lock, missed call, green/red call; 100X ×11: muted, door lock, staircase, phone green/red, page 1–4, autosw, wifi) | `binary_sensor` | `bticino/led/<name>/state` | Sysfs names differ per model (`led_exc_call` vs `led_call_exp`, …); `mmc*` skipped |
| GPIO (13 on 300X, 12 on 100X) | `binary_sensor` | `bticino/gpio/<pin>/state` | Pin list follows `DeviceVariant`; names carry the verified function |

## Events (`event` platform)

Momentary **event entities** — ideal for automations ("when the doorbell rings…").
Each fires with a timestamp and an `event_type`; the payload is JSON on the topic.

| Entity | Topic | event_types |
|---|---|---|
| Doorbell (event) | `bticino/event/doorbell` | `pressed` |
| Landing doorbell (event) | `bticino/event/doorbell_floor` | `pressed` |
| Call (event) | `bticino/event/call` | `incoming`, `connected`, `ended` (incoming carries `caller`) |

These complement the `binary_sensor`/`sensor` versions: the binary sensors show
current state, the event entities give a clean per-trigger event for automations.

## Call (incoming-call detection)

| Entity | Domain | State topic | Values |
|---|---|---|---|
| Call status | `sensor` | `bticino/call/state` | `INCOMING` / `CONNECTED` / `IDLE` |
| Caller | `sensor` | `bticino/call/caller` | SIP user (e.g. `c300x`) |

**Total auto-discovered: ~42 entities** (43 with the doorbell camera below).

---

## Configuration — restart buttons (optional)

Two buttons in HA's **Configuration** section, **off by default**. Enable with
`mqtt.enable_system_buttons: true`:

| Button | Command topic | Action |
|---|---|---|
| Restart bridge | `bticino/system/restart_bridge/set` | Relaunches the `bticino_bridge` process (setsid, ~2 s gap) |
| Restart device | `bticino/system/reboot/set` | **Reboots the whole intercom** (`/sbin/reboot`) |

> ⚠️ The reboot button restarts the entire door unit. Restarting dropbear (SSH)
> is intentionally **not** offered — the device has no service supervisor for it,
> so a failed restart would drop SSH permanently.

## Doorbell camera

A **camera** entity (`camera.doorbell_camera`, image topic `bticino/camera/doorbell`,
base64 JPEG) that shows *who rang the bell*. When a doorbell rings or a call comes
in — the moments when the native video session is active — the bridge captures a
cooperative snapshot (single `*7*300`, validated at 688×480) and publishes it.

This entity appears **only when `streaming.video_on_demand: true`**, because it
needs the video-activation path. It is **off by default** for safety:

```yaml
streaming:
  video_on_demand: true    # enables /api/snapshot + the doorbell camera
  video_backend: "avmedia" # cooperative *7*300 (recommended)
```

> ⚠️ Enabling video competes with the native firmware for the camera; only turn
> it on after validating on your unit. See the safety note in the main `README.md`.
> With it **off** (default), the camera entity simply isn't created; call/floor
> sensors and everything else still work.

Related REST endpoints (diagnostics, always available):

- `POST /api/video/probe?confirm=yes` — single `*7*300`, returns a JPEG if a
  native session is active
- `POST /api/audio/probe?confirm=yes` — measures the Speex audio RTP flow
- `GET /api/snapshot` — JPEG (only wired when `video_on_demand: true`)

---

## Live video during a call (`live_on_call`)

When a call comes in **to your unit**, the bridge passively mirrors the native
video leg (a read-only packet tap on `lo:5007`, where the firmware already streams
cleartext H.264 RTP for the whole call) into the RTSP relay, which **go2rtc**
publishes as a live WebRTC/HLS stream. HA can then show **live video** (motion) —
not just the still snapshot — for the duration of the call.

- **Enabled by default** (`streaming.live_on_call: true`). It sends **zero commands
  to the device bus** (safer than the cooperative snapshot) and is **independent of
  `video_on_demand`** (no self-INVITE, the native call session already exists).
- go2rtc serves the stream **`doorbell-video`** (video only). Point HA at it via the
  go2rtc / `webrtc-camera` integration (`http://<device-ip>:1984`), or add a generic
  camera to `rtsp://<device-ip>:6554/doorbell-video`.
- Outside a call there is no live video (that's normal); `camera.doorbell_camera` keeps
  showing the last snapshot as a preview.

```yaml
streaming:
  live_on_call: true  # live door video during an incoming call (default)
```

> **Building privacy:** live video only works for calls to *your* unit. If the door
> entry system is busy with a call to another flat, no video is available — this is
> **by design** (a neighbour can't watch your camera), not a fault.

> **Audio is not included.** The door only emits audio as RTP when a SIP party is in
> the call, so *listening* in HA needs the bridge to join the call as a SIP endpoint —
> a future phase (see `docs/BACKLOG.md`).

---

## Example Lovelace card

```yaml
type: vertical-stack
cards:
  - type: picture-entity          # only if video_on_demand: true
    entity: camera.doorbell_camera
    name: Doorbell Camera
    camera_view: auto
  - type: entities
    title: BTicino Video Intercom
    entities:
      - entity: lock.bticino_door
      - entity: binary_sensor.bticino_doorbell
      - entity: binary_sensor.bticino_doorbell_floor
      - entity: sensor.bticino_call_state
      - entity: sensor.bticino_call_caller
      - entity: switch.bticino_voicemail
      - entity: sensor.bticino_new_messages
      - entity: sensor.bticino_temperature
  - type: button
    name: Open door
    tap_action:
      action: call-service
      service: button.press
      target:
        entity_id: button.bticino_door_open
```

## Automations

Ready-made examples live in [`configs/homeassistant/automations.yaml`](../configs/homeassistant/automations.yaml)
(notify on ring, open door on button, etc.). A manual discovery/entity reference
is in [`configs/homeassistant/discovery.yaml`](../configs/homeassistant/discovery.yaml).
