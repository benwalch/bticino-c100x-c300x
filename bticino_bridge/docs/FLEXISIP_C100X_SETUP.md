# Flexisip SIP setup — Classe 100X (C100X)

**Device:** Classe 100X (C100X), domain `<DEVICE_ID>.bs.iotleg.com`
**Date verified:** 2026-10-11
**Status:** users created, REGISTER binds confirmed; re-REGISTER recovery is bridge-side work (see §6)

This replaces `setup_flexisip_local.sh` for this unit — do **not** run that
script here (see §7 for why).

## 1. Goal

Give the bridge two working local SIP identities (`webrtc` = caller,
`c100x` = answerer, selected via `bticino.DeviceVariant`) so self-INVITE,
auto-answer, on-demand video and talkback can work. The passive live-video
tap needs no SIP and is unaffected either way.

Non-goal: doorbell routing. Doorbell press is learned via the OWN bus
(`doorbell.pressed`), never via SIP INVITE — nothing below changes who
receives doorbell calls.

## 2. Pre-state found

* `/etc/flexisip/users/users.db.txt` held exactly one user (gateway UUID).
  No `webrtc`/`c100x`/`c300x` — initial REGISTERs got `200 (trusted-hosts)`
  but created no usable state; re-REGISTERs timed out forever.
* Transports (stock, kept): `sips:<lan-ip>:5061` + `sip:127.0.0.1:5060`,
  `trusted-hosts=127.0.0.1`.
* `route.conf` is a symlink to `route_ext.conf`: `alluser@domain → gateway
  user → cloud`. No local-user routes (none needed — the registrar routes
  to registered contacts on its own).

## 3. Changes applied (device)

Backup first (persistent partition):

```bash
cp /etc/flexisip/users/users.db.txt \
  /home/bticino/cfg/extra/users.db.txt.bak-$(date +%s)
```

Append (password == username convention — the same hashes the bridge
derives as `MD5(user:domain:user)`, so no secret needs storing anywhere):

```bash
D=<DEVICE_ID>.bs.iotleg.com
printf '%s\n' \
  "webrtc@$D md5:$(echo -n "webrtc:$D:webrtc" | md5sum | cut -d' ' -f1) ;" \
  "c100x@$D md5:$(echo -n "c100x:$D:c100x" | md5sum | cut -d' ' -f1) ;" \
  >> /etc/flexisip/users/users.db.txt
```

(The gateway-UUID line is untouched.)

Restart flexisip via the init script (NOT a reboot), restoring the stock
binds. Pass the current LAN IP so the TLS listener keeps it:

```bash
/etc/init.d/flexisipsh stop; sleep 2
/etc/init.d/flexisipsh start <DEVICE_IP>   # use the current LAN IP
```

Verify the invocation (must show `sips:<lan-ip>:5061… sip:127.0.0.1…`):

```bash
for p in $(ls /proc | grep -E '^[0-9]+$'); do
  grep -qa 'usr/bin/flexisip' /proc/$p/cmdline 2>/dev/null &&
    tr '\0' ' ' < /proc/$p/cmdline
done
```

Then restart the bridge (own restart endpoint) so both identities REGISTER
fresh — re-registration reuses dead sockets and never redials (bridge-side
gap, §6).

## 4. Verification (2026-10-11)

* Manual `REGISTER webrtc@…` → instant `SIP/2.0 200` with `pub-gruu`.
* The 200 lists the bridge's live contact (`127.0.0.1:42356`, GRUU,
  `expires=300`) — bindings work.
* Bridge log after restart: both identities `registered (no auth required,
  trusted-hosts)`, no 407 (expected from loopback).

## 5. Deliberately NOT changed

* `setup_flexisip_local.sh` — hardcoded `192.168.1.38` / `<GATEWAY_ID>.…`
  domain, creates a `baresip` user the bridge never uses, dead copy-hash
  fallbacks here, a route to a bare IP, and a LAN-wide SIP/TCP bind.
* No static routes for `webrtc`/`c100x` — registrar bindings cover it.
* No `route_int.conf` doorbell-group edit — would fork doorbell INVITEs
  to the bridge and change native behavior.
* No reboot — init-script restart is enough.
* `trusted-hosts` stays loopback-only.

## 6. Bridge-side follow-ups (repo)

* ~~Reconnect-on-failure in `registrationManager` (`pkg/sip/client.go`)~~ —
  **done 2026-10-17**: a lost connection re-dials and re-registers that identity
  with 1s→60s backoff (`pkg/sip/sip_reconnect.go`); a per-connection generation
  counter ignores errors from replaced sockets. Verified by two forced Flexisip
  outages.
* TCP framing rewrite (same file, uncommitted at time of writing):
  per-identity leftover buffering + Content-Length framing; the old
  single-`Read` splitter drops coalesced messages and chokes on fragments
  (`got 2 messages in one read`, `failed to parse SIP message` in logs).

## 7. Rollback

⚠️ **`flexisipsh start` alone is not enough on this unit.** The TLS transport
(5061) needs the keystore passphrase, which is only pushed at boot through
`/var/tmp/bt_answering_machine.fifo` by `bt_answering_machine`. Worse, even a
hand-restarted Flexisip leaves the **native** SIP client unregistered — the
unit then cannot be reached until it is rebooted. See
[NATIVE_FIRMWARE.md](NATIVE_FIRMWARE.md) §3.4/§3.8.

```bash
cp /home/bticino/cfg/extra/users.db.txt.bak-<ts> /etc/flexisip/users/users.db.txt
# then reboot the device — do NOT restart flexisip by hand
```

Then restart the bridge. The unit returns to pre-change SIP behavior
(passive video unaffected throughout).
