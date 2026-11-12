# Native firmware & behaviour — Classe 100X

Findings about the **native** firmware of this unit (Classe 100X16E), gathered
2026-10-14…17 through read-only inspection (`/proc`, sysfs, native config files,
logs), loopback packet captures and MQTT observation.

Everything below was **observed** unless marked *unverified*. The bridge itself
is documented elsewhere (`docs/FEATURES.md`, `docs/HOME_ASSISTANT.md`).

> ⚠️ Two findings are operationally critical and easy to get wrong:
> **A manual Flexisip restart breaks SIP until the unit is rebooted** (§3.4) and
> **killing the bridge with `-9` orphans native SIP dialogs** (§8).

---

## 1. Unit identity

| Fact | Source | Value |
|---|---|---|
| Hostname | `/etc/hostname` | `<HOSTNAME>` |
| Model (exact) | `/var/tmp/conf.xml` `<setup><generale><modello>` | `Classe 100 X16E` |
| Model (short) | same file, `<nome>` / `<creator_sw><name>` | `C100X` |
| Device ID | same file, `<deviceID>` | `<DEVICE_ID>` — **identical to the SIP domain prefix** |
| Firmware stamp | `/etc/version` | `20250522064330` |
| Kernel | `uname -a` | `4.9.11+imx`, armv7l, SMP PREEMPT |
| Hardware base | `/proc/device-tree/model` | `BTicino SHARK board (Dig-2/Car-7)` (same across models) |

**Reliable runtime model detection:** `/var/tmp/conf.xml` → `<modello>`/`<nome>`.
`pkg/deviceconfig/confxml.go` already parses this file, so a runtime cross-check
of the compiled `bticino.DeviceVariant` is possible with existing code.

⚠️ **Do not trust `…/cfg/extra/90/extra.xml`:** its `<sw><name>` says `C300X`
on this 100X unit (the extra package was authored for the 300X).

### 1.1 Input devices and keys (2026-11-11)

`/proc/bus/input/devices` lists two devices: `C100_KB_TOUCH keypad`
(`event0`, I2C `c100_kb_touch@69`, key codes 2–8) and `gpio-keys` (`event1`).
There is no `event2` and no touchscreen.

The device tree names the `gpio-keys` entries (`/proc/device-tree/gpio-keys`):

| Label | Code | Meaning |
|---|---|---|
| `floor_call` | 213 | the rear terminals for the external floor pushbutton (manual: clamp 7); a real floor ring produced it, about 3 s before the bus frame `*7*59#14#0#0*##` |
| `wheel_up` / `wheel_down` / `wheel_push` | 103 / 108 / 28 | the front joystick |
| `horn_up` / `horn_down` | 218 / 206 | the green (answer) and red (hang up) handset keys, verified by pressing each key twice |

The keypad's device-tree node has no keymap; its driver defines the codes.
Pressing each front key gave P1–P4 (the programmable keys) = 2–5,
star = 6, key = 7, eye = 8. The 300X keypad uses 2–5 for key, star, eye and
phone; its manuals list no programmable keys.

For comparison, the device trees in the official Classe 300X firmware 1.7.19
(`BTicino ZIA board`) have a single `gpio-keys` entry, `ch_piano` (floor call,
manual: clamp 9) with code 6 (the manufacturing device tree adds
`powerfail`, code 7), a `cypress,i2c_kb_touch` keypad without keymap and a
`ti,tsc2005` touchscreen. No other node produces key codes.

---

## 2. Native processes (steady state)

| Process | Role |
|---|---|
| `bt_daemon` (×2) | starts/supervises the native app set (`/etc/init.d/bt_daemon-apps.sh`) |
| `bt_device`, `bt_vct` | device/video control, bus handling |
| `bt_av_media` | SIP media engine (RTP for native sessions) |
| `bt_ipcamera`, `BtClass100` | camera plumbing, GUI (QML) |
| `bt_dbus_manager`, `fw_manager`, `scsserver` | DBus, firmware manager, service control |
| `openserver` | OpenWebNet server (monitor 20000, commands 30006, video 30007) |
| `flexisip` | local SIP proxy/registrar (see §3) |
| `bt_answering_machine` | answering machine **and** keystore-passphrase pusher (§3.4, §6) |
| `mosquitto` | local MQTT broker (device-internal, not the bridge's) |

**No supervisor for Flexisip:** `bt_daemon` does *not* restart it if it dies
(verified: killed Flexisip, waited 15 s, no respawn).

---

## 3. SIP / Flexisip

### 3.1 How it runs

Flexisip 1.0.12 (sofia-sip 1.13.25bc) is started at boot via
`/etc/init.d/bt_daemon-apps.sh` → `bt_daemon` → `/etc/init.d/flexisipsh`, with:

```
--daemon --syslog --pidfile /var/run/flexisip.pid
--p12-passphrase-file /var/tmp/bt_answering_machine.fifo
--transports "sips:<DEVICE_IP>:5061;maddr=<DEVICE_IP>;require-peer-certificate=1
              sip:127.0.0.1;maddr=127.0.0.1"
```

* **5061/TLS** = the cloud/app side (peer certificate required).
* **5060/TCP on loopback** = plaintext signaling for the native client and
  anything else on the device (this is what the bridge uses).
* `/etc/init.d/flexisipsh` supports only `start <ip> [passphrase-fifo]` and
  `stop` — there is **no** `restart` target.

### 3.2 Config essentials (`/etc/flexisip/flexisip.conf`)

| Setting | Value | Consequence |
|---|---|---|
| `aliases` / `reg-domains` / `auth-domains` | `1ca7e788-…bs.iotleg.com` | the SIP domain = `conf.xml` `<deviceID>` |
| `trusted-hosts` | `127.0.0.1` | loopback REGISTER/INVITE needs no credentials |
| `[module::MediaRelay]` | enabled, `nortpproxy=disable` | rewrites SDP `c=` to `127.0.0.1`; all media anchors on loopback |
| `[module::Router]` | `allow-target-factorization=true` | contact-form INVITEs are routed 1:1 (no fork) |
| `log-level` / `syslog-level` | `error` | **connection closes and routing failures are invisible** |

To debug SIP problems, raise `log-level` to `info`/`debug` temporarily — remember
to revert (and read §3.4 before restarting anything).

### 3.3 Users and routes

`/etc/flexisip/users/`:

* `users.db.txt` — native identity `<uuid>_<serial>@<domain>`, plus the
  bridge-created `webrtc@` and `c100x@` identities.
* `route.conf` (linked to `route_ext.conf`) — `alluser@domain` → native identity
  → `sips:vdesip.bs.iotleg.com` (cloud trunk).
* `route_int.conf` — local delivery (alluser → native identity).
* `domain-registration.conf` — outbound trunk to `vdesip.bs.iotleg.com` (TLS).

### 3.4 ⚠️ Keystore passphrase FIFO — why Flexisip must not be restarted manually

The TLS certificate/key live in a **password-protected** p12
(`tls-certificates-dir=/home/bticino/cfg/extra/.keystore/aswm_truststore.p12`;
empty passphrase fails with `Mac verify error`).

At boot the passphrase is pushed **once** through a FIFO:

1. Flexisip starts with `--p12-passphrase-file /var/tmp/bt_answering_machine.fifo`
   and blocks reading it.
2. `bt_answering_machine` (which contains `push_passphrase_keystore`) writes the
   passphrase.
3. Flexisip proceeds and binds 5061.

What that means in practice (all verified on 2026-10-17):

| Action | Result |
|---|---|
| `/etc/init.d/flexisipsh stop` + `start` | `start` **hangs forever** on the FIFO — no writer |
| `start` without the passphrase argument | comes up **local-only**: 5060 listening, **5061 not bound**, cloud registration fails (`Could not enable transport sips:…`) |
| killing Flexisip | nothing restarts it — SIP stays down |

Those are only the *first* symptoms. Getting Flexisip back by hand (start with
the FIFO, then restart `bt_answering_machine` so it pushes the passphrase) does
bind 5061 again — **but the unit stays unreachable over SIP**:

* the **native** SIP client (`gui/BtClass100`) does not re-register after a
  Flexisip restart (§3.8), so camera sessions and app calls have no target;
* a hand-started `bt_answering_machine` registers *itself* as the unit and
  declines calls with `603 Decline`.

**A device reboot is the only reliable recovery.** Do not treat the manual
sequence as a recovery path — it restores the proxy and leaves the unit
unreachable.

### 3.5 Cloud trunk

* Outbound TLS to `vde-sip4.bs.iotleg.com:5228` (e.g. `51.105.204.121`),
  certificate `CN=vdesip.bs.iotleg.com`, issuer `Legrand Non-Public - PROD`
  (self-signed chain), TLS 1.3 / AES-256-GCM.
* The device cannot resolve `vdesip.bs.iotleg.com` (`getaddrinfo` errors in
  syslog) yet the trunk stays connected — *unverified* how the peer address is
  obtained.
* The trunk is TLS 1.3 with forward secrecy: a passive capture cannot be
  decrypted, and the truststore p12s contain no client key (SIP Digest
  authenticates instead). **The same signaling reappears in plaintext on
  loopback**, so decrypting the trunk is unnecessary for analysis.

### 3.6 Periodic user-list reload

`<aswm> New user list detected` appears every ~10 minutes
(`/var/log/log_rotation.log`), i.e. something rewrites the Flexisip user list
and the registrar reloads it ~144×/day. It did **not** correlate with the one
observed connection drop (see §3.7).

### 3.7 Connection drops (cause unknown)

Observed once (2026-10-17, 01:53:11): both bridge connections were closed within
the same second while the native unit's connection survived. Flexisip had not
restarted (uptime = device uptime) and `log-level=error` recorded nothing.
The bridge at that time had **no reconnect path** (fixed in the bridge, see
`CHANGELOG.md`); the native side recovered on its own.

### 3.8 The native SIP UA does not re-register after a Flexisip restart

After restarting Flexisip (2026-10-17, 23:34/23:36) the **native** client
(`gui/BtClass100`, the belle-sip responder that normally answers on
`127.0.0.1:54688`) never re-registered. Consequences and symptoms:

* No contact for the unit on Flexisip: camera-session INVITEs cannot be
  delivered (`dial failed`), and the bridge's native-contact resolver aborts
  (`want exactly 1 native contact, found 0`).
* If `bt_answering_machine` is started **manually** while the GUI is
  unregistered, *it* registers as the unit's contact and **declines** calls
  with `603 Decline` — i.e. camera sessions get `INVITE rejected: 603`.
  (`603` therefore means "some non-GUI endpoint is registered", not "busy".)

**Only a reboot reliably restores the GUI's registration.** Treat a Flexisip
restart on this unit as requiring a device reboot afterwards.

---

## 4. SIP dialog behaviour

### 4.1 Incoming app call

Phone → (TLS) → `vde-sip4…:5228` → local Flexisip → (plaintext) → native client
on `127.0.0.1:54688`. The phone is **never** on the LAN: app calls hairpin
through the cloud even when both are on the same network.

* MediaRelay rewrites the SDP `c=` line to `127.0.0.1`; the phone's Via shows
  its public IP.
* Native user agent: `belle-sip/1.6.3`.

### 4.2 Forking depends on the request URI

| Request URI | Result |
|---|---|
| `sip:c100x@<domain>` (address of record) | **forked** to every registered contact (native **and** the bridge) — first final response wins |
| `sip:c100x@127.0.0.1:<native-port>;transport=tcp` (contact form, as the cloud does) | delivered **only** to the native client |

Consequence: a bridge self-INVITE to the bare AoR races its own auto-answer
and loses the native branch; addressing the native contact avoids the fork
entirely.

### 4.3 `a=DEVADDR` is validated

The SDP answer succeeds only with the unit's real DEVADDR UUID
(`<DEVADDR>` on this unit, also used by the app).
A random UUID yields an **instant `486 Busy here`** with no provisionals —
the same response a *busy* unit gives, but distinguishable: busy arrives
**after** `100 Trying`/`180 Ringing`.

### 4.4 Busy and preemption

* While a native session is active, an INVITE to the native contact gets
  `486` after provisionals.
* A doorbell ring **preempts** an existing headless session: the native side
  sends `BYE` to us first, then dials `alluser` for the call (§4.1).

---

## 5. Media paths

| Path | Port / detail |
|---|---|
| Native video leg (cleartext H.264, RTP PT 96) | `lo:5002` (100X) / `lo:5007` (300X high-res) |
| Native audio RTP declared in SDP | `7078` (speex/8000, `RTP/SAVP`) |
| Native video RTP declared in SDP | `9078` (H264, `profile-level-id=42801F`) |
| Bridge offer ports (black holes) | `65000` audio / `65002` video |
| Audio actually observed | SRTP to the ports we offered (payload not used by the bridge) |
| Video actually analysed | cleartext stream on `lo:5002`, mirrored by the bridge's passive tap |

The native side duplicates/echoes some signaling on the monitor port, e.g.
`*7*300#127#0#0#1#5002#1*##` (its own video-leg activation) appears on the bus
at session answer.

---

## 6. Answering machine (*segreteria*)

Evidence on this unit:

* `cfg/extra/47/segreteria.xml` → `<attiva>1</attiva>`, `memmax=110000`, `memfree=0`
* `cfg/extra/47/aswm_settings.ini` → `[Answering Machine] PlayMemo=80`
* `/var/tmp/conf.xml` → `<segreteria>` block (greeting `audio.wav`, email-forward
  flags) and `<servizi><abil_segreteria>1</abil_segreteria>`
* `messages/`, `memos_text/`, `memos_voice/` exist but are empty since **2019**
* `bt_answering_machine` **runs** (it also pushes the keystore passphrase)

Verdict: the *platform* ships the feature and its config says "enabled", but the
**product** does not expose it — the Classe 100X16E user manual (344682, IT/EN/DE)
has no answering-machine function, no recording has ever been observed, and the
GUI offers no access. The bridge therefore treats it as 300X-only
(`bticino.AnsweringMachineAvailable()`).

*Unverified:* whether an unanswered call would record anything anyway. A cheap
test would be leaving one call unanswered and checking whether `memfree` in
`segreteria.xml` or the `messages/` dir changes.

---

## 7. OpenWebNet bus frames observed

Frames the **native** side emits that matter for the bridge:

| Frame | Meaning | Notes |
|---|---|---|
| `*8*9#1#4*20##` | incoming entrance call / doorbell | also `*8*1#1#4#21*16##` on 300X |
| `*8*9#13#2*410##`, `*8*1#13#2*10##` | call/aux frames with **different WHERE values** | 100X uses 13/410 where the 300X tables say 1#4/20 → not matched by the bridge's description table |
| `*8*3#5#4*411##`, `*8*3#1#4*411##` | call ended | |
| `*7*59#14#0#0*##` | **floor/landing button pressed** | observed twice, ~3 s after the press |
| `*7*59#8#0#0*##` | video-status chatter during entrance rings | **not** a button — was logged as a floor ring until filtered |
| `*#8**35*N*O*P##` | audio session state (idle/starting/active) | `N` changes with the call phase |
| `*#8**33*0/1##` | ringer mute state | also the answer to a `*#8**33##` query |
| `*7*73#1#100*##`, `*7*73#0#0*##` | display on (brightness) / off | |
| `*7*300#127#0#0#1#<port>#<type>*##` | native video-leg activation | `<type>` 1 = 5002, 0 = 5007 |
| `*7*55*##`, `*7*219*##`, `*7*0*##` | video stopped / disconnected | session teardown sequence |

Frame classification rule of thumb: `*N*…##` = command/event, `*#…##` = status
request/answer.

---

## 8. ⚠️ Operational traps

1. **Never restart Flexisip by hand** (init script, `kill`, manual start). TLS
   needs the boot-time passphrase (§3.4), and the native SIP client does not
   re-register afterwards (§3.8) — the unit becomes unreachable over SIP and
   only a reboot fixes it.
2. **Never `pkill -9 bticino_bridge`** while a SIP dialog is up: no `BYE` is
   sent and the native side keeps a half-open dialog (it then refuses new
   INVITEs). The bridge now stops owned sessions before exiting, and
   `scripts/deploy.sh` sends `TERM` first.
3. **A device reboot is the safe recovery** for any SIP/flexisip mess; it is
   forbidden to the agent (see `AGENTS.md`) and must be done by the operator.
   After a reboot the bridge comes back only if `bt_bridge_autostart` is
   installed (see [DEPLOY.md](DEPLOY.md#autostart-after-a-reboot)); otherwise it
   stays down until it is started manually or deployed again.
4. `log-level=error` in Flexisip means "no evidence" — raise it before
   investigating a SIP failure, and revert afterwards.

---

## 9. How these findings were obtained (recipes)

```sh
# native identity / model
cat /etc/hostname; grep -E '<modello>|<nome>|<deviceID>' /var/tmp/conf.xml

# SIP config + transports
grep -vE '^\s*#|^\s*$' /etc/flexisip/flexisip.conf
tr '\0' ' ' < /proc/$(cat /var/run/flexisip.pid)/cmdline

# who is connected to loopback SIP
awk '$2 ~ /:13C4/ || $3 ~ /:13C4/ {print $2, $3, $4}' /proc/net/tcp

# signaling + media (bounded)
/usr/sbin/tcpdump -c 4000 -ni lo -s 1600 -w - 'tcp port 5060 or udp'
/usr/sbin/tcpdump -c 4000 -ni lo -s 1600 -w - 'tcp port 20000'   # OWN bus

# syslog (Flexisip/app/bus messages)
grep -a flexisip /var/log/log_rotation.log | tail
```

`tcpdump` lives at `/usr/sbin/tcpdump` (not on `PATH`).

---

## 10. Open questions

1. Why did Flexisip close both bridge connections on 2026-10-17 01:53 (§3.7)?
   Needs a `log-level=debug` session or an instrumented recurrence.
2. Is the ~10-minute user-list rewrite (§3.6) able to drop registrations at all?
3. Does the answering machine record anything despite having no UI (§6)?
4. Where does the trunk's peer address come from when DNS fails (§3.5)?
