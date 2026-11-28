<script>
  // Voice & video settings (a section of Settings): which microphone,
  // speakers and camera Eris uses, and how much noise is taken out of your
  // microphone (kept on this PC). Echo cancellation is on.
  import { onMount } from 'svelte'
  import { voice } from './voice.svelte.js'

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

  // Noise reduction on your microphone (what others hear).
  const noiseModes = [
    ['strong', 'Strong', 'Removes keyboards, fans, traffic and room noise, keeping your voice. Music and game sound near your microphone are removed too.'],
    ['standard', 'Standard', 'Light: takes out steady hum. Uses the least of your PC.'],
    ['off', 'Off', 'Your microphone as it is: for a good microphone, an instrument, or a noise filter you already use (NVIDIA Broadcast, NoiseTorch...).'],
  ]

  const kinds = [
    ['audioinput', 'Microphone'],
    ['audiooutput', 'Speakers or headphones'],
    ['videoinput', 'Camera'],
  ]
</script>

<div class="devices">
  <h2>Voice & video</h2>
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
      <label>
        Noise reduction
        <select value={voice.noise} onchange={(e) => voice.setNoise(e.target.value)}>
          {#each noiseModes as [value, name] (value)}
            <option {value}>{name}</option>
          {/each}
        </select>
        <span class="muted small hint">{noiseModes.find(([v]) => v === voice.noise)?.[2]}</span>
      </label>
      <p class="muted small">Echo cancellation is on. Changes apply straight away, also in a call.</p>
    {/if}
</div>

<style>
  /* Only as tall as its contents: in Settings' full-height area the grid
     otherwise spread the extra height over its rows, stretching the menus
     and leaving each title nearer the menu above (Dev Notes). */
  .devices { display: grid; gap: 22px; max-width: 460px; align-content: start; align-self: start; flex: 1; }
  h2 { margin: 0; font-size: 18px; text-transform: none; letter-spacing: 0; color: var(--text); }
  /* Each title sits right on top of its own menu, well apart from the one above. */
  label { display: grid; gap: 6px; align-content: start; font-size: 13px; font-weight: 600; }
  select { font: inherit; font-weight: 400; color: var(--text); background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); padding: 6px 8px; }
  .small { font-size: 12px; margin: 0; }
  .hint { font-weight: 400; }
</style>
