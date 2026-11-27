<script>
  // Adding or changing a room (owner and admins): its name, and whether it's
  // open, private with a password, or private for picked members.
  let { conn, room = null, onclose } = $props()

  let name = $state(room?.name || '')
  let privacy = $state(room?.private || '')
  let password = $state('')
  let allowed = $state(new Set(room?.allowed || []))
  let error = $state('')

  let others = $derived(conn.members.filter((m) => m.role === 'member'))

  function save(e) {
    e.preventDefault()
    error = ''
    if (!name.trim()) return (error = 'Give the room a name.')
    if (privacy === 'password' && !password && room?.private !== 'password') return (error = 'Set a password for the room.')
    const settings = { name: name.trim(), private: privacy, password: privacy === 'password' ? password : '', allowed: [...allowed] }
    if (room) conn.updateRoom(room.id, settings)
    else conn.createRoom(settings)
    onclose()
  }

  function remove() {
    if (confirm(`Delete #${room.name} with all its messages and files? This can't be undone.`)) {
      conn.deleteRoom(room.id)
      onclose()
    }
  }

  function toggle(id) {
    const s = new Set(allowed)
    s.has(id) ? s.delete(id) : s.add(id)
    allowed = s
  }
</script>

<div class="backdrop">
  <form class="dialog" onsubmit={save}>
    <h3>{room ? `Room settings: #${room.name}` : 'Add a room'}</h3>
    <label>Name <input bind:value={name} maxlength="32" autofocus /></label>
    <fieldset>
      <label class="opt"><input type="radio" bind:group={privacy} value="" /> Open to everyone on the server</label>
      <label class="opt"><input type="radio" bind:group={privacy} value="password" /> Private, with a password</label>
      <label class="opt"><input type="radio" bind:group={privacy} value="list" /> Private, for members I pick</label>
    </fieldset>
    {#if privacy === 'password'}
      <label>
        {room?.private === 'password' ? 'New password (empty keeps the current one)' : 'Password'}
        <input type="password" bind:value={password} />
      </label>
      <p class="muted small">Whoever gives the password once is in from then on. A new password locks everyone out until they give it.</p>
    {:else if privacy === 'list'}
      <div class="list">
        {#each others as m (m.id)}
          <label class="opt"><input type="checkbox" checked={allowed.has(m.id)} onchange={() => toggle(m.id)} /> {m.name}</label>
        {:else}
          <p class="muted small">No members yet besides the owner and admins (who can always go in).</p>
        {/each}
      </div>
    {/if}
    {#if error}<p class="error">{error}</p>{/if}
    <div class="row">
      {#if room}<button type="button" class="danger" onclick={remove}>Delete room</button>{/if}
      <span class="grow"></span>
      <button type="button" onclick={onclose}>Cancel</button>
      <button class="primary">{room ? 'Save' : 'Add room'}</button>
    </div>
  </form>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.55); display: grid; place-items: center; z-index: 5; }
  .dialog { background: var(--surface); border: 1px solid var(--border); border-radius: 10px; padding: 20px; width: 400px; display: grid; gap: 12px; }
  h3 { margin: 0 0 4px; }
  label { display: grid; gap: 4px; font-size: 13px; }
  fieldset { border: none; padding: 0; margin: 0; display: grid; gap: 6px; }
  .opt { display: flex; align-items: center; gap: 8px; font-size: 14px; }
  .opt input { width: auto; }
  .list { display: grid; gap: 6px; max-height: 200px; overflow-y: auto; }
  .small { font-size: 12px; margin: 0; }
  .row { display: flex; gap: 8px; align-items: center; }
  .grow { flex: 1; }
  .danger { background: none; border-color: var(--highlight); color: var(--highlight); }
  p { margin: 0; }
</style>
