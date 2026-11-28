<script>
  // Picking what to share: a whole screen or one window, with thumbnails.
  // The main process asks for this (main/main.js, shareHandler).
  import { onDestroy } from 'svelte'

  let ask = $state(null) // {n, sources, canAudio}
  let picked = $state('')
  let audio = $state(true)

  const off = window.eris.onPickShare((a) => {
    ask = a
    picked = a.sources.find((s) => s.screen)?.id || a.sources[0]?.id || ''
  })
  onDestroy(off)

  function answer(id) {
    window.eris.sharePicked(ask.n, id, ask.canAudio && audio)
    ask = null
  }

  let screens = $derived(ask ? ask.sources.filter((s) => s.screen) : [])
  let windows = $derived(ask ? ask.sources.filter((s) => !s.screen) : [])
</script>

{#if ask}
  <div class="backdrop">
    <div class="dialog">
      <h3>Share your screen</h3>
      {#each [['Screens', screens], ['Windows', windows]] as [title, list]}
        {#if list.length}
          <h4>{title}</h4>
          <div class="grid">
            {#each list as s (s.id)}
              <button class="src" class:sel={picked === s.id} onclick={() => (picked = s.id)} ondblclick={() => answer(s.id)}>
                <img src={s.thumb} alt="" />
                <span>{s.name}</span>
              </button>
            {/each}
          </div>
        {/if}
      {/each}
      <p class="muted small">Only people in the room who press <b>Watch</b> receive it.</p>
      <div class="row">
        {#if ask.canAudio}<label class="opt"><input type="checkbox" bind:checked={audio} /> Share sound too</label>{/if}
        <span class="grow"></span>
        <button onclick={() => answer('')}>Cancel</button>
        <button class="primary" disabled={!picked} onclick={() => answer(picked)}>Share</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.6); display: grid; place-items: center; z-index: 8; }
  .dialog { background: var(--surface); border: 1px solid var(--border); border-radius: 10px; padding: 20px; width: min(760px, 92vw); max-height: 86vh; overflow-y: auto; display: grid; gap: 10px; }
  h3 { margin: 0; }
  h4 { margin: 6px 0 0; font-size: 12px; text-transform: uppercase; letter-spacing: 0.06em; color: var(--muted); }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 10px; }
  .src { display: grid; gap: 6px; padding: 8px; background: var(--bg); border: 2px solid var(--border); text-align: left; }
  .src.sel { border-color: var(--highlight); }
  .src img { width: 100%; aspect-ratio: 16 / 9; object-fit: contain; background: #000; border-radius: 4px; }
  .src span { font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .small { font-size: 12px; margin: 0; }
  .row { display: flex; gap: 8px; align-items: center; }
  .grow { flex: 1; }
  .opt { display: flex; gap: 6px; align-items: center; font-size: 13px; }
  .opt input { width: auto; }
</style>
