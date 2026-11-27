// One connection to one server: who's there, who's live, test results, and
// the live WebSocket (DESIGN.md 3.4). Reconnects by itself.

export function httpBase(address) {
  return address.includes('://') ? address.replace(/^eris:\/\//, 'http://') : 'http://' + address
}

export function wsUrl(address) {
  return httpBase(address).replace(/^http/, 'ws') + '/api/ws'
}

export class Conn {
  status = $state('connecting') // connecting, online, offline, rejected
  error = $state('')
  you = $state(null)
  serverName = $state('')
  members = $state([]) // {id, name, role, online}
  live = $state({}) // member id -> {title, since}
  results = $state([]) // newest first

  #ws = null
  #retry = null
  #closed = false
  #handlers = { signal: [], invite: [] }

  constructor(entry) {
    this.entry = entry // {address, token, name}
    this.connect()
  }

  on(kind, fn) {
    this.#handlers[kind].push(fn)
  }

  connect() {
    clearTimeout(this.#retry)
    this.status = 'connecting'
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
        this.you = m.you
        this.serverName = m.server?.name || ''
        this.members = m.members
        this.live = Object.fromEntries((m.live || []).map((l) => [l.member_id, l]))
        this.loadResults()
        break
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
