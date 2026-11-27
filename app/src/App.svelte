<script>
  import { onMount } from 'svelte'
  import { Conn, join, parseLink } from './lib/conn.svelte.js'
  import { stream } from './lib/stream.svelte.js'
  import { update } from './lib/update.svelte.js'
  import AddServer from './lib/AddServer.svelte'
  import Results from './lib/Results.svelte'
  import Rooms from './lib/Rooms.svelte'
  import Chat from './lib/Chat.svelte'
  import Members from './lib/Members.svelte'
  import RoomDialog from './lib/RoomDialog.svelte'
  import logo from '../build/icon.svg'

  let servers = $state([]) // saved: {address, token, name, role}
  let conns = $state({}) // address -> Conn
  let selected = $state(null) // address
  let adding = $state(false)
  let addInitial = $state({})
  let info = $state({ canHost: false, version: '' })

  let conn = $derived(selected ? conns[selected] : null)
  let roomSel = $state({}) // server address -> the room shown
  let view = $state('chat') // chat or tests
  let roomDialog = $state(null) // {room} while adding or changing a room
  let room = $derived(conn ? conn.rooms.find((r) => r.id === roomSel[conn.entry.address]) || conn.rooms[0] : null)

  function connect(entry) {
    const c = new Conn(entry)
    stream.attach(c)
    conns[entry.address] = c
    return c
  }

  onMount(async () => {
    info = await window.eris.info()
    Conn.appVersion = info.version
    servers = await window.eris.loadServers()
    for (const s of servers) connect(s)
    selected = servers[0]?.address || null
    if (info.auto) runAuto(info.auto).catch((e) => console.error('auto test:', e))
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
    console.info(`auto test: Eris ${info.version}`, JSON.stringify(a))
    if (a.update) {
      // Accept an update as soon as one is offered (testing updates).
      const t = setInterval(() => {
        if (update.state === 'available') update.download()
        if (update.state === 'ready') {
          clearInterval(t)
          console.info(`auto test: installing ${update.version}`)
          update.install()
        }
      }, 500)
    }
    if (!a.join) return
    const { address, pin } = parseLink(a.join.link || a.join.address)
    if (!servers.some((s) => s.address === address)) {
      await window.eris.pin(address, pin)
      const r = await join(address, a.join.name, a.join.password || '', a.join.ownerCode || '')
      await added({ address, pin, token: r.token, name: r.member.name, role: r.member.role })
    }
    const c = conns[address]
    const until = (f) => new Promise((res) => { const t = setInterval(() => { if (f()) { clearInterval(t); res() } }, 200) })
    await until(() => c.status === 'online')
    if (a.say || a.expect) {
      // Chat: send a message (and a small file) to the first room, and wait
      // for the other copy's.
      await until(() => c.rooms.length > 0)
      const r = c.rooms[0]
      await until(() => c.chat[r.id])
      if (a.say) {
        const f = await c.upload(new File([`file from ${a.join.name}`], `${a.join.name}.txt`, { type: 'text/plain' }))
        // and a small picture, to see pictures show in place
        const cv = Object.assign(document.createElement('canvas'), { width: 160, height: 90 })
        const g = cv.getContext('2d')
        g.fillStyle = '#e94560'
        g.fillRect(0, 0, 160, 90)
        g.fillStyle = '#fff'
        g.font = 'bold 28px sans-serif'
        g.fillText(a.join.name, 12, 56)
        const png = await new Promise((res) => cv.toBlob(res, 'image/png'))
        const p = await c.upload(new File([png], `${a.join.name}.png`, { type: 'image/png' }))
        c.sendMessage(r.id, a.say, [f.id, p.id])
      }
      if (a.expect) {
        await until(() => c.chat[r.id]?.messages.some((m) => m.text === a.expect && m.files.length === 2))
        console.info(`auto test: got "${a.expect}" with its file`)
      }
    }
    if (a.live) stream.goLive(c, { extraArgs: a.hostArgs })
    if (a.watch) {
      // Join a member's stream as soon as they're live (a plain join, no test).
      await until(() => c.members.some((m) => m.name === a.watch && c.live[m.id]))
      stream.join(c, c.members.find((m) => m.name === a.watch).id)
    }
    if (a.testWith) {
      await until(() => c.members.some((m) => m.name === a.testWith && m.online))
      await until(() => stream.hosting && c.live[c.you.id])
      if (a.testDelay) await new Promise((r) => setTimeout(r, a.testDelay * 1000))
      const m = c.members.find((m) => m.name === a.testWith)
      stream.testWith(c, m.id, a.seconds || 20)
    }
  }

</script>

{#if update.state === 'available' || update.state === 'downloading' || update.state === 'ready'}
  <div class="updatebar">
    {#if update.state === 'available'}
      Eris {update.version} is available (you have {info.version}).
      <button class="primary" onclick={() => update.download()}>Update</button>
    {:else if update.state === 'downloading'}
      Downloading Eris {update.version}... {update.percent}%
    {:else}
      Eris {update.version} is ready.
      <button class="primary" onclick={() => update.install()}>Restart and update</button>
    {/if}
  </div>
{/if}

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

  {#if !conn}
    <main class="empty">
      <img src={logo} alt="" width="96" height="96" />
      <h1>Eris</h1>
      <p class="muted">Add a server to start: paste the join link its owner gave you, and your name.</p>
      <button class="primary" onclick={() => (adding = true)}>Add a server</button>
    </main>
  {:else}
    <div class="server">
      <header>
        <div class="title">
          <h1>{conn.serverName || conn.entry.address}{#if info.dev}<span class="devtag" title="Built from the source folder (tools/dev.sh), not a release">DEV</span>{/if}</h1>
          <span class="muted">you are {conn.you?.name || conn.entry.name}{conn.you?.role === 'owner' ? ' (owner)' : ''} · {conn.status}{conn.serverVersion ? ` · server ${conn.serverVersion}` : ''} · app {info.version}</span>
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

      {#if conn.status === 'outdated'}
        <div class="banner warn">
          <p>
            This server has been updated to Eris <b>{conn.serverVersion}</b>, and you have {info.version}. Update
            Eris before reconnecting, so everything works together.
          </p>
          <div class="actions">
            {#if update.state === 'available'}
              <button class="primary" onclick={() => update.download()}>Update to {update.version}</button>
            {:else if update.state === 'downloading'}
              <span>Downloading... {update.percent}%</span>
            {:else if update.state === 'ready'}
              <button class="primary" onclick={() => update.install()}>Restart and update</button>
            {:else if update.state === 'dev'}
              <span class="muted">This copy runs from the source folder and can't update itself.</span>
            {:else if update.state === 'error'}
              <span class="error">Couldn't check for updates: {update.error}</span>
              <button onclick={() => update.check()}>Try again</button>
            {:else}
              <span class="muted">Checking for an update...</span>
            {/if}
            <button onclick={() => ((conn.ignoreVersion = true), conn.connect())}>Connect anyway</button>
          </div>
        </div>
      {/if}
      {#if conn.error}
        <div class="banner bad">{conn.error} <button class="x" onclick={() => (conn.error = '')}>×</button></div>
      {/if}
      {#if stream.notice}
        <div class="banner">{stream.notice} <button class="x" onclick={() => (stream.notice = '')}>×</button></div>
      {/if}

      <div class="body">
        <Rooms
          {conn}
          selected={room?.id}
          {view}
          onview={(v) => (view = v)}
          onselect={(id) => ((roomSel[conn.entry.address] = id), (view = 'chat'))}
          onedit={(r) => (roomDialog = { room: r })}
          oncreate={() => (roomDialog = { room: null })}
        />
        {#if view === 'tests'}
          <section class="tests">
            <h2>Stream tests</h2>
            <Results results={conn.results} />
          </section>
        {:else if room}
          {#key room.id}<Chat {conn} {room} />{/key}
        {:else}
          <section class="tests muted">{conn.status === 'online' ? 'No rooms yet.' : 'Connecting...'}</section>
        {/if}
        <Members {conn} />
      </div>
    </div>
  {/if}
</div>

{#if roomDialog}
  <RoomDialog {conn} room={roomDialog.room} onclose={() => (roomDialog = null)} />
{/if}

{#if adding}
  <AddServer initial={addInitial} onadded={added} oncancel={() => (adding = false)} />
{/if}

<style>
  .layout { display: flex; height: 100%; }
  .updatebar { background: var(--accent); padding: 8px 16px; display: flex; align-items: center; gap: 12px; border-bottom: 1px solid var(--border); }
  .servers { width: 64px; background: var(--bg); border-right: 1px solid var(--border); display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 10px 0; flex: none; }
  .icon { position: relative; width: 44px; height: 44px; border-radius: 14px; padding: 0; text-transform: uppercase; font-weight: 600; }
  .icon.sel { background: var(--highlight); }
  .icon.add { background: var(--surface-2); font-size: 20px; }
  .dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; background: var(--muted); }
  .icon .dot { position: absolute; right: -1px; bottom: -1px; border: 2px solid var(--bg); width: 12px; height: 12px; }
  .dot.online { background: var(--good); }
  .dot.connecting { background: var(--warn); }
  .dot.rejected { background: var(--highlight); }
  .server { flex: 1; display: flex; flex-direction: column; min-width: 0; }
  header { display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 10px 16px; border-bottom: 1px solid var(--border); background: var(--surface); }
  .title { min-width: 0; }
  h1 { margin: 0 0 2px; font-size: 18px; }
  .title .muted { font-size: 12px; }
  .devtag { background: var(--warn); color: #1a1a2e; font-size: 11px; font-weight: 700; padding: 1px 6px; border-radius: 4px; margin-left: 8px; vertical-align: middle; }
  .actions { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  .banner { background: var(--surface-2); border-left: 3px solid var(--highlight); padding: 8px 12px; margin: 8px 12px 0; border-radius: var(--radius); display: flex; align-items: center; gap: 8px; }
  .banner.warn { border-left-color: var(--warn); display: block; }
  .banner.warn p { margin: 0 0 8px; }
  .banner .x { margin-left: auto; background: none; border: none; padding: 0 6px; }
  .body { flex: 1; display: flex; min-height: 0; }
  .tests { flex: 1; overflow: auto; padding: 16px 24px; }
  .empty { flex: 1; max-width: 420px; margin: 18vh auto; text-align: center; display: grid; gap: 12px; justify-items: center; align-content: start; }
</style>
