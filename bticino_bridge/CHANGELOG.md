# Changelog - BTicino Classe 300X Enhanced Bridge

## [Unreleased]

- When the native unit ends a camera session (after about 30 s), the bridge
  no longer sends a BYE of its own, which was answered with 481.

- On shutdown the RTSP accept loop and the RTP reader no longer spin on their
  closed sockets, which logged dozens of `use of closed network connection`
  errors per stop.

- `deploy.sh` shows the device's address from the SSH config in its closing
  hints instead of a hard-coded old IP.

- Removed further unused helpers in `deviceconfig`, `openwebnet`, `version`
  and others, including `homekit.DefaultConfig` with its fixed default PIN.

- Removed the unused input event bus with its button-to-command mapping
  (it mapped KEY_1 to opening the door) and the unused GPIO manager.

- Removed unused SIP code: the old RTSP server (replaced by the enhanced
  one), the go2rtc manager, the single-identity REGISTER helpers, and the
  video stream start/stop path, which opened and closed the shared
  OpenWebNet connection.

- Removed the legacy web UI that was embedded in `server.go` (HTML, CSS, JS
  and page handlers). It was no longer served; the Svelte app is the web UI.

- A camera session is no longer ended by a late BYE of an earlier dialog; such
  a BYE is answered with 200 and the running session continues.
- The answer to a re-INVITE during a camera session now carries the request's
  `To` header as RFC 3261 requires. It used the native side's tag instead of
  ours.
- SIP registration is judged by the status code of the answer. Before, the
  whole message was searched for `200 `, so once the CSeq reached 200 (after
  about 8 hours of refreshes) a refusal also counted as registered.

- The OpenWebNet monitor on port 20000 no longer gives up. After a dropped
  connection it retried `retry_attempts` times `retry_delay` apart (3 s by
  default) and then stopped for good, so doorbell events silently stopped
  reaching Home Assistant until the bridge was restarted. It now retries until
  it is connected again, waiting up to 30 s between attempts, and it also does
  so when the first connect at startup fails. `retry_attempts` now only applies
  to that first connect.
- Bus events are no longer lost around a connect: frames that arrived together
  with the session ACK were dropped, and frames before the event handler was
  installed were ignored (up to about 45 s while the SIP start was retried).
  The monitor now connects only after its handler is in place.
- Stopping the bridge no longer logs `listener: read error` and
  `SIP … connection lost — reconnecting`; the SIP client no longer takes its own
  shutdown for a lost connection.

- Security: the web server refuses POST, PUT and DELETE requests that a browser
  sends from another site (403), using Go's `http.CrossOriginProtection`. Before,
  any web page open in a browser that had logged in to the dashboard could open
  the door or send bus commands, because the browser adds Basic Auth by itself.
  The dashboard itself, `curl` and Home Assistant are not affected. GET stays
  allowed, so `GET /api/snapshot` can still be triggered from other sites.
  Migration: building needs Go 1.25 or newer.
- Security: the bridge no longer starts when `config.yaml` cannot be read or
  parsed. Before, it silently fell back to built-in defaults, without web
  authentication and with HomeKit on a default PIN. It now exits with code 1 and
  writes `cannot load config …` to stderr and to `bticino_bridge.crash.log`,
  which `scripts/deploy.sh` shows. A `-test` run needs a readable config too.
- Security: `POST /api/talkback/start` accepts only a `source` below `/tmp` or
  `/home/bticino/cfg/extra` and only a loopback `host` (or none); anything else
  is refused with 400 before the bus command is sent. Before, it streamed any
  readable file to any host. `POST /api/audio/duplex/start` checks `talk` the
  same way.
- Removed `GET /api/config/sip-accounts`. Nothing used it, and it returned the
  Flexisip accounts with their password hashes.

- Fixed two crashes. The GPIO poller wrote its state map without the lock while
  the 30 s status check read it; the Go runtime aborts such a program with
  `concurrent map read and map write`. And a `settings.xml` that fails to parse,
  for example while the firmware rewrites it, no longer crashes the bridge: the
  string-based fallback parser panicked on every such file and is removed. The
  read now fails with `parse settings.xml: …` and the next periodic read
  publishes the values again.

- The bridge now shuts down cleanly on `TERM`. Before, it hung at
  `Stopping BTicino SIP client` (seen on the device): the SIP client waited for
  a lock it already held. `deploy.sh` and the restart then killed it with `-9`
  after 15 s, so MQTT did not publish `offline` (Home Assistant only learned it
  from the last will) and the OpenWebNet connections were not closed. Two
  further hangs in the same shutdown are fixed: the input monitor waited for
  reads on `/dev/input/event*` that only end with a key press, and the web
  server waited for open `/api/events` streams (an open dashboard).
- A camera session stopped while it is still dialing now ends right after the
  dial instead of holding for the full `camera_session_duration`. After the SIP
  client has stopped, no new camera session starts.

- Security: bus commands no longer go through a shell. Before, each one ran as
  `sh -c "echo '<frame>' | nc 0 30006"`, so a crafted command over
  `POST /api/controls/command` or the MQTT topic `<prefix>/command/send` could
  run shell commands as root. The bridge now opens its own short connection with
  the same exchange as the busybox `nc` in the firmware: the frame and a newline,
  a half-close, then it waits for the server to close. That wait now ends after
  5 s; `nc` had no limit. Only a single frame is sent: digits, `*` and `#`,
  starting with `*` and ending with the only `##`, as the OpenWebNet
  specification defines it. Anything else is refused before connecting, over
  MQTT as well, which did not check at all before. The web endpoint answers 400.
  Log lines changed: `OWN command executed via netcat` and
  `OpenWebNet command executed successfully via netcat` are now `OWN command sent`.

- MQTT no longer stays off when the broker is not reachable at bridge start, for
  example when Home Assistant boots later after a power loss: the bridge retries
  every 30 s until the first connection, then the reconnect works as before. Each
  new connection error is logged once as a warning, so a wrong password shows up
  without filling the log. The device config publisher waits for the connection
  instead of giving up, and so do the startup queries, whose network info was lost
  before. Log lines changed: `MQTT: Connected to Home Assistant at`
  is now `MQTT: Bridge started for`, and the startup summary says whether MQTT is
  connected; `MQTT Bridge connected to` still marks a successful connection.
- New `docs/FUTURE_FEATURES.md` with the tested but not implemented plan for a bridge
  autostart after reboot, and the idea of a manual start from Home Assistant.
- The periodic `Publishing LED states to MQTT` line is now logged at debug level.
- `make deploy` and `scripts/deploy.sh` spare the flash: backups are hard links, so
  they copy no data, and only files that changed are written. A transfer goes into
  RAM first and reaches the flash only with a matching checksum. After a successful
  start, 2 backups each of the binary and `config.yaml` remain; older ones are
  deleted. Old web assets are removed. The binary is built stripped (`-s -w`) and
  with `-trimpath`, so it is about 30% smaller and debuggers lack its symbols. A
  failed Go build now stops the deploy.
  Migration: copy backups you want to keep off the device before the next deploy.
- The bridge rotates its own log and applies `logging.level`, `file`, `max_size`
  and `max_files`, which had no effect before. New defaults: `2MB` and 3 files, so
  at most 6 MB in the tmpfs behind `/var/log`. `max_files` counts the active file.
  Without `logging.level` or `--log-level` the level is now `error`, not `info`, so
  start lines, including the version, are not logged; set `level: info` to keep them.
  `--log-level` still wins when given. The log now survives a bridge restart.
  Lines that cannot be written are dropped and counted; the bridge keeps running.
  Go crashes such as panics go to `bticino_bridge.crash.log` next to the binary, on
  flash, so they survive a reboot. Each start adds one line with version and time,
  and the file is cut back to its last 256 KB at startup when it exceeds 1 MB. The
  next start logs an error when the previous run crashed. If the log file cannot be
  opened, the bridge retries every 5 s and notes the problem in the crash log.
  `scripts/deploy.sh` shows the crash log when the bridge is not running after a
  deploy.
  `logging.file` must be in `/var/log` or `/tmp`, `max_files` must not exceed 16 and
  `max_size` x `max_files` must not exceed 48 MB. Other values are refused when saving in the web interface and
  replaced by the defaults at startup, with an error in the log. A `-test` run logs
  to stderr. The log level in the web settings now sets `logging.level`, and log
  settings apply after a bridge restart.
  Migration: start the bridge with `> /dev/null 2>&1`. A redirect to
  `bticino_bridge.log` makes two writers share that file. A config that still sets
  `10MB` and 5 files exceeds the limit and falls back to `2MB` and 3 files. An older
  binary only logs to stdout, so start it with its old redirect after a rollback.
- `make deploy` and `scripts/deploy.sh` now stop when the web build fails. Before,
  Node versions too old for Vite left the previous `web/dist` in place and the
  deploy still reported success.
  Migration: run `nvm use` in `bticino_bridge/` (`.nvmrc` selects Node 22) before
  building.
- The bridge no longer sends test commands on the bus at startup, among them
  `*1*0*11##` (light point 11) and `*2*1*11##` (shutter point 11).
- `POST` and `DELETE /api/talkback/greeting` return 501 on units without an
  answering machine, such as the Classe 100X.
- Merged the upstream bridge up to 0.29.4. Its features are not verified on the
  Classe 100X and start off: `sip.answerer`, `streaming.panel_view`,
  `streaming.live_audio_on_call`, `audio.duplex_enabled`,
  `audio.listen_tap_enabled` and `access`. Their Home Assistant entities and
  commands exist only when enabled. `POST /api/talkback/say` is 300X only.
- `sip.transport` stays `tcp`: camera sessions failed with `udp` on a Classe 100X
  and work with `tcp`.
  Migration: if you changed the value to `udp`, set it back to `tcp`.
- Registrations renew after half of their expiry instead of on every timer tick.
  A camera session owns the webrtc socket until it ends, a renewal due in that
  time waits for it.
- Repository layout follows upstream: the firmware build script and its helpers
  moved to `legacy/`.
  Migration: run `sudo python3 legacy/main.py` from the repository root.
- Breaking: `sip.native_dev_addr` no longer falls back to a built-in unit
  address, because that address belongs to one unit. Camera sessions now
  refuse to start while it is unset.
  Migration: set `sip.native_dev_addr`, or find it with the settings wizard.
- Firmware builds now always include `bt_ssh_guard`: `init` respawns it, and it
  restarts dropbear when port 22 stops listening, recording each outage on flash.
  It replaces the dropbear check in `bt_service_watchdog`, which offered no
  protection without MQTT and was fooled by open SSH sessions.
  Migration: rebuild and flash the firmware, or install the script and the
  `sshg` inittab entry by hand (see the root `README.md`).
- Camera session switch (`switch.bticino_video_session`, opt-in via
  `streaming.camera_session`): bridge-initiated headless video over a
  loopback self-call the native unit answers (display stays off). Manual
  starts hold for `camera_session_duration`, RTSP-driven ones follow viewer
  presence (10-minute leak backstop), then auto-hang-up. Zero bus commands.
  Deploy stops the bridge gracefully (TERM first) so in-dialog sessions
  get their BYE instead of orphaning the native dialog.
  RTSP demand prefers it over the legacy self-INVITE path when enabled;
  otherwise legacy behavior is unchanged. The session end publishes a fresh
  still to the doorbell camera entity.
- Unit address for the session SDP offer: `sip.native_dev_addr`, the unit UUID
  offered as `a=DEVADDR`. Verified per unit — a wrong value is refused with an
  instant 486. The settings SIP
  tab gains a guided discovery wizard: passive 30s listen on the loopback SIP
  leg while the user starts a camera view in the app, auto-verify of the found
  value with one test INVITE (a 486 after provisionals means the unit is busy,
  not that the value is wrong), apply only on explicit confirmation. The SIP
  domain is filled from the local Flexisip config
  (`GET /api/sip/domain/discover`).
  Migration: config key `sip.devaddr` → `sip.native_dev_addr` (the old key is
  ignored) and API paths `/api/sip/devaddr/...` → `/api/sip/native-dev-addr/...`.
  The numeric `sip.dev_addr` is unrelated and unchanged; `sip.*` events now
  report it as `dev_addr`.
- Camera sessions now log why they ended (`stop requested`, `native ended the
  dialog`, `dialog connection lost`, `hold elapsed`) and skip the
  end-of-session still when the native side already ended the dialog — there is
  no video left to capture, and it used to burn the full 8 s timeout.
- Ending an RTSP view stops the native session ~10 s sooner: the
  end-of-session still is now captured during the teardown grace (video still
  flowing) instead of after the session had been torn down, where it burned
  the full 8 s timeout and published nothing. `sip.call.ended` is no longer
  published ahead of the session's own BYE, so the video tap stays armed for
  the capture.
- SIP self-healing: if Flexisip drops one of the bridge's connections (seen
  once after ~19 h of uptime), the bridge now re-dials and re-registers that
  identity with 1s→60s backoff instead of writing into the dead socket until
  the next restart. A per-connection generation counter ignores errors from
  already-replaced connections (no reconnect loops), the call state reports
  `Reconnecting`, and the listener log spam (one line every 2 s) is gone.
- Answering-machine leftovers on units without one (Classe 100X) no longer
  publish: the retained `voicemail` state, the `answering` device-config
  mirror, the message parser and the `/api/status` component report are all
  gone; `/api/status` says `message_parser: unavailable` instead.
- SIP status and system diagnostics now share a single liveness probe
  (`*#130**1*2##`) instead of polling the doorbell query (`*#8**33##`),
  whose answer is echoed on the monitor port and was logged as
  "Doorbell sound enabled (unmuted)" on every poll.
- MQTT: the bus frame entity is now named "OWN Bus (Event)", matching the
  other event labels (`Entrance Doorbell (Event)`, `Call (Event)`, …).
- Relay ingress bound to loopback (`127.0.0.1:10002/10000`); LAN RTP injection no longer possible, local tap/GStreamer/`*7*300` unaffected.
- RTSP idle-session reaper now releases relay consumers, GStreamer and self-call like the other teardown paths; server restart recreates its stop channel.

### MQTT topics v2 (breaking)

All data topics renamed to `bticino/<domain>/<object>` (bare noun = state;
`…/set` commands; `…/event` triggers; `…/attributes` structured data;
`…/image` snapshot) and every `object_id`/`unique_id` now mirrors its topic.
Migration: after upgrading, delete all stale v1 entities and discovery
topics once — retained v1 configs stay on the broker until removed, and HA
shows both generations side by side until then. Automations/dashboards must
move to the new entity IDs (see `docs/HOME_ASSISTANT.md`).

### Removed datetime MQTT topic; device info boot-only

- Removed `sensor.bticino_datetime` (`bticino/system/datetime`: a 60s clock
  feed nothing consumed). Delete the stale entity in HA manually.
- Removed `sensor.bticino_language`, `sensor.bticino_timezone` and
  `sensor.bticino_ntp` (static config mirror, republished every 60s).
- `sensor.bticino_system_info` attributes now also carry `bridge_version`
  (bridge binary version, static until redeploy).
- Removed `sensor.bticino_bus_event`: it duplicated `event.bticino_event_bus_own`
  (same frame, and was even published twice per frame). Raw bus frames are
  available on `bticino/event/bus_own` only now. Delete the stale entity in
  HA manually.
- Back-to-back duplicate OWN frames (e.g. the 5-minute bell-state keepalive)
  no longer republish `event.bticino_event_bus_own` or the activity log;
  the first occurrence of any frame still publishes.
- `bticino/system/device` (model/IP/version, static until reboot) is now
  published once at boot instead of every 60s.
- `sensor.bticino_system_info` IP refreshes on DHCP change (local interface
  read on the WiFi tick, zero bus traffic); all other facts stay boot-only.
- Camera brightness discovery follows configured TVCC IDs instead of
  hardcoded camera 20.
- `display/brightness` + `display/clean_time` merged into one `display/settings`
  facet topic (brightness + clean_time); `camera/<id>/config` moved to
  `camera/<id>/settings` with the native `TVCC ` ID prefix stripped (the old
  topic never received data — the publisher sent `camera/TVCC <id>/config`).
- Removed `sensor.bticino_multicast` (listener datagram counter, no
  consumers; the multicast listener itself is untouched — floor/entrance
  detection depends on it). Delete the stale entity in HA manually.
- Answering machine is now 300X-only (`bticino.AnsweringMachineAvailable()`):
  voicemail switch, message sensors, mark-all-read button, answering status
  entities and voicemail bus queries are skipped on Classe 100X (no UI,
  daemon never runs, memo dirs unused). Delete the stale entities in HA
  manually.
- Ringer, display and mute SET commands are now 300X-only
  (`bticino.RemoteSetCommandsWork()`): verified ACKed-but-ignored on Classe
  100X (ringer 3 variants, display power, 2026-10-13). Entities, command
  subscriptions and state queries for them are skipped on 100X.
- Removed `button.bticino_door_open` ("Door Opener"): identical bus sequence
  to `lock.bticino_door`, which stays as the single door control. Delete the
  stale entity in HA manually.
- Fixed ringtone parsing on 100X (`id_ringtone` lives on `<obj>`, not `<ist>`);
  added `sensor.bticino_ringtone_door` ("Floor Doorbell Ringtone",
  `Ringtone #…` templates); volume labels aligned ("Entrance/Floor Doorbell
  Volume").
- Added `sensor.bticino_ringtone_s1` and `sensor.bticino_volume_s1`
  ("Secondary Doorbell ...", secondary entrance station).
- Display names aligned to the Entrance/Floor doorbell scheme (topics and
  IDs unchanged).

### Home Assistant unique_ids renamed to model-neutral prefix (breaking)

All HA discovery `unique_id`s changed from `bticino_c300x_*` to
`bticino_intercom_*` so the same IDs work on Classe 300X and Classe 100X
(selected via `bticino.DeviceVariant`). `object_id`s (hence HA `entity_id`s),
state/command topics and payloads are unchanged. After upgrading, HA will
create new entities (e.g. `lock.bticino_door` gets a new unique ID backing
it) — delete the stale `*_c300x_*` entries from the MQTT integration if they
linger as unavailable.

### Home Assistant entities renamed from Spanish to English (breaking)

All HA `object_id`s (which drive HA `entity_id`s) are now English and match
their discovery IDs. Friendly `name`s were already English. After upgrading,
HA will create new entities (e.g. `sensor.bticino_temperature` instead of
`sensor.bticino_temperatura`) — update automations/dashboards and delete the
stale Spanish entities:

- `bticino_puerta` → `bticino_door`, `bticino_contestador` → `bticino_voicemail`,
  `bticino_sonido_timbre` → `bticino_doorbell_sound`,
  `bticino_pantalla` → `bticino_display`,
  `bticino_luz_escalera` → `bticino_staircase_light`,
  `bticino_abrir_puerta` → `bticino_door_open`,
  `bticino_temperatura` → `bticino_temperature`,
  `bticino_mensajes_nuevos` → `bticino_new_messages`,
  `bticino_total_mensajes` → `bticino_total_messages`,
  `bticino_almacenamiento` → `bticino_storage`,
  `bticino_teclado` → `bticino_keypad`, `bticino_timbre` → `bticino_doorbell`,
  `bticino_evento_bus` → `bticino_bus_event`,
  `bticino_estado_sip` → `bticino_sip_status`,
  `bticino_diagnostico` → `bticino_system_diag`,
  `bticino_actividad` → `bticino_activity_log`,
  `bticino_info_sistema` → `bticino_system_info`,
  `bticino_idioma` → `bticino_language`,
  `bticino_answering` → `bticino_answering_status`,
  `bticino_camera_20` → `bticino_camera_20_brightness`,
  `bticino_estado_llamada` → `bticino_call_state`,
  `bticino_quien_llama` → `bticino_call_caller`,
  `bticino_timbre_rellano` → `bticino_doorbell_floor`,
  `bticino_evento_timbre` → `bticino_event_doorbell`,
  `bticino_evento_timbre_rellano` → `bticino_event_doorbell_floor`,
  `bticino_evento_llamada` → `bticino_event_call`,
  `bticino_evento_bus_own` → `bticino_event_bus_own`,
  `bticino_ip` → `bticino_ip_address`, `bticino_mac` → `bticino_mac_address`,
  `bticino_wifi` → `bticino_wifi_signal`,
  `bticino_marcar_todos_leidos` → `bticino_mark_all_read`,
  `bticino_reiniciar_bridge` → `bticino_restart_bridge`,
  `bticino_reiniciar_dispositivo` → `bticino_reboot_device`,
  `bticino_camara_timbre` → `bticino_doorbell_camera`
  (so `camera.camara_timbre` → `camera.doorbell_camera`).
- LED names now English (`LED Message`, `LED Answering Machine`, `LED Lock`,
  `LED Missed Call`, `LED Green Call`, `LED Red Call`); default extra-lock name
  `Cerradura N` → `Lock N`.
- MQTT payloads now English: keypad values (`key`/`star`/`eye`/`phone`),
  `activity_log` descriptions, `system_diag` startup value (`starting`),
  `system_info` fallback (`unknown`), IP/MAC `value_template` defaults
  (`unknown`). MQTT topics are unchanged.
- Docs (`docs/HOME_ASSISTANT.md`, `docs/ha_config/README.md`) updated to the
  English entity IDs.
## [0.29.4] - 2026-10-16

### Corregido
- **`AudioCall.Say` (talk-back de WAV) no colgaba.** El diálogo quedaba vivo hasta
  que el nativo lo cortaba, y el vigilante del cliente SIP retenía mientras tanto el
  socket webrtc (re-REGISTER aplazado). Ahora ocupa la pata única
  (`ErrAudioLegBusy`) y cuelga siempre al terminar, también si el answer SDP es
  inválido (no deja diálogo huérfano).
- **`MakeCall` (vídeo bajo demanda) roto desde la 0.29.0.** El listener c300x dejó
  de auto-contestar TODAS las llamadas que originamos, pero solo las de
  `InviteWithSDP` (ojo, AudioCall) deben ir al nativo. Nuevo registro
  `nativeCalls` para esas; el self-INVITE de vídeo vuelve a auto-contestarse en
  loopback. Solo afecta con `streaming.video_on_demand: true`.
- **Pulsar "Ver placa" con un ojo ya abierto** daba el log engañoso "llamada
  nativa/contestador activa" (el propio ojo enciende el canal nativo) y publicaba
  `panelview.rejected`, que ponía el sensor HA en `idle` con el ojo abierto. Ahora
  se comprueba antes el ojo propio: `ErrEyeBusy`, log Info "ya hay un ojo activo",
  sin tocar el sensor.

## [0.29.3] - 2026-10-16

### Corregido
- **La tarjeta de HA no pintaba vídeo** aunque el vídeo llegara al relay. Causa
  (reproducida en local con el mismo comando): el stream `doorbell` de go2rtc es un
  ffmpeg que transcodifica el audio (`-c:a pcm_alaw`) y **no emite nada —ni vídeo—
  hasta recibir el primer paquete de audio válido**; sin él, go2rtc mata el exec
  por timeout (`ERR [exec] timeout`). Desde que `streaming.live_audio_on_call` quedó
  en `false` (09-oct) la pista de audio del RTSP iba vacía, y el OÍR del ojo salía
  con PT98 en vez del PT110 del SDP (ffmpeg lo descartaba).
- **Pista de audio siempre viva** (`pkg/sip/rtp_relay_fill.go`): el relay de audio
  reescribe todo con un solo SSRC, secuencia continua y PT110, e inyecta silencio
  speex cada 20 ms si no llega audio (mismas tramas que `talk_keepalive`; validado
  que ffmpeg las decodifica y empieza a emitir). El vídeo ya no depende de que haya
  una fuente de audio activa.
- **OÍR del ojo con prioridad:** PanelView reenvía a una entrada prioritaria del
  relay (`127.0.0.1:10012`); mientras llegue audio por ahí se descarta el tap de
  `lo:5000` (mudo en modo ojo), así que en HA se oye la calle y nunca se mezclan dos
  flujos.

- **La imagen tardaba hasta ~90 s (o no llegaba) en la tarjeta.** En reposo el RTSP
  no emitía vídeo, el ffmpeg de go2rtc moría por timeout a los 30 s y go2rtc lo
  relanzaba cada ~90 s; si el ojo (30 s) caía en el hueco no había imagen (visto en
  vivo el 16-oct). Ahora el relay de vídeo emite un **fondo negro H.264 a 1 fps** en
  reposo (`pkg/sip/rtp_relay_vfill.go`, IDR embebido con la misma cabecera que la
  placa: Constrained Baseline 3.0, 688x480), con un solo SSRC y secuencia continua;
  el vídeo real conserva marker y separación de timestamps y desplaza al fondo al
  instante. Validado en local de punta a punta con el comando de go2rtc: ffmpeg se
  mantiene conectado en reposo y pasa fondo → vídeo real → fondo sin errores.

### Notas
- En el device se volvió a poner `live_audio_on_call: true` (arreglo inmediato, con
  copia `config.yaml.bak-pre-live-audio-202610162317`). Con esta versión ya no es
  imprescindible para ver la imagen, pero sigue aportando el audio de lo:5000 en
  las llamadas normales.

## [0.29.2] - 2026-10-16

### Corregido
Tres fallos SIP encadenados que el gate en vivo de la v0.29.1 (22:47) destapó: el
primer ojo iba perfecto (30 s completos), pero el segundo daba timeout.
- **El BYE no llegaba al `bt_vct`.** Iba a `sip:c300x@dominio` sin `Route`; Flexisip
  no lo entregaba y el nativo seguía en llamada hasta su `nortp_timeout` (~30 s),
  contestando **486** a cualquier ojo en ese margen (también los 486 del 09-oct).
  Ahora ACK y BYE van al **Contact** del 200 OK con **Route = Record-Route
  invertido** (RFC 3261 §12.1.2).
- **El listener c300x ignoraba la rama del fork con nuestro propio INVITE.**
  Flexisip esperaba esa rama y no reenviaba la respuesta final del nativo (su 486):
  el ojo acababa en timeout de 30 s. Ahora contesta **480** (To-tag estable por
  Call-ID; un 2xx del nativo prevalece). El ACK de ese 480 y un BYE de diálogo
  propio ya no tocan el estado de sesión.
- **Dos lectores por socket.** El re-REGISTER leía el socket webrtc a la vez que el
  INVITE o el vigilante (`Failed to refresh webrtc registration`), y en c300x el
  listener se comía la respuesta del re-REGISTER (`Failed to refresh c300x
  registration ... i/o timeout` cada ~4-5 min, desde antes de la 0.29). Ahora cada
  socket tiene un único lector: en c300x el listener entrega las respuestas por
  canal; en webrtc el INVITE y el vigilante poseen el lector y el re-REGISTER lo
  intenta sin bloquear (se aplaza si está ocupado). La renovación se comprueba
  cada 30 s y se hace a mitad de la expiración (margen de sobra para reintentos).
- **Endurecido tras revisión independiente:** el bucle del INVITE **traspasa** el
  lector al vigilante del diálogo (no lo suelta), así que el vigilante nunca lo
  espera (sin interbloqueo posible con `stopDialogWatch`) y el re-REGISTER no se
  cuela entre el ACK y la vigilancia; la respuesta del REGISTER se casa por CSeq
  completa (número + método) y se vacían respuestas rezagadas antes de enviar;
  timeout del REGISTER a 3 s (Flexisip es local); aviso Warn si el refresco lleva
  tiempo aplazado y el registro se acerca a caducar; un diálogo entrante
  auto-contestado ya no hereda la ruta de un ojo previo.

### Notas
- Regresión latente detectada, sin tocar: desde la 0.29.0 `MakeCall` (self-INVITE
  de vídeo bajo demanda) registra su Call-ID en `originatedCalls` y el listener ya
  no lo auto-contesta. Solo afecta con `streaming.video_on_demand: true`
  (desactivado por defecto y en el device).

## [0.29.1] - 2026-10-16

### Corregido
- **"Ver placa" colgaba nada más conectar (BYE inmediato).** Causa raíz
  (diagnosticada en estático con `/tmp/linphone.log` del device, sin prueba en
  vivo): `InviteWithSDP` y `MakeCall` aceptaban el **primer** `200` que leían del
  socket webrtc sin comprobar Call-ID ni CSeq. `Hangup()` envía el BYE sin
  consumir su `200`, así que en el siguiente ojo ese `200` sin SDP se tomaba como
  respuesta → `ParseAudioAnswer` fallaba → BYE. El `200` real del `bt_vct` se
  quedaba sin ACK y el nativo lo retransmitía ~32 s (vídeo "huérfano", rechazos
  `callActive`, 486 Busy), envenenando también el intento siguiente. Ahora solo se
  acepta una respuesta con nuestro Call-ID y CSeq de INVITE, y a los BYE de otros
  diálogos que llegan por ese socket se les contesta `200`.
- **El ojo se entera de que el nativo lo cuelga.** Tras el ACK nadie leía el
  socket webrtc: el BYE del `bt_vct` se quedaba sin `200` (retransmitido) y el
  sensor seguía en `viewing` con OÍR/HABLAR armados hasta el auto-off. Ahora un
  vigilante del diálogo (`pkg/sip/dialog_watch.go`) contesta `200` al BYE remoto y
  publica `sip.dialog.ended{call_id, reason: remote_bye}`; PanelView desmonta sin
  BYE propio (`panelview.ended` con `reason: remote_bye`). El vigilante se para y
  se espera **antes** de cualquier `InviteWithSDP`/`MakeCall`/`Hangup`: nunca hay
  dos lectores en el socket.
- **PanelView ya no se traga los errores:** un INVITE fallido o un answer SDP
  inválido se loguean a nivel Warn (antes `Show()` se invocaba desde el bus con el
  error descartado y el fallo era invisible).

### Notas
- Análisis estático de la app oficial 1.17.3 (jadx): el micro se silencia en
  local (sin re-INVITE ni trama OWN), el ojo no tiene timeout en la app, y un
  INVITE entrante durante el ojo se rechaza con 486 (`max_calls=1`) sin colgarlo.
  Contrastado con el bridge: la subida continua ya existe (`talk_keepalive`,
  v0.27.0) y el answer del timbre ya va con audio `sendrecv`. Se mantiene la
  cesión del ojo a un timbre real (no 486): prima la visita.

## [0.29.0] - 2026-10-08

### Añadido
- **"Ver placa" (`PanelView`): autoaccensión a demanda desde HA.** Un botón HA
  (`bticino_ver_placa`) publica `panelview.request` en el bus; el subsistema
  origina un INVITE "de ojo" hacia el c300x con `DEVADDR` (la dirección OWN de la
  placa de calle) y vídeo `recvonly` / audio `sendrecv`, para que la placa se
  encienda sin que nadie pulse en el monitor. Sensor HA **Placa en vista**
  (`bticino_placa_en_vista`) con estados `viewing` / `idle` (`dp/panelview/state`).
- **Tres pistas de media, ya cableadas:**
  - **VER:** tap pasivo de `lo:5007` armado por `sip.call.connected`, igual que
    la llamada normal.
  - **OÍR:** `AudioReceiver.CaptureForward` descifra el SRTP de bajada en línea y
    lo reenvía a relé `127.0.0.1:10000`.
  - **HABLAR:** `TalkSender` se arma con `panelview.connected` (modo ojo). Requiere
    `sip.answerer.talk_enabled: true`.
- **Ciclo de vida:** auto-off a ~30 s (`timeout_seconds`) con BYE; cede a un
  timbre real (un timbrazo entrante tiene prioridad sobre el ojo); el listener
  c300x ignora los INVITE originados por PanelView (correlación de Call-ID) para
  no auto-contestarse.
- **Fail-closed:** bloque nuevo `streaming.panelview` con `enabled: false` por
  defecto. Sin `devaddr` no dispara. Ver `docs/panel-view-setup.md`.

### Notas
- **Requiere añadir el bloque `streaming.panelview` al `config.yaml` del device**
  (el deploy no sube el config), y `sip.answerer.talk_enabled: true` (con
  `sip.answerer.enabled: true`) para que HABLAR funcione.
- **Gate en vivo pendiente.** Esta versión no está validada en vivo. El gate
  debe confirmar dos cosas: (1) que el INVITE con `DEVADDR` llega al `bt_vct`
  nativo y enciende la placa sin reiniciar el c300x (sonda de uptime), y (2) que
  el `DEVADDR` se captura de un timbrazo real en `/tmp/linphone.log`. Hasta
  entonces, `enabled` debe quedarse en `false` en el device.
- **No-interferencia:** el patrón del bridge no cambia (monitor en 20000,
  comandos por `nc` a localhost). Hay que confirmarlo en el gate.

## [0.28.0] - 2026-10-06

### Añadido
- **Tipo de llamada entrante en HA.** El contestador clasifica cada INVITE por su
  SDP, con la misma señal que usa la app oficial 1.17.3 (si el remoto trae
  vídeo): `placa` si hay `m=video` con puerto distinto de 0, `intercom` si solo
  hay audio, `desconocido` si no hay líneas `m=` legibles
  (`pkg/sip/call_type.go`). `doorcall.incoming` lleva `call_type`, y el sensor
  HA **Contestador** lo expone como atributo (`json_attributes_topic`
  `bticino/answerer/attributes`, publicado antes del estado `ringing`).
- Alcance honesto: solo la llamada de placa está respaldada por capturas.
  `intercom` es conjetura (no hay captura de una llamada entre monitores) y en
  una instalación con un único monitor interior saldrá siempre `placa`. El
  contestador como llamante no se modela: no hay evidencia de que llegue por SIP.

### Mantenimiento
- `go test -race ./...` y `gofmt -l` ahora pasan limpios en todo el bridge: la
  carrera estaba en `pkg/events/bus_test.go` (estado compartido con el handler
  sin sincronizar), no en el EventBus; formateados `talkback.go`,
  `media_events.go`, `config_manager.go` y `talkback_say_test.go` (solo formato).

## [0.27.0] - 2026-10-06

### Añadido
- **Subida de audio continua (`answerer.talk_keepalive`).** Ataca el bloqueo del
  gate F1 en vivo: se contesta y se ve, pero no se oye al visitante hasta abrir la
  puerta. Hipótesis RTP simétrico: la placa no manda el audio de bajada hasta
  recibir audio de subida. Nuevo relé RTP `upstreamKeepalive`
  (`pkg/sip/talk_keepalive.go`) delante de ffmpeg: escucha el micro de HA
  (backchannel go2rtc :40004) y emite un flujo **continuo** hacia ffmpeg
  (:40006): reenvía la voz al instante y, si el micro calla más de 60 ms, inyecta
  una trama speex de silencio cada 20 ms (38 bytes, generada con libspeex).
  Secuencia/SSRC propios y timestamp de reloj real. ffmpeg no cambia (copy +
  SRTP); solo lee del relé, así nunca se queda sin paquetes.
- Mezclar en ffmpeg con `amix` se descartó: comprobado que se bloquea si la
  entrada del micro no trae paquetes, y no sale ni el silencio.
- **Fail-closed:** `talk_keepalive: false` por defecto ⇒ comportamiento idéntico
  al anterior. Requiere `talk_enabled: true`. Si el relé no arranca, cae al camino
  clásico (hablar sin keepalive mejor que no hablar).
- **Para probarlo en el device** hay que añadir `talk_keepalive: true` al
  `config.yaml` del device (el deploy no sube el config).

### Notas
- Validado en local con un ffmpeg real (SRTP continuo sin micro). Falta lo único
  que no se puede probar sin timbrazo: que la placa abra el audio de bajada.
- Reemplaza al experimento de `talk_sender` (silencio en lugar del micro), que
  queda en la rama `experimento-talk-silencio`: este keepalive manda silencio
  **y** conserva el micro de HA.

## [0.26.2] - 2026-10-06

### Corregido
- **Contestador atascado en RINGING (guarda de timbre).** No había temporizador
  mientras sonaba: si nadie contestaba desde HA y el CANCEL de la placa se
  perdía, el contestador se quedaba sonando para siempre y respondía `486 Busy`
  a todos los timbrazos siguientes hasta reiniciar el bridge. Ahora, a los
  `ring_timeout_seconds` (default **70**, el mismo valor que usa la app oficial
  1.17.3 para la llamada entrante), cierra el INVITE con `480 Temporarily
  Unavailable` y vuelve a idle. Se desarma al contestar, al cancelar y en
  `Stop()` (sin esto la guarda podía dispararse tras parar el bridge y publicar
  en el bus ya cerrado → panic; cubierto por test). Default en código: el device
  no necesita cambiar su `config.yaml`.

### Cambiado
- **Vídeo `recvonly` en el answer del contestador.** El SDP declaraba el vídeo
  `sendrecv`, pero el bridge solo recibe el vídeo de la placa (no tiene cámara).
  La app oficial 1.17.3 contesta con `VideoDirection RecvOnly`. No afecta al
  vídeo que llega a HA (tap pasivo / RTSP).

### Notas
- `max_call_seconds` (120 s) ya limitaba la llamada contestada; sin cambios.
- Origen: análisis jadx de la app oficial 1.17.3
  (`_investigacion/hack/DoorEntry_1.17.3_jadx/`).

## [0.26.1] - 2026-08-24

### Corregido
- **Crash del contestador (F1) al contestar en el device (ARM32).** `mediaDrain`
  (`pkg/sip/answerer_drain.go`) incrementaba contadores `int64` con
  `atomic.AddInt64` sobre campos que, tras tres punteros (12 bytes en ARMv7),
  quedaban alineados a 4 y no a 8 bytes → `panic: unaligned 64-bit atomic
  operation` que tumbaba **todo el proceso** justo al armar el drenaje de media
  en una llamada real (invisible en los tests, que corren en amd64 donde los
  atómicos de 64 bits siempre están alineados). Detectado en el gate F1 en vivo.
  Arreglado migrando los cuatro contadores a `atomic.Int64` (Go 1.19+), que el
  compilador alinea a 8 bytes en todas las plataformas. `RTPRelay` ya evitaba
  esto colocando sus contadores como primeros campos; `mediaDrain` era el único
  sitio afectado.

## [0.26.0] - 2026-08-23

### Añadido
- **F2: hablar desde Home Assistant durante la llamada contestada.** Completa
  el contestador de v0.25.0 con el sentido que faltaba (subida): con
  `sip.answerer.talk_enabled: true`, al contestar (`doorcall.answered`) el
  bridge lanza un `ffmpeg` hijo que recibe el backchannel de go2rtc (el botón
  de micro de la tarjeta WebRTC HA, RTP speex sin cifrar en
  `127.0.0.1:40004`), lo re-etiqueta al PT del answer y lo reenvía como SRTP
  (con la clave que el propio bridge anunció en el SDP) al puerto de audio de
  la placa — sin transcodificar (`-acodec copy`). Se mata en `doorcall.ended`
  o en el shutdown del proceso.
- `pkg/sip/talk_sdp.go` (`BuildBackchannelSDP`): SDP que describe, para
  ffmpeg-como-receptor, el RTP speex/8000 que llega del backchannel de go2rtc.
- `pkg/sip/talk_sender.go` (`TalkSender`, `NewTalkSender`, `buildFFmpegArgs`):
  se suscribe a `doorcall.answered`/`doorcall.ended` vía el `EventBus`; lanza
  el ffmpeg de talk-back a través de un `ProcRunner` inyectado (testeable sin
  proceso real) y guarda `Proc` + el SDP temporal bajo mutex; `Stop()`
  idempotente. **Un fallo del ffmpeg (no arranca, muere, sale con error) nunca
  tumba la llamada contestada**: se loguea y se ignora.
- `pkg/sip/proc_runner.go` (`ProcRunner`, `Proc`, `ExecRunner`,
  `NewExecRunner`): runner real basado en `os/exec`, inyectado en
  `cmd/main.go`; en los tests de `TalkSender` se sustituye por un fake sin
  tocar el sistema.
- `cmd/main.go`: dentro del bloque del Answerer (F1), tras `answerer.Start()`
  y `webServer.SetAnswerer(answerer)`, si `cfg.SIP.Answerer.TalkEnabled`
  construye `sip.NewTalkSender(cfg.SIP.Answerer, sip.NewExecRunner(),
  eventBus, logger, "/usr/local/bin/ffmpeg", "127.0.0.1", 40004, 97)` y lo
  arranca (`talkSender = nil` si `Start()` falla, sin abortar el contestador).
  `var talkSender *sip.TalkSender` declarado junto a `var answerer
  *sip.Answerer`; en el shutdown, `talkSender.Stop()` corre **antes** de
  `answerer.Stop()` para no dejar un ffmpeg huérfano si el answerer cae primero.

### Notas
- **Objetivo norte cubierto en código:** oír + ver + **hablar** desde HA
  durante una llamada, sin la app, queda implementado de punta a punta con
  esta pieza. **Pendiente el gate en
  vivo** (contestar desde HA + pulsar el micro de la tarjeta WebRTC +
  confirmar que se oye por la placa) — no se ha validado contra el device
  real todavía; ver `docs/answerer-setup.md` §Hablar (F2).
- **Fail-closed:** `talk_enabled: false` por defecto (config ya existía desde
  F1); esta versión no cambia `configs/config.yaml`, solo activa el camino de
  código que ese flag gatea. Como siempre, `deploy-standard.sh` no pushea
  `config.yaml` — hay que poner `talk_enabled: true` a mano en el device.
- **ffmpeg del device:** se reutiliza la misma ruta absoluta que ya usan
  `audio_sender`/`audio_receiver`/`audio_tap_decode`/`greeting_handlers`
  (`/usr/local/bin/ffmpeg`, estático, con `libspeex` y protocolo `srtp`), no
  el `ffmpeg` del PATH del servicio.
- **No-interferencia:** el bridge no abre sockets propios nuevos; el único
  socket adicional es el que abre el propio ffmpeg hijo (`127.0.0.1:40004` de
  entrada, `srtp://<host>:<audioPort>` de salida hacia la placa); no se toca
  `client.go` ni la config de go2rtc (el backchannel `exec:` hacia `:40004` ya
  estaba cableado desde antes).

## [0.25.0] - 2026-08-23

### Añadido
- **F1: contestador SIP bajo demanda desde Home Assistant.** El bridge puede
  ahora responder la llamada SIP entrante del c300x (el fork que hace hacia
  Flexisip al pulsar el timbre) sin necesidad de la app: `button.bticino_contestar`
  / `button.bticino_colgar` + `sensor.bticino_contestador` (nuevo, MQTT) junto al
  vídeo/audio ya expuestos por los taps pasivos de v0.24.x. Sin auto-answer: la
  llamada solo se contesta si un humano pulsa el botón desde HA.
- `pkg/sip/answerer.go` (`Answerer`, FSM completa) + `pkg/sip/answerer_tcp.go`
  (`TCPTransport`, registro/transporte contra el Flexisip local por TCP) +
  drenaje de media SRTP (audio/vídeo) hacia los puertos locales configurados
  (`sip.answerer.audio_port`/`video_port`) para que los taps de v0.24 puedan
  seguir sirviendo vídeo/audio a HA durante la llamada contestada.
- `pkg/sip/route_check.go` (`CheckAnswererRoute`): comprobación **read-only** de
  que el AOR (`sip.answerer.user@sip.domain`) está dado de alta en
  `route_ext.conf` de Flexisip; si falta, solo avisa (no bloquea el arranque).
- `cmd/main.go`: wiring del contestador — flag `-enable-answerer` (default
  `true`), gate `cfg.SIP.Answerer.Enabled = cfg.SIP.Answerer.Enabled &&
  cfg.SIP.Enabled && *enableAnswerer`, construcción (route-check + `TCPTransport`
  + `Answerer.Start()` + `webServer.SetAnswerer`) fuera del bloque `if
  cfg.SIP.Enabled` para que compile igual con SIP apagado, y parada en el
  shutdown **antes** de `sipClient.Stop()` (para que la pata del contestador
  mande BYE primero si hay una llamada en curso).
- Config `sip.answerer` (nuevo bloque, **fail-closed**: `enabled: false` por
  defecto): `user`, `audio_port`, `video_port` (`-1` = rechazar `m=video`,
  audio-only), `max_call_seconds` (guard anti-placa-ocupada), `talk_enabled`
  (reservado para F2, aún inerte), `route_file`.
- `docs/answerer-setup.md`: runbook de puesta en marcha (alta de la ruta en
  Flexisip con `scripts/setup_answerer_route.sh`, reinicio de Flexisip —
  **nunca SIGHUP**, bloque de config del device, tarjeta de dashboard HA
  `portero-bticino`, automatización opcional de notificación móvil).

### Notas
- **F2 (hablar) queda pendiente.** `talk_enabled` existe en config pero no
  tiene implementación todavía: el contestador solo drena/expone audio+vídeo
  entrantes (oír/ver), no inyecta el backchannel del operador de HA hacia la
  puerta. Se abordará en una fase posterior.
- **Fail-closed en cascada:** con `sip.answerer.enabled: false` (default) el
  bloque de wiring en `main.go` no hace nada — ni abre el `TCPTransport` ni
  registra rutas en el webserver. `deploy-standard.sh` **no pushea
  `config.yaml`**: hay que dar de alta el bloque `sip.answerer` (con
  `enabled: true`) a mano en el device tras registrar la ruta, ver
  `docs/answerer-setup.md`.
- No-interferencia: el contestador solo abre una conexión TCP a
  `127.0.0.1:5060` (el Flexisip local, mismo patrón que el resto del pipeline
  SIP) y sockets UDP de drenaje en `10100`/`10102`; cero puertos nuevos hacia
  el firmware nativo (`30006`/`30007`/vídeo intactos).
- **Riesgo residual sin confirmar (F0):** la coexistencia con el monitor
  interior mientras el contestador tiene la llamada abierta no se ha validado
  en vivo todavía; ver `docs/answerer-setup.md` §5.

## [0.24.0] - 2026-08-22

### Añadido
- **Armado de las tres pistas de media por canal-up nativo.** Las tres pistas
  (`live-video`, `live-audio`, `audio-listen`) ahora arman también en el evento
  `media.channel.up`, publicado cuando el monitor OWN del bus recibe el frame
  de canal-up nativo (`*7*300#…#5000#*` audio / `*7*300#…#5007#*` vídeo),
  detectado por el nuevo helper `bticino.IsMediaChannelUp`.
- Motivo (validación en vivo B1, 2026-08-22): abrir cámara + micrófono desde la
  app es **auto-activación nativa** (OpenWebNet WHO=7/8), NO una llamada SIP, así
  que los disparadores existentes `sip.call.*`/`doorbell.pressed` no se activan
  y las pistas nunca arman aunque el media limpio está en `lo:5000`/`lo:5007`.

### Notas
- Robustez frente al retraso: el canal-up llega tras el `*8*3#5#4*416##` rezagado,
  evitando el teardown prematuro. Desarme (`call.hangup`/`sip.call.ended`) y guard
  de 5 min sin cambios **intactos**.
- **Disparadores SIP y doorbell sin cambios:** el armado sigue respondiendo a
  `doorbell.pressed` e `sip.call.incoming`/`sip.call.connected`, en paralelo con
  el nuevo `media.channel.up`.
- No-interferencia **total:** cero comandos nuevos al bus; **solo** reacción a lo que
  el device anuncia (Fase 0 intacta). El helper `IsMediaChannelUp` es puro (sin
  efectos secundarios).
- **Requisito operativo:** requiere que la config del device tenga
  `streaming.live_audio_on_call: true` (introducido en v0.23.0). **Nota crítica:**
  `deploy-standard.sh` sube el binario pero NO la config; hay que asegurar que el
  `config.yaml` en el device incluya el bloque `streaming:` con ese flag.

## [0.23.0] - 2026-08-21

### Añadido
- **B1: oír + ver la puerta desde Home Assistant.** El audio de la puerta (capturado
  en v0.22.0) se enruta al **relay de audio del RTSP** (`10000`) para que el stream
  `/doorbell` lleve **vídeo + audio**, y go2rtc (que ya sirve `/doorbell` con
  `audio=pcma`) lo publique a HA por WebRTC. **Cero decode/transcode en el bridge**
  (el códec Speex/8000 PT110 ya casa con la pista de audio del RTSP).
- `pkg/sip/live_audio.go` (`LiveAudioOnCall`): espejo pasivo de `LiveVideoOnCall`
  (tap `lo:5000` → relay `10000`), armado en los mismos eventos de llamada, con el
  **fix del leak Start/Stop** incorporado de fábrica. Sin `CallActiveMarker` (el
  audio no dispara self-INVITE).
- Config `streaming.live_audio_on_call` (**fail-closed**).

### Notas
- No-interferencia: tap AF_PACKET **pasivo** en `lo` (como el de vídeo); alimentar
  `10000` es escribir al relay del propio bridge, no al device nativo. go2rtc no se
  toca. Coexiste con el tap de validación WAV de v0.22.0 (`5000→11000`) sin conflicto.
- **Requisito operativo:** el `5000` solo emite con parte de red + audio 2-vías
  activo en la llamada (heredado de v0.22.0).
- **Validación en vivo PENDIENTE (2026-08-22):** el código está review-clean (mirror
  mecánico del tap de vídeo ya en producción; puertos 5000→10000 verificados; go2rtc
  ya sirve `/doorbell` con audio) y las piezas están validadas por separado (captura
  del audio en v0.22.0), pero la confirmación end-to-end "se oye en HA vía go2rtc"
  quedó pendiente por indisponibilidad de la app en el momento del merge. A confirmar:
  (a) que el tap arma durante una llamada real (que disparen `doorbell.pressed`/
  `sip.call.*`), (b) que HA reproduce el audio junto al vídeo.

## [0.22.0] - 2026-08-21

### Añadido
- **"Oír" la puerta vía tap pasivo de `lo:5000`** (corrige la recepción que v0.21.0
  no lograba). El reversing on-device mostró que el audio Speex de bajada de la
  puerta sale **EN CLARO por el puerto de loopback `5000`** (RTP PT=110), análogo
  al vídeo en `5007`, **solo cuando hay una parte de red en la llamada** (la app).
- `pkg/sip/audio_tap_decode.go` (`AudioTapDecoder`): decodifica el RTP Speex **en
  claro** (`RTP/AVP`, sin crypto) a WAV vía ffmpeg leyendo un SDP generado.
- `pkg/sip/audio_listen_on_call.go` (`AudioListenOnCall`): arma el tap de `5000` +
  el decoder en los **eventos de llamada** (espejo de `LiveVideoOnCall`), sin
  auto-disparar ninguna pata. Reutiliza el `LoopbackRTPTap` genérico del vídeo.
  Constante `NativeAudioRTPPort = 5000`.
- Endpoints `/api/audio/listen/{start,stop,status}` (tras Basic Auth) para forzar
  el armado en validación. Config `audio.listen_tap_enabled` (fail-closed) +
  `audio.listen_sink_path` (sink WAV propio, distinto del de talk-back).

### Notas
- **De-risk del disparo (Tarea 1):** la pata propia del bridge NO dispara la
  emisión a `5000` (el self-INVITE es inestable y su audio suena en la unidad
  interna, no en la calle). El `5000` emite con una parte de red real en la
  llamada → el tap se arma en los eventos de llamada. Incógnita para validación:
  ¿emite ya mientras suena, o solo al contestar en red? (necesita timbrazo físico).
- No-interferencia: tap AF_PACKET **pasivo** en `lo` (como el de vídeo) + decode
  RTP local en `127.0.0.1:11000`; sin sockets nuevos a puertos del device. El
  vídeo (`5007`) no se toca. Fail-closed.
- **Validado on-device (2026-08-21):** con la app manteniendo comunicación **con
  el micro activo** (audio 2-vías), el tap captura el Speex de `5000`, lo reenvía a
  `11000` y ffmpeg lo decodifica a un WAV con **audio de calle real** (200 KB/12 s,
  mean −44.6 dB / max −6.4 dB). Requirió `-flush_packets 1` en el decoder (ffmpeg
  bufferiza la salida; sin ese flag el WAV sale a 0 bytes al parar el decoder).
  Requisito operativo: el `5000` solo emite con una **parte de red y audio 2-vías**
  activos en la llamada (con solo vídeo o micro cerrado, no hay audio que capturar).

## [0.21.0] - 2026-08-21

### Añadido
- **Audio full-duplex en la pata propia del bridge** (cimiento del objetivo
  "contestar el interfono desde HA", subproyecto A): el bridge ahora puede
  **oír** el audio Speex/SRTP de la puerta además de **hablarle**, en su propia
  pata SIP de audio a `c300x` (arquitectura β2). Reutiliza el talk-back v2
  (envío) y añade la recepción.
- `pkg/sip/audio_receiver.go` (`AudioReceiver`): recibe el SRTP entrante y lo
  vuelca a un WAV vía ffmpeg leyendo un SDP generado (`BuildInboundSDP`),
  escuchando en el puerto LOCAL del bridge y descifrando con la clave del answer.
- `AudioCall.OpenDuplex/Close`: sesión full-duplex con **una sola pata activa**
  (`ErrAudioLegBusy`), auto-teardown cuando el receiver expira (reset + BYE), e
  `IsActive()` para estado autoritativo.
- Endpoints de validación local `POST /api/audio/duplex/start|stop`,
  `GET /api/audio/duplex/status` (tras el Basic Auth; **nunca** exponen la clave
  SRTP).
- Bloque de config `audio:` (`duplex_enabled` fail-closed, `duplex_sink_path`).

### Notas
- **β2 justificada por reversing** (2026-08-21): el audio de la puerta NO viaja
  por IP en el loopback (va por DSP/bus SCS cuando se contesta en el panel); solo
  el vídeo se paquetiza. Para oír, el bridge debe ser parte de red (pata propia).
  No hay vía pasiva de audio. El vídeo sigue siendo tap pasivo, intacto.
- Plumbing P1 (dos ffmpeg, sin dependencias). Fallback P2 (`pion/srtp`) previsto.
- **Validación on-device (2026-08-21):** funcionan el plumbing full-duplex, **hablar**
  (talk-back v2), el cierre limpio, la **coexistencia segura** (con panel/app en la
  llamada, `start` falla limpio sin dejar pata colgada) y el fail-closed. **El "oír" NO
  captura todavía** con el mecanismo de esta versión (receiver en el puerto SIP → 0 bytes):
  el reversing mostró que el audio de la puerta sale **en claro por el puerto de loopback
  `5000`** (análogo al vídeo en `5007`), solo cuando hay una parte de red en la llamada. La
  recepción correcta es un **tap pasivo de `5000`**, que llega en un incremento aparte. La
  decisión P1/P2 queda obsoleta.
- No-interferencia respetada: solo la pata SIP local + `nc` a 30006 (WHO=8); sin
  sockets nuevos; vídeo sin tocar. Fail-closed: dúplex OFF salvo config explícita.

## [0.20.0] - 2026-08-21

### Añadido
- **Clasificador de accesos → HA** (`pkg/access`): clasifica cada apertura del
  portal como `telefonillo` (correlacionada con timbre/llamada previos dentro de
  `correlation_window`), `llave` (sin correlación, RFID/residente) o
  `desconocido` (señales en conflicto). La duración del pulso de cerradura ajusta
  la confianza (`alta`/`baja`/`media`), no la etiqueta.
- Nuevas entidades HA: entidad `event` "Acceso portal" (event_types
  telefonillo/llave/desconocido, para automatizaciones) y sensor "Último acceso"
  con atributos (confianza, hubo_llamada, duración, where, timestamp). Primer uso
  de la plataforma `event` en el bridge.
- Bloque de config `access:` (`enabled`, `correlation_window`, `max_hold_wait`,
  `duration_visitor_max`).

### Notas
- Componente 100% pasivo: solo escucha el EventBus interno; no abre sockets ni
  envía comandos al bus (respeta la no-interferencia).
- La señal es a nivel de edificio (no identifica piso). `duration_s` y `had_call`
  viajan en los atributos para validar el umbral contra el histórico de HA.

## [0.19.0] - 2026-08-20

### Talk-back to the DOOR panel via the answering-machine greeting (v1)

Playing bridge audio out the **door station speaker** (so a visitor hears us) is
now possible — through the device's own answering-machine greeting, the only
path that reaches the door on this hardware. Endpoint: `POST/GET/DELETE
/api/talkback/greeting`.

- **Why not the obvious routes** (all reverse-engineered and ruled out live):
  - *Local SIP self-call* (`/api/talkback/say`, `sip.AudioCall`): the bridge's
    `c300x` identity is its own, so Flexisip loops the INVITE back to the bridge
    — audio never leaves the box. (Fixed en route: the bridge's SIP realm was
    the stale `YOUR_GATEWAY_ID…`; the live Flexisip realm is `YOUR_GATEWAY_ID.bs.iotleg.com` —
    `configs/config.yaml` `sip.domain` corrected, which also un-breaks the video
    self-INVITE REGISTER that had silently failed in production.)
  - *Parallel RTP injection* into the live audio legs (`5000→4000` PT110,
    `7078↔3606` PT98, captured with an AF_PACKET sniff): the DSP/belle-sip
    discards the foreign SSRC — no audio, at every layer.
  - *`bt_av_media` file play* (`*7*51*##`, plays `/var/tmp/records/audio.wav`
    via `filesrc!wavparse!audioconvert!alsasink`): **works**, but the ZL380
    voice-DSP topology routes playback to the **indoor** speaker (Rout), never
    the door (Sout). Confirmed audible indoors, silent at the door.
- **What reaches the door**: `bt_answering_machine` (a liblinphone SIP client)
  auto-answers an unanswered door call and plays its greeting **to the bus**
  (DSP state `EN_AUDIO_PLAY_LOC_SCS`, `GreetingBus`), via `*7*58#…#1*##`
  (message source). Openserver + the DSP mixer apply the bus routing. Validated
  live: with our audio as the greeting, a visitor at the panel hears it.
- **`pkg/webserver/greeting_handlers.go`** — `POST /api/talkback/greeting`
  (audio in the body; any format) transcodes to PCM s16le/mono/8000 with the
  device `ffmpeg` and installs it as `/home/bticino/cfg/extra/7/messaggio.wav`
  (+ `audio.wav`), drops a `.bridge_greeting` sentinel, and (unless
  `?activate=false`) enables the answering machine (`*8*91##`). `GET` reports
  state; `DELETE` removes only a bridge-set greeting (restoring the firmware
  default), optionally deactivating the machine (`?deactivate=true`).
- **Limitation**: one-shot greeting when the machine picks up, not live
  interactive audio. On-demand door playback would require replaying the
  liblinphone answer+play flow or setting DSP state 10 directly (open lead).

## [0.18.0] - 2026-08-18

### Live door video in HA during an incoming call (passive mirror)

When a call rings **your unit**, the bridge now feeds the door's live H.264
video into the existing relay → RTSP (`/doorbell-video`, port 6554) → go2rtc →
HA WebRTC pipeline, for the whole duration of the call.

- **Phase-0 validation with a real doorbell ring (2026-08-18)** invalidated the
  original design: a bridge-issued `*7*300` during a real call is ACKed but
  **ignored** — at ring time the native firmware claims **both** video
  duplication slots with its own `*7*300` (type 0 → `lo:5007`, type 1 →
  `lo:5002`, its local SIP legs). Evidence:
  `_investigacion/device_backups/video_spike_2026-08-18.pcap`.
- **`pkg/sip/rtp_tap.go` — `LoopbackRTPTap`**: read-only AF_PACKET capture on
  `lo` that mirrors the native video leg (UDP dst 5007, **cleartext H.264 RTP,
  ~205 pps sustained ring→hangup**) into the relay input (10002). Discards the
  loopback `PACKET_OUTGOING` duplicates. **Zero commands on the device bus** —
  strictly safer than the previous cooperative design (no watchdog risk, no
  slot contention). No new dependencies (raw `syscall`).
- **`LiveVideoOnCall` v2**: same event wiring (`sip.call.incoming` /
  `.connected` → arm, `.ended` → disarm; building-privacy gating unchanged) and
  still marks the RTSP server call-active to inhibit self-INVITE, but now
  drives the tap instead of sending `*7*300`. Config key
  `streaming.live_keepalive_seconds` removed (nothing to re-arm);
  `live_on_call` no longer requires the OpenWebNet client.
- 12 new unit tests (loopback frame parser incl. IP options/truncation,
  forward loop with fake source + real UDP sink, idempotent lifecycle,
  graceful degradation when the tap can't open).
- **Wiring fix**: in production the local SIP REGISTER never succeeds, so
  `sip.call.*` events never fire (0 occurrences in 5 weeks of logs). The tap
  now arms on **`doorbell.pressed`** (OWN bus monitor, reliable) and disarms on
  a new **`call.hangup`** event published when the hang-up frames
  (`*8*3#…*416##` and variants) appear on the bus, plus a **5-minute guard
  auto-stop** in case the hang-up frame is missed. `sip.call.*` subscriptions
  kept for the day local SIP registers.
- **RTSP crash fix (found by the live E2E test)**: when go2rtc closed its RTSP
  connection, `handleTeardown` called `sipClient.Hangup()` with a nil SIP
  client → SIGSEGV. New `releaseSelfCallIfIdle()` unifies both duplicated
  teardown paths, only ever hangs up **self-INVITE** calls (never a native
  call marked via `SetCallActive`) and tolerates a nil SIP client; UDP SETUP
  and `ensureSIPCallActive` nil-guarded too (5 new tests, panic reproduced in
  RED first).
- **E2E validated on a real doorbell ring (17:04)**: ring → `doorbell.pressed`
  → tap arms → relay → RTSP `/doorbell-video` → go2rtc returns a live
  **688×480 JPEG frame** during the call. go2rtc now auto-starts from
  `start-bridge.sh` (device-side) with stream `doorbell-video`.

### Cooperative video/audio, new HA entities, message fixes

Validated end-to-end on the real device (natives stayed healthy throughout).

#### Video/audio (cooperative `*7*300`, no self-INVITE)
- **`pkg/sip/avmedia_capture.go`** — shared `captureCooperative()`: launch a
  `gst udpsrc → imxvpudec → jpegenc` pipeline, send **one** `*7*300` asking
  bt_av_media to duplicate its RTP, poll for the last complete JPEG. No camera
  contention, no retry. Confirmed at **688×480**.
- **Video probe** `POST /api/video/probe?confirm=yes` and **audio probe**
  `POST /api/audio/probe?confirm=yes` (single-shot diagnostics). Audio confirmed:
  **302 Speex RTP packets, PT=110**. Both need an active native session
  (eye/auto-on or a real call).
- Snapshot (`GET /api/snapshot`) rewritten onto the cooperative path (drops the
  self-INVITE + relay-mirror approach).

#### Home Assistant
- New auto-discovery entities (retained): **Call status** (`bticino/call/state`),
  **Caller** (`bticino/call/caller`), **Landing doorbell**
  (`bticino/doorbell_floor/state`).
- **Doorbell camera** (`camera.camara_timbre`) — captures a cooperative snapshot
  on ring/incoming call and publishes it (base64). Appears only when
  `streaming.video_on_demand: true`.
- Initial states published on startup (`call/state=IDLE`, `doorbell_floor=OFF`)
  so the entities show a value immediately instead of *unknown*.
- New authoritative reference: **`docs/HOME_ASSISTANT.md`**.
- **HA entity organization** (like r0bb10's companion): `entity_category`
  auto-assigned so LEDs, GPIO, network and device-config sensors land under
  **Diagnostic**; controls/operational sensors stay in the main view.
- **IP** and **MAC** as dedicated diagnostic sensors; **WiFi signal (%)** sensor
  via `connmanctl` (60 s refresh).
- **Mark-all-read button** (`bticino/messages/markallread/set`).
- Optional **restart buttons** (Configuration) — restart bridge / reboot device,
  gated behind `mqtt.enable_system_buttons` (default off). Dropbear restart
  deliberately omitted (no supervisor → would drop SSH).
- **`configuration_url`** on the device (HA "Visit device" link to the web
  dashboard, IP auto-detected).
- **Event entities** (HA `event` platform): doorbell, floor doorbell and call
  (`incoming`/`connected`/`ended`) — clean per-trigger events for automations.

#### Messages UI
- **Mark all read** button → `POST /api/messages/mark-all-read` +
  `MessageParser.MarkAllMessagesAsRead()`.
- Fixed stale pagination after deleting the last item on a page (now refetches
  and steps back a page instead of showing "no messages").

#### Deploy
- `deploy-standard.sh` now deploys the **Svelte frontend** too (`web` command
  and `--web` flag).
- New **`docs/DEPLOY.md`** (supersedes the stale deploy guides).

## [0.16.1] - 2026-07-07

### Safety hardening (video on-demand disabled by default)

After a real-device test, activating video on demand (self-INVITE + *7*300)
while the native camera was on caused a command storm that clicked the routing
relay and triggered the system watchdog to reboot the unit. No hardware damage,
but the video-activation paths are now gated off by default.

- **`*7*300` sent once, never retried** (`pkg/openwebnet/client.go`): the
  retry-on-NACK loop (3x) was the trigger; ActivateVideo/AudioStream now use a
  single attempt.
- **`streaming.video_on_demand` config flag, default `false`**
  (`pkg/config/config.go`): gates every video-activation path.
  - RTSP `ensureSIPCallActive()` refuses when disabled (no self-INVITE / *7*300).
  - The `VideoStreamManager` (auto-starts video on doorbell press, `*7*32`) is
    not created when disabled.
  - The snapshot endpoint is not wired when disabled.
  - SIP registration + incoming-call detection stay ON (passive, safe).
- **Robust `deploy-standard.sh`**: correct binary name (`bticino_bridge`),
  upload-then-md5-verify, backup to `.prev`, kill-all-instances, single-instance
  start via `setsid`, health-check with **automatic rollback**.
- Regression tests for the activation gate (`pkg/sip/rtsp_activation_test.go`).

## [0.16.0] - 2026-07-07

### Snapshots, Incoming-Call Detection, Binary Multicast Parser

Feature round inspired by a gap analysis against r0bb10's BTicino-GO-Companion
(techniques re-implemented independently; no code copied).

#### New Features:
- **On-demand JPEG snapshots** (`pkg/sip/snapshot.go`): `GET /api/snapshot`
  captures a still from the live H.264 stream by mirroring the video RTP from
  the relay to a loopback port and decoding it through
  `rtph264depay ! h264parse ! imxvpudec ! jpegenc`. Waits for real RTP flow
  before mirroring, polls the temp file for a complete JPEG (SOI/EOI), 2s
  result cache, captures serialized. Query params `timeout` and `max_age`.
- **Real incoming-call detection** (`pkg/sip/client.go`): self-originated
  INVITE Call-IDs are now tracked, so the c300x auto-answer path tells a real
  external call apart from our own video self-INVITE. Publishes a distinct
  `sip.call.incoming` event (with parsed caller) and `sip.call.ended` on
  remote BYE. The video self-INVITE flow is unchanged.
- **Call control API**: `GET /api/call` (state + registration) and
  `POST /api/controls/call/hangup` (SIP BYE).
- **MQTT call/floor entities**: `bticino/call/state` (+ `/caller`) and
  `bticino/doorbell_floor/state` for the landing/floor doorbell.

#### Improvements:
- **Binary multicast datagram parser** (`pkg/multicast/listener.go`): parses
  the structured binary syslog datagrams on `239.255.76.67:7667`
  (8-byte header, NUL-terminated system name at offset 8, metadata bytes
  before the message; `REGISTRATION`/`LCM_SELF_TEST` special cases) with a
  plain-text fallback. Unit-tested.
- **Floor/landing doorbell** (`*7*59#`) recognised as its own
  `doorbell.floor.pressed` event.
- **`boot_time`** (RFC3339) added to `/api/status` so remote clients can
  verify a restart happened even when the restart request times out.

#### Tests:
- New unit tests: `pkg/multicast/listener_test.go`, `pkg/sip/client_test.go`.

#### Fixes:
- Fixed a pre-existing `go vet` error in `pkg/sip/go2rtc_manager.go`
  (missing `Infof` argument).

## [0.15.5] - 2026-04-04

### RTSP Video Streaming via Direct GStreamer + RTP Relay

**Major milestone**: Full RTSP video streaming pipeline bypassing bt_av_media entirely.

#### New Files:
- `pkg/sip/rtp_relay.go` - RTP relay with UDP fan-out to RTSP clients. Listens on UDP ports for GStreamer RTP output, forwards to all registered consumers. Supports both UDP unicast and TCP interleaved modes. ARM 64-bit atomic alignment fix (uint64 fields first in structs).
- `pkg/sip/gstreamer.go` - GStreamer pipeline launcher for camera capture. Hardware-accelerated H.264 encoding via i.MX VPU (imxvpuenc_h264). Configurable GOP size, IDR interval, bitrate. Stderr capture for diagnostics. Audio pipeline failure is non-fatal.

#### Modified:
- `pkg/sip/rtsp_server_enhanced.go` - Integrated RTP relay pair (video:10002, audio:10000). Rewrote SETUP for per-track UDP/TCP interleaved support. Rewrote PLAY to register RTP consumers with relay. Added consumer cleanup on TEARDOWN and TCP connection close. Fixed SDP: 720x576, 7fps, profile-level-id=42C01E, bitrate 1500.
- `pkg/sip/rtsp.go` - Added `VideoClientPorts` and `AudioClientPorts` to `RTSPSession` for per-track port tracking (fixes second SETUP overwriting first).
- `pkg/sip/client.go` - Dual-role SIP client (webrtc + c300x) for self-INVITE call flow.
- `pkg/config/config.go` - Added `StreamingConfig` and `SIPConfig` structs.
- `pkg/openwebnet/client.go` - ActivateVideoStream/AudioStream support.

#### Architecture:
```
RTSP Client (ffplay/VLC) ──RTSP──> bticino_bridge:6554
                                        │
                                   SIP INVITE (webrtc→c300x via Flexisip)
                                        │
                                   GStreamer pipelines:
                                     imxv4l2videosrc → imxvpuenc_h264 → rtph264pay → udpsink :10002
                                     alsasrc → speexenc → rtpspeexpay → udpsink :10000
                                        │
                                   RTP Relay (fan-out to all RTSP clients)
```

#### Key Discoveries:
- bt_av_media MQTT interface is unusable from external publishers (libjel.so routing bug)
- Camera outputs 720x576 UYVY interlaced PAL at ~7fps (not 25fps)
- `gop-size=7, idr-interval=1, config-interval=1` = best compromise for mid-stream joining
- ARM 32-bit requires 64-bit aligned uint64 fields for sync/atomic operations

#### Bug Fixes:
- Fixed ARM 64-bit atomic alignment panic in RTPRelay struct
- Fixed per-track UDP port overwrite (video ports lost when audio SETUP ran)
- Made audio pipeline failure non-fatal (video continues)
- Added stderr capture for GStreamer error diagnostics

## [0.15.0] - 2026-04-03

### 🚀 **Real-Time Updates & Memos Support**

- ✅ **SSE (Server-Sent Events)**: New `/api/events` endpoint for real-time LED/GPIO updates
- ✅ **Web Dashboard**: Updated to use EventSource for live updates (no polling needed)
- ✅ **GPIO Display**: Dashboard now shows GPIO pins with real-time state changes
- ✅ **Memos API**: New `/api/memos` endpoint for voice and text notes
- ✅ **MemoParser**: New parser for `memos_text/` and `memos_voice/` directories
- ✅ **Voice Memos**: Support for audio.wav files
- ✅ **Text Memos**: Support for message.txt files
- ✅ **Web Files**: Fixed deployment of Svelte static files

### 📊 **New API Endpoints**:
- `GET /api/events` - SSE stream for real-time updates
- `GET /api/memos` - List all memos (voice + text)
- `GET /api/memos/{id}` - Get specific memo

### 🎯 **Verified**:
- API returning 4 memos (1 text, 3 voice) as expected
- One text memo marked as unread

## [0.14.8] - 2026-04-03

### 🔄 **State Synchronization & Logging**

- ✅ **LED/GPIO MQTT Publishing**: Added logging when publishing LED and GPIO states to MQTT
- ✅ **Mark Unread**: Added `MarkMessageAsUnread()` in messageparser.go
- ✅ **API Integration**: Updated server.go to use messageParser.MarkMessageAsUnread
- ✅ **Improved Debugging**: Logs now show "Publishing LED states to MQTT leds=..." and "Publishing GPIO states to MQTT gpio=..."
- ✅ **Version bump**: VERSION file updated to 0.14.8

### 📊 **MQTT Topics Published**:
- `bticino/led/{name}/state` - LED states (ON/OFF) every 30 seconds
- `bticino/gpio/{pin}/state` - GPIO pin states every 30 seconds

### 🎯 **Verified Working**:
- LED states visible in Home Assistant
- GPIO pins 12,13,47,49,52,54,56,58,60,154,155,176,180 monitored
- MQTT connection stable

## [0.14.7] - 2026-04-01

### 💬 **Messages UI Improvements**

- ✅ **Mark Read/Unread**: Implemented mark as read/unread for messages
- ✅ **Download Video**: Added video download API endpoint
- ✅ **Messages Page**: Modal view with filters, mark read/unread, delete
- ✅ **Video Download Button**: "Download Video" button in Messages modal

## [0.14.6] - 2026-03-

### 🔄 **Configuration Sync**

- (See FUTURE_IMPROVEMENTS.md for detailed progress)

## [0.14.5] - 2026-04-

### 🔄 **Configuration Management**

- (See FUTURE_IMPROVEMENTS.md for detailed progress)

### 📚 **Swagger UI API Documentation**

- ✅ Add Swaggo dependency for automatic swagger generation
- ✅ Add @Summary/@Description/@Tags annotations to 40+ API handlers
- ✅ Generate swagger.json with `swag init`
- ✅ Serve Swagger UI at `/api/docs/` endpoint
- ✅ Add "API Docs ↗" link to web UI navigation (opens in new tab)
- ✅ Fix duplicate navbar in Settings/Controls/Logs pages

### 📝 **API Endpoints Documented (40+)**:

**Status/System (2)**:
- `GET /api/status` - System status
- `GET /api/system` - Device info

**Messages (6)**:
- `GET /api/messages`, `/api/messages/list`, `/api/messages/{id}`
- `GET /api/messages/download/{id}/{type}`
- `POST /api/messages/mark-read/{id}`
- `DELETE /api/messages/delete/{id}`

**Controls (10)**:
- Door unlock, Answering machine toggle
- Display on/off, Mute on/off
- Doorbell sound on/off, Staircase light
- Arbitrary command

**Streaming (9)**:
- `GET /api/streaming`, `POST /api/streaming/start|stop`
- `GET /api/streaming/sessions|config`
- `POST /api/streaming/record`
- `POST /api/webrtc/start|stop`
- `GET /api/webrtc/status`

**Bridge Config (8)**:
- `GET /api/config`, `POST /api/config/save|validate|backup|restore|reload`
- `GET /api/config/backups|history`

**Device Config (9)**:
- `GET /api/config/device|language|timezone|ntp|ringtones|volumes|display|cameras|answering`

**Device (7)**:
- `GET /api/device/ntp|timezone|language|ringtone|ringtones|languages`
- `POST /api/device/save`

**Logs (2)**:
- `GET /api/logs`, `GET /api/logs/download`

## [0.14.3] - 2026-03-28

### 🎨 **Simplified Dashboard**

- ✅ Removed Quick Actions section
- ✅ Removed Enhanced Features
- ✅ Cleaner, more minimal dashboard
- ✅ Smaller bundle: 57KB → 56KB

### 📝 **Fixes**

- ✅ deploy.sh now copies the VERSION file automatically
- ✅ Versioning documentation added (VERSIONING.md)

## [0.14.2] - 2026-03-27 - Svelte UI Migration 🎉

### 🎨 **NEW: Modern UI with Svelte**

**Complete migration from embedded UI to modern Svelte**

#### **Created Components**:
- ✅ **Dashboard** (`/`) - System status, components, LEDs, quick actions
- ✅ **Settings** (`/settings`) - 5 tabs (Bridge, Device, SIP, MQTT, OpenWebNet)
- ✅ **Controls** (`/controls`) - Door, Voicemail, Display, Mute, Doorbell, Light
- ✅ **Logs** (`/logs`) - Filter, search, auto-refresh, download

#### **Features**:
- ✅ Hash routing (no external dependencies)
- ✅ Auto-refresh (30s status, 5s logs)
- ✅ Form binding and validation
- ✅ Success/Error messages
- ✅ Loading states
- ✅ Responsive design

#### **Bundle Size**:
- Web UI: **58 KB** (gzipped)
- Go Binary: **14 MB** (ARM)
- **Total**: 60% smaller than the embedded UI

---

### 🚀 **NEW: Single Deploy**

**Unified script for build + deploy**

#### **scripts/deploy.sh**:
```bash
./scripts/deploy.sh
```

**What it does**:
1. ✅ Build web frontend (Vite + Svelte)
2. ✅ Build Go binary (ARM)
3. ✅ SSH and automatic backup
4. ✅ Binary + web files transfer
5. ✅ Service restart
6. ✅ Verification

**Updated Makefile**:
```bash
make install    # Instalar dependencias
make dev        # Development mode (hot reload)
make build      # Build completo
make deploy     # Build + deploy
make clean      # Limpiar
```

---

### 📁 **Project Structure**

```
bticino_bridge/
├── web/                        # 🆕 Svelte UI
│   ├── src/
│   │   ├── main.js
│   │   ├── App.svelte
│   │   └── routes/
│   │       ├── +page.svelte         # Dashboard
│   │       ├── settings/
│   │       │   └── +page.svelte     # Settings
│   │       ├── controls/
│   │       │   └── +page.svelte     # Controls
│   │       └── logs/
│   │           └── +page.svelte     # Logs
│   ├── index.html
│   ├── package.json
│   ├── vite.config.js
│   └── svelte.config.js
├── scripts/
│   └── deploy.sh             # 🆕 Single deploy
├── Makefile                  # 🆕 Con build Svelte
├── VERSION                   # 🆕 0.14.2
└── docs/
    ├── SVELTE_SETUP.md       # 🆕 Setup guide
    └── PROGRESS_REPORT.md    # 🆕 Progress report
```

---

### 📊 **Metrics**

| Aspect | Before | Now | Improvement |
|---------|-------|-------|--------|
| **UI Framework** | Embedded HTML | Svelte 4 | ✅ Modern |
| **server.go lines** | 4802 | ~3000 | -37% |
| **Bundle Size** | 14MB together | 14MB + 58KB | ✅ Separate |
| **Deploy Scripts** | 4 different | 1 single | ✅ Simplified |
| **Hot Reload** | ❌ No | ✅ Yes | ✅ Fast |
| **Components** | 0 | 4 | ✅ Reusable |

---

### 🔧 **Technical Changes**

#### **Added Dependencies**:
```json
{
  "devDependencies": {
    "@sveltejs/vite-plugin-svelte": "^3.0.0",
    "svelte": "^4.2.0",
    "vite": "^5.0.0",
    "terser": "^5.0.0"
  }
}
```

#### **Used API Endpoints**:
- `GET /api/status` - Dashboard
- `GET /api/config` - Settings
- `POST /api/config/save` - Settings save
- `GET /api/logs` - Logs viewer
- `POST /api/controls/*` - Controls actions

#### **Modified Files**:
- `pkg/webserver/server.go` - Static web routes
- `Makefile` - Build commands
- `VERSION` - 0.14.2

#### **Created Files**:
- `web/src/**/*.svelte` - 4 components (1605 lines)
- `web/vite.config.js` - Vite config
- `web/package.json` - Dependencies
- `scripts/deploy.sh` - Deploy script (150 lines)
- `docs/SVELTE_SETUP.md` - Setup guide
- `docs/PROGRESS_REPORT.md` - Progress report

---

### 🐛 **Bug Fixes**

- ✅ Svelte bind errors on Settings page
- ✅ Components variable scope in Controls
- ✅ Terser minification for production build

---

### ⚠️ **Breaking Changes**

**None** - The REST API stays the same. Only the UI changes.

---

### 📝 **Migration Notes**

**To upgrade from previous versions**:

```bash
# 1. Install npm dependencies
cd bticino_bridge/web
npm install

# 2. Build
cd ..
make build

# 3. Deploy
make deploy
```

**Existing configuration**: unaffected. config.yaml stays the same.

---

### 🎯 **Next Steps (v0.15.0)**

- [ ] Device tab (read from QML)
- [ ] WebSocket for real-time logs
- [ ] Messages page
- [ ] Dark mode
- [ ] TypeScript migration
- [ ] Unit tests (Vitest)

---

**Comparison with slyoldfox/c300x-controller**:

| Feature | slyoldfox | bticino_bridge v0.14.2 |
|---------|-----------|------------------------|
| Web UI | ❌ No | ✅ **Modern Svelte** |
| REST API | ✅ Limited | ✅ **Complete** |
| Deploy | Manual | ✅ **Automated** |
| Hot Reload | ❌ No | ✅ **Yes** |
| Components | ❌ No | ✅ **4 components** |

---

**Estado**: ✅ **PRODUCTION READY**  
**Testing**: ✅ Successful deploy on real device (192.168.1.38)  
**Documentation**: ✅ Complete
