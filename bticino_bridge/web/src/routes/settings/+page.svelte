<script>
  import { onMount } from 'svelte'
  import Icon from '../../lib/Icon.svelte'
  import { ScrollText, Phone, Radio, House, Video, Shield, Network, RefreshCw, Save, Search, CircleCheck, TriangleExclamationPoint } from 'lucide'
  
  let config = null
  let loading = true
  let saving = false
  let error = null
  let success = null
  
  const tabs = [
    { id: 'bridge', label: 'Bridge', icon: ScrollText, desc: 'Logging of the bridge.' },
    { id: 'sip', label: 'SIP', icon: Phone, desc: 'Connection to the local Flexisip server of the intercom.' },
    { id: 'mqtt', label: 'MQTT', icon: Radio, desc: 'Broker connection for Home Assistant.' },
    { id: 'homekit', label: 'HomeKit', icon: House, desc: 'HomeKit bridge accessory.' },
    { id: 'streaming', label: 'Streaming', icon: Video, desc: 'RTSP server and video activation.' },
    { id: 'security', label: 'Security', icon: Shield, desc: 'Access to this dashboard and the API.' },
    { id: 'openwebnet', label: 'OpenWebNet', icon: Network, desc: "Connection to the intercom's OpenWebNet server." }
  ]
  
  let activeTab = 'bridge'
  $: tab = tabs.find(t => t.id === activeTab)
  
  onMount(async () => {
    await loadConfig()
  })
  
  async function loadConfig() {
    try {
      loading = true
      const response = await fetch('/api/config')
      if (!response.ok) throw new Error('Failed to load config')
      config = await response.json()
      error = null
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }
  
  async function saveConfig() {
    try {
      saving = true
      const response = await fetch('/api/config/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config: config.config, user: 'admin' })
      })
      const result = await response.json()
      if (result.success) {
        success = 'Configuration saved. Restart required.'
        error = null
        setTimeout(() => success = null, 5000)
      } else {
        error = result.error || 'Failed to save'
        if (result.errors) error += '\n' + result.errors.join('\n')
      }
    } catch (e) {
      error = e.message
    } finally {
      saving = false
    }
  }
  
  // native dev addr discovery wizard state
  let showWizard = false
  let wizardStep = 1
  let wizardReady = false
  let wizardBusy = false
  let wizardError = null
  let discoverValue = ''
  let verifyResult = null
  let pollTimer = null

  function openWizard() {
    showWizard = true
    wizardStep = 1
    wizardReady = false
    wizardBusy = false
    wizardError = null
    discoverValue = ''
    verifyResult = null
  }

  function closeWizard() {
    cancelDiscover()
    showWizard = false
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
  }

  async function startDiscover() {
    try {
      wizardBusy = true
      wizardError = null
      const response = await fetch('/api/sip/native-dev-addr/discover?confirm=yes', { method: 'POST' })
      const result = await response.json()
      if (result.state === 'busy') throw new Error(result.message || 'session running')
      if (result.state !== 'waiting') throw new Error(result.error || 'discover rejected')
      wizardStep = 2
      pollTimer = setInterval(pollDiscover, 2000)
    } catch (e) {
      wizardError = e.message
    } finally {
      wizardBusy = false
    }
  }

  async function pollDiscover() {
    try {
      const response = await fetch('/api/sip/native-dev-addr/discover/status')
      const result = await response.json()
      if (result.state === 'found' && result.value) {
        clearInterval(pollTimer); pollTimer = null
        discoverValue = result.value
        wizardStep = 3
        await verifyValue()
      } else if (result.state === 'idle') {
        clearInterval(pollTimer); pollTimer = null
        wizardError = 'No app call seen within 30s — try again while starting a camera view.'
        wizardStep = 1
      }
    } catch (e) {
      wizardError = e.message
    }
  }

  async function cancelDiscover() {
    try {
      await fetch('/api/sip/native-dev-addr/discover/cancel', { method: 'POST' })
    } catch (e) { /* best effort */ }
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null }
  }

  async function verifyValue() {
    try {
      wizardBusy = true
      const response = await fetch('/api/sip/native-dev-addr/verify?confirm=yes', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ value: discoverValue })
      })
      verifyResult = await response.json()
    } catch (e) {
      verifyResult = { valid: false, error: e.message }
    } finally {
      wizardBusy = false
    }
  }

  async function applyValue() {
    config.config.SIP.NativeDevAddr = discoverValue
    await saveConfig()
    closeWizard()
  }

  async function discoverDomain() {
    try {
      error = null
      const response = await fetch('/api/sip/domain/discover')
      const result = await response.json()
      if (!result.value) throw new Error(result.error || 'no domain found')
      config.config.SIP.Domain = result.value
      success = 'Domain filled from ' + (result.source || 'Flexisip') + ' — press Save.'
      setTimeout(() => success = null, 5000)
    } catch (e) {
      error = e.message
    }
  }
</script>

<div class="page">
  <div class="page-head">
    <div class="stack">
      <h1>Settings</h1>
      <span class="muted">Saved to config.yaml. Log settings apply after a bridge restart.</span>
    </div>
    {#if config}
    <div class="actions">
      <button type="button" class="btn" on:click={loadConfig}><Icon icon={RefreshCw} size={16} />Reload</button>
      <button type="button" class="btn primary" on:click={saveConfig} disabled={saving}>
        <Icon icon={Save} size={16} />{saving ? 'Saving…' : 'Save'}
      </button>
    </div>
    {/if}
  </div>

  {#if success}
  <div class="banner ok" role="status"><Icon icon={CircleCheck} />{success}</div>
  {/if}

  {#if loading}
    <div class="loading">Loading configuration...</div>
  {:else if error && !config}
    <div class="banner bad">Error: {error}</div>
  {:else if config}
    {#if error}
    <div class="banner bad" role="alert">{error}</div>
    {/if}

    <div class="layout">
      <nav class="card side" aria-label="Settings sections">
        {#each tabs as t}
        <button type="button" class:active={activeTab === t.id} aria-current={activeTab === t.id ? 'true' : undefined}
          on:click={() => activeTab = t.id}>
          <Icon icon={t.icon} />{t.label}
        </button>
        {/each}
      </nav>

      <section class="card section" aria-labelledby="section-title">
        <div class="stack gap4">
          <h2 id="section-title">{tab.label}</h2>
          <span class="muted">{tab.desc}</span>
        </div>

        {#if activeTab === 'bridge'}
        <div class="fields">
          <label class="field">Log level
            <select bind:value={config.config.Logging.Level}>
              <option value="debug">Debug</option>
              <option value="info">Info</option>
              <option value="warn">Warning</option>
              <option value="error">Error</option>
            </select>
          </label>
          <label class="field">Log file
            <input type="text" bind:value={config.config.Logging.File} />
          </label>
          <label class="field">Max log size
            <input type="text" bind:value={config.config.Logging.MaxSize} />
            <small>Max 48 MB across all log files, at most 16 files. Log file must be in /var/log or /tmp.</small>
          </label>
        </div>
        {/if}

        {#if activeTab === 'sip'}
        <label class="toggle">
          <span class="stack"><span class="toggle-title">SIP enabled</span></span>
          <input type="checkbox" bind:checked={config.config.SIP.Enabled} />
        </label>
        <div class="fields">
          <label class="field">Server host
            <input type="text" bind:value={config.config.SIP.ServerHost} />
          </label>
          <label class="field">Server port
            <input type="number" bind:value={config.config.SIP.ServerPort} />
          </label>
          <label class="field">Transport
            <select bind:value={config.config.SIP.Transport}>
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
              <option value="tls">TLS</option>
            </select>
          </label>
          <div class="field">
            <label for="sip_domain">Domain</label>
            <div class="input-row">
              <input type="text" id="sip_domain" bind:value={config.config.SIP.Domain} />
              <button type="button" class="icon-btn" aria-label="Discover domain from Flexisip" title="Discover domain from Flexisip" on:click={discoverDomain}><Icon icon={Search} size={16} /></button>
            </div>
          </div>
          <label class="field">Username
            <input type="text" bind:value={config.config.SIP.Username} />
          </label>
          <label class="field">Password
            <input type="password" bind:value={config.config.SIP.Password} />
          </label>
          <div class="field">
            <label for="sip_native_dev_addr">Native dev addr</label>
            <div class="input-row">
              <input type="text" id="sip_native_dev_addr" bind:value={config.config.SIP.NativeDevAddr} placeholder="auto (validated default)" />
              <button type="button" class="icon-btn" aria-label="Discover native dev addr" title="Discover native dev addr" on:click={openWizard}><Icon icon={Search} size={16} /></button>
            </div>
          </div>
        </div>
        <label class="toggle">
          <span class="stack">
            <span class="toggle-title">Insecure TLS</span>
            <span class="muted small">Skips certificate checks.</span>
          </span>
          <input type="checkbox" bind:checked={config.config.SIP.InsecureTLS} />
        </label>
        {/if}

        {#if activeTab === 'mqtt'}
        <label class="toggle">
          <span class="stack"><span class="toggle-title">MQTT enabled</span></span>
          <input type="checkbox" bind:checked={config.config.MQTT.Enabled} />
        </label>
        <div class="fields">
          <label class="field">Broker host
            <input type="text" bind:value={config.config.MQTT.Host} />
          </label>
          <label class="field">Broker port
            <input type="number" bind:value={config.config.MQTT.Port} />
          </label>
          <label class="field">Username
            <input type="text" bind:value={config.config.MQTT.Username} />
          </label>
          <label class="field">Password
            <input type="password" bind:value={config.config.MQTT.Password} />
          </label>
          <label class="field">Topic prefix
            <input type="text" bind:value={config.config.MQTT.TopicPrefix} />
          </label>
        </div>
        {/if}

        {#if activeTab === 'homekit'}
        <label class="toggle">
          <span class="stack"><span class="toggle-title">HomeKit enabled</span></span>
          <input type="checkbox" bind:checked={config.config.HomeKit.Enabled} />
        </label>
        <div class="fields">
          <label class="field">Bridge name
            <input type="text" bind:value={config.config.HomeKit.Name} />
          </label>
          <label class="field">Port
            <!-- text on purpose: HomeKit.Port is a string in the backend; a
                 type="number" would send a number and break saving -->
            <input type="text" inputmode="numeric" bind:value={config.config.HomeKit.Port} />
          </label>
          <label class="field">PIN
            <input type="text" bind:value={config.config.HomeKit.Pin} />
          </label>
          <label class="field">Model
            <input type="text" bind:value={config.config.HomeKit.Model} />
          </label>
          <label class="field">Manufacturer
            <input type="text" bind:value={config.config.HomeKit.Manufacturer} />
          </label>
        </div>
        {/if}

        {#if activeTab === 'streaming'}
        <div class="fields">
          <label class="field">RTSP port
            <input type="number" bind:value={config.config.Streaming.RTSPPort} />
          </label>
          <label class="field">Recording path
            <input type="text" bind:value={config.config.Streaming.RecordingPath} />
          </label>
        </div>
        <h3>Video activation (safety)</h3>
        <label class="toggle">
          <span class="stack">
            <span class="toggle-title">Live video on call</span>
            <span class="muted small">Passive mirror of the native video during an incoming call. Sends no commands to the device bus.</span>
          </span>
          <input type="checkbox" bind:checked={config.config.Streaming.LiveOnCall} />
        </label>
        <label class="toggle warn">
          <span class="stack">
            <span class="toggle-title"><Icon icon={TriangleExclamationPoint} size={16} />Video on demand</span>
            <span class="muted small">Self-INVITE / on-demand activation competes with the native firmware and may reboot the device. Enable only on a validated setup.</span>
          </span>
          <input type="checkbox" bind:checked={config.config.Streaming.VideoOnDemand} />
        </label>
        <label class="toggle">
          <span class="stack">
            <span class="toggle-title">Camera session</span>
            <span class="muted small">Bridge-initiated headless video (loopback self-call answered natively, display stays off) as an HA switch. Zero bus commands.</span>
          </span>
          <input type="checkbox" bind:checked={config.config.Streaming.CameraSession} />
        </label>
        <div class="fields">
          <label class="field">Camera session duration (seconds)
            <input type="number" bind:value={config.config.Streaming.CameraSessionDuration} />
          </label>
          <label class="field">Video backend
            <select bind:value={config.config.Streaming.VideoBackend}>
              <option value="avmedia">avmedia (cooperative, recommended)</option>
              <option value="gstreamer">gstreamer (competes with native)</option>
              <option value="auto">auto</option>
            </select>
          </label>
        </div>
        {/if}

        {#if activeTab === 'security'}
        <label class="toggle">
          <span class="stack">
            <span class="toggle-title">Web auth required</span>
            <span class="muted small">Protects the whole dashboard and API with HTTP Basic Auth. Requires a password below.</span>
          </span>
          <input type="checkbox" bind:checked={config.config.Security.WebAuthRequired} />
        </label>
        <div class="fields">
          <label class="field">Web username
            <input type="text" bind:value={config.config.Security.WebUsername} />
          </label>
          <label class="field">Web password
            <input type="password" bind:value={config.config.Security.WebPassword} />
          </label>
        </div>
        {/if}

        {#if activeTab === 'openwebnet'}
        <div class="fields">
          <label class="field">OpenWebNet host
            <input type="text" bind:value={config.config.OpenWebNet.Host} />
          </label>
          <label class="field">Timeout (ns)
            <input type="number" bind:value={config.config.OpenWebNet.Timeout} />
          </label>
          <label class="field">Retry attempts
            <input type="number" bind:value={config.config.OpenWebNet.RetryAttempts} />
          </label>
          <label class="field">Retry delay (ns)
            <input type="number" bind:value={config.config.OpenWebNet.RetryDelay} />
          </label>
        </div>
        {/if}
      </section>
    </div>
  {/if}

  {#if showWizard}
  <div class="modal-overlay">
    <div class="modal card" role="dialog" aria-modal="true" aria-labelledby="wizard-title">
      <h2 id="wizard-title"><Icon icon={Search} />Discover unit address</h2>
      {#if wizardStep === 1}
        <p>The bridge listens passively for your next app call and reads its unit address. Nothing is sent until verification.</p>
        <p><strong>Requirements</strong></p>
        <ul>
          <li>Door Entry app connected with your device</li>
          <li>Camera session working from the app</li>
        </ul>
        <label class="check">
          <input type="checkbox" bind:checked={wizardReady} />
          I have the app open, ready to start a camera view
        </label>
        {#if wizardError}<div class="banner bad">{wizardError}</div>{/if}
        <div class="modal-actions">
          <button type="button" class="btn" on:click={closeWizard}>Cancel</button>
          <button type="button" class="btn primary" on:click={startDiscover} disabled={!wizardReady || wizardBusy}>
            {wizardBusy ? 'Arming…' : 'Start discovery'}
          </button>
        </div>
      {:else if wizardStep === 2}
        <p>Listening… <strong>start a camera view in your app now</strong> (30 s window).</p>
        {#if wizardError}<div class="banner bad">{wizardError}</div>{/if}
        <div class="modal-actions">
          <button type="button" class="btn" on:click={closeWizard}>Cancel</button>
        </div>
      {:else}
        <p>Found unit address:</p>
        <code>{discoverValue}</code>
        {#if verifyResult && verifyResult.valid}
          <div class="banner ok"><Icon icon={CircleCheck} />Verified — the unit answers this address</div>
        {:else if verifyResult && verifyResult.busy}
          <div class="banner warn">Unit busy — retry when no session is running</div>
        {:else if verifyResult}
          <div class="banner warn">Unverified ({verifyResult.error || 'rejected'}) — you can still apply it manually</div>
        {/if}
        <div class="modal-actions">
          <button type="button" class="btn" on:click={closeWizard}>Keep current</button>
          <button type="button" class="btn primary" on:click={applyValue} disabled={saving}>Use this value</button>
        </div>
      {/if}
    </div>
  </div>
  {/if}
</div>

<style>
  .page {
    max-width: 1200px;
    margin: 0 auto;
    padding: 24px 22px 40px;
    display: flex;
    flex-direction: column;
    gap: 20px;
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
  .page-head .stack { gap: 4px; }
  h1 { margin: 0; font-size: 24px; font-weight: 700; letter-spacing: -0.02em; }
  .actions { display: flex; gap: 8px; }

  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    min-height: 40px;
    padding: 0 16px;
    border-radius: 10px;
    border: 1px solid var(--border);
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
  .btn:disabled { opacity: 0.6; cursor: not-allowed; }

  .banner {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px 16px;
    border-radius: 11px;
    font-size: 14px;
    font-weight: 600;
    white-space: pre-line;
  }
  .banner.ok { background: var(--ok-soft); color: var(--ok); }
  .banner.bad { background: #FEF2F2; color: #B91C1C; }
  .banner.warn { background: var(--warn-soft); color: var(--warn); }

  .layout { display: flex; flex-wrap: wrap; gap: 20px; align-items: flex-start; }

  .side {
    flex: 1 1 220px;
    max-width: 260px;
    padding: 8px;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .side button {
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 40px;
    padding: 0 12px;
    border: 0;
    border-radius: 9px;
    background: transparent;
    color: var(--text-label);
    font: inherit;
    font-size: 14px;
    font-weight: 500;
    text-align: left;
    cursor: pointer;
  }
  .side button:hover { background: var(--chip); }
  .side button.active { background: var(--accent-soft); color: var(--accent); font-weight: 600; }

  .section {
    flex: 999 1 560px;
    min-width: 0;
    padding: 20px 22px 24px;
    display: flex;
    flex-direction: column;
    gap: 20px;
  }
  h2 { margin: 0; font-size: 18px; font-weight: 700; }
  h3 { margin: 4px 0 -8px; font-size: 15px; font-weight: 600; color: var(--text-label); }

  .fields {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
    gap: 16px 18px;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-size: 13px;
    font-weight: 600;
    color: var(--text-label);
  }
  .field small { font-weight: 400; color: var(--text-muted); }
  input[type="text"], input[type="number"], input[type="password"], select {
    min-height: 40px;
    padding: 0 12px;
    border: 1px solid #D5D9E0;
    border-radius: 9px;
    font: inherit;
    font-size: 14px;
    font-weight: 400;
    color: var(--text);
    background: var(--surface);
    box-sizing: border-box;
  }
  input:focus, select:focus { outline: 2px solid var(--accent); outline-offset: -1px; }
  .input-row { display: flex; gap: 8px; }
  .input-row input { flex: 1; min-width: 0; }
  .icon-btn {
    width: 40px;
    height: 40px;
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    border: 1px solid var(--border);
    border-radius: 9px;
    background: var(--surface);
    color: var(--text-label);
    cursor: pointer;
  }
  .icon-btn:hover { background: var(--chip); }

  .toggle {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 12px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    cursor: pointer;
  }
  .toggle .stack { gap: 2px; }
  .toggle-title { display: flex; align-items: center; gap: 6px; font-size: 14px; font-weight: 600; }
  .toggle.warn { border-color: var(--warn-border); background: var(--warn-soft); }
  .toggle.warn .toggle-title { color: var(--warn); }
  input[type="checkbox"] { width: 20px; height: 20px; flex-shrink: 0; accent-color: var(--accent); }

  .loading { text-align: center; padding: 3rem; font-size: 1.2rem; }

  .modal-overlay {
    position: fixed;
    inset: 0;
    background: rgba(20, 23, 28, 0.45);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
    z-index: 200;
  }
  .modal {
    max-width: 520px;
    width: 100%;
    padding: 22px;
    box-shadow: 0 20px 40px rgba(0, 0, 0, 0.18);
    display: flex;
    flex-direction: column;
    gap: 10px;
    font-size: 14px;
  }
  .modal h2 { display: flex; align-items: center; gap: 10px; }
  .modal p, .modal ul { margin: 0; }
  .modal code {
    font-family: var(--font-mono);
    background: var(--chip);
    padding: 8px 12px;
    border-radius: 8px;
    word-break: break-all;
  }
  .check { display: flex; align-items: center; gap: 10px; }
  .modal-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 8px; }
</style>
