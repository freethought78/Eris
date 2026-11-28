<script>
  // Settings, like Discord's: a full view with sections down the side.
  // Voice & video (your devices), Your server (hosting a server on this PC:
  // start it the first time, stop, restart, updates, its log), This server
  // (owner controls for the server you're looking at, from any PC), Stream
  // and About (this app's version, updates and diagnostics). Esc closes it.
  import { onMount, onDestroy } from 'svelte'
  import DeviceSettings from './DeviceSettings.svelte'
  import ServerPanel from './ServerPanel.svelte'
  import { diag } from './diag.svelte.js'
  import { update } from './update.svelte.js'

  // conn: the server shown (or null); local/remote: ServerPanel apis.
  let { conn, info, section = 'voice', localApi, remoteApi, onjoin, listed, onclose } = $props()

  let current = $state(section)
  let owner = $derived(conn?.you?.role === 'owner')

  const key = (e) => e.key === 'Escape' && onclose()
  onMount(() => window.addEventListener('keydown', key))
  onDestroy(() => window.removeEventListener('keydown', key))

  let sections = $derived([
    ['voice', 'Voice & video'],
    ...(info.canHostServer ? [['host', 'Your server (this PC)']] : []),
    ...(owner ? [['server', `Server: ${conn.serverName || conn.entry.address}`]] : []),
    ['about', 'About Eris'],
  ])
</script>

<div class="settings">
  <nav>
    <h3>Settings</h3>
    {#each sections as [id, label] (id)}
      <button class:sel={current === id} onclick={() => (current = id)}>{label}</button>
    {/each}
  </nav>
  <main>
    <button class="close" title="Close (Esc)" onclick={onclose}>✕</button>
    {#if current === 'voice'}
      <DeviceSettings />
    {:else if current === 'host'}
      <ServerPanel api={localApi} local title="Your server (on this PC)" appVersion={info.version} {onjoin} {listed} />
    {:else if current === 'server' && owner}
      {#key conn.entry.address}
        <ServerPanel api={remoteApi(conn)} title={`Server: ${conn.serverName || conn.entry.address}`} appVersion={info.version} />
      {/key}
    {:else}
      <div class="pane">
        <h2>About Eris</h2>
        <p>Version <b>{info.version}</b>{info.dev ? ' (a dev build)' : ''}.</p>
        <p>
          {#if update.state === 'available'}Eris {update.version} is out. <button class="primary" onclick={() => update.download()}>Update</button>
          {:else if update.state === 'downloading'}Downloading {update.version}... {update.percent}%
          {:else if update.state === 'ready'}Eris {update.version} is ready. <button class="primary" onclick={() => update.install()}>Restart and update</button>
          {:else if update.state === 'latest'}You have the latest version. <button onclick={() => update.check()}>Check again</button>
          {:else if update.state === 'dev'}A dev build doesn't update itself.
          {:else if update.state === 'checking'}Checking for updates...
          {:else}<button onclick={() => update.check()}>Check for updates</button>{/if}
        </p>
        <p class="muted small">Eris also checks when a server you connect to is newer than it.</p>
        <h2>Diagnostics</h2>
        <label class="opt">
          <input type="checkbox" checked={diag.enabled} onchange={(e) => diag.setEnabled(e.target.checked)} />
          Help server owners fix problems: send them this app's diagnostics
        </label>
        <p class="muted small">That's this app's log, connection stats while in voice, and Play Together sessions' logs, sent to the server they're about. Never your messages. They're kept for 14 days.</p>
      </div>
    {/if}
  </main>
</div>

<style>
  .settings { position: fixed; inset: 0; z-index: 6; display: flex; background: var(--bg); }
  nav { width: 240px; background: var(--surface); border-right: 1px solid var(--border); padding: 24px 12px; display: flex; flex-direction: column; gap: 2px; }
  nav h3 { margin: 0 8px 10px; font-size: 12px; text-transform: uppercase; letter-spacing: 0.06em; color: var(--muted); }
  nav button { background: none; border: none; text-align: left; padding: 8px 10px; color: var(--muted); border-radius: var(--radius); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  nav button:hover { background: var(--surface-2); color: var(--text); }
  nav button.sel { background: var(--accent); color: var(--text); }
  main { flex: 1; position: relative; display: flex; min-width: 0; padding: 24px 28px; overflow-y: auto; }
  main :global(.panel) { padding: 0; }
  .close { position: absolute; top: 16px; right: 18px; border-radius: 50%; width: 36px; height: 36px; padding: 0; z-index: 1; }
  .pane { display: grid; gap: 12px; align-content: start; flex: 1; }
  .pane h2 { margin: 0; font-size: 18px; text-transform: none; letter-spacing: 0; color: var(--text); }
  .pane p { margin: 0; }
  .small { font-size: 12px; }
  .opt { display: flex; gap: 8px; align-items: center; }
  .opt input { width: auto; }
</style>
