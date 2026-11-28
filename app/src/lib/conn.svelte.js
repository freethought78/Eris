// One connection to one server: who's there, who's live, test results, and
// the live WebSocket (DESIGN.md 3.4). Reconnects by itself (the server may
// restart, after an update for example). Before each connection it asks the
// server's version: if the server is newer than this app, it stops there and
// suggests updating the app first ("outdated"); the user can connect anyway.
import { newer } from './version.js'

// Servers are reached over HTTPS with their own certificate (pinned by the
// main process; see server/tls.go). address is host:port.
export function httpBase(address) {
  return 'https://' + address.replace(/^\w+:\/\//, '').replace(/[/#].*$/, '')
}

// parseLink splits a join link (eris://host:port#fingerprint, or just
// host[:port]) into the address and the certificate's fingerprint.
export function parseLink(text) {
  let t = String(text || '').trim().replace(/^\w+:\/\//, '')
  let pin = ''
  const hash = t.indexOf('#')
  if (hash >= 0) {
    pin = t.slice(hash + 1).trim()
    t = t.slice(0, hash)
  }
  t = t.replace(/\/+$/, '')
  if (t && !/:\d+$/.test(t)) t += ':7300'
  return { address: t, pin }
}

export function wsUrl(address) {
  return httpBase(address).replace(/^http/, 'ws') + '/api/ws'
}

export class Conn {
  static appVersion = '0.0.0'

  status = $state('connecting') // connecting, online, offline, rejected, outdated
  serverVersion = $state('')
  ignoreVersion = false
  error = $state('')
  you = $state(null)
  serverName = $state('')
  members = $state([]) // {id, name, role, online}
  live = $state({}) // member id -> {title, since}
  results = $state([]) // newest first
  rooms = $state([]) // {id, name, private, access, last_id, allowed}
  chat = $state({}) // room id -> {messages, loading, done}
  read = $state({}) // room id -> newest message id seen (kept on this PC)
  maxFileMB = $state(100)
  voice = $state({}) // member id -> the room whose voice they're in
  voiceOn = $state(false) // the server runs voice

  #ws = null
  #retry = null
  #closed = false
  #handlers = { signal: [], invite: [], hello: [] }

  constructor(entry) {
    this.entry = entry // {address, token, name, pin}
    try {
      this.read = JSON.parse(localStorage.getItem('eris-read:' + entry.address) || '{}')
    } catch {}
    this.connect()
  }

  on(kind, fn) {
    this.#handlers[kind].push(fn)
  }

  async connect() {
    clearTimeout(this.#retry)
    this.status = 'connecting'
    const info = await window.eris.api({ method: 'GET', url: httpBase(this.entry.address) + '/api/info' })
    if (this.#closed) return
    if (info.status !== 200) {
      this.status = 'offline'
      this.error = info.status === 0 ? `Can't reach the server (${info.json?.error || 'no answer'}). Trying again...` : ''
      if (info.status === 0) {
        // A server from before encryption answers plain HTTP only.
        const old = await window.eris.api({ method: 'GET', url: httpBase(this.entry.address).replace(/^https/, 'http') + '/api/info' })
        if (old.status === 200 && !old.json?.encrypted) {
          this.error = `This server runs Eris ${old.json?.version || '(old)'}, from before encryption. It updates itself within the hour, or its owner can update it now. Trying again...`
        }
      }
      this.#later()
      return
    }
    this.serverVersion = info.json.version || ''
    if (!this.ignoreVersion && newer(this.serverVersion, Conn.appVersion)) {
      console.info(`server ${this.entry.address} runs ${this.serverVersion}, newer than this app (${Conn.appVersion}): suggesting an update`)
      this.status = 'outdated'
      this.error = ''
      window.eris.checkUpdate()
      return
    }
    let ws
    try {
      ws = new WebSocket(wsUrl(this.entry.address))
    } catch (e) {
      this.error = String(e)
      this.#later()
      return
    }
    this.#ws = ws
    ws.onopen = () => ws.send(JSON.stringify({ type: 'auth', token: this.entry.token }))
    ws.onmessage = (ev) => this.#handle(JSON.parse(ev.data))
    ws.onclose = (ev) => {
      if (this.#ws !== ws) return
      if (ev.code === 4001) {
        this.status = 'rejected'
        this.error = 'This server no longer knows us (the member token was refused). Remove it and join again.'
        return
      }
      this.status = 'offline'
      this.#later()
    }
  }

  #later() {
    if (!this.#closed) this.#retry = setTimeout(() => this.connect(), 3000)
  }

  close() {
    this.#closed = true
    clearTimeout(this.#retry)
    this.#ws?.close()
  }

  send(msg) {
    if (this.#ws?.readyState === WebSocket.OPEN) this.#ws.send(JSON.stringify(msg))
  }

  member(id) {
    return this.members.find((m) => m.id === id)
  }

  setLive(on, title = '') {
    this.send({ type: 'live', on, title })
  }

  signal(to, data) {
    this.send({ type: 'signal', to, data })
  }

  invite(to, testId, seconds) {
    this.send({ type: 'test-invite', to, test_id: testId, seconds })
  }

  async loadResults() {
    const r = await window.eris.api({ method: 'GET', url: httpBase(this.entry.address) + '/api/tests?limit=100', token: this.entry.token })
    if (r.status === 200 && Array.isArray(r.json)) this.results = r.json
  }

  // --- Rooms and chat ---

  room(id) {
    return this.rooms.find((r) => r.id === id)
  }

  unread(room) {
    return room.access && room.last_id > (this.read[room.id] || 0)
  }

  markRead(id) {
    const r = this.room(id)
    if (!r || !r.access || (this.read[id] || 0) >= r.last_id) return
    this.read[id] = r.last_id
    try {
      localStorage.setItem('eris-read:' + this.entry.address, JSON.stringify(this.read))
    } catch {}
  }

  // loadMore loads a room's older messages (the newest first time).
  async loadMore(id) {
    if (!this.chat[id]) this.chat[id] = { messages: [], loading: false, done: false }
    const c = this.chat[id] // the state's own copy (what was assigned is copied into it)
    if (c.loading || c.done) return
    c.loading = true
    const before = c.messages[0]?.id || 0
    const r = await window.eris.api({
      method: 'GET',
      url: `${httpBase(this.entry.address)}/api/rooms/${id}/messages?limit=50&before=${before}`,
      token: this.entry.token,
    })
    c.loading = false
    if (r.status !== 200 || !Array.isArray(r.json)) {
      this.error = r.json?.error || `Couldn't load the room's messages (${r.status}).`
      return
    }
    const have = new Set(c.messages.map((m) => m.id))
    c.messages = [...r.json.filter((m) => !have.has(m.id)), ...c.messages]
    if (r.json.length < 50) c.done = true
  }

  sendMessage(roomId, text, files = []) {
    this.send({ type: 'message', room_id: roomId, text, files })
  }

  editMessage(id, text) {
    this.send({ type: 'edit', id, text })
  }

  deleteMessage(id) {
    this.send({ type: 'delete', id })
  }

  unlock(id, password) {
    this.send({ type: 'room-unlock', id, password })
  }

  createRoom(room) {
    this.send({ type: 'room-create', room })
  }

  updateRoom(id, room) {
    this.send({ type: 'room-update', id, room })
  }

  deleteRoom(id) {
    this.send({ type: 'room-delete', id })
  }

  fileUrl(id) {
    return `${httpBase(this.entry.address)}/api/files/${id}`
  }

  // upload sends a file to the server, for a message; it gives back its info.
  async upload(file) {
    if (file.size > this.maxFileMB * 1024 * 1024) throw new Error(`${file.name} is over this server's ${this.maxFileMB} MB limit.`)
    const r = await window.eris.upload({
      url: `${httpBase(this.entry.address)}/api/files?name=${encodeURIComponent(file.name)}`,
      token: this.entry.token,
      type: file.type,
      data: await file.arrayBuffer(),
    })
    if (r.status !== 200) throw new Error(r.json?.error || `The upload failed (${r.status}).`)
    return r.json
  }

  async postResult(testId, side, summary) {
    return window.eris.api({
      method: 'POST',
      url: httpBase(this.entry.address) + '/api/tests',
      token: this.entry.token,
      body: { test_id: testId, side, summary },
    })
  }

  #handle(m) {
    switch (m.type) {
      case 'hello':
        this.status = 'online'
        this.error = ''
        this.serverVersion = m.server?.version || this.serverVersion
        this.you = m.you
        this.serverName = m.server?.name || ''
        this.members = m.members
        this.live = Object.fromEntries((m.live || []).map((l) => [l.member_id, l]))
        this.maxFileMB = m.server?.max_file_mb || 100
        this.rooms = m.rooms || []
        this.chat = {} // reloaded when shown: messages may have come and gone while away
        this.voice = Object.fromEntries((m.voice || []).map((v) => [v.member_id, v.room_id]))
        this.voiceOn = !!m.server?.voice
        this.loadResults()
        for (const fn of this.#handlers.hello) fn()
        break
      case 'voice':
        if (m.room_id) this.voice[m.member_id] = m.room_id
        else delete this.voice[m.member_id]
        break
      case 'rooms':
        this.rooms = m.rooms
        for (const r of m.rooms) if (!r.access) delete this.chat[r.id]
        break
      case 'message': {
        const r = this.room(m.message.room_id)
        if (r && m.message.id > r.last_id) r.last_id = m.message.id
        const c = this.chat[m.message.room_id]
        if (c) c.messages.push(m.message)
        break
      }
      case 'message-edit': {
        const c = this.chat[m.message.room_id]
        const i = c ? c.messages.findIndex((x) => x.id === m.message.id) : -1
        if (i >= 0) c.messages[i] = m.message
        break
      }
      case 'message-delete': {
        const c = this.chat[m.room_id]
        if (c) c.messages = c.messages.filter((x) => x.id !== m.id)
        break
      }
      case 'presence': {
        const i = this.members.findIndex((x) => x.id === m.member.id)
        const entry = { ...m.member, online: m.online }
        if (i >= 0) this.members[i] = entry
        else this.members.push(entry)
        break
      }
      case 'live':
        if (m.on) this.live[m.member_id] = { member_id: m.member_id, title: m.title }
        else delete this.live[m.member_id]
        break
      case 'signal':
        for (const fn of this.#handlers.signal) fn(m.from, m.data)
        break
      case 'test-invite':
        for (const fn of this.#handlers.invite) fn(m.from, m.test_id, m.seconds)
        break
      case 'test-result':
        this.results = [m.result, ...this.results]
        break
      case 'error':
        this.error = m.error
        break
    }
  }
}

// join asks a server for a member token.
export async function join(address, name, password, ownerCode) {
  const r = await window.eris.api({
    method: 'POST',
    url: httpBase(address) + '/api/join',
    body: { name, password, owner_code: ownerCode },
  })
  if (r.status !== 200) throw new Error(r.json?.error || `the server answered ${r.status}`)
  return r.json // {token, member}
}
