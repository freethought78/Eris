<script>
  // The voice bar under the rooms: which room's voice you're in, with mute,
  // deafen, camera and leave; and audio and video settings.
  import { voice } from './voice.svelte.js'

  let { onsettings } = $props()

  let roomName = $derived(voice.conn?.room(voice.roomId)?.name || '')
</script>

<div class="bar">
  {#if voice.error}<p class="err" title={voice.error}>{voice.error} <button onclick={() => (voice.error = '')}>×</button></p>{/if}
  {#if voice.state}
    <div class="where">
      <span class="status" class:ok={voice.state === 'connected'}>
        {voice.state === 'connected' ? 'In voice' : voice.state === 'connecting' ? 'Connecting...' : 'Reconnecting...'}
      </span>
      <span class="room">#{roomName} · {voice.conn?.serverName || ''}</span>
    </div>
  {/if}
  <div class="buttons">
    {#if voice.state}
      <button class:on={voice.muted} title={voice.muted ? 'Unmute' : 'Mute'} onclick={() => voice.toggleMute()}>{voice.muted ? '🔇' : '🎤'}</button>
      <button class:on={voice.deafened} title={voice.deafened ? 'Undeafen' : 'Deafen (hear nobody, and mute)'} onclick={() => voice.toggleDeafen()}>{voice.deafened ? '🔕' : '🎧'}</button>
      <button class:lit={voice.camera} title={voice.camera ? 'Turn your camera off' : 'Turn your camera on'} onclick={() => voice.toggleCamera()}>📷</button>
      <button class="leave" title="Leave the voice" onclick={() => voice.leave()}>✕</button>
    {/if}
    <span class="grow"></span>
    <button title="Audio and video settings" onclick={onsettings}>⚙</button>
  </div>
</div>

<style>
  .bar { margin: 8px -8px 0; padding: 8px; background: var(--bg); border-top: 1px solid var(--border); display: grid; gap: 6px; }
  .where { display: grid; font-size: 12px; }
  .status { color: var(--warn); font-weight: 600; }
  .status.ok { color: var(--good); }
  .room { color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .buttons { display: flex; gap: 4px; align-items: center; }
  .buttons button { padding: 4px 8px; font-size: 14px; background: var(--surface-2); }
  .buttons button.on { background: var(--highlight); }
  .buttons button.lit { background: var(--good); }
  .buttons .leave { background: var(--highlight); }
  .grow { flex: 1; }
  .err { margin: 0; font-size: 12px; color: var(--highlight); display: flex; gap: 4px; align-items: flex-start; }
  .err button { background: none; border: none; padding: 0 4px; }
</style>
