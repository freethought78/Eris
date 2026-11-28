<script>
  // A room's text chat (DESIGN.md 4.3): history that loads as you scroll up,
  // sending with Enter (Shift+Enter for a new line), files by button, drag
  // and drop or paste (pictures show in place, others as a download card),
  // and editing or deleting your own messages. A private room you can't go
  // in asks for its password, or says it's for listed members.
  import { tick } from 'svelte'
  import { voice, attach } from './voice.svelte.js'
  import { stream } from './stream.svelte.js'

  let { conn, room } = $props()

  let text = $state('')
  let pending = $state([]) // files uploading or ready: {key, name, info, error}
  let editing = $state(null) // message id
  let editText = $state('')
  let password = $state('')
  let dragging = $state(false)
  let list = $state(null)
  let input = $state(null)
  let picker = $state(null)
  let stageEl = $state(null)

  // Members live for Play Together who are in this room's voice (and us).
  let liveHere = $derived(
    Object.keys(conn.live)
      .map(Number)
      .filter((id) => conn.voice[id] === room.id || id === conn.you?.id)
      .map((id) => ({ id, name: conn.member(id)?.name || '?' })),
  )
  let preview = $state(null) // image url shown large

  let chat = $derived(conn.chat[room.id])
  let messages = $derived(chat?.messages || [])
  let staff = $derived(conn.you?.role === 'owner' || conn.you?.role === 'admin')
  let uploading = $derived(pending.some((p) => !p.info && !p.error))

  // Load the room when it's shown (and after a reconnect cleared it).
  $effect(() => {
    if (room.access && !conn.chat[room.id]) conn.loadMore(room.id).then(() => toBottom())
  })

  // Stay at the bottom as messages arrive, if we were there.
  let atBottom = true
  $effect.pre(() => {
    messages.length
    if (list) atBottom = list.scrollTop + list.clientHeight >= list.scrollHeight - 40
  })
  $effect(() => {
    messages.length
    if (atBottom) toBottom()
    if (document.hasFocus()) conn.markRead(room.id)
  })

  async function toBottom() {
    await tick()
    if (list) list.scrollTop = list.scrollHeight
    conn.markRead(room.id)
  }

  async function scrolled() {
    if (!list || list.scrollTop > 60 || chat?.done || chat?.loading) return
    const before = list.scrollHeight
    await conn.loadMore(room.id)
    await tick()
    list.scrollTop += list.scrollHeight - before
  }

  function send() {
    if (uploading) return
    const files = pending.filter((p) => p.info).map((p) => p.info.id)
    if (!text.trim() && files.length === 0) return
    conn.sendMessage(room.id, text, files)
    text = ''
    pending = []
    atBottom = true
  }

  function keydown(e) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    } else if (e.key === 'ArrowUp' && !text) {
      const mine = [...messages].reverse().find((m) => m.member_id === conn.you?.id)
      if (mine) startEdit(mine)
    }
  }

  async function addFiles(files) {
    for (const f of files) {
      const p = { key: Math.random(), name: f.name, info: null, error: '' }
      pending = [...pending, p]
      try {
        const info = await conn.upload(f)
        pending = pending.map((x) => (x.key === p.key ? { ...x, info } : x))
      } catch (err) {
        pending = pending.map((x) => (x.key === p.key ? { ...x, error: err.message } : x))
      }
    }
  }

  function drop(e) {
    e.preventDefault()
    dragging = false
    if (room.access && e.dataTransfer?.files?.length) addFiles([...e.dataTransfer.files])
  }

  function paste(e) {
    const files = [...(e.clipboardData?.files || [])]
    if (files.length) {
      e.preventDefault()
      addFiles(files)
    }
  }

  function startEdit(m) {
    editing = m.id
    editText = m.text
  }

  function saveEdit(m) {
    if (editText.trim() !== m.text) conn.editMessage(m.id, editText)
    editing = null
  }

  function remove(m) {
    if (confirm('Delete this message' + (m.files.length ? ' and its files' : '') + '?')) conn.deleteMessage(m.id)
  }

  function unlock(e) {
    e.preventDefault()
    conn.error = ''
    if (password) conn.unlock(room.id, password)
    password = ''
  }

  // Text with web links made clickable (they open in the browser).
  function parts(t) {
    const out = []
    let last = 0
    for (const m of t.matchAll(/https?:\/\/[^\s<>"]+[^\s<>".,;:!?)\]]/g)) {
      if (m.index > last) out.push({ text: t.slice(last, m.index) })
      out.push({ link: m[0] })
      last = m.index + m[0].length
    }
    if (last < t.length) out.push({ text: t.slice(last) })
    return out
  }

  const isImage = (f) => /^image\/(png|jpeg|gif|webp)$/.test(f.type)
  const size = (n) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${(n / 1024).toFixed(0)} KB` : `${(n / (1 << 20)).toFixed(1)} MB`)

  function when(ms) {
    const d = new Date(ms)
    const t = d.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
    return d.toDateString() === new Date().toDateString() ? t : `${d.toLocaleDateString()} ${t}`
  }

  // A message starts a new group when someone else wrote the one before, or
  // it was more than 5 minutes ago.
  const starts = (i) => i === 0 || messages[i - 1].member_id !== messages[i].member_id || messages[i].at - messages[i - 1].at > 5 * 60 * 1000
</script>

<section
  class="chat"
  aria-label={`#${room.name}`}
  ondragover={(e) => (e.preventDefault(), room.access && (dragging = true))}
  ondragleave={(e) => e.currentTarget === e.target && (dragging = false)}
  ondrop={drop}
>
  <header>
    <span class="mark">{room.private ? (room.access ? '🔓' : '🔒') : '#'}</span>
    <h2>{room.name}</h2>
    {#if room.private}<span class="muted">{room.private === 'password' ? 'private, with a password' : 'private, for listed members'}</span>{/if}
    <span class="grow"></span>
    {#if conn.voiceOn && room.access}
      {#if voice.isIn(conn, room.id)}
        <span class="invoice">In this room's voice</span>
      {:else}
        <button class="primary joinvoice" onclick={() => voice.join(conn, room.id)}>Join voice</button>
      {/if}
    {/if}
  </header>
  {#if voice.isIn(conn, room.id) && (voice.shares.length || liveHere.length)}
    <div class="cards">
      {#each voice.shares as s (s.id)}
        <div class="card">
          <span>📺 <b>{s.local ? 'You are' : `${s.name} is`}</b> sharing {s.local ? 'your' : 'their'} screen</span>
          {#if s.local}
            <button onclick={() => voice.toggleShare()}>Stop sharing</button>
          {:else if voice.watching === s.id}
            <button onclick={() => voice.unwatch()}>Stop watching</button>
          {:else}
            <button class="primary" onclick={() => voice.watch(s.id)}>Watch</button>
          {/if}
        </div>
      {/each}
      {#each liveHere as m (m.id)}
        <div class="card">
          <span>🎮 <b>{m.id === conn.you?.id ? 'You are' : `${m.name} is`}</b> live for Play Together <span class="muted">{conn.live[m.id]?.title || ''}</span></span>
          {#if m.id === conn.you?.id}
            <button onclick={() => stream.stopLive()}>Stop</button>
          {:else if stream.playing?.hostId === m.id}
            <button onclick={() => stream.leave()}>Leave</button>
          {:else}
            <button class="primary" onclick={() => stream.join(conn, m.id)} title="Opens their game in a window; your controller plays">Join</button>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
  {#if voice.isIn(conn, room.id) && voice.stage}
    <div class="stage" bind:this={stageEl}>
      <div class="screen" use:attach={voice.stage.track}></div>
      <div class="stagebar">
        <span>{voice.stage.name}'s screen</span>
        <span class="grow"></span>
        <button onclick={() => stageEl.requestFullscreen()}>Fullscreen</button>
        <button onclick={() => voice.unwatch()}>Stop watching</button>
      </div>
    </div>
  {/if}
  {#if voice.isIn(conn, room.id) && voice.tiles.length}
    <div class="tiles">
      {#each voice.tiles as t (t.id + (t.local ? '-me' : ''))}
        <div class="tile" class:me={t.local}>
          <div class="video" use:attach={t.track}></div>
          <span class="tname">{t.name}{t.local ? ' (you)' : ''}</span>
        </div>
      {/each}
    </div>
  {/if}

  {#if !room.access}
    <div class="locked">
      {#if room.private === 'password'}
        <p>This room has a password. Give it once and you're in from then on.</p>
        <form onsubmit={unlock}>
          <input type="password" bind:value={password} placeholder="Room password" autofocus />
          <button class="primary">Go in</button>
        </form>
      {:else}
        <p>This room is for the members the owner picked. Ask them to add you.</p>
      {/if}
    </div>
  {:else}
    <div class="messages" bind:this={list} onscroll={scrolled}>
      {#if chat?.done}
        <p class="start muted">This is the start of #{room.name}.</p>
      {:else if chat?.loading}
        <p class="start muted">Loading...</p>
      {/if}
      {#each messages as m, i (m.id || i)}
        <div class="msg" class:first={starts(i)}>
          {#if starts(i)}
            <div class="who"><b>{m.member}</b> <span class="muted">{when(m.at)}</span></div>
          {/if}
          {#if editing === m.id}
            <textarea
              class="edit"
              bind:value={editText}
              onkeydown={(e) => (e.key === 'Enter' && !e.shiftKey ? (e.preventDefault(), saveEdit(m)) : e.key === 'Escape' && (editing = null))}
              autofocus
            ></textarea>
            <div class="muted small">Enter to save, Escape to cancel</div>
          {:else if m.text}
            <div class="text">
              {#each parts(m.text) as p}{#if p.link}<a href={p.link} target="_blank" rel="noreferrer">{p.link}</a>{:else}{p.text}{/if}{/each}{#if m.edited}<span class="muted small"> (edited)</span>{/if}
            </div>
          {/if}
          {#each m.files as f (f.id)}
            {#if isImage(f)}
              <button class="pic" onclick={() => (preview = conn.fileUrl(f.id))} title={f.name}>
                <img src={conn.fileUrl(f.id)} alt={f.name} loading="lazy" onload={() => atBottom && toBottom()} />
              </button>
            {:else}
              <div class="file">
                <span class="icon">📄</span>
                <div><div class="fname">{f.name}</div><div class="muted small">{size(f.size)}</div></div>
                <button onclick={() => window.eris.download(conn.fileUrl(f.id))}>Save</button>
              </div>
            {/if}
          {/each}
          {#if m.id && editing !== m.id && (m.member_id === conn.you?.id || staff)}
            <div class="tools">
              {#if m.member_id === conn.you?.id}<button onclick={() => startEdit(m)} title="Edit">✎</button>{/if}
              <button onclick={() => remove(m)} title="Delete">🗑</button>
            </div>
          {/if}
        </div>
      {/each}
    </div>

    <div class="compose">
      {#if pending.length}
        <div class="pending">
          {#each pending as p (p.key)}
            <span class="chip" class:bad={p.error} title={p.error}>
              {p.error ? '⚠ ' : p.info ? '' : '⏳ '}{p.name}
              <button onclick={() => (pending = pending.filter((x) => x.key !== p.key))}>×</button>
            </span>
          {/each}
        </div>
      {/if}
      <div class="row">
        <button class="attach" title={`Attach files (up to ${conn.maxFileMB} MB each)`} onclick={() => picker.click()}>+</button>
        <input type="file" multiple hidden bind:this={picker} onchange={(e) => (addFiles([...e.target.files]), (e.target.value = ''))} />
        <textarea bind:this={input} bind:value={text} onkeydown={keydown} onpaste={paste} rows="1" placeholder={`Message #${room.name}`}></textarea>
        <button class="primary" onclick={send} disabled={uploading || (!text.trim() && !pending.some((p) => p.info))}>Send</button>
      </div>
    </div>
  {/if}

  {#if dragging}<div class="dropzone">Drop files to send them to #{room.name}</div>{/if}
  {#if preview}
    <button class="lightbox" onclick={() => (preview = null)}><img src={preview} alt="" /></button>
  {/if}
</section>

<style>
  .chat { flex: 1; display: flex; flex-direction: column; min-width: 0; position: relative; }
  header { display: flex; align-items: baseline; gap: 8px; padding: 12px 18px; border-bottom: 1px solid var(--border); }
  .grow { flex: 1; }
  .invoice { color: var(--good); font-size: 12px; font-weight: 600; }
  .joinvoice { padding: 4px 12px; font-size: 13px; }
  .cards { display: grid; gap: 6px; padding: 10px 18px 0; }
  .card { display: flex; align-items: center; gap: 10px; justify-content: space-between; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 6px 10px; }
  .card button { padding: 4px 12px; font-size: 13px; }
  .stage { margin: 10px 18px 0; background: #000; border-radius: var(--radius); overflow: hidden; display: flex; flex-direction: column; }
  .stage .screen { flex: 1; display: grid; place-items: center; min-height: 0; }
  .stage .screen :global(video) { width: 100%; max-height: 52vh; object-fit: contain; display: block; }
  .stage:fullscreen .screen :global(video) { max-height: none; height: calc(100vh - 44px); }
  .stagebar { display: flex; gap: 8px; align-items: center; padding: 6px 10px; background: rgba(255, 255, 255, 0.05); font-size: 13px; }
  .stagebar button { padding: 3px 10px; font-size: 12px; }
  .tiles { display: flex; gap: 8px; padding: 10px 18px; border-bottom: 1px solid var(--border); overflow-x: auto; background: #10101f; }
  .tile { position: relative; width: 240px; aspect-ratio: 16 / 9; background: #000; border-radius: var(--radius); overflow: hidden; flex: none; }
  .tile :global(video) { width: 100%; height: 100%; object-fit: cover; display: block; }
  .tile.me :global(video) { transform: scaleX(-1); }
  .tname { position: absolute; left: 6px; bottom: 6px; background: rgba(0, 0, 0, 0.6); padding: 1px 6px; border-radius: 4px; font-size: 12px; }
  header h2 { margin: 0; font-size: 16px; text-transform: none; letter-spacing: 0; color: var(--text); }
  .mark { color: var(--muted); }
  .messages { flex: 1; overflow-y: auto; padding: 8px 18px 12px; }
  .start { text-align: center; margin: 12px 0; }
  .msg { position: relative; padding: 2px 60px 2px 0; border-radius: var(--radius); }
  .msg.first { margin-top: 12px; }
  .msg:hover { background: rgba(255, 255, 255, 0.03); }
  .who { margin-bottom: 2px; }
  .who .muted { font-size: 12px; margin-left: 6px; }
  .text { white-space: pre-wrap; word-wrap: break-word; line-height: 1.4; user-select: text; }
  .text a { color: #6cb4ff; }
  .small { font-size: 11px; }
  .tools { position: absolute; right: 4px; top: 0; display: none; gap: 2px; }
  .msg:hover .tools { display: flex; }
  .tools button { padding: 2px 6px; font-size: 12px; background: var(--surface-2); }
  .edit { width: 100%; min-height: 60px; font: inherit; color: var(--text); background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); padding: 6px 8px; }
  .pic { display: block; padding: 0; margin: 4px 0; background: none; border: none; }
  .pic img { max-width: min(420px, 100%); max-height: 320px; border-radius: var(--radius); display: block; }
  .file { display: inline-flex; align-items: center; gap: 10px; background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius); padding: 8px 10px; margin: 4px 0; max-width: 420px; }
  .file .icon { font-size: 22px; }
  .fname { font-weight: 600; word-break: break-all; }
  .compose { padding: 0 18px 16px; }
  .pending { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 6px; }
  .chip { background: var(--surface-2); border-radius: 12px; padding: 2px 4px 2px 10px; font-size: 12px; display: inline-flex; align-items: center; gap: 4px; }
  .chip.bad { border: 1px solid var(--highlight); }
  .chip button { background: none; border: none; padding: 0 4px; }
  .row { display: flex; gap: 8px; align-items: flex-end; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 6px; }
  .row textarea { flex: 1; resize: none; max-height: 200px; min-height: 22px; font: inherit; color: var(--text); background: none; border: none; outline: none; padding: 6px 4px; field-sizing: content; }
  .attach { font-size: 18px; line-height: 1; padding: 4px 10px; }
  .locked { margin: 40px auto; max-width: 380px; text-align: center; display: grid; gap: 10px; }
  .locked form { display: flex; gap: 8px; }
  .locked button { white-space: nowrap; }
  .dropzone { position: absolute; inset: 0; background: rgba(15, 52, 96, 0.85); display: grid; place-items: center; font-size: 18px; border: 2px dashed var(--muted); pointer-events: none; }
  .lightbox { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.85); display: grid; place-items: center; border: none; border-radius: 0; z-index: 10; }
  .lightbox img { max-width: 92vw; max-height: 92vh; }
</style>
