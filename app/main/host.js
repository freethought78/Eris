// Hosting a server on this PC (DESIGN.md 3.1): the app starts eris-server in
// its own terminal window, which stays open as long as the server runs
// (closing it stops the server) and keeps running when the app closes. The
// app shows its status and log and stops, restarts and updates it, with the
// control token the server leaves in its data folder for this PC's user.
//
// Its settings are in host.json in ~/.config/Eris-server (%APPDATA%\Eris-server
// on Windows): {"data": folder, "addr": ":7300", "public": name in the join
// link}. The server updates itself, so it runs from a copy it can replace
// (bin/ there), refreshed from the app's own when the app's is newer.
const { app } = require('electron')
const path = require('node:path')
const fs = require('node:fs')
const https = require('node:https')
const crypto = require('node:crypto')
const { spawn, execFileSync } = require('node:child_process')

function hostDir() {
  const profile = process.env.ERIS_PROFILE ? '-' + process.env.ERIS_PROFILE : ''
  return path.join(app.getPath('appData'), 'Eris-server' + profile)
}

const file = (name) => path.join(hostDir(), name)

function settings() {
  let s = null
  for (const name of ['host.json', 'dashboard.json']) {
    try {
      s = JSON.parse(fs.readFileSync(file(name), 'utf8'))
      break
    } catch {}
  }
  const out = { data: s?.data || file('server-data'), addr: s?.addr || ':7300', public: s?.public || '', ...(s?.media_port ? { media_port: s.media_port } : {}) }
  return out
}

// saveSettings writes host.json the first time a server is started here, so
// its choices can be edited (only reading it doesn't make the folder).
function saveSettings(s) {
  if (fs.existsSync(file('host.json'))) return
  fs.mkdirSync(hostDir(), { recursive: true })
  fs.writeFileSync(file('host.json'), JSON.stringify(s, null, 2) + '\n')
}

function port(s) {
  return Number(String(s.addr).split(':').pop()) || 7300
}

// newer: is version a newer than b? The same rule as src/lib/version.js and
// the server's.
function newer(a, b) {
  const split = (v) => {
    const [main, suffix = ''] = String(v || '0').replace(/^v/, '').split('-', 2)
    const p = main.split('.').map((x) => parseInt(x, 10) || 0)
    return [[p[0] || 0, p[1] || 0, p[2] || 0], suffix]
  }
  const [pa, sa] = split(a)
  const [pb, sb] = split(b)
  for (let i = 0; i < 3; i++) if (pa[i] !== pb[i]) return pa[i] > pb[i]
  return sa === '' && sb !== ''
}

function versionOf(exe) {
  try {
    return execFileSync(exe, ['-version'], { encoding: 'utf8', timeout: 5000 }).trim()
  } catch {
    return ''
  }
}

function carriedServer() {
  const exe = process.platform === 'win32' ? 'eris-server.exe' : 'eris-server'
  if (app.isPackaged) return path.join(process.resourcesPath, 'server', exe)
  return path.join(__dirname, '..', '..', 'server', 'bin', exe)
}

function put(from, to) {
  fs.mkdirSync(path.dirname(to), { recursive: true })
  fs.copyFileSync(from, to + '.new')
  fs.chmodSync(to + '.new', 0o755)
  fs.renameSync(to + '.new', to)
}

// serverToRun: from the source folder, its build; otherwise the updatable
// copy, refreshed from the app's own server when that's newer.
function serverToRun() {
  const carried = carriedServer()
  if (!app.isPackaged) return carried
  const exe = file(path.join('bin', path.basename(carried)))
  const have = versionOf(exe)
  const offered = versionOf(carried)
  if (offered && (!have || newer(offered, have))) put(carried, exe)
  // LiveKit (voice and cameras) goes beside it, when the app's differs.
  const lk = path.join(path.dirname(carried), path.basename(carried).replace('eris-server', 'livekit-server'))
  const lkTo = path.join(path.dirname(exe), path.basename(lk))
  if (fs.existsSync(lk) && (!fs.existsSync(lkTo) || fs.statSync(lk).size !== fs.statSync(lkTo).size)) put(lk, lkTo)
  return exe
}

// local is a call to this PC's server, trusting only its own certificate
// (read from its data folder), with its control token.
function local(method, apiPath) {
  const s = settings()
  let token, cert
  try {
    token = fs.readFileSync(path.join(s.data, 'control-token'), 'utf8').trim()
    cert = fs.readFileSync(path.join(s.data, 'tls', 'cert.pem'), 'utf8')
  } catch {
    return Promise.resolve({ status: 0, json: { error: 'not running' } })
  }
  const der = Buffer.from(cert.replace(/-----[^-]+-----/g, '').replace(/\s+/g, ''), 'base64')
  return new Promise((resolve) => {
    const req = https.request(
      { host: '127.0.0.1', port: port(s), method, path: apiPath, headers: { Authorization: 'Bearer ' + token }, rejectUnauthorized: false, timeout: 5000 },
      (res) => {
        const peer = res.socket.getPeerCertificate()
        if (!peer?.raw || !crypto.timingSafeEqual(crypto.createHash('sha256').update(peer.raw).digest(), crypto.createHash('sha256').update(der).digest())) {
          res.destroy()
          return resolve({ status: 0, json: { error: "something else is answering on the server's port" } })
        }
        let body = ''
        res.on('data', (c) => (body += c))
        res.on('end', () => {
          let json = null
          try {
            json = JSON.parse(body)
          } catch {}
          resolve({ status: res.statusCode, json })
        })
      },
    )
    req.on('timeout', () => req.destroy(new Error('no answer')))
    req.on('error', (err) => resolve({ status: 0, json: { error: String(err.code || err.message) } }))
    req.end()
  })
}

// status says whether this PC's server runs, and if so, what it reports.
async function status() {
  const s = settings()
  const r = await local('GET', '/api/admin/status')
  if (r.status === 200) return { running: true, ...r.json, settings: s, canHost: fs.existsSync(carriedServer()) }
  // Something answering on the port without controls: a server from before
  // they existed (0.1.4 and older), which can't be stopped from here.
  const info = await plainInfo(port(s))
  if (info) return { running: true, legacy: true, version: info.version, name: info.name, settings: s, canHost: fs.existsSync(carriedServer()) }
  return { running: false, settings: s, canHost: fs.existsSync(carriedServer()) }
}

function plainInfo(p) {
  return new Promise((resolve) => {
    const req = require('node:http').get({ host: '127.0.0.1', port: p, path: '/api/info', timeout: 3000 }, (res) => {
      let body = ''
      res.on('data', (c) => (body += c))
      res.on('end', () => {
        try {
          resolve(res.statusCode === 200 ? JSON.parse(body) : null)
        } catch {
          resolve(null)
        }
      })
    })
    req.on('timeout', () => req.destroy())
    req.on('error', () => resolve(null))
  })
}

// Terminals to try, in order (Linux).
const terminals = [
  ['gnome-terminal', (cmd) => ['--title=Eris server', '--', ...cmd]],
  ['ptyxis', (cmd) => ['--', ...cmd]],
  ['kgx', (cmd) => ['-e', ...cmd]],
  ['konsole', (cmd) => ['-e', ...cmd]],
  ['xfce4-terminal', (cmd) => ['--title=Eris server', '-x', ...cmd]],
  ['x-terminal-emulator', (cmd) => ['-e', ...cmd]],
  ['xterm', (cmd) => ['-T', 'Eris server', '-e', ...cmd]],
]

function which(name) {
  for (const dir of (process.env.PATH || '').split(path.delimiter)) {
    const p = path.join(dir, name)
    try {
      fs.accessSync(p, fs.constants.X_OK)
      return p
    } catch {}
  }
  return ''
}

// start opens the server in its own window, unless it's already running.
async function start() {
  if ((await status()).running) return { ok: true, already: true }
  const s = settings()
  let exe
  try {
    exe = serverToRun()
  } catch (err) {
    return { ok: false, error: `Couldn't set up the server program: ${err.message}` }
  }
  if (!fs.existsSync(exe)) return { ok: false, error: `The server program is missing (${exe}).` }
  saveSettings(s)
  fs.mkdirSync(s.data, { recursive: true })
  const cmd = [exe, '-data', s.data, '-addr', s.addr, ...(s.public ? ['-public', s.public] : []), ...(s.media_port ? ['-media-port', String(s.media_port)] : [])]
  // Not ours to keep: the window's process lives on after the app closes.
  const env = { ...process.env }
  for (const k of ['APPDIR', 'APPIMAGE', 'ARGV0', 'OWD', 'CHROME_DESKTOP', 'ELECTRON_RUN_AS_NODE', 'GDK_BACKEND']) delete env[k]
  let child
  if (process.platform === 'win32') {
    child = spawn(cmd[0], cmd.slice(1), { detached: true, stdio: 'ignore', windowsHide: false, env })
  } else {
    const t = terminals.find(([name]) => which(name))
    if (!t) return { ok: false, error: 'No terminal program found to show the server in (gnome-terminal, konsole, xterm...).' }
    child = spawn(t[0], t[1](cmd), { detached: true, stdio: 'ignore', env })
  }
  child.on('error', () => {})
  child.unref()
  for (let i = 0; i < 40; i++) {
    await new Promise((r) => setTimeout(r, 250))
    if ((await status()).running) return { ok: true }
  }
  return { ok: false, error: "The server's window opened, but the server didn't start; its window says why." }
}

async function action(what) {
  const r = await local('POST', `/api/admin/${what}`)
  return r.status === 200 ? { ok: true } : { ok: false, error: r.json?.error || `the server answered ${r.status}` }
}

async function log() {
  const r = await local('GET', '/api/admin/log')
  return r.status === 200 && Array.isArray(r.json) ? r.json : []
}

module.exports = { status, start, action, log, settings }
