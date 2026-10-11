# Roadmap — Two-way audio (talk-back) & native WebRTC

Development plan for the two big remaining features from the r0bb10 gap analysis.
They share prerequisites, so they are planned together. **Nothing here is
implemented yet.** License reminder: r0bb10's Companion has no license — we
reimplement techniques, never copy code.

## What we already have (validated on the device)

| Direction | Media | How | Status |
|---|---|---|---|
| Downstream video | H.264 PT96 | `*7*300#…#0*##` → our UDP port | ✅ 688×480 |
| Downstream audio | Speex PT110 | `*7*300#…#2*##` → our UDP port | ✅ 302 pkts |
| Upstream audio | Speex/8000 **PT98** → intercom | Plain RTP to the c300x audio port (7078) | 🟠 injector implemented + RTP verified, BUT injecting alongside the app hangs up the call (SSRC) → the bridge still needs to be the ONLY audio party |

> **LIVE TEST 2026-07-09 (result):** `TalkbackInjector` deployed and
> tested. **Verified on the wire**: the bridge delivers correct RTP to `127.0.0.1:7078`
> — RTP header `80 62 ...` = **payload type 98** (`a=rtpmap:98 speex/8000`, the PT
> announced by the c300x; the initial 110 was the mistake). c300x audio port =
> **7078 consistent** across all call types (monitor, doorbell, "call home").
> **BUT**: injecting that stream **while the app is in a call** makes the c300x
> **hang up** (2/2 attempts) — it detects two RTP sources on its audio port (SSRC
> collision / misaligned sequence) and aborts. Conclusion: **you can't inject in
> parallel with the app's stream**. Next step (new phase): make the **bridge the
> ONLY audio party** — ORIGINATE/own a call to the c300x (like "call home" does
> but from the bridge), sending its audio as the remote party → it comes out of the
> c300x speaker without competing. The bridge already auto-answers INVITEs to `c300x`
> and does self-INVITE (video); what's missing is negotiating real audio (not
> black-hole ports) and routing the upstream RTP. Tools ready: `pkg/sip/talkback.go`,
> `/api/talkback/*` endpoints, test WAV. Media analysis: `_investigacion/docs/TALKBACK_AUDIO_ASCENDENTE_2026-07-09.md`.

> **UPDATE 2026-07-09 — talk-back UNBLOCKED.** A real app call was captured (see
> `_investigacion/docs/TALKBACK_AUDIO_ASCENDENTE_2026-07-09.md`) confirming: audio is
> **bidirectional plain RTP** (dummy crypto in the SDP), upstream goes to the
> **SDP-negotiated door panel audio port** (NOT 4000, which is internal DSP), and
> **bidirectional audio is armed with `*8*2#5#4*16##`**
> (`bticino.CmdAnswerPickup`). The injector is implemented: `pkg/sip/talkback.go`
> (`TalkbackInjector`: GStreamer `filesrc→speexenc→rtpspeexpay pt=110→udpsink`) +
> `POST /api/talkback/start` `{host,port,source,loop,arm}`,
> `/api/talkback/stop`, `/api/talkback/status` endpoints. **Pending: live test** to
> confirm which port the panel plays back on.
>
> How to test (during an active call):
> 1. `make deploy` (new bridge) + copy `_investigacion/device_backups/talkback_test.wav` to the device.
> 2. With a call in progress, watch the panel's audio port (from the SDP in the
>    loopback capture, or try candidates 7078/5224/etc.).
> 3. `curl -XPOST http://192.168.1.38:8082/api/talkback/start -d '{"host":"127.0.0.1","port":<PUERTO>,"source":"/var/volatile/talkback_test.wav","loop":true,"arm":true}'`
> 4. Listen whether the panel plays the tone. Iterate over ports if not.

Both downstream flows need an **active native session** (eye/auto-on button or a
real call). This gating is the backbone of both features.

## Shared prerequisites (do these first)

### P0 — Validate the return-audio path (blocker for talk-back)
**TESTED 2026-07-07 — result: port 4000 is NOT the return-audio port** for the
local eye+phone flow. During an active session, `4000`/`40004` are **not bound**
by anything; instead the session opens **dynamic RTP ports** (observed: 45703,
55485, 60497 + IPv6) negotiated via SIP/SDP. A Speex tone sent to `127.0.0.1:4000`
was heard by no one. The `udpproxy` 40004→4000 (from slyoldfox) was likely for
the **mobile-app** call flow, not the local monitor.

**Implication:** talk-back cannot be a fixed-port UDP send. The return-audio RTP
port is **SDP-negotiated per session**, so talk-back must participate in the SIP
session (learn the negotiated port from the SDP) — it is therefore **coupled to
Feature B (SIP/WebRTC)**, not a standalone quick win.

**SDP capture (2026-07-07): the local eye+phone flow is NOT SIP-based.** A
tcpdump on `lo` (ports 5060/5070/5080) during an eye+phone session showed only
**TCP keepalives** on our own two SIP registrations (webrtc + c300x) to Flexisip
— **no INVITE, no SDP**. The native local monitor (bt_vct/bt_av_media) handles
the entrance-panel audio/video **internally** (SCS bus + local RTP); SIP/Flexisip
is only for external / mobile-app calls. So there is no SDP to read for the local
flow, and the return-audio path lives inside bt_av_media on internally-negotiated
dynamic ports.

**Refined next steps for talk-back (deeper RE, not a quick test):**
1. During a session, identify which of the new dynamic UDP ports **bt_av_media
   (pid 687) binds/listens on** — a listening port is a candidate return-audio
   sink to inject into. Probe each with the `/tmp/tone.wav` pipeline.
2. Investigate whether an OpenWebNet/`*7*` command exists to open a *receive*
   audio path (the mirror of `*7*300`, which only *sends* to us).
3. Alternatively, drive talk-back through an **originated SIP call** where WE
   author the SDP (mobile-app-style flow via Flexisip), so we control the return
   RTP port — but this collides with the dual-role c300x registration (we answer
   our own calls; the native c300x never engages). Resolving that is a
   prerequisite.

Conclusion: talk-back is a **multi-session RE effort**, not a single validation.
Downstream audio (Speex PT110) is solved; the upstream is the hard part.

**Port-probe result (2026-07-07, 3 attempts): direct UDP injection does NOT
work.** Method: during a session, enumerated bt_av_media (pid 687) recv UDP
ports, classified them by live traffic (2 carry panel media, ~4 idle), and
injected a looped Speex-PT110 tone to all idle candidates in parallel (~15s,
session kept alive by screen touch). **No tone was ever heard at the entrance
panel.** So bt_av_media does not play audio blindly injected to its idle recv
ports — the return path requires an explicit **"arm/open external audio"
command** first (the mobile-app flow), not raw RTP. The idle recv ports are
likely RTCP / internally reserved. Note: the session auto-disconnects after a
timeout unless the screen is touched — keep it alive during any test.

**Only remaining lead (bigger effort, not attempted):** capture what the
**BTicino mobile app** sends when it enables talk-back during a call — the
OpenWebNet command(s) + the UDP flow — to learn the arm command and the real
port/codec. Needs the app + a capture rig. Until then, talk-back is **parked**.

### P1 — Bundle a GStreamer runtime with Opus
The device `gst` lacks `opusenc`/`opusdec`. Browser/WebRTC audio is Opus; the
intercom is Speex. Transcoding needs Opus plugins.
- Ship a self-contained gst runtime (like r0bb10's `gst/` dir, ~9 `.so`),
  isolated via `LD_LIBRARY_PATH` / `GST_PLUGIN_PATH` / `GST_PLUGIN_SCANNER` env
  so it never touches the read-only rootfs.
- Deploy it alongside the binary (extend `deploy-standard.sh` with a `--gst`
  step, base64 like the web assets).
- Acceptance: `opusenc`/`opusdec` usable from an isolated pipeline on-device.

### P2 — Decide the WebRTC transport (blocker for camera)
HA's MQTT `camera` cannot do WebRTC. Three options:
- **(a) pion in the bridge + a custom HA integration** (Python, separate repo).
  Full control, lowest latency, but a whole new deliverable.
- **(b) go2rtc** on the device/HA bridging our RTSP → WebRTC. Least code, but an
  external dependency and needs the RTSP video pipeline running.
- **(c) keep the MQTT snapshot camera** (current) for stills only.
- Recommendation: prototype **(b) go2rtc** first (fast to prove value), design
  **(a) pion** only if latency/integration demands it.

---

## Feature A — Two-way audio (talk-back)

**Goal:** speak from HA/browser to the door panel.

**Depends on:** P0 (return path), P1 (Opus gst).

### Architecture
```
Browser mic (Opus, WebRTC)
   → bridge intake (pion or a simple RTP endpoint)
   → gst: opusdec → speexenc → rtpspeexpay
   → udpsink 127.0.0.1:4000  (intercom return-audio)
```
Plus the existing downstream (`*7*300` type=2) for the door→user direction, so
HA gets both ways.

### Phases
1. **P0 + P1** (prerequisites above).
2. **Uplink pipeline (canned):** file/tone → Speex → port 4000, gated behind an
   active session, single-shot, supervised gst (restart budget). Prove the
   pipeline end-to-end without a browser.
3. **Live intake:** accept an audio stream from the client. Simplest first: an
   HTTP/WebSocket audio push or an RTP endpoint; full WebRTC only with Feature B.
4. **Session orchestration:** ensure video+audio session (eye/call) active →
   open uplink → close on hangup. Reuse the cooperative-command safety model
   (one command, no retry loops).
5. **HA surface:** a `switch`/`button` "Talk" (or via the WebRTC camera's
   backchannel once B exists).

### Risks
- Return path may need a specific "arm" command or may echo → P0 answers this.
- Bundled gst size/compat on i.MX6.
- Half-duplex intercom: talk-back may cut the door→user audio.
- **Safety:** audio path touches native state; keep single-shot commands, gating,
  and test with the user present (remember the relay/watchdog incident).

### Effort: **High.**

---

## Feature B — Native WebRTC camera (low-latency live video in HA)

**Goal:** live H.264 (+audio, +talk-back) in HA without go2rtc round-trips.

**Depends on:** P2 (transport), P1 (Opus, for audio track), Feature A (backchannel).

### Option B1 — go2rtc bridge (fast path)
```
*7*300 → our RTP → RTSP :6554 (existing, needs video_on_demand)
   → go2rtc  → WebRTC → HA camera
```
- Phases: run go2rtc on device/HA; config a stream from our RTSP; point HA's
  generic camera / WebRTC at go2rtc; document. Reader-driven start of the RTSP
  pipeline so it only runs when watched.
- Pros: little new code. Cons: external dep; latency of the RTSP hop.

### Option B2 — pion in the bridge + custom HA integration (full path)
```
*7*300 → our RTP (H.264 PT96) ──┐
                                 ├→ pion PeerConnection (H.264 passthrough track)
Speex → opusdec/opusenc → Opus ──┘   + Opus audio track  + mic backchannel (Feature A)
                                 → trickle ICE (localOutboundIP already available)
                                 → HA custom integration (Python, separate repo)
                                    camera entity w/ async_handle_web_rtc_offer
```
- Phases:
  1. pion PeerConnection, H.264 passthrough video track fed from the RTP.
  2. Audio track (Speex→Opus via bundled gst) + mic backchannel (Feature A).
  3. Trickle ICE, interface filtering, pending-candidate queue.
  4. **Custom HA integration** (new repo, HACS): camera with WebRTC offer/answer,
     config flow, talk button. This is a whole separate Python deliverable.
  5. Reader-driven lifecycle: start streams on first viewer, stop on last.
- Pros: lowest latency, two-way, no external dep. Cons: large; needs the HA
  custom component (own repo), which is a project in itself.

### Risks
- Native WebRTC in HA **requires a custom integration or go2rtc** — MQTT can't do
  it. This is the fork in the road.
- H.264 from the camera is 720×576@~7fps interlaced PAL — fine for WebRTC but
  keyframe cadence (SPS/PPS `config-interval=1`) matters for fast join.
- Codec transcode for audio needs P1.

### Effort: **B1 medium, B2 very high.**

---

## Suggested order

1. **P0** — validate return-audio port 4000 (cheap, unblocks everything audio).
2. **P2 prototype** — go2rtc over our RTSP → WebRTC in HA (fast win for live video).
3. **P1** — bundle Opus gst (unblocks talk-back and WebRTC audio).
4. **Feature A** phases 2–5 (talk-back).
5. **Feature B2** only if go2rtc latency/integration proves insufficient — and as
   its own HA-integration repo.

Everything stays behind the existing safety gates (`video_on_demand`,
single-shot commands). Nothing auto-activates.
