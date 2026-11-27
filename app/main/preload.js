// The window's only way to the main process: a few named calls.
const { contextBridge, ipcRenderer } = require('electron')

contextBridge.exposeInMainWorld('eris', {
  info: () => ipcRenderer.invoke('info'),
  loadServers: () => ipcRenderer.invoke('servers-load'),
  saveServers: (list) => ipcRenderer.invoke('servers-save', list),
  api: (req) => ipcRenderer.invoke('api', req),
  pin: (address, pin) => ipcRenderer.invoke('pin', { address, pin }),
  upload: (req) => ipcRenderer.invoke('upload', req),
  download: (url) => ipcRenderer.invoke('download', url),
  startHelper: (role, opts) => ipcRenderer.invoke('helper-start', { role, opts }),
  stopHelper: (role) => ipcRenderer.invoke('helper-stop', role),
  sendHelper: (role, msg) => ipcRenderer.invoke('helper-send', { role, msg }),
  updateStatus: () => ipcRenderer.invoke('update-status'),
  checkUpdate: () => ipcRenderer.invoke('update-check'),
  downloadUpdate: () => ipcRenderer.invoke('update-download'),
  installUpdate: () => ipcRenderer.invoke('update-install'),
  onUpdate: (fn) => {
    const h = (_e, data) => fn(data)
    ipcRenderer.on('update', h)
    return () => ipcRenderer.removeListener('update', h)
  },
  onHelper: (fn) => {
    const h = (_e, data) => fn(data)
    ipcRenderer.on('helper', h)
    return () => ipcRenderer.removeListener('helper', h)
  },
})
