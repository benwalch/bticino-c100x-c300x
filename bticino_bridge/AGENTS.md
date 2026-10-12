# AGENTS.md — bticino_bridge

## Scope 
The software was originally written for Classe 300X — verify 300X assumptions
(LED names, voicemail, GPIO) against this unit before changing behavior.

## Device access
- Host: ask the user for the current IP at the start of every session.
- User: root2
- Key: `<repo-root>/bticinokey` (+ `.pub`); perms must stay 600; never copy key material into chat/files.
- SSH opts (required — old RSA host/key types):
  `-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o HostKeyAlgorithms=+ssh-rsa -o PubkeyAcceptedKeyTypes=+ssh-rsa -o PubkeyAcceptedAlgorithms=+ssh-rsa -o BatchMode=yes -o ConnectTimeout=10`
- Reachability: agent shell runs on the dev machine; device must be LAN-reachable from there.

## Allowed without asking (read-only)
- `cat`, `ls`, `grep`, `tail`
- `tcpdump -c N -ni lo ...` with a packet count limit
- `curl` against localhost status endpoints (`/api/status`)
- `ps`, `nc`, `netstat -tlnp | grep <port>`

## Ask first (state-changing)
- Any OWN bus send (`*8*...`, `*7*...`)
- `bticino_bridge` restart / rebuild / redeploy
- Any file write on device
- `ffplay`/`ffmpeg` RTSP captures longer than ~15 s
- Deployment is always a separate approval: running `scripts/deploy.sh`
  (or transferring binaries/config/web to the device by any other means)
  requires an explicit go-ahead every time — even inside an otherwise
  approved write command. Never bundle it silently.

## Forbidden
- `reboot`, flashing, `iptables` changes, dropping SSH (`dropbear`) management