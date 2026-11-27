// Renders build/icon.svg to PNGs with Electron's Chromium (no other SVG tool
// needed): npx electron tools/render-icon.cjs OUTDIR SIZE...
const { app, BrowserWindow } = require('electron')
const fs = require('node:fs')
const path = require('node:path')

const [outDir, ...sizes] = process.argv.slice(2).filter((a) => !a.startsWith("--"))
const svg = fs.readFileSync(path.join(__dirname, '..', 'build', 'icon.svg'), 'utf8')

app.disableHardwareAcceleration()
app.on('window-all-closed', () => {}) // one window per size; don't quit between them
process.on('unhandledRejection', (e) => { console.error(e); app.exit(1) })
app.whenReady().then(async () => {
  fs.mkdirSync(outDir, { recursive: true })
  for (const s of sizes.map(Number)) {
    const win = new BrowserWindow({ width: s, height: s, show: false, transparent: true, frame: false, useContentSize: true,
      webPreferences: { offscreen: true } })
    const html = `<html><body style="margin:0;background:transparent;overflow:hidden">
      <img src="data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}" width="${s}" height="${s}" style="display:block"></body></html>`
    await win.loadURL('data:text/html;base64,' + Buffer.from(html).toString('base64'))
    await new Promise((r) => setTimeout(r, 300))
    const img = await win.webContents.capturePage({ x: 0, y: 0, width: s, height: s })
    fs.writeFileSync(path.join(outDir, `${s}x${s}.png`), img.resize({ width: s, height: s, quality: 'best' }).toPNG())
    win.destroy()
  }
  app.quit()
})
