<script>
  import { onMount } from 'svelte'
  import Dashboard from './routes/+page.svelte'
  import Settings from './routes/settings/+page.svelte'
  import Controls from './routes/controls/+page.svelte'
  import Messages from './routes/messages/+page.svelte'
  import Memos from './routes/memos/+page.svelte'
  import Logs from './routes/logs/+page.svelte'
  import Icon from './lib/Icon.svelte'
  import { Server, LayoutDashboard, Settings as SettingsIcon, SlidersHorizontal, Voicemail, StickyNote, ScrollText } from 'lucide'
  
  let currentRoute = 'dashboard'
  let status = null
  
  onMount(() => {
    window.addEventListener('hashchange', handleRouteChange)
    handleRouteChange()
    loadStatus()
    setInterval(loadStatus, 30000)
  })
  
  function handleRouteChange() {
    const hash = window.location.hash.slice(1) || 'dashboard'
    currentRoute = hash
  }
  
  async function loadStatus() {
    try {
      const response = await fetch('/api/status')
      if (response.ok) {
        status = await response.json()
      }
    } catch (e) {
      console.error('Failed to load status:', e)
    }
  }
</script>

<main class="app">
  <header class="navbar">
    <div class="navbar-inner">
      <div class="brand">
        <span class="brand-icon"><Icon icon={Server} size={20} /></span>
        <span class="brand-name">BTicino Bridge</span>
        {#if status?.version}
        <span class="version">{status.version}</span>
        {/if}
      </div>
      <nav class="nav-links" aria-label="Main">
        <a href="#dashboard" class:active={currentRoute === 'dashboard'}><Icon icon={LayoutDashboard} />Dashboard</a>
        <a href="#settings" class:active={currentRoute === 'settings'}><Icon icon={SettingsIcon} />Settings</a>
        <a href="#controls" class:active={currentRoute === 'controls'}><Icon icon={SlidersHorizontal} />Controls</a>
        {#if status?.device?.answering_machine}
        <a href="#messages" class:active={currentRoute === 'messages'}><Icon icon={Voicemail} />Messages</a>
        <a href="#memos" class:active={currentRoute === 'memos'}><Icon icon={StickyNote} />Notes</a>
        {/if}
        <a href="#logs" class:active={currentRoute === 'logs'}><Icon icon={ScrollText} />Logs</a>
      </nav>
    </div>
  </header>

  <div class="content">
    {#if currentRoute === 'dashboard'}
      <Dashboard />
    {:else if currentRoute === 'settings'}
      <Settings />
    {:else if currentRoute === 'controls'}
      <Controls />
    {:else if currentRoute === 'messages' && status?.device?.answering_machine}
      <Messages />
    {:else if currentRoute === 'memos' && status?.device?.answering_machine}
      <Memos />
    {:else if currentRoute === 'logs'}
      <Logs />
    {:else}
      <div class="not-found">
        <h2>404 - Page Not Found</h2>
        <a href="#dashboard">← Back to Dashboard</a>
      </div>
    {/if}
  </div>
</main>

<style>
  /* Design tokens. The dashboard uses them; other pages can follow. */
  :global(:root) {
    --bg: #F4F5F7;
    --surface: #FFFFFF;
    --border: #E3E6EB;
    --divider: #F0F1F4;
    --text: #14171C;
    --text-muted: #5B6370;
    --text-label: #4B5361;
    --chip: #EEF0F3;
    --accent: #0E7490;
    --accent-strong: #155E75;
    --accent-soft: #E6F4F7;
    --ok: #166534;
    --ok-soft: #DCFCE7;
    --ok-dot: #16A34A;
    --warn: #9A3412;
    --warn-icon: #C2410C;
    --warn-soft: #FFF7ED;
    --warn-icon-bg: #FFEDD5;
    --warn-border: #FDBA74;
    --radius-sm: 11px;
    --radius: 13px;
    --radius-lg: 15px;
    --font-mono: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  }

  :global(body) {
    margin: 0;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
    background: var(--bg);
    color: var(--text);
  }

  .app {
    min-height: 100vh;
  }

  .navbar {
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    position: sticky;
    top: 0;
    z-index: 100;
  }

  .navbar-inner {
    max-width: 1200px;
    margin: 0 auto;
    padding: 12px 22px;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .brand-icon {
    width: 36px;
    height: 36px;
    border-radius: 10px;
    background: var(--accent);
    color: #FFFFFF;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  .brand-name {
    font-size: 17px;
    font-weight: 700;
    letter-spacing: -0.01em;
  }

  .version {
    font-size: 12px;
    font-weight: 500;
    color: var(--text-label);
    background: var(--chip);
    border-radius: 999px;
    padding: 3px 10px;
  }

  .nav-links {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }

  .nav-links a {
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 40px;
    padding: 0 14px;
    border-radius: 10px;
    color: var(--text-label);
    font-size: 14px;
    font-weight: 500;
    text-decoration: none;
  }

  .nav-links a:hover {
    background: var(--chip);
  }

  .nav-links a.active {
    background: var(--accent-soft);
    color: var(--accent);
    font-weight: 600;
  }

  .not-found {
    text-align: center;
    padding: 4rem 2rem;
  }

  .not-found h2 {
    font-size: 2rem;
    margin-bottom: 1rem;
  }

  .not-found a {
    color: var(--accent);
    text-decoration: none;
    font-weight: 600;
  }

  .not-found a:hover {
    text-decoration: underline;
  }
</style>
