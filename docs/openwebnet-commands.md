# OpenWebNet commands and discovery

The Classe 300X and 100X speak OpenWebNet on their local bus: commands
`*WHO*WHAT*WHERE##`, queries `*#WHO*WHERE*DIM##` and writes
`*#WHO*WHERE*#DIM*VALUE##`. These are the frames known so far, useful whether
you drive the device through the bridge or through plain MQTT/shell. They are
a reference, not a guarantee: WHERE values depend on your installation.

Each table says where a frame comes from:

- **observed**: seen on the bus of a Classe 300X.
- **verified (100X)**: sent and checked on a Classe 100X.
- **firmware**: read from the native firmware (what the device's own UI
  sends), not sent from outside yet. Treat these as a lead, not as tested.

## Known commands and notifications

| Frame | Meaning |
| --- | --- |
| `*8*19*20##` | main door opening button pressed |
| `*8*20*20##` | main door opening button released |
| `*8*19*21##` | secondary door opening button pressed |
| `*8*20*21##` | secondary door opening button released |
| `*8*21*10##` | stairs light activated |
| `*8*1#5#4#20*10##` | intercom camera on |
| `*8*3#5#4*420##` | intercom camera off |
| `*8*1#1#4#21*10##` | incoming external call |
| `*8*1#1#4#21*11##` | incoming external call |
| `*8*1#1#4#21*16##` | incoming external call |
| `*7*59#12#0#0*##` | incoming internal call |

All frames in this table are **observed**. Opening a door is usually a press
frame followed by a release frame about a second later, for example
`*8*19*20##` then `*8*20*20##`.

## System information (WHO 13)

Queries; the answer repeats the frame with the values appended. The bridge
reads them at startup. **Verified (100X).**

| Query | Answer |
| --- | --- |
| `*#13**10##` | IP address, `*#13**10*A*B*C*D##` |
| `*#13**11##` | netmask, `*#13**11*A*B*C*D##` |
| `*#13**12##` | MAC address, six decimal numbers |
| `*#13**16##` | firmware version, `*#13**16*V1*V2*V3##` |
| `*#13**17##` | hardware version, `*#13**17*V1*V2*V3##` |
| `*#13**23##` | kernel version |
| `*#13**24##` | distribution version |

## Device settings

These frames are handled by the native processes, not by the bus. They only
take effect in a command session on port 20000 (see below).

| Frame | Meaning | Source |
| --- | --- | --- |
| `*8*99*##` | reload ringtones: `bt_answering_machine` rereads `settings.xml` and relinks the files `bt_av_media` plays | verified (100X) |
| `*8*81*##` | reload configuration | firmware |
| `*7*58#<type>#0#0**##` | play the ringtone set for a ringtone type; which number is which type is not known yet | firmware |
| `*7*55**##` | stop the ringtone (not the same frame as the video stop `*7*55*##`) | firmware |
| `*#8**#33*<0\|1>##` | ringer mute; the status frame `*#8**33*0##` / `*#8**33*1##` reports it | firmware, status observed |
| `*#8**#41*<0-100>##` | ringtone volume | firmware |
| `*#8**#42*<0-100>##` | playback volume | firmware |
| `*#8**#43*<0-100>##` | voice memo playback volume | firmware |
| `*#8**#44*<0-100>##` | door entry call volume | firmware |
| `*#8**#45*<0-100>##` | intercom call volume | firmware |
| `*#8**#50*<0-100>##` | SIP intercom call volume | firmware |
| `*#7**#5*<0-100>##` | image brightness | firmware |
| `*#7**#6*<0-100>##` | image contrast | firmware |
| `*#7**#7*<0-100>##` | image colour saturation | firmware |
| `*7*70#<0\|1>*##` | microphone mute | firmware |
| `*7*73#<on>#<brightness>*##` | display on/off and brightness | firmware |

The device's own UI keeps the volumes in memory and sends the ringtone volume
(`#41`) again each time the unit rings, so a volume written from outside lasts
only until the next ring. Ringtones are different: the UI only names the
ringtone type and `bt_av_media` plays whatever file the reload linked, which is
why the bridge can set ringtones but not the ring volume.

## Sending a command on the device

The native `openserver` listens on localhost:

- **Port 30006** takes one frame per connection, for example with netcat:

  ```sh
  echo '*8*19*20##' | nc 0 30006
  ```

  Frames meant for the native processes, such as `*8*99*##`, are accepted
  there but have no effect.
- **Port 20000** is the OpenWebNet server proper. Open a command session
  first: read the greeting (`*#*1##`), send `*99*0##`, wait for `*#*1##`,
  then send the frame. It answers queries with data, and it passes frames
  such as `*8*99*##` on to the native processes. On a Classe 100X the
  session needs no password. The bridge sets ringtones this way.

## Discovering new commands

- **MQTT sniffing:** watch the MQTT topics (the bridge or `mqtt_scripts/`
  republish bus traffic) with any MQTT listener while you trigger the action
  from the official app or a physical button.
- **TCP sniffing on the device:** capture loopback traffic, trigger the action,
  then read the capture in Wireshark:

  ```sh
  ssh root2@<intercom_ip> '/usr/sbin/tcpdump -i lo -U -w - "not port 22"' > capture.pcap
  # trigger the action from the app, then Ctrl+C
  wireshark capture.pcap
  ```

Replace `<intercom_ip>` with your unit's address. Match the new frame against
the WHO/WHAT/WHERE structure above to label it.
