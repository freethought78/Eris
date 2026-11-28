// Going live and joining someone's stream, with the streaming helper doing
// the work (helper/). Connection setup between the two helpers goes through
// the server's live connection: our helper's offer becomes a "signal" to the
// host's app, which hands it to its helper and sends the answer back.
//
// A stream test is a join with a time limit and a test id: when it ends,
// both helpers' summaries are posted to the server, which keeps them.
//
// Playing needs the host's yes (DESIGN.md 5.4): a player's request waits
// on the host's screen until they let them in or decline; up to 3 players
// (players 2 to 4) each get their own stream and controller, and the host
// can take a controller back at any time. A stream test the host started
// needs no yes.
//
// Each helper run has its own number, on every message from it: a run that's
// being replaced (a stream test starting while we already watch, say) still
// reports to the test it belonged to, and its ending can't touch the new run.

import { diag } from './diag.svelte.js'

// A helper's session goes to the server's diagnostics: its summary, and the
// end of its log.
async function sendSession(conn, side, msg) {
  if (!conn) return
  if (msg.type === 'summary') diag.add({ kind: 'stream-summary', side, summary: msg.summary }, conn)
  if (msg.type === 'exit' && msg.log) {
    const lines = await window.eris.logTail(msg.log)
    diag.add({ kind: 'stream-log', side, code: msg.code, lines }, conn)
  }
}

function newTestId() {
  const d = new Date()
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}-${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`
}

let runs = 0

class Stream {
  hosting = $state(false) // our helper is streaming our screen
  live = $state(false) // ...and it's ready: we're live
  hostConn = $state(null) // the server we're live on
  hostRun = 0 // our host helper's run
  playing = $state(null) // {conn, hostId, testId, seconds, state, run}
  #playerRuns = new Map() // run -> {conn, hostId, testId}, until it exits
  #invited = new Map() // member id -> the test id we invited them to
  requests = $state([]) // {conn, id, name, sdp}: members asking to play, waiting for our yes
  players = $state([]) // {conn, id, name}: members playing on our stream, in the order they came
  autoAllow = false // automated tests: let everyone in
  notice = $state('') // the latest thing worth saying
  lastSummary = $state(null) // {side, summary}

  constructor() {
    window.eris.onHelper(({ role, run, msg }) => (role === 'host' ? this.#fromHost(run, msg) : this.#fromPlayer(run, msg)))
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
    this.hostRun = ++runs
    await window.eris.startHelper('host', { publicAddr: isLocal ? '' : host, ...opts, run: this.hostRun })
  }

  stopLive() {
    if (!this.hosting) return
    for (const r of this.requests) r.conn.signal(r.id, { kind: 'refused', error: 'stopped streaming' })
    this.requests = []
    this.players = []
    this.hostConn?.setLive(false)
    window.eris.stopHelper('host')
    this.hosting = false
    this.live = false
    this.notice = 'Stream stopped.'
  }

  // Join hostId's stream; with testId and seconds it's a stream test.
  async join(conn, hostId, { testId = '', seconds = 0, extraArgs } = {}) {
    if (this.playing) window.eris.stopHelper('player') // it still reports to its own test
    const run = ++runs
    this.#playerRuns.set(run, { conn, hostId, testId })
    this.playing = { conn, hostId, testId, seconds, state: 'starting', run }
    this.notice = testId ? `Stream test ${testId} starting...` : 'Joining the stream...'
    await window.eris.startHelper('player', { seconds, width: 1280, height: 720, extraArgs, run })
  }

  // Start a stream test with a player: they join with a time limit.
  testWith(conn, memberId, seconds = 60) {
    const testId = newTestId()
    this.#invited.set(memberId, testId)
    conn.invite(memberId, testId, seconds)
    this.notice = `Stream test ${testId} started with ${conn.member(memberId)?.name || 'them'}.`
    return testId
  }

  leave() {
    const p = this.playing
    if (p && p.state === 'asking') p.conn.signal(p.hostId, { kind: 'cancel' })
    window.eris.stopHelper('player')
    this.playing = null
  }

  // The host's side of asking: let a member play, or not.
  allow(id) {
    const r = this.requests.find((r) => r.id === id)
    if (!r) return
    this.requests = this.requests.filter((x) => x !== r)
    window.eris.sendHelper('host', { type: 'offer', peer: r.id, sdp: r.sdp, test_id: '' })
  }

  decline(id) {
    const r = this.requests.find((r) => r.id === id)
    if (!r) return
    this.requests = this.requests.filter((x) => x !== r)
    r.conn.signal(r.id, { kind: 'refused', error: "didn't let you play this time" })
  }

  // takeBack ends a player's session: their controller unplugs and their
  // game window closes (they can still watch in the call, or ask again).
  takeBack(id) {
    const p = this.players.find((p) => p.id === id)
    if (!p) return
    p.conn.signal(id, { kind: 'ended' })
    window.eris.sendHelper('host', { type: 'end', peer: id })
    this.players = this.players.filter((x) => x !== p)
  }

  #signal(conn, from, data) {
    switch (data?.kind) {
      case 'offer': // a player wants our stream
        if (!this.hosting || conn !== this.hostConn) {
          conn.signal(from, { kind: 'refused', error: "isn't streaming right now" })
          return
        }
        {
          // An older player app may not pass the test id on; we know whom we invited.
          const testId = data.test_id || this.#invited.get(from) || ''
          this.#invited.delete(from)
          // A stream test we started, someone already playing (their player
          // restarted), or a test run: no need to ask.
          if (testId || this.autoAllow || this.players.some((p) => p.id === from)) {
            if (this.autoAllow && !testId) console.info(`auto test: let ${conn.member(from)?.name} play`)
            window.eris.sendHelper('host', { type: 'offer', peer: from, sdp: data.sdp, test_id: testId })
            break
          }
          const name = conn.member(from)?.name || 'Someone'
          this.requests = [...this.requests.filter((r) => r.id !== from), { conn, id: from, name, sdp: data.sdp }]
          this.notice = `${name} wants to play.`
        }
        break
      case 'cancel': // they stopped asking
        this.requests = this.requests.filter((r) => !(r.conn === conn && r.id === from))
        break
      case 'ended': // the host took our controller back
        if (this.playing && this.playing.hostId === from) {
          this.notice = `${conn.member(from)?.name || 'The host'} took the controller back.`
          this.leave()
        }
        break
      case 'answer': // the host's helper answered ours
        if (this.playing && this.playing.hostId === from) {
          window.eris.sendHelper('player', { type: 'answer', sdp: data.sdp, width: data.width, height: data.height, fps: data.fps })
          if (this.playing.state === 'asking') this.notice = "You're in: your controller plays."
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

  #fromHost(run, msg) {
    const conn = this.hostConn
    sendSession(conn, 'host', msg)
    if (msg.type === 'summary') {
      // A player's session ended.
      if (run === this.hostRun) this.players = this.players.filter((p) => p.id !== msg.peer)
      this.lastSummary = { side: 'host', summary: msg.summary }
      if (msg.test_id && conn) this.#post(conn, msg.test_id, 'host', msg.summary)
      return
    }
    if (run !== this.hostRun) return // an earlier run, already replaced
    switch (msg.type) {
      case 'ready':
        conn?.setLive(true, `${msg.width}x${msg.height} at ${msg.fps} fps`)
        this.live = true
        this.notice = 'You are live.'
        break
      case 'answer':
        conn?.signal(msg.peer, { kind: 'answer', sdp: msg.sdp, width: msg.width, height: msg.height, fps: msg.fps })
        if (conn && !this.players.some((p) => p.id === msg.peer)) {
          this.players = [...this.players, { conn, id: msg.peer, name: conn.member(msg.peer)?.name || '?' }]
        }
        break
      case 'refused':
        conn?.signal(msg.peer, { kind: 'refused', error: msg.error })
        break
      case 'error':
        this.notice = msg.error
        this.hosting = false
        this.live = false
        break
      case 'exit':
        if (this.hosting) {
          this.hostConn?.setLive(false)
          this.hosting = false
          this.live = false
          this.players = []
          this.notice = `The stream stopped (helper exited with ${msg.code}; its log: ${msg.log}).`
        }
        break
    }
  }

  #fromPlayer(run, msg) {
    const ctx = this.#playerRuns.get(run)
    sendSession(ctx?.conn, 'player', msg)
    if (msg.type === 'summary') {
      this.lastSummary = { side: 'player', summary: msg.summary }
      if (ctx?.testId) {
        this.#post(ctx.conn, ctx.testId, 'player', msg.summary)
        this.notice = `Stream test ${ctx.testId} finished; sending the results.`
      }
      return
    }
    if (msg.type === 'exit') this.#playerRuns.delete(run)
    const p = this.playing
    if (!p || p.run !== run) return // an earlier run, already replaced
    switch (msg.type) {
      case 'offer':
        p.conn.signal(p.hostId, { kind: 'offer', sdp: msg.sdp, test_id: p.testId })
        if (!p.testId) {
          p.state = 'asking'
          this.notice = `Asking ${p.conn.member(p.hostId)?.name || 'the host'} to let you play...`
        }
        break
      case 'error':
        this.notice = msg.error
        this.playing = null
        break
      case 'exit':
        if (!p.testId && msg.code) this.notice = `The game window closed (code ${msg.code}; its log: ${msg.log}).`
        this.playing = null
        break
    }
  }

  // #post sends one side's results; a failure is said, and goes in app.log.
  async #post(conn, testId, side, summary) {
    const r = await conn.postResult(testId, side, summary)
    if (r.status === 200) {
      console.info(`stream test ${testId}: ${side} results stored`)
      if (side === 'player') this.notice = `Stream test ${testId} finished; results sent.`
      return
    }
    const why = r.json?.error || `the server answered ${r.status}`
    console.error(`stream test ${testId}: couldn't store the ${side} results: ${why}`)
    this.notice = `Stream test ${testId}: the ${side} results weren't saved (${why}).`
  }
}

export const stream = new Stream()
