<script>
  import { onMount, onDestroy } from 'svelte'
  
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
    led_p1:        { label: 'Programmable Button 1',      desc: 'Programmable Button 1' },
    led_p2:        { label: 'Programmable Button 2',      desc: 'Programmable Button 2 — unconfirmed' },
    led_p3:        { label: 'Programmable Button 3',      desc: 'Programmable Button 3 — unconfirmed' },
    led_p4:        { label: 'Programmable Button 4',      desc: 'Programmable Button 4 — unconfirmed' },
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
</script>

<div class="dashboard">
  <div class="container">
    {#if loading}
      <div class="loading">Loading...</div>
    {:else if error}
      <div class="error">Error: {error}</div>
    {:else if status}
      <h2>System Status</h2>
      
      <div class="status-grid">
        <div class="status-card">
          <h3>📊 Version</h3>
          <p class="value">{status.version}</p>
        </div>
        
        <div class="status-card">
          <h3>⏱️ Uptime</h3>
          <p class="value">{status.uptime}</p>
        </div>
        
        <div class="status-card">
          <h3>💾 Storage</h3>
          <p class="value">{status.storage_used}</p>
        </div>
        
        {#if status.mqtt}
        <div class="status-card">
          <h3>📡 MQTT</h3>
          <p class="value">
            {#if status.mqtt.connected}
              ✅ {status.mqtt.broker}
            {:else}
              ❌ Disconnected
            {/if}
          </p>
        </div>
        {/if}
      </div>
      
      {#if status.components}
      <h3>Components</h3>
      <div class="components">
        {#each Object.entries(status.components) as [name, info]}
        <div class="component">
          <strong>{name}</strong>
          <span class="status">{info.status}</span>
        </div>
        {/each}
      </div>
      {/if}
      
      {#if status.leds}
      <h3>LEDs Status</h3>
      <div class="leds">
        {#each Object.entries(status.leds) as [name, on]}
        <div class="led {on ? 'on' : 'off'}" title={LED_INFO[name]?.desc || 'Function unknown'}>
          <span class="led-name">{LED_INFO[name]?.label || name}</span>
          <span class="led-status">{on ? '✅ On' : '❌ Off'}</span>
        </div>
        {/each}
      </div>
      {/if}
      
      {#if status.gpio}
      <h3>GPIO Status</h3>
      <div class="gpios">
        {#each Object.entries(status.gpio) as [pin, on]}
        <div class="gpio {on ? 'on' : 'off'}" title={gpioInfo(pin)?.desc || 'Function unknown'}>
          <span class="gpio-pin">GPIO {pin}</span>
          {#if gpioInfo(pin)}
          <span class="gpio-label">{gpioInfo(pin).label}</span>
          {/if}
          <span class="gpio-status">{on ? '✅ High' : '❌ Low'}</span>
        </div>
        {/each}
      </div>
      {/if}
    {/if}
  </div>
</div>

<style>
  .dashboard { padding: 2rem; }
  .container { max-width: 1200px; margin: 0 auto; }
  h2 { color: #333; margin-bottom: 1.5rem; }
  h3 { color: #666; margin-top: 2rem; margin-bottom: 1rem; }
  
  .status-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
    gap: 1.5rem;
    margin-bottom: 2rem;
  }
  
  .status-card {
    background: white;
    padding: 1.5rem;
    border-radius: 12px;
    box-shadow: 0 4px 6px rgba(0,0,0,0.1);
    transition: transform 0.3s, box-shadow 0.3s;
  }
  
  .status-card:hover {
    transform: translateY(-4px);
    box-shadow: 0 8px 12px rgba(0,0,0,0.15);
  }
  
  .status-card h3 {
    margin: 0 0 1rem 0;
    color: #666;
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }
  
  .value {
    font-size: 1.5rem;
    font-weight: bold;
    color: #2196F3;
    margin: 0;
  }
  
  .components {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
    gap: 1rem;
  }
  
  .component {
    background: white;
    padding: 1rem;
    border-radius: 8px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    box-shadow: 0 2px 4px rgba(0,0,0,0.1);
  }
  
  .status {
    color: #4CAF50;
    font-weight: bold;
    padding: 0.25rem 0.75rem;
    background: #E8F5E9;
    border-radius: 12px;
    font-size: 0.85rem;
  }
  
  .leds {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
    gap: 1rem;
  }
  
  .led {
    background: white;
    padding: 1rem;
    border-radius: 8px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    box-shadow: 0 2px 4px rgba(0,0,0,0.1);
  }
  
  .led.on .led-status { color: #4CAF50; }
  .led.off .led-status { color: #999; }
  .led-name { color: #666; font-size: 0.9rem; }
  
  .gpios {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: 0.75rem;
  }
  
  .gpio {
    background: white;
    padding: 0.75rem;
    border-radius: 6px;
    display: flex;
    justify-content: space-between;
    align-items: center;
    box-shadow: 0 2px 4px rgba(0,0,0,0.1);
  }
  
  .gpio.on .gpio-status { color: #4CAF50; }
  .gpio.off .gpio-status { color: #999; }
  .gpio-pin { color: #666; font-size: 0.85rem; }
  .gpio-label { color: #2196F3; font-size: 0.8rem; font-weight: 600; display: block; }
  
  .loading, .error {
    text-align: center;
    padding: 3rem;
    font-size: 1.2rem;
  }
  
  .error {
    color: #f44336;
    background: #FFEBEE;
    border-radius: 8px;
    border: 1px solid #f44336;
  }
</style>
