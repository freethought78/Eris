import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// The window's code: src/ built into dist/ (loaded from disk by main/main.js).
export default defineConfig({
  root: 'src',
  base: './',
  plugins: [svelte()],
  build: { outDir: '../dist', emptyOutDir: true },
})
