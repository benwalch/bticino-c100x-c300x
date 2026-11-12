# Possible future features

Plans that were worked out and tested in part, but are not implemented. Each entry
says what is verified and what is not, so it can be picked up later without
repeating the analysis. Upstream ideas stay in [BACKLOG.md](BACKLOG.md).

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
