// Eris app, main process: the window, the saved server list, HTTP calls to
// servers, and the streaming helper (DESIGN.md section 4; the helper is
// helper/, started here and talked to with JSON lines on stdin and stdout).
const { app, BrowserWindow, ipcMain, screen, session, net, shell, dialog } = require('electron')
const crypto = require('node:crypto')
const path = require('node:path')
const fs = require('node:fs')
const readline = require('node:readline')
const { spawn, execFileSync } = require('node:child_process')
const updater = require('./updater')
const host = require('./host')
const linuxInstall = require('./linux-install')

// `Eris --server` only starts this PC's server in its own window (host.js)
// and exits: for a desktop shortcut that starts the server without the app.
const serverMode = process.argv.includes('--server')

// ERIS_PROFILE=name keeps a separate server list and logs, so two copies can
// run on one PC (for testing).
if (process.env.ERIS_PROFILE) {
  app.setPath('userData', path.join(app.getPath('appData'), 'Eris-' + process.env.ERIS_PROFILE))
}

let win = null
const helpers = {} // role ("host" or "player") -> child process

function userFile(name) {
  return path.join(app.getPath('userData'), name)
}

function helperExe(name) {
  const exe = process.platform === 'win32' ? name + '.exe' : name
  if (app.isPackaged) return path.join(process.resourcesPath, 'helper', exe)
  const dev = process.platform === 'win32' ? ['helper', 'dist', 'windows', exe] : ['helper', 'bin', exe]
  return path.join(__dirname, '..', '..', ...dev)
}

function send(channel, data) {
  if (win && !win.isDestroyed()) win.webContents.send(channel, data)
}

// mullvadConnected: the host runs outside a connected Mullvad VPN, so a
// remote player's packets come and go through the router (DESIGN.md 5.4).
function mullvadConnected() {
  if (process.platform !== 'linux') return false
  try {
    return /^Connected/m.test(execFileSync('mullvad', ['status'], { encoding: 'utf8', timeout: 3000 }))
  } catch {
    return false
  }
}

function startHelper(role, opts) {
  stopHelper(role)
  const name = role === 'host' ? 'ptsend' : 'ptrecv'
  const exe = helperExe(name)
  const run = opts.run || 0 // the window's id for this run, on every message from it
  if (!fs.existsSync(exe)) {
    send('helper', { role, run, msg: { type: 'error', error: `The streaming helper is missing (${exe}).` } })
    return
  }
  let args = ['-signal', 'stdio']
  if (role === 'host') {
    const d = screen.getPrimaryDisplay()
    const b = d.bounds
    const s = d.scaleFactor || 1
    const region = `${Math.round(b.width * s)}x${Math.round(b.height * s)}+${Math.round(b.x * s)}+${Math.round(b.y * s)}`
    // A dev build (tools/dev.sh) streams on its own port, so it can run next to the real app.
    const udpPort = opts.udpPort || (process.env.ERIS_PROFILE === 'dev' ? 7411 : 7401)
    args.push('-region', region, '-udp-port', String(udpPort), '-pad')
    if (opts.publicAddr) args.push('-public', opts.publicAddr)
  } else {
    args.push('-w', String(opts.width || 1280), '-h', String(opts.height || 720))
    if (opts.seconds) args.push('-seconds', String(opts.seconds))
    if (opts.fakeInput) args.push('-fake-input')
  }
  if (opts.extraArgs) args.push(...opts.extraArgs)
  let cmd = exe
  if (role === 'host' && mullvadConnected()) {
    args = [exe, ...args]
    cmd = 'mullvad-exclude'
  }
  fs.mkdirSync(userFile('logs'), { recursive: true })
  const stamp = new Date().toISOString().replace(/[:.]/g, '-')
  const logPath = userFile(path.join('logs', `${role}-${stamp}.log`))
  const log = fs.createWriteStream(logPath)
  const child = spawn(cmd, args, { stdio: ['pipe', 'pipe', 'pipe'] })
  helpers[role] = child
  child.stderr.pipe(log)
  readline.createInterface({ input: child.stdout }).on('line', (line) => {
    try {
      send('helper', { role, run, msg: JSON.parse(line) })
    } catch {
      log.write(line + '\n')
    }
  })
  child.on('exit', (code) => {
    if (helpers[role] === child) delete helpers[role]
    send('helper', { role, run, msg: { type: 'exit', code, log: logPath } })
  })
  child.on('error', (err) => send('helper', { role, run, msg: { type: 'error', error: String(err) } }))
}

function stopHelper(role) {
  const child = helpers[role]
  if (!child) return
  delete helpers[role]
  // Asked to stop, the helper ends its run and sends its summary (a stream
  // test's results); it's killed only if it hasn't gone after 5 s.
  try {
    child.stdin.write(JSON.stringify({ type: 'stop' }) + '\n')
  } catch {}
  setTimeout(() => child.kill(), 5000)
}

function createWindow() {
  win = new BrowserWindow({
    width: 1100,
    height: 720,
    backgroundColor: '#1a1a2e',
    title: app.isPackaged ? 'Eris' : 'Eris (dev)',
    icon: path.join(__dirname, '..', 'build', 'icon.png'),
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  })
  win.setMenuBarVisibility(false)
  // Links in messages open in the browser; the window never goes anywhere else.
  win.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:\/\//.test(url)) shell.openExternal(url)
    return { action: 'deny' }
  })
  win.webContents.on('will-navigate', (e, url) => {
    if (!url.startsWith('file://')) {
      e.preventDefault()
      if (/^https?:\/\//.test(url)) shell.openExternal(url)
    }
  })
  // The window's console goes to logs/app.log, to find problems on a PC we
  // can't see.
  fs.mkdirSync(userFile('logs'), { recursive: true })
  const appLog = fs.createWriteStream(userFile(path.join('logs', 'app.log')), { flags: 'a' })
  appLog.write(`--- ${new Date().toISOString()} Eris ${app.getVersion()} on ${process.platform}\n`)
  win.webContents.on('console-message', (e) => appLog.write(`[${e.level}] ${e.message}\n`))
  win.webContents.on('render-process-gone', (_e, d) => appLog.write(`window process gone: ${d.reason}\n`))
  win.loadFile(path.join(__dirname, '..', 'dist', 'index.html'))
}

// For automated tests: --auto='{"join":...}' or ERIS_AUTO='{...}' is handed
// to the window. (On Windows the command line loses JSON's quotes, so tests
// there use the environment.)
function autoConfig() {
  const a = process.argv.find((x) => x.startsWith('--auto='))
  const text = a ? a.slice('--auto='.length) : process.env.ERIS_AUTO
  if (!text) return null
  try {
    return JSON.parse(text)
  } catch {
    return null
  }
}

// Servers' certificates (server/tls.go): each server makes its own, and the
// join link carries its fingerprint. A server's certificate is trusted when
// it matches the one pinned for its host name; a server added before
// encryption is pinned the first time it's seen. Any other certificate is
// refused. Everything else (GitHub, for updates) is checked the usual way.
let knownHosts = new Set() // host names of the saved servers
let pins = {} // host name -> fingerprint (base64url SHA-256 of the certificate)
const refused = new Set() // host names whose certificate didn't match

function hostOf(address) {
  return String(address || '').replace(/^\w+:\/\//, '').replace(/[/#].*$/, '').replace(/:\d+$/, '').replace(/^\[|\]$/g, '')
}

function loadPins() {
  try {
    pins = JSON.parse(fs.readFileSync(userFile('pins.json'), 'utf8'))
  } catch {
    pins = {}
  }
}

function savePins() {
  fs.mkdirSync(app.getPath('userData'), { recursive: true })
  fs.writeFileSync(userFile('pins.json'), JSON.stringify(pins, null, 2), { mode: 0o600 })
}

function noteServers(list) {
  knownHosts = new Set(list.map((s) => hostOf(s.address)))
  let changed = false
  for (const s of list) {
    // A pin from a join link wins (the owner may have made a new certificate).
    if (s.pin && pins[hostOf(s.address)] !== s.pin) {
      pins[hostOf(s.address)] = s.pin
      refused.delete(hostOf(s.address))
      changed = true
    }
  }
  if (changed) savePins()
}

function fingerprint(cert) {
  const der = Buffer.from(String(cert.data).replace(/-----[^-]+-----/g, '').replace(/\s+/g, ''), 'base64')
  return crypto.createHash('sha256').update(der).digest('base64url')
}

function verifyCertificate(req, done) {
  const host = req.hostname
  const pin = pins[host]
  if (!pin && !knownHosts.has(host)) return done(-3) // not one of our servers
  const fp = fingerprint(req.certificate)
  if (pin === fp) return done(0)
  if (!pin) {
    pins[host] = fp // first sight of a server added before encryption
    savePins()
    return done(0)
  }
  refused.add(host)
  done(-2)
}

ipcMain.handle('servers-load', () => {
  let list = []
  try {
    list = JSON.parse(fs.readFileSync(userFile('servers.json'), 'utf8'))
  } catch {}
  noteServers(list)
  return list
})

ipcMain.handle('servers-save', (_e, list) => {
  fs.mkdirSync(app.getPath('userData'), { recursive: true })
  fs.writeFileSync(userFile('servers.json'), JSON.stringify(list, null, 2), { mode: 0o600 })
  noteServers(list)
})

// A join link's pin, before the server is saved (joining needs it first).
// Without one (an address typed by hand), the first certificate seen is
// trusted from then on.
ipcMain.handle('pin', (_e, { address, pin }) => {
  const host = hostOf(address)
  knownHosts.add(host)
  if (!pin) return
  pins[host] = pin
  refused.delete(host)
  savePins()
})

function netError(url, err) {
  if (refused.has(hostOf(url))) {
    return "This server's certificate has changed, so it may not be your server. If its owner made a new one, remove the server and join again with the new link."
  }
  return String(err.cause?.code || err.message || err)
}

// Uploading a file for a message: the window hands over the bytes.
ipcMain.handle('upload', async (_e, { url, token, type, data }) => {
  try {
    const res = await net.fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': type || 'application/octet-stream', Authorization: 'Bearer ' + token },
      body: Buffer.from(data),
    })
    let json = null
    try {
      json = await res.json()
    } catch {}
    return { status: res.status, json }
  } catch (err) {
    return { status: 0, json: { error: netError(url, err) } }
  }
})

// Saving a file from a message: asks where (the usual save dialog).
ipcMain.handle('download', (_e, url) => {
  if (win && !win.isDestroyed() && /^https:\/\//.test(url)) win.webContents.downloadURL(url)
})

// HTTP to a server, from here rather than the window (no cross-origin limits).
ipcMain.handle('api', async (_e, { method, url, token, body }) => {
  try {
    const res = await net.fetch(url, {
      method,
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: 'Bearer ' + token } : {}),
      },
      body: body ? JSON.stringify(body) : undefined,
      signal: AbortSignal.timeout(10000),
    })
    let json = null
    try {
      json = await res.json()
    } catch {}
    return { status: res.status, json }
  } catch (err) {
    return { status: 0, json: { error: netError(url, err) } }
  }
})

ipcMain.handle('update-status', () => updater.status())
ipcMain.handle('update-check', () => updater.check())
ipcMain.handle('update-download', () => updater.download())
ipcMain.handle('update-install', () => updater.install())

ipcMain.handle('helper-start', (_e, { role, opts }) => startHelper(role, opts || {}))
ipcMain.handle('helper-stop', (_e, role) => stopHelper(role))
ipcMain.handle('helper-send', (_e, { role, msg }) => {
  const child = helpers[role]
  if (child) child.stdin.write(JSON.stringify(msg) + '\n')
})
ipcMain.handle('info', () => ({
  version: app.getVersion(),
  platform: process.platform,
  canHost: process.platform === 'linux', // the Windows host (ddagrab, ViGEm) isn't built yet
  dev: !app.isPackaged,
  auto: autoConfig(),
}))

// Hosting a server on this PC.
ipcMain.handle('host-status', async () => ({
  ...(await host.status()),
  canAddIcon: linuxInstall.canInstall(),
  serverIcon: linuxInstall.hasServerIcon(),
}))
ipcMain.handle('host-add-icon', () => linuxInstall.addServerIcon())
ipcMain.handle('host-start', () => host.start())
ipcMain.handle('host-action', (_e, what) => (['stop', 'restart', 'update'].includes(what) ? host.action(what) : null))
ipcMain.handle('host-log', () => host.log())

if (serverMode) {
  app.whenReady().then(async () => {
    linuxInstall.integrate()
    const r = await host.start()
    if (!r.ok) dialog.showErrorBox('Eris server', r.error)
    app.quit()
  })
} else {
  app.whenReady().then(() => {
    loadPins()
    session.defaultSession.setCertificateVerifyProc(verifyCertificate)
    linuxInstall.integrate() // app menu and desktop icon, like an installer (AppImage only)
    createWindow()
    updater.setup((s) => send('update', s))
  })
  app.on('window-all-closed', () => {
    for (const role of Object.keys(helpers)) stopHelper(role)
    app.quit()
  })
}
