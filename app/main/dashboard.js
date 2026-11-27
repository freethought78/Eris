// The server dashboard: `Eris --server` runs this PC's eris-server and shows
// it in a small window, with its log and a Stop button. The server lives
// exactly as long as the window: closing the window stops it, and the server
// runs with -exit-with-stdin, so if this app dies any other way (a crash, a
// kill, logging out) the server's stdin closes and it stops too.
//
// Its settings are in dashboard.json in its own data folder (~/.config/
// Eris-server on Linux): {"data": folder, "addr": ":7300", "public": name}.
const { app, BrowserWindow, ipcMain } = require('electron')
const path = require('node:path')
const fs = require('node:fs')
const readline = require('node:readline')
const { spawn } = require('node:child_process')

let win = null
let server = null // the child process, while it runs
let state = { state: 'starting', lines: [] } // starting, running, stopping, stopped, failed
let closing = false
let serverLog = null

function file(name) {
  return path.join(app.getPath('userData'), name)
}

function serverExe() {
  const exe = process.platform === 'win32' ? 'eris-server.exe' : 'eris-server'
  if (app.isPackaged) return path.join(process.resourcesPath, 'server', exe)
  return path.join(__dirname, '..', '..', 'server', 'bin', exe)
}

function settings() {
  let s = {}
  try {
    s = JSON.parse(fs.readFileSync(file('dashboard.json'), 'utf8'))
  } catch {}
  const out = { data: s.data || file('server-data'), addr: s.addr || ':7300', public: s.public || '' }
  if (!fs.existsSync(file('dashboard.json'))) fs.writeFileSync(file('dashboard.json'), JSON.stringify(out, null, 2) + '\n')
  return out
}

function update(patch) {
  state = { ...state, ...patch }
  if (win && !win.isDestroyed()) win.webContents.send('state', { ...state, lines: undefined })
}

function line(text) {
  state.lines.push(text)
  if (state.lines.length > 1000) state.lines.splice(0, state.lines.length - 1000)
  serverLog?.write(text + '\n')
  if (win && !win.isDestroyed()) win.webContents.send('line', text)
  // What the server prints that the dashboard shows on its own.
  const v = text.match(/eris-server (\S+): "(.*)", data in (.*), listening on (\S+)/)
  if (v) update({ state: 'running', version: v[1], name: v[2], data: v[3], addr: v[4] })
  const j = text.match(/Join link: (\S+)/)
  if (j) update({ link: j[1] })
  const o = text.match(/Owner code \(first join only\): (\S+)/)
  if (o) update({ ownerCode: o[1] })
}

function startServer() {
  const s = settings()
  const exe = serverExe()
  if (!fs.existsSync(exe)) {
    update({ state: 'failed', error: `The server program is missing (${exe}).` })
    return
  }
  const args = ['-exit-with-stdin', '-data', s.data, '-addr', s.addr]
  if (s.public) args.push('-public', s.public)
  if (app.isPackaged) args.push('-no-update-check') // it updates with the app
  update({ state: 'starting', error: '', link: '', ownerCode: '', since: Date.now() })
  line(`--- ${new Date().toLocaleString()}: starting ${exe}`)
  const child = spawn(exe, args, { stdio: ['pipe', 'pipe', 'pipe'] })
  server = child
  for (const stream of [child.stdout, child.stderr]) {
    readline.createInterface({ input: stream }).on('line', (l) => l.trim() && line(l))
  }
  child.on('error', (err) => update({ state: 'failed', error: String(err) }))
  child.on('exit', (code, signal) => {
    if (server === child) server = null
    const how = signal ? `stopped by ${signal}` : `exited with ${code}`
    line(`--- the server ${how}`)
    if (closing) return finishClosing()
    update(state.state === 'stopping' || code === 0 ? { state: 'stopped' } : { state: 'failed', error: `The server ${how}; its last lines are below.` })
  })
}

// stopServer asks the server to stop (it closes members' connections and
// its database), and kills it if it hasn't after 5 s.
function stopServer() {
  const child = server
  if (!child) return
  update({ state: 'stopping' })
  try {
    child.stdin.write('stop\n')
    child.stdin.end()
  } catch {}
  setTimeout(() => child.exitCode === null && child.signalCode === null && child.kill('SIGKILL'), 5000)
}

function finishClosing() {
  if (win && !win.isDestroyed()) win.destroy()
  app.quit()
}

function createWindow() {
  win = new BrowserWindow({
    width: 760,
    height: 560,
    minWidth: 480,
    minHeight: 320,
    backgroundColor: '#1a1a2e',
    title: 'Eris server',
    icon: path.join(__dirname, '..', 'build', 'icon.png'),
    webPreferences: {
      preload: path.join(__dirname, 'dashboard-preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  })
  win.setMenuBarVisibility(false)
  win.loadFile(path.join(__dirname, 'dashboard.html'))
  // Closing the window stops the server first; the window goes when it has.
  win.on('close', (e) => {
    if (!server) return
    e.preventDefault()
    closing = true
    stopServer()
  })
}

function start() {
  const profile = process.env.ERIS_PROFILE ? '-' + process.env.ERIS_PROFILE : ''
  app.setPath('userData', path.join(app.getPath('appData'), 'Eris-server' + profile))
  // One dashboard (and so one server) at a time: a second start shows the first.
  if (!app.requestSingleInstanceLock()) {
    app.quit()
    return
  }
  app.on('second-instance', () => {
    if (win && !win.isDestroyed()) {
      if (win.isMinimized()) win.restore()
      win.focus()
    }
  })
  ipcMain.handle('dash-state', () => state)
  ipcMain.handle('dash-stop', () => {
    closing = true
    stopServer()
    if (!server) finishClosing()
  })
  ipcMain.handle('dash-start', () => !server && startServer())
  ipcMain.handle('dash-restart', () => {
    if (!server) return startServer()
    const child = server
    child.once('exit', () => setTimeout(startServer, 300))
    stopServer()
  })
  app.whenReady().then(() => {
    fs.mkdirSync(path.join(app.getPath('userData'), 'logs'), { recursive: true })
    serverLog = fs.createWriteStream(file(path.join('logs', 'server.log')), { flags: 'a' })
    createWindow()
    startServer()
  })
  app.on('window-all-closed', () => app.quit())
  // However the app ends, the server goes with it (and -exit-with-stdin
  // covers the ways this can't run).
  app.on('will-quit', () => {
    try {
      server?.stdin.end()
    } catch {}
  })
}

module.exports = { start }
