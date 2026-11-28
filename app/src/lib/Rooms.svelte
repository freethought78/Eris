<script>
  // The rooms list: every room of the server, a lock on private ones, a dot
  // on rooms with unread messages, and under each room who's in its voice
  // (with a light while they speak, when you're in that voice too).
  // Double-click a room (or its speaker button) to join its voice. The owner
  // and admins add and change rooms. The voice bar sits at the bottom.
  import { voice } from './voice.svelte.js'
  import VoiceBar from './VoiceBar.svelte'
  import Volume from '@lucide/svelte/icons/volume-2'
  import Settings from '@lucide/svelte/icons/settings'
  import MicOff from '@lucide/svelte/icons/mic-off'
  import Video from '@lucide/svelte/icons/video'
  import MonitorUp from '@lucide/svelte/icons/monitor-up'

  let { conn, selected, onselect, onedit, oncreate, onsettings, canHost = false } = $props()

  let staff = $derived(conn.you?.role === 'owner' || conn.you?.role === 'admin')
  let volumeFor = $state(null) // member id whose volume slider is open

  function inVoice(roomId) {
    return Object.entries(conn.voice)
      .filter(([, rid]) => rid === roomId)
      .map(([mid]) => ({ id: Number(mid), name: conn.member(Number(mid))?.name || '?' }))
      .sort((a, b) => a.name.localeCompare(b.name))
  }

  // What the call itself says about someone (speaking, muted), when we're in it.
  function live(roomId, memberId) {
    if (!voice.isIn(conn, roomId)) return null
    return voice.people.find((p) => p.id === String(memberId)) || null
  }

  function join(r) {
    onselect(r.id)
    if (r.access && conn.voiceOn) voice.join(conn, r.id)
  }
</script>

<div class="rooms">
  <div class="head">
    <span>Rooms</span>
    {#if staff}<button class="add" title="Add a room" onclick={oncreate}>+</button>{/if}
  </div>
  {#each conn.rooms as r (r.id)}
    <div class="room" class:sel={r.id === selected} class:unread={conn.unread(r) && r.id !== selected}>
      <button
        class="name"
        onclick={() => onselect(r.id)}
        ondblclick={() => join(r)}
        title={(r.private === 'password' ? 'Private: password. ' : r.private === 'list' ? 'Private: members list. ' : '') + (conn.voiceOn ? 'Double-click to join its voice.' : '')}
      >
        <span class="mark">{r.private ? (r.access ? '🔓' : '🔒') : '#'}</span>
        <span class="text">{r.name}</span>
        {#if conn.unread(r) && r.id !== selected}<span class="dot"></span>{/if}
      </button>
      {#if conn.voiceOn && r.access && !voice.isIn(conn, r.id)}
        <button class="hover" title="Join the voice" onclick={() => join(r)}><Volume size={15} /></button>
      {/if}
      {#if staff}<button class="hover gear" title="Room settings" onclick={() => onedit(r)}><Settings size={14} /></button>{/if}
    </div>
    {#each inVoice(r.id) as m (m.id)}
      {@const l = live(r.id, m.id)}
      <div class="talker" class:speaking={l?.speaking}>
        <span class="light"></span>
        {#if l && !l.local}
          <button class="who" title="Their volume" onclick={() => (volumeFor = volumeFor === m.id ? null : m.id)}>{m.name}</button>
        {:else}
          <span class="who">{m.name}{m.id === conn.you?.id ? ' (you)' : ''}</span>
        {/if}
        {#if l?.muted}<span class="icon" title="Muted"><MicOff size={13} /></span>{/if}
        {#if l?.camera}<span class="icon" title="Camera on"><Video size={13} /></span>{/if}
        {#if l?.sharing}<span class="icon" title="Sharing their screen"><MonitorUp size={13} /></span>{/if}
      </div>
      {#if volumeFor === m.id && l}
        <label class="volume">
          <input type="range" min="0" max="3" step="0.05" value={voice.volume(m.id)} oninput={(e) => voice.setVolume(m.id, Number(e.target.value))} title="Up to 300%; a limiter keeps loud moments from crackling" />
          <span>{Math.round(voice.volume(m.id) * 100)}%</span>
        </label>
      {/if}
    {/each}
  {/each}
  <div class="spacer"></div>
  <VoiceBar {onsettings} {conn} {canHost} />
</div>

<style>
  .rooms { width: 230px; background: var(--surface); border-right: 1px solid var(--border); display: flex; flex-direction: column; padding: 10px 8px 0; gap: 2px; overflow-y: auto; }
  .head { display: flex; align-items: center; justify-content: space-between; color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: 0.06em; padding: 4px 6px 8px; }
  .add { background: none; border: none; padding: 0 4px; font-size: 18px; line-height: 1; color: var(--muted); }
  .room { display: flex; align-items: center; border-radius: var(--radius); }
  .room:hover { background: var(--surface-2); }
  .room.sel { background: var(--accent); }
  .name { flex: 1; display: flex; align-items: center; gap: 6px; background: none; border: none; text-align: left; padding: 6px 8px; color: var(--muted); min-width: 0; }
  .room.sel .name, .room.unread .name { color: var(--text); }
  .room.unread .text { font-weight: 600; }
  .mark { width: 16px; text-align: center; font-size: 12px; opacity: 0.8; }
  .text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--text); margin-left: auto; flex: none; }
  .hover { background: none; border: none; padding: 4px 6px; color: var(--muted); visibility: hidden; display: grid; place-items: center; }
  .room:hover .hover, .room.sel .gear { visibility: visible; }
  .talker { display: flex; align-items: center; gap: 6px; padding: 2px 8px 2px 30px; font-size: 13px; color: var(--muted); }
  .light { width: 8px; height: 8px; border-radius: 50%; background: var(--border); flex: none; }
  .talker.speaking .light { background: var(--good); box-shadow: 0 0 6px var(--good); }
  .talker.speaking .who { color: var(--text); }
  .who { background: none; border: none; padding: 0; color: inherit; font: inherit; text-align: left; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  button.who:hover { text-decoration: underline; }
  .icon { display: grid; place-items: center; color: var(--muted); }
  .volume { display: flex; align-items: center; gap: 6px; padding: 2px 8px 6px 44px; font-size: 11px; color: var(--muted); }
  .volume input { width: 110px; padding: 0; }
  .spacer { flex: 1; }
</style>
