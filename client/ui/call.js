let signalDC = null;
let peerName = "";
let state = "idle";
let onCallAccepted = null;
let onCallEnded = null;

let ringtoneCtx = null;
let ringtoneGain = null;
let ringtoneOsc1 = null;
let ringtoneOsc2 = null;
let ringtoneTimer = null;

let incomingOverlay = null;
let callingIndicator = null;

export function getCallState() { return state; }

export function setPeerName(name) {
  if (name) peerName = name;
}

export function resetCallState() {
  stopRingtone();
  removeOverlay();
  removeIndicator();
  state = "idle";
}

export function initCall(signal, name, callbacks) {
  signalDC = signal;
  peerName = name;
  if (callbacks.onCallAccepted) onCallAccepted = callbacks.onCallAccepted;
  if (callbacks.onCallEnded) onCallEnded = callbacks.onCallEnded;
  signalDC.addEventListener("message", (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    if (["call-request","call-accepted","call-declined","call-canceled","call-ended"].includes(msg.type)) {
      handleCallSignal(msg);
    }
  });
}

export function requestCall(kind) {
  if (state !== "idle") return;
  state = "calling";
  signalDC?.send(JSON.stringify({ type: "call-request", kind }));
  showCallingUI(kind);
  startRingback();
}

export function cancelCall() {
  if (state !== "calling") return;
  signalDC?.send(JSON.stringify({ type: "call-canceled" }));
  resetCallState();
}

export function endCall(kind) {
  if (state !== "connected") return;
  signalDC?.send(JSON.stringify({ type: "call-ended", kind }));
  onCallEnded?.(kind);
  resetCallState();
}

export function acceptCall() {
  if (state !== "ringing") return;
  state = "connected";
  signalDC?.send(JSON.stringify({ type: "call-accepted" }));
  stopRingtone();
  removeOverlay();
  onCallAccepted?.();
}

export function declineCall() {
  if (state !== "ringing") return;
  signalDC?.send(JSON.stringify({ type: "call-declined" }));
  stopRingtone();
  removeOverlay();
  state = "idle";
}

// ── Signal handlers ──

export function handleCallSignal(msg) {
  switch (msg.type) {
    case "call-request":
      if (state !== "idle") {
        signalDC?.send(JSON.stringify({ type: "call-declined" }));
        return;
      }
      state = "ringing";
      showIncomingCall(msg.kind);
      startRingtone();
      break;

    case "call-accepted":
      if (state !== "calling") return;
      state = "connected";
      stopRingtone();
      removeIndicator();
      onCallAccepted?.();
      break;

    case "call-declined":
      if (state !== "calling") return;
      stopRingtone();
      removeIndicator();
      showToast("Call declined");
      state = "idle";
      break;

    case "call-canceled":
      if (state !== "ringing") return;
      stopRingtone();
      removeOverlay();
      state = "idle";
      break;

    case "call-ended":
      if (state !== "connected") return;
      stopRingtone();
      removeOverlay();
      removeIndicator();
      showToast("Call ended");
      onCallEnded?.();
      state = "idle";
      break;
  }
}

// ── Ringtone (Web Audio API) ──

function startRingback() {
  try {
    ringtoneCtx = new (window.AudioContext || window.webkitAudioContext)();
    ringtoneGain = ringtoneCtx.createGain();
    ringtoneGain.gain.value = 0.3;
    ringtoneGain.connect(ringtoneCtx.destination);
    scheduleRingPattern(440, 2000, 4000);
  } catch {}
}

function startRingtone() {
  try {
    ringtoneCtx = new (window.AudioContext || window.webkitAudioContext)();
    ringtoneGain = ringtoneCtx.createGain();
    ringtoneGain.gain.value = 0.4;
    ringtoneGain.connect(ringtoneCtx.destination);
    scheduleRingPattern([440, 480], 500, 500);
  } catch {}
}

function scheduleRingPattern(freqs, onMs, offMs) {
  if (!ringtoneCtx || !ringtoneGain) return;
  const now = ringtoneCtx.currentTime;

  ringtoneOsc1 = ringtoneCtx.createOscillator();
  ringtoneOsc1.type = "sine";
  ringtoneOsc1.frequency.value = Array.isArray(freqs) ? freqs[0] : freqs;
  ringtoneOsc1.connect(ringtoneGain);
  ringtoneOsc1.start(now);
  ringtoneOsc1.stop(now + 30);

  if (Array.isArray(freqs) && freqs.length > 1) {
    ringtoneOsc2 = ringtoneCtx.createOscillator();
    ringtoneOsc2.type = "sine";
    ringtoneOsc2.frequency.value = freqs[1];
    ringtoneOsc2.connect(ringtoneGain);
    ringtoneOsc2.start(now);
    ringtoneOsc2.stop(now + 30);
  }

  let on = true;
  ringtoneTimer = setInterval(() => {
    if (!ringtoneGain) return;
    const t = ringtoneCtx.currentTime;
    ringtoneGain.gain.setTargetAtTime(on ? 0 : (Array.isArray(freqs) ? 0.4 : 0.3), t, 0.01);
    on = !on;
  }, on ? onMs : offMs);

  // First half-cycle: start with sound
  ringtoneGain.gain.setValueAtTime(Array.isArray(freqs) ? 0.4 : 0.3, now);
  // Mute after onMs
  const muteTime = now + onMs / 1000;
  ringtoneGain.gain.setValueAtTime(0, muteTime);
}

function stopRingtone() {
  if (ringtoneTimer) { clearInterval(ringtoneTimer); ringtoneTimer = null; }
  try { ringtoneOsc1?.stop(); } catch {}
  try { ringtoneOsc2?.stop(); } catch {}
  try { ringtoneCtx?.close(); } catch {}
  ringtoneOsc1 = null;
  ringtoneOsc2 = null;
  ringtoneGain = null;
  ringtoneCtx = null;
}

// ── Incoming call overlay ──

function showIncomingCall(kind) {
  removeOverlay();
  const label = kind === "video" ? "Video" : "Voice";
  incomingOverlay = document.createElement("div");
  incomingOverlay.className = "call-overlay";
  incomingOverlay.innerHTML = `
    <div class="call-overlay-box">
      <p class="call-overlay-label">Incoming ${label} Call</p>
      <p class="call-overlay-peer">${esc(peerName)}</p>
      <div class="call-overlay-buttons">
        <button class="call-btn call-btn-decline">Decline</button>
        <button class="call-btn call-btn-accept">Accept</button>
      </div>
    </div>
  `;
  incomingOverlay.querySelector(".call-btn-decline").addEventListener("click", declineCall);
  incomingOverlay.querySelector(".call-btn-accept").addEventListener("click", acceptCall);
  document.body.appendChild(incomingOverlay);
}

function removeOverlay() {
  if (incomingOverlay) { incomingOverlay.remove(); incomingOverlay = null; }
}

// ── Calling indicator ──

function showCallingUI(kind) {
  removeIndicator();
  const label = kind === "video" ? "Video" : "Voice";
  callingIndicator = document.createElement("div");
  callingIndicator.className = "calling-indicator";
  callingIndicator.innerHTML = `
    <span class="calling-label">${label} Call — Ringing...</span>
    <button class="call-btn call-btn-cancel">Cancel</button>
  `;
  callingIndicator.querySelector(".call-btn-cancel").addEventListener("click", cancelCall);
  document.body.appendChild(callingIndicator);
}

function removeIndicator() {
  if (callingIndicator) { callingIndicator.remove(); callingIndicator = null; }
}

// ── Toast ──

function showToast(text) {
  const toast = document.createElement("div");
  toast.className = "call-toast";
  toast.textContent = text;
  document.body.appendChild(toast);
  setTimeout(() => toast.remove(), 3000);
}

function esc(s) {
  const d = document.createElement("div");
  d.textContent = s;
  return d.innerHTML;
}
