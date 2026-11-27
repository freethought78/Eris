// Eris app, main process: the window, the saved server list, HTTP calls to
// servers, and the streaming helper (DESIGN.md section 4; the helper is
// helper/, started here and talked to with JSON lines on stdin and stdout).
const { app, BrowserWindow, ipcMain, screen } = require('electron')
const path = require('node:path')
const fs = require('node:fs')
const readline = require('node:readline')
const { spawn, execFileSync } = require('node:child_process')

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
  if (!fs.existsSync(exe)) {
    send('helper', { role, msg: { type: 'error', error: `The streaming helper is missing (${exe}).` } })
    return
  }
  let args = ['-signal', 'stdio']
  if (role === 'host') {
    const d = screen.getPrimaryDisplay()
    const b = d.bounds
    const s = d.scaleFactor || 1
    const region = `${Math.round(b.width * s)}x${Math.round(b.height * s)}+${Math.round(b.x * s)}+${Math.round(b.y * s)}`
    args.push('-region', region, '-udp-port', String(opts.udpPort || 7401), '-pad')
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
      send('helper', { role, msg: JSON.parse(line) })
    } catch {
      log.write(line + '\n')
    }
  })
  child.on('exit', (code) => {
    if (helpers[role] === child) delete helpers[role]
    send('helper', { role, msg: { type: 'exit', code, log: logPath } })
  })
  child.on('error', (err) => send('helper', { role, msg: { type: 'error', error: String(err) } }))
}

function stopHelper(role) {
  const child = helpers[role]
  if (!child) return
  delete helpers[role]
  try {
    child.stdin.write(JSON.stringify({ type: 'stop' }) + '\n')
  } catch {}
  setTimeout(() => child.kill(), 1500)
}

function createWindow() {
  win = new BrowserWindow({
    width: 1100,
    height: 720,
    backgroundColor: '#1a1a2e',
    title: 'Eris',
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  })
  win.setMenuBarVisibility(false)
  win.loadFile(path.join(__dirname, '..', 'dist', 'index.html'))
}

// For automated tests: --auto='{"join":...}' is handed to the window.
function autoConfig() {
  const a = process.argv.find((x) => x.startsWith('--auto='))
  if (!a) return null
  try {
    return JSON.parse(a.slice('--auto='.length))
  } catch {
    return null
  }
}

ipcMain.handle('servers-load', () => {
  try {
    return JSON.parse(fs.readFileSync(userFile('servers.json'), 'utf8'))
  } catch {
    return []
  }
})

ipcMain.handle('servers-save', (_e, list) => {
  fs.mkdirSync(app.getPath('userData'), { recursive: true })
  fs.writeFileSync(userFile('servers.json'), JSON.stringify(list, null, 2), { mode: 0o600 })
})

// HTTP to a server, from here rather than the window (no cross-origin limits).
ipcMain.handle('api', async (_e, { method, url, token, body }) => {
  try {
    const res = await fetch(url, {
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
    return { status: 0, json: { error: String(err.cause?.code || err.message || err) } }
  }
})

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
  auto: autoConfig(),
}))

app.whenReady().then(createWindow)
app.on('window-all-closed', () => {
  for (const role of Object.keys(helpers)) stopHelper(role)
  app.quit()
})
