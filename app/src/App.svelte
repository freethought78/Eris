<script>
  import { onMount } from 'svelte'
  import { Conn, join } from './lib/conn.svelte.js'
  import { stream } from './lib/stream.svelte.js'
  import AddServer from './lib/AddServer.svelte'
  import Results from './lib/Results.svelte'

  let servers = $state([]) // saved: {address, token, name, role}
  let conns = $state({}) // address -> Conn
  let selected = $state(null) // address
  let adding = $state(false)
  let addInitial = $state({})
  let info = $state({ canHost: false, version: '' })

  let conn = $derived(selected ? conns[selected] : null)

  function connect(entry) {
    const c = new Conn(entry)
    stream.attach(c)
    conns[entry.address] = c
    return c
  }

  onMount(async () => {
    info = await window.eris.info()
    servers = await window.eris.loadServers()
    for (const s of servers) connect(s)
    selected = servers[0]?.address || null
    if (info.auto) runAuto(info.auto)
  })

  async function added(entry) {
    adding = false
    servers = [...servers.filter((s) => s.address !== entry.address), entry]
    await window.eris.saveServers($state.snapshot(servers))
    conns[entry.address]?.close()
    connect(entry)
    selected = entry.address
  }

  async function remove(address) {
    conns[address]?.close()
    delete conns[address]
    servers = servers.filter((s) => s.address !== address)
    await window.eris.saveServers($state.snapshot(servers))
    selected = servers[0]?.address || null
  }

  // Automated tests start the app with --auto='{...}' (see app/README.md).
  async function runAuto(a) {
    if (a.join && !servers.some((s) => s.address === a.join.address)) {
      const r = await join(a.join.address, a.join.name, a.join.password || '', a.join.ownerCode || '')
      await added({ address: a.join.address, token: r.token, name: r.member.name, role: r.member.role })
    }
    const c = conns[a.join?.address || selected]
    const until = (f) => new Promise((res) => { const t = setInterval(() => { if (f()) { clearInterval(t); res() } }, 200) })
    await until(() => c.status === 'online')
    if (a.live) stream.goLive(c, { extraArgs: a.hostArgs })
    if (a.testWith) {
      await until(() => c.members.some((m) => m.name === a.testWith && m.online))
      await until(() => stream.hosting && c.live[c.you.id])
      const m = c.members.find((m) => m.name === a.testWith)
      stream.testWith(c, m.id, a.seconds || 20)
    }
  }

  let others = $derived(conn ? conn.members.filter((m) => m.id !== conn.you?.id) : [])
</script>

<div class="layout">
  <nav class="servers">
    {#each servers as s (s.address)}
      <button class="icon" class:sel={s.address === selected} title={s.address} onclick={() => (selected = s.address)}>
        {(conns[s.address]?.serverName || s.address).slice(0, 2)}
        <span class="dot {conns[s.address]?.status}"></span>
      </button>
    {/each}
    <button class="icon add" title="Add a server" onclick={() => ((addInitial = {}), (adding = true))}>+</button>
  </nav>

  <main>
    {#if !conn}
      <div class="empty">
        <h1>Eris</h1>
        <p class="muted">Add a server to start: the address and your name. The server's host gives you the address.</p>
        <button class="primary" onclick={() => (adding = true)}>Add a server</button>
      </div>
    {:else}
      <header>
        <div>
          <h1>{conn.serverName || conn.entry.address}</h1>
          <span class="muted">{conn.entry.address} · you are {conn.you?.name || conn.entry.name}{conn.you?.role === 'owner' ? ' (owner)' : ''} · {conn.status}</span>
        </div>
        <div class="actions">
          {#if info.canHost}
            {#if stream.hosting}
              <button onclick={() => stream.stopLive()}>Stop streaming</button>
            {:else}
              <button class="primary" disabled={conn.status !== 'online'} onclick={() => stream.goLive(conn)}>Go Live</button>
            {/if}
          {/if}
          <button onclick={() => remove(conn.entry.address)} title="Remove this server from the list">Remove</button>
        </div>
      </header>

      {#if conn.error}<p class="error">{conn.error}</p>{/if}
      {#if stream.notice}<p class="notice">{stream.notice}</p>{/if}

      <section>
        <h2>Members</h2>
        <ul class="members">
          {#each others as m (m.id)}
            <li>
              <span class="dot {m.online ? 'online' : 'offline'}"></span>
              <span class="name">{m.name}</span>
              {#if m.role !== 'member'}<span class="muted">{m.role}</span>{/if}
              {#if conn.live[m.id]}
                <span class="live">LIVE</span>
                <span class="muted">{conn.live[m.id].title}</span>
                {#if stream.playing?.hostId === m.id}
                  <button onclick={() => stream.leave()}>Leave</button>
                {:else}
                  <button class="primary" onclick={() => stream.join(conn, m.id)}>Join</button>
                {/if}
              {/if}
              {#if stream.hosting && m.online}
                <button onclick={() => stream.testWith(conn, m.id, 60)} title="They join your stream for 60 s; both sides' results come back here">Stream test</button>
              {/if}
            </li>
          {:else}
            <li class="muted">Nobody else has joined yet. Give them the address: {conn.entry.address}</li>
          {/each}
        </ul>
      </section>

      <section>
        <h2>Stream tests</h2>
        <Results results={conn.results} />
      </section>
    {/if}
  </main>
</div>

{#if adding}
  <AddServer initial={addInitial} onadded={added} oncancel={() => (adding = false)} />
{/if}

<style>
  .layout { display: flex; height: 100%; }
  .servers { width: 64px; background: var(--surface); border-right: 1px solid var(--border); display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 10px 0; }
  .icon { position: relative; width: 44px; height: 44px; border-radius: 14px; padding: 0; text-transform: uppercase; font-weight: 600; }
  .icon.sel { background: var(--highlight); }
  .icon.add { background: var(--surface-2); font-size: 20px; }
  .dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; background: var(--muted); }
  .icon .dot { position: absolute; right: -1px; bottom: -1px; border: 2px solid var(--surface); width: 12px; height: 12px; }
  .dot.online { background: var(--good); }
  .dot.connecting { background: var(--warn); }
  .dot.rejected { background: var(--highlight); }
  main { flex: 1; overflow: auto; padding: 16px 24px; }
  header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }
  h1 { margin: 0 0 4px; font-size: 20px; }
  .actions { display: flex; gap: 8px; }
  .empty { max-width: 420px; margin: 18vh auto; text-align: center; display: grid; gap: 12px; justify-items: center; }
  .notice { background: var(--surface-2); border-left: 3px solid var(--highlight); padding: 8px 12px; border-radius: var(--radius); }
  .members { list-style: none; padding: 0; margin: 0; display: grid; gap: 6px; }
  .members li { display: flex; align-items: center; gap: 10px; padding: 6px 8px; background: var(--surface); border-radius: var(--radius); }
  .name { font-weight: 600; }
  .live { background: var(--highlight); color: white; font-size: 11px; font-weight: 700; padding: 1px 6px; border-radius: 4px; }
</style>
