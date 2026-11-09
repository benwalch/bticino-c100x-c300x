# Configuration

`config.yaml` is the only configuration file. `make deploy` copies it to the
device as `/home/bticino/cfg/extra/config.yaml`, and the bridge runs with
`-config config.yaml` from that directory. Use `scripts/deploy.sh
--skip-config` to keep the file on the device instead.

The file holds secrets (MQTT, web login, SIP). Do not commit real values.

## Editing

- **Web UI:** the Settings page at `http://<device-ip>:8082/settings`. Saving
  writes a backup to `/home/bticino/cfg/extra/backups/config_<timestamp>.yaml`
  first. The saved file loses its comments.
- **By hand:** edit the file on the device, then restart the bridge from the
  Controls page.

The bridge [`README.md`](../README.md) lists the options that matter most.
Options the bridge never applied were removed from the Settings page; see
[`CHANGELOG.md`](../CHANGELOG.md) under 1.0.0.
