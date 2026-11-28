// Eris app, main process: the window, the saved server list, HTTP calls to
// servers, and the streaming helper (DESIGN.md section 4; the helper is
// helper/, started here and talked to with JSON lines on stdin and stdout).
const { app, BrowserWindow, ipcMain, screen, session, net, shell, dialog, desktopCapturer } = require('electron')
const crypto = require('node:crypto')
const path = require('node:path')
const fs = require('node:fs')
const readline = require('node:readline')
const { spawn, execFileSync } = require('node:child_process')
const updater = require('./updater')
const host = require('./host')
const linuxInstall = require('./linux-install')
const contextMenu = require('./contextmenu')

// `Eris --server` only starts this PC's server in its own window (host.js)
// and exits: for a desktop shortcut that starts the server without the app.
const serverMode = process.argv.includes('--server')

// Automated tests: Chromium's fake microphone (a tone) and camera (a test
// picture), with no permission prompts, and no sound played on this PC.
// Silent: the tests check that sound arrives, and play nothing out loud.
if (process.env.ERIS_FAKE_MEDIA) {
  app.commandLine.appendSwitch('use-fake-device-for-media-stream')
  if (!process.env.ERIS_SHOW_PICKER) app.commandLine.appendSwitch('use-fake-ui-for-media-stream') // (it skips the share picker too)
  app.commandLine.appendSwitch('mute-audio')
}

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

// mullvadExclude: with Mullvad installed, the host always runs outside the VPN
// (connected now or later), so a remote player's packets come and go through
// the router (DESIGN.md 5.4).
function mullvadExclude() {
  if (process.platform !== 'linux') return ''
  for (const dir of (process.env.PATH || '').split(path.delimiter)) {
    try {
      fs.accessSync(path.join(dir, 'mullvad-exclude'), fs.constants.X_OK)
      return path.join(dir, 'mullvad-exclude')
    } catch {}
  }
  return ''
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
    // The game's sound goes to the player too, without this app's own
    // (the call's voices): Linux with PipeWire for now.
    if (process.platform === 'linux') args.push('-game-sound', '-exclude-pid', String(process.pid))
  } else {
    args.push('-w', String(opts.width || 1280), '-h', String(opts.height || 720))
    if (opts.seconds) args.push('-seconds', String(opts.seconds))
    if (opts.fakeInput) args.push('-fake-input')
  }
  if (opts.extraArgs) args.push(...opts.extraArgs)
  let cmd = exe
  if (role === 'host' && mullvadExclude()) {
    args = [exe, ...args]
    cmd = mullvadExclude()
  }
  fs.mkdirSync(userFile('logs'), { recursive: true })
  const stamp = new Date().toISOString().replace(/[:.]/g, '-')
  const logPath = userFile(path.join('logs', `${role}-${stamp}.log`))
  const log = fs.createWriteStream(logPath)
  // Tests stay silent: the player's sound goes to SDL's "dummy" output.
  const env = process.env.ERIS_FAKE_MEDIA ? { ...process.env, SDL_AUDIODRIVER: 'dummy' } : process.env
  const child = spawn(cmd, args, { stdio: ['pipe', 'pipe', 'pipe'], env })
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

// The app's logs (logs/ in its data folder) are kept for 3 days: app.log is
// today's (an older one is renamed app-DAY.log), helper sessions have their
// own files, and anything older than 3 days is deleted at each start. A day's
// app.log stops at 100 MB.
const LOG_DAYS = 3
const LOG_DAY_MAX = 100 * 1024 * 1024

function openAppLog() {
  const dir = userFile('logs')
  fs.mkdirSync(dir, { recursive: true })
  const day = (d) => d.toISOString().slice(0, 10)
  const current = path.join(dir, 'app.log')
  try {
    const st = fs.statSync(current)
    if (day(st.mtime) !== day(new Date())) fs.renameSync(current, path.join(dir, `app-${day(st.mtime)}.log`))
  } catch {}
  const cut = Date.now() - LOG_DAYS * 24 * 3600 * 1000
  for (const f of fs.readdirSync(dir)) {
    try {
      if (fs.statSync(path.join(dir, f)).mtimeMs < cut) fs.rmSync(path.join(dir, f))
    } catch {}
  }
  let size = 0
  try {
    size = fs.statSync(current).size
  } catch {}
  const out = fs.createWriteStream(current, { flags: 'a' })
  let full = false
  return {
    write(text) {
      if (full) return
      if (size + text.length > LOG_DAY_MAX) {
        full = true
        out.write('--- this log reached its size limit for today\n')
        return
      }
      size += text.length
      out.write(text)
    },
  }
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
  contextMenu.setup(win, send)
  // The window keeps its own title (the page's would hide "(dev)").
  win.on('page-title-updated', (e) => e.preventDefault())
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
  const appLog = openAppLog()
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

// Sharing a screen or window: the window shows its own picker (thumbnails of
// every screen and window); its answer comes back here. Sound with the share
// is Windows only (Chromium's "loopback"). Automated tests pick the first
// screen (ERIS_AUTO_SHARE).
const picks = new Map() // request number -> resolve
let pickCount = 0
// The game share for spectators (Play Together) takes the main screen, the
// one the host helper streams, without asking.
let mainScreenNext = false
ipcMain.handle('share-main-screen-next', () => (mainScreenNext = true))
function shareHandler(_request, callback) {
  // Screens and windows asked for apart: together, one failing (no window
  // list on some desktops) leaves both empty.
  const get = (type) => desktopCapturer.getSources({ types: [type], thumbnailSize: { width: 320, height: 180 }, fetchWindowIcons: false }).catch(() => [])
  Promise.all([get('screen'), get('window')])
    .then(([screens, windows]) => [...screens, ...windows])
    .then(async (sources) => {
      let choice = null
      if (mainScreenNext) {
        mainScreenNext = false
        const id = String(screen.getPrimaryDisplay().id)
        const s = sources.find((s) => s.display_id === id) || sources.find((s) => s.id.startsWith('screen:'))
        choice = { id: s?.id, audio: false }
      } else if (process.env.ERIS_AUTO_SHARE) {
        choice = { id: sources.find((s) => s.id.startsWith('screen:'))?.id, audio: false }
      } else {
        const n = ++pickCount
        const list = sources
          .filter((s) => s.name !== 'Eris' && s.name !== 'Eris (dev)')
          .map((s) => ({ id: s.id, name: s.name, screen: s.id.startsWith('screen:'), thumb: s.thumbnail.toDataURL() }))
        send('pick-share', { n, sources: list, canAudio: process.platform === 'win32' })
        choice = await new Promise((resolve) => picks.set(n, resolve))
      }
      const source = choice && sources.find((s) => s.id === choice.id)
      if (!source) return callback({})
      callback({ video: source, ...(choice.audio && process.platform === 'win32' ? { audio: 'loopback' } : {}) })
    })
    .catch(() => callback({}))
}
ipcMain.handle('share-picked', (_e, { n, id, audio }) => {
  picks.get(n)?.(id ? { id, audio } : null)
  picks.delete(n)
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

// The end of a helper's log, for diagnostics (only logs in our own folder).
ipcMain.handle('log-tail', (_e, file) => {
  const dir = userFile('logs') + path.sep
  const p = path.resolve(String(file || ''))
  if (!p.startsWith(dir)) return []
  try {
    return fs.readFileSync(p, 'utf8').split('\n').slice(-300)
  } catch {
    return []
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
    session.defaultSession.setDisplayMediaRequestHandler(shareHandler)
    linuxInstall.integrate() // app menu and desktop icon, like an installer (AppImage only)
    createWindow()
    updater.setup((s) => send('update', s))
  })
  app.on('window-all-closed', () => {
    for (const role of Object.keys(helpers)) stopHelper(role)
    app.quit()
  })
}
