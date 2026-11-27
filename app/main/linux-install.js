// Installing on Linux the normal way: download the AppImage from Eris's
// releases, make it runnable, run it. On each start the AppImage puts Eris in
// the app menu (~/.local/share/applications/eris.desktop) and, the first
// time, an icon on the desktop; both are kept pointing at the AppImage after
// it updates itself (an update may give it a new file name). The same for
// the server's own desktop icon, when one was added (Host: "Add a desktop
// icon for the server"). Windows gets all this from its installer.
//
// Only a packaged AppImage does this; a dev build (tools/dev.sh) never does.
const { app } = require('electron')
const path = require('node:path')
const fs = require('node:fs')
const os = require('node:os')
const { execFileSync } = require('node:child_process')
const crypto = require('node:crypto')

function home(...p) {
  return path.join(os.homedir(), ...p)
}

function desktopDir() {
  try {
    const d = execFileSync('xdg-user-dir', ['DESKTOP'], { encoding: 'utf8', timeout: 3000 }).trim()
    if (d && d !== os.homedir()) return d
  } catch {}
  return home('Desktop')
}

function quoted(p) {
  return /[\s"'\\$`]/.test(p) ? `"${p.replace(/(["\\$`])/g, '\\$1')}"` : p
}

function entry(appImage, icon, server) {
  return [
    '[Desktop Entry]',
    'Type=Application',
    server ? 'Name=Eris Server' : 'Name=Eris',
    server
      ? 'Comment=Starts your Eris server in its own window; closing that window stops it'
      : 'Comment=Rooms, chat, voice and game streaming on your own server',
    server ? `Exec=${quoted(appImage)} --server` : `Exec=${quoted(appImage)} %U`,
    `Icon=${icon}`,
    'Terminal=false',
    server ? 'Categories=Network;' : 'Categories=Network;Chat;',
    'StartupWMClass=eris',
    '',
  ].join('\n')
}

// write puts text in a file if it isn't already exactly that; desktop icons
// are made runnable and trusted (GNOME asks otherwise).
function write(file, text, desktop) {
  try {
    if (fs.readFileSync(file, 'utf8') === text) return false
  } catch {}
  fs.mkdirSync(path.dirname(file), { recursive: true })
  fs.writeFileSync(file, text, { mode: 0o755 })
  fs.chmodSync(file, 0o755)
  if (desktop) {
    try {
      execFileSync('gio', ['set', file, 'metadata::trusted', 'true'], { timeout: 3000 })
    } catch {}
  }
  return true
}

function stateFile() {
  return path.join(app.getPath('userData'), 'install.json')
}

function readState() {
  try {
    return JSON.parse(fs.readFileSync(stateFile(), 'utf8'))
  } catch {
    return {}
  }
}

function saveState(s) {
  fs.mkdirSync(path.dirname(stateFile()), { recursive: true })
  fs.writeFileSync(stateFile(), JSON.stringify(s, null, 2))
}

function appImage() {
  return process.platform === 'linux' && app.isPackaged ? process.env.APPIMAGE || '' : ''
}

// iconPath puts the app's icon where the menu and desktop icons point. Its
// file name carries a hash of the picture: GNOME remembers icons by file
// name, so a new picture needs a new name to show up. Older ones are removed.
function iconPath() {
  const dir = home('.local', 'share', 'eris')
  try {
    const want = fs.readFileSync(path.join(__dirname, '..', 'build', 'icon.png'))
    const name = `eris-${crypto.createHash('sha256').update(want).digest('hex').slice(0, 10)}.png`
    const icon = path.join(dir, name)
    if (!fs.existsSync(icon)) {
      fs.mkdirSync(dir, { recursive: true })
      fs.writeFileSync(icon, want)
    }
    for (const f of fs.readdirSync(dir)) {
      if (/^eris(-[0-9a-f]+)?\.png$/.test(f) && f !== name) fs.rmSync(path.join(dir, f), { force: true })
    }
    return icon
  } catch {
    return path.join(dir, 'eris.png')
  }
}

// integrate runs at every start of the AppImage.
function integrate() {
  const img = appImage()
  if (!img) return
  try {
    const icon = iconPath()
    const s = readState()
    write(home('.local', 'share', 'applications', 'eris.desktop'), entry(img, icon, false), false)
    const desk = path.join(desktopDir(), 'eris.desktop')
    // The desktop icon: made the first time; afterwards only kept current,
    // never made again if it was removed.
    if (!s.desktopIcon && fs.existsSync(path.dirname(desk)) && !fs.existsSync(desk)) {
      write(desk, entry(img, icon, false), true)
      s.desktopIcon = desk
    } else if (s.desktopIcon && fs.existsSync(s.desktopIcon)) {
      write(s.desktopIcon, entry(img, icon, false), true)
    }
    if (s.serverIcon && fs.existsSync(s.serverIcon)) write(s.serverIcon, entry(img, icon, true), true)
    s.appImage = img
    saveState(s)
  } catch (err) {
    console.error('linux install:', err)
  }
}

// addServerIcon puts "Eris Server" on the desktop (Host's button).
function addServerIcon() {
  const img = appImage()
  if (!img) return { ok: false, error: 'Only the installed app (the AppImage) can add desktop icons.' }
  const file = path.join(desktopDir(), 'eris-server.desktop')
  write(file, entry(img, iconPath(), true), true)
  const s = readState()
  s.serverIcon = file
  saveState(s)
  return { ok: true, file }
}

function hasServerIcon() {
  const s = readState()
  return !!(s.serverIcon && fs.existsSync(s.serverIcon))
}

module.exports = { integrate, addServerIcon, hasServerIcon, canInstall: () => !!appImage() }
