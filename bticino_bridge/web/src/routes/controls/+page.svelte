<script>
  import { onMount } from 'svelte'
  import Icon from '../../lib/Icon.svelte'
  import { Lock, LockOpen, Lightbulb, Bell, Monitor, Voicemail, RotateCcw, Power, RefreshCw, CircleCheck } from 'lucide'
  
  let controls = {
    door: { locked: true, loading: false },
    voicemail: { enabled: false, loading: false },
    display: { on: true, loading: false },
    doorbell_sound: { on: true, loading: false },
    light: { on: false, loading: false }
  }
  
  let loading = true
  let error = null
  let success = null
  // From /api/status: which controls the unit supports (Classe 100X ignores
  // display and doorbell-sound SETs and has no answering machine).
  let device = {}
  
  let system = {
    restart: { loading: false },
    reboot: { loading: false }
  }

  onMount(async () => {
    await loadControls()
  })
  
  async function loadControls() {
    try {
      loading = true
      
      const [deviceRes, statusRes] = await Promise.all([
        fetch('/api/config/device'),
        fetch('/api/status')
      ])
      
      const deviceData = await deviceRes.json()
      const statusData = await statusRes.json()
      device = statusData.device || {}
      
      if (deviceData.success && deviceData.config) {
        const answering = deviceData.config.answering || {}
        controls.voicemail.enabled = answering.activated === true || answering.activated === 1
      }
      
      if (statusData.leds) {
        // Mute indicator: Classe 100X exposes led_call_exp (ON = muted),
        // Classe 300X guesses led_ans_machine. Prefer the 100X name.
        const muted = statusData.leds.led_call_exp === true ||
          (statusData.leds.led_call_exp === undefined && statusData.leds.led_ans_machine === false)
        controls.doorbell_sound.on = !muted
      }
      
      error = null
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }
  
  async function restartBridge() {
    // Double confirmation: the bridge is out of service for a few seconds.
    if (!confirm('Restart the bridge service?\n\nWeb, MQTT and HomeKit will drop for a few seconds.')) return
    if (!confirm('Are you sure? This restarts the bridge NOW.')) return
    try {
      system.restart.loading = true
      const response = await fetch('/api/system/restart-bridge', { method: 'POST' })
      const result = await response.json()
      if (result.success) {
        error = null
        success = 'Bridge restarting… this page will reconnect in about 10 s'
        setTimeout(() => location.reload(), 12000)
      } else {
        error = result.message || 'Failed'
      }
    } catch (e) {
      error = e.message
    } finally {
      system.restart.loading = false
    }
  }

  async function rebootDevice() {
    // Double confirmation: reboots the WHOLE intercom (native functions included).
    if (!confirm('⚠️ REBOOT THE WHOLE INTERCOM?\n\nThe device (including the native doorbell functions) will be offline for 1-2 minutes.')) return
    if (!confirm('Last chance: really reboot the device NOW?')) return
    try {
      system.reboot.loading = true
      const response = await fetch('/api/system/reboot', { method: 'POST' })
      const result = await response.json()
      if (result.success) {
        error = null
        success = 'Device rebooting — back in 1–2 minutes'
      } else {
        error = result.message || 'Failed'
      }
    } catch (e) {
      error = e.message
    } finally {
      system.reboot.loading = false
    }
  }

  async function toggleVoicemail() {
    const action = controls.voicemail.enabled ? 'disable' : 'enable'
    try {
      controls.voicemail.loading = true
      const response = await fetch(`/api/controls/answering-machine/toggle?action=${action}`, { method: 'POST' })
      const result = await response.json()
      
      if (result.success) {
        error = null
        controls.voicemail.enabled = !controls.voicemail.enabled
        success = `Voicemail ${action === 'enable' ? 'enabled' : 'disabled'}`
        setTimeout(() => success = null, 3000)
      } else {
        error = result.message || 'Failed'
      }
    } catch (e) {
      error = e.message
    } finally {
      controls.voicemail.loading = false
    }
  }
  
  async function toggleDoorbellSound() {
    const action = controls.doorbell_sound.on ? 'off' : 'on'
    try {
      controls.doorbell_sound.loading = true
      const response = await fetch(`/api/controls/doorbell/${action}`, { method: 'POST' })
      const result = await response.json()
      
      if (result.success) {
        error = null
        controls.doorbell_sound.on = !controls.doorbell_sound.on
        success = `Doorbell sound ${action === 'on' ? 'enabled' : 'disabled'}`
        setTimeout(() => success = null, 3000)
      } else {
        error = result.message || 'Failed'
      }
    } catch (e) {
      error = e.message
    } finally {
      controls.doorbell_sound.loading = false
    }
  }
  
  async function toggleDisplay() {
    const action = controls.display.on ? 'off' : 'on'
    try {
      controls.display.loading = true
      const response = await fetch(`/api/controls/display/${action}`, { method: 'POST' })
      const result = await response.json()
      
      if (result.success) {
        error = null
        controls.display.on = !controls.display.on
        success = `Display ${action === 'on' ? 'turned on' : 'turned off'}`
        setTimeout(() => success = null, 3000)
      } else {
        error = result.message || 'Failed'
      }
    } catch (e) {
      error = e.message
    } finally {
      controls.display.loading = false
    }
  }
  
  async function unlockDoor() {
    try {
      controls.door.loading = true
      const response = await fetch('/api/controls/door/unlock', { method: 'POST' })
      const result = await response.json()
      
      if (result.success) {
        error = null
        success = 'Door unlocked'
        setTimeout(() => success = null, 3000)
      } else {
        error = result.message || 'Failed'
      }
    } catch (e) {
      error = e.message
    } finally {
      controls.door.loading = false
    }
  }
  
  async function toggleLight() {
    try {
      controls.light.loading = true
      const response = await fetch('/api/controls/light/on', { method: 'POST' })
      const result = await response.json()
      
      if (result.success) {
        error = null
        success = 'Staircase light activated'
        setTimeout(() => success = null, 3000)
      } else {
        error = result.message || 'Failed'
      }
    } catch (e) {
      error = e.message
    } finally {
      controls.light.loading = false
    }
  }
</script>

<div class="page">
  <div class="page-head">
    <div class="stack gap4">
      <h1>Controls</h1>
      <span class="muted">Commands go to the intercom over OpenWebNet.</span>
    </div>
    <button type="button" class="btn" on:click={loadControls}><Icon icon={RefreshCw} size={16} />Refresh</button>
  </div>

  {#if success}
  <div class="banner ok" role="status"><Icon icon={CircleCheck} />{success}</div>
  {/if}
  {#if error}
  <div class="banner bad" role="alert">Error: {error}</div>
  {/if}

  {#if loading}
    <div class="loading">Loading controls...</div>
  {:else}
    <section class="block" aria-labelledby="h-quick">
      <h2 id="h-quick">Quick controls</h2>
      <div class="grid">
        <div class="card control">
          <div class="control-head">
            <div class="control-icon accent"><Icon icon={Lock} size={20} /></div>
            <div class="stack"><span class="control-name">Door lock</span><span class="muted small">Locked</span></div>
          </div>
          <button type="button" class="btn primary" on:click={unlockDoor} disabled={controls.door.loading}>
            <Icon icon={LockOpen} size={16} />{controls.door.loading ? 'Opening…' : 'Unlock door'}
          </button>
        </div>

        <div class="card control">
          <div class="control-head">
            <div class="control-icon"><Icon icon={Lightbulb} size={20} /></div>
            <div class="stack"><span class="control-name">Staircase light</span><span class="muted small">{controls.light.on ? 'On' : 'Off'}</span></div>
          </div>
          <button type="button" class="btn primary" on:click={toggleLight} disabled={controls.light.loading}>
            {controls.light.loading ? 'Switching…' : 'Turn on'}
          </button>
        </div>

        {#if device.remote_set_commands}
        <div class="card control">
          <div class="control-head">
            <div class="control-icon"><Icon icon={Bell} size={20} /></div>
            <div class="stack"><span class="control-name">Doorbell sound</span><span class="muted small">{controls.doorbell_sound.on ? 'Enabled' : 'Disabled'}</span></div>
          </div>
          <button type="button" class="btn" on:click={toggleDoorbellSound} disabled={controls.doorbell_sound.loading}>
            {controls.doorbell_sound.loading ? '…' : (controls.doorbell_sound.on ? 'Disable' : 'Enable')}
          </button>
        </div>

        <div class="card control">
          <div class="control-head">
            <div class="control-icon"><Icon icon={Monitor} size={20} /></div>
            <div class="stack"><span class="control-name">Display</span><span class="muted small">{controls.display.on ? 'On' : 'Off'}</span></div>
          </div>
          <button type="button" class="btn" on:click={toggleDisplay} disabled={controls.display.loading}>
            {controls.display.loading ? '…' : (controls.display.on ? 'Turn off' : 'Turn on')}
          </button>
        </div>
        {/if}

        {#if device.answering_machine}
        <div class="card control">
          <div class="control-head">
            <div class="control-icon"><Icon icon={Voicemail} size={20} /></div>
            <div class="stack"><span class="control-name">Voicemail</span><span class="muted small">{controls.voicemail.enabled ? 'Enabled' : 'Disabled'}</span></div>
          </div>
          <button type="button" class="btn" on:click={toggleVoicemail} disabled={controls.voicemail.loading}>
            {controls.voicemail.loading ? '…' : (controls.voicemail.enabled ? 'Disable' : 'Enable')}
          </button>
        </div>
        {/if}
      </div>
    </section>

    <section class="block" aria-labelledby="h-system">
      <h2 id="h-system">System</h2>
      <div class="grid wide">
        <div class="card danger">
          <div class="control-head">
            <div class="control-icon red"><Icon icon={RotateCcw} size={20} /></div>
            <div class="stack"><span class="control-name">Restart bridge</span><span class="muted small">Web, MQTT and HomeKit drop for about 5 s</span></div>
          </div>
          <button type="button" class="btn danger-outline" on:click={restartBridge} disabled={system.restart.loading}>
            {system.restart.loading ? '…' : 'Restart'}
          </button>
        </div>

        <div class="card danger">
          <div class="control-head">
            <div class="control-icon red"><Icon icon={Power} size={20} /></div>
            <div class="stack"><span class="control-name">Reboot device</span><span class="muted small">Whole intercom offline for 1–2 min</span></div>
          </div>
          <button type="button" class="btn danger-solid" on:click={rebootDevice} disabled={system.reboot.loading}>
            {system.reboot.loading ? '…' : 'Reboot'}
          </button>
        </div>
      </div>
    </section>
  {/if}
</div>

<style>
  .page {
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

  .stack { display: flex; flex-direction: column; }
  .gap4 { gap: 4px; }
  .muted { color: var(--text-muted); font-size: 14px; }
  .small { font-size: 13px; }

  .page-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  h1 { margin: 0; font-size: 24px; font-weight: 700; letter-spacing: -0.02em; }
  h2 { margin: 0; font-size: 15px; font-weight: 600; color: var(--text-label); }

  .block { display: flex; flex-direction: column; gap: 12px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 14px; }
  .grid.wide { grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); }

  .control { padding: 18px; display: flex; flex-direction: column; gap: 14px; }
  .danger {
    border-color: #FECACA;
    padding: 18px;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 14px;
  }
  .control-head { display: flex; align-items: center; gap: 12px; }
  .control-name { font-size: 15px; font-weight: 600; }
  .control-icon {
    width: 40px;
    height: 40px;
    flex-shrink: 0;
    border-radius: 11px;
    background: var(--chip);
    color: var(--text-muted);
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .control-icon.accent { background: var(--accent-soft); color: var(--accent); }
  .control-icon.red { background: #FEF2F2; color: #B91C1C; }

  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 42px;
    padding: 0 16px;
    border-radius: 10px;
    border: 1px solid #D5D9E0;
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: 14px;
    font-weight: 500;
    cursor: pointer;
  }
  .btn:hover { background: var(--chip); }
  .btn.primary { background: var(--accent); border-color: var(--accent); color: #FFFFFF; font-weight: 600; }
  .btn.primary:hover { filter: brightness(1.1); }
  .btn.danger-outline { border-color: #B91C1C; color: #B91C1C; font-weight: 600; }
  .btn.danger-outline:hover { background: #FEF2F2; }
  .btn.danger-solid { background: #B91C1C; border-color: #B91C1C; color: #FFFFFF; font-weight: 600; }
  .btn.danger-solid:hover { filter: brightness(1.1); }
  .btn:disabled { opacity: 0.6; cursor: not-allowed; }

  .banner {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px 16px;
    border-radius: 11px;
    font-size: 14px;
    font-weight: 600;
  }
  .banner.ok { background: var(--ok-soft); color: var(--ok); }
  .banner.bad { background: #FEF2F2; color: #B91C1C; }

  .loading { text-align: center; padding: 3rem; font-size: 1.2rem; }
</style>
