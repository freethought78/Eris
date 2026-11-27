<script>
  import { join, parseLink } from './conn.svelte.js'

  let { onadded, oncancel, initial = {} } = $props()

  let address = $state(initial.address || '')
  let name = $state(initial.name || '')
  let password = $state('')
  let ownerCode = $state(initial.ownerCode || '')
  let busy = $state(false)
  let error = $state('')

  async function submit(e) {
    e?.preventDefault()
    error = ''
    const { address: addr, pin } = parseLink(address)
    if (!addr) return (error = 'Paste the join link the server owner gave you.')
    if (!name.trim()) return (error = 'Enter the name others will see.')
    busy = true
    try {
      // The link's fingerprint says which certificate is really this server's.
      await window.eris.pin(addr, pin)
      const r = await join(addr, name.trim(), password, ownerCode.trim())
      onadded({ address: addr, pin, token: r.token, name: r.member.name, role: r.member.role })
    } catch (err) {
      error = String(err.message || err)
    } finally {
      busy = false
    }
  }
</script>

<div class="backdrop">
  <form class="dialog" onsubmit={submit}>
    <h3>Add a server</h3>
    <label>Join link <input bind:value={address} placeholder="eris://myname.example.net:7300#..." autofocus /></label>
    <label>Your name <input bind:value={name} maxlength="32" /></label>
    <label>Server password <span class="muted">(if it has one)</span><input bind:value={password} type="password" /></label>
    <label>Owner code <span class="muted">(only the first time, on your own server)</span><input bind:value={ownerCode} /></label>
    {#if error}<p class="error">{error}</p>{/if}
    <div class="row">
      <button type="button" onclick={oncancel}>Cancel</button>
      <button class="primary" disabled={busy}>{busy ? 'Joining...' : 'Join'}</button>
    </div>
  </form>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.55); display: grid; place-items: center; }
  .dialog { background: var(--surface); border: 1px solid var(--border); border-radius: 10px; padding: 20px; width: 380px; display: grid; gap: 12px; }
  h3 { margin: 0 0 4px; }
  label { display: grid; gap: 4px; font-size: 13px; }
  .row { display: flex; justify-content: flex-end; gap: 8px; }
  p { margin: 0; }
</style>
