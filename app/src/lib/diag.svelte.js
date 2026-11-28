// Diagnostics for server owners (server/diag.go): what helps find problems
// in a test between two homes, sent automatically to the server it concerns.
// The app's log (its console), errors, and while in voice, connection stats
// every 10 s; Play Together sessions' summaries and helper logs. Never
// messages. Settings (About Eris) can turn it off on this PC.
import { voice } from './voice.svelte.js'

const KEY = 'eris-diagnostics'

class Diag {
  enabled = $state(true)
  #conns = new Set()
  #buf = new Map() // conn -> entries waiting
  #info = {}

  constructor() {
    try {
      this.enabled = localStorage.getItem(KEY) !== 'off'
    } catch {}
    // The app's console goes along (it's what app.log has).
    for (const level of ['info', 'warn', 'error']) {
      const orig = console[level].bind(console)
      console[level] = (...args) => {
        orig(...args)
        this.add({ kind: 'log', level, text: args.map((a) => (typeof a === 'string' ? a : safe(a))).join(' ').slice(0, 2000) })
      }
    }
    window.addEventListener('error', (e) => this.add({ kind: 'error', text: String(e.message), where: `${e.filename}:${e.lineno}` }))
    window.addEventListener('unhandledrejection', (e) => this.add({ kind: 'error', text: String(e.reason?.stack || e.reason) }))
    setInterval(() => this.#voiceStats(), 10000)
    setInterval(() => this.flush(), 10000)
  }

  setEnabled(on) {
    this.enabled = on
    try {
      localStorage.setItem(KEY, on ? 'on' : 'off')
    } catch {}
    if (!on) this.#buf.clear()
  }

  setInfo(info) {
    this.#info = { version: info.version, platform: info.platform, dev: !!info.dev }
  }

  // attach: a server connection; on each (re)connection it gets who we are.
  attach(conn) {
    this.#conns.add(conn)
    conn.on('hello', () => this.add({ kind: 'app', ...this.#info, server_version: conn.serverVersion }, conn))
  }

  detach(conn) {
    this.#conns.delete(conn)
    this.#buf.delete(conn)
  }

  // add an entry for one server (conn), or for all of them.
  add(entry, conn = null) {
    if (!this.enabled) return
    const e = { t: Date.now(), ...entry }
    for (const c of conn ? [conn] : this.#conns) {
      const b = this.#buf.get(c) || []
      b.push(e)
      if (b.length > 1000) b.splice(0, b.length - 1000)
      this.#buf.set(c, b)
    }
  }

  async flush() {
    if (!this.enabled) return
    for (const [conn, entries] of this.#buf) {
      if (!entries.length || conn.status !== 'online') continue
      this.#buf.set(conn, [])
      const r = await window.eris.api({ method: 'POST', url: conn.diagUrl(), token: conn.entry.token, body: entries })
      if (r.status !== 200 && r.status !== 404) {
        // Try again next time (a server from before diagnostics answers 404: drop).
        this.#buf.set(conn, [...entries, ...(this.#buf.get(conn) || [])].slice(-1000))
      }
    }
  }

  async #voiceStats() {
    if (!this.enabled || voice.state !== 'connected' || !voice.conn) return
    try {
      const s = await voice.stats()
      if (s) this.add({ kind: 'voice', ...s }, voice.conn)
    } catch {}
  }
}

function safe(v) {
  try {
    return JSON.stringify(v)
  } catch {
    return String(v)
  }
}

export const diag = new Diag()
