<script>
  import { onMount } from 'svelte'
  import { Conn, join, parseLink } from './lib/conn.svelte.js'
  import { stream } from './lib/stream.svelte.js'
  import { update } from './lib/update.svelte.js'
  import AddServer from './lib/AddServer.svelte'
  import Rooms from './lib/Rooms.svelte'
  import Chat from './lib/Chat.svelte'
  import Members from './lib/Members.svelte'
  import RoomDialog from './lib/RoomDialog.svelte'
  import Settings from './lib/Settings.svelte'
  import SettingsIcon from '@lucide/svelte/icons/settings'
  import SharePicker from './lib/SharePicker.svelte'
  import { voice } from './lib/voice.svelte.js'
  import { diag } from './lib/diag.svelte.js'
  import { httpBase } from './lib/conn.svelte.js'
  import logo from '../build/icon.svg'

  let servers = $state([]) // saved: {address, token, name, role}
  let conns = $state({}) // address -> Conn
  let selected = $state(null) // address
  let adding = $state(false)
  let addInitial = $state({})
  let info = $state({ canHost: false, version: '' })

  let conn = $derived(selected ? conns[selected] : null)
  let roomSel = $state({}) // server address -> the room shown
  let roomDialog = $state(null) // {room} while adding or changing a room
  let settings = $state(null) // the section open in Settings, or null
  let hostInfo = $state(null) // this PC's server (main/host.js status)

  // This PC's server: is it running, and which saved server is it?
  async function checkHost() {
    try {
      hostInfo = await window.eris.hostStatus()
    } catch {}
  }
  checkHost()
  setInterval(checkHost, 15000)

  function isLocalServer(c) {
    if (!hostInfo?.settings || !c) return false
    const port = String(hostInfo.settings.addr).split(':').pop() || '7300'
    const names = [hostInfo.settings.public, '127.0.0.1', 'localhost'].filter(Boolean)
    return names.some((h) => c.entry.address === `${h}:${port}`)
  }

  let startingLocal = $state(false)
  async function startLocal() {
    startingLocal = true
    const r = await window.eris.hostStart()
    startingLocal = false
    await checkHost()
    if (!r.ok) conn.error = r.error
    else conn.connect()
  }
  let room = $derived(conn ? conn.rooms.find((r) => r.id === roomSel[conn.entry.address]) || conn.rooms[0] : null)

  function connect(entry) {
    const c = new Conn(entry)
    stream.attach(c)
    c.on('hello', () => voice.rejoined(c))
    diag.attach(c)
    conns[entry.address] = c
    return c
  }

  onMount(async () => {
    info = await window.eris.info()
    diag.setInfo(info)
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
    if (conns[address]) diag.detach(conns[address])
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
      window.eris.checkUpdate()
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
        // and a second of silence as a WAV, to see audio play in the chat
        const wav = new DataView(new ArrayBuffer(44 + 16000))
        const str = (o, t) => [...t].forEach((ch, i) => wav.setUint8(o + i, ch.charCodeAt(0)))
        str(0, 'RIFF'); wav.setUint32(4, 36 + 16000, true); str(8, 'WAVEfmt '); wav.setUint32(16, 16, true)
        wav.setUint16(20, 1, true); wav.setUint16(22, 1, true); wav.setUint32(24, 8000, true); wav.setUint32(28, 16000, true)
        wav.setUint16(32, 2, true); wav.setUint16(34, 16, true); str(36, 'data'); wav.setUint32(40, 16000, true)
        const w = await c.upload(new File([wav.buffer], `${a.join.name}.wav`, { type: 'audio/wav' }))
        c.sendMessage(r.id, a.say, [f.id, p.id, w.id])
      }
      if (a.expect) {
        await until(() => c.chat[r.id]?.messages.some((m) => m.text === a.expect && m.files.length === 3))
        console.info(`auto test: got "${a.expect}" with its file`)
      }
    }
    if (a.voice) {
      // Voice and cameras: join the first room's voice, and wait to hear
      // (and, with camera, see) the other copy.
      await until(() => c.rooms.length > 0)
      await voice.join(c, c.rooms[0].id)
      if (voice.error) console.error('auto test: voice:', voice.error)
      if (a.camera) await voice.toggleCamera()
      await until(() => voice.hearing > 0)
      console.info(`auto test: hearing ${voice.people.filter((p) => !p.local).map((p) => p.name).join(', ')}`)
      if (a.camera) {
        await until(() => voice.tiles.some((t) => !t.local))
        console.info(`auto test: seeing ${voice.tiles.filter((t) => !t.local).map((t) => t.name).join(', ')}`)
      }
      if (a.share) {
        await voice.toggleShare()
        console.info(`auto test: sharing ${voice.sharing}`)
      }
      if (a.watch) {
        // Opt-in: a share is offered but nothing comes until Watch.
        await until(() => voice.shares.some((s) => !s.local))
        const s = voice.shares.find((s) => !s.local)
        await new Promise((r) => setTimeout(r, 3000))
        console.info(`auto test: ${s.name}'s share offered; received before Watch: ${voice.receivingShare(s.id)}`)
        voice.watch(s.id)
        await until(() => voice.stage?.track)
        await until(() => (voice.stage.track.mediaStreamTrack?.getSettings().width || 0) > 0 || document.querySelector('.stage video')?.videoWidth > 0)
        console.info(`auto test: watching ${s.name}'s screen`)
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


  // This PC's server, through the main process (main/host.js).
  const localApi = {
    status: () => window.eris.hostStatus(),
    log: () => window.eris.hostLog(),
    action: (what) => window.eris.hostAction(what),
    start: () => window.eris.hostStart(),
  }

  // A server the member owns, wherever it runs (with their member token).
  function remoteApi(c) {
    const call = (method, path) => window.eris.api({ method, url: httpBase(c.entry.address) + path, token: c.entry.token })
    return {
      status: async () => {
        const r = await call('GET', '/api/admin/status')
        if (r.status === 200) return { running: true, ...r.json }
        // A server from before owner controls (0.1.4 and older) answers 404.
        return r.status === 404 ? { running: true, legacy: true, version: c.serverVersion, name: c.serverName } : { running: false }
      },
      log: async () => {
        const r = await call('GET', '/api/admin/log')
        return r.status === 200 ? r.json : []
      },
      action: async (what) => {
        const r = await call('POST', `/api/admin/${what}`)
        return r.status === 200 ? { ok: true } : { ok: false, error: r.json?.error || `the server answered ${r.status}` }
      },
      icon: () => c.iconUrl(),
      setIcon: async (file) => {
        const r = file
          ? await window.eris.upload({ url: httpBase(c.entry.address) + '/api/admin/icon', token: c.entry.token, type: file.type, data: await file.arrayBuffer() })
          : await call('DELETE', '/api/admin/icon')
        return r.status === 200 ? { ok: true } : { ok: false, error: r.json?.error || `the server answered ${r.status}` }
      },
    }
  }

  // The Host view offers to join this PC's server when it isn't listed yet.
  function ownLink(st) {
    return String(st.join_link || '').replace("this-pc's-address", '127.0.0.1')
  }

  function ownListed(st) {
    const { address, pin } = parseLink(ownLink(st))
    return servers.find((s) => s.address === address || (pin && s.pin === pin)) || null
  }

  function joinOwn(st) {
    const listed = ownListed(st)
    if (listed) {
      selected = listed.address
      return
    }
    addInitial = { address: ownLink(st), ownerCode: st.owner_code || '' }
    adding = true
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
      <button class="icon" class:sel={s.address === selected} class:pic={!!conns[s.address]?.icon} title={conns[s.address]?.serverName || s.address} onclick={() => (selected = s.address)}>
        {#if conns[s.address]?.icon}
          <img src={conns[s.address].iconUrl()} alt="" />
        {:else}
          {(conns[s.address]?.serverName || s.address).slice(0, 2)}
        {/if}
        <span class="dot {conns[s.address]?.status}"></span>
      </button>
    {/each}
    <button class="icon add" title="Add a server" onclick={() => ((addInitial = {}), (adding = true))}>+</button>
    <span class="grow"></span>
    <button class="icon gear" title="Settings: voice & video, your server, this server" onclick={() => (settings = 'voice')}><SettingsIcon size={22} /></button>
  </nav>

  {#if !conn}
    <main class="empty">
      <img src={logo} alt="" width="96" height="96" />
      <h1>Eris</h1>
      <p class="muted">Add a server to start: paste the join link its owner gave you, and your name.</p>
      <button class="primary" onclick={() => (adding = true)}>Add a server</button>
      {#if hostInfo?.canHost}
        <p class="muted">Or host your own: <button onclick={() => (settings = 'host')}>Host a server on this PC</button></p>
      {/if}
    </main>
  {:else}
    <div class="server">
      <header>
        <div class="title">
          <h1>{conn.serverName || conn.entry.address}{#if info.dev}<span class="devtag" title="Built from the source folder (tools/dev.sh), not a release">DEV</span>{/if}</h1>
          <span class="muted">you are {conn.you?.name || conn.entry.name}{conn.you?.role === 'owner' ? ' (owner)' : ''} · {conn.status}{conn.serverVersion ? ` · server ${conn.serverVersion}` : ''} · app {info.version}</span>
        </div>
        <div class="actions">
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
      {#if conn.status !== 'online' && isLocalServer(conn) && hostInfo && !hostInfo.running}
        <div class="banner warn start">
          <span>This is your server, on this PC, and it isn't running.</span>
          <button class="primary" disabled={startingLocal} onclick={startLocal}>{startingLocal ? 'Starting...' : 'Start server'}</button>
        </div>
      {:else if conn.error}
        <div class="banner bad">{conn.error} <button class="x" onclick={() => (conn.error = '')}>×</button></div>
      {/if}
      {#if stream.notice}
        <div class="banner">{stream.notice} <button class="x" onclick={() => (stream.notice = '')}>×</button></div>
      {/if}

      <div class="body">
        <Rooms
          {conn}
          selected={room?.id}
          onselect={(id) => (roomSel[conn.entry.address] = id)}
          onedit={(r) => (roomDialog = { room: r })}
          oncreate={() => (roomDialog = { room: null })}
          onsettings={() => (settings = 'voice')}
          canHost={info.canHost}
        />
        {#if room}
          {#key room.id}<Chat {conn} {room} />{/key}
        {:else}
          <section class="tests muted">{conn.status === 'online' ? 'No rooms yet.' : 'Connecting...'}</section>
        {/if}
        <Members {conn} />
      </div>
    </div>
  {/if}
</div>

<SharePicker />

{#if settings}
  <Settings
    {conn}
    info={{ ...info, canHostServer: !!hostInfo?.canHost }}
    section={settings}
    {localApi}
    {remoteApi}
    onjoin={(st) => ((settings = null), joinOwn(st))}
    listed={(st) => !!ownListed(st)}
    onclose={() => ((settings = null), checkHost())}
  />
{/if}

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
  .icon.pic { padding: 0; overflow: visible; }
  .icon.pic img { width: 44px; height: 44px; border-radius: 14px; object-fit: cover; display: block; }
  .icon.pic.sel img { outline: 2px solid var(--highlight); outline-offset: 2px; }
  .icon.add { background: var(--surface-2); font-size: 20px; }
  .icon.host { background: var(--surface-2); font-size: 20px; }
  .grow { flex: 1; }
  .icon.gear { background: var(--surface-2); display: grid; place-items: center; }
  .banner.start { display: flex; border-left-color: var(--warn); }
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
