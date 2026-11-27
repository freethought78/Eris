// Live updates (DESIGN.md section 6, "Order, revised"): the app follows its
// servers. It checks Eris's GitHub releases when a server it connects to is
// newer than it (conn.svelte.js), or when asked (Check for updates), says
// when there's a newer version, and on the user's word downloads it and
// restarts into it. It asks; it never updates behind the user's back.
//
// Works in the Windows installer's copy and in the Linux AppImage. A copy run
// from the source folder doesn't update (its state is "dev"). ERIS_UPDATE_URL
// points it at another place with the same files (latest.yml and the
// installers), for testing.
const { app } = require('electron')
const { autoUpdater } = require('electron-updater')

let state = { state: 'idle' }
let send = () => {}

function set(s) {
  state = s
  send(state)
}

function canUpdate() {
  if (process.env.ERIS_UPDATE_URL) return true
  if (!app.isPackaged) return false
  if (process.platform === 'linux' && !process.env.APPIMAGE) return false // only the AppImage can replace itself
  return true
}

function check() {
  if (!canUpdate()) {
    set({ state: 'dev' })
    return
  }
  autoUpdater.checkForUpdates().catch((err) => set({ state: 'error', error: String(err.message || err) }))
}

function setup(sendFn) {
  send = sendFn
  autoUpdater.autoDownload = false
  autoUpdater.autoInstallOnAppQuit = false
  autoUpdater.allowPrerelease = false
  if (process.env.ERIS_UPDATE_URL) {
    autoUpdater.forceDevUpdateConfig = true
    autoUpdater.setFeedURL({ provider: 'generic', url: process.env.ERIS_UPDATE_URL })
  }
  autoUpdater.on('checking-for-update', () => set({ state: 'checking' }))
  autoUpdater.on('update-available', (i) => set({ state: 'available', version: i.version }))
  autoUpdater.on('update-not-available', () => set({ state: 'latest', version: app.getVersion() }))
  autoUpdater.on('download-progress', (p) => set({ ...state, state: 'downloading', percent: Math.round(p.percent) }))
  autoUpdater.on('update-downloaded', (i) => set({ state: 'ready', version: i.version }))
  autoUpdater.on('error', (err) => set({ state: 'error', error: String(err.message || err) }))
  if (!canUpdate()) set({ state: 'dev' })
}

module.exports = {
  setup,
  check,
  status: () => state,
  download: () => autoUpdater.downloadUpdate().catch((err) => set({ state: 'error', error: String(err.message || err) })),
  install: () => autoUpdater.quitAndInstall(false, true), // not silent; start again afterwards
}
