// The app's own updates, as the main process reports them (main/updater.js).
class Update {
  state = $state('idle') // idle, dev, checking, latest, available, downloading, ready, error
  version = $state('')
  percent = $state(0)
  error = $state('')

  constructor() {
    window.eris.onUpdate((s) => this.#set(s))
    window.eris.updateStatus().then((s) => this.#set(s))
  }

  #set(s) {
    if (s.state !== this.state) console.info(`update: ${s.state}${s.version ? ' ' + s.version : ''}${s.error ? ' (' + s.error + ')' : ''}`)
    this.state = s.state
    this.version = s.version || ''
    this.percent = s.percent || 0
    this.error = s.error || ''
  }

  check() {
    window.eris.checkUpdate()
  }
  download() {
    window.eris.downloadUpdate()
  }
  install() {
    window.eris.installUpdate()
  }
}

export const update = new Update()
