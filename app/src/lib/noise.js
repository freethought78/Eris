// Strong noise reduction for the microphone: RNNoise (Xiph's small neural
// network for speech), run in an AudioWorklet on this PC, through
// @sapphi-red/web-noise-suppressor. It plugs into LiveKit as the microphone
// track's processor: what's sent is the cleaned sound.
//
// The filter and its WebAssembly are part of the app's code (the window is a
// local file, where fetching more files can be refused): the worklet is
// shipped next to it, the WebAssembly inlined as base64 (vite.config.js).
// It runs in its own 48 kHz audio engine, the rate RNNoise works at, so
// deafening (which pauses the call's engine) doesn't touch it. About 10 ms
// of delay.
import { RnnoiseWorkletNode } from '@sapphi-red/web-noise-suppressor'
import workletUrl from '@sapphi-red/web-noise-suppressor/rnnoiseWorklet.js?url'
import plainWasm from '@sapphi-red/web-noise-suppressor/rnnoise.wasm?base64'
import simdWasm from '@sapphi-red/web-noise-suppressor/rnnoise_simd.wasm?base64'

// A tiny module that uses SIMD: if it validates, the faster build runs.
const SIMD_TEST = new Uint8Array([0, 97, 115, 109, 1, 0, 0, 0, 1, 5, 1, 96, 0, 1, 123, 3, 2, 1, 0, 10, 10, 1, 8, 0, 65, 0, 253, 15, 253, 98, 11])

let wasm = null
function wasmBinary() {
  if (!wasm) {
    const bin = atob(WebAssembly.validate(SIMD_TEST) ? simdWasm : plainWasm)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    wasm = bytes.buffer
  }
  return wasm
}

let ctx = null
let ready = null
async function engine() {
  if (!ctx || ctx.state === 'closed') {
    ctx = new AudioContext({ sampleRate: 48000, latencyHint: 'interactive' })
    ready = ctx.audioWorklet.addModule(workletUrl)
  }
  await ready
  if (ctx.state === 'suspended') await ctx.resume().catch(() => {})
  return ctx
}

// Rnnoise is a LiveKit track processor (track.setProcessor).
export class Rnnoise {
  name = 'rnnoise'
  processedTrack = undefined
  #src = null
  #node = null
  #dest = null

  async init({ track }) {
    const c = await engine()
    this.#src = c.createMediaStreamSource(new MediaStream([track]))
    this.#node = new RnnoiseWorkletNode(c, { maxChannels: 1, wasmBinary: wasmBinary() })
    this.#dest = c.createMediaStreamDestination()
    this.#src.connect(this.#node)
    this.#node.connect(this.#dest)
    this.processedTrack = this.#dest.stream.getAudioTracks()[0]
  }

  async restart(opts) {
    await this.destroy()
    await this.init(opts)
  }

  async destroy() {
    this.#src?.disconnect()
    this.#node?.disconnect()
    this.#node?.destroy?.()
    this.processedTrack?.stop()
    this.#src = this.#node = this.#dest = null
    this.processedTrack = undefined
  }
}
