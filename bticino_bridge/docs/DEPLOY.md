# Deploying to the device

## Prerequisites

- Go 1.25+ and Node.js on the build machine.
- An SSH host `bticino` in `~/.ssh/config` that logs in to the unit as `root2`
  with your key. The unit's dropbear needs the old RSA algorithms:

  ```
  Host bticino
    HostName <device-ip>
    User root2
    IdentityFile <path-to-key>
    HostKeyAlgorithms +ssh-rsa
    PubkeyAcceptedAlgorithms +ssh-rsa
  ```

- The model is set at build time in `pkg/bticino/constants.go`
  (`DeviceVariant`), see the bridge [`README.md`](../README.md).

## One command

```bash
cd bticino_bridge
make deploy
```

`make deploy` builds the web UI and the ARM binary, then runs
`scripts/deploy.sh`, which:

1. Transfers the binary, `configs/config.yaml` (as `config.yaml`), the web UI
   and the Swagger files to `/home/bticino/cfg/extra/`. Files go over SSH as
   base64; only files that changed are written.
2. Keeps the previous binary and config as `*.backup.<timestamp>` hard links
   (they copy no data) and keeps the last two of each.
3. Stops the bridge with TERM, so open camera sessions get their BYE, waits up
   to 15 s for it to exit, then starts the new one.
4. Checks that the process runs; if not, it prints the end of the crash log.

## Options

Run `scripts/deploy.sh` directly to use them:

| Option | Effect |
|---|---|
| `--skip-web` | Use the previous web build |
| `--skip-go` | Use the previous binary |
| `--skip-config` | Keep the config on the device |
| `--skip-docs` | Do not transfer the Swagger files |
| `--config FILE` | Deploy another config file |
| `--dry-run` | Only show what would be done |
| `--verbose` | More output |

## Rollback

Put a backup back in place and restart the bridge:

```bash
ssh bticino 'cd /home/bticino/cfg/extra && ls bticino_bridge.backup.*'
ssh bticino 'cd /home/bticino/cfg/extra && cp bticino_bridge.backup.<timestamp> bticino_bridge'
```

Then restart it from the Controls page, or with:

```bash
ssh bticino 'cd /home/bticino/cfg/extra && pkill bticino_bridge; sleep 3; nohup ./bticino_bridge -config config.yaml > /dev/null 2>&1 &'
```

## Device facts

- The binary is `/home/bticino/cfg/extra/bticino_bridge` and runs with
  `-config config.yaml` from that directory.
- The root filesystem is read-only; everything lives in
  `/home/bticino/cfg/extra/`.
- The unit has a hardware watchdog: a command storm against the native
  processes can make it reboot. Keep `streaming.video_on_demand` off.
