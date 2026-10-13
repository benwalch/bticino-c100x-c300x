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

Entities are sorted into HA's standard sections via `entity_category`.
**Controls** holds switches/buttons (only for features that accept commands);
**Sensors** holds operational readouts; **Diagnostic** holds LEDs, GPIO,
network info and the device-config mirror; **Configuration** holds the
optional restart buttons.

> **Topics v2 (breaking).** State topics are bare nouns (`doorbell/entrance`,
> not `doorbell/state`); commands append `/set`; momentary triggers live
> under their domain (`doorbell/entrance/event`); structured data under
> `/attributes`; the snapshot under `camera/doorbell/image`. `object_id` is
> always the topic path flattened (`/` → `_`). After upgrading, delete stale
> v1 entities/discovery topics once — HA will not do it for you.

---

## Controls (you can act on these)

| Entity | Domain | State topic | Command topic | Notes |
|---|---|---|---|---|
| Door | `lock` | `bticino/lock/main` | `bticino/lock/main/set` | `LOCK`/`UNLOCK`; opens the main door |
| Answering machine *(300X only)* | `switch` | `bticino/voicemail` | `bticino/voicemail/set` | `ON`/`OFF` |
| Doorbell sound *(300X only)* | `switch` | `bticino/audio/ring/muted` | `bticino/audio/ring/muted/set` | Ringer mute/unmute |
| Display *(300X only)* | `switch` | `bticino/display/power` | `bticino/display/power/set` | Screen on/off |
| Staircase light | `button` | — | `bticino/light/staircase/set` | Momentary (press+release) |
| Mark all as read *(300X only)* | `button` | — | `bticino/messages/mark_read/set` | Marks every message read |
| Additional locks | `lock` | `bticino/lock/<id>` | `bticino/lock/<id>/set` | Only if configured (secondary gates) |

## Sensors (read-only)

| Entity | Domain | State topic | Notes |
|---|---|---|---|
| Temperature | `sensor` | `bticino/environment/temperature` | °C, device internal |
| New messages *(300X only)* | `sensor` | `bticino/messages/unread` | Unread answering-machine messages |
| Total messages *(300X only)* | `sensor` | `bticino/messages/total` | |
| Storage used *(300X only)* | `sensor` | `bticino/messages/storage` | % used |
| Keypad | `sensor` | `bticino/panel/keypad` | Last physical button pressed |
| System diagnostics | `sensor` | `bticino/system/diagnostics` | |
| Activity log | `sensor` | `bticino/bus/log` | + `.../attributes` for history |
| Firmware version | `sensor` | `bticino/system/network` | State is the firmware version; IP/MAC/kernel in attributes |

### Diagnostic — network (`entity_category: diagnostic`)

| Entity | State topic | Notes |
|---|---|---|
| IP address | `bticino/system/network/address` | plain value |
| MAC address | `bticino/system/network/mac` | plain value |
| Wi-Fi signal (%) | `bticino/network/wifi` | from `connmanctl` Strength, refreshed every 60 s |

### Device configuration (read-only mirror of the native settings)

| Entity | State topic |
|---|---|
| Answering machine status *(300X only)* | `bticino/answering` |
| S0 ringtone (entrance panel) | `bticino/audio/ringtone` (`value_json.s0`) |
| Secondary doorbell ringtone | `bticino/audio/ringtone` (`value_json.s1`) |
| Floor door ringtone | `bticino/audio/ringtone` (`value_json.floor`) |
| Entrance doorbell volume | `bticino/audio/volume` (`value_json.s0`) |
| Secondary doorbell volume | `bticino/audio/volume` (`value_json.s1`) |
| Floor doorbell volume | `bticino/audio/volume` (`value_json.floor`) |
| Display brightness | `bticino/display/settings` (`value_json.brightness`; also `clean_time`) |
| Camera brightness (<id> per TVCC config) | `bticino/camera/<id>/settings` |
| Camera contrast (<id> per TVCC config) | `bticino/camera/<id>/settings` (`value_json.contrast`) |

## Binary sensors

| Entity | Domain | State topic | Notes |
|---|---|---|---|
| Entrance doorbell | `binary_sensor` | `bticino/doorbell/entrance` | Entrance-panel ring |
| Floor doorbell | `binary_sensor` | `bticino/doorbell/floor` | Flat door button (`*7*59#`), `device_class: occupancy` |
| SIP status | `binary_sensor` | `bticino/sip/registration` | SIP registration up/down |
| LEDs (300X ×7, 100X ×11) | `binary_sensor` | `bticino/hardware/led/<id>` | Sysfs names differ per model; `mmc*` skipped |
| GPIO (13 on 300X, 12 on 100X) | `binary_sensor` | `bticino/hardware/gpio/<pin>` | Pin list follows `DeviceVariant`; names carry the verified function |

## Events (`event` platform)

Momentary **event entities** — ideal for automations ("when the doorbell rings…").
Each fires with a timestamp and an `event_type`; the payload is JSON on the topic.

| Entity | Topic | event_types |
|---|---|---|
| Entrance doorbell (event) | `bticino/doorbell/entrance/event` | `pressed` |
| Floor doorbell (event) | `bticino/doorbell/floor/event` | `pressed` |
| Call (event) | `bticino/call/event` | `incoming`, `connected`, `ended` (incoming carries `caller`) |
| Bus frame (event) | `bticino/bus/frame/event` | `frame` (carries `raw`) |

These complement the `binary_sensor`/`sensor` versions: the binary sensors show
current state, the event entities give a clean per-trigger event for automations.

## Call (incoming-call detection)

| Entity | Domain | State topic | Values |
|---|---|---|---|
| Call status | `sensor` | `bticino/call` | `INCOMING` / `CONNECTED` / `IDLE` |
| Caller | `sensor` | `bticino/call/caller` | SIP user (e.g. `c100x`) |

**Total auto-discovered: ~50 entities** (100X; more with optional buttons/locks, minus 300X-only answering set).

---

## Configuration — restart buttons (optional)

Two buttons in HA's **Configuration** section, **off by default**. Enable with
`mqtt.enable_system_buttons: true`:

| Button | Command topic | Action |
|---|---|---|
| Restart bridge | `bticino/system/restart/set` | Relaunches the `bticino_bridge` process (setsid, ~2 s gap) |
| Restart device | `bticino/system/reboot/set` | **Reboots the whole intercom** (`/sbin/reboot`) |

> ⚠️ The reboot button restarts the entire door unit. Restarting dropbear (SSH)
> is intentionally **not** offered — the device has no service supervisor for it,
> so a failed restart would drop SSH permanently.

## Doorbell camera

A **camera** entity (`camera.bticino_camera_doorbell`, image topic `bticino/camera/doorbell/image`,
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
video leg (a read-only packet tap on `lo:5002`, where the firmware already streams
cleartext H.264 RTP for the whole call) into the RTSP relay, which **go2rtc**
publishes as a live WebRTC/HLS stream. HA can then show **live video** (motion) —
not just the still snapshot — for the duration of the call.

- **Enabled by default** (`streaming.live_on_call: true`). It sends **zero commands
  to the device bus** (safer than the cooperative snapshot) and is **independent of
  `video_on_demand`** (no self-INVITE, the native call session already exists).
- go2rtc serves the stream **`doorbell-video`** (video only). Point HA at it via the
  go2rtc / `webrtc-camera` integration (`http://<device-ip>:1984`), or add a generic
  camera to `rtsp://<device-ip>:6554/doorbell-video`.
- Outside a call there is no live video (that's normal); `camera.bticino_camera_doorbell` keeps
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
    entity: camera.bticino_camera_doorbell
    name: Doorbell Camera
    camera_view: auto
  - type: entities
    title: BTicino Video Intercom
    entities:
      - entity: lock.bticino_lock_main
      - entity: binary_sensor.bticino_doorbell_entrance
      - entity: binary_sensor.bticino_doorbell_floor
      - entity: sensor.bticino_call
      - entity: sensor.bticino_call_caller
      - entity: switch.bticino_voicemail
      - entity: sensor.bticino_messages_unread
      - entity: sensor.bticino_environment_temperature
  - type: button
    name: Open door
    tap_action:
      action: call-service
      service: lock.unlock
      target:
        entity_id: lock.bticino_lock_main
```

## Automations

Ready-made examples live in [`configs/homeassistant/automations.yaml`](../configs/homeassistant/automations.yaml)
(notify on ring, open door on button, etc.). A manual discovery/entity reference
is in [`configs/homeassistant/discovery.yaml`](../configs/homeassistant/discovery.yaml).
