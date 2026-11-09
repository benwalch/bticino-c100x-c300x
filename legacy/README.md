# Legacy flashing method

These are the original scripts and notes from the first version of this
project. Back then, getting root on a Classe 300X meant unpacking an official
`.fwz` by hand, editing the ext4 rootfs, repacking it, and flashing the result
with BTicino's MyHOME Suite on Windows. The Python helpers (`main.py`,
`bticino.py`, `diagnose_mqtt.py`), the pack/unpack shell scripts, and the
assorted comparison notes in this folder belong to that workflow.

They are kept here for reference and reproducibility. They are **not** the
recommended path anymore. The current approach builds a rooted image on the fly
from your own firmware and flashes it over USB without MyHOME Suite — see the
top-level `README.md` and the `hashes/` folder. Nothing in this
directory ships firmware: you still bring your own `.fwz`.

**In this fork:** the flasher and builder are not in this repository yet, so
`main.py` is still the only working way to build a firmware, and the only one
for the Classe 100X. Run it from the repository root:
`sudo python3 legacy/main.py`.

## Build the firmware

`main.py` asks for the model and firmware version, the root password, the SSH
key, and whether to install the MQTT scripts (`tcpdump2mqtt`, see
[`mqtt_scripts/`](../mqtt_scripts/README.md)). The new image ends up in
`custom_firmware/`. The bridge is not part of the image; it is deployed
afterwards with `make deploy`, see [`bticino_bridge/`](../bticino_bridge/README.md).

[![asciicast](https://asciinema.org/a/514007.svg)](https://asciinema.org/a/514007)

On GNU/Linux:

```bash
git clone https://github.com/benwalch/bticino-c100x-c300x.git
cd bticino-c100x-c300x
sudo python3 -m pip install --upgrade pip
sudo python3 -m pip install -r requirements.txt
sudo python3 legacy/main.py
```

With Docker:

```bash
git clone https://github.com/benwalch/bticino-c100x-c300x.git
cd bticino-c100x-c300x
docker compose run bticino
```

## Flash it with MyHOME Suite

- Download and install the "My Home Suite" configuration software (Windows
  only) from [the official page](https://www.homesystems-legrandgroup.com/en/home/-/productsheets/2493426).
- Take the unit off the wall and, while it is still connected to the SCS 2-wire
  bus, connect to it through the Mini-USB port on the back. The technical sheets
  show where the port is:
  [C100X](https://www.homesystems-legrandgroup.com/MatrixENG/liferay/bt_mxLiferayCheckout.jsp?fileFormat=generic&fileName=ST-00000695-EN.pdf&fileId=58107.23188.48156.49690) -
  [C300X](https://www.homesystems-legrandgroup.com/MatrixENG/liferay/bt_mxLiferayCheckout.jsp?fileFormat=generic&fileName=ST-00000361-EN.pdf&fileId=58107.23188.48057.62932)
- Follow the animation to select your model and flash the image built above:

![](../myhome_flash.gif)

## SSH availability

Every firmware built by `main.py` includes `bt_ssh_guard`, with or without
MQTT. It checks once a minute that port 22 is listening and otherwise restarts
dropbear. `init` respawns the guard itself (`sshg` entry in `/etc/inittab`).

- Each outage is recorded once in `/home/bticino/cfg/extra/ssh_guard.log`, with
  a copy of the system log in `ssh_guard_syslog.log` next to it, so the
  evidence survives a power cycle.
- It does not help if WLAN is down or the whole unit hangs.
- A dropbear stopped on purpose comes back within about a minute.
- To remove it, delete the `sshg` line from `/etc/inittab`, run `init q`, then
  delete `/usr/sbin/bt_ssh_guard` (the root filesystem must be writable).
