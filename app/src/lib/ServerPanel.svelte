<script>
  // A server's controls (server/control.go): is it running, who's on, its
  // version and updates, its recent log; Stop, Restart, Check for updates,
  // and (for the server on this PC) Start. Used for the server on this PC
  // (Host) and by owners for their server from anywhere (Server).
  //
  // api: {status(), log(), action(what), start?()}; local: it's this PC's.
  import { onDestroy } from 'svelte'
  import { update } from './update.svelte.js'

  let { api, local = false, title, onjoin = null, listed = () => true, appVersion = '' } = $props()

  let st = $state(null) // what the server reports, or {running: false}
  let lines = $state([])
  let busy = $state('')
  let error = $state('')
  let copied = $state(false)
  let logBox = $state(null)

  async function refresh() {
    try {
      st = await api.status()
      if (st.running) {
        const l = await api.log()
        const atEnd = !logBox || logBox.scrollTop + logBox.clientHeight >= logBox.scrollHeight - 8
        lines = l
        if (atEnd) queueMicrotask(() => logBox && (logBox.scrollTop = logBox.scrollHeight))
      }
    } catch (err) {
      st = { running: false }
    }
  }
  refresh()
  const timer = setInterval(refresh, 3000)
  onDestroy(() => clearInterval(timer))

  async function run(what) {
    error = ''
    if (what === 'stop') {
      const again = local ? 'Start it again here when you want.' : 'It can only be started again on the PC it runs on.'
      if (!confirm(`Stop the server? Everyone is disconnected until it's started again. ${again}`)) return
    }
    busy = what
    const r = what === 'start' ? await api.start() : await api.action(what)
    if (!r?.ok) error = r?.error || 'That didn\'t work.'
    // Updating: the server checks now (and restarts into a newer one); the
    // app checks too, so both end up on the same version.
    if (what === 'update') window.eris.checkUpdate()
    setTimeout(() => (busy = ''), what === 'update' ? 4000 : 1500)
    setTimeout(refresh, 800)
  }

  let iconPicker = $state(null)

  async function setIcon(file) {
    error = ''
    if (file && file.size > 1024 * 1024) return (error = 'The icon can be up to 1 MB.')
    const r = await api.setIcon(file)
    if (!r.ok) error = r.error
  }

  async function addIcon() {
    const r = await window.eris.hostAddIcon()
    if (!r?.ok) error = r?.error || "Couldn't add the icon."
    refresh()
  }

  function copy(text) {
    navigator.clipboard.writeText(text)
    copied = true
    setTimeout(() => (copied = false), 1500)
  }

  const ago = (ms) => {
    if (!ms) return ''
    const m = Math.round((Date.now() - ms) / 60000)
    return m < 1 ? 'just now' : m < 60 ? `${m} min ago` : `${Math.floor(m / 60)} h ago`
  }

  function uptime(ms) {
    const m = Math.floor((Date.now() - ms) / 60000)
    return m < 60 ? `${m} min` : m < 2880 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${Math.floor(m / 1440)} days`
  }

  let upd = $derived.by(() => {
    const u = st?.update
    if (!u) return ''
    return {
      checking: 'checking for a newer server...',
      latest: `up to date (checked ${ago(u.checked)})`,
      available: `${u.latest} is out`,
      downloaded: `${u.latest} downloaded; it goes in as soon as nobody is streaming`,
      error: `the last check failed: ${u.error || ''}`,
      off: 'update checks are off',
      idle: 'not checked yet',
    }[u.state]
  })
</script>

<section class="panel">
  <header>
    <h2>{title}</h2>
    {#if st}
      <span class="pill" class:on={st.running}><span class="dot"></span>{st.running ? 'Running' : 'Not running'}</span>
    {/if}
    <span class="grow"></span>
    {#if st?.running && !st.legacy}
      <button onclick={() => run('update')} disabled={!!busy}>{busy === 'update' ? 'Checking...' : 'Check for updates'}</button>
      <button onclick={() => run('restart')} disabled={!!busy}>Restart</button>
      <button class="stop" onclick={() => run('stop')} disabled={!!busy}>Stop server</button>
    {:else if st && local && st.canHost}
      <button class="primary" onclick={() => run('start')} disabled={!!busy}>{busy === 'start' ? 'Starting...' : 'Start server'}</button>
    {/if}
  </header>

  {#if error}<p class="error">{error}</p>{/if}
  {#if local && st?.canAddIcon && !st?.serverIcon}
    <p class="note">
      Want to start the server without opening Eris?
      <button class="mini" onclick={addIcon}>Add a desktop icon for the server</button>
    </p>
  {/if}

  {#if !st}
    <p class="muted">Looking...</p>
  {:else if !st.running}
    {#if local}
      {#if st.canHost}
        <p>Your server isn't running. <b>Start server</b> opens it in its own window, which stays open as long as it
          runs; close that window (or press Stop here) to stop it. It keeps running when you close Eris.</p>
        <p class="muted small">Its data: {st.settings?.data}. It listens on {st.settings?.addr}. Settings: host.json beside the data.</p>
      {:else}
        <p class="muted">This copy of Eris doesn't include the server program (it's in the Linux app for now).</p>
      {/if}
    {:else}
      <p class="muted">Can't reach the server's controls. It may be stopped (it can only be started on the PC it runs on),
        or this copy of Eris isn't its owner.</p>
    {/if}
  {:else if st.legacy}
    <p>This server runs Eris <b>{st.version}</b>, from before it could be controlled from the app. It updates itself
      within the hour (or {local ? 'type update in its window' : 'its owner can type update in its window'}); after that,
      everything here works.{#if local} To stop it now, close its window.{/if}</p>
  {:else}
    <div class="facts">
      <span class="muted">Name</span><b>{st.name}</b>
      <span class="muted">Version</span><b>{st.version}{#if appVersion && st.version !== appVersion}<span class="muted"> (this app is {appVersion})</span>{/if}</b>
      <span class="muted">Updates</span><b>{upd}</b>
      <span class="muted">This app</span>
      <b>
        {#if update.state === 'available'}{update.version} is out <button class="mini primary" onclick={() => update.download()}>Update the app</button>
        {:else if update.state === 'downloading'}downloading {update.version}... {update.percent}%
        {:else if update.state === 'ready'}{update.version} is ready <button class="mini primary" onclick={() => update.install()}>Restart and update</button>
        {:else if update.state === 'latest'}up to date
        {:else if update.state === 'dev'}a dev build (doesn't update itself)
        {:else if update.state === 'checking'}checking...
        {:else}{appVersion}{/if}
      </b>
      <span class="muted">Up</span><b>{uptime(st.started)}</b>
      <span class="muted">Online</span><b>{st.online?.length ? st.online.join(', ') : 'nobody'}{st.live ? ` · ${st.live} streaming` : ''}</b>
      {#if api.setIcon}
        <span class="muted">Icon</span>
        <b class="iconrow">
          {#if api.icon()}<img src={api.icon()} alt="" />{:else}<span class="muted">none: the list shows its first letters</span>{/if}
          <button class="mini" onclick={() => iconPicker.click()}>{api.icon() ? 'Change' : 'Choose a picture'}</button>
          {#if api.icon()}<button class="mini" onclick={() => setIcon(null)}>Remove</button>{/if}
          <input type="file" accept="image/png,image/jpeg,image/webp,image/gif" hidden bind:this={iconPicker} onchange={(e) => (setIcon(e.target.files[0]), (e.target.value = ''))} />
        </b>
      {/if}
      <span class="muted">Join link</span>
      <b class="link">{st.join_link} <button class="mini" onclick={() => copy(st.join_link)}>{copied ? 'Copied' : 'Copy'}</button></b>
      {#if st.owner_code}
        <span class="muted">Owner code</span><b>{st.owner_code} <span class="muted">(the first to join with it becomes the owner)</span></b>
      {/if}
      {#if local}<span class="muted">Data</span><b>{st.data}</b>{/if}
    </div>
    {#if onjoin && !listed(st)}
      <p class="note">This server isn't in your list yet. <button class="mini primary" onclick={() => onjoin(st)}>Join it</button></p>
    {/if}
    <h3>Log</h3>
    <pre bind:this={logBox}>{lines.join('\n')}</pre>
  {/if}
</section>

<style>
  .panel { flex: 1; display: flex; flex-direction: column; gap: 12px; padding: 16px 22px; min-width: 0; overflow: hidden; }
  header { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  h2 { margin: 0; font-size: 18px; text-transform: none; letter-spacing: 0; color: var(--text); }
  h3 { margin: 4px 0 0; font-size: 12px; text-transform: uppercase; letter-spacing: 0.06em; color: var(--muted); }
  .grow { flex: 1; }
  .pill { display: inline-flex; align-items: center; gap: 6px; font-weight: 600; color: var(--muted); }
  .pill .dot { width: 10px; height: 10px; border-radius: 50%; background: var(--muted); }
  .pill.on { color: var(--text); }
  .pill.on .dot { background: var(--good); box-shadow: 0 0 8px var(--good); }
  .stop { background: var(--highlight); border-color: var(--highlight); font-weight: 600; }
  .facts { display: grid; grid-template-columns: max-content 1fr; gap: 6px 16px; background: var(--surface); border-radius: var(--radius); padding: 12px 16px; }
  .facts b { font-weight: 600; user-select: text; word-break: break-all; }
  .iconrow { display: flex; align-items: center; gap: 6px; }
  .iconrow img { width: 40px; height: 40px; border-radius: 12px; object-fit: cover; }
  .link { font-family: ui-monospace, monospace; font-size: 12px; }
  .mini { padding: 2px 8px; font-size: 12px; margin-left: 6px; font-family: system-ui, sans-serif; }
  .note { background: var(--surface-2); border-left: 3px solid var(--good); padding: 8px 12px; border-radius: var(--radius); margin: 0; }
  .small { font-size: 12px; }
  p { margin: 0; line-height: 1.5; }
  pre { flex: 1; min-height: 120px; overflow: auto; margin: 0; background: #10101f; border: 1px solid var(--border); border-radius: var(--radius); padding: 10px 12px; font: 12px ui-monospace, monospace; white-space: pre-wrap; user-select: text; }
</style>
