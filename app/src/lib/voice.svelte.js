// Voice and cameras in a room (DESIGN.md 4.4), with LiveKit: the server gives
// a token for the room's voice (server/voice.go), and LiveKit's signalling
// goes through the server's own port, encrypted and pinned like everything
// else. One room's voice at a time, across all servers.
//
// Reading a room and being in its voice are separate: joining is deliberate
// (double-click the room, or Join voice).
//
// Nothing is received automatically except voices and cameras: a screen
// share is only sent to those who press Watch (LiveKit then sends it only
// while someone watches), to keep traffic down.
import { AudioPresets, Room, RoomEvent, Track, VideoPresets, createLocalScreenTracks } from 'livekit-client'
import { httpBase } from './conn.svelte.js'
import { Rnnoise } from './noise.js'

const store = {
  get(key, fallback) {
    try {
      const v = localStorage.getItem('eris-voice:' + key)
      return v === null ? fallback : JSON.parse(v)
    } catch {
      return fallback
    }
  },
  set(key, value) {
    try {
      localStorage.setItem('eris-voice:' + key, JSON.stringify(value))
    } catch {}
  },
}

class Voice {
  conn = $state(null) // the server we're in voice on
  roomId = $state(0)
  state = $state('') // '', connecting, connected, reconnecting
  muted = $state(store.get('muted', false))
  deafened = $state(false)
  camera = $state(false)
  people = $state([]) // {id, name, speaking, muted, camera, local}
  tiles = $state([]) // {id, name, track, local}: cameras on
  shares = $state([]) // {id, name, local}: screens being shared
  watching = $state('') // whose share we receive
  stage = $state(null) // {id, name, track}: the share being watched
  myShare = $state(null) // {track}: our own share, for its preview
  sharing = $state(false) // we share our screen
  game = $state(false) // our share is our game, for spectators (Play Together)
  error = $state('')
  hearing = $state(0) // others' audio tracks being played
  volumes = $state(store.get('volumes', {})) // member id -> 0..1
  devices = $state(store.get('devices', {})) // audioinput, audiooutput, videoinput -> device id
  // Noise reduction on our microphone: 'off', 'standard' (the browser
  // engine's own, the lightest) or 'strong' (RNNoise, noise.js).
  noise = $state(store.get('noise', 'strong'))

  #room = null
  // Who's speaking, measured here from each voice's own sound (see #meter).
  #meterCtx = null
  #meters = new Map() // track sid -> {id, an, buf, src, last}
  #meterTimer = 0
  #talking = new Set() // identities lit now
  #gameWanted = false // we're live for Play Together in this call
  #gameDeclined = false // ...but its share was stopped by hand
  #gameTracks = [] // the game share's video and sound
  #ctx = null // the call's audio engine: volumes above 100%, with a limiter
  #audio = new Map() // track sid -> element
  #box = null

  isIn(conn, roomId) {
    return this.conn === conn && this.roomId === roomId && !!this.state
  }

  async join(conn, roomId) {
    if (this.isIn(conn, roomId)) return
    if (this.state) await this.leave()
    this.error = ''
    this.conn = conn
    this.roomId = roomId
    this.state = 'connecting'
    const r = await window.eris.api({ method: 'POST', url: `${httpBase(conn.entry.address)}/api/rooms/${roomId}/voice`, token: conn.entry.token })
    if (r.status !== 200) {
      this.error = r.json?.error || `Couldn't join the voice (${r.status}).`
      this.#reset()
      return
    }
    // Voices play through an audio engine of ours, so a person's volume can
    // go up to 300%, with a limiter on each so loud moments don't crackle.
    this.#ctx = new AudioContext()
    const room = new Room({
      webAudioMix: { audioContext: this.#ctx },
      adaptiveStream: true,
      dynacast: true,
      audioCaptureDefaults: this.#capture(),
      videoCaptureDefaults: { resolution: VideoPresets.h540.resolution, deviceId: this.devices.videoinput },
      audioOutput: { deviceId: this.devices.audiooutput },
    })
    this.#room = room
    room
      .on(RoomEvent.TrackPublished, (pub) => (this.#policy(pub), this.#refresh()))
      .on(RoomEvent.TrackUnpublished, () => this.#refresh())
      .on(RoomEvent.TrackSubscribed, (track, pub, p) => this.#subscribed(track, pub, p))
      .on(RoomEvent.TrackUnsubscribed, (track) => this.#unsubscribed(track))
      .on(RoomEvent.ParticipantConnected, () => this.#refresh())
      .on(RoomEvent.ParticipantDisconnected, () => this.#refresh())
      .on(RoomEvent.TrackMuted, () => this.#refresh())
      .on(RoomEvent.TrackUnmuted, () => (this.#meterMic(), this.#refresh()))
      .on(RoomEvent.LocalTrackPublished, () => (this.#meterMic(), this.#refresh()))
      .on(RoomEvent.LocalTrackUnpublished, () => (this.#meterMic(), this.#refresh()))
      // Unmuting or another microphone can mean a new track to measure.
      .on(RoomEvent.ActiveDeviceChanged, () => this.#meterMic())
      .on(RoomEvent.Reconnecting, () => (this.state = 'reconnecting'))
      .on(RoomEvent.Reconnected, () => ((this.state = 'connected'), this.#refresh()))
      .on(RoomEvent.Disconnected, () => {
        if (this.#room === room) {
          this.conn?.send({ type: 'voice', room_id: 0 })
          this.#reset()
        }
      })
    try {
      await room.connect(httpBase(conn.entry.address).replace(/^https/, 'wss'), r.json.token, { autoSubscribe: false })
      for (const p of room.remoteParticipants.values()) for (const pub of p.trackPublications.values()) this.#policy(pub)
    } catch (err) {
      this.error = `Couldn't connect to the voice: ${err.message || err}`
      this.#reset()
      return
    }
    if (this.#room !== room) return // left while connecting
    this.#ctx?.resume().catch(() => {})
    this.state = 'connected'
    conn.send({ type: 'voice', room_id: roomId })
    console.info(`voice: joined room ${roomId} on ${conn.entry.address}`)
    try {
      await room.localParticipant.setMicrophoneEnabled(!this.muted && !this.deafened)
      await this.#applyNoise()
    } catch (err) {
      this.error = `Your microphone couldn't be used (${err.message || err}); you can still listen.`
    }
    this.#refresh()
  }

  async leave() {
    const room = this.#room
    const conn = this.conn
    this.#reset()
    conn?.send({ type: 'voice', room_id: 0 })
    if (room) await room.disconnect()
  }

  // After the server connection comes back (its restart, say): say again
  // which voice we're in.
  rejoined(conn) {
    if (this.conn === conn && this.state) conn.send({ type: 'voice', room_id: this.roomId })
  }

  async toggleMute() {
    this.muted = !this.muted
    store.set('muted', this.muted)
    if (this.deafened && !this.muted) this.deafened = false
    await this.#applyMic()
    this.#applyDeafen()
  }

  async toggleDeafen() {
    this.deafened = !this.deafened
    this.#applyDeafen()
    await this.#applyMic()
  }

  async toggleCamera() {
    if (!this.#room) return
    try {
      await this.#room.localParticipant.setCameraEnabled(!this.camera)
      this.camera = !this.camera
    } catch (err) {
      this.error = `Your camera couldn't be used (${err.message || err}).`
    }
    this.#refresh()
  }

  // Screen sharing: the window shows a picker (main/main.js asks it).
  async toggleShare() {
    if (!this.#room) return
    if (this.game) {
      // Sharing by hand instead of the game.
      await this.#stopGame()
      this.#gameDeclined = true
    }
    try {
      await this.#room.localParticipant.setScreenShareEnabled(
        !this.sharing,
        {
          // The share's sound (Windows): the system's sound minus Eris's own
          // (the call's voices), so nobody hears themselves; and none of the
          // voice processing that cuts music and game sound in and out.
          audio: { restrictOwnAudio: true, echoCancellation: false, noiseSuppression: false, autoGainControl: false },
          contentHint: 'detail',
          resolution: { width: 1920, height: 1080, frameRate: 30 },
        },
        { videoEncoding: { maxBitrate: 4_000_000, maxFramerate: 30 } },
      )
      // Whether the engine left our own sound out (for the diagnostics: it
      // can only be checked on Windows, in a real share).
      const audio = this.#room.localParticipant.getTrackPublication(Track.Source.ScreenShareAudio)?.track
      if (audio) console.info(`share sound: ${JSON.stringify(audio.mediaStreamTrack.getSettings())}`)
    } catch (err) {
      if (!/denied|cancel|abort/i.test(String(err.message || err))) this.error = `Couldn't share your screen (${err.message || err}).`
    }
    this.#refresh()
    // No share by hand now, while live for Play Together: the game again.
    if (!this.sharing && this.#gameWanted) {
      this.#gameDeclined = false
      this.setGameShare(true)
    }
  }

  // While we're live for Play Together and in a voice room on that server,
  // the game also goes to the call, as our share, with the game's sound
  // (without the call's voices: the host helper's "Eris-game-sound"
  // device). Spectators press Watch as for any share, and nothing is sent
  // until someone does. The player gets the low-latency stream from the
  // helper instead. App.svelte calls this whenever that changes.
  async setGameShare(on) {
    this.#gameWanted = on
    if (!on) {
      this.#gameDeclined = false
      await this.#stopGame()
      return
    }
    const room = this.#room
    if (!room || this.game || this.sharing || this.#gameDeclined) return
    this.game = true
    try {
      await window.eris.shareMainScreenNext()
      const [video] = await createLocalScreenTracks({ audio: false, resolution: { width: 1280, height: 720, frameRate: 30 }, contentHint: 'motion' })
      if (!this.game || this.#room !== room) return video.stop()
      this.#gameTracks.push(video)
      await room.localParticipant.publishTrack(video, { source: Track.Source.ScreenShare, name: 'game', simulcast: false, videoEncoding: { maxBitrate: 2_500_000, maxFramerate: 30 } })
      const sound = await this.#gameSound()
      if (sound && this.game && this.#room === room) {
        this.#gameTracks.push(sound)
        await room.localParticipant.publishTrack(sound, {
          source: Track.Source.ScreenShareAudio, name: 'game-sound', dtx: false, red: false, forceStereo: true, audioPreset: AudioPresets.musicHighQualityStereo,
        })
      } else if (sound) sound.stop()
      console.info(`game share: on, ${sound ? 'with' : 'without'} its sound`)
    } catch (err) {
      console.error(`game share: ${err.message || err}`)
      await this.#stopGame()
    }
    this.#refresh()
  }

  // The game's sound: the host helper's capture device (Linux), found by
  // name; it can take a moment to appear after going live.
  async #gameSound() {
    for (let i = 0; i < 10; i++) {
      const devs = await navigator.mediaDevices.enumerateDevices().catch(() => [])
      const d = devs.find((d) => d.kind === 'audioinput' && /Eris-game-sound/i.test(d.label))
      if (d) {
        const s = await navigator.mediaDevices.getUserMedia({
          audio: { deviceId: { exact: d.deviceId }, echoCancellation: false, noiseSuppression: false, autoGainControl: false, channelCount: 2 },
        })
        return s.getAudioTracks()[0]
      }
      await new Promise((r) => setTimeout(r, 300))
    }
    return null
  }

  // stopGame ends the game share (by hand: until we go live again).
  async stopGame() {
    await this.#stopGame()
    this.#gameDeclined = true
  }

  async #stopGame() {
    const tracks = this.#gameTracks
    this.#gameTracks = []
    const was = this.game
    this.game = false
    for (const t of tracks) {
      try {
        await this.#room?.localParticipant.unpublishTrack(t, true)
      } catch {}
      t.stop()
    }
    if (was) {
      console.info('game share: off')
      this.#refresh()
    }
  }

  // watch receives one share (and stops receiving the one watched before).
  watch(id) {
    const room = this.#room
    if (!room) return
    if (this.watching && this.watching !== id) this.#subscribeShare(this.watching, false)
    this.watching = id
    this.#subscribeShare(id, true)
    this.#refresh()
  }

  unwatch() {
    if (this.watching) this.#subscribeShare(this.watching, false)
    this.watching = ''
    this.#refresh()
  }

  #subscribeShare(id, on) {
    const p = this.#room?.remoteParticipants.get(String(id))
    if (!p) return
    for (const pub of p.trackPublications.values()) {
      if (pub.source === Track.Source.ScreenShare || pub.source === Track.Source.ScreenShareAudio) pub.setSubscribed(on)
    }
  }

  // #policy: voices and cameras come automatically; screen shares only when watched.
  #policy(pub) {
    if (!pub.setSubscribed) return // our own
    if (pub.source === Track.Source.ScreenShare || pub.source === Track.Source.ScreenShareAudio) {
      pub.setSubscribed(this.watching === pub.participantIdentity || this.watching === this.#ownerOf(pub))
    } else {
      pub.setSubscribed(true)
    }
  }

  #ownerOf(pub) {
    for (const p of this.#room?.remoteParticipants.values() || []) if (p.trackPublications.get(pub.trackSid) === pub) return p.identity
    return ''
  }

  // stats: the call's connection, for diagnostics (diag.svelte.js): round
  // trip and route, and for each track coming and going: loss, jitter,
  // picture size and frame rate, bytes, and what limits our sending.
  async stats() {
    const room = this.#room
    if (!room) return null
    const out = { room: this.roomId, people: room.numParticipants, quality: room.localParticipant.connectionQuality, muted: this.muted, deafened: this.deafened, noise: this.noise, tracks: [] }
    const read = async (track, from, source, dir) => {
      if (!track?.getRTCStatsReport) return
      const rep = await track.getRTCStatsReport()
      if (!rep) return
      rep.forEach((s) => {
        if (s.type === 'media-source' && s.kind === 'audio' && dir === 'out') {
          // Our microphone's loudness, as sent (0..1; ~0.1 is quiet speech).
          out.mic_level = level(s)
        }
        if (s.type === 'inbound-rtp' || s.type === 'outbound-rtp') {
          out.tracks.push({
            level: s.kind === 'audio' && s.type === 'inbound-rtp' ? level(s) : undefined,
            dir, from, source, kind: s.kind,
            lost: s.packetsLost, packets: s.packetsReceived ?? s.packetsSent, jitter_ms: s.jitter != null ? Math.round(s.jitter * 1000) : undefined,
            fps: s.framesPerSecond, w: s.frameWidth, h: s.frameHeight, bytes: s.bytesReceived ?? s.bytesSent,
            limited_by: s.qualityLimitationReason, codec: rep.get(s.codecId)?.mimeType,
          })
        } else if (s.type === 'candidate-pair' && s.nominated && s.state === 'succeeded' && out.rtt_ms == null) {
          out.rtt_ms = Math.round((s.currentRoundTripTime || 0) * 1000)
          const l = rep.get(s.localCandidateId)
          const r = rep.get(s.remoteCandidateId)
          out.route = `${l?.candidateType}/${l?.protocol} -> ${r?.candidateType}`
        }
      })
    }
    // Loudness: the average over the call (root of energy per second), and now.
    const level = (s) => ({
      avg: s.totalSamplesDuration > 0 ? Math.round(Math.sqrt(s.totalAudioEnergy / s.totalSamplesDuration) * 1000) / 1000 : undefined,
      now: s.audioLevel != null ? Math.round(s.audioLevel * 1000) / 1000 : undefined,
    })
    for (const p of room.remoteParticipants.values()) {
      for (const pub of p.trackPublications.values()) await read(pub.track, p.name || p.identity, pub.source, 'in')
    }
    for (const pub of room.localParticipant.trackPublications.values()) await read(pub.track, 'me', pub.source, 'out')
    return out
  }

  // receivingShare: whether someone's screen share is coming to us (tests:
  // it mustn't before Watch).
  receivingShare(id) {
    const p = this.#room?.remoteParticipants.get(String(id))
    const pub = p?.getTrackPublication(Track.Source.ScreenShare)
    return !!pub?.isSubscribed
  }

  // shareSound: whether someone's share's sound is coming to us (tests).
  shareSound(id) {
    const p = this.#room?.remoteParticipants.get(String(id))
    return !!p?.getTrackPublication(Track.Source.ScreenShareAudio)?.track
  }

  setVolume(id, v) {
    this.volumes[id] = v
    store.set('volumes', $state.snapshot(this.volumes))
    const p = this.#room?.remoteParticipants.get(String(id))
    p?.setVolume(v)
  }

  volume(id) {
    return this.volumes[id] ?? 1
  }

  // listDevices: each device once, as people know it. The system lists
  // some twice or more: Windows adds "Default - X" and "Communications - X"
  // entries for devices also listed as X, and PipeWire can list one device
  // under several profiles with the same name. The default entry is named
  // for the device it uses, and that device isn't listed again under it
  // (picking the default uses it anyway). USB codes like "(1852:5110)" that
  // Chromium adds to names are left out.
  async listDevices() {
    const tidy = (l) => l.replace(/\s*\([0-9a-f]{4}:[0-9a-f]{4}\)\s*$/i, '').trim()
    const out = {}
    for (const kind of ['audioinput', 'audiooutput', 'videoinput']) {
      let list = []
      try {
        list = await Room.getLocalDevices(kind, true)
      } catch {}
      const def = list.find((d) => d.deviceId === 'default')
      const defName = def ? tidy(def.label.replace(/^Default\s*-\s*/i, '')) : ''
      const seen = new Set(def && defName && defName !== tidy(def.label) ? [defName] : [])
      out[kind] = def ? [{ deviceId: 'default', label: defName && defName !== tidy(def.label) ? `System default: ${defName}` : 'System default' }] : []
      for (const d of list) {
        if (d.deviceId === 'default' || d.deviceId === 'communications') continue
        const name = tidy(d.label)
        if (seen.has(name) && d.deviceId !== this.devices[kind]) continue // (kept if picked by name before)
        seen.add(name)
        out[kind].push({ deviceId: d.deviceId, label: name })
      }
    }
    return out
  }

  async setDevice(kind, id) {
    this.devices[kind] = id
    store.set('devices', $state.snapshot(this.devices))
    if (this.#room) {
      try {
        await this.#room.switchActiveDevice(kind, id)
      } catch (err) {
        this.error = `Couldn't switch to that device (${err.message || err}).`
      }
    }
  }

  // #capture: how the microphone is opened. The browser engine's noise
  // suppression only for 'standard' (RNNoise does it for 'strong', and two
  // filters in a row chop speech); echo cancellation and loudness always.
  #capture() {
    return { echoCancellation: true, noiseSuppression: this.noise === 'standard', autoGainControl: true, deviceId: this.devices.audioinput }
  }

  // #applyNoise puts RNNoise on our microphone for 'strong', and takes it
  // off otherwise.
  async #applyNoise() {
    const track = this.#room?.localParticipant.getTrackPublication(Track.Source.Microphone)?.track
    if (!track) return
    const on = track.getProcessor()?.name === 'rnnoise'
    try {
      if (this.noise === 'strong' && !on) {
        await track.setProcessor(new Rnnoise())
        console.info('noise reduction: strong (RNNoise)')
      } else if (this.noise !== 'strong' && on) {
        await track.stopProcessor()
        console.info(`noise reduction: ${this.noise}`)
      }
    } catch (err) {
      console.error(`noise reduction: ${err.message || err}`)
      this.error = `Strong noise reduction couldn't start (${err.message || err}); using your microphone as it is.`
    }
    this.#meterMic()
  }

  async setNoise(mode) {
    this.noise = mode
    store.set('noise', mode)
    if (this.#room) this.#room.options.audioCaptureDefaults = { ...this.#room.options.audioCaptureDefaults, ...this.#capture() }
    const track = this.#room?.localParticipant.getTrackPublication(Track.Source.Microphone)?.track
    if (!track) return
    try {
      if (this.noise !== 'strong' && track.getProcessor()) await track.stopProcessor()
      await track.restartTrack(this.#capture()) // the browser engine's suppression on or off
    } catch (err) {
      console.error(`noise reduction: ${err.message || err}`)
    }
    await this.#applyNoise()
  }

  async #applyMic() {
    try {
      await this.#room?.localParticipant.setMicrophoneEnabled(!this.muted && !this.deafened)
      await this.#applyNoise()
    } catch (err) {
      this.error = `Your microphone couldn't be used (${err.message || err}).`
    }
    this.#refresh()
  }

  #applyDeafen() {
    for (const el of this.#audio.values()) el.muted = this.deafened
    if (this.#ctx) (this.deafened ? this.#ctx.suspend() : this.#ctx.resume()).catch(() => {})
  }

  #subscribed(track, pub, p) {
    if (track.kind === Track.Kind.Audio && pub.source === Track.Source.Microphone) this.#meter(track.sid, p.identity, track.mediaStreamTrack)
    if (track.kind === Track.Kind.Audio) {
      if (this.#ctx && track.setWebAudioPlugins) {
        // A limiter: leaves normal speech alone, holds peaks below clipping.
        const lim = this.#ctx.createDynamicsCompressor()
        lim.threshold.value = -3
        lim.knee.value = 0
        lim.ratio.value = 20
        lim.attack.value = 0.002
        lim.release.value = 0.15
        track.setWebAudioPlugins([lim])
      }
      const el = track.attach()
      el.muted = this.deafened
      this.#container().appendChild(el)
      this.#audio.set(track.sid, el)
      this.hearing = this.#audio.size
      p.setVolume(this.volume(p.identity))
    }
    this.#refresh()
  }

  #unsubscribed(track) {
    this.#unmeter(track.sid)
    const el = this.#audio.get(track.sid)
    if (el) {
      track.detach(el)
      el.remove()
      this.#audio.delete(track.sid)
      this.hearing = this.#audio.size
    }
    this.#refresh()
  }

  // The green light: LiveKit's own detection runs on the server, from levels
  // the apps report, about twice a second and smoothed, so the light lagged,
  // dropped between words and missed quiet speech (Dev Notes). Instead each
  // voice (ours too) is measured here, 20 times a second: lit above -50 dB
  // and for 300 ms after, so it doesn't flicker between words. Only
  // microphones count, not a share's sound; a muted mic is silent.
  #meter(sid, id, mst) {
    if (!mst) return
    this.#unmeter(sid)
    try {
      if (!this.#meterCtx) this.#meterCtx = new AudioContext()
      const src = this.#meterCtx.createMediaStreamSource(new MediaStream([mst]))
      const an = this.#meterCtx.createAnalyser()
      an.fftSize = 1024
      src.connect(an)
      this.#meters.set(sid, { id, an, src, mst, buf: new Float32Array(an.fftSize), last: 0 })
    } catch (err) {
      console.error(`speaking light: ${err.message || err}`)
      return
    }
    if (!this.#meterTimer) this.#meterTimer = setInterval(() => this.#measure(), 50)
  }

  // #meterMic measures our own microphone's current track, if any.
  #meterMic() {
    const lp = this.#room?.localParticipant
    const track = lp?.getTrackPublication(Track.Source.Microphone)?.track
    const t = track?.getProcessor()?.processedTrack || track?.mediaStreamTrack // what others hear
    const m = this.#meters.get('mic')
    if (m?.mst === t) return
    if (t) this.#meter('mic', lp.identity, t)
    else this.#unmeter('mic')
  }

  #unmeter(sid) {
    const m = this.#meters.get(sid)
    if (!m) return
    m.src.disconnect()
    this.#meters.delete(sid)
  }

  #measure() {
    const now = performance.now()
    const lit = new Set()
    for (const m of this.#meters.values()) {
      m.an.getFloatTimeDomainData(m.buf)
      let sum = 0
      for (const v of m.buf) sum += v * v
      if (Math.sqrt(sum / m.buf.length) > 0.00316) m.last = now // -50 dBFS
      if (now - m.last < 300) lit.add(m.id)
    }
    if (lit.size === this.#talking.size && [...lit].every((id) => this.#talking.has(id))) return
    this.#talking = lit
    this.people = this.people.map((p) => ({ ...p, speaking: lit.has(p.id) }))
  }

  #container() {
    if (!this.#box) {
      this.#box = document.createElement('div')
      this.#box.style.display = 'none'
      document.body.appendChild(this.#box)
    }
    return this.#box
  }

  // #refresh copies the room's state into what the window shows.
  #refresh() {
    const room = this.#room
    if (!room) return
    const all = [room.localParticipant, ...room.remoteParticipants.values()]
    this.people = all.map((p) => ({
      id: p.identity,
      name: p.name || p.identity,
      speaking: this.#meters.size ? this.#talking.has(p.identity) : p.isSpeaking,
      muted: !p.isMicrophoneEnabled,
      camera: p.isCameraEnabled,
      sharing: p.isScreenShareEnabled,
      local: p === room.localParticipant,
    }))
    const tiles = []
    const shares = []
    let stage = null
    for (const p of all) {
      const local = p === room.localParticipant
      const name = p.name || p.identity
      const pub = p.getTrackPublication(Track.Source.Camera)
      if (pub?.track && !pub.isMuted) tiles.push({ id: p.identity, name, track: pub.track, local })
      const sh = p.getTrackPublication(Track.Source.ScreenShare)
      if (sh) {
        shares.push({ id: p.identity, name, local, game: sh.trackName === 'game' })
        if (!local && this.watching === p.identity && sh.track) stage = { id: p.identity, name, track: sh.track, game: sh.trackName === 'game' }
      }
    }
    if (this.watching && !shares.some((s) => s.id === this.watching)) this.watching = '' // they stopped
    this.tiles = tiles
    this.shares = shares
    this.stage = stage
    const mine = room.localParticipant.getTrackPublication(Track.Source.ScreenShare)
    this.sharing = !!mine
    this.myShare = mine?.track ? { track: mine.track } : null
  }

  #reset() {
    clearInterval(this.#meterTimer)
    this.#meterTimer = 0
    for (const sid of [...this.#meters.keys()]) this.#unmeter(sid)
    this.#meterCtx?.close().catch(() => {})
    this.#meterCtx = null
    this.#talking = new Set()
    for (const el of this.#audio.values()) el.remove()
    this.#audio.clear()
    this.#ctx?.close().catch(() => {})
    this.#ctx = null
    this.hearing = 0
    this.#room = null
    this.conn = null
    this.roomId = 0
    this.state = ''
    this.camera = false
    this.deafened = false
    this.people = []
    this.tiles = []
    this.shares = []
    this.watching = ''
    this.stage = null
    this.myShare = null
    this.sharing = false
    this.game = false
    for (const t of this.#gameTracks) t.stop()
    this.#gameTracks = []
    this.#gameDeclined = false
  }
}

export const voice = new Voice()

// attach is a Svelte action that shows a video track in an element.
export function attach(node, track) {
  let el = track.attach()
  node.appendChild(el)
  return {
    update(t) {
      // The video list is rebuilt on every change in the call (someone
      // speaking, muting...): re-attaching the same video made it blink.
      if (t === track) return
      track.detach(el)
      el.remove()
      track = t
      el = track.attach()
      node.appendChild(el)
    },
    destroy() {
      track.detach(el)
      el.remove()
    },
  }
}
