// The server dashboard window's calls to the main process (dashboard.js).
const { contextBridge, ipcRenderer } = require('electron')

contextBridge.exposeInMainWorld('dash', {
  state: () => ipcRenderer.invoke('dash-state'),
  stop: () => ipcRenderer.invoke('dash-stop'),
  start: () => ipcRenderer.invoke('dash-start'),
  restart: () => ipcRenderer.invoke('dash-restart'),
  checkUpdate: () => ipcRenderer.invoke('dash-update'),
  onState: (fn) => ipcRenderer.on('state', (_e, s) => fn(s)),
  onLine: (fn) => ipcRenderer.on('line', (_e, l) => fn(l)),
})
