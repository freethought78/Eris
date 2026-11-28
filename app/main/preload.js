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
  hostStatus: () => ipcRenderer.invoke('host-status'),
  hostStart: () => ipcRenderer.invoke('host-start'),
  hostAction: (what) => ipcRenderer.invoke('host-action', what),
  hostLog: () => ipcRenderer.invoke('host-log'),
  logTail: (file) => ipcRenderer.invoke('log-tail', file),
  hostAddIcon: () => ipcRenderer.invoke('host-add-icon'),
  onPickShare: (fn) => {
    const h = (_e, data) => fn(data)
    ipcRenderer.on('pick-share', h)
    return () => ipcRenderer.removeListener('pick-share', h)
  },
  shareMainScreenNext: () => ipcRenderer.invoke('share-main-screen-next'),
  clipboardFiles: () => ipcRenderer.invoke('clipboard-files'),
  onPasteFiles: (fn) => {
    const h = (_e, data) => fn(data)
    ipcRenderer.on('paste-files', h)
    return () => ipcRenderer.removeListener('paste-files', h)
  },
  sharePicked: (n, id, audio) => ipcRenderer.invoke('share-picked', { n, id, audio }),
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
