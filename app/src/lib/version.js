// newer says whether version a is newer than b ("0.2.0" > "0.1.9" > "0.1.0"
// > "0.1.0-dev"). The same rule as the server's (server/update.go).
export function newer(a, b) {
  const split = (v) => {
    const [main, suffix = ''] = String(v || '0').replace(/^v/, '').split('-', 2)
    const p = main.split('.').map((x) => parseInt(x, 10) || 0)
    return [[p[0] || 0, p[1] || 0, p[2] || 0], suffix]
  }
  const [pa, sa] = split(a)
  const [pb, sb] = split(b)
  for (let i = 0; i < 3; i++) if (pa[i] !== pb[i]) return pa[i] > pb[i]
  return sa === '' && sb !== ''
}
