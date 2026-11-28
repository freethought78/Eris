// The window's right-click menu: spelling suggestions, cut, copy, paste and
// select all in text boxes, copy elsewhere, and copy link or image. Paste
// also takes files (copied in a file manager) and pictures from the
// clipboard: they're attached to the message being written, as when
// they're dropped in or pasted with the keyboard.
const { Menu, clipboard, ipcMain } = require('electron')
const fs = require('node:fs')
const path = require('node:path')
const { fileURLToPath } = require('node:url')

const TYPES = {
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', bmp: 'image/bmp', svg: 'image/svg+xml',
  mp3: 'audio/mpeg', ogg: 'audio/ogg', oga: 'audio/ogg', opus: 'audio/ogg', wav: 'audio/wav', flac: 'audio/flac', m4a: 'audio/mp4', aac: 'audio/aac',
  mp4: 'video/mp4', webm: 'video/webm', ogv: 'video/ogg', mov: 'video/quicktime', mkv: 'video/x-matroska',
  pdf: 'application/pdf', txt: 'text/plain', md: 'text/markdown', json: 'application/json', zip: 'application/zip',
}
const MAX = 2 * 1024 * 1024 * 1024 // not read past this; the server has its own limit

// Electron's clipboard is asynchronous, with ClipboardItems by MIME type;
// a system format it doesn't map is read raw.
const raw = (f) => `electron application/osclipboard;format="${f}"`

async function readType(items, type) {
  for (const it of items) {
    if (!it.types.includes(type)) continue
    try {
      const v = await it.getType(type)
      return v
    } catch {}
  }
  return null
}

const asText = async (v) => (v == null ? '' : typeof v === 'string' ? v : await v.text())

// copiedFiles: paths of files copied in a file manager (Linux: GNOME, KDE
// and others offer file:// links; Windows: the first file).
async function copiedFiles(items) {
  let text = ''
  for (const t of ['text/uri-list', raw('x-special/gnome-copied-files'), raw('x-special/KDE-copied-files')]) {
    text = await asText(await readType(items, t))
    if (!text && t.startsWith('electron') && (await clipboard.has(t).catch(() => false))) {
      text = await asText(await readType(await clipboard.read(), t))
    }
    if (text) break
  }
  if (!text) {
    const plain = await clipboard.readText().catch(() => '')
    if (/^file:\/\//m.test(plain)) text = plain
  }
  const paths = text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter((l) => l.startsWith('file://'))
    .map((l) => {
      try {
        return fileURLToPath(l)
      } catch {
        return ''
      }
    })
    .filter(Boolean)
  if (!paths.length && process.platform === 'win32' && (await clipboard.has(raw('FileNameW')).catch(() => false))) {
    const v = await readType(await clipboard.read(), raw('FileNameW'))
    const b = v && typeof v !== 'string' ? Buffer.from(await v.arrayBuffer()) : null
    const p = b ? b.toString('ucs2').replace(/\0.*$/s, '') : ''
    if (p) paths.push(p)
  }
  return paths
}

// clipboardFiles: what's on the clipboard as files ({name, type, data}), or
// [] when it's plain text.
async function clipboardFiles() {
  const items = await clipboard.read().catch(() => [])
  const out = []
  for (const p of await copiedFiles(items)) {
    try {
      const st = fs.statSync(p)
      if (!st.isFile() || st.size > MAX) continue
      const ext = path.extname(p).slice(1).toLowerCase()
      out.push({ name: path.basename(p), type: TYPES[ext] || 'application/octet-stream', data: fs.readFileSync(p) })
    } catch {}
  }
  if (out.length) return out
  const img = await readType(items, 'image/png')
  if (img && typeof img !== 'string') {
    const d = new Date()
    const p = (n) => String(n).padStart(2, '0')
    out.push({ name: `pasted ${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}.${p(d.getMinutes())}.${p(d.getSeconds())}.png`, type: 'image/png', data: Buffer.from(await img.arrayBuffer()) })
  }
  return out
}

function setup(win, send) {
  // The keyboard's paste (Ctrl+V, Shift+Insert) asks here when the
  // clipboard holds files the page can't see by itself.
  ipcMain.handle('clipboard-files', () => clipboardFiles())

  win.webContents.on('context-menu', (_e, p) => {
    const wc = win.webContents
    const items = []
    if (p.misspelledWord) {
      for (const s of p.dictionarySuggestions.slice(0, 6)) items.push({ label: s, click: () => wc.replaceMisspelling(s) })
      if (!p.dictionarySuggestions.length) items.push({ label: 'No suggestions', enabled: false })
      items.push({ label: `Add "${p.misspelledWord}" to the dictionary`, click: () => wc.session.addWordToSpellCheckerDictionary(p.misspelledWord) })
      items.push({ type: 'separator' })
    }
    if (p.isEditable) {
      items.push(
        { label: 'Cut', accelerator: 'CmdOrCtrl+X', enabled: p.editFlags.canCut, click: () => wc.cut() },
        { label: 'Copy', accelerator: 'CmdOrCtrl+C', enabled: p.editFlags.canCopy, click: () => wc.copy() },
        {
          label: 'Paste',
          accelerator: 'CmdOrCtrl+V',
          click: async () => {
            const files = await clipboardFiles()
            if (files.length) send('paste-files', files)
            else wc.paste()
          },
        },
        { type: 'separator' },
        { label: 'Select all', accelerator: 'CmdOrCtrl+A', click: () => wc.selectAll() },
      )
    } else if (p.selectionText) {
      items.push({ label: 'Copy', accelerator: 'CmdOrCtrl+C', click: () => wc.copy() })
    }
    if (p.linkURL && /^https?:/.test(p.linkURL)) items.push({ label: 'Copy link', click: () => clipboard.writeText(p.linkURL).catch(() => {}) })
    if (p.mediaType === 'image' && p.hasImageContents) items.push({ label: 'Copy image', click: () => wc.copyImageAt(p.x, p.y) })
    while (items.length && items[items.length - 1].type === 'separator') items.pop()
    if (items.length) Menu.buildFromTemplate(items).popup({ window: win })
  })
}

module.exports = { setup, clipboardFiles }
