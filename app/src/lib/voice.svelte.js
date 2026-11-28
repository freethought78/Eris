// Voice and cameras in a room (DESIGN.md 4.4), with LiveKit: the server gives
// a token for the room's voice (server/voice.go), and LiveKit's signalling
// goes through the server's own port, encrypted and pinned like everything
// else. One room's voice at a time, across all servers.
//
// Reading a room and being in its voice are separate: joining is deliberate
// (double-click the room, or Join voice).
import { Room, RoomEvent, Track, VideoPresets } from 'livekit-client'
import { httpBase } from './conn.svelte.js'

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
  error = $state('')
  hearing = $state(0) // others' audio tracks being played
  volumes = $state(store.get('volumes', {})) // member id -> 0..1
  devices = $state(store.get('devices', {})) // audioinput, audiooutput, videoinput -> device id

  #room = null
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
    const room = new Room({
      adaptiveStream: true,
      dynacast: true,
      audioCaptureDefaults: { echoCancellation: true, noiseSuppression: true, autoGainControl: true, deviceId: this.devices.audioinput },
      videoCaptureDefaults: { resolution: VideoPresets.h540.resolution, deviceId: this.devices.videoinput },
      audioOutput: { deviceId: this.devices.audiooutput },
    })
    this.#room = room
    room
      .on(RoomEvent.TrackSubscribed, (track, pub, p) => this.#subscribed(track, pub, p))
      .on(RoomEvent.TrackUnsubscribed, (track) => this.#unsubscribed(track))
      .on(RoomEvent.ParticipantConnected, () => this.#refresh())
      .on(RoomEvent.ParticipantDisconnected, () => this.#refresh())
      .on(RoomEvent.ActiveSpeakersChanged, () => this.#refresh())
      .on(RoomEvent.TrackMuted, () => this.#refresh())
      .on(RoomEvent.TrackUnmuted, () => this.#refresh())
      .on(RoomEvent.LocalTrackPublished, () => this.#refresh())
      .on(RoomEvent.LocalTrackUnpublished, () => this.#refresh())
      .on(RoomEvent.Reconnecting, () => (this.state = 'reconnecting'))
      .on(RoomEvent.Reconnected, () => ((this.state = 'connected'), this.#refresh()))
      .on(RoomEvent.Disconnected, () => {
        if (this.#room === room) {
          this.conn?.send({ type: 'voice', room_id: 0 })
          this.#reset()
        }
      })
    try {
      await room.connect(httpBase(conn.entry.address).replace(/^https/, 'wss'), r.json.token)
    } catch (err) {
      this.error = `Couldn't connect to the voice: ${err.message || err}`
      this.#reset()
      return
    }
    if (this.#room !== room) return // left while connecting
    this.state = 'connected'
    conn.send({ type: 'voice', room_id: roomId })
    console.info(`voice: joined room ${roomId} on ${conn.entry.address}`)
    try {
      await room.localParticipant.setMicrophoneEnabled(!this.muted && !this.deafened)
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

  setVolume(id, v) {
    this.volumes[id] = v
    store.set('volumes', $state.snapshot(this.volumes))
    const p = this.#room?.remoteParticipants.get(String(id))
    p?.setVolume(v)
  }

  volume(id) {
    return this.volumes[id] ?? 1
  }

  async listDevices() {
    const out = {}
    for (const kind of ['audioinput', 'audiooutput', 'videoinput']) {
      try {
        out[kind] = await Room.getLocalDevices(kind, true)
      } catch {
        out[kind] = []
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

  async #applyMic() {
    try {
      await this.#room?.localParticipant.setMicrophoneEnabled(!this.muted && !this.deafened)
    } catch (err) {
      this.error = `Your microphone couldn't be used (${err.message || err}).`
    }
    this.#refresh()
  }

  #applyDeafen() {
    for (const el of this.#audio.values()) el.muted = this.deafened
  }

  #subscribed(track, pub, p) {
    if (track.kind === Track.Kind.Audio) {
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
    const el = this.#audio.get(track.sid)
    if (el) {
      track.detach(el)
      el.remove()
      this.#audio.delete(track.sid)
      this.hearing = this.#audio.size
    }
    this.#refresh()
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
      speaking: p.isSpeaking,
      muted: !p.isMicrophoneEnabled,
      camera: p.isCameraEnabled,
      local: p === room.localParticipant,
    }))
    const tiles = []
    for (const p of all) {
      const pub = p.getTrackPublication(Track.Source.Camera)
      if (pub?.track && !pub.isMuted) tiles.push({ id: p.identity, name: p.name || p.identity, track: pub.track, local: p === room.localParticipant })
    }
    this.tiles = tiles
  }

  #reset() {
    for (const el of this.#audio.values()) el.remove()
    this.#audio.clear()
    this.hearing = 0
    this.#room = null
    this.conn = null
    this.roomId = 0
    this.state = ''
    this.camera = false
    this.deafened = false
    this.people = []
    this.tiles = []
  }
}

export const voice = new Voice()

// attach is a Svelte action that shows a video track in an element.
export function attach(node, track) {
  let el = track.attach()
  node.appendChild(el)
  return {
    update(t) {
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
