# BTicino Classe 100X / 300X — rooted firmware and Home Assistant bridge

Run a BTicino Classe 100X or 300X video door entry unit with
[Home Assistant](https://www.home-assistant.io/), without the official cloud
app. The project builds a rooted firmware image from the official firmware you
already own and adds `bticino_bridge`, which exposes the unit over MQTT (with
Home Assistant auto-discovery), HomeKit, a web dashboard and RTSP video.

## About this fork

This repository is a fork of
[fquinto/bticinoClasse300x](https://github.com/fquinto/bticinoClasse300x).
Changes from upstream are merged into it. The fork adds its own work on the
bridge: a redesigned web UI, security and shutdown fixes, English entity names,
and an SSH watchdog in the firmware. It is tested on a Classe 100X; the Classe
300X features are not tested here.

## Getting started

1. Build the firmware with `legacy/main.py` and flash it with MyHOME Suite, see
   [`legacy/README.md`](legacy/README.md).
2. Deploy the bridge (needs Go 1.25+ and Node.js on the build machine):
   ```bash
   cd bticino_bridge
   make deploy
   ```
3. Open the dashboard at `http://<device-ip>:8082` and set up MQTT on the
   Settings page.

Details: [`bticino_bridge/README.md`](bticino_bridge/README.md), changes:
[`bticino_bridge/CHANGELOG.md`](bticino_bridge/CHANGELOG.md), OpenWebNet
commands: [`docs/openwebnet-commands.md`](docs/openwebnet-commands.md).

## Related projects

- [`intercom-firmware-tool`](https://github.com/jorgeavlobo/intercom-firmware-tool)
  by jorgeavlobo: a firmware tool with a Windows-friendly flow.
- [`bticino-classe100x-ha`](https://github.com/jorgeavlobo/bticino-classe100x-ha)
  by jorgeavlobo: a lighter Classe 100X ↔ Home Assistant integration.

## Safety and license

You bring your own firmware; this project does not host or redistribute BTicino
images ([`docs/bring-your-own-firmware.md`](docs/bring-your-own-firmware.md)).
Flashing can brick the unit; read [`THREAT_MODEL.md`](THREAT_MODEL.md) first.
Security issues: [`SECURITY.md`](SECURITY.md). Licensed under the GNU GPL v2
([`LICENSE`](LICENSE)).
