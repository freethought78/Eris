import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)

// `import x from 'pkg/file.wasm?base64'` gives the file as a base64 string,
// bundled into the code: the window is a local file, where fetching more
// files can be refused (src/lib/noise.js).
function base64() {
  return {
    name: 'eris-base64',
    enforce: 'pre',
    resolveId(id) {
      if (id.endsWith('?base64')) return '\0b64:' + require.resolve(id.slice(0, -'?base64'.length))
    },
    load(id) {
      if (id.startsWith('\0b64:')) return `export default ${JSON.stringify(readFileSync(id.slice(5)).toString('base64'))}`
    },
  }
}

// The window's code: src/ built into dist/ (loaded from disk by main/main.js).
export default defineConfig({
  root: 'src',
  base: './',
  plugins: [base64(), svelte()],
  build: { outDir: '../dist', emptyOutDir: true },
})
