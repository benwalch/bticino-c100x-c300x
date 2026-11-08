# Possible future features

Plans that were worked out and tested in part, but are not implemented. Each entry
says what is verified and what is not, so it can be picked up later without
repeating the analysis. Upstream ideas stay in [BACKLOG.md](BACKLOG.md).

## Bridge autostart after a reboot

**Status:** planned 2026-11-07, not implemented. Effort was judged too high for the
benefit; a manual start from Home Assistant is preferred for now, see
[Manual start from Home Assistant](#manual-start-from-home-assistant).

### Problem

After a reboot of the intercom, the bridge does not start by itself. The firmware
reboots on its own now and then (`REBOOT (bt_eliot)` in the syslog, cause unknown),
and after a power loss the bridge is also gone until someone starts it.

### Findings on the device

| Finding | Evidence | Consequence |
|---|---|---|
| A series of firmware reboots happened without a running bridge | No SSH login between those reboots, while later logins are logged; without autostart nobody could have started the bridge | These reboots were not caused by the bridge |
| Restarting the bridge does not reboot the device | Several bridge restarts within one uptime of over a day | Starting at every boot adds little bus load: at start the bridge only sends status queries |
| Flexisip (SIP, `127.0.0.1:5060`) starts only once WLAN is up | Syslog: `Network is up: try to start flexisip`; WLAN took between about 12 s and 2 min | The bridge must wait for it |
| Without Flexisip the bridge gives up SIP | 5 attempts, about 45 s, then SIP stays off until the next start | Starting too early is harmful for the whole run |
| Without a reachable broker MQTT stays off | Tested: broker comes up after the first attempt, 0 messages are ever sent | After a power loss Home Assistant may boot later than the intercom |
| After a power loss the clock is wrong for about 2 min | Syslog shows a date in the past until the NTP jump | Wait for the clock as well, briefly |
| OpenWebNet (`20000`) is up a few seconds after boot | `Openserver is WORKING!!!` about 5 s after the kernel start | Easy to wait for |

The boot order is sysvinit, runlevel 5: `S98-bt_daemon-apps.sh` (may wait up to 100 s
for the Cortex-M), `S98dropbear`, then the `S99` scripts. The root filesystem is
read-only; `/home/bticino/cfg/extra` is writable flash and survives a reboot.

### Options

| | Option | Assessment |
|---|---|---|
| **A** | **One start per boot from `/etc/rc5.d`, waiting for the services** | **Recommended.** Deploy and the restart button keep working unchanged. |
| B | `respawn` in `/etc/inittab` | Rejected: in the test, the deploy sequence left **two bridges running**, because `init` restarts the bridge while `deploy.sh` starts its own. Deploy and the restart button would need rework. |
| C | A plus a restart after a crash | Not now: no crash recorded in `bticino_bridge.crash.log` so far, which covers only a short time. |

### Design of option A

1. **Script `/etc/init.d/bt_bridge_autostart`**, linked as
   `/etc/rc5.d/S99bt_bridge_autostart`. The name must differ from the binary:
   a script named `bticino_bridge` finds itself with `pgrep -x` and never starts
   the bridge (tested).
   - Waits in the background for ports `20000` and `5060`, at most 15 min, then
     for a plausible clock, at most 60 s. The boot is not blocked.
   - Starts nothing if a bridge is already running, for example after a deploy.
   - Runs under `setsid`, so the bridge gets no SIGHUP when the boot scripts end.
   - **Off switch without touching the root filesystem:** if
     `/home/bticino/cfg/extra/bticino_bridge.noautostart` exists, the script does
     nothing. It takes effect at the next boot and does not stop a running bridge.
     Open point: the prototype checks the file only at the beginning; it should be
     checked again right before the start.
   - **Reboot loop guard:** each start increments
     `bticino_bridge.early_starts` on flash; the file is removed once the bridge
     still runs after 10 min. After 3 early starts in a row, the script waits
     30 min before starting, which leaves time to set the off switch over SSH.
     It never disables the bridge for good. Without autostart a reboot ends any
     bridge problem by itself; this guard replaces that protection. There is no
     evidence that the bridge causes reboots today, it is insurance for future
     changes (a command storm after a feature change rebooted the device before,
     see CHANGELOG).
2. **MQTT connect retry:** done separately, the bridge now retries until the
   broker is reachable (see CHANGELOG).
3. **Firmware build:** add the script to `firmware_scripts/` and install it in
   `legacy/main.py` like the SSH guard, because a firmware update replaces the
   root filesystem. That build path is untested.

Prototype of the script, as tested in the emulation:

```sh
#!/bin/sh
# Starts the bridge once after boot, as soon as the firmware services it needs are up.
# It never restarts a running or crashed bridge: deploy and the web restart do that.
# The name must differ from the binary, or pgrep would find this script itself.
PATH=/sbin:/usr/sbin:/bin:/usr/bin

BRIDGE_DIR=/home/bticino/cfg/extra
BRIDGE=bticino_bridge
DISABLE_FILE=$BRIDGE_DIR/$BRIDGE.noautostart
EARLY_STARTS_FILE=$BRIDGE_DIR/$BRIDGE.early_starts

SERVICES_LIMIT_SECONDS=900
CLOCK_LIMIT_SECONDS=60
HEALTHY_AFTER_SECONDS=600
MAX_EARLY_STARTS=3
EARLY_DELAY_SECONDS=1800

note() {
    logger -t bt_bridge_autostart "$1" 2> /dev/null
}

port_is_listening() {
    grep -qsE ":$(printf '%04X' "$1") [0-9A-F]+:0000 0A" /proc/net/tcp
}

services_are_ready() {
    port_is_listening 20000 && port_is_listening 5060
}

# After a power loss the clock stands in the past until NTP corrects it.
clock_is_set() {
    [ "$(date +%s)" -gt "$(stat -c %Y "$BRIDGE_DIR/$BRIDGE")" ]
}

wait_for() {
    limit=$1
    condition=$2
    waited=0
    until "$condition"; do
        [ "$waited" -ge "$limit" ] && { note "$condition: gave up after $limit s"; return 1; }
        sleep 5
        waited=$((waited + 5))
    done
}

bridge_is_running() {
    pgrep -x "$BRIDGE" > /dev/null
}

start_when_ready() {
    wait_for "$SERVICES_LIMIT_SECONDS" services_are_ready
    wait_for "$CLOCK_LIMIT_SECONDS" clock_is_set

    # A bridge that reboots the device soon after each start must not keep it in a loop.
    early_starts=$(cat "$EARLY_STARTS_FILE" 2> /dev/null)
    case "$early_starts" in ''|*[!0-9]*) early_starts=0 ;; esac
    if [ "$early_starts" -ge "$MAX_EARLY_STARTS" ]; then
        note "$early_starts early starts in a row, waiting $EARLY_DELAY_SECONDS s"
        sleep "$EARLY_DELAY_SECONDS"
    fi

    bridge_is_running && return
    echo $((early_starts + 1)) > "$EARLY_STARTS_FILE"
    note "starting the bridge"
    cd "$BRIDGE_DIR" || return
    "./$BRIDGE" -config config.yaml &

    sleep "$HEALTHY_AFTER_SECONDS"
    bridge_is_running && rm -f "$EARLY_STARTS_FILE"
}

case "$1" in
    start)
        if [ -x "$BRIDGE_DIR/$BRIDGE" ] && [ ! -e "$DISABLE_FILE" ]; then
            # setsid: the bridge must not get a SIGHUP when the boot scripts end.
            setsid "$0" run < /dev/null > /dev/null 2>&1 &
        fi
        ;;
    run)
        start_when_ready
        ;;
esac
exit 0
```

### Test results

**Boot, in the real firmware root filesystem** (ARM under emulation, sysvinit,
`/etc/init.d/rc`, read-only root; the firmware services were simulated by
listening ports and a placeholder bridge):

| Test | Result |
|---|---|
| Boot without services | Boot not blocked, script waits, nothing starts |
| Only port `20000` up | Still waits |
| Both ports up | Exactly one start, after about 5 s; `cwd`, arguments and `/dev/null` as stdin and stderr correct, own session |
| Bridge started by a deploy during the wait | No second instance |
| Binary missing / off switch set | Nothing happens |
| Services never come (limit shortened) | Starts after the limit |
| Clock in the past, NTP jump during the wait | Starts right after the jump |
| Clock stays wrong (limit shortened) | Starts after the limit |
| 3 early starts | Delay, then start; count cleared after the healthy time |
| Bridge stopped before the healthy time | Count stays, no restart; garbage in the count file is ignored |
| Script named like the binary | Finds itself, the bridge never starts |
| Option B: `respawn` plus the deploy sequence | Two bridges running |
| Real ARM bridge started by the script | Runs with `/dev/null` output, writes its log and crash marker, exits cleanly on `TERM` |

**MQTT retry, against a real Mosquitto** (amd64, race detector on):

| Test | Result |
|---|---|
| Today: broker comes after the first attempt | 0 messages, MQTT stays off |
| With the fix: broker comes after 3 min | Online 20 s after the broker; the same 74 messages as a normal start |
| With the fix: broker restarts while connected | Reconnects |
| With the fix: `TERM` during the retry | Clean exit, no data race |
| With the fix: wrong credentials | Keeps running and retrying, but the log texts are wrong (see above) |

### Not verified

- A real boot of the device. This needs a reboot, which only the user may trigger
  or approve (see `AGENTS.md`).
- Protection against SIGHUP: the own session is verified, a real SIGHUP at boot is not.
- How the bridge copes when Flexisip restarts shortly after its start, for example
  after a WLAN change. Flexisip restarts like that also happen during normal operation.
- WLAN away for longer than 15 min after boot: the bridge then starts without SIP
  and stays so until its next start.
- The cause of the firmware reboots. Autostart does not prevent them, it only
  brings the bridge back.

### Rollout, if picked up

1. Repo: script, docs, CHANGELOG; tests.
2. Install: remount the root filesystem read-write, add script and `S99` link,
   remount read-only. The running bridge is not affected.
3. Reboot (by the user), then check: start time, one instance, SIP, MQTT, log,
   SSH guard, a door call.

Rollback: create the off switch file, or delete the script and the link.

## Manual start from Home Assistant

**Status:** idea, not planned in detail.

A button in Home Assistant that starts the bridge on the intercom when it is not
running. The bridge cannot do this itself, and its MQTT topics are gone while it
is down, so the start has to come from outside:

- Home Assistant `shell_command` that runs `ssh` to the intercom with its own key.
- On the intercom, a small start script on the flash partition, for example
  `start_bridge_if_stopped.sh`: it does nothing if `pgrep -x bticino_bridge` finds
  a bridge, otherwise it starts the bridge the same way as `scripts/deploy.sh`.
- The SSH key for Home Assistant should only be allowed to run that script
  (`command="..."` in `authorized_keys`). The keys live in
  `/etc/dropbear/authorized_keys` on the read-only root filesystem, so adding one
  needs a remount once. Whether the device's dropbear honours that option has
  not been checked.
- Trigger in Home Assistant: the availability of the bridge entities, see below,
  for example a notification when the bridge has been offline for some minutes,
  with the button next to it.

### Bridge health in Home Assistant today

- The bridge publishes `bticino/bridge/state` (retained): `online` after connecting,
  `offline` on a clean shutdown, and the broker sends `offline` as last will
  when the connection drops, for example after a crash or power loss (after the
  keepalive of 60 s runs out, up to about 90 s).
- Every discovered entity uses this topic as availability, so all bridge entities
  show *unavailable* while the bridge is down.
- There is no dedicated entity for the bridge itself. A Home Assistant template or
  MQTT binary sensor on `bticino/bridge/state` gives one. Adding it to the
  discovery is possible; it must not use the same availability, or it would show
  *unavailable* instead of *off*.
- `online` only means the MQTT connection is up. SIP has its own entity
  (`SIP Status`); there is no single "everything works" value.
