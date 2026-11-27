// Going live and joining someone's stream, with the streaming helper doing
// the work (helper/). Connection setup between the two helpers goes through
// the server's live connection: our helper's offer becomes a "signal" to the
// host's app, which hands it to its helper and sends the answer back.
//
// A stream test is a join with a time limit and a test id: when it ends,
// both helpers' summaries are posted to the server, which keeps them.

function newTestId() {
  const d = new Date()
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`
}

class Stream {
  hosting = $state(false) // our helper is streaming our screen
  hostConn = null // the server we're live on
  playing = $state(null) // {conn, hostId, testId, seconds, state}
  notice = $state('') // the latest thing worth saying
  lastSummary = $state(null) // {side, summary}

  constructor() {
    window.eris.onHelper(({ role, msg }) => (role === 'host' ? this.#fromHost(msg) : this.#fromPlayer(msg)))
  }

  // A connection's signals and invitations come here.
  attach(conn) {
    conn.on('signal', (from, data) => this.#signal(conn, from, data))
    conn.on('invite', (from, testId, seconds) => {
      const who = conn.member(from)?.name || 'someone'
      this.notice = `${who} started a ${seconds} s stream test with you.`
      this.join(conn, from, { testId, seconds })
    })
  }

  // Go Live: stream our main monitor on conn.
  async goLive(conn, opts = {}) {
    if (this.hosting) return
    this.hostConn = conn
    this.hosting = true
    this.notice = 'Starting the stream...'
    const host = conn.entry.address.replace(/^.*:\/\//, '').replace(/:\d+$/, '')
    const isLocal = /^(localhost|127\.|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(host)
    await window.eris.startHelper('host', { publicAddr: isLocal ? '' : host, ...opts })
  }

  stopLive() {
    if (!this.hosting) return
    this.hostConn?.setLive(false)
    window.eris.stopHelper('host')
    this.hosting = false
    this.notice = 'Stream stopped.'
  }

  // Join hostId's stream; with testId and seconds it's a stream test.
  async join(conn, hostId, { testId = '', seconds = 0, extraArgs } = {}) {
    if (this.playing) window.eris.stopHelper('player')
    this.playing = { conn, hostId, testId, seconds, state: 'starting' }
    this.notice = testId ? `Stream test ${testId} starting...` : 'Joining the stream...'
    await window.eris.startHelper('player', { seconds, width: 1280, height: 720, extraArgs })
  }

  // Start a stream test with a player: they join with a time limit.
  testWith(conn, memberId, seconds = 60) {
    const testId = newTestId()
    conn.invite(memberId, testId, seconds)
    this.notice = `Stream test ${testId} started with ${conn.member(memberId)?.name || 'them'}.`
    return testId
  }

  leave() {
    window.eris.stopHelper('player')
    this.playing = null
  }

  #signal(conn, from, data) {
    switch (data?.kind) {
      case 'offer': // a player wants our stream
        if (!this.hosting || conn !== this.hostConn) {
          conn.signal(from, { kind: 'refused', error: "isn't streaming right now" })
          return
        }
        window.eris.sendHelper('host', { type: 'offer', peer: from, sdp: data.sdp, test_id: data.test_id || '' })
        break
      case 'answer': // the host's helper answered ours
        if (this.playing && this.playing.hostId === from) {
          window.eris.sendHelper('player', { type: 'answer', sdp: data.sdp, width: data.width, height: data.height, fps: data.fps })
          this.playing.state = 'connecting'
        }
        break
      case 'refused':
        if (this.playing && this.playing.hostId === from) {
          this.notice = `${conn.member(from)?.name || 'The host'} ${data.error}.`
          this.leave()
        }
        break
    }
  }

  #fromHost(msg) {
    const conn = this.hostConn
    switch (msg.type) {
      case 'ready':
        conn?.setLive(true, `${msg.width}x${msg.height} at ${msg.fps} fps`)
        this.notice = 'You are live.'
        break
      case 'answer':
        conn?.signal(msg.peer, { kind: 'answer', sdp: msg.sdp, width: msg.width, height: msg.height, fps: msg.fps })
        break
      case 'refused':
        conn?.signal(msg.peer, { kind: 'refused', error: msg.error })
        break
      case 'summary':
        this.lastSummary = { side: 'host', summary: msg.summary }
        if (msg.test_id && conn) conn.postResult(msg.test_id, 'host', msg.summary)
        break
      case 'error':
        this.notice = msg.error
        this.hosting = false
        break
      case 'exit':
        if (this.hosting) {
          this.hostConn?.setLive(false)
          this.hosting = false
          this.notice = `The stream stopped (helper exited with ${msg.code}; its log: ${msg.log}).`
        }
        break
    }
  }

  #fromPlayer(msg) {
    const p = this.playing
    switch (msg.type) {
      case 'offer':
        p?.conn.signal(p.hostId, { kind: 'offer', sdp: msg.sdp, test_id: p.testId })
        break
      case 'summary':
        this.lastSummary = { side: 'player', summary: msg.summary }
        if (p?.testId) {
          p.conn.postResult(p.testId, 'player', msg.summary)
          this.notice = `Stream test ${p.testId} finished; results sent.`
        }
        break
      case 'error':
        this.notice = msg.error
        this.playing = null
        break
      case 'exit':
        if (p && !p.testId && msg.code) this.notice = `The game window closed (code ${msg.code}; its log: ${msg.log}).`
        this.playing = null
        break
    }
  }
}

export const stream = new Stream()
