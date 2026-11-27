<script>
  // Stream test results, newest first: both sides of each test together.
  let { results } = $props()

  let tests = $derived.by(() => {
    const byId = new Map()
    for (const r of results) {
      if (!byId.has(r.test_id)) byId.set(r.test_id, { id: r.test_id, at: r.at, host: null, player: null })
      const t = byId.get(r.test_id)
      if (!t[r.side]) t[r.side] = r
      t.at = Math.min(t.at, r.at)
    }
    return [...byId.values()].sort((a, b) => b.at - a.at)
  })

  let open = $state(null)

  const ms = (v) => (v == null ? '-' : Math.round(v))
  const pair = (p) => (Array.isArray(p) ? `${ms(p[0])} / ${ms(p[1])}` : '-')
  function verdict(s) {
    if (!s || s.button_to_screen == null || s.target == null) return ''
    return s.button_to_screen <= s.target ? 'good' : 'warn'
  }
</script>

{#if tests.length === 0}
  <p class="muted">No stream tests yet. Start one from someone's name while you're live.</p>
{:else}
  <table>
    <thead>
      <tr>
        <th>Test</th><th>Player</th><th>Shown</th><th>Lost</th><th>Screen to screen</th><th>Button to screen</th>
        <th>Round trip</th><th>Picture</th><th>GPU decode</th><th>Host sent</th>
      </tr>
    </thead>
    <tbody>
      {#each tests as t (t.id)}
        {@const p = t.player?.summary}
        {@const h = t.host?.summary}
        <tr class="row" onclick={() => (open = open === t.id ? null : t.id)}>
          <td>{t.id}</td>
          <td>{t.player?.member || '-'}{p?.os ? ` (${p.os})` : ''}</td>
          <td>{p ? `${p.shown_fps} fps` : '-'}</td>
          <td>{p ? p.frames_lost : '-'}</td>
          <td>{p ? pair(p.screen_to_screen) : '-'} <span class="muted">ms</span></td>
          <td class={verdict(p)}>{p?.button_to_screen != null ? `${ms(p.button_to_screen)} ms` : '-'}{p?.target != null ? ` (target ${ms(p.target)})` : ''}</td>
          <td>{p?.round_trip ? `${ms(p.round_trip[0])} ms` : '-'}</td>
          <td>{p?.picture || '-'}</td>
          <td>{p?.gpu_decode_pct != null ? `${p.gpu_decode_pct}%` : '-'}</td>
          <td>{h ? `${h.fps} fps, ${h.video_mbps} Mbps` : '-'}</td>
        </tr>
        {#if open === t.id}
          <tr>
            <td colspan="10" class="details">
              {#if h?.changes?.length}
                <h4>Host: what auto-quality changed</h4>
                <pre>{h.changes.join('\n')}</pre>
              {/if}
              {#if h}
                <h4>Host</h4>
                <pre>{JSON.stringify({ ...h, changes: undefined }, null, 2)}</pre>
              {/if}
              {#if p?.text}
                <h4>Player</h4>
                <pre>{p.text}</pre>
              {/if}
            </td>
          </tr>
        {/if}
      {/each}
    </tbody>
  </table>
{/if}

<style>
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th { text-align: left; color: var(--muted); font-weight: 500; padding: 4px 6px; border-bottom: 1px solid var(--border); white-space: nowrap; }
  td { padding: 6px; border-bottom: 1px solid var(--border); white-space: nowrap; }
  .row { cursor: pointer; }
  .row:hover { background: var(--surface-2); }
  .good { color: var(--good); }
  .warn { color: var(--warn); }
  .details { background: var(--bg); white-space: normal; }
  pre { font-size: 12px; overflow-x: auto; margin: 4px 0 12px; }
  h4 { margin: 8px 0 2px; font-size: 12px; color: var(--muted); }
</style>
