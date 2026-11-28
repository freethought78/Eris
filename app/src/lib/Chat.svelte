<script>
  // A room's text chat (DESIGN.md 4.3): history that loads as you scroll up,
  // sending with Enter (Shift+Enter for a new line), files by button, drag
  // and drop or paste (pictures show in place, others as a download card),
  // and editing or deleting your own messages. A private room you can't go
  // in asks for its password, or says it's for listed members.
  import { tick } from 'svelte'
  import { voice, attach } from './voice.svelte.js'
  import { stream } from './stream.svelte.js'
  import Maximize from '@lucide/svelte/icons/maximize'
  import Minimize from '@lucide/svelte/icons/minimize'

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

  // The room's videos: cameras, the share we watch, and our own share's
  // preview. One can be big (clicked, or the share just watched); the rest
  // are tiles. The big one's height is dragged and kept on this PC.
  let videos = $derived([
    ...(voice.stage ? [{ key: 'share:' + voice.stage.id, kind: 'share', label: `${voice.stage.name}'s ${voice.stage.game ? 'game' : 'screen'}`, track: voice.stage.track }] : []),
    ...(voice.myShare ? [{ key: 'share:me', kind: 'share', local: true, label: voice.game ? 'Your game (what spectators see)' : 'Your screen (what others see)', track: voice.myShare.track }] : []),
    ...voice.tiles.map((t) => ({ key: 'cam:' + t.id + (t.local ? ':me' : ''), kind: 'cam', label: t.local ? `${t.name} (you)` : t.name, track: t.track, mirror: t.local })),
  ])
  let focus = $state('') // the big one's key; '' = the watched share; 'none' = none
  let big = $derived(
    focus === 'none' ? null : videos.find((v) => v.key === focus) || (voice.stage ? videos.find((v) => v.key === 'share:' + voice.stage.id) : null),
  )
  $effect(() => {
    if (voice.watching) focus = '' // a share just watched becomes the big one
  })
  let stageH = $state(Number(localStorage.getItem('eris-stage-height')) || 380)

  function startResize(e) {
    const y0 = e.clientY
    const h0 = stageH
    const move = (ev) => (stageH = Math.max(140, Math.min(window.innerHeight * 0.85, h0 + ev.clientY - y0)))
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      try {
        localStorage.setItem('eris-stage-height', String(Math.round(stageH)))
      } catch {}
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  function full(el) {
    if (document.fullscreenElement) document.exitFullscreen()
    else el?.requestFullscreen()
  }

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

  // addFiles uploads files and sends them straight away (with whatever is
  // typed), as one message; a file that fails stays shown with its error.
  async function addFiles(files) {
    const batch = files.map((f) => ({ key: Math.random(), name: f.name, info: null, error: '' }))
    pending = [...pending, ...batch]
    await Promise.all(
      files.map(async (f, i) => {
        try {
          const info = await conn.upload(f)
          batch[i].info = info
          pending = pending.map((x) => (x.key === batch[i].key ? { ...x, info } : x))
        } catch (err) {
          console.error(`upload failed: ${f.name} (${f.size} bytes): ${err.message}`)
          pending = pending.map((x) => (x.key === batch[i].key ? { ...x, error: err.message } : x))
        }
      }),
    )
    const ids = batch.filter((b) => b.info).map((b) => b.info.id)
    if (ids.length) {
      conn.sendMessage(room.id, text, ids)
      text = ''
      atBottom = true
    }
    const sent = new Set(batch.filter((b) => b.info).map((b) => b.key))
    pending = pending.filter((x) => !sent.has(x.key))
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
  const isAudio = (f) => /^audio\/(mpeg|mp3|ogg|wav|x-wav|wave|mp4|x-m4a|aac|flac|x-flac|webm|opus)$/.test(f.type)
  const isVideo = (f) => /^video\/(mp4|webm|ogg)$/.test(f.type)
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
          {#if s.game}
            <span>🎮 <b>{s.local ? 'You are' : `${s.name} is`}</b> sharing {s.local ? 'your' : 'their'} game {#if s.local}<span class="muted">for anyone who wants to watch</span>{/if}</span>
          {:else}
            <span>📺 <b>{s.local ? 'You are' : `${s.name} is`}</b> sharing {s.local ? 'your' : 'their'} screen</span>
          {/if}
          {#if s.local && s.game}
            <button onclick={() => voice.stopGame()} title="Stop showing your game in the call (Play Together goes on)">Stop sharing</button>
          {:else if s.local}
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
          {:else if stream.playing?.hostId === m.id && stream.playing.state === 'asking'}
            <span class="muted small">Waiting for {m.name} to let you in...</span>
            <button onclick={() => stream.leave()}>Cancel</button>
          {:else if stream.playing?.hostId === m.id}
            <button onclick={() => stream.leave()}>Leave</button>
          {:else}
            <button class="primary" onclick={() => stream.join(conn, m.id)} title="Asks {m.name} to let you play: their game opens in a window and your controller plays">Ask to play</button>
          {/if}
        </div>
        {#if m.id === conn.you?.id && stream.players.length}
          <div class="card players">
            {#each stream.players as p, i (p.id)}
              <span class="player">🎮 <b>{p.name}</b> <span class="muted small">player {i + 2}</span>
                <button onclick={() => stream.takeBack(p.id)} title="Unplugs their controller and closes their game window">Take controller back</button></span>
            {/each}
          </div>
        {/if}
      {/each}
    </div>
  {/if}
  {#if voice.isIn(conn, room.id) && videos.length}
    <div class="media">
      {#if big}
        <div class="stage" style="height: {stageH}px" bind:this={stageEl} ondblclick={() => full(stageEl)} role="presentation">
          <div class="screen" class:mirror={big.mirror} use:attach={big.track}></div>
          <div class="stagebar">
            <span>{big.label}</span>
            <span class="grow"></span>
            <button title="Fullscreen (or double-click)" onclick={() => full(stageEl)}><Maximize size={15} /></button>
            {#if big.kind === 'share' && !big.local}<button onclick={() => voice.unwatch()}>Stop watching</button>{/if}
            <button title="Back to small" onclick={() => (focus = 'none')}><Minimize size={15} /></button>
          </div>
        </div>
        <div class="resize" title="Drag to resize" onpointerdown={startResize} role="separator" aria-orientation="horizontal"></div>
      {/if}
      <div class="tiles">
        {#each videos.filter((v) => v.key !== big?.key) as v (v.key)}
          <div class="tile" class:share={v.kind === 'share'}>
            <button class="pick" title="Click to enlarge, double-click for fullscreen" onclick={() => (focus = v.key)} ondblclick={(e) => full(e.currentTarget.parentElement)}>
              <div class="video" class:mirror={v.mirror} use:attach={v.track}></div>
            </button>
            <span class="tname">{v.label}</span>
            <button class="tfull" title="Fullscreen" onclick={(e) => full(e.currentTarget.parentElement)}><Maximize size={13} /></button>
          </div>
        {/each}
      </div>
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
            {:else if isAudio(f)}
              <div class="file media">
                <div class="fname">{f.name} <span class="muted small">{size(f.size)}</span></div>
                <div class="row2">
                  <audio controls preload="metadata" src={conn.fileUrl(f.id)}></audio>
                  <button onclick={() => window.eris.download(conn.fileUrl(f.id))}>Save</button>
                </div>
              </div>
            {:else if isVideo(f)}
              <div class="file media">
                <video controls preload="metadata" src={conn.fileUrl(f.id)}><track kind="captions" /></video>
                <div class="row2"><span class="fname">{f.name} <span class="muted small">{size(f.size)}</span></span>
                  <button onclick={() => window.eris.download(conn.fileUrl(f.id))}>Save</button></div>
              </div>
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
  .card.players { justify-content: flex-start; flex-wrap: wrap; gap: 14px; }
  .player { display: inline-flex; align-items: center; gap: 6px; }
  .media { padding: 10px 18px 0; display: grid; gap: 6px; }
  .stage { background: #000; border-radius: var(--radius); overflow: hidden; display: flex; flex-direction: column; min-height: 140px; }
  /* The whole picture always shows, with bars where its shape differs from
     the space (Discord-style): the video fills the area exactly and fits
     the picture inside it. (As a grid item it took the picture's own
     height and the bottom got cut off.) */
  .stage .screen { flex: 1; min-height: 0; position: relative; }
  .stage .screen :global(video) { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: contain; display: block; }
  .stage:fullscreen { height: 100vh !important; }
  .stagebar { display: flex; gap: 6px; align-items: center; padding: 4px 8px; background: rgba(255, 255, 255, 0.06); font-size: 13px; }
  .stagebar button { padding: 3px 8px; font-size: 12px; display: inline-grid; place-items: center; }
  .resize { height: 8px; margin: -3px 0; cursor: ns-resize; border-radius: 4px; background: linear-gradient(var(--border), var(--border)) center / 40px 3px no-repeat; }
  .resize:hover { background-color: var(--surface-2); }
  .mirror :global(video) { transform: scaleX(-1); }
  .tiles { display: flex; gap: 8px; overflow-x: auto; }
  .tile { position: relative; width: 220px; aspect-ratio: 16 / 9; background: #000; border-radius: var(--radius); overflow: hidden; flex: none; }
  .tile.share { outline: 1px solid var(--border); }
  .tile .pick { position: absolute; inset: 0; padding: 0; border: none; background: none; cursor: zoom-in; }
  .tile .video { width: 100%; height: 100%; position: relative; }
  .tile :global(video) { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; display: block; }
  .tile.share :global(video) { object-fit: contain; }
  .tile:fullscreen :global(video) { object-fit: contain; }
  .tname { position: absolute; left: 6px; bottom: 6px; background: rgba(0, 0, 0, 0.6); padding: 1px 6px; border-radius: 4px; font-size: 12px; pointer-events: none; }
  .tfull { position: absolute; right: 4px; top: 4px; padding: 3px; display: none; place-items: center; background: rgba(0, 0, 0, 0.6); border: none; }
  .tile:hover .tfull { display: grid; }
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
  .file.media { display: grid; gap: 6px; max-width: 440px; }
  .file.media .row2 { display: flex; align-items: center; gap: 8px; }
  .file.media audio { width: 320px; height: 36px; }
  .file.media video { width: 100%; max-height: 320px; border-radius: 4px; background: #000; }
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
