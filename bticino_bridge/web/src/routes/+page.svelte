<script>
  import { onMount, onDestroy } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import {
    MonitorSpeaker, Clock, HardDrive, MemoryStick, Thermometer, Voicemail,
    Network, Radio, Globe, Activity, ChevronDown, Circle,
    Lock, Lightbulb, BellOff, PhoneOff, Phone, WifiOff, Wifi, Repeat,
    Video, VideoOff, Mail, PhoneMissed
  } from 'lucide'
  
  let status = null
  let loading = true
  let error = null
  let eventSource = null

  // Function of each monitored pin. Tables differ per model (pins 60/176/180
  // mean different things on 300X vs 100X); selected via status.device.variant.
  const GPIO_INFO_300X = {
    12:  { label: 'TV demod power',   desc: 'tda9885 video demodulator power' },
    13:  { label: 'Input',            desc: 'sysfs input — function not identified' },
    47:  { label: 'Input',            desc: 'sysfs input (high) — function not identified' },
    49:  { label: 'Aux ringer',       desc: 'Enable supplementary ringer (suoneria supplementare)' },
    52:  { label: 'PoE power',        desc: 'Microsemi PoE controller power' },
    54:  { label: 'Audio amp',        desc: 'TPA2028D1 speaker amplifier enable' },
    56:  { label: 'Microphone',       desc: 'Mic power enable — goes ON while recording a voice memo' },
    58:  { label: 'Speaker mute',     desc: 'Mute speaker' },
    60:  { label: 'Amplifier',        desc: 'Enable amplifier' },
    154: { label: 'Audio path A',     desc: 'Enable audio path' },
    155: { label: 'Audio path B',     desc: 'Enable audio path' },
    176: { label: 'PoE reset',        desc: 'Microsemi PoE controller reset' },
    180: { label: '5V audio',        desc: 'Enable 5V audio stage — goes OFF while recording (avoids feedback)' }
  }

  // Classe 100X GPIO functions, verified on-device 2026-10-11 (sysfs-logical).
  const GPIO_INFO_100X = {
    47:  { label: 'Extra power',   desc: 'ADDITIONAL_PWR rail (static ON)' },
    64:  { label: 'Amplifier',     desc: 'en_ampli — ON while answered (brief pulse on chime)' },
    67:  { label: 'Telecoil',      desc: 'en_tcoil — ON only while answered' },
    71:  { label: 'TV demod',      desc: 'tda9885 power — ON during ring+call' },
    73:  { label: 'Suppl ringer',  desc: 'en_suppl_ring — chime pulse, unmuted only' },
    77:  { label: 'Phone path',    desc: 'en_phon — ON during ring+call' },
    148: { label: 'Safe lock',     desc: 'SECURE_LOCK service (static, not the release)' },
    176: { label: 'PoE reset',     desc: 'rst_microsemi — inverted: 0 in session, 1 idle' },
    180: { label: 'LCD DEV4',      desc: 'LCD select line (static)' },
    187: { label: 'LCD DEV1',      desc: 'LCD select line (static)' },
    189: { label: 'LCD DEV2',      desc: 'LCD select line (static)' },
    190: { label: 'LCD DEV3',      desc: 'LCD select line (static)' }
  }

  function gpioInfo(pin) {
    const table = status?.device?.variant === 'c100x' ? GPIO_INFO_100X : GPIO_INFO_300X
    return table[pin]
  }

  // Friendly names for sysfs LEDs. Names differ per model: Classe 300X uses
  // led_lock/led_vct_*/led_memo/led_exc_call/led_gwifi/led_ans_machine, while
  // Classe 100X (C100X) uses led_door_lock/led_horn_*/led_p*/led_star/
  // led_call_exp/led_autosw/led_wifi. Unknown LEDs fall back to the raw name.
  // Verified on C100X; 300X entries below the verified block are legacy guesses.
  const LED_INFO = {
    // --- Classe 100X ---
    led_call_exp:  { label: 'Muted',           desc: 'Ringer excluded — ON while doorbell sound is muted' },
    led_door_lock: { label: 'Door lock',       desc: 'ON while the door-release key is active' },
    led_star:      { label: 'Staircase light', desc: 'ON while the staircase-light (star) key is active' },
    led_horn_r:    { label: 'Phone red',       desc: 'ON while the red (hangup) phone key is active' },
    led_horn_g:    { label: 'Phone green',     desc: 'Likely the green (answer) phone key' },
    led_p1:        { label: 'Button P1',                desc: 'Programmable Button 1' },
    led_p2:        { label: 'Button P2',                desc: 'Programmable Button 2 — unconfirmed' },
    led_p3:        { label: 'Button P3',                desc: 'Programmable Button 3 — unconfirmed' },
    led_p4:        { label: 'Button P4',                desc: 'Programmable Button 4 — unconfirmed' },
    led_autosw:    { label: 'Autoswitch',      desc: 'Function not identified' },
    led_wifi:      { label: 'WiFi',            desc: 'ON while no WiFi connection' },
    // --- Classe 300X ---
    led_lock:        { label: 'Door lock',       desc: 'Door open (lock active)' },
    led_vct_green:   { label: 'Video call',      desc: 'Active video communication' },
    led_vct_red:     { label: 'Video call red',  desc: 'Unknown' },
    led_memo:        { label: 'Messages',        desc: 'Pending messages' },
    led_exc_call:    { label: 'Missed call (?)', desc: '300X-only; never verified ON' },
    led_gwifi:       { label: 'WiFi',            desc: 'Unknown (WiFi?)' },
    led_ans_machine: { label: 'Answering',       desc: 'Unknown (answering machine?)' }
  }
  
  onMount(async () => {
    await loadStatus()
    
    // Connect to SSE for real-time LED/GPIO updates
    if (typeof EventSource !== 'undefined') {
      eventSource = new EventSource('/api/events')
      eventSource.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data)
          if (data.type === 'leds' && data.leds) {
            status.leds = data.leds
          } else if (data.type === 'gpio' && data.gpio) {
            status.gpio = data.gpio
          }
        } catch (e) {
          console.error('SSE parse error:', e)
        }
      }
      eventSource.onerror = () => {
        console.log('SSE connection error, falling back to polling')
        setInterval(loadStatus, 30000)
      }
    } else {
      setInterval(loadStatus, 30000)
    }
  })
  
  onDestroy(() => {
    if (eventSource) {
      eventSource.close()
    }
  })
  
  async function loadStatus() {
    try {
      const response = await fetch('/api/status')
      if (!response.ok) throw new Error('Failed to fetch status')
      status = await response.json()
      error = null
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  // Programmable buttons: one dot per button number.
  function dots(n) {
    const xs = { 1: [12], 2: [8, 16], 3: [6, 12, 18], 4: [4.5, 9.5, 14.5, 19.5] }[n]
    return xs.map(x => ['circle', { cx: String(x), cy: '12', r: '1' }])
  }

  const LED_ICON = {
    led_call_exp: BellOff, led_door_lock: Lock, led_star: Lightbulb,
    led_horn_r: PhoneOff, led_horn_g: Phone, led_autosw: Repeat, led_wifi: WifiOff,
    led_p1: dots(1), led_p2: dots(2), led_p3: dots(3), led_p4: dots(4),
    led_lock: Lock, led_vct_green: Video, led_vct_red: VideoOff, led_memo: Mail,
    led_exc_call: PhoneMissed, led_gwifi: Wifi, led_ans_machine: Voicemail
  }

  const MB = 1024 * 1024

  function percent(used, total) {
    return total ? Math.round((used * 100) / total) : 0
  }

  // /etc/version is a build stamp like 20250522064330.
  function firmwareDate(stamp) {
    return /^\d{14}$/.test(stamp) ? `${stamp.slice(0, 4)}-${stamp.slice(4, 6)}-${stamp.slice(6, 8)}` : stamp
  }

  function since(iso) {
    return new Date(iso).toLocaleString(undefined, {
      day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit'
    })
  }

  function connectionsOf(s) {
    const c = s.components || {}
    const list = []
    if (c.openwebnet) {
      list.push({ name: 'OpenWebNet', detail: `Port ${c.openwebnet.port}`, icon: Network,
        ok: c.openwebnet.status === 'active', state: c.openwebnet.status === 'active' ? 'Active' : c.openwebnet.status })
    }
    if (s.mqtt) {
      list.push({ name: 'MQTT', detail: `Broker ${s.mqtt.broker}`, icon: Radio,
        ok: s.mqtt.connected, state: s.mqtt.connected ? 'Connected' : 'Disconnected' })
    }
    if (c.web_dashboard) {
      list.push({ name: 'Web dashboard', detail: `Port ${c.web_dashboard.port}`, icon: Globe,
        ok: c.web_dashboard.status === 'active', state: c.web_dashboard.status === 'active' ? 'Active' : c.web_dashboard.status })
    }
    if (c.message_parser) {
      list.push({ name: 'Message parser', icon: Voicemail,
        detail: `${c.message_parser.messages_found} messages, ${c.message_parser.new_messages} new`,
        ok: c.message_parser.status === 'active', state: c.message_parser.status === 'active' ? 'Active' : c.message_parser.status })
    }
    return list
  }

  $: connections = status ? connectionsOf(status) : []
  $: sys = status?.system || {}
</script>

<div class="dashboard">
  {#if loading}
    <div class="loading">Loading...</div>
  {:else if error}
    <div class="error">Error: {error}</div>
  {:else if status}
    <section class="card device" aria-label="Device">
      <div class="device-main">
        <div class="device-icon"><Icon icon={MonitorSpeaker} size={26} /></div>
        <div class="device-text">
          <div class="eyebrow">Device</div>
          <div class="device-title">
            <h1>{status.device?.model}</h1>
            {#if status.device?.variant}
            <span class="variant">{status.device.variant.toUpperCase()}</span>
            {/if}
          </div>
          <div class="muted">
            BTicino indoor unit
            {#if status.device?.firmware} · firmware {firmwareDate(status.device.firmware)}{/if}
            {#if status.boot_time} · bridge running since {since(status.boot_time)}{/if}
          </div>
        </div>
      </div>
      <div class="online"><span class="dot"></span>Bridge online</div>
    </section>

    <section class="block" aria-labelledby="h-system">
      <h2 id="h-system">System</h2>
      <div class="grid tiles">
        <div class="card tile">
          <div class="tile-label"><Icon icon={Clock} />Uptime</div>
          <div class="tile-value">{status.uptime}</div>
          {#if sys.device_uptime}
          <div class="muted small">Device up {sys.device_uptime}</div>
          {/if}
        </div>

        {#if sys.flash}
        <div class="card tile">
          <div class="tile-label"><Icon icon={HardDrive} />Flash storage</div>
          <div class="tile-row">
            <span class="tile-value">{percent(sys.flash.used_bytes, sys.flash.total_bytes)} %</span>
            <span class="muted small">{Math.round(sys.flash.used_bytes / MB)} of {Math.round(sys.flash.total_bytes / MB)} MB</span>
          </div>
          <div class="bar" role="img" aria-label="Flash usage {percent(sys.flash.used_bytes, sys.flash.total_bytes)} percent">
            <div style="width: {percent(sys.flash.used_bytes, sys.flash.total_bytes)}%"></div>
          </div>
        </div>
        {/if}

        {#if sys.memory}
        <div class="card tile">
          <div class="tile-label"><Icon icon={MemoryStick} />Memory</div>
          <div class="tile-row">
            <span class="tile-value">{percent(sys.memory.used_bytes, sys.memory.total_bytes)} %</span>
            <span class="muted small">{Math.round(sys.memory.used_bytes / MB)} of {Math.round(sys.memory.total_bytes / MB)} MB</span>
          </div>
          <div class="bar" role="img" aria-label="Memory usage {percent(sys.memory.used_bytes, sys.memory.total_bytes)} percent">
            <div style="width: {percent(sys.memory.used_bytes, sys.memory.total_bytes)}%"></div>
          </div>
        </div>
        {/if}

        {#if sys.temperature_c !== undefined}
        <div class="card tile">
          <div class="tile-label"><Icon icon={Thermometer} />CPU temperature</div>
          <div class="tile-value">{sys.temperature_c.toFixed(1)} °C</div>
          <div class="muted small">SoC sensor</div>
        </div>
        {/if}

        {#if status.device?.answering_machine}
        <div class="card tile">
          <div class="tile-label"><Icon icon={Voicemail} />Message storage</div>
          <div class="tile-value">{status.storage_used}</div>
        </div>
        {/if}
      </div>
    </section>

    <section class="block" aria-labelledby="h-connections">
      <h2 id="h-connections">Connections</h2>
      <div class="grid tiles">
        {#each connections as c}
        <div class="card connection">
          <div class="connection-main">
            <span class="connection-icon"><Icon icon={c.icon} size={20} /></span>
            <div class="stack">
              <span class="connection-name">{c.name}</span>
              <span class="muted small">{c.detail}</span>
            </div>
          </div>
          <span class="pill" class:ok={c.ok} class:bad={!c.ok}>{c.state}</span>
        </div>
        {/each}
      </div>
    </section>

    {#if status.leds}
    <section class="block" aria-labelledby="h-leds">
      <div class="block-head">
        <h2 id="h-leds">LEDs</h2>
        <span class="muted small">Live via events</span>
      </div>
      <div class="grid leds">
        {#each Object.entries(status.leds) as [name, on]}
        <div class="led" class:on title={LED_INFO[name]?.desc || 'Function unknown'}>
          <div class="led-icon"><Icon icon={LED_ICON[name] || Circle} /></div>
          <div class="stack">
            <span class="led-name">{LED_INFO[name]?.label || name}</span>
            <span class="led-state">{on ? 'On' : 'Off'}</span>
          </div>
        </div>
        {/each}
      </div>
    </section>
    {/if}

    {#if status.gpio}
    <section class="card gpio-card" aria-labelledby="h-gpio">
      <details>
        <summary>
          <span class="gpio-title">
            <Icon icon={Activity} />
            <span id="h-gpio">GPIO</span>
            <span class="muted small">{Object.keys(status.gpio).length} pins · technical</span>
          </span>
          <span class="chevron"><Icon icon={ChevronDown} /></span>
        </summary>
        <div class="gpios">
          {#each Object.entries(status.gpio) as [pin, on]}
          <div class="gpio" title={gpioInfo(pin)?.desc || 'Function unknown'}>
            <span class="gpio-name">
              <span class="gpio-pin">{pin}</span>
              <span>{gpioInfo(pin)?.label || 'Unknown'}</span>
            </span>
            <span class="pill small-pill" class:ok={on}>{on ? 'High' : 'Low'}</span>
          </div>
          {/each}
        </div>
      </details>
    </section>
    {/if}
  {/if}
</div>

<style>
  .dashboard {
    max-width: 1200px;
    margin: 0 auto;
    padding: 24px 22px 40px;
    display: flex;
    flex-direction: column;
    gap: 26px;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }

  .muted { color: var(--text-muted); font-size: 14px; }
  .small { font-size: 13px; }
  .stack { display: flex; flex-direction: column; }

  h2 {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
    color: var(--text-label);
  }

  .block { display: flex; flex-direction: column; gap: 12px; }
  .block-head { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }

  .grid { display: grid; }
  .tiles { grid-template-columns: repeat(auto-fit, minmax(230px, 1fr)); gap: 14px; }
  .leds { grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: 10px; }

  /* Device */
  .device {
    border-radius: var(--radius-lg);
    padding: 20px 22px;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 20px;
  }
  .device-main { display: flex; align-items: center; gap: 18px; }
  .device-icon {
    width: 50px;
    height: 50px;
    flex-shrink: 0;
    border-radius: 13px;
    background: var(--accent-soft);
    color: var(--accent);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .device-text { display: flex; flex-direction: column; gap: 4px; }
  .eyebrow {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }
  .device-title { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; }
  .device-title h1 { margin: 0; font-size: 24px; font-weight: 700; letter-spacing: -0.02em; }
  .variant {
    font-size: 13px;
    font-weight: 600;
    color: var(--accent);
    background: var(--accent-soft);
    border-radius: 999px;
    padding: 4px 10px;
  }
  .online {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 14px;
    font-weight: 600;
    color: var(--ok);
    background: var(--ok-soft);
    border-radius: 999px;
    padding: 8px 14px;
  }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--ok-dot); }

  /* System tiles */
  .tile { padding: 17px 18px; display: flex; flex-direction: column; gap: 12px; }
  .tile-label {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--text-muted);
    font-size: 13px;
    font-weight: 500;
  }
  .tile-row { display: flex; align-items: baseline; gap: 8px; }
  .tile-value { font-size: 24px; font-weight: 700; letter-spacing: -0.02em; }
  .bar { height: 8px; border-radius: 999px; background: var(--chip); overflow: hidden; }
  .bar div { height: 8px; border-radius: 999px; background: var(--accent); }

  /* Connections */
  .connection {
    padding: 13px 18px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  .connection-main { display: flex; align-items: center; gap: 12px; }
  .connection-icon { color: var(--text-muted); display: flex; }
  .connection-name { font-size: 15px; font-weight: 600; }
  .pill {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-label);
    background: var(--chip);
    border-radius: 999px;
    padding: 4px 10px;
    white-space: nowrap;
  }
  .pill.ok { color: var(--ok); background: var(--ok-soft); }
  .pill.bad { color: var(--warn); background: var(--warn-icon-bg); }
  .small-pill { padding: 2px 8px; }

  /* LEDs */
  .led {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 11px 14px;
    display: flex;
    align-items: center;
    gap: 12px;
  }
  .led-icon {
    width: 34px;
    height: 34px;
    flex-shrink: 0;
    border-radius: 9px;
    background: var(--chip);
    color: var(--text-muted);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .led-name { font-size: 14px; font-weight: 600; }
  .led-state { font-size: 13px; color: var(--text-muted); }
  .led.on { background: var(--warn-soft); border-color: var(--warn-border); }
  .led.on .led-icon { background: var(--warn-icon-bg); color: var(--warn-icon); }
  .led.on .led-state { color: var(--warn); font-weight: 600; }

  /* GPIO */
  .gpio-card { margin-top: 13px; }
  summary {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    min-height: 44px;
    padding: 10px 18px;
    box-sizing: border-box;
    cursor: pointer;
    list-style: none;
    color: var(--text-muted);
  }
  summary::-webkit-details-marker { display: none; }
  .gpio-title { display: flex; align-items: center; gap: 10px; }
  #h-gpio { font-size: 15px; font-weight: 600; color: var(--text-label); }
  .chevron { display: flex; transition: transform 0.2s; }
  details[open] .chevron { transform: rotate(180deg); }
  .gpios {
    border-top: 1px solid var(--border);
    padding: 12px 18px 16px;
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: 4px 24px;
  }
  .gpio {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 6px 0;
    border-bottom: 1px solid var(--divider);
    font-size: 14px;
  }
  .gpio-name { display: flex; gap: 12px; align-items: baseline; }
  .gpio-pin { font-family: var(--font-mono); font-size: 13px; color: var(--text-muted); width: 34px; }

  .loading, .error {
    text-align: center;
    padding: 3rem;
    font-size: 1.2rem;
  }
  .error {
    color: #B91C1C;
    background: #FEF2F2;
    border-radius: var(--radius);
    border: 1px solid #FCA5A5;
  }
</style>
