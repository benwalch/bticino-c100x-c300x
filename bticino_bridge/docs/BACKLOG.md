# Feature backlog (BTicino bridge)

Future ideas and phases, out of active scope. Each needs its own
spec → plan before implementation.

## Video/audio (related to the live-video spec)

- **Live audio (listening to the door in HA).** Requires the bridge to be a **SIP
  party** in the call (auto-answer as an audio endpoint) so the door emits
  Speex RTP; transcode Speex→Opus with the device ffmpeg
  (`/usr/local/bin/ffmpeg`, has libspeex+libopus) → go2rtc → HA. Base: standalone
  SIP account (200 OK verified) + existing INVITE auto-answer.
  See `_investigacion/docs/SPIKE_LISTEN_HA_2026-07-11.md`.
- **Talk-back (speaking toward the door).** `pkg/sip/talkback.go` + RE done; requires
  the bridge to own the call (SSRC collision if injected in parallel with the app).
- **"Call home".** The bridge originates a SIP call to the intercom (outgoing
  intercom). Its audio does NOT go through bt_av_media (confirmed in the spike).

## Other

- **Log rotation that actually works.** `/var/log` is a tmpfs symlink
  (`/var/volatile/log`, 261M) and the bridge logs via shell redirect, so the
  `logging.max_size/max_files` config is decorative and nothing ever rotates:
  on 2026-10-12 the SIP listener flood filled tmpfs 100% and silenced all
  logging for ~19h with the process alive. Options: (a) bridge-managed file
  output honoring `MaxSize`/`MaxFiles` (+ make `logging.level/file` actually
  take effect — currently only `--log-level` applies); (b) a `logrotate`
  config on device; (c) keep `--log-level warn` on the starter. Partial
  mitigation landed: routine SIP listener traffic downgraded to Debug.

- **Access/lock event classifier by duration.** The OpenWebNet bus
  emits lock events (unlock → lock + duration) from **any floor**
  (without identifying the specific floor — building privacy). Empirical pattern
  observed by the user:
  - **~2-3 s** → opening after intercom (a visitor being let in).
  - **~4 s** → opening with an **RFID key** (typically an arriving resident).

  **Value:** publish a classified event in HA ("key entry" vs "intercom entry")
  for automations/notifications, security signals (openings at odd hours / with
  nobody home) and a "who came in" timeline correlated with our
  doorbell calls.

  **Caveats:** a **building-level** signal (doesn't identify the floor); the 2-3 s
  vs 4 s threshold is an observed heuristic and **must be validated with real data**
  (it may vary by door/person/season) before relying on it. The groundwork partly
  exists already: the bridge already emits `door.unlocked` events and a lock entity
  over MQTT.
