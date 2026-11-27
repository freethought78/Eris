// The server dashboard page (dashboard.html): shows what dashboard.js says.
const $ = (id) => document.getElementById(id)
const words = { starting: 'Starting...', running: 'Running', stopping: 'Stopping...', stopped: 'Stopped', failed: 'Not running' }
let since = 0

function show(s) {
  $('status').className = 'pill ' + s.state
  $('statusText').textContent = words[s.state] || s.state
  if (s.name) $('name').textContent = s.name
  $('link').textContent = s.link || '-'
  $('owner').textContent = s.ownerCode || 'used (the owner has joined)'
  $('version').textContent = s.version || '-'
  $('data').textContent = s.data || '-'
  $('error').hidden = !s.error
  $('error').textContent = s.error || ''
  const up = s.state === 'running' || s.state === 'starting'
  $('start').hidden = up || s.state === 'stopping'
  $('restart').disabled = !up
  $('stop').textContent = up || s.state === 'stopping' ? 'Stop server' : 'Close'
  since = s.state === 'running' ? s.since : 0
  tick()
}

function tick() {
  if (!since) return ($('uptime').textContent = '')
  const m = Math.floor((Date.now() - since) / 60000)
  $('uptime').textContent = ` · up ${m < 60 ? m + ' min' : Math.floor(m / 60) + ' h ' + (m % 60) + ' min'}`
}
setInterval(tick, 30000)

function addLine(l) {
  const log = $('log')
  const atEnd = log.scrollTop + log.clientHeight >= log.scrollHeight - 8
  log.textContent += l + '\n'
  if (atEnd) log.scrollTop = log.scrollHeight
}

$('stop').onclick = () => window.dash.stop()
$('start').onclick = () => window.dash.start()
$('restart').onclick = () => window.dash.restart()
window.dash.onState(show)
window.dash.onLine(addLine)
window.dash.state().then((s) => {
  show(s)
  $('log').textContent = s.lines.join('\n') + '\n'
  $('log').scrollTop = $('log').scrollHeight
})
