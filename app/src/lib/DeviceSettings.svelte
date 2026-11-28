<script>
  // Audio and video settings: which microphone, speakers and camera Eris
  // uses (kept on this PC). Echo cancellation and noise suppression are on.
  import { onMount } from 'svelte'
  import { voice } from './voice.svelte.js'

  let { onclose } = $props()
  let devices = $state(null)

  onMount(async () => {
    devices = await voice.listDevices()
  })

  // The device in use: the one picked here, else the system's default (or,
  // for cameras, which have no default entry, the first).
  function chosen(kind) {
    const list = devices?.[kind] || []
    const picked = voice.devices[kind]
    if (picked && list.some((d) => d.deviceId === picked)) return picked
    return list.find((d) => d.deviceId === 'default')?.deviceId || list[0]?.deviceId || 'default'
  }

  const kinds = [
    ['audioinput', 'Microphone'],
    ['audiooutput', 'Speakers or headphones'],
    ['videoinput', 'Camera'],
  ]
</script>

<div class="backdrop">
  <div class="dialog">
    <h3>Audio and video</h3>
    {#if !devices}
      <p class="muted">Looking for your devices...</p>
    {:else}
      {#each kinds as [kind, label]}
        <label>
          {label}
          <select value={chosen(kind)} onchange={(e) => voice.setDevice(kind, e.target.value)}>
            {#if !devices[kind].length}<option value="default">None found</option>{/if}
            {#each devices[kind] as d (d.deviceId)}
              <option value={d.deviceId}>{d.label || (d.deviceId === 'default' ? 'Default' : kind)}</option>
            {/each}
          </select>
        </label>
      {/each}
      <p class="muted small">Echo cancellation and noise suppression are on. Changes apply straight away, also in a call.</p>
    {/if}
    <div class="row"><button class="primary" onclick={onclose}>Done</button></div>
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.55); display: grid; place-items: center; z-index: 5; }
  .dialog { background: var(--surface); border: 1px solid var(--border); border-radius: 10px; padding: 20px; width: 420px; display: grid; gap: 12px; }
  h3 { margin: 0 0 4px; }
  label { display: grid; gap: 4px; font-size: 13px; }
  select { font: inherit; color: var(--text); background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); padding: 6px 8px; }
  .small { font-size: 12px; margin: 0; }
  .row { display: flex; justify-content: flex-end; }
</style>
