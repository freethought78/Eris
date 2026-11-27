<script>
  // The rooms list: every room of the server, a lock on private ones, a dot
  // on rooms with unread messages. The owner and admins add and change rooms.
  let { conn, selected, onselect, onedit, oncreate, view, onview } = $props()

  let staff = $derived(conn.you?.role === 'owner' || conn.you?.role === 'admin')
</script>

<div class="rooms">
  <div class="head">
    <span>Rooms</span>
    {#if staff}<button class="add" title="Add a room" onclick={oncreate}>+</button>{/if}
  </div>
  {#each conn.rooms as r (r.id)}
    <div class="room" class:sel={view === 'chat' && r.id === selected} class:unread={conn.unread(r) && r.id !== selected}>
      <button class="name" onclick={() => onselect(r.id)} title={r.private === 'password' ? 'Private: password' : r.private === 'list' ? 'Private: members list' : ''}>
        <span class="mark">{r.private ? (r.access ? '🔓' : '🔒') : '#'}</span>
        <span class="text">{r.name}</span>
        {#if conn.unread(r) && r.id !== selected}<span class="dot"></span>{/if}
      </button>
      {#if staff}<button class="gear" title="Room settings" onclick={() => onedit(r)}>⚙</button>{/if}
    </div>
  {/each}
  <div class="spacer"></div>
  {#if conn.you?.role === 'owner'}
    <button class="tests" class:sel={view === 'server'} onclick={() => onview(view === 'server' ? 'chat' : 'server')}>Server</button>
  {/if}
  <button class="tests" class:sel={view === 'tests'} onclick={() => onview(view === 'tests' ? 'chat' : 'tests')}>Stream tests</button>
</div>

<style>
  .rooms { width: 220px; background: var(--surface); border-right: 1px solid var(--border); display: flex; flex-direction: column; padding: 10px 8px; gap: 2px; overflow-y: auto; }
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
  .gear { background: none; border: none; padding: 4px 8px; color: var(--muted); visibility: hidden; }
  .room:hover .gear, .room.sel .gear { visibility: visible; }
  .spacer { flex: 1; }
  .tests { background: none; border: 1px solid var(--border); color: var(--muted); font-size: 12px; }
  .tests.sel { color: var(--text); background: var(--accent); }
</style>
