<script>
  import { onMount, onDestroy } from 'svelte'
  import Icon from '../../lib/Icon.svelte'
  import { RefreshCw, Download, Eraser, Search } from 'lucide'

  let logs = []
  let loading = true
  let error = null
  let autoRefresh = true
  let filterLevel = 'all'
  let searchTerm = ''
  let refreshInterval = null
  let mounted = false

  // Values as the backend reports them: logrus names the warn level "warning".
  const levels = [
    { value: 'all', label: 'All' },
    { value: 'debug', label: 'Debug' },
    { value: 'info', label: 'Info' },
    { value: 'warning', label: 'Warn' },
    { value: 'error', label: 'Error' }
  ]

  onMount(async () => {
    await loadLogs()
    mounted = true
  })

  onDestroy(() => {
    if (refreshInterval) clearInterval(refreshInterval)
  })

  // Follows the checkbox: refresh every 5 s only while it is ticked.
  $: if (mounted) {
    if (refreshInterval) clearInterval(refreshInterval)
    refreshInterval = autoRefresh ? setInterval(loadLogs, 5000) : null
  }

  async function loadLogs() {
    try {
      const response = await fetch('/api/logs?count=100&level=' + filterLevel)
      if (!response.ok) throw new Error('Failed to load logs')
      const data = await response.json()
      logs = data.logs || []
      error = null
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  function downloadLogs() {
    window.location.href = '/api/logs/download?count=500'
  }

  // Only empties this view; the next refresh shows the log again.
  function clearLogs() {
    logs = []
  }

  $: filteredLogs = logs.filter(log => {
    if (!searchTerm) return true
    const search = searchTerm.toLowerCase()
    return (
      log.message?.toLowerCase().includes(search) ||
      log.fields?.toLowerCase().includes(search) ||
      log.level?.toLowerCase().includes(search) ||
      log.timestamp?.toLowerCase().includes(search)
    )
  })

  $: errorCount = logs.filter(l => l.level === 'error' || l.level === 'fatal' || l.level === 'panic').length
  $: warnCount = logs.filter(l => l.level === 'warning').length
</script>

<div class="page">
  <div class="page-head">
    <div class="stack gap4">
      <h1>Logs</h1>
      <span class="muted">Last 100 entries of the bridge log.</span>
    </div>
    <div class="actions">
      <button type="button" class="btn" on:click={loadLogs}><Icon icon={RefreshCw} size={16} />Refresh</button>
      <button type="button" class="btn" on:click={downloadLogs}><Icon icon={Download} size={16} />Download</button>
      <button type="button" class="btn" on:click={clearLogs} title="Empties this view; the log itself is unchanged">
        <Icon icon={Eraser} size={16} />Clear view
      </button>
    </div>
  </div>

  {#if loading}
    <div class="loading">Loading logs...</div>
  {:else}
    {#if error}
    <div class="banner bad" role="alert">Error: {error}</div>
    {/if}

    <div class="card toolbar">
      <label class="inline">Level
        <select bind:value={filterLevel} on:change={loadLogs}>
          {#each levels as level}
          <option value={level.value}>{level.label}</option>
          {/each}
        </select>
      </label>
      <div class="search">
        <Icon icon={Search} size={16} />
        <input type="search" aria-label="Search logs" placeholder="Search logs…" bind:value={searchTerm} />
      </div>
      <label class="inline check">
        <input type="checkbox" bind:checked={autoRefresh} />
        Auto-refresh (5 s)
      </label>
    </div>

    <div class="chips">
      <span class="chip">Total <strong>{logs.length}</strong></span>
      <span class="chip">Showing <strong>{filteredLogs.length}</strong></span>
      <span class="chip bad">Errors <strong>{errorCount}</strong></span>
      <span class="chip warn">Warnings <strong>{warnCount}</strong></span>
    </div>

    <div class="card table-box">
      {#if filteredLogs.length === 0}
        <div class="empty">No logs found</div>
      {:else}
        <table>
          <thead>
            <tr>
              <th class="col-time">Time</th>
              <th class="col-level">Level</th>
              <th>Message</th>
            </tr>
          </thead>
          <tbody>
            {#each filteredLogs as log}
            <tr class="level-{log.level}">
              <td class="time">{log.timestamp}</td>
              <td><span class="badge badge-{log.level}">{log.level === 'warning' ? 'warn' : log.level}</span></td>
              <td class="message">
                {log.message}
                {#if log.fields}<span class="fields">{log.fields}</span>{/if}
              </td>
            </tr>
            {/each}
          </tbody>
        </table>
      {/if}
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
    gap: 18px;
  }

  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }

  .stack { display: flex; flex-direction: column; }
  .gap4 { gap: 4px; }
  .muted { color: var(--text-muted); font-size: 14px; }

  .page-head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }
  h1 { margin: 0; font-size: 24px; font-weight: 700; letter-spacing: -0.02em; }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; }

  .btn {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    min-height: 40px;
    padding: 0 14px;
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

  .banner {
    padding: 12px 16px;
    border-radius: 11px;
    font-size: 14px;
    font-weight: 600;
  }
  .banner.bad { background: #FEF2F2; color: #B91C1C; }

  .toolbar {
    padding: 14px 16px;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 12px 16px;
  }
  .inline {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    font-weight: 600;
    color: var(--text-label);
  }
  .inline.check { font-size: 14px; font-weight: 400; color: var(--text); cursor: pointer; }
  select {
    min-height: 38px;
    padding: 0 10px;
    border: 1px solid #D5D9E0;
    border-radius: 9px;
    font: inherit;
    font-size: 14px;
    font-weight: 400;
    color: var(--text);
    background: var(--surface);
  }
  .search {
    flex: 1 1 260px;
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 38px;
    padding: 0 12px;
    border: 1px solid #D5D9E0;
    border-radius: 9px;
    color: var(--text-muted);
  }
  .search:focus-within { outline: 2px solid var(--accent); outline-offset: -1px; }
  .search input {
    flex: 1;
    min-width: 0;
    border: 0;
    outline: none;
    font: inherit;
    font-size: 14px;
    color: var(--text);
    background: transparent;
  }
  input[type="checkbox"] { width: 18px; height: 18px; accent-color: var(--accent); }

  .chips { display: flex; flex-wrap: wrap; gap: 8px; }
  .chip {
    font-size: 13px;
    color: var(--text-label);
    background: var(--chip);
    border-radius: 999px;
    padding: 4px 12px;
  }
  .chip.bad { color: #B91C1C; background: #FEF2F2; }
  .chip.warn { color: var(--warn); background: var(--warn-soft); }

  .table-box { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: 14px; }
  th {
    text-align: left;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .col-time { width: 160px; }
  .col-level { width: 90px; }
  td { padding: 10px 16px; border-bottom: 1px solid var(--divider); vertical-align: top; }
  tbody tr:last-child td { border-bottom: 0; }
  .time {
    font-family: var(--font-mono);
    font-size: 13px;
    color: var(--text-muted);
    white-space: nowrap;
  }
  .message { word-break: break-word; }
  .fields {
    display: block;
    margin-top: 2px;
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--text-muted);
  }
  tr.level-warning { background: #FFFBF5; }
  tr.level-error, tr.level-fatal, tr.level-panic { background: #FFFAFA; }

  .badge {
    font-size: 12px;
    font-weight: 600;
    border-radius: 999px;
    padding: 2px 8px;
    color: var(--text-label);
    background: var(--chip);
  }
  .badge-info { color: var(--accent); background: var(--accent-soft); }
  .badge-warning { color: var(--warn); background: var(--warn-icon-bg); }
  .badge-error, .badge-fatal, .badge-panic { color: #B91C1C; background: #FEE2E2; }

  .empty, .loading { text-align: center; padding: 3rem; color: var(--text-muted); }
</style>
