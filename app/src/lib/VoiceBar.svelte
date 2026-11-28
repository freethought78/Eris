<script>
  // The panel under the rooms, like Discord's: while in a room's voice, the
  // call's controls (mute, deafen, camera, share your screen, leave); always,
  // who you are, Go Live (Play Together, on Linux) and ⚙ for settings.
  import { voice } from './voice.svelte.js'
  import { stream } from './stream.svelte.js'

  // conn: the server shown; canHost: this PC can go live (Linux, for now).
  let { onsettings, conn, canHost = false } = $props()

  let roomName = $derived(voice.conn?.room(voice.roomId)?.name || '')
</script>

<div class="bar">
  {#if voice.error}<p class="err" title={voice.error}>{voice.error} <button onclick={() => (voice.error = '')}>×</button></p>{/if}
  {#if voice.state}
    <div class="call">
      <div class="where">
        <span class="status" class:ok={voice.state === 'connected'}>
          {voice.state === 'connected' ? 'In voice' : voice.state === 'connecting' ? 'Connecting...' : 'Reconnecting...'}
        </span>
        <span class="room">#{roomName} · {voice.conn?.serverName || ''}</span>
      </div>
      <div class="buttons">
        <button class:on={voice.muted} title={voice.muted ? 'Unmute' : 'Mute'} onclick={() => voice.toggleMute()}>{voice.muted ? '🔇' : '🎤'}</button>
        <button class:on={voice.deafened} title={voice.deafened ? 'Undeafen' : 'Deafen (hear nobody, and mute)'} onclick={() => voice.toggleDeafen()}>{voice.deafened ? '🔕' : '🎧'}</button>
        <button class:lit={voice.camera} title={voice.camera ? 'Turn your camera off' : 'Turn your camera on'} onclick={() => voice.toggleCamera()}>📷</button>
        <button class:lit={voice.sharing} title={voice.sharing ? 'Stop sharing your screen' : 'Share your screen or a window (people in the room choose to watch it)'} onclick={() => voice.toggleShare()}>🖵</button>
        <button class="leave" title="Leave the voice" onclick={() => voice.leave()}>✕</button>
      </div>
    </div>
  {/if}
  <div class="me">
    <span class="name">{conn?.you?.name || ''}</span>
    {#if canHost && conn}
      {#if stream.hosting}
        <button class="lit" title="Stop Play Together" onclick={() => stream.stopLive()}>🎮</button>
      {:else}
        <button title="Go live for Play Together: low-latency game streaming; players join with their controllers" disabled={conn.status !== 'online'} onclick={() => stream.goLive(conn)}>🎮</button>
      {/if}
    {/if}
    <button title="Settings: voice & video, your server, this server" onclick={onsettings}>⚙</button>
  </div>
</div>

<style>
  .bar { margin: 8px -8px 0; padding: 8px; background: var(--bg); border-top: 1px solid var(--border); display: grid; gap: 8px; }
  .call { display: grid; gap: 6px; padding-bottom: 8px; border-bottom: 1px solid var(--border); }
  .where { display: grid; font-size: 12px; min-width: 0; }
  .status { color: var(--warn); font-weight: 600; }
  .status.ok { color: var(--good); }
  .room { color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .buttons { display: grid; grid-template-columns: repeat(5, 1fr); gap: 4px; }
  button { padding: 4px 0; font-size: 14px; background: var(--surface-2); min-width: 0; }
  button.on { background: var(--highlight); }
  button.lit { background: var(--good); }
  .leave { background: var(--highlight); }
  .me { display: flex; align-items: center; gap: 4px; }
  .me .name { flex: 1; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; }
  .me button { width: 34px; }
  .err { margin: 0; font-size: 12px; color: var(--highlight); display: flex; gap: 4px; align-items: flex-start; }
  .err button { background: none; padding: 0 4px; width: auto; }
</style>
