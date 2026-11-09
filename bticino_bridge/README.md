# BTicino Bridge

Go bridge that runs on a BTicino Classe 100X or 300X video door entry unit
(ARM7, i.MX6) and connects it to Home Assistant (MQTT), Apple HomeKit, a web
dashboard, a REST API and RTSP video.

This bridge comes from
[fquinto/bticinoClasse300x](https://github.com/fquinto/bticinoClasse300x),
which wrote it for the Classe 300X. This fork is tested on a Classe 100X; the
300X-only features (answering machine, display, ringer) are not tested here.
Version and changes: [`VERSION`](VERSION), [`CHANGELOG.md`](CHANGELOG.md).

## Device variant

The model is chosen at build time: `DeviceVariant` in
`pkg/bticino/constants.go` is `"c100x"` or `"c300x"`. Rebuild after changing
it. It selects the LED table and the video port, and on the Classe 100X it
hides what that unit does not have or ignores: the answering machine with its
messages and memos, and the display and ringer controls.

## Status

| Component | Status |
|---|---|
| Web dashboard (:8082) | Dashboard, Controls, Logs, Settings; Messages and Notes on the 300X only |
| Web/API authentication | Optional HTTP Basic Auth (`security.web_auth_required`). POST, PUT and DELETE from other sites are refused (403). No HTTPS |
| MQTT / Home Assistant | Auto-discovery, see [`docs/HOME_ASSISTANT.md`](docs/HOME_ASSISTANT.md) |
| OpenWebNet | Monitor on port 20000, commands to port 30006 |
| Live video on call (:6554 `/doorbell-video`) | Passive tap of the native video leg, no bus commands (`streaming.live_on_call`) |
| Camera session | Video without a call over a loopback self-call, started by an RTSP viewer or the HA switch `switch.bticino_video_session` (`streaming.camera_session`) |
| Camera snapshots | `GET /api/snapshot`; a still goes to MQTT on a ring |
| REST API | Swagger UI at `/api/docs/` |
| Live updates | Server-sent events at `/api/events` (LEDs, GPIO) |
| HomeKit (:8081) | Implemented, not verified |

## Architecture

```
Native processes: bt_vct, openserver, bt_av_media, flexisip (not touched)

bticino_bridge
 ├── OpenWebNet ────── monitor :20000, one short connection per command to :30006
 ├── Web server ────── Svelte UI + REST API + Swagger + SSE on :8082
 ├── MQTT ──────────── Home Assistant discovery and command topics
 ├── HomeKit ───────── brutella/hap on :8081
 ├── SIP / RTSP ────── loopback SIP via the local Flexisip, RTP relay, RTSP :6554
 ├── Device config ─── reads and watches conf.xml, aswm, tvcc
 ├── Input monitor ─── buttons, touch, GPIO
 └── Message parser ── answering machine messages and memos (300X)
```

### Non-interfering mode

The bridge shares the unit with the native processes. It only monitors port
20000 and sends each command in its own short connection to 30006, the same
exchange as `echo '<frame>' | nc 0 30006`: the frame and a newline, a
half-close, then it waits up to 5 s for the server to close. Only a single
frame is accepted (digits, `*` and `#`, starting with `*` and ending with the
only `##`). Do not open persistent sockets to 30006/30007. The unit needs about
310 ms between commands.

## Build and deploy

Needs Go 1.25+ and Node.js.

```bash
make build        # web UI (web/dist) + ARM binary
make build-go     # binary only
make build-web    # web UI only
make dev          # local development: vite + go run
make deploy       # build, copy to the device, restart the bridge
make clean
```

`make deploy` runs `scripts/deploy.sh`, which connects to the SSH host
`bticino` (set it up in `~/.ssh/config`) and installs to
`/home/bticino/cfg/extra/`. Its options are listed at the top of the script.

Flags: `-config` (default `configs/config.yaml`; the device runs with
`config.yaml`), `-log-level`, `-version`, `-test` (no device connection),
`-web-port`, and `-enable-openwebnet`, `-enable-web`, `-enable-homekit`,
`-enable-video`, `-enable-answerer`.

## Configuration

One file: `configs/config.yaml`, deployed as
`/home/bticino/cfg/extra/config.yaml`. Most options can be changed on the
Settings page. Saving from the web UI drops the comments in the file.

```yaml
mqtt:
  enabled: true
  host: "<broker-ip>"
  port: 1883
  username: "<CHANGE-ME>"
  password: "<CHANGE-ME>"
  topic_prefix: "bticino"     # do not use "homeassistant"

sip:
  native_dev_addr: "<CHANGE-ME>"   # required for camera sessions, no fallback
  transport: "tcp"                 # camera sessions failed with udp on a 100X

streaming:
  rtsp_port: 6554
  live_on_call: true
  camera_session: true
  video_on_demand: false

security:
  web_auth_required: true
  web_username: "<CHANGE-ME>"
  web_password: "<CHANGE-ME>"
```

> **Video safety.** Leave `video_on_demand` off. The old on-demand path
> competes with the native firmware for the camera, and a command storm once
> made the unit's watchdog reboot it. The camera session is the supported way to
> get video without a call.

## RTSP video

```bash
ffplay rtsp://<device-ip>:6554/doorbell
```

See [`docs/WEBRTC_RTSP_STREAMING.md`](docs/WEBRTC_RTSP_STREAMING.md) and
[`docs/GO2RTC_INTEGRATION.md`](docs/GO2RTC_INTEGRATION.md).

## Tests

```bash
go test ./...
```

On macOS run them in the `golang:1.25` image. `make test` runs the integration
tests in `scripts/run_all_tests.sh` against the device.
