<script>
  // Who's on the server: online first, who's live (with Join), and stream
  // tests while you're live.
  import { stream } from './stream.svelte.js'

  let { conn } = $props()

  let sorted = $derived(
    [...conn.members].sort((a, b) => (b.online ? 1 : 0) - (a.online ? 1 : 0) || a.name.localeCompare(b.name)),
  )
  let online = $derived(sorted.filter((m) => m.online))
  let offline = $derived(sorted.filter((m) => !m.online))
</script>

<aside class="members">
  {#each [['Online', online], ['Offline', offline]] as [title, list]}
    {#if list.length}
      <h4>{title} — {list.length}</h4>
      {#each list as m (m.id)}
        <div class="m" class:off={!m.online}>
          <span class="dot" class:on={m.online}></span>
          <span class="name">{m.name}{m.id === conn.you?.id ? ' (you)' : ''}</span>
          {#if m.role !== 'member'}<span class="role">{m.role}</span>{/if}
        </div>
        {#if conn.live[m.id] && m.id !== conn.you?.id}
          <div class="live">
            <span class="badge">LIVE</span>
            <span class="muted small">{conn.live[m.id].title}</span>
            {#if stream.playing?.hostId === m.id}
              <button onclick={() => stream.leave()}>Leave</button>
            {:else}
              <button class="primary" onclick={() => stream.join(conn, m.id)}>Join</button>
            {/if}
          </div>
        {/if}
      {/each}
    {/if}
  {/each}
</aside>

<style>
  .members { width: 220px; background: var(--surface); border-left: 1px solid var(--border); padding: 12px 10px; overflow-y: auto; }
  h4 { margin: 10px 4px 6px; font-size: 11px; text-transform: uppercase; letter-spacing: 0.06em; color: var(--muted); font-weight: 600; }
  .m { display: flex; align-items: center; gap: 8px; padding: 5px 6px; border-radius: var(--radius); }
  .m.off { opacity: 0.5; }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--muted); flex: none; }
  .dot.on { background: var(--good); }
  .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .role { margin-left: auto; font-size: 11px; color: var(--muted); }
  .live { display: flex; align-items: center; gap: 6px; padding: 0 6px 6px 22px; flex-wrap: wrap; }
  .badge { background: var(--highlight); color: white; font-size: 10px; font-weight: 700; padding: 1px 5px; border-radius: 4px; }
  .small { font-size: 11px; }
  .live button { padding: 3px 8px; font-size: 12px; }
</style>
